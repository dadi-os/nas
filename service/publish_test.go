package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	ip, err := controlHostname("https://216.163.53.187")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "216.163.53.187" {
		t.Fatalf("got %q", ip)
	}
}

func TestApplianceControlURL(t *testing.T) {
	if got := applianceControlURL("10.4.18.27"); got != "http://10.4.18.27:8080" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyServerURL(t *testing.T) {
	in := []byte("server_url: http://127.0.0.1:8080\nlisten_addr: 0.0.0.0:8080\n")
	out, err := applyServerURL(in, "http://10.4.18.27:8080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "server_url: http://10.4.18.27:8080\n") {
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

func TestMintControlURLCompose(t *testing.T) {
	s := stateConfig{dir: t.TempDir(), runtime: "compose", composeDir: t.TempDir()}
	got, err := s.mintControlURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != composeControlURL {
		t.Fatalf("got %q", got)
	}
}

func TestGetPublishCompose(t *testing.T) {
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
	var st publishStatus
	if err := json.NewDecoder(rec.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.ControlURL != composeControlURL {
		t.Fatalf("control_url %q", st.ControlURL)
	}
	if st.Hostname != "localhost" {
		t.Fatalf("hostname %q", st.Hostname)
	}
}
