package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlHostname(t *testing.T) {
	host, err := controlHostname("https://mesh.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if host != "mesh.example.com" {
		t.Fatalf("got %q", host)
	}
	if _, err := controlHostname("https://216.163.53.187"); err == nil {
		t.Fatal("expected error for IP")
	}
}

func TestValidateControlURL(t *testing.T) {
	if err := validateControlURL("podman", "https://mesh.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := validateControlURL("podman", "http://mesh.example.com"); err == nil {
		t.Fatal("expected https on appliance")
	}
	if err := validateControlURL("compose", "http://localhost:8080"); err != nil {
		t.Fatal(err)
	}
	if err := validateControlURL("compose", "not-a-url"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCaddyHeadscaleSnippet(t *testing.T) {
	got := caddyHeadscaleSnippet("mesh.example.com")
	if !strings.Contains(got, "https://mesh.example.com") {
		t.Fatalf("snippet %q", got)
	}
	if !strings.Contains(got, "reverse_proxy 127.0.0.1:8080") {
		t.Fatalf("snippet %q", got)
	}
}

func TestApplyServerURL(t *testing.T) {
	in := []byte("server_url: http://127.0.0.1:8080\nlisten_addr: 0.0.0.0:8080\n")
	out, err := applyServerURL(in, "https://mesh.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "server_url: https://mesh.example.com\n") {
		t.Fatalf("got %s", out)
	}
	if _, err := applyServerURL([]byte("listen_addr: :8080\n"), "https://x"); err == nil {
		t.Fatal("expected missing server_url error")
	}
}

func TestParseRouteSrcIPv4(t *testing.T) {
	got, err := parseRouteSrcIPv4("1.1.1.1 via 10.4.18.1 dev enp2s0 src 10.4.18.27 uid 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.4.18.27" {
		t.Fatalf("got %q", got)
	}
}

func TestParseUPnPExternalIP(t *testing.T) {
	got, err := parseUPnPExternalIP("ExternalIPAddress = 216.163.53.187\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "216.163.53.187" {
		t.Fatalf("got %q", got)
	}
	got, err = parseUPnPExternalIP("External IP address assigned by IGD: 1.2.3.4\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.3.4" {
		t.Fatalf("got %q", got)
	}
}

func TestPutControlURLCompose(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "compose", composeDir: dir}
	mux := http.NewServeMux()
	registerConfigRoutes(mux, s)

	req := httptest.NewRequest(http.MethodPut, "/headscale/control-url", strings.NewReader("http://localhost:8080"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body, err := os.ReadFile(filepath.Join(dir, "headscale", "control_url"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "http://localhost:8080" {
		t.Fatalf("got %q", body)
	}
}

func TestPutControlURLRejectsApplianceHTTP(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "podman"}
	mux := http.NewServeMux()
	registerConfigRoutes(mux, s)

	req := httptest.NewRequest(http.MethodPut, "/headscale/control-url", strings.NewReader("http://mesh.example.com"))
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

func TestGetPublishCompose(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "compose", composeDir: dir}
	if err := os.MkdirAll(filepath.Join(dir, "headscale"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "headscale", "control_url"), []byte("http://localhost:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerConfigRoutes(mux, s)
	req := httptest.NewRequest(http.MethodGet, "/headscale/publish", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var st publishStatus
	if err := json.NewDecoder(rec.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.Hostname != "localhost" {
		t.Fatalf("hostname %q", st.Hostname)
	}
}

func TestGetPublishComposeEmptyURL(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "compose", composeDir: dir}
	mux := http.NewServeMux()
	registerConfigRoutes(mux, s)
	req := httptest.NewRequest(http.MethodGet, "/headscale/publish", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}
