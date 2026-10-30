// Package helpers runs Dadi's browser and terminal helpers: one model loop per task, on
// Dwar's /chat/complete, operating one browser or one shell for whichever agent steers it.
// A helper is not an agent. Its charter and tools come from its kind (see the browser and
// terminal packages); everything about the job comes in the steer.
package helpers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// forgetAfter is how long a task may sit idle before its history is dropped and its id
	// retired (the 24 hours Ankur agreed, 2026-10-29).
	forgetAfter = 24 * time.Hour
	// sweepInterval is how often tasks are checked for sleep and forgetting.
	sweepInterval = 30 * time.Second
	// resultsMaxChars and keptResultChars bound a helper's history: once its tool results
	// pass resultsMaxChars, all but the newest keptResultChars of them read as a short
	// cleared receipt.
	resultsMaxChars = 120_000
	keptResultChars = 50_000
	// textTurnsMax is how many turns in a row may pass without a tool call before the run
	// stops as failed.
	textTurnsMax = 3
	// purposeChars is how much of a task's purpose its view shows.
	purposeChars = 120
	toolFinish   = "finish"
	toolAsk      = "ask"
	textTurn     = "[continue] Text you write reaches no one. Take the next step with a tool, call ask for something only the agent or Ankur can give, or call finish with the result."
)

// Status is where a task stands: its helper is working, idle after a finish, waiting on an
// answer it asked for, or asleep with its resource stopped and its history kept.
type Status string

const (
	StatusWorking Status = "working"
	StatusIdle    Status = "idle"
	StatusAsking  Status = "asking"
	StatusAsleep  Status = "asleep"
)

// Kit is one kind of helper: its charter, its tools, and how it wakes, sleeps and acts on
// its resource. A tool's error is shown to the model as the tool's result.
type Kit interface {
	Kind() string
	Charter() string
	Tools() []ToolSpec
	// Wake starts the task's resource (a browser on the agent's folder with its tabs
	// reopened, a shell in its directory).
	Wake(t *Task) error
	// Sleep stops the task's resource and frees its memory, keeping what Wake restores.
	Sleep(t *Task) error
	Call(t *Task, name string, input json.RawMessage) (string, error)
	// State is the short state of the work: tabs and their pages, or directory and files touched.
	State(t *Task) string
	// IdleSleep is how long a task of this kind may sit idle before it sleeps.
	IdleSleep() time.Duration
	// Room reports whether there is memory to wake one more task of this kind.
	Room() (bool, error)
}

// Task is one helper run, addressed task/<kind>/<id>. Its fields persist across sleep and
// Nas restarts; a kit keeps what it needs to wake in Resource, Folder, Cwd and its notes
// (Note, Notes), which are safe to change while the task is being listed.
type Task struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Owner string `json:"owner"`
	// SteeredBy is the agent whose steer the helper is working on; its result goes there.
	SteeredBy string `json:"steered_by"`
	// Purpose is the whole first steer; views clip it.
	Purpose      string            `json:"purpose"`
	Status       Status            `json:"status"`
	Resource     string            `json:"resource,omitempty"`
	Folder       string            `json:"folder,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	Memo         map[string]string `json:"memo,omitempty"`
	History      []Message         `json:"history"`
	ResultSizes  map[string]int    `json:"result_sizes"`
	ResultOrder  []string          `json:"result_order"`
	Cleared      map[string]bool   `json:"cleared"`
	Pending      []Steer           `json:"pending,omitempty"`
	LastReport   *Report           `json:"last_report,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	LastActivity time.Time         `json:"last_activity"`
	memoMu       sync.Mutex
}

// Note sets a kit's note key to value; an empty value removes it.
func (t *Task) Note(key, value string) {
	t.memoMu.Lock()
	defer t.memoMu.Unlock()
	if value == "" {
		delete(t.Memo, key)
		return
	}
	t.Memo[key] = value
}

// Notes is a copy of the kit's notes.
func (t *Task) Notes() map[string]string {
	t.memoMu.Lock()
	defer t.memoMu.Unlock()
	out := make(map[string]string, len(t.Memo))
	for k, v := range t.Memo {
		out[k] = v
	}
	return out
}

