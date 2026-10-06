package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

var (
	errForbidden   = errors.New("forbidden")
	errNotFound    = errors.New("not found")
	errInvalidPath = errors.New("invalid path")
	errBinaryFile  = errors.New("binary file")
)

type fsHost struct {
	host *hostRuntime
}

// newFSHost returns the filesystem API backed by host.
func newFSHost(host *hostRuntime) *fsHost {
	return &fsHost{host: host}
}

// writePathError maps a path resolution or filesystem error to its HTTP status and code.
func writePathError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errForbidden):
		writeError(w, r, http.StatusForbidden, CodeForbidden, "forbidden")
	case errors.Is(err, fs.ErrPermission):
		writeError(w, r, http.StatusForbidden, CodeForbidden, err.Error())
	case errors.Is(err, errNotFound):
		writeError(w, r, http.StatusNotFound, CodeNotFound, "not_found")
	case errors.Is(err, errBinaryFile):
		writeError(w, r, http.StatusUnsupportedMediaType, CodeBinaryFile, "binary file")
	case errors.Is(err, errInvalidPath):
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
	default:
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
	}
}

// resolveHostPath requires an absolute path and resolves symlinks when mustExist.
// When forWrite is true, rejects OS / dadiOS runtime prefixes.
func (h *hostRuntime) resolveHostPath(path string, mustExist, forWrite bool) (string, error) {
	if !filepathIsAbs(path) {
		return "", fmt.Errorf("%w: path must be absolute", errInvalidPath)
	}
	clean := filepath.Clean(path)

	if mustExist {
		resolved, err := filepath.EvalSymlinks(clean)
		if err != nil {
			if os.IsNotExist(err) {
				return "", errNotFound
			}
			return "", err
		}
		clean = resolved
	} else {
		resolved, err := resolveExistingPrefix(clean)
		if err != nil {
			return "", err
		}
		clean = resolved
	}

	if forWrite && h.isWriteProtected(clean) {
		return "", errForbidden
	}
	return clean, nil
}

// resolveExistingPrefix resolves symlinks in the longest existing prefix of path and appends
// the missing components, so a path being created is checked where it will really land.
func resolveExistingPrefix(path string) (string, error) {
	clean := filepath.Clean(path)
	if clean == "/" {
		return "/", nil
	}
	var missing []string
	cur := clean
	for {
		fi, err := os.Lstat(cur)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(cur)
				if err != nil {
					return "", err
				}
				cur = resolved
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		missing = append([]string{filepath.Base(cur)}, missing...)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	resolved := cur
	if resolved != "/" {
		if r, err := filepath.EvalSymlinks(resolved); err == nil {
			resolved = r
		}
	}
	for _, part := range missing {
		resolved = filepath.Join(resolved, part)
	}
	return filepath.Clean(resolved), nil
}

// isBinaryHead reports whether the first 8 KiB of path contain a NUL byte.
func isBinaryHead(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	return bytes.IndexByte(buf[:n], 0) >= 0, nil
}

type readRequest struct {
	Path     string `json:"path"`
	Offset   *int   `json:"offset"`
	Limit    *int   `json:"limit"`
	MaxBytes *int   `json:"max_bytes"`
}

type readResponse struct {
	Content    string `json:"content"`
	TotalLines int    `json:"total_lines"`
	Truncated  bool   `json:"truncated"`
}

// handleRead is POST /fs/read: a text file as numbered lines from offset, bounded by limit
// lines and max_bytes. Binary files are refused with CodeBinaryFile.
func (f *fsHost) handleRead(w http.ResponseWriter, r *http.Request) {
	var req readRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	path, err := f.host.resolveHostPath(strings.TrimSpace(req.Path), true, false)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	bin, err := isBinaryHead(path)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	if bin {
		writePathError(w, r, errBinaryFile)
		return
	}
	offset := 1
	if req.Offset != nil {
		offset = *req.Offset
	}
	if offset < 1 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "offset must be >= 1")
		return
	}
	limit := 500
	if req.Limit != nil {
		limit = *req.Limit
	}
	if limit < 1 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "limit must be positive")
		return
	}
	maxBytes := 65536
	if req.MaxBytes != nil {
		maxBytes = *req.MaxBytes
	}
	if maxBytes < 1 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "max_bytes must be positive")
		return
	}

	file, err := os.Open(path)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		return
	}
	allLines := strings.Split(string(data), "\n")
	if len(allLines) > 0 && allLines[len(allLines)-1] == "" {
		allLines = allLines[:len(allLines)-1]
	}
	total := len(allLines)

	var (
		b         strings.Builder
		written   int
		truncated bool
		byteCount int
	)
	for i := offset - 1; i < total; i++ {
		if written >= limit {
			truncated = true
			break
		}
		entry := fmt.Sprintf("%d\t%s\n", i+1, allLines[i])
		if byteCount+len(entry) > maxBytes {
			truncated = true
			break
		}
		b.WriteString(entry)
		byteCount += len(entry)
		written++
	}
	writeJSON(w, http.StatusOK, readResponse{
		Content:    b.String(),
		TotalLines: total,
		Truncated:  truncated,
	})
}

type writeRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type writeResponse struct {
	Bytes int `json:"bytes"`
}

// handleWrite is POST /fs/write: creates or replaces a file, creating missing parents.
// Write-protected paths are forbidden.
func (f *fsHost) handleWrite(w http.ResponseWriter, r *http.Request) {
	var req writeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	path, err := f.host.resolveHostPath(strings.TrimSpace(req.Path), false, true)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writePathError(w, r, err)
		return
	}
	if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
		writePathError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, writeResponse{Bytes: len(req.Content)})
}

type editRequest struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

// handleEdit is POST /fs/edit: replaces old_string with new_string in a file. old_string
// must occur exactly once, otherwise it is a conflict.
func (f *fsHost) handleEdit(w http.ResponseWriter, r *http.Request) {
	var req editRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	if req.OldString == "" {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "old_string is required")
		return
	}
	path, err := f.host.resolveHostPath(strings.TrimSpace(req.Path), true, true)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	count := strings.Count(string(body), req.OldString)
	if count != 1 {
		writeError(w, r, http.StatusConflict, CodeConflict, fmt.Sprintf("expected exactly 1 match, got %d", count))
		return
	}
	updated := strings.Replace(string(body), req.OldString, req.NewString, 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		writePathError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"replaced": true})
}

type globRequest struct {
	Pattern string `json:"pattern"`
	Cwd     string `json:"cwd"`
	Limit   *int   `json:"limit"`
}

type globResponse struct {
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
}

type pathMtime struct {
	path  string
	mtime int64
}

// handleGlob is POST /fs/glob: doublestar matches under cwd (the dadi home when omitted),
// newest first, up to limit.
func (f *fsHost) handleGlob(w http.ResponseWriter, r *http.Request) {
	var req globRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Pattern) == "" {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "pattern is required")
		return
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		cwd = f.host.defaultCwd()
	}
	cwdResolved, err := f.host.resolveHostPath(cwd, true, false)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	limit := 500
	if req.Limit != nil {
		limit = *req.Limit
	}
	if limit < 1 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "limit must be positive")
		return
	}

	matches, err := doublestar.Glob(os.DirFS(cwdResolved), req.Pattern)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}
	items := make([]pathMtime, 0, len(matches))
	for _, rel := range matches {
		abs := filepath.Join(cwdResolved, rel)
		resolved, err := f.host.resolveHostPath(abs, true, false)
		if err != nil {
			continue
		}
		fi, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		items = append(items, pathMtime{path: resolved, mtime: fi.ModTime().UnixNano()})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].mtime == items[j].mtime {
			return items[i].path < items[j].path
		}
		return items[i].mtime > items[j].mtime
	})
	truncated := false
	if len(items) > limit {
		items = items[:limit]
		truncated = true
	}
	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = it.path
	}
	writeJSON(w, http.StatusOK, globResponse{Paths: paths, Truncated: truncated})
}

