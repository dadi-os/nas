package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const browserInputTimeout = 5 * time.Second

// browserInputEvent is one pointer or key event from a live view. X and Y are fractions of
// the virtual display (0 to 1) so callers need not know its size.
type browserInputEvent struct {
	// Type is move, down, up, wheel, key_down or key_up.
	Type string  `json:"type"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	// Button is none, left, middle or right; Buttons is the held-button bitmask (left 1,
	// right 2, middle 4) so a drag keeps its button down.
	Button     string  `json:"button"`
	Buttons    int     `json:"buttons"`
	ClickCount int     `json:"click_count"`
	DeltaX     float64 `json:"delta_x"`
	DeltaY     float64 `json:"delta_y"`
	// Key, Code and KeyCode are the DOM KeyboardEvent fields. Text is set only for keys that
	// type a character.
	Key       string `json:"key"`
	Code      string `json:"code"`
	KeyCode   int    `json:"key_code"`
	Text      string `json:"text"`
	Modifiers int    `json:"modifiers"`
}

type browserInputRequest struct {
	Events []browserInputEvent `json:"events"`
}

// cdpCommand is one Chrome DevTools Protocol call.
type cdpCommand struct {
	Method string
	Params map[string]any
}

// cdpInputCommand translates e into a CDP Input call. chromeTop is the height of the
// browser's own toolbar in display pixels, which the page viewport sits below. ok is false
// for a pointer event over the toolbar, which has no page to receive it. An unknown event
// type or button is an error.
func cdpInputCommand(e browserInputEvent, chromeTop float64) (cmd cdpCommand, ok bool, err error) {
	switch e.Type {
	case "key_down", "key_up":
		kind := "keyUp"
		if e.Type == "key_down" {
			kind = "rawKeyDown"
			if e.Text != "" {
				kind = "keyDown"
			}
		}
		return cdpCommand{"Input.dispatchKeyEvent", map[string]any{
			"type":                  kind,
			"key":                   e.Key,
			"code":                  e.Code,
			"text":                  e.Text,
			"unmodifiedText":        e.Text,
			"windowsVirtualKeyCode": e.KeyCode,
			"nativeVirtualKeyCode":  e.KeyCode,
			"modifiers":             e.Modifiers,
		}}, true, nil
	case "move", "down", "up", "wheel":
	default:
		return cdpCommand{}, false, fmt.Errorf("unknown event type %q", e.Type)
	}
	switch e.Button {
	case "none", "left", "middle", "right":
	default:
		return cdpCommand{}, false, fmt.Errorf("unknown button %q", e.Button)
	}
	y := e.Y*browserScreenH - chromeTop
	if y < 0 {
		return cdpCommand{}, false, nil
	}
	params := map[string]any{
		"x":         e.X * browserScreenW,
		"y":         y,
		"button":    e.Button,
		"buttons":   e.Buttons,
		"modifiers": e.Modifiers,
	}
	switch e.Type {
	case "move":
		params["type"] = "mouseMoved"
	case "down":
		params["type"] = "mousePressed"
		params["clickCount"] = e.ClickCount
	case "up":
		params["type"] = "mouseReleased"
		params["clickCount"] = e.ClickCount
	case "wheel":
		params["type"] = "mouseWheel"
		params["deltaX"] = e.DeltaX
		params["deltaY"] = e.DeltaY
	}
	return cdpCommand{"Input.dispatchMouseEvent", params}, true, nil
}

// cdpConn is a CDP websocket to one page target.
type cdpConn struct {
	ws   *websocket.Conn
	next int
}

// call sends method and returns its result, skipping the events and replies that arrive
// before it. The websocket deadline bounds the wait.
func (c *cdpConn) call(method string, params map[string]any) (json.RawMessage, error) {
	c.next++
	id := c.next
	if err := c.ws.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := c.ws.ReadJSON(&reply); err != nil {
			return nil, err
		}
		if reply.ID != id {
			continue
		}
		if reply.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, reply.Error.Message)
		}
		return reply.Result, nil
	}
}

// pageSocketURL returns the websocket URL of browser id's first page target, the one
// Chromium lists as most recently used.
func pageSocketURL(id int) (string, error) {
	client := http.Client{Timeout: browserInputTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", cdpPort(id)))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var targets []struct {
		Type                 string `json:"type"`
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", err
	}
	for _, t := range targets {
		if t.Type == "page" && t.WebSocketDebuggerURL != "" {
			return t.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("browser %d has no page", id)
}

// chromeTop returns the height of the browser toolbar above the page viewport, measured in
// the page as outer minus inner window height.
func (c *cdpConn) chromeTop() (float64, error) {
	raw, err := c.call("Runtime.evaluate", map[string]any{
		"expression":    "window.outerHeight - window.innerHeight",
		"returnByValue": true,
	})
	if err != nil {
		return 0, err
	}
	var result struct {
		Result struct {
			Value float64 `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, err
	}
	return result.Result.Value, nil
}

// handleInput is POST /browsers/{id}/input, which replays a batch of pointer and key events
// from a live view onto the browser's current page over CDP, in order. Batches are
// serialised so a drag never interleaves with the next one.
func (b *browserHost) handleInput(w http.ResponseWriter, r *http.Request) {
	id, ok := b.parseID(w, r)
	if !ok {
		return
	}
	if !b.requireDisplay(w, r, id) {
		return
	}
	var req browserInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Events) == 0 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "body must be {events: [...]} with at least one event")
		return
	}
	b.inputMu.Lock()
	defer b.inputMu.Unlock()
	wsURL, err := pageSocketURL(id)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, CodeUpstreamUnreachable, err.Error())
		return
	}
	dialer := websocket.Dialer{HandshakeTimeout: browserInputTimeout}
	ws, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, CodeUpstreamUnreachable, err.Error())
		return
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(browserInputTimeout))
	conn := &cdpConn{ws: ws}
	top, err := conn.chromeTop()
	if err != nil {
		writeError(w, r, http.StatusBadGateway, CodeUpstreamUnreachable, err.Error())
		return
	}
	commands := make([]cdpCommand, 0, len(req.Events))
	for _, e := range req.Events {
		cmd, send, err := cdpInputCommand(e, top)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
			return
		}
		if send {
			commands = append(commands, cmd)
		}
	}
	for _, cmd := range commands {
		if _, err := conn.call(cmd.Method, cmd.Params); err != nil {
			writeError(w, r, http.StatusBadGateway, CodeUpstreamUnreachable, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
