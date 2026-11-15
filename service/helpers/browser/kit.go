// Package browser is the browser helper's kit: its charter (charter.md), its tools, and the
// Chromium it drives over the DevTools protocol on a per-agent Chromium folder. Nothing else
// in Dadi opens or drives a browser.
package browser

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dadi-os/nas/service/helpers"
	"github.com/dadi-os/nas/service/helpers/sites"
)

//go:embed charter.md
var charter string

const (
	// idleSleep is how long a browser task may sit idle before its Chromium is stopped.
	idleSleep = 15 * time.Minute
	// stateTimeout bounds reading the open tabs for a task listing, which the runner does while
	// holding its lock.
	stateTimeout = 3 * time.Second
	// callTimeout bounds one tool call; downloadCallTimeout bounds the download tool, which waits for a file.
	callTimeout         = 90 * time.Second
	downloadCallTimeout = 10 * time.Minute
	// failuresAfter is how many failed calls in a row on one site make a failures fact; the fact
	// lists the latest failuresAfter errors, each cut to failureChars.
	failuresAfter = 3
	failureChars  = 160
	// staleContext is the protocol error of a page command sent to an execution context a navigation just replaced.
	staleContext = "Cannot find context with specified id"
	// extractMaxChars is the most characters of extracted data a result carries; the helper cannot read files.
	extractMaxChars = 8_000
	// downloadWait is how long a download started by a click or a URL may take to finish.
	downloadWait = 120 * time.Second
	// tabsNote is the memo key holding the URLs of the tabs a sleeping task reopens.
	tabsNote = "tabs"
	// profiledNote is the prefix of the memo keys marking the hosts whose site profile the task
	// has already been shown.
	profiledNote = "profiled:"
	// toolNoteSite records a fact about a site in its profile.
	toolNoteSite = "note_site"
)

// Launch is a Chromium that a Host started: its slot number and its DevTools websocket URL.
type Launch struct {
	ID     int
	CDPURL string
}

// Host starts and stops Chromium (with its X display) for the kit.
type Host interface {
	// StartBrowser starts a browser on the Chromium folder at profileDir.
	StartBrowser(profileDir string) (Launch, error)
	// StopBrowser stops the browser started as id.
	StopBrowser(id int)
	// AsDadi runs fn with the filesystem identity of the user the terminal helper's shell runs
	// as, so what fn creates in the dadi home belongs to that user.
	AsDadi(fn func() error) error
}

// Kit is the browser helper kind.
type Kit struct {
	host        Host
	chaavi      *chaaviClient
	dwar        string
	http        *http.Client
	home        string
	browsersDir string
	cgroup      string
	profiles    *sites.Store
	mu          sync.Mutex
	sessions    map[string]*session
	busy        map[string]bool
}

// session is one task's running browser.
type session struct {
	mu     sync.Mutex
	id     int
	b      *Browser
	agent  string
	folder string
	// failures lists the errors of the calls in a row that failed on each site.
	failures map[string][]string
	// rows is how many rows the last extract returned.
	rows int
}

// New builds the browser kit. browsersDir holds the per-agent Chromium folders
// (<browsersDir>/<agent>/1..n), cgroup is the cgroup whose memory limits how many browsers
// run, and home is the dadi home (downloads land in <home>/Downloads). profiles holds what is
// known of each site. It fails when the cgroup's memory cannot be read.
func New(host Host, chaaviURL, dwarURL, home, browsersDir, cgroup string, profiles *sites.Store) (*Kit, error) {
	if _, err := readCgroup(cgroup); err != nil {
		return nil, fmt.Errorf("BROWSER_CGROUP %s: %w", cgroup, err)
	}
	return &Kit{
		host: host, chaavi: &chaaviClient{base: chaaviURL, http: &http.Client{Timeout: chaaviTimeout}}, dwar: dwarURL,
		http: &http.Client{Timeout: 2 * time.Minute}, home: home, browsersDir: browsersDir, cgroup: cgroup, profiles: profiles,
		sessions: map[string]*session{}, busy: map[string]bool{},
	}, nil
}

// Kind is "browser".
func (k *Kit) Kind() string { return "browser" }

// Charter is charter.md.
func (k *Kit) Charter() string { return charter }

