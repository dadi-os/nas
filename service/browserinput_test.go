package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCDPInputCommandPointer(t *testing.T) {
	cmd, ok, err := cdpInputCommand(browserInputEvent{Type: "down", X: 0.5, Y: 0.5, Button: "left", Buttons: 1, ClickCount: 1}, 40)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if cmd.Method != "Input.dispatchMouseEvent" || cmd.Params["type"] != "mousePressed" {
		t.Fatalf("got %s %v", cmd.Method, cmd.Params)
	}
	if cmd.Params["x"] != 960.0 || cmd.Params["y"] != 500.0 {
		t.Fatalf("want page point (960,500) got (%v,%v)", cmd.Params["x"], cmd.Params["y"])
	}
	if cmd.Params["clickCount"] != 1 || cmd.Params["buttons"] != 1 {
		t.Fatalf("click state lost: %v", cmd.Params)
	}
}

func TestCDPInputCommandDropsToolbar(t *testing.T) {
	_, ok, err := cdpInputCommand(browserInputEvent{Type: "move", Y: 0.01, Button: "none"}, 85)
	if err != nil || ok {
		t.Fatalf("toolbar event should be dropped: ok=%v err=%v", ok, err)
	}
}

func TestCDPInputCommandWheel(t *testing.T) {
	cmd, _, err := cdpInputCommand(browserInputEvent{Type: "wheel", X: 0.1, Y: 0.9, Button: "none", DeltaY: 120}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Params["type"] != "mouseWheel" || cmd.Params["deltaY"] != 120.0 {
		t.Fatalf("got %v", cmd.Params)
	}
}

func TestCDPInputCommandKeys(t *testing.T) {
	typed, _, _ := cdpInputCommand(browserInputEvent{Type: "key_down", Key: "a", Code: "KeyA", KeyCode: 65, Text: "a"}, 0)
	if typed.Params["type"] != "keyDown" {
		t.Fatalf("character key should be keyDown, got %v", typed.Params["type"])
	}
	raw, _, _ := cdpInputCommand(browserInputEvent{Type: "key_down", Key: "Backspace", Code: "Backspace", KeyCode: 8}, 0)
	if raw.Params["type"] != "rawKeyDown" {
		t.Fatalf("non-character key should be rawKeyDown, got %v", raw.Params["type"])
	}
	up, _, _ := cdpInputCommand(browserInputEvent{Type: "key_up", Key: "a", Code: "KeyA", KeyCode: 65}, 0)
	if up.Params["type"] != "keyUp" {
		t.Fatalf("got %v", up.Params["type"])
	}
}

func TestCDPInputCommandRejectsUnknown(t *testing.T) {
	if _, _, err := cdpInputCommand(browserInputEvent{Type: "tap"}, 0); err == nil {
		t.Fatal("unknown type accepted")
	}
	if _, _, err := cdpInputCommand(browserInputEvent{Type: "move", Button: "fourth"}, 0); err == nil {
		t.Fatal("unknown button accepted")
	}
}

func TestStreamParam(t *testing.T) {
	get := func(query string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "/browsers/10/stream?"+query, nil)
	}
	if n, err := streamParam(get(""), "fps", 5, 1, 15); err != nil || n != 5 {
		t.Fatalf("absent: got %d, %v", n, err)
	}
	if n, err := streamParam(get("fps=12"), "fps", 5, 1, 15); err != nil || n != 12 {
		t.Fatalf("present: got %d, %v", n, err)
	}
	for _, q := range []string{"fps=0", "fps=16", "fps=fast"} {
		if _, err := streamParam(get(q), "fps", 5, 1, 15); err == nil {
			t.Fatalf("%s accepted", q)
		}
	}
}

// serveCDP runs a fake DevTools socket that reads count commands before replying to any,
// answering each in order after an event, and failing the command numbered failID if set.
func serveCDP(t *testing.T, count, failID int) *cdpConn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		ids := make([]int, count)
		for i := range ids {
			var cmd struct {
				ID int `json:"id"`
			}
			if ws.ReadJSON(&cmd) != nil {
				return
			}
			ids[i] = cmd.ID
		}
		for _, id := range ids {
			_ = ws.WriteJSON(map[string]any{"method": "Page.frameNavigated"})
			if id == failID {
				_ = ws.WriteJSON(map[string]any{"id": id, "error": map[string]any{"message": "no such target"}})
				return
			}
			_ = ws.WriteJSON(map[string]any{"id": id, "result": map[string]any{}})
		}
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	_ = ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	return &cdpConn{ws: ws}
}

func TestCDPCallAllWritesEveryCommandBeforeReading(t *testing.T) {
	conn := serveCDP(t, 3, 0)
	cmds := []cdpCommand{{"Input.dispatchMouseEvent", nil}, {"Input.dispatchMouseEvent", nil}, {"Input.dispatchKeyEvent", nil}}
	if err := conn.callAll(cmds); err != nil {
		t.Fatal(err)
	}
}

func TestCDPCallAllReturnsFailedReply(t *testing.T) {
	conn := serveCDP(t, 2, 2)
	err := conn.callAll([]cdpCommand{{"Input.dispatchMouseEvent", nil}, {"Input.dispatchMouseEvent", nil}})
	if err == nil || !strings.Contains(err.Error(), "no such target") {
		t.Fatalf("got %v", err)
	}
}
