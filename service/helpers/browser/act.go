package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// actionableTimeout is how long an element may take to become visible, stable and
	// uncovered before the action on it fails.
	actionableTimeout = 8 * time.Second
	// stablePause is how long an element's rectangle must stay put to count as stable.
	stablePause = 60 * time.Millisecond
	// clickSettle is how long after a click or key the page is given to start a navigation.
	clickSettle = 500 * time.Millisecond
	// idleWait bounds the wait for the network to go quiet after "networkidle".
	idleWait = 10 * time.Second
)

// Action is one step of an act batch. Type is one of click, type, select, key, scroll,
// check, upload, wait or dialog; the other fields are the arguments that type reads.
type Action struct {
	Type      string   `json:"type"`
	Ref       string   `json:"ref"`
	Text      string   `json:"text"`
	Value     string   `json:"value"`
	Key       string   `json:"key"`
	Submit    bool     `json:"submit"`
	Double    bool     `json:"double"`
	Checked   *bool    `json:"checked"`
	Paths     []string `json:"paths"`
	Direction string   `json:"direction"`
	Amount    int      `json:"amount"`
	Ms        int      `json:"ms"`
	Accept    *bool    `json:"accept"`
}

// String names the action without its text, which may be a secret.
func (a Action) String() string {
	switch {
	case a.Ref != "":
		return a.Type + " " + a.Ref
	case a.Key != "":
		return a.Type + " " + a.Key
	}
	return a.Type
}

// actionability says what an action needs of its element.
type actionability struct {
	enabled bool
	writes  bool
	hit     bool
}

type checkState struct {
	Gone     bool       `json:"gone"`
	Visible  bool       `json:"visible"`
	Disabled bool       `json:"disabled"`
	Readonly bool       `json:"readonly"`
	Covered  string     `json:"covered"`
	Rect     [4]float64 `json:"rect"`
}

// ready scrolls el into view and waits until it is visible, stable, enabled (when need says)
// and not covered, then returns where to click it in page coordinates. A disabled or
// read-only element fails at once; the rest fail after actionableTimeout with the reason.
func (el element) ready(ctx context.Context, ref string, need actionability) (float64, float64, error) {
	deadline := time.Now().Add(actionableTimeout)
	var prev [4]float64
	steady := false
	reason := "not visible"
	for {
		if err := el.t.b.c.send(ctx, el.f.sess, "DOM.scrollIntoViewIfNeeded", map[string]any{"objectId": el.oid}, nil); err != nil {
			reason = err.Error()
		} else {
			var st checkState
			if err := el.call(ctx, checkFn, &st); err != nil {
				return 0, 0, err
			}
			switch {
			case st.Gone:
				return 0, 0, staleRefError{ref: ref}
			case need.enabled && st.Disabled:
				return 0, 0, fmt.Errorf("ref %s is disabled; it does nothing until the page enables it", ref)
			case need.writes && st.Readonly:
				return 0, 0, fmt.Errorf("ref %s is read-only", ref)
			case !st.Visible:
				reason = "not visible"
				steady = false
			case need.hit && st.Covered != "":
				reason = "covered by " + st.Covered
				steady = false
			case steady && st.Rect == prev:
				return el.point(ctx)
			default:
				reason = "still moving"
				steady = true
			}
			prev = st.Rect
		}
		if time.Now().After(deadline) {
			return 0, 0, fmt.Errorf("ref %s cannot be used: %s after %s", ref, reason, actionableTimeout)
		}
		select {
		case <-time.After(stablePause):
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		}
	}
}

// point is the centre of el's first box in the coordinates of the page's viewport.
func (el element) point(ctx context.Context) (float64, float64, error) {
	x, y, w, h, err := el.box(ctx)
	return x + w/2, y + h/2, err
}

