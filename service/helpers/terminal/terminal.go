// Package terminal is the terminal helper's kit: its charter (charter.md and Ankur's
// doctrine in doctrine/), its tools, and its shell, a Nas tmux session reached through
// Nas's own terminal and file API like any other client.
package terminal

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dadi-os/nas/service/helpers"
)

//go:embed charter.md doctrine/*.md
var charterFiles embed.FS

const (
	// idleSleep is how long a terminal task may sit idle before its shell is closed.
	idleSleep = 60 * time.Minute
	// outputTailBytes is how much of a command's output a result shows; the rest is in its log.
	outputTailBytes = 16_000
	// defaultTimeout and maxTimeout bound a foreground command, in seconds.
	defaultTimeout = 120
	maxTimeout     = 3600
	// logDir is where full command output is kept, under the dadi home.
	logDir = ".cache/dadi-helpers"
	// touchedMax is how many touched files the state of the work lists.
	touchedMax = 20
)

// Kit is the terminal helper kind. nas is Nas's own API base URL, chaavi Chaavi's.
type Kit struct {
	nas     string
	chaavi  string
	home    string
	charter string
	http    *http.Client
}

// New builds the terminal kit, reading its charter and doctrine. home is the dadi home.
func New(nasURL, chaaviURL, home string) (*Kit, error) {
	charter, err := charterFiles.ReadFile("charter.md")
	if err != nil {
		return nil, err
	}
	parts := []string{string(charter)}
	names, err := fs.Glob(charterFiles, "doctrine/*.md")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, name := range names {
		text, err := charterFiles.ReadFile(name)
		if err != nil {
			return nil, err
		}
		parts = append(parts, string(text))
	}
	return &Kit{nas: nasURL, chaavi: chaaviURL, home: home, charter: strings.Join(parts, "\n\n"), http: &http.Client{Timeout: (maxTimeout + 30) * time.Second}}, nil
}

// Kind is "terminal".
func (k *Kit) Kind() string { return "terminal" }

// Charter is charter.md followed by the doctrine files.
func (k *Kit) Charter() string { return k.charter }

// IdleSleep is 60 minutes.
func (k *Kit) IdleSleep() time.Duration { return idleSleep }

// Room is always true: a shell costs next to nothing.
func (k *Kit) Room() (bool, error) { return true, nil }

// call sends a JSON request to url and decodes a 2xx answer into out (when non-nil). Any
// other answer is an error carrying the service's message.
func (k *Kit) call(method, url string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := k.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var failed struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &failed) == nil && failed.Error.Type != "" {
			return fmt.Errorf("%s: %s", failed.Error.Type, failed.Error.Message)
		}
		return fmt.Errorf("%s %s: %d %s", method, url, resp.StatusCode, bytes.TrimSpace(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Wake opens a shell for t in its working directory (the dadi home when it has none),
// closing the shell it had before sleep or a Nas restart if it still runs.
func (k *Kit) Wake(t *helpers.Task) error {
	if t.Resource != "" {
		if err := k.Sleep(t); err != nil {
			return err
		}
	}
	var created struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	}
	body := map[string]string{}
	if t.Cwd != "" {
		body["cwd"] = t.Cwd
	}
	if err := k.call(http.MethodPost, k.nas+"/terminals", body, &created); err != nil {
		return err
	}
	t.Resource = created.ID
	t.Cwd = created.Cwd
	return nil
}

// Sleep closes t's shell; its files and history stay.
func (k *Kit) Sleep(t *helpers.Task) error {
	return k.call(http.MethodDelete, k.nas+"/terminals/"+t.Resource, nil, nil)
}

// State is the shell's directory, its running background jobs, and the files touched.
func (k *Kit) State(t *helpers.Task) string {
	lines := []string{"Working directory: " + t.Cwd}
	notes := t.Notes()
	var jobs []string
	for key, value := range notes {
		if strings.HasPrefix(key, "job:") {
			jobs = append(jobs, strings.TrimPrefix(key, "job:")+" ("+value+")")
		}
	}
	sort.Strings(jobs)
	if len(jobs) > 0 {
		lines = append(lines, "Background jobs: "+strings.Join(jobs, ", "))
	}
	if touched := notes["touched"]; touched != "" {
		lines = append(lines, "Files touched: "+touched)
	}
	return strings.Join(lines, "\n")
}

// touch records path among t's touched files, newest last.
func touch(t *helpers.Task, path string) {
	var files []string
	for _, f := range strings.Split(t.Notes()["touched"], ", ") {
		if f != "" && f != path {
			files = append(files, f)
		}
	}
	files = append(files, path)
	if len(files) > touchedMax {
		files = files[len(files)-touchedMax:]
	}
	t.Note("touched", strings.Join(files, ", "))
}

// shellQuote quotes s as one POSIX shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type execResult struct {
	ExitCode *int   `json:"exit_code"`
	Output   string `json:"output"`
	TimedOut bool   `json:"timed_out"`
}

