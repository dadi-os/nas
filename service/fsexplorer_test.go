package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func doGet(mux *http.ServeMux, target string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func errType(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return e.Error.Type
}

func doUpload(mux *http.ServeMux, fields [][2]string, fileName string, content []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, kv := range fields {
		_ = mw.WriteField(kv[0], kv[1])
	}
	if fileName != "" {
		fw, _ := mw.CreateFormFile("file", fileName)
		_, _ = fw.Write(content)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/fs/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestFSList(t *testing.T) {
	_, mux, home := testFSEnv(t)
	root := filepath.Join(home, "list")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"zeta", "Alpha"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"b.txt", "a.pdf", ".hidden"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		query  string
		status int
		names  []string
		code   string
	}{
		{"folders first then name, dotfiles hidden", url.Values{"path": {root}}.Encode(), 200, []string{"Alpha", "zeta", "a.pdf", "b.txt", "link"}, ""},
		{"hidden=1 shows dotfiles", url.Values{"path": {root}, "hidden": {"1"}}.Encode(), 200, []string{"Alpha", "zeta", ".hidden", "a.pdf", "b.txt", "link"}, ""},
		{"missing path", url.Values{"path": {filepath.Join(root, "nope")}}.Encode(), 404, nil, "not_found"},
		{"file is not a directory", url.Values{"path": {filepath.Join(root, "a.pdf")}}.Encode(), 400, nil, "invalid_request"},
		{"relative path", url.Values{"path": {"a/b"}}.Encode(), 400, nil, "invalid_request"},
		{"dotdot escape", url.Values{"path": {home + "/../.."}}.Encode(), 403, nil, "forbidden"},
		{"outside home", url.Values{"path": {"/usr"}}.Encode(), 403, nil, "forbidden"},
		{"symlink out of home is not followed", url.Values{"path": {filepath.Join(root, "link")}}.Encode(), 403, nil, "forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doGet(mux, "/fs/list?"+tc.query, nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d %s", rec.Code, rec.Body)
			}
			if tc.code != "" {
				if got := errType(t, rec.Body.Bytes()); got != tc.code {
					t.Fatalf("code %q", got)
				}
				return
			}
			var resp fsListResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range resp.Entries {
				names = append(names, e.Name)
			}
			if strings.Join(names, ",") != strings.Join(tc.names, ",") {
				t.Fatalf("names %v", names)
			}
			for _, e := range resp.Entries {
				if e.Name == "link" && e.Kind != "symlink" {
					t.Fatalf("link kind %q", e.Kind)
				}
				if e.Name == "a.pdf" && (e.Kind != "file" || e.MediaType != "application/pdf" || e.Size != 1) {
					t.Fatalf("a.pdf %+v", e)
				}
				if e.Name == "Alpha" && e.Kind != "folder" {
					t.Fatalf("Alpha kind %q", e.Kind)
				}
				if _, err := time.Parse(time.RFC3339, e.Modified); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestFSStat(t *testing.T) {
	_, mux, root := testFSEnv(t)
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		path   string
		status int
		kind   string
		code   string
	}{
		{"file", file, 200, "file", ""},
		{"home", root, 200, "folder", ""},
		{"symlink reported as itself", filepath.Join(root, "out"), 200, "symlink", ""},
		{"removed file is not_found", filepath.Join(root, "gone.txt"), 404, "", "not_found"},
		{"missing parent is not_found", filepath.Join(root, "x", "gone.txt"), 404, "", "not_found"},
		{"outside home", "/etc/hosts", 403, "", "forbidden"},
		{"dotdot", root + "/../x", 403, "", "forbidden"},
		{"through symlink out of home", filepath.Join(root, "out", "hosts"), 403, "", "forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doGet(mux, "/fs/stat?path="+url.QueryEscape(tc.path), nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d %s", rec.Code, rec.Body)
			}
			if tc.code != "" {
				if got := errType(t, rec.Body.Bytes()); got != tc.code {
					t.Fatalf("code %q", got)
				}
				return
			}
			var e fsEntry
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			if e.Kind != tc.kind {
				t.Fatalf("kind %q", e.Kind)
			}
		})
	}
}

