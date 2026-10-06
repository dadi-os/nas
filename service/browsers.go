package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	browserIDMin    = 10
	cdpPortBase     = 9300
	xvfbWait        = 2 * time.Second
	chromiumWait    = 30 * time.Second
	browserKillWait = 5 * time.Second
	browserScreenW  = 1920
	browserScreenH  = 1080
	// defaultChromium is the product's Chromium binary on dadiOS (Fedora's chromium-browser).
	// CHROMIUM_BIN is an optional override; the Compose/dev images set it to chromium.
	defaultChromium = "chromium-browser"
	// browserStreamFPS, browserStreamWidth, browserStreamQuality and browserStreamBoundary
	// set the live view's frame rate, scaled width (about 2x a thaali panel), ffmpeg JPEG
	// quality (2 best to 31 worst) and multipart boundary; browserStreamChunk sizes the relay.
	browserStreamFPS      = 5
	browserStreamWidth    = 960
	browserStreamQuality  = 7
	browserStreamBoundary = "frame"
	browserStreamChunk    = 64 << 10
)

var chromiumSingletonLocks = []string{
	"SingletonLock",
	"SingletonCookie",
	"SingletonSocket",
}

// browserHost manages headed Chromium + Xvfb instances for the host agent API.
// On the appliance (host.switchUser) Chromium runs as dadi; compose/dev keep the process user.
type browserHost struct {
	host        *hostRuntime
	chromiumBin string
	createMu    sync.Mutex
	runUID      int
	runGID      int
}

type browserInfo struct {
	ID      int    `json:"id"`
	Display string `json:"display"`
	CDPURL  string `json:"cdp_url"`
	Healthy bool   `json:"healthy"`
	// DownloadsDir is where downloads belong ($DADI_HOME/Downloads). CDP clients reset the
	// download behavior when they connect, so Hath points Chromium here on every connect.
	DownloadsDir string `json:"downloads_dir"`
}

type createBrowserRequest struct {
	ID *int `json:"id"`
}

type createBrowserResponse struct {
	ID      int    `json:"id"`
	Display string `json:"display"`
	CDPURL  string `json:"cdp_url"`
	// DownloadsDir is the same as browserInfo.DownloadsDir.
	DownloadsDir string `json:"downloads_dir"`
}

type cdpVersion struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type lineLogger struct {
	browser int
	proc    string
	buf     bytes.Buffer
	mu      sync.Mutex
}

// newBrowserHost returns a browserHost using CHROMIUM_BIN, or defaultChromium when it is
// unset. On the appliance Chromium runs as dadi.
func newBrowserHost(host *hostRuntime) *browserHost {
	bin := strings.TrimSpace(os.Getenv("CHROMIUM_BIN"))
	if bin == "" {
		bin = defaultChromium
	}
	bh := &browserHost{host: host, chromiumBin: bin, runUID: -1, runGID: -1}
	if host.switchUser {
		bh.runUID = host.dadiUID
		bh.runGID = host.dadiGID
	}
	return bh
}

// register mounts the /browsers routes on mux.
func (b *browserHost) register(mux *http.ServeMux) {
	mux.HandleFunc("POST /browsers", b.handleCreate)
	mux.HandleFunc("GET /browsers", b.handleList)
	mux.HandleFunc("DELETE /browsers/{id}", b.handleDelete)
	mux.HandleFunc("GET /browsers/{id}/json/version", b.handleJSONVersion)
	mux.HandleFunc("GET /browsers/{id}/json/list", b.handleJSONList)
	mux.HandleFunc("GET /browsers/{id}/devtools/{rest...}", b.handleDevtools)
	mux.HandleFunc("GET /browsers/{id}/screenshot", b.handleScreenshot)
	mux.HandleFunc("GET /browsers/{id}/stream", b.handleStream)
}

// displayName is the X display for browser id (":<id>").
func displayName(id int) string {
	return fmt.Sprintf(":%d", id)
}

// cdpPort is the loopback Chromium DevTools port for browser id.
func cdpPort(id int) int {
	return cdpPortBase + id
}

// xSocketPath is the Xvfb socket for browser id under /tmp/.X11-unix.
func xSocketPath(id int) string {
	return filepath.Join("/tmp/.X11-unix", fmt.Sprintf("X%d", id))
}

// profileDir is browser id's Chromium user-data directory.
func (b *browserHost) profileDir(id int) string {
	return filepath.Join(b.host.browsersDir, strconv.Itoa(id))
}

// chromiumPattern matches browser id's Chromium processes for pgrep -f.
func (b *browserHost) chromiumPattern(id int) string {
	return fmt.Sprintf("remote-debugging-port=%d", cdpPort(id))
}

