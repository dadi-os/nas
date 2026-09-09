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
	host, err := newHostRuntime(state)
	if err != nil {
		t.Fatal(err)
	}
	// Short isolated socket (macOS AF_UNIX path limit).
	host.runDir = filepath.Join(os.TempDir(), fmt.Sprintf("nas-t-%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000))
	host.tmuxSocket = filepath.Join(host.runDir, "tmux.sock")
	if err := os.MkdirAll(host.runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	th := newTerminalHost(host)
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
	// Wait for shell to recover after interrupt.
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
