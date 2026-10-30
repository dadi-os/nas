package helpers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeKit is a Kit that records wakes, sleeps and calls; Call blocks on gate when set.
type fakeKit struct {
	mu     sync.Mutex
	wakes  int
	sleeps int
	calls  []string
	room   func() bool
	gate   chan struct{}
}

func (k *fakeKit) Kind() string    { return "terminal" }
func (k *fakeKit) Charter() string { return "charter" }
func (k *fakeKit) Tools() []ToolSpec {
	return []ToolSpec{{Name: "run", InputSchema: map[string]any{"type": "object"}}}
}
func (k *fakeKit) IdleSleep() time.Duration { return time.Minute }
func (k *fakeKit) State(t *Task) string     { return "cwd /home" }
func (k *fakeKit) Wake(t *Task) error {
	k.mu.Lock()
	k.wakes++
	k.mu.Unlock()
	t.Resource = "t1"
	return nil
}
func (k *fakeKit) Sleep(t *Task) error { k.mu.Lock(); k.sleeps++; k.mu.Unlock(); return nil }
func (k *fakeKit) Room() (bool, error) {
	if k.room == nil {
		return true, nil
	}
	return k.room(), nil
}
func (k *fakeKit) Call(t *Task, name string, input json.RawMessage) (string, error) {
	if k.gate != nil {
		<-k.gate
	}
	k.mu.Lock()
	k.calls = append(k.calls, name+" "+string(input))
	k.mu.Unlock()
	return "ok " + string(input), nil
}

// fakeDwar answers each /chat/complete call with the next scripted content and records the
// requests.
type fakeDwar struct {
	mu       sync.Mutex
	script   [][]Block
	requests []chatRequest
	callers  []string
}

func (d *fakeDwar) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		d.mu.Lock()
		d.requests = append(d.requests, req)
		d.callers = append(d.callers, r.Header.Get("X-Dadi-Caller"))
		if len(d.script) == 0 {
			d.mu.Unlock()
			http.Error(w, `{"error":{"type":"test","message":"script exhausted"}}`, http.StatusInternalServerError)
			return
		}
		next := d.script[0]
		d.script = d.script[1:]
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(chatResponse{Content: next, StopReason: "tool_use"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func use(id, name string, input map[string]any) Block {
	return Block{"type": "tool_use", "id": id, "name": name, "input": input}
}

// newTestRunner returns a runner over a temp dir with kit and dwar, and a channel of its reports.
func newTestRunner(t *testing.T, kit Kit, dwar *fakeDwar) (*Runner, chan Report, string) {
	t.Helper()
	dir := t.TempDir()
	reports := make(chan Report, 16)
	r, err := NewRunner(dir, dwar.server(t).URL, []Kit{kit}, func(rep Report) { reports <- rep })
	if err != nil {
		t.Fatal(err)
	}
	return r, reports, dir
}

func waitReport(t *testing.T, reports chan Report) Report {
	t.Helper()
	select {
	case rep := <-reports:
		return rep
	case <-time.After(5 * time.Second):
		t.Fatal("no report")
		return Report{}
	}
}

func TestPurposeKeepsTheFullFirstSteerAndViewsClipIt(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "ok"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	steer := strings.Repeat("sign in at example.com and ", 20) + "then read the statement from chase.com"
	view, err := r.Start("terminal", "agent-a", "  "+steer+"  ", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	if !strings.HasSuffix(view.Purpose, "…") || len([]rune(view.Purpose)) != purposeChars+1 {
		t.Fatalf("view purpose not clipped: %q", view.Purpose)
	}
	r.mu.Lock()
	full := r.tasks["terminal/"+view.ID].Purpose
	r.mu.Unlock()
	if full != steer {
		t.Fatalf("task purpose %q, want the whole first steer", full)
	}
	for _, v := range r.List() {
		if v.ID == view.ID && len([]rune(v.Purpose)) != purposeChars+1 {
			t.Fatalf("listed purpose not clipped: %q", v.Purpose)
		}
	}
}

func TestHelperRunsToolsThenFinishes(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "run", map[string]any{"command": "ls"})},
		{use("u2", "finish", map[string]any{"result": "listed", "files": []any{"/home/a.txt"}})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "list the home", "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Task != "task/terminal/"+view.ID || view.Owner != "agent-a" || view.Purpose != "list the home" {
		t.Fatalf("view %+v", view)
	}
	rep := waitReport(t, reports)
	if rep.State != "done" || rep.Text != "listed" || rep.AgentID != "agent-a" || len(rep.Files) != 1 || rep.Files[0] != "/home/a.txt" {
		t.Fatalf("report %+v", rep)
	}
	if len(kit.calls) != 1 || !strings.HasPrefix(kit.calls[0], "run ") {
		t.Fatalf("calls %v", kit.calls)
	}
	got, err := r.Get("terminal", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusIdle || got.LastReport == nil || got.LastReport.Seq != rep.Seq {
		t.Fatalf("after finish %+v", got)
	}
	first := dwar.requests[0]
	if first.System != "charter" || len(first.Tools) != 3 || !strings.Contains(fmt.Sprint(first.Messages[0].Content), "[Steer from agent-a]") {
		t.Fatalf("first request %+v", first)
	}
	if dwar.callers[0] != "nas/helper/terminal/agent-a" {
		t.Fatalf("caller %s", dwar.callers[0])
	}
}

func TestHelperAskThenAnswerContinuesTheSameHistory(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "ask", map[string]any{"question": "need the 6-digit code"})},
		{use("u2", "finish", map[string]any{"result": "signed in"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "sign in", "")
	if err != nil {
		t.Fatal(err)
	}
	asked := waitReport(t, reports)
	if asked.State != "needs_input" || asked.Text != "need the 6-digit code" {
		t.Fatalf("ask report %+v", asked)
	}
	if got, _ := r.Get("terminal", view.ID); got.Status != StatusAsking {
		t.Fatalf("status %s", got.Status)
	}
	if _, err := r.Steer("terminal", view.ID, "agent-b", "865353"); err != nil {
		t.Fatal(err)
	}
	done := waitReport(t, reports)
	if done.State != "done" || done.AgentID != "agent-b" {
		t.Fatalf("done report %+v", done)
	}
	second := dwar.requests[1]
	if len(second.Messages) != 3 || !strings.Contains(fmt.Sprint(second.Messages[2].Content), "865353") {
		t.Fatalf("answer did not join the same history: %+v", second.Messages)
	}
	if kit.wakes != 1 {
		t.Fatalf("woke %d times, want 1", kit.wakes)
	}
}

func TestSteerWhileWorkingJoinsBeforeTheNextStep(t *testing.T) {
	kit := &fakeKit{gate: make(chan struct{})}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "run", map[string]any{"command": "make"})},
		{use("u2", "finish", map[string]any{"result": "built with the flag"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "build it", "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		dwar.mu.Lock()
		n := len(dwar.requests)
		dwar.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := r.Steer("terminal", view.ID, "agent-a", "use the release flag"); err != nil {
		t.Fatal(err)
	}
	close(kit.gate)
	waitReport(t, reports)
	second := dwar.requests[1].Messages
	last := fmt.Sprint(second[len(second)-1].Content)
	if !strings.Contains(last, "tool_result") || !strings.Contains(last, "use the release flag") {
		t.Fatalf("steer not folded in after the tool result: %s", last)
	}
}

func TestIdleTasksSleepWakeOnSteerAndAreForgotten(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "finish", map[string]any{"result": "first"})},
		{use("u2", "finish", map[string]any{"result": "second"})},
	}}
	r, reports, dir := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "first job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	r.Sweep(time.Now().Add(30 * time.Second))
	if got, _ := r.Get("terminal", view.ID); got.Status != StatusIdle {
		t.Fatalf("slept before its idle limit: %s", got.Status)
	}
	r.Sweep(time.Now().Add(2 * time.Minute))
	if got, _ := r.Get("terminal", view.ID); got.Status != StatusAsleep || kit.sleeps != 1 {
		t.Fatalf("after idle limit: %s, sleeps %d", got.Status, kit.sleeps)
	}
	if _, err := r.Steer("terminal", view.ID, "agent-a", "second job"); err != nil {
		t.Fatal(err)
	}
	if rep := waitReport(t, reports); rep.Text != "second" {
		t.Fatalf("report %+v", rep)
	}
	if kit.wakes != 2 || len(dwar.requests[1].Messages) != 3 {
		t.Fatalf("wakes %d, history %d turns", kit.wakes, len(dwar.requests[1].Messages))
	}
	r.Sweep(time.Now().Add(forgetAfter + time.Hour))
	if _, err := r.Get("terminal", view.ID); err == nil {
		t.Fatal("task not forgotten after 24 idle hours")
	}
	if _, err := os.Stat(filepath.Join(dir, "terminal-"+view.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("forgotten task's file: %v", err)
	}
	if _, err := r.Steer("terminal", view.ID, "agent-a", "again"); err == nil {
		t.Fatal("steered a forgotten task")
	}
}

