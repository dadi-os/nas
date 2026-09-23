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
	if len(podman) != 5 {
		t.Fatalf("podman targets: %d", len(podman))
	}
	if !strings.Contains(podman[0].url, "127.0.0.1") {
		t.Fatalf("podman should use localhost: %s", podman[0].url)
	}

	compose := healthTargets("compose")
	if len(compose) != 5 {
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

func TestParseLokiRangeNewestFirstAcrossStreams(t *testing.T) {
	body := []byte(`{
		"data": {
			"result": [
				{
					"stream": {"service": "nas"},
					"values": [
						["1700000000000000000", "{\"time\":\"2024-01-01T00:00:00Z\",\"level\":\"error\",\"service\":\"nas\",\"msg\":\"older\"}"]
					]
				},
				{
					"stream": {"service": "dimaag"},
					"values": [
						["1700000002000000000", "{\"time\":\"2024-01-01T00:00:02Z\",\"level\":\"error\",\"service\":\"dimaag\",\"msg\":\"newer\"}"]
					]
				}
			]
		}
	}`)
	entries, err := parseLokiRange(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Msg != "newer" || entries[1].Msg != "older" {
		t.Fatalf("order %+v", entries)
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

func TestKnownLogServicesIncludesChaavi(t *testing.T) {
	got := knownLogServices("podman")
	found := false
	for _, n := range got {
		if n == "chaavi" {
			found = true
		}
	}
	if !found {
		t.Fatalf("chaavi missing from %v", got)
	}
}

func TestMergeLogServicesUnionsAndSorts(t *testing.T) {
	got := mergeLogServices([]string{"yaad", "nas", "chaavi"}, []string{"caddy", "yaad", "hath"})
	want := []string{"caddy", "chaavi", "hath", "nas", "yaad"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestParseLokiLabelValues(t *testing.T) {
	got, err := parseLokiLabelValues([]byte(`{"status":"success","data":["chaavi","nas"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "chaavi" || got[1] != "nas" {
		t.Fatalf("got %v", got)
	}
}

func TestHandleLogServicesInvalidRange(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/logs/services?from=2026-01-01T00:00:00Z&to=2026-01-01T00:00:00Z", nil)
	rec := httptest.NewRecorder()
	handleLogServices(rec, req, "http://loki:3100", "compose")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
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
	if nodes[0].GivenName != "os" || !nodes[0].Online || nodes[0].LastSeen == nil || *nodes[0].LastSeen != "2026-01-02T03:04:05Z" {
		t.Fatalf("node0 %+v", nodes[0])
	}
	if nodes[1].GivenName != "ankur-phone" || nodes[1].Online || nodes[1].LastSeen != nil {
		t.Fatalf("node1 %+v", nodes[1])
	}
}

func TestParseHeadscaleNodesRejectsObject(t *testing.T) {
	raw := []byte(`{"nodes":[{"name":"laptop","givenName":"","online":true,"lastSeen":"2026-01-01T00:00:00Z","ipAddresses":["100.64.0.2"]}]}`)
	if _, err := parseHeadscaleNodes(raw); err == nil {
		t.Fatal("expected error for non-array JSON")
	}
}

func TestEnrichClientsWithHostMesh(t *testing.T) {
	ls := "2026-09-23T12:00:00Z"
	clients := []meshClient{
		{NodeName: "mac", Online: false, IPAddresses: []string{}, LastSeen: nil},
		{NodeName: "phone", Online: false, IPAddresses: []string{"100.64.0.3"}},
	}
	got := enrichClientsWithHostMesh(clients, []hostMeshPeer{
		{HostName: "os", Online: true, Active: true, TailscaleIPs: []string{"100.64.0.1"}},
		{HostName: "mac", Online: false, Active: true, TailscaleIPs: []string{"100.64.0.2"}, LastSeen: ls},
	})
	byName := map[string]meshClient{}
	for _, c := range got {
		byName[c.NodeName] = c
	}
	mac := byName["mac"]
	if !mac.Online || len(mac.IPAddresses) != 1 || mac.IPAddresses[0] != "100.64.0.2" {
		t.Fatalf("mac %+v", mac)
	}
	if mac.LastSeen == nil || *mac.LastSeen != ls {
		t.Fatalf("mac last_seen %+v", mac.LastSeen)
	}
	if _, ok := byName["os"]; !ok {
		t.Fatal("expected os from host self")
	}
	if byName["phone"].Online {
		t.Fatal("phone should stay offline without a host peer")
	}

	fresh := []meshClient{
		{NodeName: "mac", Online: false, IPAddresses: []string{}, LastSeen: nil},
	}
	unchanged := enrichClientsWithHostMesh(fresh, nil)
	if len(unchanged) != 1 || unchanged[0].Online {
		t.Fatalf("nil peers must leave Headscale rows alone: %+v", unchanged)
	}
}

func TestDiskStatusFromStatfs(t *testing.T) {
	full := diskStatusFromStatfs(26497024, 0)
	if full.UsedPercent != 100 {
		t.Fatalf("composefs-full: %v", full.UsedPercent)
	}
	half := diskStatusFromStatfs(1000, 500)
	if half.UsedPercent != 50 {
		t.Fatalf("half: %v", half.UsedPercent)
	}
	over := diskStatusFromStatfs(100, 120)
	if over.UsedPercent != 0 || over.FreeBytes != 120 {
		t.Fatalf("avail>total: %+v", over)
	}
}

func TestCollectMountsAndSkip(t *testing.T) {
	tree := lsblkDevice{
		Name: "nvme0n1",
		Type: "disk",
		Children: []lsblkDevice{
			{Name: "nvme0n1p1", Mountpoints: []any{"/boot/efi"}},
			{Name: "nvme0n1p2", Mountpoints: []any{"/boot"}},
			{
				Name: "nvme0n1p3",
				Children: []lsblkDevice{
					{Name: "luks-root", Mountpoints: []any{"/var", "/sysroot", nil}},
				},
			},
		},
	}
	mounts := collectMounts(tree)
	if len(mounts) != 4 {
		t.Fatalf("mounts: %v", mounts)
	}
	kept := 0
	for _, m := range mounts {
		if !skipMount(m) {
			kept++
			if m != "/var" && m != "/sysroot" {
				t.Fatalf("unexpected kept mount %q", m)
			}
		}
	}
	if kept != 2 {
		t.Fatalf("kept=%d mounts=%v", kept, mounts)
	}
}

func TestDiskSortKey(t *testing.T) {
	if diskSortKey(diskVolumeStatus{Name: "nvme0n1", Transport: "nvme"}) >= diskSortKey(diskVolumeStatus{Name: "sda", Transport: "sata"}) {
		t.Fatal("nvme should sort before sata")
	}
}

func TestDiskLabel(t *testing.T) {
	if got := diskLabel(diskVolumeStatus{Name: "sda", Model: "ST1000"}); got != "ST1000" {
		t.Fatalf("model: %q", got)
	}
	if got := diskLabel(diskVolumeStatus{Name: "sda", Transport: "sata"}); got != "sda (sata)" {
		t.Fatalf("tran: %q", got)
	}
}

func TestMountPref(t *testing.T) {
	if mountPref("/") >= mountPref("/sysroot") || mountPref("/sysroot") >= mountPref("/var") {
		t.Fatal("expected / < /sysroot < /var")
	}
}