// box is el's first box in the coordinates of the page's viewport, adding the offset of every
// frame in another process between el and the page.
func (el element) box(ctx context.Context) (x, y, w, h float64, err error) {
	var quads struct {
		Quads [][]float64 `json:"quads"`
	}
	if err := el.t.b.c.send(ctx, el.f.sess, "DOM.getContentQuads", map[string]any{"objectId": el.oid}, &quads); err != nil {
		return 0, 0, 0, 0, err
	}
	found := false
	for _, q := range quads.Quads {
		if len(q) < 8 {
			continue
		}
		if q[2]-q[0] > 0 && q[5]-q[1] > 0 {
			x, y, w, h = q[0], q[1], q[2]-q[0], q[5]-q[1]
			found = true
			break
		}
	}
	if !found {
		return 0, 0, 0, 0, errors.New("the element has no box on the page")
	}
	cur := el.f
	for cur.sess != el.t.sid {
		parent, ok := el.t.frameByID(cur.parent)
		if !ok {
			return 0, 0, 0, 0, errors.New("a frame lost its parent while locating the element")
		}
		var owner struct {
			BackendNodeID int64 `json:"backendNodeId"`
		}
		if err := el.t.b.c.send(ctx, parent.sess, "DOM.getFrameOwner", map[string]any{"frameId": cur.id}, &owner); err != nil {
			return 0, 0, 0, 0, err
		}
		var model struct {
			Model struct {
				Content []float64 `json:"content"`
			} `json:"model"`
		}
		if err := el.t.b.c.send(ctx, parent.sess, "DOM.getBoxModel", map[string]any{"backendNodeId": owner.BackendNodeID}, &model); err != nil {
			return 0, 0, 0, 0, err
		}
		if len(model.Model.Content) < 2 {
			return 0, 0, 0, 0, errors.New("a frame has no box on the page")
		}
		x, y = x+model.Model.Content[0], y+model.Model.Content[1]
		cur = parent
	}
	return x, y, w, h, nil
}

// mouse sends one mouse event on the tab's session at page coordinates.
func (t *Tab) mouse(ctx context.Context, kind string, x, y float64, clicks int) error {
	params := map[string]any{"type": kind, "x": x, "y": y, "button": "left", "clickCount": clicks}
	if kind == "mouseMoved" {
		params["button"], params["clickCount"] = "none", 0
	}
	t.mu.Lock()
	t.mouseX, t.mouseY = x, y
	t.mu.Unlock()
	return t.sendOrDialog(ctx, "Input.dispatchMouseEvent", params)
}

// clickAt presses and releases the left button at page coordinates clicks times in a row.
func (t *Tab) clickAt(ctx context.Context, x, y float64, clicks int) error {
	if err := t.mouse(ctx, "mouseMoved", x, y, 0); err != nil {
		return err
	}
	for n := 1; n <= clicks; n++ {
		if err := t.mouse(ctx, "mousePressed", x, y, n); err != nil {
			return err
		}
		if err := t.mouse(ctx, "mouseReleased", x, y, n); err != nil {
			return err
		}
	}
	return nil
}

// Click clicks the element at ref once it is actionable, then lets the page start a
// navigation (waiting for that document to load) or redraw in place.
func (t *Tab) Click(ctx context.Context, ref string, double bool) error {
	el, err := t.resolve(ctx, ref)
	if err != nil {
		return err
	}
	x, y, err := el.ready(ctx, ref, actionability{enabled: true, hit: true})
	if err != nil {
		return err
	}
	clicks := 1
	if double {
		clicks = 2
	}
	return t.settled(ctx, func() error { return t.clickAt(ctx, x, y, clicks) })
}

