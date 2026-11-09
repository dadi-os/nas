package helpers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func rowAt(typ, agent string) *stateRow {
	return &stateRow{head: head{Type: typ, Agent: agent}, Status: "idle", Reason: "x"}
}

func seqs(t *testing.T, entries []json.RawMessage) []int64 {
	t.Helper()
	var out []int64
	for _, e := range entries {
		var row struct {
			Seq int64 `json:"seq"`
		}
		if err := json.Unmarshal(e, &row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row.Seq)
	}
	return out
}

func TestJournalPagesByAfterSeqAndLimitWithNextSeq(t *testing.T) {
	j, err := newJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := j.append("terminal", "a1", rowAt(entryState, "ag")); err != nil {
			t.Fatal(err)
		}
	}
	entries, next, exists, err := j.read("terminal", "a1", 0, 2)
	if err != nil || !exists || len(entries) != 2 || next == nil || *next != 2 {
		t.Fatalf("page 1: %v %v %v %v", seqs(t, entries), next, exists, err)
	}
	entries, next, _, _ = j.read("terminal", "a1", 2, 3)
	if got := seqs(t, entries); len(got) != 3 || got[0] != 3 || got[2] != 5 || next != nil {
		t.Fatalf("page 2: %v %v", got, next)
	}
	entries, next, _, _ = j.read("terminal", "a1", 5, 10)
	if len(entries) != 0 || next != nil {
		t.Fatalf("past the end: %v %v", entries, next)
	}
	if _, _, exists, _ := j.read("terminal", "nope", 0, 10); exists {
		t.Fatal("a task with no log exists")
	}
	if _, _, exists, _ := j.read("../x", "y", 0, 10); exists {
		t.Fatal("a path-like kind exists")
	}
}

func TestJournalSeqContinuesAfterARestart(t *testing.T) {
	dir := t.TempDir()
	j, _ := newJournal(dir)
	for i := 0; i < 3; i++ {
		if err := j.append("terminal", "a1", rowAt(entryState, "ag")); err != nil {
			t.Fatal(err)
		}
	}
	again, _ := newJournal(dir)
	if err := again.append("terminal", "a1", rowAt(entryState, "ag")); err != nil {
		t.Fatal(err)
	}
	entries, _, _, _ := again.read("terminal", "a1", 0, 10)
	if got := seqs(t, entries); len(got) != 4 || got[3] != 4 {
		t.Fatalf("seqs %v", got)
	}
}

func TestClipNeverSplitsARuneAndMarksTheCut(t *testing.T) {
	s := strings.Repeat("日本語", 10)
	got := clipRunes(s, 7)
	if !utf8.ValidString(got) || got != strings.Repeat("日本語", 2)+"日…" {
		t.Fatalf("clip %q", got)
	}
	if clipRunes("short", 10) != "short" {
		t.Fatal("short text was changed")
	}
}

func TestUsageSumsRowsSinceAndOrdersByLastAt(t *testing.T) {
	j, _ := newJournal(t.TempDir())
	turn := func(id, agent string, in int) {
		row := &turnRow{head: head{Type: entryTurn, Agent: agent}, Usage: Usage{InputTokens: in, OutputTokens: 1, CacheReadInputTokens: 2, CacheCreationInputTokens: 3}}
		if err := j.append("terminal", id, row); err != nil {
			t.Fatal(err)
		}
	}
	turn("old00000", "a", 100)
	time.Sleep(20 * time.Millisecond)
	cut := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	turn("old00000", "b", 10)
	_ = j.append("terminal", "old00000", &resultRow{head: head{Type: entryResult, Agent: "b"}})
	turn("new00000", "c", 7)
	got, err := j.usage(cut)
	if err != nil || len(got) != 2 {
		t.Fatalf("usage %v %v", got, err)
	}
	if got[0].Task != "task/terminal/new00000" || got[0].Owner != "c" || got[0].InputTokens != 7 {
		t.Fatalf("first %+v", got[0])
	}
	o := got[1]
	if o.Calls != 1 || o.Steps != 1 || o.InputTokens != 10 || o.OutputTokens != 1 || o.CacheReadInputTokens != 2 || o.CacheCreationInputTokens != 3 || o.Owner != "b" || o.Kind != "terminal" {
		t.Fatalf("old %+v", o)
	}
	if none, _ := j.usage(time.Now().Add(time.Hour)); len(none) != 0 {
		t.Fatalf("future since %v", none)
	}
}

