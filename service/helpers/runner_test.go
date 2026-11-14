package helpers

import (
	"encoding/json"
	"errors"
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

// testBudget is the step budget of the test runners.
var testBudget = StepBudget{Warn: 3, Max: 5}

// fakeKit is a Kit that records wakes, sleeps and calls; Call signals started, then blocks on
// gate, when they are set.
type fakeKit struct {
	mu      sync.Mutex
	wakes   int
	sleeps  int
	halts   int
	calls   []string
	room    func() bool
	gate    chan struct{}
	started chan struct{}
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
func (k *fakeKit) Halt(t *Task) error  { k.mu.Lock(); k.halts++; k.mu.Unlock(); return nil }
func (k *fakeKit) Room() (bool, error) {
	if k.room == nil {
		return true, nil
	}
	return k.room(), nil
}
func (k *fakeKit) Call(t *Task, name string, input json.RawMessage) (string, error) {
	if k.started != nil {
		k.started <- struct{}{}
	}
	if k.gate != nil {
		<-k.gate
	}
	k.mu.Lock()
	k.calls = append(k.calls, name+" "+string(input))
	k.mu.Unlock()
	return "ok " + string(input), nil
}

// fakeDwar answers each /chat/complete call with the next scripted content and records the
// requests; it waits on hold, when set, before answering.
type fakeDwar struct {
	mu       sync.Mutex
	script   [][]Block
	requests []chatRequest
	callers  []string
	hold     chan struct{}
	usage    Usage
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
		if d.hold != nil {
			d.mu.Unlock()
			<-d.hold
			d.mu.Lock()
		}
		if len(d.script) == 0 {
			d.mu.Unlock()
			http.Error(w, `{"error":{"type":"test","message":"script exhausted"}}`, http.StatusInternalServerError)
			return
		}
		next := d.script[0]
		d.script = d.script[1:]
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(chatResponse{Content: next, StopReason: "tool_use", Usage: d.usage})
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
	r, err := NewRunner(dir, dwar.server(t).URL, testBudget, []Kit{kit}, func(rep Report) { reports <- rep })
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

// TestStepBudgetWarnsThenForcesFinish gives a helper that never finishes a warning at the
// warn turn and, from the max turn, a request that offers only finish.
func TestStepBudgetWarnsThenForcesFinish(t *testing.T) {
	kit := &fakeKit{}
	var script [][]Block
	for i := 1; i <= testBudget.Max; i++ {
		script = append(script, []Block{use(fmt.Sprintf("u%d", i), "run", map[string]any{"command": "ls"})})
	}
	script = append(script, []Block{use("fin", "finish", map[string]any{"result": "partial"})})
	dwar := &fakeDwar{script: script}
	r, reports, _ := newTestRunner(t, kit, dwar)
	if _, err := r.Start("terminal", "agent-a", "list", ""); err != nil {
		t.Fatal(err)
	}
	if rep := waitReport(t, reports); rep.State != "done" || rep.Text != "partial" {
		t.Fatalf("report %+v", rep)
	}
	if len(dwar.requests) != testBudget.Max+1 {
		t.Fatalf("%d requests", len(dwar.requests))
	}
	warned := fmt.Sprint(dwar.requests[testBudget.Warn].Messages)
	if !strings.Contains(warned, "[budget] 2 turns left") || strings.Contains(fmt.Sprint(dwar.requests[testBudget.Warn-1].Messages), "[budget]") {
		t.Fatal("the warning is missing at the warn turn or came early")
	}
	for i, req := range dwar.requests {
		finishOnly := i == testBudget.Max
		if finishOnly != (len(req.Tools) == 1 && req.Tools[0].Name == toolFinish && req.ToolChoice == "any") {
			t.Fatalf("request %d tools %d choice %s", i, len(req.Tools), req.ToolChoice)
		}
	}
}

// TestStepBudgetRestartsWithASteer lets a steer that arrives mid-loop start a fresh budget.
func TestStepBudgetRestartsWithASteer(t *testing.T) {
	kit := &fakeKit{gate: make(chan struct{}), started: make(chan struct{}, 8)}
	var script [][]Block
	for i := 1; i <= testBudget.Max; i++ {
		script = append(script, []Block{use(fmt.Sprintf("u%d", i), "run", map[string]any{"command": "ls"})})
	}
	script = append(script, []Block{use("fin", "finish", map[string]any{"result": "ok"})})
	dwar := &fakeDwar{script: script}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "list", "")
	if err != nil {
		t.Fatal(err)
	}
	<-kit.started
	for i := 0; i < testBudget.Max-1; i++ {
		kit.gate <- struct{}{}
		<-kit.started
	}
	if _, err := r.Steer("terminal", view.ID, "agent-a", "again"); err != nil {
		t.Fatal(err)
	}
	close(kit.gate)
	waitReport(t, reports)
	for i, req := range dwar.requests {
		if len(req.Tools) == 1 {
			t.Fatalf("request %d was finish-only although a steer restarted the budget", i)
		}
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
	reloaded, err := NewRunner(dir, dwar.server(t).URL, testBudget, []Kit{kit}, func(Report) {})
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

// waitRequests waits until dwar has received n requests.
func waitRequests(t *testing.T, dwar *fakeDwar, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		dwar.mu.Lock()
		got := len(dwar.requests)
		dwar.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dwar got %d requests, want %d", got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitStatus waits until task kind/id has status want.
func waitStatus(t *testing.T, r *Runner, kind, id string, want Status) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := r.Get(kind, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("status %s, want %s", got.Status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDrivePausesAtTheBoundaryAndHandBackResumesWithTheNote(t *testing.T) {
	kit := &fakeKit{gate: make(chan struct{}), started: make(chan struct{}, 4)}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "run", map[string]any{"command": "login"})},
		{use("u2", "finish", map[string]any{"result": "carried on"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "sign in", "")
	if err != nil {
		t.Fatal(err)
	}
	<-kit.started
	if !r.Drive("terminal", "t1") {
		t.Fatal("first input did not pause a working helper")
	}
	if r.Drive("terminal", "t1") {
		t.Fatal("a second input changed the task again")
	}
	close(kit.gate)
	time.Sleep(100 * time.Millisecond)
	if got, _ := r.Get("terminal", view.ID); got.Status != StatusPaused {
		t.Fatalf("status %s, want paused", got.Status)
	}
	if len(dwar.requests) != 1 {
		t.Fatalf("a model turn started after the pause: %d requests", len(dwar.requests))
	}
	if len(kit.calls) != 1 {
		t.Fatalf("the in-flight call did not finish: %v", kit.calls)
	}
	if _, err := r.Resume("terminal", view.ID, "I signed in by hand"); err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	last := dwar.requests[1].Messages[len(dwar.requests[1].Messages)-1]
	text := fmt.Sprint(last.Content)
	for _, want := range []string{"tool_result", "[Hand back from Ankur]", "I signed in by hand", "cwd /home", "summary of what Ankur did", "Look at the terminal again"} {
		if !strings.Contains(text, want) {
			t.Fatalf("resume turn lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "not replayed") {
		t.Fatalf("a summary was given, yet the turn says nothing is known: %s", text)
	}
}

func TestPauseDropsAModelAnswerThatArrivesAfterIt(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{hold: make(chan struct{}), script: [][]Block{
		{use("u1", "finish", map[string]any{"result": "too early"})},
		{use("u2", "finish", map[string]any{"result": "after the hand back"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitRequests(t, dwar, 1)
	if _, err := r.Pause("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	dwar.hold <- struct{}{}
	select {
	case rep := <-reports:
		t.Fatalf("a paused helper reported %+v", rep)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := r.Resume("terminal", view.ID, ""); err != nil {
		t.Fatal(err)
	}
	go func() { dwar.hold <- struct{}{} }()
	if rep := waitReport(t, reports); rep.Text != "after the hand back" {
		t.Fatalf("report %+v", rep)
	}
	plain := fmt.Sprint(dwar.requests[1].Messages)
	if !strings.Contains(plain, "[Resumed after Ankur drove]") || !strings.Contains(plain, "not replayed to you") {
		t.Fatalf("no plain resume turn: %v", dwar.requests[1].Messages)
	}
}

func TestPauseAnswersTheCallsAfterTheRunningOneAsNotRun(t *testing.T) {
	kit := &fakeKit{gate: make(chan struct{}), started: make(chan struct{}, 4)}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "run", map[string]any{"command": "one"}), use("u2", "run", map[string]any{"command": "two"})},
		{use("u3", "finish", map[string]any{"result": "ok"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	<-kit.started
	r.Drive("terminal", "t1")
	close(kit.gate)
	waitStatus(t, r, "terminal", view.ID, StatusPaused)
	if _, err := r.Resume("terminal", view.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	if len(kit.calls) != 1 {
		t.Fatalf("the second call ran after the pause: %v", kit.calls)
	}
	resumed := fmt.Sprint(dwar.requests[1].Messages)
	if strings.Count(resumed, "[not run]") != 1 || !strings.Contains(resumed, "u2") {
		t.Fatalf("second call not answered as not run: %s", resumed)
	}
}

func TestSteerWhilePausedIsHeldUntilResume(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{hold: make(chan struct{}), script: [][]Block{
		{use("u1", "finish", map[string]any{"result": "ignored"})},
		{use("u2", "finish", map[string]any{"result": "used the held steer"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitRequests(t, dwar, 1)
	if _, err := r.Pause("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	held, err := r.Steer("terminal", view.ID, "agent-b", "also check the footer")
	if err != nil {
		t.Fatal(err)
	}
	if held.Status != StatusPaused {
		t.Fatalf("steer changed a paused task to %s", held.Status)
	}
	dwar.hold <- struct{}{}
	time.Sleep(100 * time.Millisecond)
	if len(dwar.requests) != 1 {
		t.Fatalf("a held steer started a model turn: %d requests", len(dwar.requests))
	}
	if _, err := r.Resume("terminal", view.ID, ""); err != nil {
		t.Fatal(err)
	}
	go func() { dwar.hold <- struct{}{} }()
	rep := waitReport(t, reports)
	if rep.AgentID != "agent-b" || !strings.Contains(fmt.Sprint(dwar.requests[1].Messages), "also check the footer") {
		t.Fatalf("held steer not delivered: report %+v", rep)
	}
}

func TestStopSleepsAPausedTaskAndTellsItsAgent(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{hold: make(chan struct{}), script: [][]Block{{use("u1", "finish", map[string]any{"result": "x"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitRequests(t, dwar, 1)
	if _, err := r.Pause("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Steer("terminal", view.ID, "agent-a", "hurry"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		dwar.hold <- struct{}{}
	}()
	stopped, err := r.Stop("terminal", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	rep := waitReport(t, reports)
	if stopped.Status != StatusAsleep || kit.sleeps != 1 {
		t.Fatalf("status %s, sleeps %d", stopped.Status, kit.sleeps)
	}
	if rep.State != "stopped" || rep.AgentID != "agent-a" || !strings.Contains(rep.Text, "1 steer(s)") {
		t.Fatalf("report %+v", rep)
	}
	if len(dwar.requests) != 1 {
		t.Fatalf("the stopped loop went on: %d requests", len(dwar.requests))
	}
}

func TestPauseResumeAndStopRefuseTheWrongStatus(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "done"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	for name, call := range map[string]func() error{
		"resume": func() error { _, err := r.Resume("terminal", view.ID, ""); return err },
		"stop":   func() error { _, err := r.Stop("terminal", view.ID); return err },
	} {
		var e Error
		if err := call(); !errors.As(err, &e) || e.Status != http.StatusConflict || e.Code != "conflict" {
			t.Errorf("%s on an idle task: %v", name, err)
		}
	}
	r.Sweep(time.Now().Add(2 * time.Minute))
	waitStatus(t, r, "terminal", view.ID, StatusAsleep)
	for name, call := range map[string]func() error{
		"pause":  func() error { _, err := r.Pause("terminal", view.ID); return err },
		"resume": func() error { _, err := r.Resume("terminal", view.ID, ""); return err },
		"stop":   func() error { _, err := r.Stop("terminal", view.ID); return err },
	} {
		var e Error
		if err := call(); !errors.As(err, &e) || e.Status != http.StatusConflict || e.Code != "conflict" {
			t.Errorf("%s on an asleep task: %v", name, err)
		}
	}
	if _, err := r.Pause("terminal", "nope"); err == nil {
		t.Error("paused a task that does not exist")
	}
}

func TestDriveIgnoresResourcesNoAwakeTaskHolds(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "done"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	if r.Drive("terminal", "other") || r.Drive("browser", "t1") {
		t.Fatal("input for a resource no task holds changed a task")
	}
	if got, _ := r.Get("terminal", view.ID); got.Status != StatusIdle {
		t.Fatalf("status %s", got.Status)
	}
}

func TestDriveTakesAnIdleTaskAndHoldsSteersUntilHandBack(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "finish", map[string]any{"result": "first"})},
		{use("u2", "finish", map[string]any{"result": "after the hand back"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	if !r.Drive("terminal", "t1") {
		t.Fatal("input did not take an idle task")
	}
	if r.Drive("terminal", "t1") {
		t.Fatal("a second input changed the task again")
	}
	held, err := r.Steer("terminal", view.ID, "agent-b", "check the footer")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if held.Status != StatusPaused || len(dwar.requests) != 1 {
		t.Fatalf("steer started work on a task Ankur holds: %s, %d requests", held.Status, len(dwar.requests))
	}
	if _, err := r.Resume("terminal", view.ID, "I fixed the header"); err != nil {
		t.Fatal(err)
	}
	rep := waitReport(t, reports)
	if rep.AgentID != "agent-b" || rep.Text != "after the hand back" {
		t.Fatalf("report %+v", rep)
	}
	turn := fmt.Sprint(dwar.requests[1].Messages[len(dwar.requests[1].Messages)-1].Content)
	for _, want := range []string{"[Hand back from Ankur]", "I fixed the header", "[Steer from agent-b]", "check the footer", "cwd /home"} {
		if !strings.Contains(turn, want) {
			t.Fatalf("hand back turn lacks %q: %s", want, turn)
		}
	}
}

func TestHandBackOfAnIdleOrAskingTaskWithNoSteerKeepsItsStatusAndNote(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "ask", map[string]any{"question": "which account?"})},
		{use("u2", "finish", map[string]any{"result": "used the note"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusAsking)
	if !r.Drive("terminal", "t1") {
		t.Fatal("input did not take an asking task")
	}
	back, err := r.Resume("terminal", view.ID, "the account is the work one")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if back.Status != StatusAsking || len(dwar.requests) != 1 {
		t.Fatalf("hand back with no steer: %s, %d requests", back.Status, len(dwar.requests))
	}
	if _, err := r.Steer("terminal", view.ID, "agent-a", "go on"); err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	turn := fmt.Sprint(dwar.requests[1].Messages[len(dwar.requests[1].Messages)-1].Content)
	for _, want := range []string{"the account is the work one", "go on"} {
		if !strings.Contains(turn, want) {
			t.Fatalf("next turn lacks %q: %s", want, turn)
		}
	}
}

func TestPauseTakesAnIdleTask(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "done"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	paused, err := r.Pause("terminal", view.ID)
	if err != nil || paused.Status != StatusPaused {
		t.Fatalf("pause of an idle task: %+v, %v", paused, err)
	}
	back, err := r.Resume("terminal", view.ID, "")
	if err != nil || back.Status != StatusIdle {
		t.Fatalf("resume of a paused idle task: %+v, %v", back, err)
	}
}

func TestStopOfAnIdleTaskSleepsItAndReportsOnlyDroppedSteers(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "finish", map[string]any{"result": "first"})},
		{use("u2", "finish", map[string]any{"result": "second"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	r.Drive("terminal", "t1")
	stopped, err := r.Stop("terminal", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case rep := <-reports:
		t.Fatalf("stopping a task that had already reported sent %+v", rep)
	case <-time.After(100 * time.Millisecond):
	}
	if stopped.Status != StatusAsleep || kit.sleeps != 1 {
		t.Fatalf("status %s, sleeps %d", stopped.Status, kit.sleeps)
	}
	if _, err := r.Steer("terminal", view.ID, "agent-a", "again"); err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	r.Drive("terminal", "t1")
	if _, err := r.Steer("terminal", view.ID, "agent-b", "while he drove"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Stop("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	rep := waitReport(t, reports)
	if rep.State != "stopped" || rep.AgentID != "agent-b" || !strings.Contains(rep.Text, "1 steer(s)") {
		t.Fatalf("report %+v", rep)
	}
}

func TestAbandonedTakeoverOfAnIdleTaskReturnsItToIdleWithoutAModelTurn(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "done"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	r.Drive("terminal", "t1")
	r.Sweep(time.Now().Add(pauseAbandonAfter + time.Minute))
	if got, _ := r.Get("terminal", view.ID); got.Status != StatusIdle || len(dwar.requests) != 1 {
		t.Fatalf("abandoned takeover: %s, %d requests", got.Status, len(dwar.requests))
	}
}

func TestPausedTaskIsNeverSleptOrForgottenButAbandonedOnesResume(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{hold: make(chan struct{}), script: [][]Block{
		{use("u1", "finish", map[string]any{"result": "first"})},
		{use("u2", "finish", map[string]any{"result": "after the abandoned takeover"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitRequests(t, dwar, 1)
	if _, err := r.Pause("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	dwar.hold <- struct{}{}
	r.Sweep(time.Now().Add(pauseAbandonAfter - time.Minute))
	if got, _ := r.Get("terminal", view.ID); got.Status != StatusPaused || kit.sleeps != 0 {
		t.Fatalf("paused task after %s of quiet: %s, sleeps %d", pauseAbandonAfter-time.Minute, got.Status, kit.sleeps)
	}
	r.Sweep(time.Now().Add(pauseAbandonAfter + time.Minute))
	go func() { dwar.hold <- struct{}{} }()
	if rep := waitReport(t, reports); rep.Text != "after the abandoned takeover" {
		t.Fatalf("report %+v", rep)
	}
}

func TestPausedTaskReloadsAsleepAfterRestart(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{hold: make(chan struct{}), script: [][]Block{{use("u1", "finish", map[string]any{"result": "x"})}}}
	r, _, dir := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitRequests(t, dwar, 1)
	if _, err := r.Pause("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewRunner(dir, dwar.server(t).URL, testBudget, []Kit{kit}, func(Report) {})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reloaded.Get("terminal", view.ID); err != nil || got.Status != StatusAsleep {
		t.Fatalf("reloaded %+v, %v", got, err)
	}
	dwar.hold <- struct{}{}
}

func TestHaltEndsAWorkingTaskAndRefusesLaterSteers(t *testing.T) {
	kit := &fakeKit{gate: make(chan struct{}), started: make(chan struct{}, 4)}
	dwar := &fakeDwar{script: [][]Block{
		{use("u1", "run", map[string]any{"command": "one"})},
		{use("u2", "finish", map[string]any{"result": "should never be reported"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	<-kit.started
	if _, err := r.Steer("terminal", view.ID, "agent-b", "while it works"); err != nil {
		t.Fatal(err)
	}
	halted, err := r.Halt("terminal", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if halted.Status != StatusStopped || kit.halts != 1 || halted.Resource != "" {
		t.Fatalf("status %s, halts %d, resource %q", halted.Status, kit.halts, halted.Resource)
	}
	rep := waitReport(t, reports)
	if rep.State != "stopped" || !strings.Contains(rep.Text, "1 steer(s)") || !strings.Contains(rep.Text, "cwd /home") {
		t.Fatalf("report %+v", rep)
	}
	close(kit.gate)
	time.Sleep(200 * time.Millisecond)
	if got, _ := r.Get("terminal", view.ID); got.Status != StatusStopped {
		t.Fatalf("the loop revived a stopped task: %s", got.Status)
	}
	if len(dwar.requests) != 1 {
		t.Fatalf("the stopped loop took another step: %d requests", len(dwar.requests))
	}
	_, err = r.Steer("terminal", view.ID, "agent-a", "are you there")
	var e Error
	if !errors.As(err, &e) || e.Status != http.StatusConflict || e.Code != "task_stopped" {
		t.Fatalf("steer of a stopped task: %v", err)
	}
	if kit.wakes != 1 {
		t.Fatalf("a steer woke a stopped task: %d wakes", kit.wakes)
	}
}

func TestHaltOfAnIdleOrStoppedTaskIsQuietAndRepeatable(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "x"})}}}
	r, reports, dir := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	for i := 0; i < 2; i++ {
		halted, err := r.Halt("terminal", view.ID)
		if err != nil || halted.Status != StatusStopped {
			t.Fatalf("halt %d: %+v %v", i, halted, err)
		}
	}
	select {
	case rep := <-reports:
		t.Fatalf("an idle task's halt reported %+v", rep)
	default:
	}
	if kit.halts != 1 {
		t.Fatalf("halts %d: the repeat killed a resource the task no longer holds", kit.halts)
	}
	reloaded, err := NewRunner(dir, dwar.server(t).URL, testBudget, []Kit{kit}, func(Report) {})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reloaded.Get("terminal", view.ID); err != nil || got.Status != StatusStopped {
		t.Fatalf("stopped task after restart: %+v %v", got, err)
	}
}

func TestHaltRouteAnswersTaskStoppedOnSteer(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "x"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	mux := http.NewServeMux()
	r.Register(mux, func(w http.ResponseWriter, _ *http.Request, status int, code, msg string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": code, "message": msg}})
	}, func(w http.ResponseWriter, status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	})
	post := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec
	}
	if rec := post("/tasks/terminal/"+view.ID+"/halt", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"stopped"`) {
		t.Fatalf("halt %d %s", rec.Code, rec.Body)
	}
	rec := post("/tasks/terminal/"+view.ID+"/steer", `{"agent_id":"agent-a","message":"hi"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"type":"task_stopped"`) {
		t.Fatalf("steer %d %s", rec.Code, rec.Body)
	}
	if rec := post("/tasks/terminal/nope/halt", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("halt of an unknown task %d", rec.Code)
	}
}

type frontKit struct{ fakeKit }

func (k *frontKit) FrontURL(t *Task) string { return "https://front.example" }

func TestViewCarriesTheFrontPageOfAKitThatHasOne(t *testing.T) {
	kit := &frontKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "ok"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "job", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	got, err := r.Get("terminal", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActiveURL != "https://front.example" {
		t.Fatalf("ActiveURL %q", got.ActiveURL)
	}
}

// TestStepBudgetTellsTheHelperWhenNoTurnsAreLeft checks that the finish-only request carries
// the instruction to report what was read.
func TestStepBudgetTellsTheHelperWhenNoTurnsAreLeft(t *testing.T) {
	kit := &fakeKit{}
	var script [][]Block
	for i := 1; i <= testBudget.Max; i++ {
		script = append(script, []Block{use(fmt.Sprintf("u%d", i), "run", map[string]any{"command": "ls"})})
	}
	script = append(script, []Block{use("fin", "finish", map[string]any{"result": "partial"})})
	dwar := &fakeDwar{script: script}
	r, reports, _ := newTestRunner(t, kit, dwar)
	if _, err := r.Start("terminal", "agent-a", "list", ""); err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	last := fmt.Sprint(dwar.requests[testBudget.Max].Messages)
	if !strings.Contains(last, "No turns left") || strings.Contains(fmt.Sprint(dwar.requests[testBudget.Max-1].Messages), "No turns left") {
		t.Fatal("the out-of-turns instruction is missing on the finish-only request or came early")
	}
}

// TestFinishOverTheLimitIsRefusedOnce sends back a long report once and takes the next one
// whatever its length.
func TestFinishOverTheLimitIsRefusedOnce(t *testing.T) {
	long := strings.Repeat("x", reportRefuseAt+1)
	dwar := &fakeDwar{script: [][]Block{
		{use("f1", "finish", map[string]any{"result": long})},
		{use("f2", "finish", map[string]any{"result": long})},
	}}
	r, reports, _ := newTestRunner(t, &fakeKit{}, dwar)
	if _, err := r.Start("terminal", "agent-a", "list", ""); err != nil {
		t.Fatal(err)
	}
	if rep := waitReport(t, reports); rep.State != "done" || rep.Text != long {
		t.Fatalf("report %q", rep.Text)
	}
	if len(dwar.requests) != 2 || !strings.Contains(fmt.Sprint(dwar.requests[1].Messages), "[too long]") {
		t.Fatalf("%d requests; the refusal did not reach the helper", len(dwar.requests))
	}
	short := &fakeDwar{script: [][]Block{{use("f", "finish", map[string]any{"result": strings.Repeat("y", reportRefuseAt)})}}}
	r2, reports2, _ := newTestRunner(t, &fakeKit{}, short)
	if _, err := r2.Start("terminal", "agent-a", "list", ""); err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports2)
	if len(short.requests) != 1 {
		t.Fatalf("a report at the limit was refused: %d requests", len(short.requests))
	}
}

// TestEarlierSteersReachTheModelAsTheirReports keeps the newest steer whole and reduces each
// finished one before it to its steer and its report.
func TestEarlierSteersReachTheModelAsTheirReports(t *testing.T) {
	steer := func(n string, withResult bool) Message {
		content := []Block{}
		if withResult {
			content = append(content, Block{"type": "tool_result", "tool_use_id": "fin" + n, "content": "delivered"})
		}
		return Message{Role: "user", Content: append(content, Block{"type": "text", "text": "[Steer from a]\nsteer " + n})}
	}
	turn := func(id, name string, in map[string]any) []Message {
		return []Message{
			{Role: "assistant", Content: []Block{use(id, name, in)}},
			{Role: "user", Content: []Block{{"type": "tool_result", "tool_use_id": id, "content": "page " + id}}},
		}
	}
	h := []Message{steer("1", false)}
	h = append(h, turn("u1", "look", nil)...)
	h = append(h, Message{Role: "assistant", Content: []Block{use("fin1", "finish", map[string]any{"result": "report one"})}})
	h = append(h, steer("2", true))
	h = append(h, turn("u2", "look", nil)...)
	got := compactSteers(h)
	text := fmt.Sprint(got)
	if strings.Contains(text, "page u1") || !strings.Contains(text, "report one") || !strings.Contains(text, "page u2") {
		t.Fatalf("history %v", got)
	}
	if len(got) != 5 || got[0].Role != "user" || got[1].Role != "assistant" || got[2].Role != "user" {
		t.Fatalf("shape %v", got)
	}
	for _, b := range got[2].Content {
		if b["type"] == "tool_result" {
			t.Fatal("the new steer still answers a call that was compacted away")
		}
	}
	if single := compactSteers(h[:3]); len(single) != 3 {
		t.Fatal("a single steer was changed")
	}
}