// settled runs trigger, then waits clickSettle for the main frame to navigate; a navigation
// is waited out until its document has loaded.
func (t *Tab) settled(ctx context.Context, trigger func() error) error {
	t.mu.Lock()
	before := t.navs
	t.mu.Unlock()
	if err := trigger(); err != nil {
		return err
	}
	deadline := time.Now().Add(clickSettle)
	for time.Now().Before(deadline) {
		if t.dialogNow() != nil {
			return nil
		}
		t.mu.Lock()
		moved := t.navs != before
		t.mu.Unlock()
		if moved {
			return t.waitLoad(ctx, "load")
		}
		select {
		case <-time.After(25 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// waitLoad waits for the main frame's document to reach the state: "domcontentloaded",
// "load" or "networkidle" (load, then the network quiet for 500ms, waited for up to idleWait).
func (t *Tab) waitLoad(ctx context.Context, state string) error {
	t.mu.Lock()
	idles := t.idles
	t.mu.Unlock()
	deadline := time.Now().Add(navigationTimeout)
	for {
		if t.dialogNow() != nil {
			return nil
		}
		frames, err := t.framesInOrder(ctx)
		if err == nil {
			var ready string
			if err := t.eval(ctx, frames[0], "document.readyState", &ready); err == nil {
				if ready == "complete" || (state == "domcontentloaded" && ready == "interactive") {
					break
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the page did not finish loading within %s", navigationTimeout)
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if state != "networkidle" {
		return nil
	}
	idleDeadline := time.Now().Add(idleWait)
	for time.Now().Before(idleDeadline) {
		t.mu.Lock()
		quiet := t.idles > idles
		t.mu.Unlock()
		if quiet {
			return nil
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Type replaces the content of the field at ref with text, the way typing does: the field is
// focused and selected and the text inserted as input, so frameworks and rich editors see
// real events. submit presses Enter afterwards. It returns a note when the field kept a
// different value than given (a mask or length limit).
func (t *Tab) Type(ctx context.Context, ref, text string, submit bool) (string, error) {
	el, err := t.resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	if _, _, err := el.ready(ctx, ref, actionability{enabled: true, writes: true}); err != nil {
		return "", err
	}
	var kind string
	if err := el.call(ctx, prepareFillFn, &kind); err != nil {
		return "", fmt.Errorf("ref %s: %w", ref, err)
	}
	switch {
	case kind == "set":
		if err := el.call(ctx, setValueFn, nil, text); err != nil {
			return "", err
		}
	case text == "":
		if err := t.press(ctx, "Backspace"); err != nil {
			return "", err
		}
	default:
		if err := t.b.c.send(ctx, t.sid, "Input.insertText", map[string]any{"text": text}, nil); err != nil {
			return "", err
		}
	}
	var now struct {
		Secret bool   `json:"secret"`
		Value  string `json:"value"`
		Length int    `json:"length"`
	}
	if err := el.call(ctx, readValueFn, &now); err != nil {
		return "", err
	}
	note := ""
	switch {
	case now.Secret && now.Length != len([]rune(text)):
		note = fmt.Sprintf("ref %s kept %d characters of the %d typed", ref, now.Length, len([]rune(text)))
	case !now.Secret && strings.TrimRight(now.Value, "\n") != strings.TrimRight(text, "\n") && kind != "editable":
		note = fmt.Sprintf("ref %s now reads %s", ref, jsonString(clipText(now.Value, 80, "")))
	}
	if submit {
		return note, t.settled(ctx, func() error { return t.press(ctx, "Enter") })
	}
	return note, nil
}

// Select picks the option of the select at ref whose value or label equals want.
func (t *Tab) Select(ctx context.Context, ref, want string) (string, error) {
	el, err := t.resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	if _, _, err := el.ready(ctx, ref, actionability{enabled: true}); err != nil {
		return "", err
	}
	var label string
	if err := el.call(ctx, selectFn, &label, want); err != nil {
		return "", fmt.Errorf("ref %s: %w", ref, err)
	}
	return label, nil
}

// Check sets the checkbox, radio or switch at ref to checked by clicking it once if it differs.
func (t *Tab) Check(ctx context.Context, ref string, checked bool) error {
	el, err := t.resolve(ctx, ref)
	if err != nil {
		return err
	}
	var now bool
	if err := el.call(ctx, checkedFn, &now); err != nil {
		return fmt.Errorf("ref %s: %w", ref, err)
	}
	if now == checked {
		return nil
	}
	if err := t.Click(ctx, ref, false); err != nil {
		return err
	}
	el, err = t.resolve(ctx, ref)
	if err != nil {
		return err
	}
	if err := el.call(ctx, checkedFn, &now); err != nil {
		return err
	}
	if now != checked {
		return fmt.Errorf("ref %s is still %s after the click", ref, map[bool]string{true: "checked", false: "unchecked"}[now])
	}
	return nil
}

// Press presses a key or chord on the element at ref (focused first) or on whatever has focus.
func (t *Tab) Press(ctx context.Context, key, ref string) error {
	if ref != "" {
		el, err := t.resolve(ctx, ref)
		if err != nil {
			return err
		}
		if err := el.call(ctx, "function(){this.focus()}", nil); err != nil {
			return err
		}
	}
	return t.settled(ctx, func() error { return t.press(ctx, key) })
}

// Scroll scrolls the element at ref into view, or the page by amount pixels up or down, or
// to its top or bottom.
func (t *Tab) Scroll(ctx context.Context, ref, direction string, amount int) error {
	if ref != "" {
		el, err := t.resolve(ctx, ref)
		if err != nil {
			return err
		}
		return t.b.c.send(ctx, el.f.sess, "DOM.scrollIntoViewIfNeeded", map[string]any{"objectId": el.oid}, nil)
	}
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return err
	}
	if amount == 0 {
		amount = 600
	}
	switch direction {
	case "top":
		return t.eval(ctx, frames[0], "window.scrollTo(0, 0)", nil)
	case "bottom":
		return t.eval(ctx, frames[0], "window.scrollTo(0, document.documentElement.scrollHeight)", nil)
	case "down", "up":
		if direction == "up" {
			amount = -amount
		}
		var size [2]float64
		if err := t.eval(ctx, frames[0], "[innerWidth, innerHeight]", &size); err != nil {
			return err
		}
		return t.b.c.send(ctx, t.sid, "Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": size[0] / 2, "y": size[1] / 2, "deltaX": 0, "deltaY": amount}, nil)
	}
	return fmt.Errorf("scroll needs a ref, or direction up, down, top or bottom (got %q)", direction)
}

// WaitText waits until text appears in the page, up to ms milliseconds (default 10000).
func (t *Tab) WaitText(ctx context.Context, text string, ms int) error {
	if ms == 0 {
		ms = 10_000
	}
	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	for {
		body, err := t.pageText(ctx)
		if err == nil && strings.Contains(strings.ToLower(body), strings.ToLower(text)) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%q did not appear within %dms", text, ms)
		}
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Do runs one action of a batch and returns a note worth telling the helper, if any.
func (t *Tab) Do(ctx context.Context, a Action) (string, error) {
	switch a.Type {
	case "click":
		return "", t.Click(ctx, a.Ref, a.Double)
	case "type":
		return t.Type(ctx, a.Ref, a.Text, a.Submit)
	case "select":
		label, err := t.Select(ctx, a.Ref, a.Value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s set to %s", a.Ref, jsonString(label)), nil
	case "key":
		return "", t.Press(ctx, a.Key, a.Ref)
	case "scroll":
		return "", t.Scroll(ctx, a.Ref, a.Direction, a.Amount)
	case "check":
		checked := true
		if a.Checked != nil {
			checked = *a.Checked
		}
		return "", t.Check(ctx, a.Ref, checked)
	case "upload":
		files, err := t.Upload(ctx, a.Ref, a.Paths)
		if err != nil {
			return "", err
		}
		return "uploaded " + files, nil
	case "wait":
		if a.Text != "" {
			return "", t.WaitText(ctx, a.Text, a.Ms)
		}
		select {
		case <-time.After(time.Duration(a.Ms) * time.Millisecond):
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	case "dialog":
		accept := a.Accept == nil || *a.Accept
		return "", t.HandleDialog(ctx, accept, a.Text)
	}
	return "", fmt.Errorf("unknown action type %q (click, type, select, key, scroll, check, upload, wait, dialog)", a.Type)
}

// changedLimit is how many new outline lines an act result lists.
const changedLimit = 60

// Act runs the batch in order on the tab and reports what changed: the new URL, new tabs,
// downloads, an open dialog and the outline lines that are new. It stops at the first action
// that fails and never repeats it, saying which actions had already run.
func (t *Tab) Act(ctx context.Context, actions []Action) (string, error) {
	if len(actions) == 0 {
		return "", errors.New("actions is empty")
	}
	t.mu.Lock()
	known := t.last != nil
	t.mu.Unlock()
	if !known {
		if _, err := t.outline(ctx, ""); err != nil {
			return "", err
		}
	}
	urlBefore := t.URL()
	tabsBefore, err := t.b.Tabs(ctx)
	if err != nil {
		return "", err
	}
	downloadsBefore := t.b.DownloadCount()
	var notes []string
	var failure error
	done := 0
	for i, a := range actions {
		if d := t.dialogNow(); d != nil && a.Type != "dialog" {
			failure = fmt.Errorf("a %s dialog is open (%s); answer it with a dialog action first", d.Type, jsonString(d.Message))
			break
		}
		note, err := t.Do(ctx, a)
		if note != "" {
			notes = append(notes, note)
		}
		if err != nil {
			failure = fmt.Errorf("action %d (%s) failed: %w", i+1, a, err)
			break
		}
		done++
	}
	var b strings.Builder
	if failure != nil {
		fmt.Fprintf(&b, "%s\n%d of %d actions ran before it; nothing was repeated\n", failure, done, len(actions))
	} else {
		fmt.Fprintf(&b, "ok: %d of %d actions ran\n", done, len(actions))
	}
	for _, n := range notes {
		b.WriteString(n + "\n")
	}
	if d := t.dialogNow(); d != nil {
		fmt.Fprintf(&b, "dialog open: %s %s (answer it with a dialog action)\n", d.Type, jsonString(d.Message))
		return b.String(), nil
	}
	if after := t.URL(); after != urlBefore {
		fmt.Fprintf(&b, "url: %s (was %s)\n", after, urlBefore)
	}
	if tabsAfter, err := t.b.Tabs(ctx); err == nil {
		seen := map[string]bool{}
		for _, tab := range tabsBefore {
			seen[tab.ID] = true
		}
		for _, tab := range tabsAfter {
			if !seen[tab.ID] {
				fmt.Fprintf(&b, "new tab %s: %s\n", tab.ID, tab.URL)
			}
		}
	}
	for _, d := range t.b.Downloads()[downloadsBefore:] {
		if d.State == "inProgress" {
			if finished, err := t.b.waitDownload(ctx, downloadsBefore, 30*time.Second); err == nil {
				d = finished
			}
		}
		fmt.Fprintf(&b, "download %s: %s %s\n", d.Name, d.State, d.Path)
	}
	changed, err := t.changedLines(ctx)
	if err != nil {
		fmt.Fprintf(&b, "could not read the page after the actions: %v\n", err)
	} else {
		b.WriteString(changed)
	}
	return b.String(), nil
}

// changedLines re-reads the page and lists the outline lines that were not there at the
// last read, or says nothing changed.
func (t *Tab) changedLines(ctx context.Context) (string, error) {
	t.mu.Lock()
	before := t.last
	t.mu.Unlock()
	body, err := t.outline(ctx, "")
	if err != nil {
		return "", err
	}
	t.remember(body, "")
	added, gone := diffLines(before, body)
	if len(added) == 0 && gone == 0 {
		return "page unchanged\n", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "changed: %d new lines, %d gone\n", len(added), gone)
	for i, line := range added {
		if i == changedLimit {
			fmt.Fprintf(&b, "... %d more new lines; look to read them\n", len(added)-changedLimit)
			break
		}
		b.WriteString("+ " + line + "\n")
	}
	return b.String(), nil
}

// diffLines is the lines of body that were not in before (a count of each line), in order,
// and how many lines of before are gone from body.
func diffLines(before map[string]int, body string) (added []string, gone int) {
	left := make(map[string]int, len(before))
	for k, v := range before {
		left[k] = v
	}
	for _, line := range strings.Split(body, "\n") {
		if left[line] > 0 {
			left[line]--
			continue
		}
		added = append(added, line)
	}
	for _, v := range left {
		gone += v
	}
	return added, gone
}

// Navigate loads url in the tab and waits for state. A URL that turns into a file download
// is a download result, not an error.
func (t *Tab) Navigate(ctx context.Context, url, state string) (*Download, error) {
	if state == "" {
		state = "load"
	}
	from := t.b.DownloadCount()
	var out struct {
		ErrorText string `json:"errorText"`
	}
	if err := t.b.c.send(ctx, t.sid, "Page.navigate", map[string]any{"url": url}, &out); err != nil {
		return nil, err
	}
	if out.ErrorText != "" {
		if out.ErrorText == "net::ERR_ABORTED" {
			if d, err := t.b.waitDownload(ctx, from, 10*time.Second); err == nil {
				return &d, nil
			}
		}
		return nil, fmt.Errorf("navigation to %s failed: %s", url, out.ErrorText)
	}
	return nil, t.waitLoad(ctx, state)
}