// Steer is one message from an agent to a helper.
type Steer struct {
	From    string `json:"from"`
	Message string `json:"message"`
}

// Report is what a helper hands back: a result (done), a question (needs_input), or the
// error that stopped it (failed). Seq orders reports across all tasks since Nas started.
type Report struct {
	Seq     int64     `json:"seq"`
	Task    string    `json:"task"`
	Kind    string    `json:"kind"`
	ID      string    `json:"id"`
	AgentID string    `json:"agent_id"`
	State   string    `json:"state"`
	Text    string    `json:"text"`
	Files   []string  `json:"files"`
	At      time.Time `json:"at"`
}

// View is a task as GET /tasks shows it.
type View struct {
	Task         string    `json:"task"`
	Kind         string    `json:"kind"`
	ID           string    `json:"id"`
	Owner        string    `json:"owner"`
	SteeredBy    string    `json:"steered_by"`
	Purpose      string    `json:"purpose"`
	Status       Status    `json:"status"`
	Resource     string    `json:"resource,omitempty"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"created_at"`
	LastActivity time.Time `json:"last_activity"`
	LastReport   *Report   `json:"last_report,omitempty"`
}

// Runner owns every task: it starts and steers helpers, runs their loops, sleeps idle ones,
// wakes them on a steer, and forgets them after forgetAfter.
type Runner struct {
	dir     string
	dwar    *dwarClient
	kits    map[string]Kit
	publish func(Report)
	mu      sync.Mutex
	tasks   map[string]*Task
	seq     int64
}

// Error is a request failure with its HTTP status and stable code.
type Error struct {
	Status  int
	Code    string
	Message string
}

// Error returns the message.
func (e Error) Error() string {
	return e.Message
}

// NewRunner loads the tasks saved under dir as asleep and drops those past forgetAfter. A
// loaded task keeps the Resource it last had, which its kit's Wake clears up first. publish receives every report as it is made.
func NewRunner(dir, dwarURL string, kits []Kit, publish func(Report)) (*Runner, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("helpers dir: %w", err)
	}
	r := &Runner{dir: dir, dwar: newDwarClient(dwarURL), kits: map[string]Kit{}, publish: publish, tasks: map[string]*Task{}}
	for _, kit := range kits {
		r.kits[kit.Kind()] = kit
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read helpers dir: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read task %s: %w", entry.Name(), err)
		}
		var t Task
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("parse task %s: %w", entry.Name(), err)
		}
		if _, ok := r.kits[t.Kind]; !ok {
			return nil, fmt.Errorf("task %s has unknown kind %q", entry.Name(), t.Kind)
		}
		if time.Since(t.LastActivity) > forgetAfter {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return nil, fmt.Errorf("forget task %s: %w", entry.Name(), err)
			}
			continue
		}
		t.Status = StatusAsleep
		if t.Memo == nil {
			t.Memo = map[string]string{}
		}
		r.tasks[t.Kind+"/"+t.ID] = &t
	}
	return r, nil
}

