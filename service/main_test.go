package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRequireEnv(t *testing.T) {
	t.Setenv("NAS_TEST_REQUIRED", "value")
	got, err := requireEnv("NAS_TEST_REQUIRED")
	if err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if got != "value" {
		t.Fatalf("got %q", got)
	}

	t.Setenv("NAS_TEST_MISSING", "")
	if _, err := requireEnv("NAS_TEST_MISSING"); err == nil {
		t.Fatal("expected error for empty env")
	}
}

func TestHealthTargets(t *testing.T) {
	podman := healthTargets("podman")
	if len(podman) != 4 {
		t.Fatalf("podman targets: %d", len(podman))
	}
	if !strings.Contains(podman[0].url, "127.0.0.1") {
		t.Fatalf("podman should use localhost: %s", podman[0].url)
	}

	compose := healthTargets("compose")
	if len(compose) != 4 {
		t.Fatalf("compose targets: %d", len(compose))
	}
	if !strings.Contains(compose[0].url, "yaad:8080") {
		t.Fatalf("compose should use service DNS: %s", compose[0].url)
	}
}

func TestParseLokiRangeJSON(t *testing.T) {
	body := []byte(`{
		"data": {
			"result": [{
				"stream": {"service": "yaad"},
				"values": [
					["1700000000000000000", "{\"time\":\"2024-01-01T00:00:00Z\",\"level\":\"error\",\"service\":\"yaad\",\"msg\":\"boom\",\"code\":\"internal_error\"}"],
					["1700000001000000000", "not json noise"]
				]
			}]
		}
	}`)
	entries, err := parseLokiRange(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 json entry, got %d", len(entries))
	}
	if entries[0].Msg != "boom" || entries[0].Level != "error" || entries[0].Code != "internal_error" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestWriteErrorShape(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	writeError(rec, req, http.StatusBadRequest, CodeInvalidRequest, "bad")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	var payload errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Type != CodeInvalidRequest || payload.Error.Message != "bad" {
		t.Fatalf("payload %+v", payload)
	}
}

func TestHandleLogsInvalidLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/logs?limit=nope", nil)
	rec := httptest.NewRecorder()
	handleLogs(rec, req, "http://loki:3100")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	var payload errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Type != CodeInvalidRequest {
		t.Fatalf("type %s", payload.Error.Type)
	}
}

func TestHandleHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	withRequestLog(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestLoadStateConfigRequiresEnv(t *testing.T) {
	os.Unsetenv("DADI_STATE_DIR")
	os.Unsetenv("DADI_RUNTIME")
	os.Unsetenv("DADI_COMPOSE_DIR")
	if _, err := loadStateConfig(); err == nil {
		t.Fatal("expected missing DADI_STATE_DIR")
	}
	t.Setenv("DADI_STATE_DIR", "/tmp/dadi-state")
	t.Setenv("DADI_RUNTIME", "compose")
	if _, err := loadStateConfig(); err == nil {
		t.Fatal("expected missing DADI_COMPOSE_DIR")
	}
	t.Setenv("DADI_COMPOSE_DIR", "/tmp/compose")
	cfg, err := loadStateConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.runtime != "compose" {
		t.Fatalf("runtime %s", cfg.runtime)
	}
}

func TestRequestLogIncludesDuration(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
	})
	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	req.Header.Set("X-Request-Id", "test-req-1")
	rec := httptest.NewRecorder()
	withRequestLog(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestPullUpdatesInvalidScope(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "compose", composeDir: dir}
	mux := http.NewServeMux()
	registerConfigRoutes(mux, s)

	req := httptest.NewRequest(http.MethodPost, "/pull_updates", strings.NewReader(`{"scope":"nope"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var payload errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Type != CodeInvalidRequest {
		t.Fatalf("type %s", payload.Error.Type)
	}
}

func TestPullUpdatesComposeRejectsOS(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "compose", composeDir: dir}
	mux := http.NewServeMux()
	registerConfigRoutes(mux, s)

	for _, scope := range []string{"os", "all"} {
		req := httptest.NewRequest(http.MethodPost, "/pull_updates", strings.NewReader(`{"scope":"`+scope+`"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("scope %s status %d body %s", scope, rec.Code, rec.Body.String())
		}
		var payload errorResponse
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Error.Type != CodeInvalidRequest {
			t.Fatalf("scope %s type %s", scope, payload.Error.Type)
		}
	}
}

func TestParseHeadscaleNodesArray(t *testing.T) {
	raw := []byte(`[
		{"name":"os","givenName":"os","online":true,"lastSeen":"2026-01-02T03:04:05Z","ipAddresses":["100.64.0.1"]},
		{"name":"phone","givenName":"ankur-phone","online":false,"lastSeen":null,"ipAddresses":[]}
	]`)
	nodes, err := parseHeadscaleNodes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("len %d", len(nodes))
	}
	clients := make([]meshClient, 0, len(nodes))
	for _, node := range nodes {
		name := strings.TrimSpace(node.GivenName)
		if name == "" {
			name = strings.TrimSpace(node.Name)
		}
		clients = append(clients, meshClient{
			NodeName:    name,
			Online:      node.Online,
			LastSeen:    formatHeadscaleLastSeen(node.LastSeen),
			IPAddresses: node.IPAddresses,
		})
	}
	if clients[0].NodeName != "os" || !clients[0].Online || clients[0].LastSeen == nil {
		t.Fatalf("client0 %+v", clients[0])
	}
	if clients[1].NodeName != "ankur-phone" || clients[1].Online || clients[1].LastSeen != nil {
		t.Fatalf("client1 %+v", clients[1])
	}
}

func TestParseHeadscaleNodesWrapped(t *testing.T) {
	raw := []byte(`{"nodes":[{"name":"laptop","givenName":"","online":true,"lastSeen":"2026-01-01T00:00:00Z","ipAddresses":["100.64.0.2"]}]}`)
	nodes, err := parseHeadscaleNodes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "laptop" {
		t.Fatalf("%+v", nodes)
	}
	if got := formatHeadscaleLastSeen(nodes[0].LastSeen); got == nil || *got != "2026-01-01T00:00:00Z" {
		t.Fatalf("lastSeen %v", got)
	}
}