// IdleSleep is 15 minutes.
func (k *Kit) IdleSleep() time.Duration { return idleSleep }

// Room reports whether the browser cgroup has memory for one more browser.
func (k *Kit) Room() (bool, error) {
	m, err := readCgroup(k.cgroup)
	if err != nil {
		return false, err
	}
	return m.hasRoom(browserReserve), nil
}

// downloadsDir is where downloads land.
func (k *Kit) downloadsDir() string { return filepath.Join(k.home, helpers.DownloadsDir) }

// Wake starts a browser for t on one of its owner's Chromium folders and reopens the tabs
// it had when it slept.
func (k *Kit) Wake(t *helpers.Task) error {
	if err := k.Sleep(t); err != nil {
		return err
	}
	sites := sitesIn(t.Purpose)
	var urls []string
	if raw := t.Notes()[tabsNote]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &urls); err != nil {
			return fmt.Errorf("saved tabs of task/browser/%s: %w", t.ID, err)
		}
		for _, u := range urls {
			sites = append(sites, siteOf(u))
		}
	}
	folder, err := k.takeFolder(t.Owner, t.Folder, sites)
	if err != nil {
		return err
	}
	launch, err := k.host.StartBrowser(filepath.Join(k.browsersDir, t.Owner, folder))
	if err != nil {
		k.release(t.Owner, folder)
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	b, err := Connect(ctx, launch.CDPURL, k.downloadsDir())
	if err != nil {
		k.host.StopBrowser(launch.ID)
		k.release(t.Owner, folder)
		return err
	}
	s := &session{id: launch.ID, b: b, agent: t.Owner, folder: folder}
	k.mu.Lock()
	k.sessions[t.ID] = s
	k.mu.Unlock()
	t.Resource, t.Folder = strconv.Itoa(launch.ID), folder
	t.Note("restore", "")
	if failed := k.restoreTabs(ctx, b, urls); failed != "" {
		t.Note("restore", failed)
	}
	return nil
}

// takeFolder reserves the Chromium folder a new browser of agent runs on and creates it when
// it is new.
func (k *Kit) takeFolder(agent, own string, sites []string) (string, error) {
	if agent == "" || strings.ContainsAny(agent, "/\\") || agent == "." || agent == ".." {
		return "", fmt.Errorf("owner %q cannot name a browser folder", agent)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	folders, err := agentFolders(k.browsersDir, agent, func(f string) bool { return k.busy[agent+"/"+f] })
	if err != nil {
		return "", err
	}
	name, create := chooseFolder(folders, own, sites)
	if create {
		if err := os.MkdirAll(filepath.Join(k.browsersDir, agent, name), 0o755); err != nil {
			return "", err
		}
	}
	k.busy[agent+"/"+name] = true
	return name, nil
}

// release frees a Chromium folder for the next browser.
func (k *Kit) release(agent, folder string) {
	k.mu.Lock()
	delete(k.busy, agent+"/"+folder)
	k.mu.Unlock()
}

// restoreTabs reopens urls in the fresh browser and returns a note naming those that did
// not load.
func (k *Kit) restoreTabs(ctx context.Context, b *Browser, urls []string) string {
	var failed []string
	for i, u := range urls {
		var tab *Tab
		var err error
		if i == 0 {
			tab, err = b.Tab(ctx, "")
		} else {
			tab, err = b.NewTab(ctx, "about:blank")
		}
		if err == nil {
			_, err = tab.Navigate(ctx, u, "domcontentloaded")
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", u, err))
		}
	}
	if len(failed) == 0 {
		return ""
	}
	return "tabs that did not reopen: " + strings.Join(failed, "; ")
}

// Sleep records t's open tabs and the sites its folder held, then stops its browser. The
// Chromium folder stays with the agent.
func (k *Kit) Sleep(t *helpers.Task) error {
	k.mu.Lock()
	s := k.sessions[t.ID]
	delete(k.sessions, t.ID)
	k.mu.Unlock()
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if err := k.saveTabs(ctx, t, s); err != nil && !s.b.Closed() {
		slog.Error("browser tabs not saved at sleep", "code", "internal_error", "task", "browser/"+t.ID, "err", err)
	}
	if err := s.b.Close(); err != nil {
		slog.Error("browser connection close failed", "code", "internal_error", "task", "browser/"+t.ID, "err", err)
	}
	k.host.StopBrowser(s.id)
	k.release(s.agent, s.folder)
	t.Resource = ""
	return nil
}

// Halt stops t's browser as Sleep does; Chromium and its child processes end with it.
func (k *Kit) Halt(t *helpers.Task) error { return k.Sleep(t) }

// State lists the open tabs with their URL and title, or the tabs a sleeping task reopens.
func (k *Kit) State(t *helpers.Task) string {
	k.mu.Lock()
	s := k.sessions[t.ID]
	k.mu.Unlock()
	var lines []string
	if s == nil {
		var urls []string
		if raw := t.Notes()[tabsNote]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &urls); err != nil {
				return "Saved tabs unreadable: " + err.Error()
			}
		}
		for _, u := range urls {
			lines = append(lines, "  "+u)
		}
		if len(lines) == 0 {
			return "No tabs open."
		}
		return "Tabs (reopened on wake):\n" + strings.Join(lines, "\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), stateTimeout)
	defer cancel()
	tabs, err := s.b.Tabs(ctx)
	if err != nil {
		return "Tabs unknown: " + err.Error()
	}
	for _, tab := range tabs {
		mark := " "
		if tab.Front {
			mark = "*"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s", mark, tab.ID[:min(8, len(tab.ID))], tab.URL, jsonString(tab.Title)))
	}
	out := "Tabs (* is in front):\n" + strings.Join(lines, "\n")
	if note := t.Notes()["restore"]; note != "" {
		out += "\n" + note
	}
	return out
}

// FrontURL is the URL of the tab in front, which is the page the browser's screen shows; it is
// empty while the browser is asleep or no tab is in front.
func (k *Kit) FrontURL(t *helpers.Task) string {
	k.mu.Lock()
	s := k.sessions[t.ID]
	k.mu.Unlock()
	if s == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), stateTimeout)
	defer cancel()
	tabs, err := s.b.Tabs(ctx)
	if err != nil {
		slog.Error("front tab not read", "code", "internal_error", "task", "browser/"+t.ID, "err", err)
		return ""
	}
	return frontURL(tabs)
}

