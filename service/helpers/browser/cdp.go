package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// cdpError is an error answer from Chromium to one protocol command.
type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Method  string `json:"-"`
}

// Error returns the method and Chromium's message.
func (e *cdpError) Error() string {
	return fmt.Sprintf("%s: %s", e.Method, e.Message)
}

// cdpMessage is one frame on the DevTools websocket: an answer (ID set) or an event (Method set).
type cdpMessage struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"sessionId"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *cdpError       `json:"error"`
}

// conn is one DevTools websocket to a Chromium browser, with flattened sessions: every
// command and event carries the sessionId of the target it belongs to ("" is the browser).
type conn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan cdpMessage
	onEvent func(sessionID, method string, params json.RawMessage)
	done    chan struct{}
	err     error
}

// dial connects to the DevTools websocket at url and starts reading it. onEvent is called
// from the read loop, so it must not send commands and wait for them.
func dial(url string, onEvent func(sessionID, method string, params json.RawMessage)) (*conn, error) {
	dialer := websocket.Dialer{ReadBufferSize: 1 << 20, WriteBufferSize: 1 << 20}
	ws, _, err := dialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to browser: %w", err)
	}
	ws.SetReadLimit(256 << 20)
	c := &conn{ws: ws, pending: map[int64]chan cdpMessage{}, onEvent: onEvent, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

// readLoop routes answers to their waiting command and events to onEvent until the socket
// closes, then fails every command still waiting.
func (c *conn) readLoop() {
	var readErr error
	for {
		var msg cdpMessage
		if readErr = c.ws.ReadJSON(&msg); readErr != nil {
			break
		}
		if msg.Method != "" {
			c.onEvent(msg.SessionID, msg.Method, msg.Params)
			continue
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
	c.mu.Lock()
	c.err = fmt.Errorf("browser connection closed: %w", readErr)
	close(c.done)
	c.mu.Unlock()
}

// closed reports whether the connection has ended.
func (c *conn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// close ends the connection.
func (c *conn) close() error {
	return c.ws.Close()
}

// send runs method in session (the browser when empty) and decodes the result into out when
// it is non-nil. It never retries: a command that fails or times out has failed.
func (c *conn) send(ctx context.Context, sessionID, method string, params, out any) error {
	id := c.nextID.Add(1)
	body := map[string]any{"id": id, "method": method}
	if sessionID != "" {
		body["sessionId"] = sessionID
	}
	if params != nil {
		body["params"] = params
	}
	reply := make(chan cdpMessage, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.pending[id] = reply
	c.mu.Unlock()
	c.writeMu.Lock()
	err := c.ws.WriteJSON(body)
	c.writeMu.Unlock()
	if err != nil {
		c.forget(id)
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case msg := <-reply:
		if msg.Error != nil {
			msg.Error.Method = method
			return msg.Error
		}
		if out == nil || len(msg.Result) == 0 {
			return nil
		}
		return json.Unmarshal(msg.Result, out)
	case <-c.done:
		return c.err
	case <-ctx.Done():
		c.forget(id)
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// forget drops the waiter for command id.
func (c *conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}
