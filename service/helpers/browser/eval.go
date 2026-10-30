package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// element is one DOM element held by a remote object in the frame that owns it.
type element struct {
	t   *Tab
	f   *frame
	oid string
}

// staleRefError is a ref that no longer names exactly one element.
type staleRefError struct {
	ref   string
	count int
}

// Error says what to do about a stale ref.
func (e staleRefError) Error() string {
	return fmt.Sprintf("ref %s resolved to %d element(s); the page changed, so look again for fresh refs", e.ref, e.count)
}

type evalResult struct {
	Result struct {
		Type     string          `json:"type"`
		Value    json.RawMessage `json:"value"`
		ObjectID string          `json:"objectId"`
	} `json:"result"`
	Exception *struct {
		Text      string `json:"text"`
		Exception struct {
			Description string `json:"description"`
		} `json:"exception"`
	} `json:"exceptionDetails"`
}

// err is the page script's exception, if it threw one.
func (r evalResult) err() error {
	if r.Exception == nil {
		return nil
	}
	msg := r.Exception.Exception.Description
	if msg == "" {
		msg = r.Exception.Text
	}
	return errors.New("page script failed: " + msg)
}

// frameByID is a copy of the tab's frame id.
func (t *Tab) frameByID(id string) (*frame, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, ok := t.frames[id]
	if !ok {
		return nil, false
	}
	cp := *f
	return &cp, true
}

// eval runs expression in frame f's page and decodes its value into out (when non-nil).
func (t *Tab) eval(ctx context.Context, f *frame, expression string, out any) error {
	var r evalResult
	err := t.b.c.send(ctx, f.sess, "Runtime.evaluate", map[string]any{
		"expression": expression, "contextId": f.ctx, "returnByValue": true, "awaitPromise": true, "userGesture": true,
	}, &r)
	if err != nil {
		return err
	}
	if err := r.err(); err != nil {
		return err
	}
	if out == nil || len(r.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(r.Result.Value, out)
}

// evalObject runs expression in frame f without converting its result, returning the remote
// object id when the result is an object and its JSON value otherwise.
func (t *Tab) evalObject(ctx context.Context, f *frame, expression string) (string, json.RawMessage, error) {
	var r evalResult
	err := t.b.c.send(ctx, f.sess, "Runtime.evaluate", map[string]any{
		"expression": expression, "contextId": f.ctx, "returnByValue": false,
	}, &r)
	if err != nil {
		return "", nil, err
	}
	if err := r.err(); err != nil {
		return "", nil, err
	}
	return r.Result.ObjectID, r.Result.Value, nil
}

// call runs the function declaration on el with args (JSON values) and decodes its value.
func (el element) call(ctx context.Context, fn string, out any, args ...any) error {
	params := make([]map[string]any, len(args))
	for i, a := range args {
		params[i] = map[string]any{"value": a}
	}
	var r evalResult
	err := el.t.b.c.send(ctx, el.f.sess, "Runtime.callFunctionOn", map[string]any{
		"objectId": el.oid, "functionDeclaration": fn, "arguments": params, "returnByValue": true, "awaitPromise": true, "userGesture": true,
	}, &r)
	if err != nil {
		return err
	}
	if err := r.err(); err != nil {
		return err
	}
	if out == nil || len(r.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(r.Result.Value, out)
}

// resolve finds the one element carrying ref in any frame, through open shadow roots. A ref
// that matches none or several is stale.
func (t *Tab) resolve(ctx context.Context, ref string) (element, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return element{}, err
	}
	expr := "(function(ref){" + jsLib + "const m=deepEls(document).filter(e=>e.getAttribute('data-dadi-ref')===ref);return m.length===1?m[0]:m.length})(" + jsonString(ref) + ")"
	var found []element
	count := 0
	for _, f := range frames {
		oid, val, err := t.evalObject(ctx, f, expr)
		if err != nil {
			continue
		}
		if oid != "" {
			found = append(found, element{t: t, f: f, oid: oid})
			count++
			continue
		}
		var n int
		if json.Unmarshal(val, &n) == nil {
			count += n
		}
	}
	if count != 1 || len(found) != 1 {
		return element{}, staleRefError{ref: ref, count: count}
	}
	return found[0], nil
}

// jsonString is s as a JavaScript string literal.
func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// pageText is the visible text of every frame, main frame first, joined by newlines.
func (t *Tab) pageText(ctx context.Context) (string, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, f := range frames {
		var text string
		if err := t.eval(ctx, f, "document.body ? document.body.innerText : ''", &text); err != nil {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n"), nil
}

// URL is the main frame's current address.
func (t *Tab) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if f := t.frames[t.main]; f != nil {
		return f.url
	}
	return ""
}

// Title is the main frame's document title.
func (t *Tab) Title(ctx context.Context) (string, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return "", err
	}
	var title string
	err = t.eval(ctx, frames[0], "document.title", &title)
	return title, err
}
