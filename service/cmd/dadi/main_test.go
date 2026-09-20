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

type toolFixture struct {
	server      *httptest.Server
	lastExecute map[string]any
}

func newToolFixture(t *testing.T) *toolFixture {
	t.Helper()
	f := &toolFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/tools":
			_, _ = io.WriteString(w, `{"tools":[{"name":"nas_get_logs","description":"Query logs","input_schema":{"type":"object","properties":{"level":{"type":"string","enum":["debug","info","warn","error"]},"services":{"type":"string"}}}},{"name":"browser_spawn","description":"Open a browser","input_schema":{"type":"object","properties":{}}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/tools/nas_get_logs":
			_, _ = io.WriteString(w, `{"name":"nas_get_logs","description":"Query logs","input_schema":{"type":"object","properties":{"level":{"type":"string","enum":["debug","info","warn","error"]},"services":{"type":"string"}},"required":[]}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/agents":
			_, _ = io.WriteString(w, `{
				"agents":[
					{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"Coding Manager","parent_agent_id":null,"active":true},
					{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Worker One","parent_agent_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","active":false},
					{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"Finance Specialist","parent_agent_id":null,"active":true}
				]
			}`)
		case r.Method == http.MethodPost && r.URL.Path == "/tools/nas_get_logs/execute":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.lastExecute = body
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
	t.Cleanup(f.server.Close)
	dimaagBase = f.server.URL
	return f
}

func TestCompleteLine(t *testing.T) {
	_ = newToolFixture(t)

	got := completeLine("dadi nas_")
	if strings.Join(got, ",") != "nas_get_logs" {
		t.Fatalf("tools: %v", got)
	}
	got = completeLine("dadi ")
	if !containsAll(got, []string{"agents", "help", "browser_spawn", "nas_get_logs"}) {
		t.Fatalf("top-level: %v", got)
	}
	got = completeLine("dadi nas_get_logs --")
	if !containsAll(got, []string{"--as", "--as-agent-id", "--as-dadi", "--level", "--services"}) {
		t.Fatalf("flags: %v", got)
	}
	got = completeLine("dadi nas_get_logs --as ")
	if !containsAll(got, []string{"Coding Manager", "Finance Specialist", "Worker One"}) {
		t.Fatalf("agent names: %v", got)
	}
	got = completeLine("dadi nas_get_logs --lev")
	if strings.Join(got, ",") != "--level" {
		t.Fatalf("flags: %v", got)
	}
	got = completeLine("dadi nas_get_logs --level ")
	if !containsAll(got, []string{"debug", "info", "warn", "error"}) {
		t.Fatalf("enums: %v", got)
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
	f := newToolFixture(t)

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
	text := string(out)
	if !strings.Contains(text, "nas_get_logs") || !strings.Contains(text, "browser_spawn") {
		t.Fatalf("help: %s", out)
	}
	if !strings.Contains(text, "dadi agents") || !strings.Contains(text, "--as-dadi") {
		t.Fatalf("help identity: %s", out)
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
	if f.lastExecute["as_agent_id"] != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("as_agent_id %+v", f.lastExecute)
	}
	if f.lastExecute["level"] != "error" {
		t.Fatalf("input %+v", f.lastExecute)
	}
}

func TestExecuteAsDadi(t *testing.T) {
	f := newToolFixture(t)
	if err := run([]string{"nas_get_logs", "--as-dadi", "--level", "error"}); err != nil {
		t.Fatal(err)
	}
	if f.lastExecute["as_agent_id"] != "dadi" {
		t.Fatalf("as_agent_id %+v", f.lastExecute)
	}
}

func TestExecuteAsName(t *testing.T) {
	f := newToolFixture(t)
	if err := run([]string{"nas_get_logs", "--as", "Coding Manager", "--level", "error"}); err != nil {
		t.Fatal(err)
	}
	if f.lastExecute["as_agent_id"] != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("as_agent_id %+v", f.lastExecute)
	}
}

func TestExecuteAsUnknownName(t *testing.T) {
	_ = newToolFixture(t)
	err := run([]string{"nas_get_logs", "--as", "Missing Manager", "--level", "error"})
	if err == nil || !strings.Contains(err.Error(), `"Missing Manager"`) || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
}

func TestExecuteRejectsMultipleIdentityFlags(t *testing.T) {
	_ = newToolFixture(t)
	err := run([]string{"nas_get_logs", "--as-dadi", "--as-agent-id", "11111111-1111-4111-8111-111111111111"})
	if err == nil || !strings.Contains(err.Error(), "pass only one") {
		t.Fatalf("got %v", err)
	}
	err = run([]string{"nas_get_logs", "--as", "Coding Manager", "--as-dadi"})
	if err == nil || !strings.Contains(err.Error(), "pass only one") {
		t.Fatalf("got %v", err)
	}
}

func TestMissingTool(t *testing.T) {
	_ = newToolFixture(t)
	err := run([]string{"help", "missing"})
	if err == nil || err.Error() != "not_found: tool missing not found" {
		t.Fatalf("got %v", err)
	}
}

func TestExecuteRequiresCallerIdentity(t *testing.T) {
	_ = newToolFixture(t)
	err := run([]string{"nas_get_logs", "--level", "error"})
	if err == nil || !strings.Contains(err.Error(), "--as-dadi") || !strings.Contains(err.Error(), "--as ") {
		t.Fatalf("got %v", err)
	}
}

func TestAgentsCommand(t *testing.T) {
	_ = newToolFixture(t)
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	if err := run([]string{"agents"}); err != nil {
		os.Stdout = old
		t.Fatal(err)
	}
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	text := string(out)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: %v", lines)
	}
	if !strings.HasPrefix(lines[0], "Coding Manager  aaaaaaaa  active") {
		t.Fatalf("root: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  Worker One  bbbbbbbb  dormant") {
		t.Fatalf("child: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "Finance Specialist  cccccccc  active") {
		t.Fatalf("sibling: %q", lines[2])
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