// frontURL is the URL of the tab marked in front, or "" when none is.
func frontURL(tabs []TabInfo) string {
	for _, tab := range tabs {
		if tab.Front {
			return tab.URL
		}
	}
	return ""
}

// session returns t's running browser, starting it again first when its Chromium is gone
// (killed for memory or crashed). Nothing a tool was asked to do has run yet at that point.
func (k *Kit) session(t *helpers.Task) (*session, error) {
	k.mu.Lock()
	s := k.sessions[t.ID]
	k.mu.Unlock()
	if s != nil && !s.b.Closed() {
		return s, nil
	}
	if err := k.Wake(t); err != nil {
		return nil, fmt.Errorf("the browser had stopped and could not be started again: %w", err)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.sessions[t.ID], nil
}

// Tools are look, act, find, extract, screenshot, tabs, navigate, sign_in, download and upload.
func (k *Kit) Tools() []helpers.ToolSpec {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	obj := func(required []string, props map[string]any) map[string]any {
		props["tab"] = str("Tab id from tabs or the state; default the tab in front")
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
	}
	action := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"type"},
		"properties": map[string]any{
			"type":      map[string]any{"type": "string", "enum": []string{"click", "type", "select", "key", "scroll", "check", "upload", "wait", "dialog"}},
			"ref":       str("Element ref from look or find (click, type, select, check, upload; optional for key and scroll)"),
			"text":      str("type: the text; wait: text to wait for; dialog: answer to a prompt"),
			"value":     str("select: the option's value or label"),
			"key":       str("key: a key or chord such as Enter, Escape, Control+Enter"),
			"submit":    map[string]any{"type": "boolean", "description": "type: press Enter afterwards"},
			"double":    map[string]any{"type": "boolean", "description": "click: double click"},
			"checked":   map[string]any{"type": "boolean", "description": "check: the state to leave it in (default true)"},
			"paths":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "upload: absolute file paths on this machine"},
			"direction": str("scroll without a ref: up, down, top or bottom"),
			"amount":    map[string]any{"type": "integer", "description": "scroll: pixels (default 600)"},
			"ms":        map[string]any{"type": "integer", "description": "wait: milliseconds (default 10000 for text)"},
			"accept":    map[string]any{"type": "boolean", "description": "dialog: accept (default) or dismiss"},
		},
	}
	return []helpers.ToolSpec{
		{Name: "look", Description: "See a page: its URL, title and a compact outline of interactive elements and landmarks, each with a ref (e12) that stays with its element between looks. Frames and open shadow roots are included. scope (a ref, or text on the page) returns just that region. Never the whole DOM.", InputSchema: obj(nil, map[string]any{"scope": str("A ref or text naming the region to read")})},
		{Name: "act", Description: "Do a batch of actions in order on a tab (click, type, select, key, scroll, check, upload, wait, dialog), for example a whole form in one call. Each waits for its element to be visible, stable, enabled and uncovered, and scrolls it into view. Returns what changed: the new URL, new tabs, downloads, an open dialog and the new outline lines. It stops at the first action that fails and never repeats it; look before you try again.", InputSchema: obj([]string{"actions"}, map[string]any{"actions": map[string]any{"type": "array", "items": action, "description": "Actions to run in order"}})},
		{Name: "find", Description: "Refs of the elements anywhere on the page, frames and shadow roots included, whose text, role or label contains query.", InputSchema: obj([]string{"query"}, map[string]any{"query": str("Text, role or label to match")})},
		{Name: "extract", Description: "Read a table, list or the page's text into rows, following next-page or load-more controls for up to pages pages. format table gives CSV, json an array of objects, text the page text. A result over 8,000 characters is cut with a note; narrow what to read the rest.", InputSchema: obj([]string{"what", "format"}, map[string]any{
			"what":   str("What to read, such as \"the transactions table\""),
			"format": map[string]any{"type": "string", "enum": []string{"table", "json", "text"}},
			"pages":  map[string]any{"type": "integer", "description": "Most pages or load-more clicks to follow (default 1)"},
			"next":   str("Text of the next-page or load-more control, when it is not Next or Load more"),
		})},
		{Name: "screenshot", Description: "Save a PNG of the tab, or of the element at scope (a ref), and return its path. Pass question to have the image described and get the answer back; use it only for what an outline cannot show: canvas, charts, layout, captchas.", InputSchema: obj(nil, map[string]any{"scope": str("A ref to capture just that element"), "question": str("What to find out from the image")})},
		{Name: "tabs", Description: "Manage tabs. With no arguments, list them. open loads a URL in a new tab, switch brings a tab to the front, close closes one.", InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"open": str("URL to open in a new tab"), "switch": str("Tab id to bring to the front"), "close": str("Tab id to close")}}},
		{Name: "navigate", Description: "Load a URL in a tab and wait for it (wait: load, domcontentloaded or networkidle). Returns the outline. A URL that turns into a file download returns the download.", InputSchema: obj([]string{"url"}, map[string]any{"url": str("Address to load"), "wait": map[string]any{"type": "string", "enum": []string{"load", "domcontentloaded", "networkidle"}}})},
		{Name: "sign_in", Description: "Sign in to the site of the tab (or site) with its Chaavi login. A passkey is used first: it is loaded into the tab and the page reloaded so the site asks again; read the page after. With no passkey, or use login, it fills the username and password, ticks remember this device, and submits. If a code is needed it returns needs_code: ask for it. Passwords never pass through you.", InputSchema: obj(nil, map[string]any{
			"site": str("Domain to sign in to; default the tab's"), "item": str("Name of the Chaavi login when the site has several"),
			"use": map[string]any{"type": "string", "enum": []string{"passkey", "login"}, "description": "Force the passkey or the username and password"},
		})},
		{Name: "download", Description: "Get a file: give url to load it, or ref to click the control that starts it. With neither, list the downloads so far. Files land in ~/Downloads under their own names and come back as paths.", InputSchema: obj(nil, map[string]any{"url": str("Address of the file"), "ref": str("Ref of the control that downloads it")})},
		{Name: toolNoteSite, Description: "Record a fact you learned for certain about a site in its profile, which every later helper is shown the first time it loads that host. field is sign_in (value {method: passkey, login or decs, item: the Chaavi item's title, none for decs}), time_zone_shown (value: the zone the site shows, as a string), due_date_pages (value {name, url}) or blocks (value: the symptom that stops work there, as a string). One field per call.", InputSchema: obj([]string{"host", "field", "value"}, map[string]any{
			"host":  str("The host name, such as d2l.msu.edu"),
			"field": map[string]any{"type": "string", "enum": []string{sites.FieldSignIn, sites.FieldTimeZone, sites.FieldDueDatePages, sites.FieldBlocks}},
			"value": map[string]any{"description": "The value for the field: an object for sign_in and due_date_pages, a string for the others"},
		})},
		{Name: "upload", Description: "Attach files to a file input by absolute path. ref may be the file input itself (also in a frame or shadow root) or the control that opens the upload; the files go to the file chooser it opens or the input it adds.", InputSchema: obj([]string{"ref", "paths"}, map[string]any{"ref": str("Ref of the input or of the control that opens the upload"), "paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}})},
	}
}

type input struct {
	Tab      string          `json:"tab"`
	Scope    string          `json:"scope"`
	Question string          `json:"question"`
	Actions  []Action        `json:"actions"`
	Query    string          `json:"query"`
	What     string          `json:"what"`
	Format   string          `json:"format"`
	Pages    int             `json:"pages"`
	Next     string          `json:"next"`
	Open     string          `json:"open"`
	Switch   string          `json:"switch"`
	Close    string          `json:"close"`
	URL      string          `json:"url"`
	Wait     string          `json:"wait"`
	Ref      string          `json:"ref"`
	Paths    []string        `json:"paths"`
	Site     string          `json:"site"`
	Item     string          `json:"item"`
	Use      string          `json:"use"`
	Host     string          `json:"host"`
	Field    string          `json:"field"`
	Value    json.RawMessage `json:"value"`
}

// Call runs one tool on t's browser. A failure is returned as the tool's error and is never
// retried.
func (k *Kit) Call(t *helpers.Task, name string, raw json.RawMessage) (string, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if name == toolNoteSite {
		if err := k.profiles.Note(in.Host, in.Field, in.Value); err != nil {
			return "", err
		}
		return "noted " + in.Field + " for " + in.Host, nil
	}
	s, err := k.session(t)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	timeout := callTimeout
	if name == "download" {
		timeout = downloadCallTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := k.dispatch(ctx, t, s, name, in)
	if saveErr := k.saveTabs(ctx, t, s); saveErr != nil {
		slog.Error("browser tabs not saved", "code", "internal_error", "task", "browser/"+t.ID, "err", saveErr)
	}
	if err != nil {
		err = fmt.Errorf("%w%s", err, k.noteFailure(ctx, s, in, err))
	} else {
		k.noteSuccess(ctx, s, in)
		if name == "navigate" {
			out += k.profileNote(t, in.URL)
		}
	}
	return out, err
}

// Progress is the key of what a successful call of tool brought that is new. A navigate or an
// act brings the address now in front, an extract its rows, and any other call its result; a
// key that came up earlier in the steer is no progress.
func (k *Kit) Progress(t *helpers.Task, tool, result string) string {
	switch tool {
	case "navigate", "act":
		if front := k.FrontURL(t); front != "" {
			return "url:" + front
		}
		return ""
	case "extract":
		k.mu.Lock()
		s := k.sessions[t.ID]
		k.mu.Unlock()
		if s == nil {
			return ""
		}
		s.mu.Lock()
		rows := s.rows
		s.mu.Unlock()
		if rows == 0 {
			return ""
		}
	}
	if strings.TrimSpace(result) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(result))
	return tool + ":" + hex.EncodeToString(sum[:])
}

// OutlineReceipt is the address and title of the page a navigate or look result outlines.
func (k *Kit) OutlineReceipt(tool, result string) (string, bool) {
	if tool != "navigate" && tool != "look" {
		return "", false
	}
	lines := strings.SplitN(result, "\n", 3)
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "url: ") || !strings.HasPrefix(lines[1], "title: ") {
		return "", false
	}
	return strings.TrimPrefix(lines[0], "url: ") + " " + strings.TrimPrefix(lines[1], "title: "), true
}

