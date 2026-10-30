package browser

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// chromiumBin is the Chromium to drive: CHROMIUM_BIN, or the first of the usual names on
// PATH. A test that needs a browser skips, saying so, when there is none.
func chromiumBin(t *testing.T) string {
	t.Helper()
	if bin := strings.TrimSpace(os.Getenv("CHROMIUM_BIN")); bin != "" {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("CHROMIUM_BIN %q is not runnable: %v", bin, err)
		}
		return bin
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("SKIPPED: no Chromium on PATH and CHROMIUM_BIN unset; the browser regression tests did not run (CI must run them)")
	return ""
}

var listening = regexp.MustCompile(`DevTools listening on (ws://\S+)`)

// startChromium runs a headless Chromium on the Chromium folder profile and returns its
// DevTools websocket URL; it is stopped at cleanup.
func startChromium(t *testing.T, profile string) string {
	t.Helper()
	url, stop := launchChromium(t, profile)
	t.Cleanup(stop)
	return url
}

// launchChromium runs a headless Chromium on the Chromium folder profile and returns its
// DevTools websocket URL and a function that stops it.
func launchChromium(t *testing.T, profile string) (string, func()) {
	t.Helper()
	bin := chromiumBin(t)
	cmd := exec.Command(bin, "--headless=new", "--remote-debugging-port=0", "--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-features=TranslateUI", "--window-size=1280,900", "about:blank")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start chromium: %v", err)
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if m := listening.FindStringSubmatch(sc.Text()); m != nil {
				found <- m[1]
			}
		}
	}()
	select {
	case url := <-found:
		return url, stop
	case <-time.After(30 * time.Second):
		stop()
		t.Fatal("chromium did not report a DevTools address within 30s")
		return "", stop
	}
}

// newBrowser starts Chromium and connects to it; downloads land in the returned directory.
func newBrowser(t *testing.T) (*Browser, string) {
	t.Helper()
	url := startChromium(t, t.TempDir())
	downloads := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b, err := Connect(ctx, url, downloads)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, downloads
}

// testCtx is a context that ends with the test or after two minutes.
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// serve starts a server of fixture pages by path.
func serve(t *testing.T, pages map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range pages {
		body := body
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(body))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// openPage opens url in the browser's front tab and waits for it to load.
func openPage(t *testing.T, ctx context.Context, b *Browser, url string) *Tab {
	t.Helper()
	tab, err := b.Tab(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if d, err := tab.Navigate(ctx, url, "load"); err != nil || d != nil {
		t.Fatalf("navigate %s: %v %v", url, d, err)
	}
	return tab
}

var refPattern = regexp.MustCompile(`\[(e\d+)\]`)

// refOn is the ref on the first outline line containing every one of the fragments.
func refOn(t *testing.T, outline string, fragments ...string) string {
	t.Helper()
	for _, line := range strings.Split(outline, "\n") {
		ok := true
		for _, f := range fragments {
			ok = ok && strings.Contains(line, f)
		}
		if m := refPattern.FindStringSubmatch(line); ok && m != nil {
			return m[1]
		}
	}
	t.Fatalf("no outline line with %q in:\n%s", fragments, outline)
	return ""
}

// jsIn evaluates expression in the main frame, or in the frame whose URL ends with frameSuffix
// when that is not empty, and returns its value.
func jsIn(t *testing.T, ctx context.Context, tab *Tab, frameSuffix, expression string) any {
	t.Helper()
	frames, err := tab.framesInOrder(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if frameSuffix == "" && f.id == tab.main || frameSuffix != "" && strings.HasSuffix(f.url, frameSuffix) {
			var out any
			if err := tab.eval(ctx, f, expression, &out); err != nil {
				t.Fatalf("eval %s: %v", expression, err)
			}
			return out
		}
	}
	t.Fatalf("no frame ending in %q", frameSuffix)
	return nil
}

// newPasskey makes a passkey for localhost the way Chaavi hands one out.
func newPasskey(t *testing.T) Passkey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return Passkey{
		CredentialID: base64.StdEncoding.EncodeToString(id), RPID: "localhost", PrivateKey: base64.StdEncoding.EncodeToString(der),
		UserHandle: base64.StdEncoding.EncodeToString([]byte("user")), SignCount: 1, Resident: true,
	}
}