// exec runs command in t's shell.
func (k *Kit) exec(t *helpers.Task, command string, timeout int) (execResult, error) {
	var out execResult
	err := k.call(http.MethodPost, k.nas+"/terminals/"+t.Resource+"/exec", map[string]any{
		"command": command, "timeout_seconds": timeout, "max_bytes": outputTailBytes,
	}, &out)
	return out, err
}

// Tools are run, job, read, search, edit, write and secret.
func (k *Kit) Tools() []helpers.ToolSpec {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	obj := func(required []string, props map[string]any) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "required": required, "properties": props}
	}
	return []helpers.ToolSpec{
		{Name: "run", Description: "Run a shell command as dadi, in cwd (default the shell's directory). Each runs in a fresh subshell, so cd and export do not carry over. Returns the exit code and the last 16000 bytes of output, with the full output's log path. A command still running at timeout keeps running in the background of the shell; background true starts a long command (a dev server, a long build) and returns a job handle at once.", InputSchema: obj([]string{"command"}, map[string]any{
			"command": str("The shell command"), "cwd": str("Absolute directory to run in"),
			"timeout":    map[string]any{"type": "integer", "description": "Seconds to wait (default 120, max 3600)"},
			"background": map[string]any{"type": "boolean", "description": "Start it in the background and return a handle"},
		})},
		{Name: "job", Description: "Follow a background job: read returns whether it is still running and the end of its output; stop ends it.", InputSchema: obj([]string{"handle"}, map[string]any{
			"handle": str("The handle run returned"), "stop": map[string]any{"type": "boolean", "description": "Stop the job"},
		})},
		{Name: "read", Description: "Read a text file at an absolute path as numbered lines: range \"120-180\" for those lines (default the first 300), or pattern (a regular expression) for just the matching lines with their numbers.", InputSchema: obj([]string{"path"}, map[string]any{
			"path": str("Absolute file path"), "range": str("Lines as start-end, such as 120-180"), "pattern": str("Regular expression to match lines"),
		})},
		{Name: "search", Description: "Search file contents with a regular expression (ripgrep) under path (default the shell's directory), optionally only files matching glob. Returns matching lines with numbers, grouped by file, clipped.", InputSchema: obj([]string{"pattern"}, map[string]any{
			"pattern": str("Regular expression"), "path": str("Absolute directory to search"), "glob": str("File name glob, such as **/*.go"),
		})},
		{Name: "edit", Description: "Replace exactly one occurrence of old with new in a file. Fails with the match count when old matches no place or several, changing nothing.", InputSchema: obj([]string{"path", "old", "new"}, map[string]any{
			"path": str("Absolute file path"), "old": str("Exact text to replace"), "new": str("Replacement text"),
		})},
		{Name: "write", Description: "Write a whole file (for new files), creating parent directories. Writes into the OS and Dadi's system folders are refused.", InputSchema: obj([]string{"path", "content"}, map[string]any{
			"path": str("Absolute file path"), "content": str("The file's full content"),
		})},
		{Name: "secret", Description: "Run a command with a secret from Chaavi (the Secure Note named name) in the environment variable env, without the secret ever being shown; the secret is scrubbed from the output.", InputSchema: obj([]string{"name", "env", "command"}, map[string]any{
			"name": str("The Secure Note's name, such as \"Stripe API key\""), "env": str("Environment variable to put it in"), "command": str("The shell command"),
		})},
	}
}

