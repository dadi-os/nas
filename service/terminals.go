package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	execMarker       = "__NAS_EXEC__"
	defaultExecTO    = 120
	maxExecTO        = 3600
	defaultMaxBytes  = 32768
	defaultCaptureN  = 200
	historyLimit     = 50000
	execPollInterval = 200 * time.Millisecond
)

var sessionNameRe = regexp.MustCompile(`^t([1-9][0-9]*)$`)

type terminalHost struct {
	host     *hostRuntime
	mu       sync.Mutex
	createMu sync.Mutex
	busy     map[string]struct{}
	// killGrace is how long a shell gets, after the command that outlived its timeout is
	// killed, to print the exec marker before the session itself is ended.
	killGrace time.Duration
}

// newTerminalHost returns the terminal API backed by host's tmux server. killGrace is
// TERMINAL_KILL_GRACE_SECONDS.
func newTerminalHost(host *hostRuntime, killGrace time.Duration) *terminalHost {
	return &terminalHost{
		host:      host,
		busy:      make(map[string]struct{}),
		killGrace: killGrace,
	}
}

// tmuxCmd returns a tmux command on the nas tmux socket, run as dadi on the appliance. On the
// appliance new-session, the one command that can start the tmux server, runs in a transient
// scope under dadi-terminals.slice, so the server and every shell and build under it are capped
// apart from nas.service and an OOM kills a build, not Nas.
func (t *terminalHost) tmuxCmd(args ...string) *exec.Cmd {
	full := append([]string{"-S", t.host.tmuxSocket}, args...)
	if !t.host.switchUser {
		return exec.Command("tmux", full...)
	}
	argv := append([]string{"runuser", "-u", dadiUsername, "--", "tmux"}, full...)
	if len(args) > 0 && args[0] == "new-session" {
		argv = append([]string{"systemd-run", "--scope", "--quiet", "--collect", "--slice=dadi-terminals.slice", "--"}, argv...)
	}
	return exec.Command(argv[0], argv[1:]...)
}

// tmuxOutput runs tmux with args and returns its combined output, with the output in the
// error on failure.
func (t *terminalHost) tmuxOutput(args ...string) (string, error) {
	out, err := t.tmuxCmd(args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// hasSession reports whether tmux session id exists.
func (t *terminalHost) hasSession(id string) bool {
	err := t.tmuxCmd("has-session", "-t", id).Run()
	return err == nil
}

// requireSession writes 404 and returns false when id is not a running terminal.
func (t *terminalHost) requireSession(w http.ResponseWriter, r *http.Request, id string) bool {
	if !sessionNameRe.MatchString(id) || !t.hasSession(id) {
		writeError(w, r, http.StatusNotFound, CodeNotFound, fmt.Sprintf("terminal %s is not running", id))
		return false
	}
	return true
}

// paneCommand returns the foreground command in session id's pane.
func (t *terminalHost) paneCommand(id string) (string, error) {
	out, err := t.tmuxOutput("display-message", "-p", "-t", id, "#{pane_current_command}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// isPaneBusy reports whether session id's pane runs something other than the idle shell.
func (t *terminalHost) isPaneBusy(id string) (bool, error) {
	cmd, err := t.paneCommand(id)
	if err != nil {
		return false, err
	}
	return cmd != "" && cmd != t.host.idleShell, nil
}

// listSessionNames returns the tmux session names. No running tmux server means no sessions.
func (t *terminalHost) listSessionNames() (map[string]struct{}, error) {
	out, err := t.tmuxCmd("ls", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		if isTmuxIdleError(err, out) {
			return map[string]struct{}{}, nil
		}
		return nil, fmt.Errorf("tmux ls: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	names := map[string]struct{}{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		names[line] = struct{}{}
	}
	return names, nil
}

// isTmuxIdleError reports whether a tmux ls failure only means no server is running.
func isTmuxIdleError(err error, out []byte) bool {
	text := string(out) + err.Error()
	return strings.Contains(text, "no server running") ||
		strings.Contains(text, "error connecting") ||
		strings.Contains(err.Error(), "exit status 1")
}

// nextSessionID returns the lowest t<n> not in use.
func (t *terminalHost) nextSessionID() (string, error) {
	names, err := t.listSessionNames()
	if err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		id := fmt.Sprintf("t%d", n)
		if _, ok := names[id]; !ok {
			return id, nil
		}
	}
}

type createTerminalRequest struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd"`
}

type createTerminalResponse struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd"`
}

// handleCreate is POST /terminals: starts a tmux session in cwd (the dadi home when omitted)
// under the requested id, or the lowest free one.
func (t *terminalHost) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createTerminalRequest
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil && err != io.EOF {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
			return
		}
	}
	created, err := t.create(req.ID, req.Cwd)
	if err != nil {
		writeAPIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, created)
}

