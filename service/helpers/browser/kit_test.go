package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dadi-os/nas/service/helpers"
)

// chromiumHost starts a real headless Chromium per Chromium folder, as Nas's host does.
type chromiumHost struct {
	t        *testing.T
	mu       sync.Mutex
	next     int
	profiles []string
	stops    map[int]func()
}

func (h *chromiumHost) StartBrowser(profile string) (Launch, error) {
	url, stop := launchChromium(h.t, profile)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	if h.stops == nil {
		h.stops = map[int]func(){}
	}
	h.stops[h.next] = stop
	h.profiles = append(h.profiles, profile)
	return Launch{ID: h.next, CDPURL: url}, nil
}

func (h *chromiumHost) StopBrowser(id int) {
	h.mu.Lock()
	stop := h.stops[id]
	h.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func newTestKit(t *testing.T) (*Kit, *chromiumHost, string) {
	t.Helper()
	chromiumBin(t)
	cg := t.TempDir()
	_ = os.WriteFile(filepath.Join(cg, "memory.max"), []byte("max\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cg, "memory.current"), []byte("1\n"), 0o644)
	host := &chromiumHost{t: t}
	dir := t.TempDir()
	k, err := New(host, "http://chaavi.invalid", "http://dwar.invalid", t.TempDir(), dir, cg)
	if err != nil {
		t.Fatal(err)
	}
	return k, host, dir
}

func newTask(id, owner, purpose string) *helpers.Task {
	return &helpers.Task{ID: id, Kind: "browser", Owner: owner, Purpose: purpose, Memo: map[string]string{}}
}

// TestChromiumFoldersBelongToTheAgent runs tasks through wake and sleep: an agent's second
// concurrent browser gets its second folder, another agent's first browser gets its own
// first, a woken task returns to its folder and reopens its tabs, a new browser prefers the
// folder that held the site it is steered toward, folders are never deleted, and a Chromium
// that died is started again before the next tool runs.
func TestChromiumFoldersBelongToTheAgent(t *testing.T) {
	k, host, dir := newTestKit(t)
	srv := serve(t, map[string]string{"/": "<title>Home page</title><h1>Welcome</h1>"})

	one, two, other := newTask("t1", "agent-a", "read my mail"), newTask("t2", "agent-a", ""), newTask("t3", "agent-b", "")
	for _, task := range []*helpers.Task{one, two, other} {
		if err := k.Wake(task); err != nil {
			t.Fatal(err)
		}
	}
	if one.Folder != "1" || two.Folder != "2" || other.Folder != "1" {
		t.Fatalf("folders %q %q %q", one.Folder, two.Folder, other.Folder)
	}
	want := []string{filepath.Join(dir, "agent-a", "1"), filepath.Join(dir, "agent-a", "2"), filepath.Join(dir, "agent-b", "1")}
	if strings.Join(host.profiles, "|") != strings.Join(want, "|") {
		t.Fatalf("browsers ran on %v, want %v", host.profiles, want)
	}

	out, err := k.Call(one, "navigate", json.RawMessage(`{"url":"`+srv.URL+`/"}`))
	if err != nil || !strings.Contains(out, "title: Home page") || !strings.Contains(out, "Welcome") {
		t.Fatalf("navigate: %v\n%s", err, out)
	}
	if state := k.State(one); !strings.Contains(state, srv.URL) || !strings.Contains(state, "Home page") {
		t.Fatalf("state:\n%s", state)
	}

	if err := k.Sleep(one); err != nil {
		t.Fatal(err)
	}
	if one.Resource != "" {
		t.Fatalf("resource %q after sleep", one.Resource)
	}
	for _, folder := range []string{"agent-a/1", "agent-a/2", "agent-b/1"} {
		if _, err := os.Stat(filepath.Join(dir, folder)); err != nil {
			t.Fatalf("folder %s after sleep: %v", folder, err)
		}
	}
	if state := k.State(one); !strings.Contains(state, "reopened on wake") || !strings.Contains(state, srv.URL) {
		t.Fatalf("asleep state:\n%s", state)
	}
	if err := k.Wake(one); err != nil {
		t.Fatal(err)
	}
	if one.Folder != "1" {
		t.Fatalf("woke on folder %q", one.Folder)
	}
	if state := k.State(one); !strings.Contains(state, srv.URL) {
		t.Fatalf("tabs not reopened:\n%s", state)
	}

	if err := k.Sleep(one); err != nil {
		t.Fatal(err)
	}
	if err := k.Sleep(two); err != nil {
		t.Fatal(err)
	}
	if err := recordSites(dir, "agent-a", "2", []string{"linkedin.com"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	later := newTask("t4", "agent-a", "message people on https://www.linkedin.com")
	if err := k.Wake(later); err != nil {
		t.Fatal(err)
	}
	if later.Folder != "2" {
		t.Fatalf("a task steered at linkedin.com got folder %q", later.Folder)
	}

	host.mu.Lock()
	id, _ := strconv.Atoi(later.Resource)
	stop := host.stops[id]
	host.mu.Unlock()
	stop()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		k.mu.Lock()
		s := k.sessions[later.ID]
		k.mu.Unlock()
		if s.b.Closed() {
			break
		}
	}
	if out, err := k.Call(later, "tabs", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("tabs after the browser died: %v\n%s", err, out)
	}
	for _, task := range []*helpers.Task{later, two, other} {
		if err := k.Sleep(task); err != nil {
			t.Fatal(err)
		}
	}
}