// shorten cuts s to max characters with an ellipsis.
func shorten(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// callSite is the site a call worked on: its URL's, else the front tab's.
func callSite(ctx context.Context, s *session, in input) string {
	if site := siteOf(in.URL); site != "" {
		return site
	}
	tabs, err := s.b.Tabs(ctx)
	if err != nil {
		return ""
	}
	for _, tab := range tabs {
		if tab.Front {
			return siteOf(tab.URL)
		}
	}
	return ""
}

// noteFailure counts a failed call against its site and, from failuresAfter failures in a row,
// returns the fact: how many calls failed on the site and the latest errors.
func (k *Kit) noteFailure(ctx context.Context, s *session, in input, failure error) string {
	site := callSite(ctx, s, in)
	if site == "" {
		return ""
	}
	if s.failures == nil {
		s.failures = map[string][]string{}
	}
	s.failures[site] = append(s.failures[site], shorten(strings.Join(strings.Fields(failure.Error()), " "), failureChars))
	errs := s.failures[site]
	if len(errs) < failuresAfter {
		return ""
	}
	return fmt.Sprintf("\n[failures] %d calls in a row failed on %s: %s", len(errs), site, strings.Join(errs[len(errs)-failuresAfter:], "; "))
}

// profileNote is the profile of the host of address, once for each host in a task, and "" when
// it was shown or the host has none.
func (k *Kit) profileNote(t *helpers.Task, address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if t.Notes()[profiledNote+host] != "" {
		return ""
	}
	p, found, err := k.profiles.Get(host)
	if err != nil {
		return "\n[site profile " + host + " not read] " + err.Error()
	}
	if !found {
		return ""
	}
	t.Note(profiledNote+host, "1")
	if text := sites.Describe(host, p); text != "" {
		return "\n" + text
	}
	return ""
}

// noteSuccess clears a site's run of failures.
func (k *Kit) noteSuccess(ctx context.Context, s *session, in input) {
	if len(s.failures) == 0 {
		return
	}
	delete(s.failures, callSite(ctx, s, in))
}

// saveTabs keeps the URLs of t's open tabs, which a wake reopens, and notes the sites they
// show against the session's Chromium folder.
func (k *Kit) saveTabs(ctx context.Context, t *helpers.Task, s *session) error {
	tabs, err := s.b.Tabs(ctx)
	if err != nil {
		return err
	}
	var urls []string
	for _, tab := range tabs {
		if strings.HasPrefix(tab.URL, "http://") || strings.HasPrefix(tab.URL, "https://") {
			urls = append(urls, tab.URL)
		}
	}
	raw, err := json.Marshal(urls)
	if err != nil {
		return err
	}
	t.Note(tabsNote, string(raw))
	if sites := openSites(tabs); len(sites) > 0 {
		return recordSites(k.browsersDir, s.agent, s.folder, sites, time.Now())
	}
	return nil
}

// tab resolves the tab a call acts on and brings it to the front.
func (s *session) tab(ctx context.Context, id string) (*Tab, error) {
	if id != "" {
		return s.b.OtherTab(ctx, id)
	}
	return s.b.Tab(ctx, "")
}

// retriesStale names the tools dispatch runs once more when the page replaced its execution
// context under the first try. act, upload and download are left out: they change the page, and
// a batch never repeats. navigate retries inside itself.
var retriesStale = map[string]bool{"look": true, "find": true, "extract": true, "screenshot": true, "sign_in": true}

// dispatch runs the named tool.
func (k *Kit) dispatch(ctx context.Context, t *helpers.Task, s *session, name string, in input) (string, error) {
	return retryStale(name, func() (string, error) { return k.dispatchOnce(ctx, t, s, name, in) })
}

// retryStale runs try, and once more when tool name is in retriesStale and the first try failed
// on a replaced execution context.
func retryStale(name string, try func() (string, error)) (string, error) {
	out, err := try()
	if err != nil && retriesStale[name] && strings.Contains(err.Error(), staleContext) {
		return try()
	}
	return out, err
}

// dispatchOnce is one try of dispatch.
func (k *Kit) dispatchOnce(ctx context.Context, t *helpers.Task, s *session, name string, in input) (string, error) {
	if name == "tabs" {
		return k.tabs(ctx, s, in)
	}
	tab, err := s.tab(ctx, in.Tab)
	if err != nil {
		return "", err
	}
	switch name {
	case "look":
		if d := tab.dialogNow(); d != nil {
			return fmt.Sprintf("a %s dialog is open: %s\nanswer it with an act dialog action", d.Type, jsonString(d.Message)), nil
		}
		return tab.Look(ctx, in.Scope)
	case "act":
		return tab.Act(ctx, in.Actions)
	case "find":
		return tab.Find(ctx, in.Query)
	case "extract":
		return k.extract(ctx, s, tab, in)
	case "screenshot":
		return k.screenshot(ctx, t, tab, in)
	case "navigate":
		return k.navigate(ctx, tab, in)
	case "sign_in":
		return k.signIn(ctx, tab, in)
	case "download":
		return k.download(ctx, s, tab, in)
	case "upload":
		files, err := tab.Upload(ctx, in.Ref, in.Paths)
		if err != nil {
			return "", err
		}
		return "uploaded " + files, nil
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

// tabs lists, opens, switches and closes tabs.
func (k *Kit) tabs(ctx context.Context, s *session, in input) (string, error) {
	var note string
	switch {
	case in.Open != "":
		tab, err := s.b.NewTab(ctx, "about:blank")
		if err != nil {
			return "", err
		}
		d, err := tab.Navigate(ctx, in.Open, "load")
		if err != nil {
			return "", err
		}
		note = "opened " + tab.id
		if d != nil {
			note += "; the address started a download: " + describeDownload(*d)
		}
	case in.Switch != "":
		tab, err := s.b.OtherTab(ctx, in.Switch)
		if err != nil {
			return "", err
		}
		note = "in front: " + tab.id
	case in.Close != "":
		tabs, err := s.b.Tabs(ctx)
		if err != nil {
			return "", err
		}
		var id string
		for _, tab := range tabs {
			if tab.ID == in.Close || (len(in.Close) >= 4 && strings.HasPrefix(tab.ID, in.Close)) {
				id = tab.ID
			}
		}
		if id == "" {
			return "", fmt.Errorf("no tab %q", in.Close)
		}
		if err := s.b.CloseTab(ctx, id); err != nil {
			return "", err
		}
		note = "closed " + id
	}
	tabs, err := s.b.Tabs(ctx)
	if err != nil {
		return "", err
	}
	var lines []string
	if note != "" {
		lines = append(lines, note)
	}
	for _, tab := range tabs {
		mark := " "
		if tab.Front {
			mark = "*"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s %s", mark, tab.ID, tab.URL, jsonString(tab.Title)))
	}
	if len(tabs) == 0 {
		lines = append(lines, "no tabs open")
	}
	return strings.Join(lines, "\n"), nil
}

// describeDownload says where a finished download went.
func describeDownload(d Download) string {
	if d.Path == "" {
		return fmt.Sprintf("%s (%s)", d.Name, d.State)
	}
	return fmt.Sprintf("%s saved to %s", d.Name, d.Path)
}

// navigate loads a URL and returns the outline, or the download the URL turned into. It tries
// once more when the page replaced its execution context under the first try.
func (k *Kit) navigate(ctx context.Context, tab *Tab, in input) (string, error) {
	out, err := k.navigateOnce(ctx, tab, in)
	if err != nil && strings.Contains(err.Error(), staleContext) {
		return k.navigateOnce(ctx, tab, in)
	}
	return out, err
}

// navigateOnce is one try of navigate.
func (k *Kit) navigateOnce(ctx context.Context, tab *Tab, in input) (string, error) {
	if _, err := url.ParseRequestURI(in.URL); err != nil {
		return "", fmt.Errorf("url must be a full address: %w", err)
	}
	d, err := tab.Navigate(ctx, in.URL, in.Wait)
	if err != nil {
		return "", err
	}
	if d != nil {
		if d.State == "inProgress" {
			finished, err := tab.b.waitDownload(ctx, tab.b.DownloadCount()-1, downloadWait)
			if err != nil {
				return "", err
			}
			d = &finished
		}
		return "download: " + describeDownload(*d), nil
	}
	return tab.Look(ctx, "")
}

// download loads a URL or clicks a control and reports the file it saved; with neither it
// lists the downloads so far.
func (k *Kit) download(ctx context.Context, s *session, tab *Tab, in input) (string, error) {
	switch {
	case in.URL != "":
		return k.navigate(ctx, tab, in)
	case in.Ref != "":
		from := s.b.DownloadCount()
		if err := tab.Click(ctx, in.Ref, false); err != nil {
			return "", err
		}
		d, err := s.b.waitDownload(ctx, from, downloadWait)
		if err != nil {
			return "", fmt.Errorf("clicking %s: %w", in.Ref, err)
		}
		return "download: " + describeDownload(d), nil
	}
	list := s.b.Downloads()
	if len(list) == 0 {
		return "no downloads yet; downloads land in " + k.downloadsDir(), nil
	}
	var lines []string
	for _, d := range list {
		lines = append(lines, describeDownload(d))
	}
	return strings.Join(lines, "\n"), nil
}

// taskDir is the directory for files t's helper makes.
func (k *Kit) taskDir(t *helpers.Task) (string, error) {
	dir := filepath.Join(k.home, helpers.WorkDir, t.ID)
	return dir, k.host.AsDadi(func() error { return os.MkdirAll(dir, 0o755) })
}

// writeFile saves data at path, making its folder, as the dadi user so the terminal helper's
// shell can use what the browser helper made.
func (k *Kit) writeFile(path string, data []byte) error {
	return k.host.AsDadi(func() error {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o644)
	})
}

// extract reads rows and returns them inline, cut at extractMaxChars.
func (k *Kit) extract(ctx context.Context, s *session, tab *Tab, in input) (string, error) {
	got, err := tab.Extract(ctx, ExtractOptions{What: in.What, Format: in.Format, Pages: in.Pages, Next: in.Next})
	if err != nil {
		return "", err
	}
	s.rows = got.Rows
	return clipText(got.Data, extractMaxChars, "narrow what to read the rest"), nil
}

// screenshot saves a PNG and, when asked a question, has Dwar describe it.
func (k *Kit) screenshot(ctx context.Context, t *helpers.Task, tab *Tab, in input) (string, error) {
	params := map[string]any{"format": "png"}
	if in.Scope != "" {
		el, err := tab.resolve(ctx, in.Scope)
		if err != nil {
			return "", err
		}
		if err := tab.b.c.send(ctx, el.f.sess, "DOM.scrollIntoViewIfNeeded", map[string]any{"objectId": el.oid}, nil); err != nil {
			return "", err
		}
		x, y, w, h, err := el.box(ctx)
		if err != nil {
			return "", err
		}
		params["clip"] = map[string]any{"x": x, "y": y, "width": w, "height": h, "scale": 1}
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := tab.b.c.send(ctx, tab.sid, "Page.captureScreenshot", params, &shot); err != nil {
		return "", err
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return "", err
	}
	dir, err := k.taskDir(t)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano()))
	if err := k.writeFile(path, png); err != nil {
		return "", err
	}
	if in.Question == "" {
		return "screenshot saved to " + path + " (pass question to have it described)", nil
	}
	answer, err := k.describe(ctx, t, png, in.Question)
	if err != nil {
		return "", fmt.Errorf("screenshot saved to %s but could not be described: %w", path, err)
	}
	return "screenshot saved to " + path + "\n" + answer, nil
}

// describe asks Dwar's image describe route about a PNG.
func (k *Kit) describe(ctx context.Context, t *helpers.Task, png []byte, question string) (string, error) {
	body, err := json.Marshal(map[string]any{"image": map[string]string{"media_type": "image/png", "data": base64.StdEncoding.EncodeToString(png)}, "prompt": question})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.dwar+"/image/describe", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Dadi-Caller", "nas/helper/browser/"+t.SteeredBy)
	resp, err := k.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("dwar unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dwar %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	var out struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Description, nil
}

// openSites is the sites of the open tabs, sorted.
func openSites(tabs []TabInfo) []string {
	seen := map[string]bool{}
	var out []string
	for _, tab := range tabs {
		if site := siteOf(tab.URL); site != "" && !seen[site] {
			seen[site] = true
			out = append(out, site)
		}
	}
	sort.Strings(out)
	return out
}
