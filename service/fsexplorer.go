package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// uploadMaxBytes is the largest file POST /fs/upload accepts.
const uploadMaxBytes = 200 << 20

// trashDir is the dadi-home folder POST /fs/trash moves into, one subfolder per day.
const trashDir = ".trash"

type fsEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
	MediaType string `json:"media_type"`
}

type fsListResponse struct {
	Path    string    `json:"path"`
	Entries []fsEntry `json:"entries"`
}

type fsUploadResponse struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type fsPathRequest struct {
	Path string `json:"path"`
}

type fsPathResponse struct {
	Path string `json:"path"`
}

type fsMoveRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// resolveHomePath is resolveHostPath that also refuses any result outside the dadi home.
func (h *hostRuntime) resolveHomePath(path string, mustExist, forWrite bool) (string, error) {
	resolved, err := h.resolveHostPath(strings.TrimSpace(path), mustExist, forWrite)
	if err != nil {
		return "", err
	}
	if !pathUnderPrefix(resolved, h.homeDir) {
		return "", errForbidden
	}
	return resolved, nil
}

// resolveHomeEntry resolves the directory holding path inside the dadi home but not path
// itself, so a symlink is addressed as the link and not its target. The parent must exist.
func (h *hostRuntime) resolveHomeEntry(path string, forWrite bool) (string, error) {
	if !filepathIsAbs(strings.TrimSpace(path)) {
		return "", fmt.Errorf("%w: path must be absolute", errInvalidPath)
	}
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == h.homeDir {
		return clean, nil
	}
	parent, err := h.resolveHomePath(filepath.Dir(clean), true, forWrite)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(clean)), nil
}

// newFSEntry describes info (from Lstat) found at path. A symlink is its own kind and is
// never followed.
func newFSEntry(path string, info fs.FileInfo) fsEntry {
	e := fsEntry{
		Name:     info.Name(),
		Path:     path,
		Kind:     "file",
		Size:     info.Size(),
		Modified: info.ModTime().UTC().Format(time.RFC3339),
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		e.Kind = "symlink"
	case info.IsDir():
		e.Kind = "folder"
	default:
		e.MediaType = mime.TypeByExtension(filepath.Ext(info.Name()))
	}
	return e
}

// suffixedName returns name with " (n)" inserted before its extension.
func suffixedName(name string, n int) string {
	ext := filepath.Ext(name)
	if ext == name {
		ext = ""
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), n, ext)
}

// handleList is GET /fs/list?path=&hidden=: the entries of a folder in the dadi home,
// folders first and then by name, without dotfiles unless hidden=1. A missing path is
// not_found and a path that is not a folder is invalid_request.
func (f *fsHost) handleList(w http.ResponseWriter, r *http.Request) {
	dir, err := f.host.resolveHomePath(r.URL.Query().Get("path"), true, false)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	info, err := os.Stat(dir)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	if !info.IsDir() {
		writePathError(w, r, fmt.Errorf("%w: not a directory", errInvalidPath))
		return
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	showHidden := r.URL.Query().Get("hidden") == "1"
	entries := make([]fsEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if !showHidden && strings.HasPrefix(de.Name(), ".") {
			continue
		}
		entryInfo, err := de.Info()
		if err != nil {
			writePathError(w, r, err)
			return
		}
		entries = append(entries, newFSEntry(filepath.Join(dir, de.Name()), entryInfo))
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if (a.Kind == "folder") != (b.Kind == "folder") {
			return a.Kind == "folder"
		}
		if la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name); la != lb {
			return la < lb
		}
		return a.Name < b.Name
	})
	writeJSON(w, http.StatusOK, fsListResponse{Path: dir, Entries: entries})
}

// handleStat is GET /fs/stat?path=: one entry in the dadi home, or not_found. A symlink is
// reported as itself.
func (f *fsHost) handleStat(w http.ResponseWriter, r *http.Request) {
	path, err := f.host.resolveHomeEntry(r.URL.Query().Get("path"), false)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	info, err := os.Lstat(path)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newFSEntry(path, info))
}

// handleRaw is GET /fs/raw?path=: a file in the dadi home streamed inline with its
// Content-Type (by extension, else sniffed), Content-Length and Range support, whatever its size.
func (f *fsHost) handleRaw(w http.ResponseWriter, r *http.Request) {
	path, err := f.host.resolveHomePath(r.URL.Query().Get("path"), true, false)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writePathError(w, r, err)
		return
	}
	if info.IsDir() {
		writePathError(w, r, fmt.Errorf("%w: not a file", errInvalidPath))
		return
	}
	name := filepath.Base(filepath.Clean(strings.TrimSpace(r.URL.Query().Get("path"))))
	encoded := strings.NewReplacer("'", "%27", "(", "%28", ")", "%29", "*", "%2A").Replace(url.PathEscape(name))
	w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+encoded)
	http.ServeContent(w, r, name, info.ModTime(), file)
}

