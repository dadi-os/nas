package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	host *hostRuntime
	mu   sync.Mutex
	busy map[string]struct{} // in-flight exec per session id
}

func newTerminalHost(host *hostRuntime) *terminalHost {
	return &terminalHost{
		host: host,
		busy: make(map[string]struct{}),
	}
}

func (t *terminalHost) tmuxCmd(args ...string) *exec.Cmd {
	full := append([]string{"-S", t.host.tmuxSocket}, args...)
	if t.host.switchUser {
		return exec.Command("runuser", append([]string{"-u", dadiUsername, "--", "tmux"}, full...)...)
	}
	return exec.Command("tmux", full...)
}

func (t *terminalHost) tmuxOutput(args ...string) (string, error) {
	out, err := t.tmuxCmd(args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (t *terminalHost) hasSession(id string) bool {
	err := t.tmuxCmd("has-session", "-t", id).Run()
	return err == nil
}

func (t *terminalHost) requireSession(w http.ResponseWriter, r *http.Request, id string) bool {
	if !sessionNameRe.MatchString(id) || !t.hasSession(id) {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
		return false
	}
	return true
}

func (t *terminalHost) paneCommand(id string) (string, error) {
	out, err := t.tmuxOutput("display-message", "-p", "-t", id, "#{pane_current_command}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (t *terminalHost) isPaneBusy(id string) (bool, error) {
	cmd, err := t.paneCommand(id)
	if err != nil {
		return false, err
	}
	return cmd != "" && cmd != t.host.idleShell, nil
}

func (t *terminalHost) listSessionNames() (map[string]struct{}, error) {
	out, err := t.tmuxCmd("ls", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		// no server / no sessions
		if strings.Contains(string(out), "no server running") ||
			strings.Contains(string(out), "error connecting") ||
			strings.Contains(err.Error(), "exit status 1") {
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
	Cwd string `json:"cwd"`
}

type createTerminalResponse struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd"`
}

func (t *terminalHost) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createTerminalRequest
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil && err != io.EOF {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
			return
		}
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		cwd = t.host.projectsDir
	}
	if !filepathIsAbs(cwd) {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "cwd must be absolute")
		return
	}
	cwd = filepath.Clean(cwd)
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "cwd must be an existing directory")
		return
	}
	id, err := t.nextSessionID()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	if _, err := t.tmuxOutput("new-session", "-d", "-s", id, "-c", cwd); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	if _, err := t.tmuxOutput("set-option", "-t", id, "history-limit", strconv.Itoa(historyLimit)); err != nil {
		_ = t.tmuxCmd("kill-session", "-t", id).Run()
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, createTerminalResponse{ID: id, Cwd: cwd})
}

type terminalInfo struct {
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	CreatedAt string `json:"created_at"`
	Busy      bool   `json:"busy"`
}

func (t *terminalHost) handleList(w http.ResponseWriter, r *http.Request) {
	out, err := t.tmuxCmd(
		"ls", "-F",
		"#{session_name}\t#{pane_current_path}\t#{session_created}\t#{pane_current_command}",
	).CombinedOutput()
	if err != nil {
		msg := string(out)
		if strings.Contains(msg, "no server running") ||
			strings.Contains(msg, "error connecting") ||
			strings.Contains(err.Error(), "exit status 1") {
			writeJSON(w, http.StatusOK, []terminalInfo{})
			return
		}
		writeError(w, r, http.StatusInternalServerError, CodeInternal, strings.TrimSpace(msg))
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	list := make([]terminalInfo, 0)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		id := parts[0]
		if !sessionNameRe.MatchString(id) {
			continue
		}
		createdUnix, _ := strconv.ParseInt(parts[2], 10, 64)
		createdAt := time.Unix(createdUnix, 0).UTC().Format(time.RFC3339)
		paneCmd := parts[3]
		_, execBusy := t.busy[id]
		busy := execBusy || (paneCmd != "" && paneCmd != t.host.idleShell)
		list = append(list, terminalInfo{
			ID:        id,
			Cwd:       parts[1],
			CreatedAt: createdAt,
			Busy:      busy,
		})
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
}

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
	// command; printf '\n__NAS_EXEC__%s %d\n' <nonce> $?
	line := req.Command + "; printf '\\n" + execMarker + "%s %d\\n' " + shellSingleQuote(nonce) + " $?"
	if err := t.tmuxCmd("send-keys", "-t", id, "-l", "--", line).Run(); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "send-keys: "+err.Error())
		return
	}
	if err := t.tmuxCmd("send-keys", "-t", id, "Enter").Run(); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "send-keys enter: "+err.Error())
		return
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	markerPrefix := execMarker + nonce + " "
	var (
		exitCode *int
		output   string
		timedOut bool
	)
	for {
		capOut, err := t.tmuxOutput("capture-pane", "-p", "-t", id, "-S", "-")
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
			return
		}
		if code, body, ok := parseExecCapture(capOut, line, markerPrefix); ok {
			exitCode = &code
			output = body
			break
		}
		if time.Now().After(deadline) {
			timedOut = true
			output = partialExecOutput(capOut, line)
			break
		}
		time.Sleep(execPollInterval)
	}

	truncated, trunc := truncateHeadTail(output, maxBytes)
	writeJSON(w, http.StatusOK, execResponse{
		ExitCode:  exitCode,
		Output:    truncated,
		Truncated: trunc,
		TimedOut:  timedOut,
	})
}

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
		// Fall back: first line that contains the marker setup before the result line.
		for i := 0; i < markerIdx; i++ {
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
	body := strings.Join(lines[start:markerIdx], "\n")
	return code, body, true
}

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
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[cmdIdx+1:], "\n")
}

func splitPaneLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

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

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type captureResponse struct {
	Output string `json:"output"`
}

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
}

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
	args = append(args, req.Keys...)
	if err := t.tmuxCmd(args...).Run(); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "send-keys: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func (t *terminalHost) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !sessionNameRe.MatchString(id) || !t.hasSession(id) {
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
		return
	}
	if err := t.tmuxCmd("kill-session", "-t", id).Run(); err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "kill-session: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (t *terminalHost) register(mux *http.ServeMux) {
	mux.HandleFunc("POST /terminals", t.handleCreate)
	mux.HandleFunc("GET /terminals", t.handleList)
	mux.HandleFunc("POST /terminals/{id}/exec", t.handleExec)
	mux.HandleFunc("GET /terminals/{id}/capture", t.handleCapture)
	mux.HandleFunc("POST /terminals/{id}/keys", t.handleKeys)
	mux.HandleFunc("DELETE /terminals/{id}", t.handleDelete)
}

func filepathIsAbs(p string) bool {
	return len(p) > 0 && p[0] == '/'
}