type grepRequest struct {
	Pattern  string `json:"pattern"`
	Cwd      string `json:"cwd"`
	Glob     string `json:"glob"`
	Limit    *int   `json:"limit"`
	MaxBytes *int   `json:"max_bytes"`
}

type grepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type grepResponse struct {
	Matches   []grepMatch `json:"matches"`
	Truncated bool        `json:"truncated"`
}

// handleGrep is POST /fs/grep: ripgrep matches under cwd (the dadi home when omitted),
// bounded by limit and max_bytes.
func (f *fsHost) handleGrep(w http.ResponseWriter, r *http.Request) {
	var req grepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Pattern) == "" {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "pattern is required")
		return
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		cwd = f.host.defaultCwd()
	}
	cwdResolved, err := f.host.resolveHostPath(cwd, true, false)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	limit := 200
	if req.Limit != nil {
		limit = *req.Limit
	}
	if limit < 1 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "limit must be positive")
		return
	}
	maxBytes := 65536
	if req.MaxBytes != nil {
		maxBytes = *req.MaxBytes
	}
	if maxBytes < 1 {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "max_bytes must be positive")
		return
	}

	args := []string{"--json", "--color", "never", "-e", req.Pattern}
	if g := strings.TrimSpace(req.Glob); g != "" {
		args = append(args, "--glob", g)
	}
	args = append(args, cwdResolved)
	cmd := exec.Command("rg", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			writeJSON(w, http.StatusOK, grepResponse{Matches: []grepMatch{}, Truncated: false})
			return
		}
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "rg: "+err.Error())
		return
	}

	matches := make([]grepMatch, 0)
	truncated := false
	byteCount := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Bytes()
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				LineNumber int `json:"line_number"`
				Lines      struct {
					Text string `json:"text"`
				} `json:"lines"`
			} `json:"data"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "match" {
			continue
		}
		text := strings.TrimRight(ev.Data.Lines.Text, "\n")
		abs := ev.Data.Path.Text
		if !filepathIsAbs(abs) {
			abs = filepath.Join(cwdResolved, abs)
		}
		resolved, err := f.host.resolveHostPath(abs, true, false)
		if err != nil {
			continue
		}
		m := grepMatch{Path: resolved, Line: ev.Data.LineNumber, Text: text}
		entryLen := len(m.Path) + len(m.Text) + 16
		if len(matches) >= limit || byteCount+entryLen > maxBytes {
			truncated = true
			break
		}
		matches = append(matches, m)
		byteCount += entryLen
	}
	writeJSON(w, http.StatusOK, grepResponse{Matches: matches, Truncated: truncated})
}

// register mounts the /fs routes on mux, each run with dadi's filesystem identity.
func (f *fsHost) register(mux *http.ServeMux) {
	mux.HandleFunc("POST /fs/read", f.asDadi(f.handleRead))
	mux.HandleFunc("POST /fs/write", f.asDadi(f.handleWrite))
	mux.HandleFunc("POST /fs/edit", f.asDadi(f.handleEdit))
	mux.HandleFunc("POST /fs/glob", f.asDadi(f.handleGlob))
	mux.HandleFunc("POST /fs/grep", f.asDadi(f.handleGrep))
}

// asDadi serves h with dadi's filesystem identity on the appliance, so agents read and
// write with dadi's permissions and everything they create belongs to dadi, as it does
// from their terminals.
func (f *fsHost) asDadi(h http.HandlerFunc) http.HandlerFunc {
	if !f.host.switchUser {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if err := asFSUser(f.host.dadiUID, f.host.dadiGID, func() { h(w, r) }); err != nil {
			writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
		}
	}
}
