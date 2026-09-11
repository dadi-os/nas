package main

import (
	"bufio"
	"bytes"
	"encoding/json"
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
	defaultChromium = "chromium-browser"
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
}

type createBrowserResponse struct {
	ID      int    `json:"id"`
	Display string `json:"display"`
	CDPURL  string `json:"cdp_url"`
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

func (b *browserHost) register(mux *http.ServeMux) {
	mux.HandleFunc("POST /browsers", b.handleCreate)
	mux.HandleFunc("GET /browsers", b.handleList)
	mux.HandleFunc("DELETE /browsers/{id}", b.handleDelete)
	mux.HandleFunc("GET /browsers/{id}/json/version", b.handleJSONVersion)
	mux.HandleFunc("GET /browsers/{id}/json/list", b.handleJSONList)
	mux.HandleFunc("GET /browsers/{id}/devtools/{rest...}", b.handleDevtools)
	mux.HandleFunc("GET /browsers/{id}/screenshot", b.handleScreenshot)
}

func displayName(id int) string {
	return fmt.Sprintf(":%d", id)
}

func cdpPort(id int) int {
	return cdpPortBase + id
}

func xSocketPath(id int) string {
	return filepath.Join("/tmp/.X11-unix", fmt.Sprintf("X%d", id))
}

func (b *browserHost) profileDir(id int) string {
	return filepath.Join(b.host.browsersDir, strconv.Itoa(id))
}

func (b *browserHost) chromiumPattern(id int) string {
	return fmt.Sprintf("remote-debugging-port=%d", cdpPort(id))
}

func (b *browserHost) xvfbPattern(id int) string {
	return fmt.Sprintf("Xvfb :%d", id)
}

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

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func (b *browserHost) nextID() (int, error) {
	for n := browserIDMin; n < 10000; n++ {
		if _, err := os.Stat(xSocketPath(n)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return 0, err
		}
		if !portFree(cdpPort(n)) {
			continue
		}
		return n, nil
	}
	return 0, fmt.Errorf("no free browser id")
}

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

func (l *lineLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// startDetached starts name under setsid. When asDadi is true and runUID is set,
// the process runs as dadi via runuser; otherwise it keeps the current user (Xvfb).
func (b *browserHost) startDetached(
	browserID int,
	procName string,
	name string,
	args []string,
	env []string,
	asDadi bool,
) (*exec.Cmd, *lineLogger, error) {
	log := &lineLogger{browser: browserID, proc: procName}
	var cmd *exec.Cmd
	if asDadi && b.runUID >= 0 {
		full := append([]string{"-u", dadiUsername, "--", "setsid", name}, args...)
		cmd = exec.Command("runuser", full...)
	} else {
		cmd = exec.Command(name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
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

func findPIDs(pattern string) []int {
	out, err := exec.Command("pgrep", "-f", pattern).Output()
	if err != nil {
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

func (b *browserHost) processesExist(id int) bool {
	if _, err := os.Stat(xSocketPath(id)); err == nil {
		return true
	}
	return len(findPIDs(b.chromiumPattern(id))) > 0 || len(findPIDs(b.xvfbPattern(id))) > 0
}

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

func (b *browserHost) handleCreate(w http.ResponseWriter, r *http.Request) {
	b.createMu.Lock()
	defer b.createMu.Unlock()

	id, err := b.nextID()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
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
	_ = os.Chmod("/tmp/.X11-unix", 0o1777)

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
		ID:      id,
		Display: display,
		CDPURL:  cdpURL,
	})
}

func (b *browserHost) handleList(w http.ResponseWriter, r *http.Request) {
	ids, err := b.scanIDs()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	out := make([]browserInfo, 0, len(ids))
	for _, id := range ids {
		info := browserInfo{
			ID:      id,
			Display: displayName(id),
		}
		if cdp, ok := b.cdpURLFor(r, id); ok {
			info.CDPURL = cdp
			info.Healthy = true
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, out)
}

func (b *browserHost) parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < browserIDMin {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
		return 0, false
	}
	return id, true
}

func (b *browserHost) requireDisplay(w http.ResponseWriter, r *http.Request, id int) bool {
	if _, err := os.Stat(xSocketPath(id)); err != nil {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
		return false
	}
	return true
}

func (b *browserHost) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := b.parseID(w, r)
	if !ok {
		return
	}
	if !b.processesExist(id) {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
		return
	}
	b.killBrowser(id)
	w.WriteHeader(http.StatusNoContent)
}

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
