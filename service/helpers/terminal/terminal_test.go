package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dadi-os/nas/service/helpers"
)

// fakeNas records the requests the kit makes to Nas's own API and Chaavi, answering exec with
// exec(command).
type fakeNas struct {
	mu       sync.Mutex
	requests []string
	bodies   []map[string]any
	exec     func(command string) map[string]any
}

func (f *fakeNas) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/terminals" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "t3", "cwd": "/var/home/dadi"})
		case strings.HasSuffix(r.URL.Path, "/exec"):
			_ = json.NewEncoder(w).Encode(f.exec(body["command"].(string)))
		case r.URL.Path == "/fs/read":
			_ = json.NewEncoder(w).Encode(map[string]any{"content": "120\tline\n", "total_lines": 500, "truncated": false})
		case r.URL.Path == "/fs/edit":
			_ = json.NewEncoder(w).Encode(map[string]bool{"replaced": true})
		case r.URL.Path == "/fs/write":
			_ = json.NewEncoder(w).Encode(map[string]int{"bytes": 5})
		case strings.HasPrefix(r.URL.Path, "/v1/secrets/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"value": "s3cr3t"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestKit(t *testing.T, f *fakeNas) (*Kit, *helpers.Task) {
	t.Helper()
	srv := f.server(t)
	kit, err := New(srv.URL, srv.URL, "/var/home/dadi")
	if err != nil {
		t.Fatal(err)
	}
	task := &helpers.Task{ID: "ab12cd34", Kind: "terminal", Memo: map[string]string{}}
	if err := kit.Wake(task); err != nil {
		t.Fatal(err)
	}
	return kit, task
}

func TestCharterCarriesTheDoctrine(t *testing.T) {
	kit, err := New("http://nas", "http://chaavi", "/home")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Terminal helper", "# Coding Doctrine", "# System Doctrine", "feature/<x>"} {
		if !strings.Contains(kit.Charter(), want) {
			t.Errorf("charter lacks %q", want)
		}
	}
}

func TestCharterCarriesTheManagersRules(t *testing.T) {
	kit, err := New("http://nas", "http://chaavi", "/home")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"You decide what to install", "known vulnerabilities", "Approve most of them", "a release under seven days old",
		"Ankur Desai <ardusa05@gmail.com>", "git config --local",
		"match on the sender, never on subject words", "finish` with the code alone",
	} {
		if !strings.Contains(kit.Charter(), want) {
			t.Errorf("charter lacks %q", want)
		}
	}
}

func TestRunKeepsFullOutputInALogAndReportsTheExitCode(t *testing.T) {
	f := &fakeNas{exec: func(string) map[string]any {
		return map[string]any{"exit_code": 2, "output": "boom", "timed_out": false}
	}}
	kit, task := newTestKit(t, f)
	if task.Resource != "t3" || task.Cwd != "/var/home/dadi" {
		t.Fatalf("woke %+v", task)
	}
	out, err := kit.Call(task, "run", json.RawMessage(`{"command":"make test","cwd":"/var/home/dadi/projects/x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "exit 2 (full output: /var/home/dadi/.cache/dadi-helpers/ab12cd34/") || !strings.HasSuffix(out, "boom") {
		t.Fatalf("result %q", out)
	}
	command := f.bodies[len(f.bodies)-1]["command"].(string)
	if !strings.Contains(command, "cd '/var/home/dadi/projects/x' && { make test") || !strings.Contains(command, "tail -c 16000") {
		t.Fatalf("command %q", command)
	}
}

func TestBackgroundJobsAreFollowedAndStopped(t *testing.T) {
	f := &fakeNas{exec: func(command string) map[string]any {
		if strings.Contains(command, "nohup") {
			return map[string]any{"exit_code": 0, "output": "4242\n"}
		}
		return map[string]any{"exit_code": 0, "output": "running\nlistening on :3000"}
	}}
	kit, task := newTestKit(t, f)
	out, err := kit.Call(task, "run", json.RawMessage(`{"command":"npm run dev","background":true}`))
	if err != nil || !strings.Contains(out, "handle 4242") {
		t.Fatalf("start %q %v", out, err)
	}
	if !strings.Contains(kit.State(task), "Background jobs: 4242") {
		t.Fatalf("state %q", kit.State(task))
	}
	if out, err := kit.Call(task, "job", json.RawMessage(`{"handle":"4242"}`)); err != nil || !strings.Contains(out, "listening") {
		t.Fatalf("read %q %v", out, err)
	}
	if _, err := kit.Call(task, "job", json.RawMessage(`{"handle":"4242","stop":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.Call(task, "job", json.RawMessage(`{"handle":"4242"}`)); err == nil {
		t.Fatal("followed a stopped job")
	}
}

func TestSecretIsStagedLoadedAndScrubbed(t *testing.T) {
	f := &fakeNas{exec: func(string) map[string]any { return map[string]any{"exit_code": 0, "output": "token=s3cr3t ok"} }}
	kit, task := newTestKit(t, f)
	out, err := kit.Call(task, "secret", json.RawMessage(`{"name":"Stripe API key","env":"STRIPE_KEY","command":"curl -u $STRIPE_KEY: https://api"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cr3t") || !strings.Contains(out, "token=*** ok") {
		t.Fatalf("output not scrubbed: %q", out)
	}
	command := f.bodies[len(f.bodies)-1]["command"].(string)
	if strings.Contains(command, "s3cr3t") || !strings.Contains(command, "export STRIPE_KEY=$(cat '/tmp/dadi-secret-ab12cd34-") || !strings.Contains(command, "rm -f") {
		t.Fatalf("command %q", command)
	}
}

func TestReadRangeAndEditsShowInTheState(t *testing.T) {
	f := &fakeNas{exec: func(string) map[string]any { return map[string]any{"exit_code": 0, "output": ""} }}
	kit, task := newTestKit(t, f)
	if _, err := kit.Call(task, "read", json.RawMessage(`{"path":"/a.go","range":"120-180"}`)); err != nil {
		t.Fatal(err)
	}
	body := f.bodies[len(f.bodies)-1]
	if body["offset"] != float64(120) || body["limit"] != float64(61) {
		t.Fatalf("read body %v", body)
	}
	if _, err := kit.Call(task, "read", json.RawMessage(`{"path":"/a.go","range":"9-3"}`)); err == nil {
		t.Fatal("accepted a backwards range")
	}
	if _, err := kit.Call(task, "edit", json.RawMessage(`{"path":"/a.go","old":"x","new":"y"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.Call(task, "write", json.RawMessage(`{"path":"/b.go","content":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if state := kit.State(task); !strings.Contains(state, "Files touched: /a.go, /b.go") {
		t.Fatalf("state %q", state)
	}
}