// Call runs one tool on t.
func (k *Kit) Call(t *helpers.Task, name string, input json.RawMessage) (string, error) {
	var in struct {
		Command    string `json:"command"`
		Cwd        string `json:"cwd"`
		Timeout    int    `json:"timeout"`
		Background bool   `json:"background"`
		Handle     string `json:"handle"`
		Stop       bool   `json:"stop"`
		Path       string `json:"path"`
		Range      string `json:"range"`
		Pattern    string `json:"pattern"`
		Glob       string `json:"glob"`
		Old        string `json:"old"`
		New        string `json:"new"`
		Content    string `json:"content"`
		Name       string `json:"name"`
		Env        string `json:"env"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	switch name {
	case "run":
		return k.run(t, in.Command, in.Cwd, in.Timeout, in.Background)
	case "job":
		return k.job(t, in.Handle, in.Stop)
	case "read":
		return k.read(in.Path, in.Range, in.Pattern)
	case "search":
		return k.search(t, in.Pattern, in.Path, in.Glob)
	case "edit":
		if err := k.call(http.MethodPost, k.nas+"/fs/edit", map[string]string{"path": in.Path, "old_string": in.Old, "new_string": in.New}, nil); err != nil {
			return "", err
		}
		touch(t, in.Path)
		return "edited " + in.Path, nil
	case "write":
		var out struct {
			Bytes int `json:"bytes"`
		}
		if err := k.call(http.MethodPost, k.nas+"/fs/write", map[string]string{"path": in.Path, "content": in.Content}, &out); err != nil {
			return "", err
		}
		touch(t, in.Path)
		return fmt.Sprintf("wrote %d bytes to %s", out.Bytes, in.Path), nil
	case "secret":
		return k.secret(t, in.Name, in.Env, in.Command)
	default:
		return "", fmt.Errorf("unknown tool %s", name)
	}
}

// run runs command in the foreground, keeping its full output in a log, or starts it in the
// background and records its handle.
func (k *Kit) run(t *helpers.Task, command, cwd string, timeout int, background bool) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < 1 || timeout > maxTimeout {
		return "", fmt.Errorf("timeout must be 1 to %d seconds", maxTimeout)
	}
	dir := filepath.Join(k.home, logDir, t.ID)
	log := filepath.Join(dir, strconv.FormatInt(time.Now().UnixNano(), 10)+".log")
	prefix := "mkdir -p " + shellQuote(dir) + " && "
	if cwd != "" {
		prefix += "cd " + shellQuote(cwd) + " && "
	}
	if background {
		res, err := k.exec(t, prefix+"nohup bash -c "+shellQuote(command)+" > "+shellQuote(log)+" 2>&1 & echo $!", 30)
		if err != nil {
			return "", err
		}
		handle := strings.TrimSpace(res.Output)
		t.Note("job:"+handle, log)
		return fmt.Sprintf("started in the background: handle %s, output in %s", handle, log), nil
	}
	res, err := k.exec(t, prefix+"{ "+command+"\n} > "+shellQuote(log)+" 2>&1; s=$?; tail -c "+strconv.Itoa(outputTailBytes)+" "+shellQuote(log)+"; exit $s", timeout)
	if err != nil {
		return "", err
	}
	if res.TimedOut {
		return fmt.Sprintf("still running after %ds; output so far in %s:\n%s", timeout, log, res.Output), nil
	}
	return fmt.Sprintf("exit %d (full output: %s)\n%s", *res.ExitCode, log, res.Output), nil
}

// job reports on or stops a background job.
func (k *Kit) job(t *helpers.Task, handle string, stop bool) (string, error) {
	log, ok := t.Notes()["job:"+handle]
	if !ok {
		return "", fmt.Errorf("no background job %q", handle)
	}
	if stop {
		if _, err := k.exec(t, "kill "+shellQuote(handle), 10); err != nil {
			return "", err
		}
		t.Note("job:"+handle, "")
		return "stopped job " + handle, nil
	}
	res, err := k.exec(t, "if kill -0 "+shellQuote(handle)+" 2>/dev/null; then echo running; else echo finished; fi; tail -c "+strconv.Itoa(outputTailBytes)+" "+shellQuote(log), 10)
	if err != nil {
		return "", err
	}
	return res.Output, nil
}

// read reads a range of a file, or its lines matching pattern.
func (k *Kit) read(path, lines, pattern string) (string, error) {
	if pattern != "" {
		return k.grep(pattern, filepath.Dir(path), filepath.Base(path))
	}
	body := map[string]any{"path": path, "limit": 300}
	if lines != "" {
		start, end, ok := strings.Cut(lines, "-")
		from, err1 := strconv.Atoi(strings.TrimSpace(start))
		to, err2 := strconv.Atoi(strings.TrimSpace(end))
		if !ok || err1 != nil || err2 != nil || from < 1 || to < from {
			return "", fmt.Errorf("range must be start-end, such as 120-180")
		}
		body["offset"] = from
		body["limit"] = to - from + 1
	}
	var out struct {
		Content    string `json:"content"`
		TotalLines int    `json:"total_lines"`
		Truncated  bool   `json:"truncated"`
	}
	if err := k.call(http.MethodPost, k.nas+"/fs/read", body, &out); err != nil {
		return "", err
	}
	if out.Truncated {
		return fmt.Sprintf("%s\n(%d lines in all; read more with range)", strings.TrimRight(out.Content, "\n"), out.TotalLines), nil
	}
	return out.Content, nil
}

// search greps under path (default t's directory).
func (k *Kit) search(t *helpers.Task, pattern, path, glob string) (string, error) {
	if path == "" {
		path = t.Cwd
	}
	return k.grep(pattern, path, glob)
}

// grep runs Nas's ripgrep and prints matches grouped by file.
func (k *Kit) grep(pattern, cwd, glob string) (string, error) {
	body := map[string]any{"pattern": pattern, "cwd": cwd, "limit": 200}
	if glob != "" {
		body["glob"] = glob
	}
	var out struct {
		Matches []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Text string `json:"text"`
		} `json:"matches"`
		Truncated bool `json:"truncated"`
	}
	if err := k.call(http.MethodPost, k.nas+"/fs/grep", body, &out); err != nil {
		return "", err
	}
	if len(out.Matches) == 0 {
		return "No matches.", nil
	}
	var b strings.Builder
	last := ""
	for _, m := range out.Matches {
		if m.Path != last {
			last = m.Path
			b.WriteString(m.Path + "\n")
		}
		text := strings.TrimRight(m.Text, "\r\n")
		if len(text) > 300 {
			text = text[:300] + "…"
		}
		fmt.Fprintf(&b, "  %d: %s\n", m.Line, text)
	}
	if out.Truncated {
		b.WriteString("(more matches: narrow the pattern, path or glob)\n")
	}
	return b.String(), nil
}

// secret stages a Chaavi secret in a file only dadi can read, loads it into env and deletes
// the file in the same command, and scrubs the value from the output.
func (k *Kit) secret(t *helpers.Task, name, env, command string) (string, error) {
	if name == "" || env == "" || command == "" {
		return "", fmt.Errorf("name, env and command are required")
	}
	var secret struct {
		Value string `json:"value"`
	}
	if err := k.call(http.MethodPost, k.chaavi+"/v1/secrets/"+name+"/reveal", nil, &secret); err != nil {
		return "", fmt.Errorf("chaavi: %w", err)
	}
	staged := filepath.Join("/tmp", "dadi-secret-"+t.ID+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := k.call(http.MethodPost, k.nas+"/fs/write", map[string]string{"path": staged, "content": secret.Value}, nil); err != nil {
		return "", err
	}
	res, err := k.exec(t, "export "+env+"=$(cat "+shellQuote(staged)+"); rm -f "+shellQuote(staged)+"; "+command, defaultTimeout)
	if err != nil {
		return "", fmt.Errorf("%w (the staged secret %s may remain; remove it)", err, staged)
	}
	output := strings.ReplaceAll(res.Output, secret.Value, "***")
	if res.TimedOut {
		return "still running after 120s:\n" + output, nil
	}
	return fmt.Sprintf("exit %d\n%s", *res.ExitCode, output), nil
}