// create starts a tmux session in cwd (the dadi home when empty) under id, or the lowest
// free t<n> when id is empty. Bad input and a running id are apiErrors.
func (t *terminalHost) create(id, cwd string) (createTerminalResponse, error) {
	t.createMu.Lock()
	defer t.createMu.Unlock()

	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = t.host.defaultCwd()
	}
	if !filepathIsAbs(cwd) {
		return createTerminalResponse{}, apiError{http.StatusBadRequest, CodeInvalidRequest, "cwd must be absolute"}
	}
	cwd = filepath.Clean(cwd)
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return createTerminalResponse{}, apiError{http.StatusBadRequest, CodeInvalidRequest, "cwd must be an existing directory"}
	}
	id = strings.TrimSpace(id)
	if id != "" {
		if !sessionNameRe.MatchString(id) {
			return createTerminalResponse{}, apiError{http.StatusBadRequest, CodeInvalidRequest, "id must be t<n> for a positive integer n"}
		}
		if t.hasSession(id) {
			return createTerminalResponse{}, apiError{http.StatusConflict, CodeConflict, "terminal id is already running"}
		}
	} else {
		var err error
		id, err = t.nextSessionID()
		if err != nil {
			return createTerminalResponse{}, err
		}
	}
	if _, err := t.tmuxOutput("new-session", "-d", "-s", id, "-c", cwd); err != nil {
		return createTerminalResponse{}, err
	}
	if _, err := t.tmuxOutput("set-option", "-t", id, "history-limit", strconv.Itoa(historyLimit)); err != nil {
		_ = t.tmuxCmd("kill-session", "-t", id).Run()
		return createTerminalResponse{}, err
	}
	return createTerminalResponse{ID: id, Cwd: cwd}, nil
}

type terminalInfo struct {
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	CreatedAt string `json:"created_at"`
	Busy      bool   `json:"busy"`
}

// sessionInfo returns session id's cwd, creation time and whether it is busy with an exec
// or a foreground command.
func (t *terminalHost) sessionInfo(id string) (terminalInfo, error) {
	cwd, err := t.tmuxOutput("display-message", "-p", "-t", id, "#{pane_current_path}")
	if err != nil {
		return terminalInfo{}, err
	}
	createdRaw, err := t.tmuxOutput("display-message", "-p", "-t", id, "#{session_created}")
	if err != nil {
		return terminalInfo{}, err
	}
	paneCmd, err := t.tmuxOutput("display-message", "-p", "-t", id, "#{pane_current_command}")
	if err != nil {
		return terminalInfo{}, err
	}
	createdUnix, _ := strconv.ParseInt(strings.TrimSpace(createdRaw), 10, 64)
	paneCmd = strings.TrimSpace(paneCmd)
	_, execBusy := t.busy[id]
	busy := execBusy || (paneCmd != "" && paneCmd != t.host.idleShell)
	return terminalInfo{
		ID:        id,
		Cwd:       strings.TrimSpace(cwd),
		CreatedAt: time.Unix(createdUnix, 0).UTC().Format(time.RFC3339),
		Busy:      busy,
	}, nil
}