// xvfbPattern matches browser id's Xvfb process for pgrep -f.
func (b *browserHost) xvfbPattern(id int) string {
	return fmt.Sprintf("Xvfb :%d", id)
}

// clearSingletonLocks removes Chromium's singleton lock files from profile dir, which a
// crashed Chromium leaves behind and which block the next launch. Missing files are expected.
func clearSingletonLocks(dir string) {
	for _, name := range chromiumSingletonLocks {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// ensureProfile creates the browser profile directory and clears Chromium singleton locks.
func (b *browserHost) ensureProfile(id int) error {
	dir := b.profileDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	clearSingletonLocks(dir)
	if b.runUID >= 0 {
		if err := os.Chown(b.host.browsersDir, b.runUID, b.runGID); err != nil {
			return fmt.Errorf("chown browsers dir: %w", err)
		}
		if err := os.Chown(dir, b.runUID, b.runGID); err != nil {
			return fmt.Errorf("chown profile: %w", err)
		}
		return nil
	}
	return b.host.chownDadi(dir)
}

// chromiumArgs returns Chromium flags for this runtime. Compose adds --no-sandbox
// and --disable-dev-shm-usage when user namespaces are unavailable.
func (b *browserHost) chromiumArgs(id, port int) []string {
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + b.profileDir(id),
		"--no-first-run",
		"--no-default-browser-check",
		fmt.Sprintf("--window-size=%d,%d", browserScreenW, browserScreenH),
		"--disable-features=TranslateUI",
	}
	if b.host.runtime == "compose" {
		args = append(args, "--no-sandbox", "--disable-dev-shm-usage")
	}
	return args
}

// portFree reports whether port can be bound on 127.0.0.1.
func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// displayFree reports whether id's X socket is absent and its CDP port can be bound.
func (b *browserHost) displayFree(id int) (bool, error) {
	if _, err := os.Stat(xSocketPath(id)); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	return portFree(cdpPort(id)), nil
}

// profileFresh reports whether the profile directory for id has no Chromium user data.
// A missing directory is fresh. Singleton lock files left by a crash are not user data.
func (b *browserHost) profileFresh(id int) (bool, error) {
	entries, err := os.ReadDir(b.profileDir(id))
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "SingletonLock", "SingletonCookie", "SingletonSocket":
			continue
		default:
			return false, nil
		}
	}
	return true, nil
}

// nextFreshID returns the lowest id >= 10 whose display is down and whose profile has no Chromium data.
func (b *browserHost) nextFreshID() (int, error) {
	for n := browserIDMin; n < 10000; n++ {
		free, err := b.displayFree(n)
		if err != nil {
			return 0, err
		}
		if !free {
			continue
		}
		fresh, err := b.profileFresh(n)
		if err != nil {
			return 0, err
		}
		if !fresh {
			continue
		}
		return n, nil
	}
	return 0, fmt.Errorf("no free browser id")
}

// scanIDs returns the browser ids (>= browserIDMin) that have an X socket, ascending.
func (b *browserHost) scanIDs() ([]int, error) {
	entries, err := os.ReadDir("/tmp/.X11-unix")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "X") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(name, "X"))
		if err != nil || n < browserIDMin {
			continue
		}
		ids = append(ids, n)
	}
	sort.Ints(ids)
	return ids, nil
}

// Write buffers Chromium or Xvfb stderr and logs each complete line at info, tagged with
// the browser id and process name.
func (l *lineLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(p)
	for {
		chunk := l.buf.Bytes()
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(chunk[:i]), "\r")
		l.buf.Next(i + 1)
		if line != "" {
			slog.Info("browser process", "browser", l.browser, "proc", l.proc, "msg", line)
		}
	}
	return len(p), nil
}

