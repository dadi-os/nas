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

// testBrowserEnv returns a browser host over a fresh state dir with the /browsers routes on
// mux; every browser still running is stopped at cleanup.
func testBrowserEnv(t *testing.T) (*browserHost, *http.ServeMux, string) {
	t.Helper()
	bin := requireBrowserDeps(t)
	dir := t.TempDir()
	state := stateConfig{dir: dir, runtime: "compose"}
	host, err := newHostRuntime(state, t.TempDir())
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

// testProfile is the Chromium folder the tests run browsers on.
func testProfile(bh *browserHost) string {
	return filepath.Join(bh.host.browsersDir, "agent", "1")
}

// startTestBrowser starts a browser on slot 10 and returns what it reports.
func startTestBrowser(t *testing.T, bh *browserHost) browserLaunch {
	t.Helper()
	launch, err := bh.startBrowser(10, testProfile(bh))
	if err != nil {
		t.Fatal(err)
	}
	return launch
}

func TestBrowserStartListKill(t *testing.T) {
	bh, mux, _ := testBrowserEnv(t)
	launch := startTestBrowser(t, bh)
	if launch.Display != ":10" || !strings.HasPrefix(launch.CDPURL, "ws://127.0.0.1:9310/devtools/browser/") {
		t.Fatalf("launch %+v", launch)
	}
	code, body := doJSON(t, mux, http.MethodGet, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("list browsers %d %s", code, body)
	}
	var list []browserInfo
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Healthy || list[0].ID != 10 {
		t.Fatalf("browsers %+v", list)
	}
	if _, err := bh.startBrowser(10, testProfile(bh)); err == nil {
		t.Fatal("started a second browser on a running slot")
	}
	bh.killBrowser(10)
	if bh.processesExist(10) {
		t.Fatal("browser 10 still running after kill")
	}
	if _, err := os.Stat(testProfile(bh)); err != nil {
		t.Fatalf("profile after kill: %v", err)
	}
}

func TestBrowsersJSONVersionUsesRequestHost(t *testing.T) {
	bh, mux, _ := testBrowserEnv(t)
	startTestBrowser(t, bh)
	req := httptest.NewRequest(http.MethodGet, "/browsers/10/json/version", nil)
	req.Host = "nas.dadi"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("json/version %d %s", rec.Code, rec.Body.String())
	}
	var ver cdpVersion
	if err := json.Unmarshal(rec.Body.Bytes(), &ver); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ver.WebSocketDebuggerURL, "ws://nas.dadi/browsers/10/") {
		t.Fatalf("rewritten url %s", ver.WebSocketDebuggerURL)
	}
}

func TestBrowsersWebsocketGetTargets(t *testing.T) {
	bh, mux, _ := testBrowserEnv(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	startTestBrowser(t, bh)

	verResp, err := http.Get(srv.URL + fmt.Sprintf("/browsers/%s/json/version", "10"))
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
	bh, mux, _ := testBrowserEnv(t)
	startTestBrowser(t, bh)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/browsers/%s/screenshot", "10"), nil)
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

func TestBrowsersStream(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if out, _ := exec.Command(ffmpeg, "-hide_banner", "-encoders").Output(); !bytes.Contains(out, []byte("libopenh264")) {
		t.Skip("ffmpeg has no libopenh264 encoder")
	}
	bh, mux, _ := testBrowserEnv(t)
	startTestBrowser(t, bh)

	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(fmt.Sprintf("%s/browsers/%s/stream", srv.URL, "10"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		t.Fatalf("stream %d %s", resp.StatusCode, msg)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/h264" {
		t.Fatalf("content-type %s", ct)
	}
	head := make([]byte, 64)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(head, []byte{0, 0, 0, 1, 0x09}) && !bytes.HasPrefix(head, []byte{0, 0, 1, 0x09}) {
		t.Fatalf("stream does not open with an access unit delimiter: % x", head[:8])
	}
}

func TestBrowsersUnhealthyListed(t *testing.T) {
	bh, mux, _ := testBrowserEnv(t)
	startTestBrowser(t, bh)
	for _, pid := range findPIDs(bh.chromiumPattern(10)) {
		_ = exec.Command("kill", "-9", fmt.Sprintf("%d", pid)).Run()
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := bh.fetchVersion(10); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	code, body := doJSON(t, mux, http.MethodGet, "/browsers", nil)
	if code != http.StatusOK {
		t.Fatalf("list %d %s", code, body)
	}
	var list []browserInfo
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != 10 || list[0].Healthy {
		t.Fatalf("expected browser 10 unhealthy: %+v", list)
	}
}
