package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// commandTimeout bounds one protocol command.
	commandTimeout = 30 * time.Second
	// navigationTimeout bounds a navigation and the wait for its page to load.
	navigationTimeout = 45 * time.Second
	// contextWait is how long a frame may take to get its first script context.
	contextWait = 3 * time.Second
)

// Browser is one Chromium reached over one DevTools connection. Pages are attached through
// flattened sessions, and frames in other processes get sessions of their own.
type Browser struct {
	c            *conn
	downloadsDir string
	mu           sync.Mutex
	tabs         map[string]*Tab
	bySession    map[string]*Tab
	attaching    map[string]bool
	downloads    []*Download
	front        string
	changed      chan struct{}
}

// Download is one file Chromium saved or is saving, named by the page that offered it.
type Download struct {
	GUID  string
	URL   string
	Name  string
	Path  string
	State string
}

// frame is one frame of a tab. sess is the session its default script context lives in,
// which is the tab's own unless the frame runs in another process.
type frame struct {
	id     string
	parent string
	url    string
	sess   string
	ctx    int64
}

// dialog is a JavaScript dialog a page has open.
type dialog struct {
	Type    string
	Message string
	Default string
}

// Tab is one page: its persistent session (kept for the tab's life so the WebAuthn virtual
// authenticator stays on), its frames and what it has reported.
type Tab struct {
	b       *Browser
	id      string
	sid     string
	ready   chan struct{}
	mu      sync.Mutex
	frames  map[string]*frame
	main    string
	navs    int
	idles   int
	dlg     *dialog
	dlgCh   chan struct{}
	chooser chan fileChooser
	authID  string
	nextRef int
	last    map[string]int
	mouseX  float64
	mouseY  float64
}

type fileChooser struct {
	frameID string
	node    int64
}

// Connect connects to the DevTools websocket at url, points downloads at downloadsDir, and
// attaches to every page already open.
func Connect(ctx context.Context, url, downloadsDir string) (*Browser, error) {
	b := &Browser{
		downloadsDir: downloadsDir, tabs: map[string]*Tab{}, bySession: map[string]*Tab{},
		attaching: map[string]bool{}, changed: make(chan struct{}),
	}
	c, err := dial(url, b.onEvent)
	if err != nil {
		return nil, err
	}
	b.c = c
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return nil, fmt.Errorf("downloads dir: %w", err)
	}
	if err := c.send(ctx, "", "Browser.setDownloadBehavior", map[string]any{"behavior": "allowAndName", "downloadPath": downloadsDir, "eventsEnabled": true}, nil); err != nil {
		_ = c.close()
		return nil, err
	}
	if err := c.send(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true}, nil); err != nil {
		_ = c.close()
		return nil, err
	}
	var targets struct {
		TargetInfos []targetInfo `json:"targetInfos"`
	}
	if err := c.send(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		_ = c.close()
		return nil, err
	}
	for _, info := range targets.TargetInfos {
		if info.Type == "page" {
			if _, err := b.attach(ctx, info.TargetID); err != nil {
				_ = c.close()
				return nil, err
			}
		}
	}
	return b, nil
}

type targetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Title    string `json:"title"`
}

// Close ends the connection; the browser process is stopped by its host.
func (b *Browser) Close() error {
	return b.c.close()
}

// Closed reports whether the connection to Chromium has ended.
func (b *Browser) Closed() bool {
	return b.c.closed()
}

// signal wakes everything waiting on a browser change (a download, a new tab).
func (b *Browser) signal() {
	b.mu.Lock()
	close(b.changed)
	b.changed = make(chan struct{})
	b.mu.Unlock()
}

// attach opens the persistent session for page target id and prepares it. A target attached
// twice (by Connect and by its targetCreated event) is attached once.
func (b *Browser) attach(ctx context.Context, id string) (*Tab, error) {
	b.mu.Lock()
	if t, ok := b.tabs[id]; ok {
		b.mu.Unlock()
		<-t.ready
		return t, nil
	}
	if b.attaching[id] {
		b.mu.Unlock()
		return b.waitTab(ctx, id)
	}
	b.attaching[id] = true
	b.mu.Unlock()
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.c.send(ctx, "", "Target.attachToTarget", map[string]any{"targetId": id, "flatten": true}, &out); err != nil {
		b.mu.Lock()
		delete(b.attaching, id)
		b.mu.Unlock()
		return nil, err
	}
	t := &Tab{b: b, id: id, sid: out.SessionID, ready: make(chan struct{}), frames: map[string]*frame{}, dlgCh: make(chan struct{}), chooser: make(chan fileChooser, 1), last: map[string]int{}}
	b.mu.Lock()
	b.tabs[id] = t
	b.bySession[t.sid] = t
	delete(b.attaching, id)
	b.mu.Unlock()
	defer close(t.ready)
	if err := t.initSession(ctx, t.sid); err != nil {
		return nil, err
	}
	var tree struct {
		FrameTree frameTree `json:"frameTree"`
	}
	if err := b.c.send(ctx, t.sid, "Page.getFrameTree", nil, &tree); err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.main = tree.FrameTree.Frame.ID
	t.mu.Unlock()
	t.seed(tree.FrameTree, t.sid)
	b.signal()
	return t, nil
}