// String returns the output not yet terminated by a newline.
func (l *lineLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// startDetached starts name in its own session. When asDadi is true and runUID is set,
// the process runs as dadi via runuser; otherwise it keeps the current user (Xvfb).
// On the appliance (podman runtime) it runs in a transient scope under dadi-browsers.slice,
// so browser memory is capped apart from nas.service and an OOM kills a browser, not a module.
func (b *browserHost) startDetached(
	browserID int,
	procName string,
	name string,
	args []string,
	env []string,
	asDadi bool,
) (*exec.Cmd, *lineLogger, error) {
	log := &lineLogger{browser: browserID, proc: procName}
	argv := append([]string{name}, args...)
	if asDadi && b.runUID >= 0 {
		argv = append([]string{"runuser", "-u", dadiUsername, "--", "setsid"}, argv...)
	}
	if b.host.runtime == "podman" {
		argv = append([]string{"systemd-run", "--scope", "--quiet", "--collect", "--slice=dadi-browsers.slice", "--"}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		return nil, log, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd, log, nil
}

// waitForFile polls until path exists or timeout passes and reports whether it appeared.
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// waitForCDP polls the Chromium DevTools /json/version endpoint until it returns
// 200 or timeout. If alive was true at least once and later returns false, wait ends early.
func waitForCDP(port int, timeout time.Duration, alive func() bool) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	versionURL := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	deadline := time.Now().Add(timeout)
	seenAlive := alive == nil
	for time.Now().Before(deadline) {
		if alive != nil {
			if alive() {
				seenAlive = true
			} else if seenAlive {
				return false
			}
		}
		resp, err := client.Get(versionURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// findPIDs returns the pids whose command line matches pattern. pgrep exit status 1
// means no match; any other pgrep failure is logged with CodeInternal and also yields none.
func findPIDs(pattern string) []int {
	out, err := exec.Command("pgrep", "-f", pattern).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			slog.Error("pgrep failed", "code", CodeInternal, "pattern", pattern, "err", err)
		}
		return nil
	}
	var pids []int
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// signalPIDs sends sig to each pid and once to each pid's process group, so runuser,
// setsid and systemd-run wrappers and their children are all reached.
func signalPIDs(pids []int, sig syscall.Signal) {
	seen := map[int]struct{}{}
	for _, pid := range pids {
		pgid, err := syscall.Getpgid(pid)
		if err == nil {
			if _, ok := seen[pgid]; !ok {
				seen[pgid] = struct{}{}
				_ = syscall.Kill(-pgid, sig)
			}
		}
		_ = syscall.Kill(pid, sig)
	}
}

// processesExist reports whether browser id still has an X socket, a Chromium or an Xvfb process.
func (b *browserHost) processesExist(id int) bool {
	if _, err := os.Stat(xSocketPath(id)); err == nil {
		return true
	}
	return len(findPIDs(b.chromiumPattern(id))) > 0 || len(findPIDs(b.xvfbPattern(id))) > 0
}

// killBrowser stops browser id's Chromium and Xvfb with SIGTERM, waits up to browserKillWait,
// sends SIGKILL to what remains, and removes the X socket and singleton locks. The profile is kept.
func (b *browserHost) killBrowser(id int) {
	signalPIDs(findPIDs(b.chromiumPattern(id)), syscall.SIGTERM)
	signalPIDs(findPIDs(b.xvfbPattern(id)), syscall.SIGTERM)
	deadline := time.Now().Add(browserKillWait)
	for time.Now().Before(deadline) {
		if !b.processesExist(id) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	signalPIDs(findPIDs(b.chromiumPattern(id)), syscall.SIGKILL)
	signalPIDs(findPIDs(b.xvfbPattern(id)), syscall.SIGKILL)
	_ = os.Remove(xSocketPath(id))
	clearSingletonLocks(b.profileDir(id))
}

// rewriteCDPBody rewrites loopback websocket debugger URLs to the nas proxy path.
func rewriteCDPBody(body []byte, host string, id, port int) []byte {
	to := fmt.Sprintf("ws://%s/browsers/%d/", host, id)
	out := bytes.ReplaceAll(body, []byte(fmt.Sprintf("ws://127.0.0.1:%d/", port)), []byte(to))
	return bytes.ReplaceAll(out, []byte(fmt.Sprintf("ws://localhost:%d/", port)), []byte(to))
}

// fetchVersion returns Chromium's /json/version body for browser id.
func (b *browserHost) fetchVersion(id int) ([]byte, error) {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", cdpPort(id)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cdp version status %d", resp.StatusCode)
	}
	return body, nil
}

// cdpURLFor returns browser id's websocket debugger URL rewritten to the nas proxy path for
// r's host. ok is false when Chromium does not answer or reports no URL.
func (b *browserHost) cdpURLFor(r *http.Request, id int) (string, bool) {
	body, err := b.fetchVersion(id)
	if err != nil {
		return "", false
	}
	var ver cdpVersion
	if err := json.Unmarshal(rewriteCDPBody(body, r.Host, id, cdpPort(id)), &ver); err != nil {
		return "", false
	}
	return ver.WebSocketDebuggerURL, ver.WebSocketDebuggerURL != ""
}

// chromiumReadyError describes why Chromium never served CDP: its buffered stderr, prefixed
// with "no chromium process" when the process is gone.
func chromiumReadyError(log *lineLogger, alive bool) string {
	msg := strings.TrimSpace(log.String())
	if alive {
		return msg
	}
	if msg == "" {
		return "no chromium process"
	}
	return "no chromium process; " + msg
}

// handleCreate is POST /browsers. It starts Xvfb and Chromium for the requested id, or the
// lowest fresh id, waits for CDP and returns the proxied websocket URL. Any failed step kills
// what was started and returns the error.
func (b *browserHost) handleCreate(w http.ResponseWriter, r *http.Request) {
	b.createMu.Lock()
	defer b.createMu.Unlock()

	var req createBrowserRequest
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil && err != io.EOF {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
			return
		}
	}

	var id int
	if req.ID != nil {
		id = *req.ID
		if id < browserIDMin {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "id must be >= 10")
			return
		}
		free, err := b.displayFree(id)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if !free {
			writeError(w, r, http.StatusConflict, CodeConflict, "browser id is already running")
			return
		}
	} else {
		var err error
		id, err = b.nextFreshID()
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
	}
	if err := b.ensureProfile(id); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}

	display := displayName(id)
	port := cdpPort(id)
	if err := os.MkdirAll("/tmp/.X11-unix", 0o1777); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "mkdir X11 unix: "+err.Error())
		return
	}
	if err := os.Chmod("/tmp/.X11-unix", 0o1777); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "chmod X11 unix: "+err.Error())
		return
	}

	xvfbArgs := []string{
		display,
		"-screen", "0", fmt.Sprintf("%dx%dx24", browserScreenW, browserScreenH),
		"-nolisten", "tcp",
	}
	_, xvfbLog, err := b.startDetached(id, "Xvfb", "Xvfb", xvfbArgs, nil, false)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "start Xvfb: "+err.Error())
		return
	}
	if !waitForFile(xSocketPath(id), xvfbWait) {
		b.killBrowser(id)
		writeError(w, r, http.StatusInternalServerError, CodeInternal,
			"Xvfb display socket did not appear: "+xvfbLog.String())
		return
	}

	_, chromeLog, err := b.startDetached(id, "chromium", b.chromiumBin, b.chromiumArgs(id, port), []string{
		"DISPLAY=" + display,
		"HOME=" + b.profileDir(id),
	}, true)
	if err != nil {
		b.killBrowser(id)
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "start chromium: "+err.Error())
		return
	}
	alive := func() bool { return len(findPIDs(b.chromiumPattern(id))) > 0 }
	if !waitForCDP(port, chromiumWait, alive) {
		errMsg := chromiumReadyError(chromeLog, alive())
		b.killBrowser(id)
		writeError(w, r, http.StatusInternalServerError, CodeInternal,
			"chromium CDP did not become ready: "+errMsg)
		return
	}

	cdpURL, ok := b.cdpURLFor(r, id)
	if !ok {
		b.killBrowser(id)
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "missing webSocketDebuggerUrl")
		return
	}
	writeJSON(w, http.StatusOK, createBrowserResponse{
		ID:           id,
		Display:      display,
		CDPURL:       cdpURL,
		DownloadsDir: filepath.Join(b.host.homeDir, "Downloads"),
	})
}

