package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
}

func testTerminalEnv(t *testing.T) (*terminalHost, *http.ServeMux) {
	t.Helper()
	requireTmux(t)
	dir := t.TempDir()
	state := stateConfig{dir: dir, runtime: "compose"}
	host, err := newHostRuntime(state, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host.runDir = filepath.Join(os.TempDir(), fmt.Sprintf("nas-t-%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000))
	host.tmuxSocket = filepath.Join(host.runDir, "tmux.sock")
	if err := os.MkdirAll(host.runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	th := newTerminalHost(host, 3*time.Second)
	mux := http.NewServeMux()
	th.register(mux)
	t.Cleanup(func() {
		names, _ := th.listSessionNames()
		for id := range names {
			_ = th.tmuxCmd("kill-session", "-t", id).Run()
		}
		_ = os.RemoveAll(host.runDir)
	})
	return th, mux
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestTerminalsCreateListKillReuse(t *testing.T) {
	_, mux := testTerminalEnv(t)

	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create1 status %d body %s", code, body)
	}
	var t1 createTerminalResponse
	if err := json.Unmarshal(body, &t1); err != nil {
		t.Fatal(err)
	}
	if t1.ID != "t1" {
		t.Fatalf("want t1 got %s", t1.ID)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create2 status %d body %s", code, body)
	}
	var t2 createTerminalResponse
	if err := json.Unmarshal(body, &t2); err != nil {
		t.Fatal(err)
	}
	if t2.ID != "t2" {
		t.Fatalf("want t2 got %s", t2.ID)
	}

	code, body = doJSON(t, mux, http.MethodGet, "/terminals", nil)
	if code != http.StatusOK {
		t.Fatalf("list status %d", code)
	}
	var list []terminalInfo
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len %d", len(list))
	}

	code, _ = doJSON(t, mux, http.MethodDelete, "/terminals/t1", nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete status %d", code)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("recreate status %d body %s", code, body)
	}
	var tReuse createTerminalResponse
	if err := json.Unmarshal(body, &tReuse); err != nil {
		t.Fatal(err)
	}
	if tReuse.ID != "t1" {
		t.Fatalf("want reuse t1 got %s", tReuse.ID)
	}
}

func TestTerminalsCreateByID(t *testing.T) {
	_, mux := testTerminalEnv(t)

	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{"id": "t3"})
	if code != http.StatusOK {
		t.Fatalf("create t3 %d %s", code, body)
	}
	var created createTerminalResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "t3" {
		t.Fatalf("want t3 got %s", created.ID)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("fresh %d %s", code, body)
	}
	var fresh createTerminalResponse
	if err := json.Unmarshal(body, &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.ID != "t1" {
		t.Fatalf("want lowest vacant t1 got %s", fresh.ID)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{"id": "t3"})
	if code != http.StatusConflict {
		t.Fatalf("running id status %d %s", code, body)
	}
	var conflict errorResponse
	if err := json.Unmarshal(body, &conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Error.Type != CodeConflict {
		t.Fatalf("conflict type %s", conflict.Error.Type)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{"id": "term"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad id status %d %s", code, body)
	}
}

func TestTerminalsExecExitAndOutput(t *testing.T) {
	_, mux := testTerminalEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createTerminalResponse
	_ = json.Unmarshal(body, &created)

	code, body = doJSON(t, mux, http.MethodPost, "/terminals/"+created.ID+"/exec", map[string]any{
		"command":         "printf 'a\\nb\\nc\\n'; (exit 42)",
		"timeout_seconds": 30,
	})
	if code != http.StatusOK {
		t.Fatalf("exec %d %s", code, body)
	}
	var resp execResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TimedOut {
		t.Fatal("unexpected timeout")
	}
	if resp.ExitCode == nil || *resp.ExitCode != 42 {
		t.Fatalf("exit_code %+v", resp.ExitCode)
	}
	if !strings.Contains(resp.Output, "a") || !strings.Contains(resp.Output, "b") || !strings.Contains(resp.Output, "c") {
		t.Fatalf("output %q", resp.Output)
	}
}

func TestTerminalsExecCannotEndSession(t *testing.T) {
	_, mux := testTerminalEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createTerminalResponse
	_ = json.Unmarshal(body, &created)

	for _, tc := range []struct {
		command string
		exit    int
	}{
		{"set -e\nfalse\necho unreachable", 1},
		{"exit 7", 7},
		{"cd / && exec true", 0},
		{"pwd # trailing comment", 0},
	} {
		code, body = doJSON(t, mux, http.MethodPost, "/terminals/"+created.ID+"/exec", map[string]any{
			"command":         tc.command,
			"timeout_seconds": 30,
		})
		if code != http.StatusOK {
			t.Fatalf("exec %q %d %s", tc.command, code, body)
		}
		var resp execResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatal(err)
		}
		if resp.TimedOut || resp.ExitCode == nil || *resp.ExitCode != tc.exit {
			t.Fatalf("exec %q: timed_out %v exit_code %+v output %q", tc.command, resp.TimedOut, resp.ExitCode, resp.Output)
		}
		if strings.Contains(resp.Output, "unreachable") {
			t.Fatalf("exec %q kept going after set -e: %q", tc.command, resp.Output)
		}
		if tc.command == "pwd # trailing comment" && strings.TrimSpace(resp.Output) == "/" {
			t.Fatalf("cd from an earlier exec leaked into the session: %q", resp.Output)
		}
	}
}

func TestTerminalsExecTimeoutThenKeys(t *testing.T) {
	_, mux := testTerminalEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createTerminalResponse
	_ = json.Unmarshal(body, &created)

	code, body = doJSON(t, mux, http.MethodPost, "/terminals/"+created.ID+"/exec", map[string]any{
		"command":         "sleep 5",
		"timeout_seconds": 1,
	})
	if code != http.StatusOK {
		t.Fatalf("exec %d %s", code, body)
	}
	var resp execResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.TimedOut {
		t.Fatalf("expected timed_out: %s", body)
	}
	if resp.ExitCode != nil {
		t.Fatalf("exit_code should be null: %+v", resp.ExitCode)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/terminals/"+created.ID+"/keys", map[string]any{
		"keys": []string{"C-c"},
	})
	if code != http.StatusOK {
		t.Fatalf("keys %d %s", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body = doJSON(t, mux, http.MethodPost, "/terminals/"+created.ID+"/exec", map[string]any{
			"command":         "echo recovered",
			"timeout_seconds": 10,
		})
		if code == http.StatusOK {
			break
		}
		if code == http.StatusConflict && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		t.Fatalf("follow-up exec %d %s", code, body)
	}
	var follow execResponse
	if err := json.Unmarshal(body, &follow); err != nil {
		t.Fatal(err)
	}
	if follow.TimedOut || follow.ExitCode == nil || *follow.ExitCode != 0 {
		t.Fatalf("follow-up %+v body %s", follow, body)
	}
	if !strings.Contains(follow.Output, "recovered") {
		t.Fatalf("output %q", follow.Output)
	}
}

func TestTerminalsExecBusy(t *testing.T) {
	_, mux := testTerminalEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createTerminalResponse
	_ = json.Unmarshal(body, &created)

	done := make(chan int, 1)
	go func() {
		c, _ := doJSON(t, mux, http.MethodPost, "/terminals/"+created.ID+"/exec", map[string]any{
			"command":         "sleep 2",
			"timeout_seconds": 5,
		})
		done <- c
	}()
	time.Sleep(300 * time.Millisecond)
	code, body = doJSON(t, mux, http.MethodPost, "/terminals/"+created.ID+"/exec", map[string]any{
		"command": "echo no",
	})
	if code != http.StatusConflict {
		t.Fatalf("expected 409 got %d %s", code, body)
	}
	var errBody errorResponse
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Type != CodeBusy {
		t.Fatalf("code %s", errBody.Error.Type)
	}
	if c := <-done; c != http.StatusOK {
		t.Fatalf("first exec status %d", c)
	}
}

func TestTruncateHeadTail(t *testing.T) {
	s := strings.Repeat("a", 100) + strings.Repeat("b", 100)
	out, trunc := truncateHeadTail(s, 50)
	if !trunc {
		t.Fatal("expected truncated")
	}
	if !strings.Contains(out, "bytes elided") {
		t.Fatalf("missing note: %q", out)
	}
	if !strings.HasPrefix(out, "aaa") || !strings.HasSuffix(out, "bbb") {
		t.Fatalf("head/tail: %q", out)
	}
	if len(out) > 50 {
		t.Fatalf("len %d", len(out))
	}
}

func TestTerminalsExecTruncation(t *testing.T) {
	_, mux := testTerminalEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createTerminalResponse
	_ = json.Unmarshal(body, &created)

	code, body = doJSON(t, mux, http.MethodPost, "/terminals/"+created.ID+"/exec", map[string]any{
		"command":         "awk 'BEGIN{for(i=0;i<2000;i++)printf \"X\"; print \"\"}'",
		"timeout_seconds": 30,
		"max_bytes":       200,
	})
	if code != http.StatusOK {
		t.Fatalf("exec %d %s", code, body)
	}
	var resp execResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Truncated {
		t.Fatalf("expected truncated: %s", body)
	}
	if !strings.Contains(resp.Output, "bytes elided") {
		t.Fatalf("missing elision note: %q", resp.Output)
	}
}

func TestParseExecCaptureScrubsMarker(t *testing.T) {
	sent := "echo hi; printf '\\n" + execMarker + "%s %d\\n' 'abc' $?"
	capture := strings.Join([]string{
		sent,
		"hi",
		"wrap " + execMarker + " fragment",
		execMarker + "abc 0",
	}, "\n")
	code, out, ok := parseExecCapture(capture, sent, execMarker+"abc ")
	if !ok {
		t.Fatal("expected parse ok")
	}
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if strings.Contains(out, execMarker) {
		t.Fatalf("marker leaked: %q", out)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("missing output: %q", out)
	}

	partial := partialExecOutput(capture, sent)
	if strings.Contains(partial, execMarker) {
		t.Fatalf("partial marker leaked: %q", partial)
	}
}

func TestParseExecCaptureIgnoresEarlierCommandsWhenTheEchoWraps(t *testing.T) {
	older := "( eval 'head -n 1 f.csv' ); printf '\\n" + execMarker + "%s %d\\n' 'old' $?"
	sent := "( eval 'awk -F, f.csv' ); printf '\\n" + execMarker + "%s %d\\n' 'new' $?"
	capture := strings.Join([]string{
		"[dadi@os ~]$ " + older,
		"STATION,DATE,HEADER",
		execMarker + "old 0",
		"[dadi@os ~]$ ( eval 'awk -F, f.csv' ); printf '\\n" + execMarker,
		"%s %d\\n' 'new' $?",
		"12-18,43.5",
		execMarker + "new 0",
	}, "\n")
	code, out, ok := parseExecCapture(capture, sent, execMarker+"new ")
	if !ok || code != 0 {
		t.Fatalf("parse ok=%v code=%d", ok, code)
	}
	if strings.Contains(out, "STATION,DATE,HEADER") {
		t.Fatalf("earlier command's output leaked: %q", out)
	}
	if !strings.Contains(out, "12-18,43.5") {
		t.Fatalf("missing this command's output: %q", out)
	}
}

func TestTerminalsDeleteIdempotent(t *testing.T) {
	_, mux := testTerminalEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createTerminalResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	code, _ = doJSON(t, mux, http.MethodDelete, "/terminals/"+created.ID, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete %d", code)
	}
	code, _ = doJSON(t, mux, http.MethodDelete, "/terminals/"+created.ID, nil)
	if code != http.StatusNoContent {
		t.Fatalf("second delete %d", code)
	}
}

func TestTerminalKeysLiteralTypesTextAndKeepsKeyNamesOtherwise(t *testing.T) {
	_, mux := testTerminalEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createTerminalResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	keys := "/terminals/" + created.ID + "/keys"
	if code, body = doJSON(t, mux, http.MethodPost, keys, map[string]any{"keys": []string{"echo Enter -la"}, "literal": true}); code != http.StatusOK {
		t.Fatalf("literal keys %d %s", code, body)
	}
	if code, body = doJSON(t, mux, http.MethodPost, keys, map[string]any{"keys": []string{"Enter"}}); code != http.StatusOK {
		t.Fatalf("named key %d %s", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body = doJSON(t, mux, http.MethodGet, "/terminals/"+created.ID+"/capture?lines=20", nil)
		if code != http.StatusOK {
			t.Fatalf("capture %d %s", code, body)
		}
		if strings.Count(string(body), "Enter -la") >= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the typed text did not run as a command and print itself: %s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestTmuxCmdSlice checks that on the appliance only new-session, which can start the tmux
// server, runs in dadi-terminals.slice, and that elsewhere tmux runs directly.
func TestTmuxCmdSlice(t *testing.T) {
	host := &hostRuntime{tmuxSocket: "/run/dadi/tmux.sock", switchUser: true}
	th := newTerminalHost(host, 3*time.Second)
	create := strings.Join(th.tmuxCmd("new-session", "-d", "-s", "t1").Args, " ")
	if !strings.HasPrefix(create, "systemd-run --scope") || !strings.Contains(create, "--slice=dadi-terminals.slice") || !strings.Contains(create, "runuser -u dadi -- tmux -S /run/dadi/tmux.sock new-session") {
		t.Fatalf("new-session argv: %s", create)
	}
	list := strings.Join(th.tmuxCmd("ls").Args, " ")
	if strings.Contains(list, "systemd-run") || !strings.HasPrefix(list, "runuser -u dadi -- tmux") {
		t.Fatalf("ls argv: %s", list)
	}
	host.switchUser = false
	if got := strings.Join(th.tmuxCmd("new-session").Args, " "); got != "tmux -S /run/dadi/tmux.sock new-session" {
		t.Fatalf("dev argv: %s", got)
	}
}

// execJSON runs command in terminal id through the exec route and returns its response.
func execJSON(t *testing.T, mux *http.ServeMux, id, command string, timeout int) execResponse {
	t.Helper()
	code, body := doJSON(t, mux, http.MethodPost, "/terminals/"+id+"/exec", map[string]any{"command": command, "timeout_seconds": timeout})
	if code != http.StatusOK {
		t.Fatalf("exec %q: %d %s", command, code, body)
	}
	var resp execResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func createTerminal(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	code, body := doJSON(t, mux, http.MethodPost, "/terminals", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createTerminalResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

// TestExecTimeoutKillsTheCommandAndLeavesAUsableShell checks that a command outliving its
// timeout is killed rather than left holding the pane, so the next exec is not queued behind it.
func TestExecTimeoutKillsTheCommandAndLeavesAUsableShell(t *testing.T) {
	_, mux := testTerminalEnv(t)
	id := createTerminal(t, mux)

	started := time.Now()
	hung := execJSON(t, mux, id, "sleep 120", 1)
	if !hung.TimedOut || !hung.Killed || hung.ShellEnded || hung.ExitCode != nil {
		t.Fatalf("hung exec: %+v", hung)
	}
	if time.Since(started) > 8*time.Second {
		t.Fatalf("a 1s timeout took %s", time.Since(started))
	}
	next := execJSON(t, mux, id, "echo alive", 10)
	if next.TimedOut || next.ExitCode == nil || *next.ExitCode != 0 || !strings.Contains(next.Output, "alive") {
		t.Fatalf("shell unusable after the kill: %+v", next)
	}
}

// TestExecClosesStdinSoACommandCannotSwallowTheNextLine checks that a command reading stdin
// sees end of file instead of consuming the exec marker typed after it.
func TestExecClosesStdinSoACommandCannotSwallowTheNextLine(t *testing.T) {
	_, mux := testTerminalEnv(t)
	id := createTerminal(t, mux)
	resp := execJSON(t, mux, id, "cat; echo after-cat", 10)
	if resp.TimedOut || resp.ExitCode == nil || *resp.ExitCode != 0 || !strings.Contains(resp.Output, "after-cat") {
		t.Fatalf("cat blocked on the terminal: %+v", resp)
	}
}

// TestPkillMatchingTheCommandTextDoesNotKillTheShell checks that a command naming its own text
// in pkill -f does not end the shell: the command text is not in any process's argv.
func TestPkillMatchingTheCommandTextDoesNotKillTheShell(t *testing.T) {
	_, mux := testTerminalEnv(t)
	id := createTerminal(t, mux)
	execJSON(t, mux, id, "pkill -f no_such_fetch_script_9917.py; true", 10)
	next := execJSON(t, mux, id, "echo alive", 10)
	if next.ExitCode == nil || !strings.Contains(next.Output, "alive") {
		t.Fatalf("shell dead after pkill -f: %+v", next)
	}
}

// TestDeleteWithTreeKillsBackgroundJobsThatSurviveAClosedSession checks that tree=true kills
// the process a nohup'd job started, and that a plain delete leaves it running.
func TestDeleteWithTreeKillsBackgroundJobsThatSurviveAClosedSession(t *testing.T) {
	_, mux := testTerminalEnv(t)
	alive := func(marker string) bool {
		return exec.Command("pgrep", "-f", marker).Run() == nil
	}
	plain, tree := "sleep 30511", "sleep 30512"
	t.Cleanup(func() {
		_ = exec.Command("pkill", "-f", plain).Run()
		_ = exec.Command("pkill", "-f", tree).Run()
	})

	for _, tc := range []struct {
		command string
		tree    bool
	}{{plain, false}, {tree, true}} {
		id := createTerminal(t, mux)
		execJSON(t, mux, id, "nohup "+tc.command+" >/dev/null 2>&1 &", 10)
		path := "/terminals/" + id
		if tc.tree {
			path += "?tree=true"
		}
		if code, body := doJSON(t, mux, http.MethodDelete, path, nil); code != http.StatusNoContent {
			t.Fatalf("delete %d %s", code, body)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if !alive(plain) {
		t.Fatal("a plain delete killed the nohup job")
	}
	if alive(tree) {
		t.Fatal("tree=true left the job running")
	}
}