// handleList is GET /terminals: every t<n> session, sorted by id. A session whose details
// cannot be read is logged with CodeInternal and left out.
func (t *terminalHost) handleList(w http.ResponseWriter, r *http.Request) {
	names, err := t.listSessionNames()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	ids := make([]string, 0, len(names))
	for id := range names {
		if sessionNameRe.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	t.mu.Lock()
	defer t.mu.Unlock()
	list := make([]terminalInfo, 0, len(ids))
	for _, id := range ids {
		info, err := t.sessionInfo(id)
		if err != nil {
			slog.Error("terminal info failed", "code", CodeInternal, "terminal", id, "err", err)
			continue
		}
		list = append(list, info)
	}
	writeJSON(w, http.StatusOK, list)
}

type execRequest struct {
	Command        string `json:"command"`
	TimeoutSeconds *int   `json:"timeout_seconds"`
	MaxBytes       *int   `json:"max_bytes"`
}

type execResponse struct {
	ExitCode  *int   `json:"exit_code"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
	TimedOut  bool   `json:"timed_out"`
	// Killed is true when the command outlived its timeout and its process group was killed;
	// the shell is still usable.
	Killed bool `json:"killed"`
	// ShellEnded is true when the shell did not answer within killGrace of that kill and the
	// session was ended with every process under it; the terminal id is gone.
	ShellEnded bool `json:"shell_ended"`
}

// handleExec runs one command in session id and waits for its exit marker. The command is
// evaluated in a subshell with stdin closed, so nothing it does to shell state (set -e, exit,
// exec, cd) outlives it or ends the session, and it cannot swallow what is typed after it.
// A command still running at its timeout is killed (see endCommand).
func (t *terminalHost) handleExec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !t.requireSession(w, r, id) {
		return
	}
	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "command is required")
		return
	}
	timeout := defaultExecTO
	if req.TimeoutSeconds != nil {
		timeout = *req.TimeoutSeconds
	}
	if timeout < 1 || timeout > maxExecTO {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "timeout_seconds must be 1..3600")
		return
	}
	maxBytes := defaultMaxBytes
	if req.MaxBytes != nil {
		maxBytes = *req.MaxBytes
	}
	if maxBytes < 1 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "max_bytes must be positive")
		return
	}

	t.mu.Lock()
	if _, ok := t.busy[id]; ok {
		t.mu.Unlock()
		writeError(w, r, http.StatusConflict, CodeBusy, "terminal is busy")
		return
	}
	paneBusy, err := t.isPaneBusy(id)
	if err != nil {
		t.mu.Unlock()
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	if paneBusy {
		t.mu.Unlock()
		writeError(w, r, http.StatusConflict, CodeBusy, "terminal is busy")
		return
	}
	t.busy[id] = struct{}{}
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.busy, id)
		t.mu.Unlock()
	}()

	nonceBytes := make([]byte, 8)
	if _, err := rand.Read(nonceBytes); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "nonce")
		return
	}
	nonce := hex.EncodeToString(nonceBytes)
	execLine := "( eval " + shellSingleQuote(req.Command) + " ) </dev/null; printf '\\n" + execMarker + "%s %d\\n' " + shellSingleQuote(nonce) + " $?"
	if err := t.tmuxCmd("send-keys", "-t", id, "-l", "--", execLine).Run(); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "send-keys: "+err.Error())
		return
	}
	if err := t.tmuxCmd("send-keys", "-t", id, "Enter").Run(); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "send-keys enter: "+err.Error())
		return
	}

	markerPrefix := execMarker + nonce + " "
	var resp execResponse
	code, output, capOut, ok, err := t.awaitMarker(id, execLine, markerPrefix, time.Now().Add(time.Duration(timeout)*time.Second))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	if ok {
		resp.ExitCode = &code
	} else {
		resp.TimedOut = true
		output = partialExecOutput(capOut, execLine)
		resp.Killed, resp.ShellEnded = t.endCommand(id, execLine, markerPrefix, timeout)
	}

	truncated, trunc := truncateHeadTail(output, maxBytes)
	resp.Output = truncated
	resp.Truncated = trunc
	writeJSON(w, http.StatusOK, resp)
}

// awaitMarker polls session id's pane until markerPrefix's exit-code line appears or deadline
// passes. It returns the exit code and output when ok, and always the last capture.
func (t *terminalHost) awaitMarker(id, execLine, markerPrefix string, deadline time.Time) (code int, output, capture string, ok bool, err error) {
	for {
		capture, err = t.tmuxOutput("capture-pane", "-p", "-J", "-t", id, "-S", "-")
		if err != nil {
			return 0, "", "", false, err
		}
		if code, output, ok = parseExecCapture(capture, execLine, markerPrefix); ok {
			return code, output, capture, true, nil
		}
		if time.Now().After(deadline) {
			return 0, "", capture, false, nil
		}
		time.Sleep(execPollInterval)
	}
}

// endCommand deals with a command that outlived its timeout: it kills the pane's foreground
// process group, then waits up to killGrace for the shell to print the exec marker. A shell
// that does so is killed=true and stays; one that does not is ended with every process under
// it (shellEnded=true), so a wedged shell is never left behind for the next exec to queue on.
func (t *terminalHost) endCommand(id, execLine, markerPrefix string, timeout int) (killed, shellEnded bool) {
	if err := t.killForeground(id); err != nil {
		slog.Error("terminal foreground kill failed", "code", CodeInternal, "terminal", id, "err", err)
	} else if _, _, _, ok, err := t.awaitMarker(id, execLine, markerPrefix, time.Now().Add(t.killGrace)); err == nil && ok {
		slog.Warn("terminal command killed at its timeout", "code", CodeCommandTimeout, "terminal", id, "timeout_seconds", timeout)
		return true, false
	}
	if err := t.kill(id, true); err != nil {
		slog.Error("terminal kill failed", "code", CodeInternal, "terminal", id, "err", err)
	}
	slog.Error("terminal shell unresponsive after its command was killed, session ended", "code", CodeShellUnresponsive, "terminal", id, "timeout_seconds", timeout)
	return false, true
}

// processEntry is one row of the process table.
type processEntry struct{ pid, pgid int }

// listProcesses returns the processes that have tty (such as "pts/3") as their controlling
// terminal, as pid and process group.
func listProcesses(tty string) ([]processEntry, error) {
	out, err := exec.Command("ps", "-t", tty, "-o", "pid=,pgid=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps -t %s: %w", tty, err)
	}
	var procs []processEntry
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		pgid, err2 := 0, error(nil)
		if len(f) == 2 {
			pgid, err2 = strconv.Atoi(f[1])
		}
		if len(f) != 2 || err1 != nil || err2 != nil {
			return nil, fmt.Errorf("ps -t %s: unreadable row %q", tty, line)
		}
		procs = append(procs, processEntry{pid, pgid})
	}
	return procs, nil
}

// panePID returns the pid of session id's shell.
func (t *terminalHost) panePID(id string) (int, error) {
	out, err := t.tmuxOutput("display-message", "-p", "-t", id, "#{pane_pid}")
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("pane pid %q: %w", strings.TrimSpace(out), err)
	}
	return pid, nil
}

// killForeground SIGKILLs the foreground process group of session id's terminal, the running
// command and everything it started, unless that group is the shell's own.
func (t *terminalHost) killForeground(id string) error {
	pid, err := t.panePID(id)
	if err != nil {
		return err
	}
	out, err := exec.Command("ps", "-o", "tpgid=,pgid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return fmt.Errorf("ps %d: %w", pid, err)
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return fmt.Errorf("ps %d: unreadable row %q", pid, strings.TrimSpace(string(out)))
	}
	fg, err1 := strconv.Atoi(f[0])
	own, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return fmt.Errorf("ps %d: unreadable row %q", pid, strings.TrimSpace(string(out)))
	}
	if fg <= 0 || fg == own {
		return nil
	}
	if err := syscall.Kill(-fg, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("kill group %d: %w", fg, err)
	}
	return nil
}

// killTree SIGKILLs every process on the controlling terminal tty, with each process group
// they occupy. A background job started from the shell keeps that terminal after its subshell
// exits and Nas's exec leaves it orphaned, so walking children would miss it. Nas's own group
// is never signalled. A process that started its own session (setsid) is out of reach.
func killTree(tty string) error {
	procs, err := listProcesses(tty)
	if err != nil {
		return err
	}
	self := syscall.Getpgrp()
	groups := map[int]bool{}
	for _, p := range procs {
		if p.pgid > 1 && p.pgid != self {
			groups[p.pgid] = true
		}
	}
	for g := range groups {
		_ = syscall.Kill(-g, syscall.SIGKILL)
	}
	for _, p := range procs {
		if p.pid != os.Getpid() {
			_ = syscall.Kill(p.pid, syscall.SIGKILL)
		}
	}
	return nil
}

// parseExecCapture finds markerPrefix's exit-code line in a pane capture and returns the
// output between the echoed sentLine and that marker. ok is false until the marker appears.
// When the echo does not match whole (a long command wraps), the nearest earlier echo of
// any exec line is the start: searching from the top of the scrollback would return every
// earlier command's output too.
func parseExecCapture(capture, sentLine, markerPrefix string) (exitCode int, output string, ok bool) {
	lines := splitPaneLines(capture)
	markerIdx := -1
	var code int
	for i, ln := range lines {
		if strings.HasPrefix(ln, markerPrefix) {
			rest := strings.TrimPrefix(ln, markerPrefix)
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				continue
			}
			markerIdx = i
			code = n
			break
		}
	}
	if markerIdx < 0 {
		return 0, "", false
	}
	cmdIdx := -1
	for i := markerIdx - 1; i >= 0; i-- {
		if lines[i] == sentLine || strings.HasSuffix(lines[i], sentLine) || strings.Contains(lines[i], sentLine) {
			cmdIdx = i
			break
		}
	}
	if cmdIdx < 0 {
		for i := markerIdx - 1; i >= 0; i-- {
			if strings.Contains(lines[i], execMarker) && strings.Contains(lines[i], "printf") {
				cmdIdx = i
				break
			}
		}
	}
	start := cmdIdx + 1
	if cmdIdx < 0 {
		start = 0
	}
	if start > markerIdx {
		start = markerIdx
	}
	body := scrubExecMarkerLines(strings.Join(lines[start:markerIdx], "\n"))
	return code, body, true
}

// partialExecOutput returns the output after the last echo of sentLine, for a timed-out exec.
func partialExecOutput(capture, sentLine string) string {
	lines := splitPaneLines(capture)
	cmdIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] == sentLine || strings.HasSuffix(lines[i], sentLine) || strings.Contains(lines[i], sentLine) {
			cmdIdx = i
			break
		}
	}
	if cmdIdx < 0 {
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.Contains(lines[i], execMarker) && strings.Contains(lines[i], "printf") {
				cmdIdx = i
				break
			}
		}
	}
	if cmdIdx < 0 {
		return scrubExecMarkerLines(strings.Join(lines, "\n"))
	}
	return scrubExecMarkerLines(strings.Join(lines[cmdIdx+1:], "\n"))
}

// scrubExecMarkerLines drops any line that still contains the exec marker (wrapped pane echo).
func scrubExecMarkerLines(s string) string {
	lines := splitPaneLines(s)
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.Contains(ln, execMarker) {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// splitPaneLines splits pane text into lines, normalizing CR and CRLF.
func splitPaneLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// truncateHeadTail keeps s within maxBytes by eliding its middle, and reports whether it did.
func truncateHeadTail(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	elided := len(s)
	note := fmt.Sprintf("\n... [%d bytes elided] ...\n", elided)
	if len(note) >= maxBytes {
		return s[:maxBytes], true
	}
	keep := maxBytes - len(note)
	head := keep / 2
	tail := keep - head
	return s[:head] + note + s[len(s)-tail:], true
}

// shellSingleQuote quotes s as a single POSIX shell word.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type captureResponse struct {
	Output string `json:"output"`
}

// handleCapture is GET /terminals/{id}/capture: the last lines (default 200) of the pane.
func (t *terminalHost) handleCapture(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !t.requireSession(w, r, id) {
		return
	}
	lines := defaultCaptureN
	if raw := strings.TrimSpace(r.URL.Query().Get("lines")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "lines must be a positive integer")
			return
		}
		lines = n
	}
	out, err := t.tmuxOutput("capture-pane", "-p", "-t", id, "-S", fmt.Sprintf("-%d", lines))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, captureResponse{Output: out})
}

type keysRequest struct {
	Keys []string `json:"keys"`
	// Literal sends each entry as the text it is, where otherwise a name such as Enter or C-c
	// is read as the key it names.
	Literal bool `json:"literal"`
}

// handleKeys is POST /terminals/{id}/keys: sends keys to the pane with tmux send-keys, as
// key names or, when literal, as typed text.
func (t *terminalHost) handleKeys(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !t.requireSession(w, r, id) {
		return
	}
	var req keysRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	if len(req.Keys) == 0 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "keys is required")
		return
	}
	args := []string{"send-keys", "-t", id}
	if req.Literal {
		args = append(args, "-l")
	}
	args = append(args, "--")
	args = append(args, req.Keys...)
	if err := t.tmuxCmd(args...).Run(); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "send-keys: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

// handleDelete is DELETE /terminals/{id}: kills the session. With ?tree=true every process the
// shell started, background jobs included, is killed with it. A session that is already gone
// is a success.
func (t *terminalHost) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !sessionNameRe.MatchString(id) {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
		return
	}
	if err := t.kill(id, r.URL.Query().Get("tree") == "true"); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// kill ends session id; a session that is already gone is not an error. With tree, every
// process under the shell is killed first.
func (t *terminalHost) kill(id string, tree bool) error {
	if !t.hasSession(id) {
		return nil
	}
	if tree {
		tty, err := t.tmuxOutput("display-message", "-p", "-t", id, "#{pane_tty}")
		if err != nil {
			return err
		}
		if err := killTree(strings.TrimPrefix(strings.TrimSpace(tty), "/dev/")); err != nil {
			return err
		}
		if !t.hasSession(id) {
			return nil
		}
	}
	if err := t.tmuxCmd("kill-session", "-t", id).Run(); err != nil {
		return fmt.Errorf("kill-session: %w", err)
	}
	return nil
}

// register mounts the /terminals routes on mux.
func (t *terminalHost) register(mux *http.ServeMux) {
	mux.HandleFunc("POST /terminals", t.handleCreate)
	mux.HandleFunc("GET /terminals", t.handleList)
	mux.HandleFunc("POST /terminals/{id}/exec", t.handleExec)
	mux.HandleFunc("GET /terminals/{id}/capture", t.handleCapture)
	mux.HandleFunc("POST /terminals/{id}/keys", t.handleKeys)
	mux.HandleFunc("DELETE /terminals/{id}", t.handleDelete)
}

// filepathIsAbs reports whether p is an absolute POSIX path, regardless of the build OS.
func filepathIsAbs(p string) bool {
	return len(p) > 0 && p[0] == '/'
}
