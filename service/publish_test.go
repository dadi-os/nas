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
	ip, err := controlHostname("https://216.163.53.187")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "216.163.53.187" {
		t.Fatalf("got %q", ip)
	}
}

func TestRelayHostname(t *testing.T) {
	dir := t.TempDir()
	s := stateConfig{dir: dir, runtime: "podman"}
	if _, err := s.relayHostname(); err == nil {
		t.Fatal("missing hostname file should fail")
	}
	if err := os.MkdirAll(filepath.Join(dir, "relay"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "localhost", "dadi.example.com {\nrespond", "10.0.0.1"} {
		if err := os.WriteFile(s.relayHostnamePath(), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.relayHostname(); err == nil {
			t.Fatalf("hostname %q should fail", bad)
		}
	}
	if err := os.WriteFile(s.relayHostnamePath(), []byte("dadi.ardusa.dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	host, err := s.relayHostname()
	if err != nil || host != "dadi.ardusa.dev" {
		t.Fatalf("got %q, %v", host, err)
	}
	st, err := s.loadPublishStatus()
	if err != nil || st.ControlURL != "https://dadi.ardusa.dev" {
		t.Fatalf("got %+v, %v", st, err)
	}
}

func TestWriteHeadscaleCaddy(t *testing.T) {
	s := stateConfig{dir: t.TempDir(), runtime: "podman"}
	changed, err := s.writeHeadscaleCaddy("dadi.ardusa.dev")
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	changed, err = s.writeHeadscaleCaddy("dadi.ardusa.dev")
	if err != nil || changed {
		t.Fatalf("repeat write: changed=%v err=%v", changed, err)
	}
	body, err := os.ReadFile(filepath.Join(s.dir, "caddy", "headscale.caddy"))
	if err != nil || !strings.Contains(string(body), "dadi.ardusa.dev {") || !strings.Contains(string(body), "reverse_proxy 127.0.0.1:8080") {
		t.Fatalf("vhost %q, %v", body, err)
	}
}

func TestEnsureExtraRecordsInsertsMissing(t *testing.T) {
	in := []byte(`dns:
  extra_records:
    - name: "yaad.dadi"
      type: "A"
      value: "100.64.0.1"
    - name: "nas.dadi"
      type: "A"
      value: "100.64.0.1"
unix_socket: /var/run/headscale/headscale.sock
`)
	out, err := ensureExtraRecords(in, []string{"yaad.dadi", "nas.dadi", "chaavi.dadi"}, "100.64.0.1")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, `name: "chaavi.dadi"`) {
		t.Fatalf("missing chaavi: %s", got)
	}
	if strings.Count(got, `name: "yaad.dadi"`) != 1 {
		t.Fatalf("duplicated yaad: %s", got)
	}
	yaad := strings.Index(got, `name: "yaad.dadi"`)
	chaavi := strings.Index(got, `name: "chaavi.dadi"`)
	sock := strings.Index(got, "unix_socket:")
	if yaad < 0 || chaavi < 0 || sock < 0 || !(yaad < chaavi && chaavi < sock) {
		t.Fatalf("order yaad=%d chaavi=%d sock=%d\n%s", yaad, chaavi, sock, got)
	}
}

func TestEnsureExtraRecordsNoopWhenPresent(t *testing.T) {
	in := []byte(`dns:
  extra_records:
    - name: "chaavi.dadi"
      type: "A"
      value: "100.64.0.1"
`)
	out, err := ensureExtraRecords(in, []string{"chaavi.dadi"}, "100.64.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatalf("rewrote complete config: %s", out)
	}
}

func TestEnsureExtraRecordsRequiresBlock(t *testing.T) {
	if _, err := ensureExtraRecords([]byte("server_url: http://x\n"), []string{"chaavi.dadi"}, "100.64.0.1"); err == nil {
		t.Fatal("expected missing extra_records error")
	}
}

func TestMeshExtraRecordNamesIncludeChaavi(t *testing.T) {
	names := meshExtraRecordNames()
	found := false
	for _, n := range names {
		if n == "chaavi.dadi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("chaavi.dadi missing from %v", names)
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