// handleList is GET /browsers: every running display with its CDP URL. Healthy is false when
// Chromium does not answer on its CDP port.
func (b *browserHost) handleList(w http.ResponseWriter, r *http.Request) {
	ids, err := b.scanIDs()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	out := make([]browserInfo, 0, len(ids))
	for _, id := range ids {
		info := browserInfo{
			ID:           id,
			Display:      displayName(id),
			DownloadsDir: filepath.Join(b.host.homeDir, "Downloads"),
		}
		if cdp, ok := b.cdpURLFor(r, id); ok {
			info.CDPURL = cdp
			info.Healthy = true
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, out)
}

// parseID reads the {id} path value and writes 404 when it is not a browser id.
func (b *browserHost) parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < browserIDMin {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
		return 0, false
	}
	return id, true
}

// requireDisplay writes 404 and returns false when browser id has no X socket.
func (b *browserHost) requireDisplay(w http.ResponseWriter, r *http.Request, id int) bool {
	if _, err := os.Stat(xSocketPath(id)); err != nil {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
		return false
	}
	return true
}

// handleDelete kills the browser if it is running and removes its profile, so a
// deleted id comes back as a fresh login. A browser that died without a delete
// (crash, host or Nas restart) keeps its profile and can be respawned by id.
func (b *browserHost) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := b.parseID(w, r)
	if !ok {
		return
	}
	if b.processesExist(id) {
		b.killBrowser(id)
	}
	if err := os.RemoveAll(b.profileDir(id)); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleJSONVersion is GET /browsers/{id}/json/version, Chromium's version document with