func TestFSRaw(t *testing.T) {
	_, mux, root := testFSEnv(t)
	if err := os.WriteFile(filepath.Join(root, "n é.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "noext"), []byte("%PDF-1.4 body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	get := func(name string, hdr map[string]string) *httptest.ResponseRecorder {
		return doGet(mux, "/fs/raw?path="+url.QueryEscape(filepath.Join(root, name)), hdr)
	}

	rec := get("n é.txt", nil)
	if rec.Code != 200 || rec.Body.String() != "0123456789" {
		t.Fatalf("full %d %q", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Header().Get("Content-Length") != "10" {
		t.Fatalf("content-length %q", rec.Header().Get("Content-Length"))
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "inline; filename*=UTF-8''n%20%C3%A9.txt" {
		t.Fatalf("disposition %q", cd)
	}

	rec = get("n é.txt", map[string]string{"Range": "bytes=2-4"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" || rec.Header().Get("Content-Range") != "bytes 2-4/10" {
		t.Fatalf("range %d %q %q", rec.Code, rec.Body, rec.Header().Get("Content-Range"))
	}

	rec = get("noext", nil)
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("sniffed content-type %q", ct)
	}

	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"missing", 404, "not_found"},
		{"", 400, "invalid_request"},
		{"out", 403, "forbidden"},
	} {
		rec := get(tc.name, nil)
		if rec.Code != tc.status || errType(t, rec.Body.Bytes()) != tc.code {
			t.Fatalf("%q: %d %s", tc.name, rec.Code, rec.Body)
		}
	}
}

func TestFSUpload(t *testing.T) {
	fh, mux, root := testFSEnv(t)
	dir := filepath.Join(root, "a", "b")

	rec := doUpload(mux, [][2]string{{"path", dir}}, "doc.tar.gz", []byte("one"))
	var resp fsUploadResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp.Path != filepath.Join(dir, "doc.tar.gz") || resp.Size != 3 {
		t.Fatalf("first %d %s", rec.Code, rec.Body)
	}
	for i, want := range []string{"doc.tar (2).gz", "doc.tar (3).gz"} {
		rec = doUpload(mux, [][2]string{{"path", dir}}, "doc.tar.gz", []byte("again"))
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp.Name != want {
			t.Fatalf("clash %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "doc.tar.gz")); string(got) != "one" {
		t.Fatalf("original overwritten: %q", got)
	}

	fh.uploadMax = 4
	rec = doUpload(mux, [][2]string{{"path", dir}}, "big.bin", []byte("12345"))
	if rec.Code != http.StatusRequestEntityTooLarge || errType(t, rec.Body.Bytes()) != "payload_too_large" {
		t.Fatalf("limit %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "big.bin")); !os.IsNotExist(err) {
		t.Fatalf("oversize file left behind: %v", err)
	}
	rec = doUpload(mux, [][2]string{{"path", dir}}, "ok.bin", []byte("1234"))
	if rec.Code != 200 {
		t.Fatalf("at limit %d %s", rec.Code, rec.Body)
	}

	if err := os.Symlink("/tmp", filepath.Join(root, "tmplink")); err != nil {
		t.Fatal(err)
	}
	fh.uploadMax = uploadMaxBytes
	cases := []struct {
		name   string
		fields [][2]string
		file   string
		status int
		code   string
	}{
		{"no file part", [][2]string{{"path", dir}}, "", 400, "invalid_request"},
		{"file before path", nil, "x.txt", 400, "invalid_request"},
		{"relative path", [][2]string{{"path", "rel"}}, "x.txt", 400, "invalid_request"},
		{"dotdot escape", [][2]string{{"path", root + "/../x"}}, "x.txt", 403, "forbidden"},
		{"symlink out of home", [][2]string{{"path", filepath.Join(root, "tmplink", "d")}}, "x.txt", 403, "forbidden"},
		{"os prefix", [][2]string{{"path", "/usr/local"}}, "x.txt", 403, "forbidden"},
		{"target is a file", [][2]string{{"path", filepath.Join(dir, "ok.bin")}}, "x.txt", 400, "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doUpload(mux, tc.fields, tc.file, []byte("x"))
			if rec.Code != tc.status || errType(t, rec.Body.Bytes()) != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestFSMkdir(t *testing.T) {
	_, mux, root := testFSEnv(t)
	if err := os.WriteFile(filepath.Join(root, "f"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		path   string
		status int
		code   string
	}{
		{"nested", filepath.Join(root, "a", "b"), 200, ""},
		{"existing folder", root, 200, ""},
		{"file in the way", filepath.Join(root, "f"), 409, "conflict"},
		{"dotdot escape", root + "/../x", 403, "forbidden"},
		{"os prefix", "/usr/x", 403, "forbidden"},
		{"relative", "x", 400, "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, mux, http.MethodPost, "/fs/mkdir", map[string]any{"path": tc.path})
			if code != tc.status || (tc.code != "" && errType(t, body) != tc.code) {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
	if fi, err := os.Stat(filepath.Join(root, "a", "b")); err != nil || !fi.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
}

func TestFSMove(t *testing.T) {
	_, mux, root := testFSEnv(t)
	for _, d := range []string{"dir/sub", "dest"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	p := func(s string) string { return filepath.Join(root, s) }
	cases := []struct {
		name     string
		from, to string
		status   int
		code     string
	}{
		{"rename", p("a.txt"), p("c.txt"), 200, ""},
		{"move into folder", p("c.txt"), p("dest/c.txt"), 200, ""},
		{"destination exists", p("b.txt"), p("dest/c.txt"), 409, "conflict"},
		{"folder into itself", p("dir"), p("dir/sub/dir"), 400, "invalid_request"},
		{"missing source", p("nope"), p("x"), 404, "not_found"},
		{"missing destination parent", p("b.txt"), p("nope/b.txt"), 404, "not_found"},
		{"destination outside home", p("b.txt"), "/tmp/b.txt", 403, "forbidden"},
		{"source outside home", "/etc/hosts", p("h"), 403, "forbidden"},
		{"dotdot escape", p("b.txt"), root + "/../b.txt", 403, "forbidden"},
		{"destination through symlink out", p("b.txt"), p("out/b.txt"), 403, "forbidden"},
		{"os prefix", p("b.txt"), "/usr/b.txt", 403, "forbidden"},
		{"home itself", root, p("dest/home"), 403, "forbidden"},
		{"symlink moves as a link", p("out"), p("dest/out"), 200, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, mux, http.MethodPost, "/fs/move", map[string]any{"from": tc.from, "to": tc.to})
			if code != tc.status || (tc.code != "" && errType(t, body) != tc.code) {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
	if got, _ := os.ReadFile(p("dest/c.txt")); string(got) != "a.txt" {
		t.Fatalf("destination overwritten: %q", got)
	}
	if _, err := os.Lstat(p("b.txt")); err != nil {
		t.Fatalf("source lost on conflict: %v", err)
	}
	if fi, err := os.Lstat(p("dest/out")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink not moved as a link: %v", err)
	}
}

func TestFSTrash(t *testing.T) {
	_, mux, root := testFSEnv(t)
	day := filepath.Join(root, ".trash", time.Now().Format("2006-01-02"))
	trash := func(path string) (int, []byte) {
		return doJSON(t, mux, http.MethodPost, "/fs/trash", map[string]any{"path": path})
	}
	write := func(rel, content string) string {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	code, body := trash(write("x/report.pdf", "one"))
	var resp fsPathResponse
	if code != 200 || json.Unmarshal(body, &resp) != nil || resp.Path != filepath.Join(day, "report.pdf") {
		t.Fatalf("first %d %s", code, body)
	}
	code, body = trash(write("y/report.pdf", "two"))
	if code != 200 || json.Unmarshal(body, &resp) != nil || resp.Path != filepath.Join(day, "report (2).pdf") {
		t.Fatalf("collision %d %s", code, body)
	}
	if got, _ := os.ReadFile(filepath.Join(day, "report.pdf")); string(got) != "one" {
		t.Fatalf("trash overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "x", "report.pdf")); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}

	if err := os.Symlink("/etc", filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		path   string
		status int
		code   string
	}{
		{"missing", filepath.Join(root, "nope"), 404, "not_found"},
		{"outside home", "/etc/hosts", 403, "forbidden"},
		{"dotdot escape", root + "/../x", 403, "forbidden"},
		{"through symlink out", filepath.Join(root, "out", "hosts"), 403, "forbidden"},
		{"home itself", root, 403, "forbidden"},
		{"already trashed", filepath.Join(day, "report.pdf"), 400, "invalid_request"},
		{"relative", "x", 400, "invalid_request"},
		{"symlink trashed as a link", filepath.Join(root, "out"), 200, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := trash(tc.path)
			if code != tc.status || (tc.code != "" && errType(t, body) != tc.code) {
				t.Fatalf("%d %s", code, body)
			}
		})
	}
}
