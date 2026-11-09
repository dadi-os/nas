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
	"strconv"
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
	// pauseAbandonAfter is how long a paused task may go without input from Ankur before the
	// sweep resumes its loop as if he had handed it back with no note, so an abandoned
	// takeover never leaves an agent waiting on a helper that will not move.
	pauseAbandonAfter = 30 * time.Minute
	// notRun is the result of a tool call the helper asked for after Ankur took over.
	notRun = "[not run] Ankur took over before this ran. Look again before you act."
	// purposeChars is how much of a task's purpose its view shows.
	purposeChars = 120
	// logLimitDefault and logLimitMax are the default and largest page of a task's log.
	logLimitDefault = 200
	logLimitMax     = 1000
	toolFinish      = "finish"
	toolAsk         = "ask"
	textTurn        = "[continue] Text you write reaches no one. Take the next step with a tool, call ask for something only the agent or Ankur can give, or call finish with the result."
)

// Status is where a task stands: its helper is working, paused while Ankur drives its
// browser or terminal, idle after a finish, waiting on an answer it asked for, or asleep
// with its resource stopped and its history kept, or stopped for good by Halt.
type Status string

const (
	StatusWorking Status = "working"
	StatusPaused  Status = "paused"
	StatusIdle    Status = "idle"
	StatusAsking  Status = "asking"
	StatusAsleep  Status = "asleep"
	StatusStopped Status = "stopped"
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
	// Halt stops the task's resource and every process it started, including background
	// jobs that survive Sleep. It is safe to repeat.
	Halt(t *Task) error
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
	// loopDone is closed when the helper loop started with the task's last move to working
	// ends; resuming is set while a hand back waits for that loop to end; pausedFrom is the
	// status the task had when Ankur took it (working, idle or asking). All three are guarded
	// by the runner's lock and are not saved.
	loopDone   chan struct{}
	resuming   bool
	pausedFrom Status
}

