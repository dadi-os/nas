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