func TestWakeSleepsTheLongestIdleWhenMemoryIsShort(t *testing.T) {
	awake := 0
	kit := &fakeKit{}
	kit.room = func() bool { return awake < 1 }
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "finish", map[string]any{"result": "one"})},
		{use("u2", "finish", map[string]any{"result": "two"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	first, err := r.Start("terminal", "agent-a", "one", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	awake = 1
	kit.room = func() bool { return kit.sleeps >= 1 }
	if _, err := r.Start("terminal", "agent-b", "two", ""); err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	if got, _ := r.Get("terminal", first.ID); got.Status != StatusAsleep {
		t.Fatalf("longest-idle task not put to sleep: %s", got.Status)
	}
}

func TestTasksReloadAsleepAfterRestart(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "done"})}}}
	r, reports, dir := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	reloaded, err := NewRunner(dir, dwar.server(t).URL, []Kit{kit}, func(Report) {})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Get("terminal", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAsleep || got.Owner != "agent-a" || got.LastReport == nil {
		t.Fatalf("reloaded %+v", got)
	}
}

func TestOldResultsAreClearedPastTheBudget(t *testing.T) {
	task := &Task{ResultSizes: map[string]int{}, Cleared: map[string]bool{}}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("u%d", i)
		task.ResultOrder = append(task.ResultOrder, id)
		task.ResultSizes[id] = 40_000
		task.History = append(task.History, Message{Role: "user", Content: []Block{{"type": "tool_result", "tool_use_id": id, "content": strings.Repeat("x", 40_000), "is_error": false}}})
	}
	r := &Runner{}
	msgs := r.messages(task)
	cleared := 0
	for _, m := range msgs {
		if text, _ := m.Content[0]["content"].(string); strings.HasPrefix(text, "[cleared]") {
			cleared++
		}
	}
	if cleared != 3 || !task.Cleared["u0"] || task.Cleared["u3"] {
		t.Fatalf("cleared %d, map %v", cleared, task.Cleared)
	}
	if text, _ := task.History[0].Content[0]["content"].(string); strings.HasPrefix(text, "[cleared]") {
		t.Fatal("clearing rewrote the stored history")
	}
}