// startLoop marks t working with a new loop about to run on it. Callers hold the runner's lock.
func (t *Task) startLoop() {
	t.Status = StatusWorking
	t.loopDone = make(chan struct{})
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

// Report is what a helper hands back: a result (done), a question (needs_input), the error
// that stopped it (failed), or Ankur's word that he stopped it (stopped). Seq orders reports
// across all tasks since Nas started.
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
	journal *journal
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
	jr, err := newJournal(filepath.Join(dir, logDirName))
	if err != nil {
		return nil, err
	}
	r := &Runner{dir: dir, journal: jr, dwar: newDwarClient(dwarURL), kits: map[string]Kit{}, publish: publish, tasks: map[string]*Task{}}
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
		if t.Status != StatusStopped {
			t.Status = StatusAsleep
		}
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
	mux.HandleFunc("POST /tasks/{kind}/{id}/pause", func(w http.ResponseWriter, req *http.Request) {
		view, err := r.Pause(req.PathValue("kind"), req.PathValue("id"))
		if err != nil {
			fail(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("POST /tasks/{kind}/{id}/resume", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Note string `json:"note"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeError(w, req, http.StatusBadRequest, "invalid_request", "body must be {note?}")
			return
		}
		view, err := r.Resume(req.PathValue("kind"), req.PathValue("id"), body.Note)
		if err != nil {
			fail(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("POST /tasks/{kind}/{id}/stop", func(w http.ResponseWriter, req *http.Request) {
		view, err := r.Stop(req.PathValue("kind"), req.PathValue("id"))
		if err != nil {
			fail(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("POST /tasks/{kind}/{id}/halt", func(w http.ResponseWriter, req *http.Request) {
		view, err := r.Halt(req.PathValue("kind"), req.PathValue("id"))
		if err != nil {
			fail(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("GET /tasks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, r.List())
	})
	mux.HandleFunc("GET /tasks/usage", func(w http.ResponseWriter, req *http.Request) {
		since, err := time.Parse(time.RFC3339, req.URL.Query().Get("since"))
		if err != nil {
			writeError(w, req, http.StatusUnprocessableEntity, "invalid_request", "since is required, an RFC3339 time")
			return
		}
		tasks, err := r.journal.usage(since)
		if err != nil {
			fail(w, req, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	})
	mux.HandleFunc("GET /tasks/{kind}/{id}/log", func(w http.ResponseWriter, req *http.Request) {
		after, limit := int64(0), logLimitDefault
		var err error
		if v := req.URL.Query().Get("after_seq"); v != "" {
			if after, err = strconv.ParseInt(v, 10, 64); err != nil || after < 0 {
				writeError(w, req, http.StatusUnprocessableEntity, "invalid_request", "after_seq must be a non-negative integer")
				return
			}
		}
		if v := req.URL.Query().Get("limit"); v != "" {
			if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > logLimitMax {
				writeError(w, req, http.StatusUnprocessableEntity, "invalid_request", fmt.Sprintf("limit must be an integer from 1 to %d", logLimitMax))
				return
			}
		}
		kind, id := req.PathValue("kind"), req.PathValue("id")
		entries, next, exists, err := r.journal.read(kind, id, after, limit)
		if err != nil {
			fail(w, req, err)
			return
		}
		if !exists {
			writeError(w, req, http.StatusNotFound, "not_found", fmt.Sprintf("task/%s/%s has no log", kind, id))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"task": "task/" + kind + "/" + id, "entries": entries, "next_seq": next})
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

// record appends row to t's log as the agent now steering t; a failure is logged and the
// work goes on. Callers hold r.mu.
func (r *Runner) record(t *Task, typ string, row entry) {
	h := row.header()
	h.Type, h.Agent = typ, t.SteeredBy
	if err := r.journal.append(t.Kind, t.ID, row); err != nil {
		slog.Error("helper log failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
	}
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
// works, held until Ankur hands it back while he has it paused, or starting its next stretch
// of work, after waking it when it sleeps.
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
	stopped, asleep := t.Status == StatusStopped, t.Status == StatusAsleep
	r.mu.Unlock()
	if stopped {
		return View{}, stoppedError(t)
	}
	if asleep {
		if err := r.wake(kit, t); err != nil {
			return View{}, err
		}
	}
	return r.steer(kit, t, agent, message)
}

// steer queues message for a working or paused helper, or appends it and starts the loop.
func (r *Runner) steer(kit Kit, t *Task, agent, message string) (View, error) {
	r.mu.Lock()
	if t.Status == StatusStopped {
		r.mu.Unlock()
		return View{}, stoppedError(t)
	}
	t.SteeredBy = agent
	t.LastActivity = time.Now().UTC()
	r.record(t, entrySteer, &steerRow{From: agent, Message: clipRunes(message, logLongMax), Held: t.Status == StatusPaused, Start: len(t.History) == 0})
	if t.Status == StatusWorking || t.Status == StatusPaused {
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
	t.startLoop()
	r.mu.Unlock()
	if err := r.save(t); err != nil {
		return View{}, err
	}
	go r.run(kit, t)
	return r.view(kit, t), nil
}

// find returns task kind/id and its kit, or a not_found Error.
func (r *Runner) find(kind, id string) (Kit, *Task, error) {
	kit, err := r.kit(kind)
	if err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	t, ok := r.tasks[kind+"/"+id]
	r.mu.Unlock()
	if !ok {
		return nil, nil, Error{http.StatusNotFound, "not_found", fmt.Sprintf("task/%s/%s does not exist", kind, id)}
	}
	return kit, t, nil
}

// interrupted reports whether t is no longer working, which ends its helper loop at the next
// boundary: Ankur paused it.
func (r *Runner) interrupted(t *Task) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return t.Status != StatusWorking
}

// stoppedError is the Error for a steer sent to a task Halt ended.
func stoppedError(t *Task) Error {
	return Error{http.StatusConflict, "task_stopped", fmt.Sprintf("task/%s/%s was stopped and cannot be steered; start a new task", t.Kind, t.ID)}
}

// conflict is the Error for a pause, resume or stop the task's status does not allow.
func conflict(t *Task, status Status, want string) Error {
	return Error{http.StatusConflict, "conflict", fmt.Sprintf("task/%s/%s is %s; only %s", t.Kind, t.ID, status, want)}
}

// Drive records that Ankur is acting on the resource of kind that a task holds (a browser
// slot or a tmux session): the task counts the input as activity, so it neither sleeps nor
// is forgotten while he drives, and a working helper is paused at its next boundary while an
// idle or asking one is paused at once, so steers sent to it wait until he hands it back. It
// reports whether a task changed status; input for a resource no awake task holds changes
// nothing. It is called for every input event, so it only takes the runner's lock.
func (r *Runner) Drive(kind, resource string) bool {
	r.mu.Lock()
	var t *Task
	for _, candidate := range r.tasks {
		if candidate.Kind == kind && candidate.Resource == resource && candidate.Status != StatusAsleep && candidate.Status != StatusStopped {
			t = candidate
			break
		}
	}
	if t == nil {
		r.mu.Unlock()
		return false
	}
	t.LastActivity = time.Now().UTC()
	if t.Status == StatusPaused {
		r.mu.Unlock()
		return false
	}
	t.pausedFrom = t.Status
	t.Status = StatusPaused
	r.mu.Unlock()
	if err := r.save(t); err != nil {
		slog.Error("helper save failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
	}
	return true
}

// Pause pauses task kind/id's helper: a working one takes no new step once the one under way
// ends, and an idle or asking one holds the steers it is sent. Pausing a paused task changes
// nothing; an asleep task is a conflict.
func (r *Runner) Pause(kind, id string) (View, error) {
	kit, t, err := r.find(kind, id)
	if err != nil {
		return View{}, err
	}
	r.mu.Lock()
	switch t.Status {
	case StatusPaused:
	case StatusWorking, StatusIdle, StatusAsking:
		t.pausedFrom = t.Status
		t.Status = StatusPaused
		t.LastActivity = time.Now().UTC()
		r.record(t, entryState, &stateRow{Status: string(t.Status), Reason: "ankur took over"})
	default:
		status := t.Status
		r.mu.Unlock()
		return View{}, conflict(t, status, "an awake task can be paused")
	}
	r.mu.Unlock()
	return r.view(kit, t), r.save(t)
}

// Resume hands a paused task back to its helper with the note Ankur wrote, if any, and the
// state of the work as he left it, and starts its loop again. Steers held while he drove
// join the same turn. The hand back waits for the paused loop to finish the step it was in,
// so a task can stay paused for a moment after Resume answers, and a second Resume in that
// moment changes nothing. A task that was idle or asking when he took it has no loop to
// continue: see handBackIdle. A task that is not paused is a conflict.
func (r *Runner) Resume(kind, id, note string) (View, error) {
	kit, t, err := r.find(kind, id)
	if err != nil {
		return View{}, err
	}
	r.mu.Lock()
	if t.Status != StatusPaused {
		status := t.Status
		r.mu.Unlock()
		return View{}, conflict(t, status, "a paused task can be resumed")
	}
	if t.pausedFrom != StatusWorking {
		r.mu.Unlock()
		return r.handBackIdle(kit, t, note)
	}
	if t.resuming {
		r.mu.Unlock()
		return r.view(kit, t), nil
	}
	t.resuming = true
	done := t.loopDone
	r.mu.Unlock()
	select {
	case <-done:
		return r.handBackNow(kit, t, note)
	default:
	}
	go func() {
		<-done
		if _, err := r.handBackNow(kit, t, note); err != nil {
			slog.Error("helper resume failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
		}
	}()
	return r.view(kit, t), nil
}

// handBackNow hands t back to its helper and starts its loop, unless a stop came first.
func (r *Runner) handBackNow(kit Kit, t *Task, note string) (View, error) {
	r.mu.Lock()
	if t.Status != StatusPaused || !t.resuming {
		r.mu.Unlock()
		return r.view(kit, t), nil
	}
	t.resuming = false
	r.handBack(kit, t, note)
	r.mu.Unlock()
	if err := r.save(t); err != nil {
		return View{}, err
	}
	go r.run(kit, t)
	return r.view(kit, t), nil
}

// handBackIdle hands back a task that was idle or asking when Ankur took it. Its helper has no
// loop to continue, so with no steer held it goes back to the status it had, and his note
// waits in its history for the next steer; with steers held it starts the stretch they ask
// for, preceded by his note and the state of the work, as a working task would.
func (r *Runner) handBackIdle(kit Kit, t *Task, note string) (View, error) {
	r.mu.Lock()
	if t.Status != StatusPaused {
		r.mu.Unlock()
		return r.view(kit, t), nil
	}
	if len(t.Pending) > 0 {
		r.handBack(kit, t, note)
		r.mu.Unlock()
		if err := r.save(t); err != nil {
			return View{}, err
		}
		go r.run(kit, t)
		return r.view(kit, t), nil
	}
	t.Status = t.pausedFrom
	t.LastActivity = time.Now().UTC()
	r.record(t, entryState, &stateRow{Status: string(t.Status), Reason: "resume"})
	if note = strings.TrimSpace(note); note != "" {
		text := Block{"type": "text", "text": "[Hand back from Ankur]\n" + note}
		if n := len(t.History); n > 0 && t.History[n-1].Role == "user" {
			t.History[n-1].Content = append(t.History[n-1].Content, text)
		} else {
			t.History = append(t.History, Message{Role: "user", Content: []Block{text}})
		}
	}
	r.mu.Unlock()
	return r.view(kit, t), r.save(t)
}

// handBack gives a paused helper its loop back: a turn with Ankur's note (or the plain fact
// that he drove), what he did being no part of it, and the state of the work now, joined by
// the steers held while he drove. The task is working again. Callers hold r.mu.
func (r *Runner) handBack(kit Kit, t *Task, note string) {
	head := "[Resumed after Ankur drove]"
	if note = strings.TrimSpace(note); note != "" {
		head = "[Hand back from Ankur]\n" + note
	}
	blocks := []Block{{"type": "text", "text": fmt.Sprintf("%s\n\nAnkur used this %s while you were paused. What he did is not replayed to you: look at it again before your next step.\n\n[State of the work]\n%s", head, t.Kind, kit.State(t))}}
	for _, s := range t.Pending {
		blocks = append(blocks, Block{"type": "text", "text": fmt.Sprintf("[Steer from %s]\n%s", s.From, s.Message)})
		t.SteeredBy = s.From
	}
	t.Pending = nil
	if n := len(t.History); n > 0 && t.History[n-1].Role == "user" {
		t.History[n-1].Content = append(t.History[n-1].Content, blocks...)
	} else {
		t.History = append(t.History, Message{Role: "user", Content: blocks})
	}
	t.startLoop()
	t.LastActivity = time.Now().UTC()
	r.record(t, entryState, &stateRow{Status: string(t.Status), Reason: "resume"})
}

// Stop ends a paused task's work once its loop has finished the step it was in: its resource
// goes to sleep with its history kept and the steers held while Ankur drove are dropped. The
// agent whose steer it was answering is told in a stopped report when the task was working or
// steers were dropped; an idle or asking task had already reported. A task that is not paused
// is a conflict.
func (r *Runner) Stop(kind, id string) (View, error) {
	kit, t, err := r.find(kind, id)
	if err != nil {
		return View{}, err
	}
	r.mu.Lock()
	if t.Status != StatusPaused {
		status := t.Status
		r.mu.Unlock()
		return View{}, conflict(t, status, "a paused task can be stopped")
	}
	held := len(t.Pending)
	wasWorking := t.pausedFrom == StatusWorking
	t.Pending = nil
	t.resuming = false
	done := t.loopDone
	r.mu.Unlock()
	if done != nil {
		<-done
	}
	if err := kit.Sleep(t); err != nil {
		return View{}, fmt.Errorf("sleep task/%s/%s: %w", t.Kind, t.ID, err)
	}
	r.mu.Lock()
	t.Status = StatusAsleep
	r.record(t, entryState, &stateRow{Status: string(t.Status), Reason: "stop"})
	if !wasWorking && held == 0 {
		t.LastActivity = time.Now().UTC()
		r.mu.Unlock()
		return r.view(kit, t), r.save(t)
	}
	text := "Ankur stopped this task while he was driving it."
	if held > 0 {
		text += fmt.Sprintf(" %d steer(s) sent while he drove were not read.", held)
	}
	report := r.newReport(t, "stopped", text, nil)
	t.LastActivity = report.At
	r.mu.Unlock()
	if err := r.save(t); err != nil {
		slog.Error("helper save failed", "code", "internal_error", "task", report.Task, "err", err)
	}
	r.publish(report)
	return r.view(kit, t), nil
}

// Halt is the hard stop: whatever the task is doing, it ends for good in status stopped. Its
// loop takes no further step (a tool call already running is cut short by the kill and its
// result dropped), its resource and every process under it are killed, held steers are
// dropped, and every later steer is refused task_stopped. An asleep task has no resource to kill. The agent whose steer was in flight
// is told in a stopped report. Halting a stopped task only repeats the kill.
func (r *Runner) Halt(kind, id string) (View, error) {
	kit, t, err := r.find(kind, id)
	if err != nil {
		return View{}, err
	}
	r.mu.Lock()
	wasStopped := t.Status == StatusStopped
	holdsResource := t.Status != StatusAsleep && (!wasStopped || t.Resource != "")
	inFlight := t.Status == StatusWorking || t.Status == StatusPaused || len(t.Pending) > 0
	held := len(t.Pending)
	t.Status = StatusStopped
	t.Pending = nil
	t.resuming = false
	t.LastActivity = time.Now().UTC()
	r.record(t, entryState, &stateRow{Status: string(t.Status), Reason: "halt"})
	r.mu.Unlock()
	if holdsResource {
		if err := kit.Halt(t); err != nil {
			return View{}, fmt.Errorf("halt task/%s/%s: %w", t.Kind, t.ID, err)
		}
		t.Resource = ""
	}
	if wasStopped || !inFlight {
		return r.view(kit, t), r.save(t)
	}
	text := "This task was stopped; its resource and every process it started were killed."
	if held > 0 {
		text += fmt.Sprintf(" %d steer(s) were not read.", held)
	}
	r.mu.Lock()
	report := r.newReport(t, "stopped", text, nil)
	r.mu.Unlock()
	if err := r.save(t); err != nil {
		slog.Error("helper save failed", "code", "internal_error", "task", report.Task, "err", err)
	}
	slog.Info("helper halted", "task", report.Task)
	r.publish(report)
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
		if err := r.sleep(kit, victim, "memory"); err != nil {
			return err
		}
	}
	if err := kit.Wake(t); err != nil {
		return fmt.Errorf("wake task/%s/%s: %w", t.Kind, t.ID, err)
	}
	r.mu.Lock()
	t.Status = StatusIdle
	r.record(t, entryState, &stateRow{Status: string(t.Status), Reason: "wake"})
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

// sleep stops t's resource and marks it asleep, keeping its history; reason is logged.
func (r *Runner) sleep(kit Kit, t *Task, reason string) error {
	if err := kit.Sleep(t); err != nil {
		return fmt.Errorf("sleep task/%s/%s: %w", t.Kind, t.ID, err)
	}
	r.mu.Lock()
	t.Status = StatusAsleep
	r.record(t, entryState, &stateRow{Status: string(t.Status), Reason: reason})
	r.mu.Unlock()
	return r.save(t)
}

// Sweep puts tasks idle past their kind's limit to sleep, forgets tasks idle past
// forgetAfter, and resumes paused tasks that Ankur has not touched for pauseAbandonAfter.
// Failures are logged; the next sweep tries again.
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
		case idle > forgetAfter && status != StatusWorking && status != StatusPaused:
			if status != StatusAsleep && status != StatusStopped {
				if err := r.sleep(kit, t, "forget"); err != nil {
					slog.Error("helper sleep failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
					continue
				}
			}
			r.mu.Lock()
			delete(r.tasks, t.Kind+"/"+t.ID)
			r.record(t, entryState, &stateRow{Status: "forgotten", Reason: "forget"})
			r.mu.Unlock()
			if err := os.Remove(r.path(t)); err != nil {
				slog.Error("helper forget failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
			}
		case status == StatusPaused && idle > pauseAbandonAfter:
			if _, err := r.Resume(t.Kind, t.ID, ""); err != nil {
				slog.Error("helper resume failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
			}
		case (status == StatusIdle || status == StatusAsking) && idle > kit.IdleSleep():
			if err := r.sleep(kit, t, "idle"); err != nil {
				slog.Error("helper sleep failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
			}
		}
	}
	if err := r.journal.pruneDue(now); err != nil {
		slog.Error("helper log prune failed", "code", "internal_error", "err", err)
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
// it finishes or asks. A failure stops the loop and is reported. When Ankur pauses the task
// the loop ends at the next boundary: a model answer that arrives after the pause is dropped,
// a tool call already running finishes, and the calls after it are answered as not run, so the
// history stays valid for the hand back.
func (r *Runner) run(kit Kit, t *Task) {
	r.mu.Lock()
	done := t.loopDone
	r.mu.Unlock()
	defer close(done)
	textTurns := 0
	for {
		r.mu.Lock()
		req := chatRequest{System: kit.Charter(), Messages: r.messages(t), Tools: append(kit.Tools(), askSpec, finishSpec), ToolChoice: "auto"}
		caller := "nas/helper/" + t.Kind + "/" + t.SteeredBy
		r.mu.Unlock()
		began := time.Now()
		resp, err := r.dwar.complete(caller, req)
		if err != nil {
			r.finish(t, "failed", err.Error(), nil)
			return
		}
		r.mu.Lock()
		r.record(t, entryTurn, newTurnRow(resp, time.Since(began)))
		if t.Status != StatusWorking {
			r.mu.Unlock()
			return
		}
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
			if r.interrupted(t) {
				results = append(results, Block{"type": "tool_result", "tool_use_id": id, "content": notRun, "is_error": true})
				continue
			}
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
			calledAt := time.Now()
			out, callErr := kit.Call(t, name, input)
			isErr := callErr != nil
			if isErr {
				out = callErr.Error()
			}
			results = append(results, Block{"type": "tool_result", "tool_use_id": id, "content": out, "is_error": isErr})
			r.mu.Lock()
			r.record(t, entryResult, &resultRow{ToolUseID: id, Name: name, IsError: isErr, Chars: len([]rune(out)), Head: clipRunes(out, logTextMax), Ms: time.Since(calledAt).Milliseconds()})
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
		if t.Status != StatusWorking {
			if len(results) > 0 {
				t.History = append(t.History, Message{Role: "user", Content: results})
			}
			r.mu.Unlock()
			if err := r.save(t); err != nil {
				slog.Error("helper save failed", "code", "internal_error", "task", t.Kind+"/"+t.ID, "err", err)
			}
			return
		}
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

// newReport makes t's next report and records it as its last. Callers hold r.mu.
func (r *Runner) newReport(t *Task, state, text string, files []string) Report {
	r.seq++
	report := Report{
		Seq: r.seq, Task: "task/" + t.Kind + "/" + t.ID, Kind: t.Kind, ID: t.ID, AgentID: t.SteeredBy,
		State: state, Text: strings.TrimSpace(text), Files: files, At: time.Now().UTC(),
	}
	if report.Files == nil {
		report.Files = []string{}
	}
	t.LastReport = &report
	r.record(t, entryReport, &reportRow{State: state, Text: clipRunes(report.Text, logLongMax), Files: report.Files})
	return report
}

// finish ends a stretch of work: t goes idle (asking for a question), and its report is
// saved and published. Steers that arrived as the stretch ended start the next one.
func (r *Runner) finish(t *Task, state, text string, files []string) {
	r.mu.Lock()
	if t.Status == StatusStopped {
		r.mu.Unlock()
		return
	}
	report := r.newReport(t, state, text, files)
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
		t.startLoop()
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