func TestPruneRemovesOnlyLogsPastRetentionAndRunsHourly(t *testing.T) {
	dir := t.TempDir()
	j, _ := newJournal(dir)
	_ = j.append("terminal", "stale000", rowAt(entryState, "a"))
	_ = j.append("terminal", "fresh000", rowAt(entryState, "a"))
	old := time.Now().Add(-logRetention - time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "terminal-stale000.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := j.pruneDue(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, exists, _ := j.read("terminal", "stale000", 0, 1); exists {
		t.Fatal("stale log kept")
	}
	if _, _, exists, _ := j.read("terminal", "fresh000", 0, 1); !exists {
		t.Fatal("fresh log removed")
	}
	_ = os.Chtimes(filepath.Join(dir, "terminal-fresh000.jsonl"), old, old)
	_ = j.pruneDue(time.Now())
	if _, _, exists, _ := j.read("terminal", "fresh000", 0, 1); !exists {
		t.Fatal("prune ran twice within the hour")
	}
	_ = j.pruneDue(time.Now().Add(2 * time.Hour))
	if _, _, exists, _ := j.read("terminal", "fresh000", 0, 1); exists {
		t.Fatal("prune did not run after the hour")
	}
}

func logMux(r *Runner) *http.ServeMux {
	mux := http.NewServeMux()
	r.Register(mux, func(w http.ResponseWriter, _ *http.Request, status int, code, msg string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": code, "message": msg}})
	}, func(w http.ResponseWriter, status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	})
	return mux
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

type logBody struct {
	Task    string            `json:"task"`
	Entries []json.RawMessage `json:"entries"`
	NextSeq *int64            `json:"next_seq"`
}

func TestRunLoopLogsSteerTurnResultAndReportAndSurvivesForget(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{usage: Usage{InputTokens: 11, OutputTokens: 5, CacheReadInputTokens: 3, CacheCreationInputTokens: 2}, script: [][]Block{
		{{"type": "thinking", "thinking": "hmm"}, {"type": "text", "text": "running"}, use("u1", "run", map[string]any{"cmd": "ls"})},
		{use("u2", "finish", map[string]any{"result": "done it"})},
	}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, err := r.Start("terminal", "agent-a", "list files", "")
	if err != nil {
		t.Fatal(err)
	}
	waitReport(t, reports)
	mux := logMux(r)
	rec := get(mux, "/tasks/terminal/"+view.ID+"/log")
	var body logBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("log %d %s", rec.Code, rec.Body)
	}
	var types []string
	rows := map[string]map[string]any{}
	for _, e := range body.Entries {
		var row map[string]any
		_ = json.Unmarshal(e, &row)
		typ := row["type"].(string)
		types = append(types, typ)
		if _, seen := rows[typ]; !seen {
			rows[typ] = row
		}
		if row["agent"] != "agent-a" {
			t.Fatalf("agent %v", row["agent"])
		}
		if _, err := time.Parse(time.RFC3339Nano, row["at"].(string)); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(types, ","); got != "state,steer,turn,result,turn,report" {
		t.Fatalf("types %s", got)
	}
	if body.Task != "task/terminal/"+view.ID || body.NextSeq != nil {
		t.Fatalf("body %+v", body)
	}
	if s := rows["steer"]; s["start"] != true || s["held"] != false || s["message"] != "list files" || s["from"] != "agent-a" {
		t.Fatalf("steer %v", s)
	}
	turn := rows["turn"]
	calls := turn["calls"].([]any)
	call := calls[0].(map[string]any)
	if turn["thinking"] != "hmm" || turn["text"] != "running" || turn["stop_reason"] != "tool_use" || call["name"] != "run" || call["input"] != `{"cmd":"ls"}` {
		t.Fatalf("turn %v", turn)
	}
	if u := turn["usage"].(map[string]any); u["input_tokens"] != float64(11) || u["cache_creation_input_tokens"] != float64(2) {
		t.Fatalf("usage %v", u)
	}
	if res := rows["result"]; res["tool_use_id"] != "u1" || res["is_error"] != false || res["head"] != `ok {"cmd":"ls"}` || res["chars"] != float64(len(`ok {"cmd":"ls"}`)) {
		t.Fatalf("result %v", res)
	}
	if rep := rows["report"]; rep["state"] != "done" || rep["text"] != "done it" {
		t.Fatalf("report %v", rep)
	}

	usage := get(mux, "/tasks/usage?since="+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	var ub struct {
		Tasks []TaskUsage `json:"tasks"`
	}
	if err := json.Unmarshal(usage.Body.Bytes(), &ub); err != nil || len(ub.Tasks) != 1 || ub.Tasks[0].Calls != 2 || ub.Tasks[0].Steps != 1 || ub.Tasks[0].InputTokens != 22 || ub.Tasks[0].Owner != "agent-a" {
		t.Fatalf("usage %d %s", usage.Code, usage.Body)
	}

	r.Sweep(time.Now().Add(forgetAfter + time.Hour))
	if _, err := r.Get("terminal", view.ID); err == nil {
		t.Fatal("task not forgotten")
	}
	rec = get(mux, "/tasks/terminal/"+view.ID+"/log?after_seq=6")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"forgotten"`) || !strings.Contains(rec.Body.String(), `"next_seq":null`) {
		t.Fatalf("forgotten log %d %s", rec.Code, rec.Body)
	}
}

func TestStateRowsRecordPauseResumeStopAndHalt(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "x"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, _ := r.Start("terminal", "agent-a", "job", "")
	waitReport(t, reports)
	if _, err := r.Pause("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume("terminal", view.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Pause("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Stop("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Steer("terminal", view.ID, "agent-a", "again"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, "terminal", view.ID, StatusIdle)
	if _, err := r.Halt("terminal", view.ID); err != nil {
		t.Fatal(err)
	}
	entries, _, _, _ := r.journal.read("terminal", view.ID, 0, 100)
	var reasons []string
	for _, e := range entries {
		var row struct{ Type, Status, Reason string }
		_ = json.Unmarshal(e, &row)
		if row.Type == entryState {
			reasons = append(reasons, row.Reason+":"+row.Status)
		}
	}
	want := "wake:idle,ankur took over:paused,resume:idle,ankur took over:paused,stop:asleep,wake:idle,halt:stopped"
	if got := strings.Join(reasons, ","); got != want {
		t.Fatalf("states %s", got)
	}
}

func TestLogDirIsNotLoadedAsATask(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "x"})}}}
	r, reports, dir := newTestRunner(t, kit, dwar)
	view, _ := r.Start("terminal", "agent-a", "job", "")
	waitReport(t, reports)
	if _, err := os.Stat(filepath.Join(dir, "log", "terminal-"+view.ID+".jsonl")); err != nil {
		t.Fatal(err)
	}
	again, err := NewRunner(dir, "http://unused", []Kit{kit}, func(Report) {})
	if err != nil {
		t.Fatal(err)
	}
	if got := again.List(); len(got) != 1 || got[0].ID != view.ID {
		t.Fatalf("loaded %v", got)
	}
}

func TestLogAndUsageRoutesCoexistWithTaskRoutesAndRefuseBadInput(t *testing.T) {
	kit := &fakeKit{}
	dwar := &fakeDwar{script: [][]Block{{use("u1", "finish", map[string]any{"result": "x"})}}}
	r, reports, _ := newTestRunner(t, kit, dwar)
	view, _ := r.Start("terminal", "agent-a", "job", "")
	waitReport(t, reports)
	mux := logMux(r)
	if rec := get(mux, "/tasks/terminal/"+view.ID); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status"`) {
		t.Fatalf("task view %d %s", rec.Code, rec.Body)
	}
	if rec := get(mux, "/tasks/usage?since=2020-01-01T00:00:00Z"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"tasks":[`) {
		t.Fatalf("usage %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/tasks/usage", "/tasks/usage?since=yesterday", "/tasks/terminal/" + view.ID + "/log?limit=0", "/tasks/terminal/" + view.ID + "/log?limit=1001", "/tasks/terminal/" + view.ID + "/log?after_seq=-1", "/tasks/terminal/" + view.ID + "/log?limit=x"} {
		if rec := get(mux, path); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"type":"invalid_request"`) {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := get(mux, "/tasks/terminal/nope0000/log"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"type":"not_found"`) {
		t.Fatalf("missing log %d %s", rec.Code, rec.Body)
	}
	if rec := get(mux, "/tasks/terminal/"+view.ID+"/log?limit=2"); !strings.Contains(rec.Body.String(), `"next_seq":2`) {
		t.Fatalf("paged log %s", rec.Body)
	}
}
