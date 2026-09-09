package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func requireBrowserDeps(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb not on PATH")
	}
	bin := strings.TrimSpace(os.Getenv("CHROMIUM_BIN"))
	if bin == "" {
		bin = defaultChromium
	}
	if _, err := exec.LookPath(bin); err != nil {
		if _, err2 := exec.LookPath("chromium"); err2 == nil {
			bin = "chromium"
		} else {
			t.Skip("chromium binary not on PATH")
		}
	}
	return bin
}

func testBrowserEnv(t *testing.T) (*browserHost, *http.ServeMux, string) {
	t.Helper()
	bin := requireBrowserDeps(t)
	dir := t.TempDir()
	state := stateConfig{dir: dir, runtime: "compose"}
	host, err := newHostRuntime(state)
	if err != nil {
		t.Fatal(err)
	}
	bh := newBrowserHost(host)
	bh.chromiumBin = bin
	mux := http.NewServeMux()
	bh.register(mux)
	t.Cleanup(func() {
		ids, _ := bh.scanIDs()
		for _, id := range ids {
			bh.killBrowser(id)
		}
	})
	return bh, mux, dir
}

func TestBrowsersCreateListDelete(t *testing.T) {
	bh, mux, _ := testBrowserEnv(t)

	code, body := doJSON(t, mux, http.MethodPost, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createBrowserResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != 10 {
		t.Fatalf("want id 10 got %d", created.ID)
	}
	if created.Display != ":10" {
		t.Fatalf("display %s", created.Display)
	}
	if !strings.Contains(created.CDPURL, "/browsers/10/devtools/browser/") {
		t.Fatalf("cdp_url %s", created.CDPURL)
	}

	code, body = doJSON(t, mux, http.MethodGet, "/browsers/10/json/version", nil)
	if code != http.StatusOK {
		t.Fatalf("json/version %d %s", code, body)
	}
	var ver cdpVersion
	if err := json.Unmarshal(body, &ver); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ver.WebSocketDebuggerURL, "/browsers/10/") {
		t.Fatalf("rewritten url %s", ver.WebSocketDebuggerURL)
	}

	code, body = doJSON(t, mux, http.MethodGet, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("list %d %s", code, body)
	}
	var list []browserInfo
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Healthy || list[0].ID != 10 {
		t.Fatalf("list %+v", list)
	}

	code, _ = doJSON(t, mux, http.MethodDelete, "/browsers/10", nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !bh.processesExist(10) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	code, body = doJSON(t, mux, http.MethodGet, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("list after delete %d", code)
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %+v", list)
	}
}

func TestBrowsersIDReuseAndProfile(t *testing.T) {
	bh, mux, _ := testBrowserEnv(t)

	code, body := doJSON(t, mux, http.MethodPost, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("create1 %d %s", code, body)
	}
	var b1 createBrowserResponse
	_ = json.Unmarshal(body, &b1)
	if b1.ID != 10 {
		t.Fatalf("id %d", b1.ID)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("create2 %d %s", code, body)
	}
	var b2 createBrowserResponse
	_ = json.Unmarshal(body, &b2)
	if b2.ID != 11 {
		t.Fatalf("id %d", b2.ID)
	}

	marker := filepath.Join(bh.profileDir(10), "nas-profile-marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _ = doJSON(t, mux, http.MethodDelete, "/browsers/10", nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && bh.processesExist(10) {
		time.Sleep(100 * time.Millisecond)
	}

	code, body = doJSON(t, mux, http.MethodPost, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("recreate %d %s", code, body)
	}
	var b3 createBrowserResponse
	_ = json.Unmarshal(body, &b3)
	if b3.ID != 10 {
		t.Fatalf("want reuse 10 got %d", b3.ID)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("profile marker %q", got)
	}
}

func TestBrowsersCDPURLUsesRequestHost(t *testing.T) {
	_, mux, _ := testBrowserEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/browsers", nil)
	req.Host = "nas.dadi"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var created createBrowserResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.CDPURL, "ws://nas.dadi/browsers/") {
		t.Fatalf("cdp_url %s", created.CDPURL)
	}
	if !strings.Contains(created.CDPURL, fmt.Sprintf("/browsers/%d/", created.ID)) {
		t.Fatalf("missing prefix in %s", created.CDPURL)
	}
}

func TestBrowsersWebsocketGetTargets(t *testing.T) {
	_, mux, _ := testBrowserEnv(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/browsers", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create %d %s", resp.StatusCode, body)
	}
	var created createBrowserResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}

	verResp, err := http.Get(srv.URL + fmt.Sprintf("/browsers/%d/json/version", created.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer verResp.Body.Close()
	verBody, _ := io.ReadAll(verResp.Body)
	var ver cdpVersion
	if err := json.Unmarshal(verBody, &ver); err != nil {
		t.Fatal(err)
	}
	wsURL := ver.WebSocketDebuggerURL
	if !strings.HasPrefix(wsURL, "ws://") {
		t.Fatalf("unexpected cdp url %s", wsURL)
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	defer conn.Close()

	msg := map[string]any{"id": 1, "method": "Target.getTargets"}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var reply map[string]any
	if err := conn.ReadJSON(&reply); err != nil {
		t.Fatal(err)
	}
	if reply["id"] != float64(1) {
		t.Fatalf("reply %+v", reply)
	}
	result, _ := reply["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no result in %+v", reply)
	}
	if _, ok := result["targetInfos"]; !ok {
		t.Fatalf("missing targetInfos: %+v", result)
	}
}

func TestBrowsersScreenshot(t *testing.T) {
	if _, err := exec.LookPath("import"); err != nil {
		t.Skip("ImageMagick import not on PATH")
	}
	_, mux, _ := testBrowserEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createBrowserResponse
	_ = json.Unmarshal(body, &created)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/browsers/%d/screenshot", created.ID), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("screenshot %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type %s", ct)
	}
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != browserScreenW || bounds.Dy() != browserScreenH {
		t.Fatalf("dimensions %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestBrowsersUnhealthyThenDelete(t *testing.T) {
	bh, mux, _ := testBrowserEnv(t)
	code, body := doJSON(t, mux, http.MethodPost, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("create %d %s", code, body)
	}
	var created createBrowserResponse
	_ = json.Unmarshal(body, &created)

	pids := findPIDs(bh.chromiumPattern(created.ID))
	if len(pids) == 0 {
		t.Fatal("no chromium pid")
	}
	for _, pid := range pids {
		_ = exec.Command("kill", "-9", fmt.Sprintf("%d", pid)).Run()
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := bh.fetchVersion(created.ID); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	code, body = doJSON(t, mux, http.MethodGet, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("list %d %s", code, body)
	}
	var list []browserInfo
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list {
		if item.ID == created.ID {
			found = true
			if item.Healthy {
				t.Fatalf("expected unhealthy: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("browser missing from list: %+v", list)
	}

	code, _ = doJSON(t, mux, http.MethodDelete, fmt.Sprintf("/browsers/%d", created.ID), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete %d", code)
	}
}