// waitTab waits for another goroutine's attach of id to finish.
func (b *Browser) waitTab(ctx context.Context, id string) (*Tab, error) {
	for {
		b.mu.Lock()
		t, ok := b.tabs[id]
		wait := b.changed
		b.mu.Unlock()
		if ok {
			<-t.ready
			return t, nil
		}
		select {
		case <-wait:
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type frameTree struct {
	Frame struct {
		ID       string `json:"id"`
		ParentID string `json:"parentId"`
		URL      string `json:"url"`
	} `json:"frame"`
	ChildFrames []frameTree `json:"childFrames"`
}

// seed records the frames of tree as living in session sid unless already known.
func (t *Tab) seed(tree frameTree, sid string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var walk func(n frameTree)
	walk = func(n frameTree) {
		f := t.frames[n.Frame.ID]
		if f == nil {
			f = &frame{id: n.Frame.ID, sess: sid}
			t.frames[f.id] = f
		}
		if n.Frame.ParentID != "" {
			f.parent = n.Frame.ParentID
		}
		f.url = n.Frame.URL
		for _, c := range n.ChildFrames {
			walk(c)
		}
	}
	walk(tree)
}

// initSession enables the domains Nas reads in session sid and auto-attaches the frames
// that run in other processes.
func (t *Tab) initSession(ctx context.Context, sid string) error {
	for _, step := range []struct {
		method string
		params any
	}{
		{"Page.enable", nil},
		{"DOM.enable", nil},
		{"Runtime.enable", nil},
		{"Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
		{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true}},
	} {
		if err := t.b.c.send(ctx, sid, step.method, step.params, nil); err != nil {
			return err
		}
	}
	return nil
}

// onEvent routes a protocol event to the browser or the tab owning its session.
func (b *Browser) onEvent(sid, method string, raw json.RawMessage) {
	if sid != "" {
		b.mu.Lock()
		t := b.bySession[sid]
		b.mu.Unlock()
		if t != nil {
			t.handle(sid, method, raw)
		}
		return
	}
	switch method {
	case "Target.targetCreated":
		var p struct {
			TargetInfo targetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(raw, &p) == nil && p.TargetInfo.Type == "page" {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
				defer cancel()
				_, _ = b.attach(ctx, p.TargetInfo.TargetID)
			}()
		}
	case "Target.targetDestroyed":
		var p struct {
			TargetID string `json:"targetId"`
		}
		if json.Unmarshal(raw, &p) == nil {
			b.mu.Lock()
			if t, ok := b.tabs[p.TargetID]; ok {
				delete(b.tabs, p.TargetID)
				delete(b.bySession, t.sid)
			}
			b.mu.Unlock()
			b.signal()
		}
	case "Browser.downloadWillBegin":
		var p struct {
			GUID string `json:"guid"`
			URL  string `json:"url"`
			Name string `json:"suggestedFilename"`
		}
		if json.Unmarshal(raw, &p) == nil {
			b.mu.Lock()
			b.downloads = append(b.downloads, &Download{GUID: p.GUID, URL: p.URL, Name: p.Name, State: "inProgress"})
			b.mu.Unlock()
			b.signal()
		}
	case "Browser.downloadProgress":
		var p struct {
			GUID  string `json:"guid"`
			State string `json:"state"`
		}
		if json.Unmarshal(raw, &p) == nil && p.State != "inProgress" {
			b.finishDownload(p.GUID, p.State)
		}
	}
}

// finishDownload gives a finished download its own file name in the downloads dir (Chromium
// saves it under its GUID), or records that it was canceled.
func (b *Browser) finishDownload(guid, state string) {
	b.mu.Lock()
	var d *Download
	for _, c := range b.downloads {
		if c.GUID == guid {
			d = c
		}
	}
	b.mu.Unlock()
	if d == nil {
		return
	}
	path, err := "", error(nil)
	if state == "completed" {
		path, err = uniquePath(b.downloadsDir, d.Name)
		if err == nil {
			err = os.Rename(filepath.Join(b.downloadsDir, guid), path)
		}
		if err != nil {
			state = "failed: " + err.Error()
		}
	}
	b.mu.Lock()
	d.Path, d.State = path, state
	b.mu.Unlock()
	b.signal()
}

// uniquePath is dir/name, or dir/name with " (n)" before the extension when that exists.
func uniquePath(dir, name string) (string, error) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || name == "." || name == "/" {
		name = "download"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 0; n < 1000; n++ {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		path := filepath.Join(dir, candidate)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no free file name for %s in %s", name, dir)
}

// handle applies one session event to the tab's frames and signals.
func (t *Tab) handle(sid, method string, raw json.RawMessage) {
	switch method {
	case "Page.frameAttached":
		var q struct {
			FrameID       string `json:"frameId"`
			ParentFrameID string `json:"parentFrameId"`
		}
		if json.Unmarshal(raw, &q) == nil {
			t.mu.Lock()
			f := t.frames[q.FrameID]
			if f == nil {
				f = &frame{id: q.FrameID, sess: sid}
				t.frames[q.FrameID] = f
			}
			f.parent = q.ParentFrameID
			t.mu.Unlock()
		}
	case "Page.frameNavigated":
		var p struct {
			Frame struct {
				ID       string `json:"id"`
				ParentID string `json:"parentId"`
				URL      string `json:"url"`
			} `json:"frame"`
		}
		if json.Unmarshal(raw, &p) == nil {
			t.mu.Lock()
			f := t.frames[p.Frame.ID]
			if f == nil {
				f = &frame{id: p.Frame.ID, sess: sid}
				t.frames[p.Frame.ID] = f
			}
			f.url = p.Frame.URL
			if p.Frame.ParentID != "" {
				f.parent = p.Frame.ParentID
			}
			if sid == t.sid && p.Frame.ParentID == "" {
				t.main = p.Frame.ID
				t.navs++
			}
			t.mu.Unlock()
		}
	case "Page.navigatedWithinDocument":
		var p struct {
			FrameID string `json:"frameId"`
			URL     string `json:"url"`
		}
		if json.Unmarshal(raw, &p) == nil {
			t.mu.Lock()
			if f := t.frames[p.FrameID]; f != nil {
				f.url = p.URL
				if p.FrameID == t.main {
					t.navs++
				}
			}
			t.mu.Unlock()
		}
	case "Page.frameDetached":
		var p struct {
			FrameID string `json:"frameId"`
			Reason  string `json:"reason"`
		}
		if json.Unmarshal(raw, &p) == nil && p.Reason != "swap" {
			t.mu.Lock()
			delete(t.frames, p.FrameID)
			t.mu.Unlock()
		}
	case "Page.lifecycleEvent":
		var p struct {
			FrameID string `json:"frameId"`
			Name    string `json:"name"`
		}
		if json.Unmarshal(raw, &p) == nil && p.Name == "networkIdle" {
			t.mu.Lock()
			if p.FrameID == t.main {
				t.idles++
			}
			t.mu.Unlock()
		}
	case "Runtime.executionContextCreated":
		var p struct {
			Context struct {
				ID      int64 `json:"id"`
				AuxData struct {
					FrameID   string `json:"frameId"`
					IsDefault bool   `json:"isDefault"`
				} `json:"auxData"`
			} `json:"context"`
		}
		if json.Unmarshal(raw, &p) == nil && p.Context.AuxData.IsDefault {
			t.mu.Lock()
			f := t.frames[p.Context.AuxData.FrameID]
			if f == nil {
				f = &frame{id: p.Context.AuxData.FrameID}
				t.frames[f.id] = f
			}
			f.sess, f.ctx = sid, p.Context.ID
			t.mu.Unlock()
		}
	case "Runtime.executionContextDestroyed":
		var p struct {
			ID int64 `json:"executionContextId"`
		}
		if json.Unmarshal(raw, &p) == nil {
			t.mu.Lock()
			for _, f := range t.frames {
				if f.ctx == p.ID && f.sess == sid {
					f.ctx = 0
				}
			}
			t.mu.Unlock()
		}
	case "Runtime.executionContextsCleared":
		t.mu.Lock()
		for _, f := range t.frames {
			if f.sess == sid {
				f.ctx = 0
			}
		}
		t.mu.Unlock()
	case "Page.javascriptDialogOpening":
		var p struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Default string `json:"defaultPrompt"`
		}
		if json.Unmarshal(raw, &p) == nil {
			t.mu.Lock()
			t.dlg = &dialog{Type: p.Type, Message: p.Message, Default: p.Default}
			close(t.dlgCh)
			t.mu.Unlock()
		}
	case "Page.javascriptDialogClosed":
		t.mu.Lock()
		if t.dlg != nil {
			t.dlg = nil
			t.dlgCh = make(chan struct{})
		}
		t.mu.Unlock()
	case "Page.fileChooserOpened":
		var p struct {
			FrameID string `json:"frameId"`
			Node    int64  `json:"backendNodeId"`
		}
		if json.Unmarshal(raw, &p) == nil {
			select {
			case t.chooser <- fileChooser{p.FrameID, p.Node}:
			default:
			}
		}
	case "Target.attachedToTarget":
		var p struct {
			SessionID  string     `json:"sessionId"`
			TargetInfo targetInfo `json:"targetInfo"`
		}
		if json.Unmarshal(raw, &p) == nil && p.TargetInfo.Type == "iframe" {
			t.b.mu.Lock()
			t.b.bySession[p.SessionID] = t
			t.b.mu.Unlock()
			t.mu.Lock()
			if f := t.frames[p.TargetInfo.TargetID]; f == nil {
				t.frames[p.TargetInfo.TargetID] = &frame{id: p.TargetInfo.TargetID, sess: p.SessionID, url: p.TargetInfo.URL}
			}
			t.mu.Unlock()
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
				defer cancel()
				var tree struct {
					FrameTree frameTree `json:"frameTree"`
				}
				err := t.initSession(ctx, p.SessionID)
				if err == nil {
					err = t.b.c.send(ctx, p.SessionID, "Page.getFrameTree", nil, &tree)
				}
				if err != nil {
					slog.Error("browser frame session not set up", "code", "internal_error", "frame", p.TargetInfo.TargetID, "err", err)
					return
				}
				t.seed(tree.FrameTree, p.SessionID)
			}()
		}
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &p) == nil {
			t.b.mu.Lock()
			delete(t.b.bySession, p.SessionID)
			t.b.mu.Unlock()
			t.mu.Lock()
			for _, f := range t.frames {
				if f.sess == p.SessionID {
					f.ctx = 0
				}
			}
			t.mu.Unlock()
		}
	}
}

// Tabs is every open page in the order Chromium lists them, with its URL and title.
func (b *Browser) Tabs(ctx context.Context) ([]TabInfo, error) {
	var targets struct {
		TargetInfos []targetInfo `json:"targetInfos"`
	}
	if err := b.c.send(ctx, "", "Target.getTargets", nil, &targets); err != nil {
		return nil, err
	}
	b.mu.Lock()
	front := b.front
	b.mu.Unlock()
	var out []TabInfo
	for _, info := range targets.TargetInfos {
		if info.Type == "page" {
			out = append(out, TabInfo{ID: info.TargetID, URL: info.URL, Title: info.Title, Front: info.TargetID == front})
		}
	}
	return out, nil
}

// TabInfo is one open page as the helper lists it.
type TabInfo struct {
	ID    string
	URL   string
	Title string
	Front bool
}

// Tab is the page with target id (any page when id is empty, the front one first), brought
// to the front: Thaali's live view shows whichever tab Chromium has in front.
func (b *Browser) Tab(ctx context.Context, id string) (*Tab, error) {
	if id == "" {
		b.mu.Lock()
		id = b.front
		b.mu.Unlock()
	}
	if id == "" {
		tabs, err := b.Tabs(ctx)
		if err != nil {
			return nil, err
		}
		if len(tabs) == 0 {
			return b.NewTab(ctx, "about:blank")
		}
		id = tabs[len(tabs)-1].ID
	}
	t, err := b.attach(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := t.Front(ctx); err != nil {
		return nil, err
	}
	return t, nil
}

// OtherTab resolves a tab given by a full target id or a unique prefix of at least 4 characters.
func (b *Browser) OtherTab(ctx context.Context, id string) (*Tab, error) {
	tabs, err := b.Tabs(ctx)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, t := range tabs {
		if t.ID == id || (len(id) >= 4 && strings.HasPrefix(t.ID, id)) {
			found = append(found, t.ID)
		}
	}
	if len(found) != 1 {
		return nil, fmt.Errorf("tab %q matches %d open tabs; list them with tabs", id, len(found))
	}
	return b.Tab(ctx, found[0])
}

// NewTab opens url in a new tab and makes it the front tab.
func (b *Browser) NewTab(ctx context.Context, url string) (*Tab, error) {
	var out struct {
		TargetID string `json:"targetId"`
	}
	if err := b.c.send(ctx, "", "Target.createTarget", map[string]any{"url": url}, &out); err != nil {
		return nil, err
	}
	t, err := b.attach(ctx, out.TargetID)
	if err != nil {
		return nil, err
	}
	return t, t.Front(ctx)
}

// CloseTab closes the page with target id.
func (b *Browser) CloseTab(ctx context.Context, id string) error {
	var out struct {
		Success bool `json:"success"`
	}
	if err := b.c.send(ctx, "", "Target.closeTarget", map[string]any{"targetId": id}, &out); err != nil {
		return err
	}
	b.mu.Lock()
	if b.front == id {
		b.front = ""
	}
	b.mu.Unlock()
	return nil
}

// Front brings the tab to the front and remembers it as the one being driven.
func (t *Tab) Front(ctx context.Context) error {
	if err := t.b.c.send(ctx, t.sid, "Page.bringToFront", nil, nil); err != nil {
		return err
	}
	t.b.mu.Lock()
	t.b.front = t.id
	t.b.mu.Unlock()
	return nil
}

// FrontID is the target id of the tab last brought to the front.
func (b *Browser) FrontID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.front
}

// ID is the tab's target id.
func (t *Tab) ID() string { return t.id }

// framesInOrder is the tab's frames with a script context, the main frame first and each
// frame before its children, in the order they were attached.
func (t *Tab) framesInOrder(ctx context.Context) ([]*frame, error) {
	deadline := time.Now().Add(contextWait)
	for {
		t.mu.Lock()
		var out []*frame
		var walk func(id string)
		kids := map[string][]*frame{}
		for _, f := range t.frames {
			kids[f.parent] = append(kids[f.parent], f)
		}
		for _, list := range kids {
			sort.Slice(list, func(i, j int) bool { return list[i].id < list[j].id })
		}
		walk = func(id string) {
			if f := t.frames[id]; f != nil && f.ctx != 0 {
				cp := *f
				out = append(out, &cp)
			}
			for _, c := range kids[id] {
				walk(c.id)
			}
		}
		walk(t.main)
		mainReady := len(out) > 0 && out[0].id == t.main
		t.mu.Unlock()
		if mainReady {
			return out, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the page has no script context yet; try again after it loads")
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// dialogNow is the dialog the page has open, or nil.
func (t *Tab) dialogNow() *dialog {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dlg
}

// dialogSignal is closed once the page opens a dialog.
func (t *Tab) dialogSignal() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dlgCh
}

// HandleDialog accepts or dismisses the open dialog, answering a prompt with text.
func (t *Tab) HandleDialog(ctx context.Context, accept bool, text string) error {
	if t.dialogNow() == nil {
		return errors.New("no dialog is open")
	}
	return t.b.c.send(ctx, t.sid, "Page.handleJavaScriptDialog", map[string]any{"accept": accept, "promptText": text}, nil)
}

// sendOrDialog runs a command that a dialog opened by it would hold up (a click's mouse
// release): it returns when the command answers or when the page opens a dialog.
func (t *Tab) sendOrDialog(ctx context.Context, method string, params any) error {
	signal := t.dialogSignal()
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		done <- t.b.c.send(cctx, t.sid, method, params, nil)
	}()
	select {
	case err := <-done:
		return err
	case <-signal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Downloads is a copy of the downloads Chromium has reported, oldest first.
func (b *Browser) Downloads() []Download {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Download, len(b.downloads))
	for i, d := range b.downloads {
		out[i] = *d
	}
	return out
}

// waitDownload returns the first download after index from, once it has finished or
// failed, or an error when none starts within wait.
func (b *Browser) waitDownload(ctx context.Context, from int, wait time.Duration) (Download, error) {
	deadline := time.Now().Add(wait)
	for {
		b.mu.Lock()
		if len(b.downloads) > from {
			d := *b.downloads[from]
			ch := b.changed
			b.mu.Unlock()
			if d.State != "inProgress" {
				return d, nil
			}
			select {
			case <-ch:
			case <-ctx.Done():
				return d, ctx.Err()
			case <-time.After(time.Until(deadline)):
				return d, fmt.Errorf("download %s did not finish within %s", d.Name, wait)
			}
			continue
		}
		ch := b.changed
		b.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return Download{}, ctx.Err()
		case <-time.After(time.Until(deadline)):
			return Download{}, errors.New("no download started")
		}
	}
}

// DownloadCount is how many downloads have started, for use as the from of waitDownload.
func (b *Browser) DownloadCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.downloads)
}
