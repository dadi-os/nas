package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testFSEnv(t *testing.T) (*fsHost, *http.ServeMux, string) {
	t.Helper()
	dir := t.TempDir()
	state := stateConfig{dir: dir, runtime: "compose"}
	host, err := newHostRuntime(state)
	if err != nil {
		t.Fatal(err)
	}
	fh := newFSHost(host)
	mux := http.NewServeMux()
	fh.register(mux)
	return fh, mux, host.stateDir
}

func TestFSReadOffsetLimit(t *testing.T) {
	_, mux, root := testFSEnv(t)
	path := filepath.Join(root, "sample.txt")
	content := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	code, body := doJSON(t, mux, http.MethodPost, "/fs/read", map[string]any{
		"path":   path,
		"offset": 2,
		"limit":  2,
	})
	if code != http.StatusOK {
		t.Fatalf("status %d %s", code, body)
	}
	var resp readResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalLines != 5 {
		t.Fatalf("total_lines %d", resp.TotalLines)
	}
	if !resp.Truncated {
		t.Fatal("expected truncated")
	}
	if !strings.Contains(resp.Content, "2\ttwo") || !strings.Contains(resp.Content, "3\tthree") {
		t.Fatalf("content %q", resp.Content)
	}
	if strings.Contains(resp.Content, "four") {
		t.Fatalf("should not include four: %q", resp.Content)
	}
}

func TestFSEditMatches(t *testing.T) {
	_, mux, root := testFSEnv(t)
	path := filepath.Join(root, "edit.txt")
	if err := os.WriteFile(path, []byte("aaa bbb aaa\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, body := doJSON(t, mux, http.MethodPost, "/fs/edit", map[string]any{
		"path":       path,
		"old_string": "zzz",
		"new_string": "y",
	})
	if code != http.StatusConflict {
		t.Fatalf("zero matches status %d %s", code, body)
	}
	var zero errorResponse
	if err := json.Unmarshal(body, &zero); err != nil {
		t.Fatal(err)
	}
	if zero.Error.Type != CodeConflict {
		t.Fatalf("type %s", zero.Error.Type)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/fs/edit", map[string]any{
		"path":       path,
		"old_string": "aaa",
		"new_string": "xxx",
	})
	if code != http.StatusConflict {
		t.Fatalf("multi status %d %s", code, body)
	}
	var multi errorResponse
	if err := json.Unmarshal(body, &multi); err != nil {
		t.Fatal(err)
	}
	if multi.Error.Type != CodeConflict || !strings.Contains(multi.Error.Message, "2") {
		t.Fatalf("body %+v", multi)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/fs/edit", map[string]any{
		"path":       path,
		"old_string": "bbb",
		"new_string": "ccc",
	})
	if code != http.StatusOK {
		t.Fatalf("one match status %d %s", code, body)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "aaa ccc aaa\n" {
		t.Fatalf("got %q", got)
	}
}

func TestFSWriteProtectedOS(t *testing.T) {
	_, mux, _ := testFSEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/fs/write", map[string]any{
		"path":    "/etc/dadi-agent-should-not-write",
		"content": "nope",
	})
	if code != http.StatusForbidden {
		t.Fatalf("status %d %s", code, body)
	}
	var errBody errorResponse
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Type != CodeForbidden {
		t.Fatalf("type %s", errBody.Error.Type)
	}
}

func TestFSReadOSAllowed(t *testing.T) {
	_, mux, _ := testFSEnv(t)
	path := "/etc/hosts"
	if _, err := os.Stat(path); err != nil {
		t.Skip("no /etc/hosts")
	}
	code, body := doJSON(t, mux, http.MethodPost, "/fs/read", map[string]any{
		"path": path,
	})
	if code != http.StatusOK {
		t.Fatalf("status %d %s", code, body)
	}
}

func TestFSWriteProtectedStateRuntime(t *testing.T) {
	_, mux, root := testFSEnv(t)
	path := filepath.Join(root, "modules", "dwar", ".env")
	code, body := doJSON(t, mux, http.MethodPost, "/fs/write", map[string]any{
		"path":    path,
		"content": "stolen",
	})
	if code != http.StatusForbidden {
		t.Fatalf("status %d %s", code, body)
	}
	var errBody errorResponse
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Type != CodeForbidden {
		t.Fatalf("type %s", errBody.Error.Type)
	}
}

func TestFSSymlinkWriteEscapeForbidden(t *testing.T) {
	_, mux, root := testFSEnv(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Point a link inside a write-protected tree... use /etc via link from workspace.
	link := filepath.Join(root, "escape")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	code, body := doJSON(t, mux, http.MethodPost, "/fs/write", map[string]any{
		"path":    link,
		"content": "hacked",
	})
	if code != http.StatusForbidden {
		t.Fatalf("status %d %s", code, body)
	}
	var errBody errorResponse
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Type != CodeForbidden {
		t.Fatalf("type %s", errBody.Error.Type)
	}
}

func TestFSBinaryReadUnsupported(t *testing.T) {
	_, mux, root := testFSEnv(t)
	path := filepath.Join(root, "bin.dat")
	data := append([]byte("hello"), 0x00, 0x01, 0x02)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := doJSON(t, mux, http.MethodPost, "/fs/read", map[string]any{
		"path": path,
	})
	if code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d %s", code, body)
	}
	var errBody errorResponse
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error.Type != CodeBinaryFile {
		t.Fatalf("type %s", errBody.Error.Type)
	}
}

func TestFSWriteAndGlob(t *testing.T) {
	_, mux, root := testFSEnv(t)
	path := filepath.Join(root, "sub", "a.txt")
	code, body := doJSON(t, mux, http.MethodPost, "/fs/write", map[string]any{
		"path":    path,
		"content": "hello",
	})
	if code != http.StatusOK {
		t.Fatalf("write %d %s", code, body)
	}
	code, body = doJSON(t, mux, http.MethodPost, "/fs/glob", map[string]any{
		"pattern": "**/*.txt",
		"cwd":     root,
	})
	if code != http.StatusOK {
		t.Fatalf("glob %d %s", code, body)
	}
	var resp globResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Paths) != 1 || resp.Paths[0] != path {
		t.Fatalf("paths %+v", resp.Paths)
	}
}

func TestFSGrep(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not on PATH")
	}
	_, mux, root := testFSEnv(t)
	path := filepath.Join(root, "g.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta findme\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := doJSON(t, mux, http.MethodPost, "/fs/grep", map[string]any{
		"pattern": "findme",
		"cwd":     root,
	})
	if code != http.StatusOK {
		t.Fatalf("grep %d %s", code, body)
	}
	var resp grepResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Matches) != 1 || resp.Matches[0].Line != 2 {
		t.Fatalf("matches %+v", resp.Matches)
	}
}