// websocket URLs rewritten to the nas proxy path.
func (b *browserHost) handleJSONVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := b.parseID(w, r)
	if !ok {
		return
	}
	if !b.requireDisplay(w, r, id) {
		return
	}
	body, err := b.fetchVersion(id)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, CodeUpstreamUnreachable, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rewriteCDPBody(body, r.Host, id, cdpPort(id)))
}

// handleJSONList is GET /browsers/{id}/json/list, Chromium's target list with websocket URLs
// rewritten to the nas proxy path.
func (b *browserHost) handleJSONList(w http.ResponseWriter, r *http.Request) {
	id, ok := b.parseID(w, r)
	if !ok {
		return
	}
	if !b.requireDisplay(w, r, id) {
		return
	}
	port := cdpPort(id)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		writeError(w, r, http.StatusBadGateway, CodeUpstreamUnreachable, err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, CodeUpstreamUnreachable, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(rewriteCDPBody(body, r.Host, id, port))
}

// handleDevtools is GET /browsers/{id}/devtools/..., a reverse proxy (websocket upgrades
// included) to the browser's loopback DevTools endpoint.
func (b *browserHost) handleDevtools(w http.ResponseWriter, r *http.Request) {
	id, ok := b.parseID(w, r)
	if !ok {
		return
	}
	if !b.requireDisplay(w, r, id) {
		return
	}
	rest := r.PathValue("rest")
	port := cdpPort(id)
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = "/devtools/" + rest
			req.Host = target.Host
			if req.URL.RawQuery == "" {
				req.URL.RawQuery = r.URL.RawQuery
			}
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			slog.Warn("cdp proxy error", "browser", id, "err", err)
			writeError(rw, req, http.StatusBadGateway, CodeUpstreamUnreachable, err.Error())
		},
	}
	proxy.ServeHTTP(w, r)
}

// handleScreenshot is GET /browsers/{id}/screenshot, a PNG of the whole virtual display taken
// with ImageMagick import.
func (b *browserHost) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	id, ok := b.parseID(w, r)
	if !ok {
		return
	}
	if !b.requireDisplay(w, r, id) {
		return
	}
	cmd := b.host.command("import", "-display", displayName(id), "-window", "root", "png:-")
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg = string(ee.Stderr)
			if msg == "" {
				msg = ee.Error()
			}
		}
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "screenshot: "+msg)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// handleStream sends the virtual monitor as MJPEG (multipart/x-mixed-replace) from an
// ffmpeg x11grab owned by this request, scaled to browserStreamWidth. The first frame is
// awaited before headers so a capture failure still returns JSON with ffmpeg's stderr.
// ffmpeg is stopped when the client disconnects or the copy ends; the signal and wait
// results are discarded because by then ffmpeg has exited or is exiting on that signal.
func (b *browserHost) handleStream(w http.ResponseWriter, r *http.Request) {
	id, ok := b.parseID(w, r)
	if !ok {
		return
	}
	if !b.requireDisplay(w, r, id) {
		return
	}
	cmd := b.host.command("ffmpeg",
		"-nostdin", "-loglevel", "error",
		"-f", "x11grab", "-framerate", strconv.Itoa(browserStreamFPS), "-i", displayName(id),
		"-vf", fmt.Sprintf("scale=%d:-2", browserStreamWidth),
		"-pix_fmt", "yuvj420p", "-q:v", strconv.Itoa(browserStreamQuality),
		"-f", "mpjpeg", "-boundary_tag", browserStreamBoundary, "pipe:1",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "stream: "+err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "stream: "+err.Error())
		return
	}
	stop := context.AfterFunc(r.Context(), func() { _ = cmd.Process.Signal(syscall.SIGTERM) })
	frames := bufio.NewReaderSize(stdout, browserStreamChunk)
	if _, err := frames.Peek(1); err != nil {
		stop()
		waitErr := cmd.Wait()
		msg := fmt.Sprintf("stream: ffmpeg exited before the first frame (%v): %s", waitErr, strings.TrimSpace(stderr.String()))
		writeError(w, r, http.StatusInternalServerError, CodeInternal, msg)
		return
	}
	defer func() {
		stop()
		clientGone := r.Context().Err() != nil
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		if !clientGone {
			slog.Warn("browser stream ended", "browser", id, "stderr", strings.TrimSpace(stderr.String()))
		}
	}()

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+browserStreamBoundary)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	buf := make([]byte, browserStreamChunk)
	for {
		n, readErr := frames.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}
