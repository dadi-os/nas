package helpers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// dwarTimeout bounds one model call; Dwar retries transient provider failures itself.
const dwarTimeout = 5 * time.Minute

// Block is one content block of a model turn (text, thinking, tool_use, tool_result), kept
// with every field Dwar returned so a provider's thinking signatures round-trip unchanged.
type Block = map[string]any

// Message is one turn of a helper's history in Dwar's chat shape.
type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

// ToolSpec is a tool as the model sees it.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type chatRequest struct {
	System     string     `json:"system"`
	Messages   []Message  `json:"messages"`
	Tools      []ToolSpec `json:"tools"`
	ToolChoice string     `json:"tool_choice"`
}

// chatResponse is Dwar's answer to one chat call.
type chatResponse struct {
	Content    []Block `json:"content"`
	StopReason string  `json:"stop_reason"`
}

// dwarClient calls Dwar's POST /chat/complete, which runs the caller's system prompt only.
type dwarClient struct {
	baseURL string
	http    *http.Client
}

// newDwarClient returns a client for the Dwar at baseURL.
func newDwarClient(baseURL string) *dwarClient {
	return &dwarClient{baseURL: baseURL, http: &http.Client{Timeout: dwarTimeout}}
}

// complete runs one model turn. caller is sent as X-Dadi-Caller so Dwar's inference log
// attributes the tokens. A non-200 answer is an error carrying Dwar's message.
func (d *dwarClient) complete(caller string, req chatRequest) (chatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return chatResponse{}, fmt.Errorf("encode chat request: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, d.baseURL+"/chat/complete", bytes.NewReader(body))
	if err != nil {
		return chatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Dadi-Caller", caller)
	resp, err := d.http.Do(httpReq)
	if err != nil {
		return chatResponse{}, fmt.Errorf("dwar unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return chatResponse{}, fmt.Errorf("read dwar answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return chatResponse{}, fmt.Errorf("dwar %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return chatResponse{}, fmt.Errorf("parse dwar answer: %w", err)
	}
	return out, nil
}
