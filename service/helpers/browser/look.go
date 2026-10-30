package browser

import (
	"context"
	"fmt"
	"strings"
)

// outlineMaxChars is how much of an outline a look returns; the rest is cut with a note.
const outlineMaxChars = 16_000

// clipText shortens s to max characters, cutting at a line end and saying what to do next.
func clipText(s string, max int, hint string) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '\n'); i > max/2 {
		cut = cut[:i]
	}
	return fmt.Sprintf("%s\n... cut at %d of %d characters; %s", cut, len(cut), len(s), hint)
}

// frameVisible reports whether the frame's own box is on the page: the frame element in its
// parent has a box model. A frame in another process is checked through its parent's session.
func (t *Tab) frameVisible(ctx context.Context, f *frame) (bool, error) {
	if f.id == t.main {
		return true, nil
	}
	parent, ok := t.frameByID(f.parent)
	if !ok {
		return false, nil
	}
	var owner struct {
		BackendNodeID int64 `json:"backendNodeId"`
	}
	if err := t.b.c.send(ctx, parent.sess, "DOM.getFrameOwner", map[string]any{"frameId": f.id}, &owner); err != nil {
		return false, nil
	}
	var box struct {
		Model struct {
			Width  float64 `json:"width"`
			Height float64 `json:"height"`
		} `json:"model"`
	}
	if err := t.b.c.send(ctx, parent.sess, "DOM.getBoxModel", map[string]any{"backendNodeId": owner.BackendNodeID}, &box); err != nil {
		if strings.Contains(err.Error(), "box model") || strings.Contains(err.Error(), "layout object") {
			return false, nil
		}
		return false, err
	}
	return box.Model.Width > 0 && box.Model.Height > 0, nil
}

// outline is the interactive outline of every visible frame, or of the region scope names
// (a ref or text): refs are numbered across frames and stay with their element between looks.
// Each frame's lines come back under a frame "url" line, indented.
func (t *Tab) outline(ctx context.Context, scope string) (string, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return "", err
	}
	var sections []string
	anyFound := false
	for _, f := range frames {
		visible, err := t.frameVisible(ctx, f)
		if err != nil {
			return "", err
		}
		if !visible {
			continue
		}
		t.mu.Lock()
		start := t.nextRef + 1
		t.mu.Unlock()
		var snap struct {
			Lines string `json:"lines"`
			Next  int    `json:"next"`
			Found bool   `json:"found"`
		}
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%d, %s)", snapshotScript, start, jsonString(scope)), &snap); err != nil {
			return "", err
		}
		t.mu.Lock()
		if snap.Next-1 > t.nextRef {
			t.nextRef = snap.Next - 1
		}
		t.mu.Unlock()
		if snap.Found {
			anyFound = true
		}
		switch {
		case f.id == t.main:
			if snap.Lines != "" {
				sections = append(sections, snap.Lines)
			}
		case snap.Lines != "":
			sections = append(sections, fmt.Sprintf("frame %s\n%s", jsonString(f.url), indentLines(snap.Lines)))
		}
	}
	if scope != "" && !anyFound {
		return "", fmt.Errorf("nothing on the page matches scope %q; look without a scope or use find", scope)
	}
	return strings.Join(sections, "\n"), nil
}

// indentLines indents every line of s by two spaces.
func indentLines(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

// Look is the page as the helper reads it: its URL and title, then the outline (or the
// outline of scope), cut at outlineMaxChars.
func (t *Tab) Look(ctx context.Context, scope string) (string, error) {
	body, err := t.outline(ctx, scope)
	if err != nil {
		return "", err
	}
	title, err := t.Title(ctx)
	if err != nil {
		return "", err
	}
	t.remember(body, scope)
	head := fmt.Sprintf("url: %s\ntitle: %s\n", t.URL(), title)
	if scope != "" {
		head += "scope: " + scope + "\n"
	}
	return head + clipText(body, outlineMaxChars, "look with a scope (a ref or text) or use find or extract to read the rest"), nil
}

// remember keeps the lines of a full-page outline, which act compares against to report
// what changed.
func (t *Tab) remember(body, scope string) {
	if scope != "" {
		return
	}
	counts := map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		counts[line]++
	}
	t.mu.Lock()
	t.last = counts
	t.mu.Unlock()
}

// Find is the refs of the elements anywhere on the page, frames and shadow roots included,
// whose text, role or label contains query.
func (t *Tab) Find(ctx context.Context, query string) (string, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return "", err
	}
	const limit = 25
	var lines []string
	for _, f := range frames {
		if len(lines) >= limit {
			break
		}
		visible, err := t.frameVisible(ctx, f)
		if err != nil {
			return "", err
		}
		if !visible {
			continue
		}
		t.mu.Lock()
		start := t.nextRef + 1
		t.mu.Unlock()
		var found struct {
			Lines []string `json:"lines"`
			Next  int      `json:"next"`
		}
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%s, %d, %d)", findScript, jsonString(query), start, limit-len(lines)), &found); err != nil {
			return "", err
		}
		t.mu.Lock()
		if found.Next-1 > t.nextRef {
			t.nextRef = found.Next - 1
		}
		t.mu.Unlock()
		for _, l := range found.Lines {
			if f.id != t.main {
				l += " (frame " + f.url + ")"
			}
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return fmt.Sprintf("nothing on the page matches %q", query), nil
	}
	out := strings.Join(lines, "\n")
	if len(lines) >= limit {
		out += "\n(first " + fmt.Sprint(limit) + " matches; narrow the query)"
	}
	return out, nil
}