// handleUpload is POST /fs/upload: multipart with a path field (the target folder, sent
// before the file) and one file part, streamed to disk without buffering. Parents are created
// and an existing name is never overwritten: the file lands as "name (2).ext", "name (3).ext".
// A file over the host's upload limit is refused with payload_too_large and removed.
func (f *fsHost) handleUpload(w http.ResponseWriter, r *http.Request) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "multipart body required")
		return
	}
	var dir string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "file part is required")
			return
		}
		if err != nil {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid multipart body")
			return
		}
		switch part.FormName() {
		case "path":
			raw, err := io.ReadAll(io.LimitReader(part, 4096))
			if err != nil {
				writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid multipart body")
				return
			}
			dir, err = f.host.resolveHomePath(string(raw), false, true)
			if err != nil {
				writePathError(w, r, err)
				return
			}
			continue
		case "file":
		default:
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "unexpected field "+part.FormName())
			return
		}
		if dir == "" {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "path field must come before the file part")
			return
		}
		name := filepath.Base(part.FileName())
		if part.FileName() == "" || name == "." || name == ".." || name == string(filepath.Separator) {
			writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "file name is required")
			return
		}
		if info, err := os.Stat(dir); err == nil && !info.IsDir() {
			writePathError(w, r, fmt.Errorf("%w: target is not a directory", errInvalidPath))
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writePathError(w, r, err)
			return
		}
		var (
			out   *os.File
			final string
		)
		for n := 1; ; n++ {
			candidate := name
			if n > 1 {
				candidate = suffixedName(name, n)
			}
			final = filepath.Join(dir, candidate)
			out, err = os.OpenFile(final, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err == nil {
				break
			}
			if !errors.Is(err, fs.ErrExist) {
				writePathError(w, r, err)
				return
			}
		}
		written, copyErr := io.Copy(out, io.LimitReader(part, f.uploadMax+1))
		closeErr := out.Close()
		if copyErr == nil && closeErr == nil && written <= f.uploadMax {
			writeJSON(w, http.StatusOK, fsUploadResponse{Path: final, Name: filepath.Base(final), Size: written})
			return
		}
		if err := os.Remove(final); err != nil {
			slog.Warn("remove failed upload", "code", CodeInternal, "path", final, "err", err)
		}
		if copyErr == nil && closeErr == nil {
			writeError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
				fmt.Sprintf("file is over the %d byte upload limit", f.uploadMax))
			return
		}
		writeError(w, r, http.StatusInternalServerError, CodeInternal, errors.Join(copyErr, closeErr).Error())
		return
	}
}

// handleMkdir is POST /fs/mkdir: creates a folder and any missing parents in the dadi home.
// An existing folder is fine; a path that exists as a file is a conflict.
func (f *fsHost) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var req fsPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	path, err := f.host.resolveHomePath(req.Path, false, true)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		writeError(w, r, http.StatusConflict, CodeConflict, "a file exists at that path")
		return
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		writePathError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fsPathResponse{Path: path})
}

// handleMove is POST /fs/move: renames or moves from to to, both in the dadi home. It never
// overwrites (conflict when to exists), never moves the home, and never moves a folder into
// itself. The parent of to must exist.
func (f *fsHost) handleMove(w http.ResponseWriter, r *http.Request) {
	var req fsMoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	from, err := f.host.resolveHomeEntry(req.From, true)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	to, err := f.host.resolveHomeEntry(req.To, true)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	if from == f.host.homeDir || to == f.host.homeDir {
		writePathError(w, r, errForbidden)
		return
	}
	info, err := os.Lstat(from)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	if info.IsDir() && pathUnderPrefix(to, from) {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "cannot move a folder into itself")
		return
	}
	if _, err := os.Lstat(to); err == nil {
		writeError(w, r, http.StatusConflict, CodeConflict, "destination exists")
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		writePathError(w, r, err)
		return
	}
	if err := os.Rename(from, to); err != nil {
		writePathError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fsPathResponse{Path: to})
}

// handleTrash is POST /fs/trash: moves a file or folder in the dadi home to
// ~/.trash/<yyyy-mm-dd>/<name>, adding " (2)" style suffixes on a clash. Nothing is deleted.
func (f *fsHost) handleTrash(w http.ResponseWriter, r *http.Request) {
	var req fsPathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "invalid json body")
		return
	}
	path, err := f.host.resolveHomeEntry(req.Path, true)
	if err != nil {
		writePathError(w, r, err)
		return
	}
	if path == f.host.homeDir {
		writePathError(w, r, errForbidden)
		return
	}
	if _, err := os.Lstat(path); err != nil {
		writePathError(w, r, err)
		return
	}
	trashRoot := filepath.Join(f.host.homeDir, trashDir)
	if pathUnderPrefix(path, trashRoot) {
		writeError(w, r, http.StatusBadRequest, CodeInvalidRequest, "already in the trash")
		return
	}
	day := filepath.Join(trashRoot, time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(day, 0o755); err != nil {
		writePathError(w, r, err)
		return
	}
	name := filepath.Base(path)
	dest := filepath.Join(day, name)
	for n := 2; ; n++ {
		_, err := os.Lstat(dest)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			writePathError(w, r, err)
			return
		}
		dest = filepath.Join(day, suffixedName(name, n))
	}
	if err := os.Rename(path, dest); err != nil {
		writePathError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fsPathResponse{Path: dest})
}
