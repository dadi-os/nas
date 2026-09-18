package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func toolServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tools":
			_, _ = io.WriteString(w, `{"tools":[{"name":"nas_get_logs","description":"Query logs","input_schema":{"type":"object","properties":{"level":{"type":"string","enum":["debug","info","warn","error"]},"services":{"type":"string"}}}},{"name":"browser_spawn","description":"Open a browser","input_schema":{"type":"object","properties":{}}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/tools/nas_get_logs":
			_, _ = io.WriteString(w, `{"name":"nas_get_logs","description":"Query logs","input_schema":{"type":"object","properties":{"level":{"type":"string","enum":["debug","info","warn","error"]},"services":{"type":"string"}},"required":[]}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/tools/nas_get_logs/execute":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["as_agent_id"] != "11111111-1111-4111-8111-111111111111" {
				t.Fatalf("as_agent_id %+v", body)
			}
			if body["level"] != "error" {
				t.Fatalf("input %+v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "is_error": false, "content": `{"entries":[]}`,
			})
		case r.URL.Path == "/tools/missing":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"type": "not_found", "message": "tool missing not found"},
			})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestCompleteLine(t *testing.T) {
	srv := toolServer(t)
	t.Cleanup(srv.Close)
	dimaagBase = srv.URL

	got := completeLine("dadi nas_")
	if strings.Join(got, ",") != "nas_get_logs" {
		t.Fatalf("tools: %v", got)
	}
	got = completeLine("dadi nas_get_logs --lev")
	if strings.Join(got, ",") != "--level" {
		t.Fatalf("flags: %v", got)
	}
	got = completeLine("dadi nas_get_logs --level ")
	if strings.Join(got, ",") != "debug,info,warn,error" && strings.Join(got, ",") != "error,warn,info,debug" {
		if !containsAll(got, []string{"debug", "info", "warn", "error"}) {
			t.Fatalf("enums: %v", got)
		}
	}
}

func containsAll(got, want []string) bool {
	have := map[string]struct{}{}
	for _, g := range got {
		have[g] = struct{}{}
	}
	for _, w := range want {
		if _, ok := have[w]; !ok {
			return false
		}
	}
	return len(got) == len(want)
}

func TestHelpAndExecute(t *testing.T) {
	srv := toolServer(t)
	t.Cleanup(srv.Close)
	dimaagBase = srv.URL

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	if err := run(nil); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), "nas_get_logs") || !strings.Contains(string(out), "browser_spawn") {
		t.Fatalf("help: %s", out)
	}

	r, w, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	if err := run([]string{"nas_get_logs", "--as-agent-id", "11111111-1111-4111-8111-111111111111", "--level", "error"}); err != nil {
		os.Stdout = old
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdout = old
	out, _ = io.ReadAll(r)
	if !strings.Contains(string(out), "entries") {
		t.Fatalf("execute: %s", out)
	}
}

func TestMissingTool(t *testing.T) {
	srv := toolServer(t)
	t.Cleanup(srv.Close)
	dimaagBase = srv.URL
	err := run([]string{"help", "missing"})
	if err == nil || err.Error() != "not_found: tool missing not found" {
		t.Fatalf("got %v", err)
	}
}

func TestExecuteRequiresAsAgentID(t *testing.T) {
	srv := toolServer(t)
	t.Cleanup(srv.Close)
	dimaagBase = srv.URL
	err := run([]string{"nas_get_logs", "--level", "error"})
	if err == nil || !strings.Contains(err.Error(), "--as-agent-id") {
		t.Fatalf("got %v", err)
	}
}

func TestParseFlagsCoerce(t *testing.T) {
	got, err := parseFlags([]string{"--level=error", "--limit", "8", "--on"})
	if err != nil {
		t.Fatal(err)
	}
	if got["level"] != "error" || got["limit"] != int64(8) || got["on"] != true {
		t.Fatalf("%v", got)
	}
}

func TestLoadDimaagBaseRequiresEnv(t *testing.T) {
	t.Setenv("DIMAAG_URL", "")
	if _, err := loadDimaagBase(); err == nil {
		t.Fatal("expected error")
	}
	t.Setenv("DIMAAG_URL", " http://dimaag.example/ ")
	got, err := loadDimaagBase()
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://dimaag.example" {
		t.Fatalf("got %q", got)
	}
}