// Register mounts the /tasks routes on mux.
func (r *Runner) Register(mux *http.ServeMux, writeError func(http.ResponseWriter, *http.Request, int, string, string), writeJSON func(http.ResponseWriter, int, any)) {
	fail := func(w http.ResponseWriter, req *http.Request, err error) {
		var e Error
		if errors.As(err, &e) {
			writeError(w, req, e.Status, e.Code, e.Message)
			return
		}
		writeError(w, req, http.StatusInternalServerError, "internal_error", err.Error())
	}
	type steerBody struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
		Cwd     string `json:"cwd"`
	}
	read := func(w http.ResponseWriter, req *http.Request) (steerBody, bool) {
		var body steerBody
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.AgentID == "" || strings.TrimSpace(body.Message) == "" {
			writeError(w, req, http.StatusBadRequest, "invalid_request", "body must be {agent_id, message}")
			return body, false
		}
		return body, true
	}
	mux.HandleFunc("POST /tasks/{kind}", func(w http.ResponseWriter, req *http.Request) {
		body, ok := read(w, req)
		if !ok {
			return
		}
		view, err := r.Start(req.PathValue("kind"), body.AgentID, body.Message, body.Cwd)
		if err != nil {
			fail(w, req, err)
			return
		}
		writeJSON(w, http.StatusCreated, view)
	})
	mux.HandleFunc("POST /tasks/{kind}/{id}/steer", func(w http.ResponseWriter, req *http.Request) {
		body, ok := read(w, req)
		if !ok {
			return
		}
		view, err := r.Steer(req.PathValue("kind"), req.PathValue("id"), body.AgentID, body.Message)
		if err != nil {
			fail(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, r.List())
	})
	mux.HandleFunc("GET /tasks/{kind}/{id}", func(w http.ResponseWriter, req *http.Request) {
		view, err := r.Get(req.PathValue("kind"), req.PathValue("id"))
		if err != nil {
			fail(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
}

// newID returns a random 8-hex-digit task id.
func newID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// clip shortens s to max characters with an ellipsis.
func clip(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// kit returns the kit for kind, or a not_found Error.
func (r *Runner) kit(kind string) (Kit, error) {
	kit, ok := r.kits[kind]
	if !ok {
		return nil, Error{http.StatusNotFound, "not_found", fmt.Sprintf("no helper kind %q", kind)}
	}
	return kit, nil
}

// Start creates a task of kind owned by agent, wakes its resource (in cwd for a terminal,
// when given) and starts its helper on message.
func (r *Runner) Start(kind, agent, message, cwd string) (View, error) {
	kit, err := r.kit(kind)
	if err != nil {
		return View{}, err
	}
	id, err := newID()
	if err != nil {
		return View{}, err
	}
	now := time.Now().UTC()
	t := &Task{
		ID: id, Kind: kind, Owner: agent, SteeredBy: agent, Purpose: strings.TrimSpace(message),
		Status: StatusAsleep, Cwd: cwd, Memo: map[string]string{}, ResultSizes: map[string]int{}, Cleared: map[string]bool{},
		CreatedAt: now, LastActivity: now,
	}
	r.mu.Lock()
	r.tasks[kind+"/"+id] = t
	r.mu.Unlock()
	if err := r.wake(kit, t); err != nil {
		r.mu.Lock()
		delete(r.tasks, kind+"/"+id)
		r.mu.Unlock()
		return View{}, err
	}
	return r.steer(kit, t, agent, message)
}

// Steer gives task kind/id a message from agent: joined to its stream between steps while it
// works, or starting its next stretch of work, after waking it when it sleeps.
func (r *Runner) Steer(kind, id, agent, message string) (View, error) {
	kit, err := r.kit(kind)
	if err != nil {
		return View{}, err
	}
	r.mu.Lock()
	t, ok := r.tasks[kind+"/"+id]
	r.mu.Unlock()
	if !ok {
		return View{}, Error{http.StatusNotFound, "not_found", fmt.Sprintf("task/%s/%s does not exist or was forgotten after 24 idle hours", kind, id)}
	}
	r.mu.Lock()
	asleep := t.Status == StatusAsleep
	r.mu.Unlock()
	if asleep {
		if err := r.wake(kit, t); err != nil {
			return View{}, err
		}
	}
	return r.steer(kit, t, agent, message)
}

// steer queues message for a working helper, or appends it and starts the loop.
func (r *Runner) steer(kit Kit, t *Task, agent, message string) (View, error) {
	r.mu.Lock()
	t.SteeredBy = agent
	t.LastActivity = time.Now().UTC()
	if t.Status == StatusWorking {
		t.Pending = append(t.Pending, Steer{From: agent, Message: message})
		r.mu.Unlock()
		return r.view(kit, t), r.save(t)
	}
	text := Block{"type": "text", "text": fmt.Sprintf("[Steer from %s]\n%s\n\n[State of the work]\n%s", agent, message, kit.State(t))}
	if n := len(t.History); n > 0 && t.History[n-1].Role == "user" {
		t.History[n-1].Content = append(t.History[n-1].Content, text)
	} else {
		t.History = append(t.History, Message{Role: "user", Content: []Block{text}})
	}
	t.Status = StatusWorking
	r.mu.Unlock()
	if err := r.save(t); err != nil {
		return View{}, err
	}
	go r.run(kit, t)
	return r.view(kit, t), nil
}

// wake starts t's resource, first putting the longest-idle awake task of its kind to sleep
// while memory is short. With nothing left to put to sleep it fails busy.
func (r *Runner) wake(kit Kit, t *Task) error {
	for {
		room, err := kit.Room()
		if err != nil {
			return err
		}
		if room {
			break
		}
		victim := r.longestIdle(t.Kind)
		if victim == nil {
			return Error{http.StatusConflict, "busy", fmt.Sprintf("not enough memory to wake another %s, and every awake one is working", t.Kind)}
		}
		if err := r.sleep(kit, victim); err != nil {
			return err
		}
	}
	if err := kit.Wake(t); err != nil {
		return fmt.Errorf("wake task/%s/%s: %w", t.Kind, t.ID, err)
	}
	r.mu.Lock()
	t.Status = StatusIdle
	r.mu.Unlock()
	return nil
}

// longestIdle is the awake, not working task of kind that has been idle longest, or nil.
func (r *Runner) longestIdle(kind string) *Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	var oldest *Task
	for _, t := range r.tasks {
		if t.Kind != kind || (t.Status != StatusIdle && t.Status != StatusAsking) {
			continue
		}
		if oldest == nil || t.LastActivity.Before(oldest.LastActivity) {
			oldest = t
		}
	}
	return oldest
}

// sleep stops t's resource and marks it asleep, keeping its history.
func (r *Runner) sleep(kit Kit, t *Task) error {
	if err := kit.Sleep(t); err != nil {
		return fmt.Errorf("sleep task/%s/%s: %w", t.Kind, t.ID, err)
	}
	r.mu.Lock()
	t.Status = StatusAsleep
	r.mu.Unlock()
	return r.save(t)
}

// Sweep puts tasks idle past their kind's limit to sleep and forgets tasks idle past
// forgetAfter. Failures are logged; the next sweep tries again.
func (r *Runner) Sweep(now time.Time) {
	r.mu.Lock()
	tasks := make([]*Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		tasks = append(tasks, t)
	}
	r.mu.Unlock()
	for _, t := range tasks {
		kit := r.kits[t.Kind]
		r.mu.Lock()
		status, idle := t.Status, now.Sub(t.LastActivity)
		r.mu.Unlock()
		switch {
		case idle > forgetAfter && status != StatusWorking:
			if status != StatusAsleep {
				if err := r.sleep(kit, t); err != nil {
					slog.Error("helper sleep failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
					continue
				}
			}
			r.mu.Lock()
			delete(r.tasks, t.Kind+"/"+t.ID)
			r.mu.Unlock()
			if err := os.Remove(r.path(t)); err != nil {
				slog.Error("helper forget failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
			}
		case (status == StatusIdle || status == StatusAsking) && idle > kit.IdleSleep():
			if err := r.sleep(kit, t); err != nil {
				slog.Error("helper sleep failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
			}
		}
	}
}

// RunSweeper sweeps every sweepInterval until stop is closed.
func (r *Runner) RunSweeper(stop <-chan struct{}) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			r.Sweep(now)
		}
	}
}

// List is every task, most recently active first.
func (r *Runner) List() []View {
	r.mu.Lock()
	tasks := make([]*Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		tasks = append(tasks, t)
	}
	r.mu.Unlock()
	out := make([]View, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, r.view(r.kits[t.Kind], t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity.After(out[j].LastActivity) })
	return out
}

// Get is one task, or not_found.
func (r *Runner) Get(kind, id string) (View, error) {
	kit, err := r.kit(kind)
	if err != nil {
		return View{}, err
	}
	r.mu.Lock()
	t, ok := r.tasks[kind+"/"+id]
	r.mu.Unlock()
	if !ok {
		return View{}, Error{http.StatusNotFound, "not_found", fmt.Sprintf("task/%s/%s does not exist", kind, id)}
	}
	return r.view(kit, t), nil
}

// view is t as GET /tasks shows it.
func (r *Runner) view(kit Kit, t *Task) View {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := ""
	if t.Status != StatusAsleep {
		state = kit.State(t)
	}
	return View{
		Task: "task/" + t.Kind + "/" + t.ID, Kind: t.Kind, ID: t.ID, Owner: t.Owner, SteeredBy: t.SteeredBy,
		Purpose: clip(t.Purpose, purposeChars), Status: t.Status, Resource: t.Resource, State: state,
		CreatedAt: t.CreatedAt, LastActivity: t.LastActivity, LastReport: t.LastReport,
	}
}

// path is where t is saved.
func (r *Runner) path(t *Task) string {
	return filepath.Join(r.dir, t.Kind+"-"+t.ID+".json")
}

// save writes t through a temp file and rename.
func (r *Runner) save(t *Task) error {
	r.mu.Lock()
	t.memoMu.Lock()
	raw, err := json.Marshal(t)
	t.memoMu.Unlock()
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("encode task: %w", err)
	}
	tmp, err := os.CreateTemp(r.dir, "task.tmp.*")
	if err != nil {
		return fmt.Errorf("create task temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write task temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close task temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), r.path(t)); err != nil {
		return fmt.Errorf("write task: %w", err)
	}
	return nil
}

var (
	finishSpec = ToolSpec{
		Name:        toolFinish,
		Description: "Report the result of the work you were steered to do: a few plain lines with exact figures, names, dates and paths, or what stopped you, what you tried and what would get past it. The agent reads only this. files are absolute paths of files to hand back. Your resource stays as it is for the next steer.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"result"},
			"properties": map[string]any{
				"result": map[string]any{"type": "string", "description": "The result, as the agent should read it"},
				"files":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute paths of files to hand back"},
			},
		},
	}
	askSpec = ToolSpec{
		Name:        toolAsk,
		Description: "Stop and ask the agent for something only it or Ankur can give: a one-time code, a choice, a missing fact. Say exactly what you need and where it was sent. Your resource stays as it is, and the answer arrives as the next steer.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"question"},
			"properties": map[string]any{"question": map[string]any{"type": "string", "description": "Exactly what you need, in one or two lines"}},
		},
	}
)

// messages is t's history as the next call sends it: once results pass resultsMaxChars, all
// but the newest keptResultChars of them read as a cleared receipt. Clearing only moves
// forward. Callers hold r.mu.
func (r *Runner) messages(t *Task) []Message {
	total := 0
	for _, id := range t.ResultOrder {
		if !t.Cleared[id] {
			total += t.ResultSizes[id]
		}
	}
	if total > resultsMaxChars {
		kept := 0
		for i := len(t.ResultOrder) - 1; i >= 0; i-- {
			id := t.ResultOrder[i]
			if t.Cleared[id] {
				continue
			}
			if kept+t.ResultSizes[id] > keptResultChars {
				t.Cleared[id] = true
			} else {
				kept += t.ResultSizes[id]
			}
		}
	}
	out := make([]Message, len(t.History))
	for i, m := range t.History {
		content := make([]Block, len(m.Content))
		for j, b := range m.Content {
			if b["type"] == "tool_result" {
				if id, _ := b["tool_use_id"].(string); t.Cleared[id] {
					text, _ := b["content"].(string)
					content[j] = Block{"type": "tool_result", "tool_use_id": id, "content": "[cleared] " + clip(text, 160), "is_error": b["is_error"]}
					continue
				}
			}
			content[j] = b
		}
		out[i] = Message{Role: m.Role, Content: content}
	}
	return out
}

// run is a helper's loop: call the model, run its tools, fold in steers that arrived, until
// it finishes or asks. A failure stops the loop and is reported.
func (r *Runner) run(kit Kit, t *Task) {
	textTurns := 0
	for {
		r.mu.Lock()
		req := chatRequest{System: kit.Charter(), Messages: r.messages(t), Tools: append(kit.Tools(), askSpec, finishSpec), ToolChoice: "auto"}
		caller := "nas/helper/" + t.Kind + "/" + t.SteeredBy
		r.mu.Unlock()
		resp, err := r.dwar.complete(caller, req)
		if err != nil {
			r.finish(t, "failed", err.Error(), nil)
			return
		}
		r.mu.Lock()
		t.History = append(t.History, Message{Role: "assistant", Content: resp.Content})
		r.mu.Unlock()
		var results []Block
		var end Block
		uses := 0
		for _, b := range resp.Content {
			if b["type"] != "tool_use" {
				continue
			}
			uses++
			id, _ := b["id"].(string)
			name, _ := b["name"].(string)
			if name == toolFinish || name == toolAsk {
				if end == nil {
					end = b
				}
				results = append(results, Block{"type": "tool_result", "tool_use_id": id, "content": "delivered", "is_error": false})
				continue
			}
			input, err := json.Marshal(b["input"])
			if err != nil {
				input = []byte("{}")
			}
			out, callErr := kit.Call(t, name, input)
			isErr := callErr != nil
			if isErr {
				out = callErr.Error()
			}
			results = append(results, Block{"type": "tool_result", "tool_use_id": id, "content": out, "is_error": isErr})
			r.mu.Lock()
			t.ResultSizes[id] = len(out)
			t.ResultOrder = append(t.ResultOrder, id)
			r.mu.Unlock()
		}
		if uses == 0 {
			if resp.StopReason == "error" {
				r.finish(t, "failed", "the helper's model stopped with an error and no tool call", nil)
				return
			}
			textTurns++
			if textTurns >= textTurnsMax {
				r.finish(t, "failed", fmt.Sprintf("the helper's model wrote %d turns in a row without calling a tool", textTurnsMax), nil)
				return
			}
			results = append(results, Block{"type": "text", "text": textTurn})
		} else {
			textTurns = 0
		}
		r.mu.Lock()
		pending := t.Pending
		t.Pending = nil
		for _, s := range pending {
			results = append(results, Block{"type": "text", "text": fmt.Sprintf("[Steer from %s]\n%s", s.From, s.Message)})
			t.SteeredBy = s.From
		}
		t.History = append(t.History, Message{Role: "user", Content: results})
		t.LastActivity = time.Now().UTC()
		r.mu.Unlock()
		if err := r.save(t); err != nil {
			slog.Error("helper save failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
		}
		if end != nil && len(pending) == 0 {
			input, _ := end["input"].(map[string]any)
			if end["name"] == toolAsk {
				question, _ := input["question"].(string)
				r.finish(t, "needs_input", question, nil)
				return
			}
			result, _ := input["result"].(string)
			var files []string
			if list, ok := input["files"].([]any); ok {
				for _, f := range list {
					if path, ok := f.(string); ok {
						files = append(files, path)
					}
				}
			}
			r.finish(t, "done", result, files)
			return
		}
	}
}

// finish ends a stretch of work: t goes idle (asking for a question), and its report is
// saved and published. Steers that arrived as the stretch ended start the next one.
func (r *Runner) finish(t *Task, state, text string, files []string) {
	r.mu.Lock()
	r.seq++
	report := Report{
		Seq: r.seq, Task: "task/" + t.Kind + "/" + t.ID, Kind: t.Kind, ID: t.ID, AgentID: t.SteeredBy,
		State: state, Text: strings.TrimSpace(text), Files: files, At: time.Now().UTC(),
	}
	if report.Files == nil {
		report.Files = []string{}
	}
	t.LastReport = &report
	t.Status = StatusIdle
	if state == "needs_input" {
		t.Status = StatusAsking
	}
	t.LastActivity = report.At
	resume := len(t.Pending) > 0
	if resume {
		var steers []Block
		for _, s := range t.Pending {
			steers = append(steers, Block{"type": "text", "text": fmt.Sprintf("[Steer from %s]\n%s", s.From, s.Message)})
			t.SteeredBy = s.From
		}
		t.Pending = nil
		if n := len(t.History); n > 0 && t.History[n-1].Role == "user" {
			t.History[n-1].Content = append(t.History[n-1].Content, steers...)
		} else {
			t.History = append(t.History, Message{Role: "user", Content: steers})
		}
		t.Status = StatusWorking
	}
	r.mu.Unlock()
	if resume {
		go r.run(r.kits[t.Kind], t)
	}
	if err := r.save(t); err != nil {
		slog.Error("helper save failed", "code", "internal_error", "task", report.Task, "err", err)
	}
	if state == "failed" {
		slog.Error("helper failed", "code", "helper_failed", "task", report.Task, "msg_detail", text)
	}
	r.publish(report)
}
