package helpers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// logRetention is how long a helper's activity log is kept after its last entry; it
	// outlives forgetAfter so the record of a forgotten task stays readable.
	logRetention = 30 * 24 * time.Hour
	// pruneEvery is the least time between two prunes of the log directory.
	pruneEvery = time.Hour
	// logDirName is the subdirectory of the helpers dir that holds the logs; the task loader
	// only reads *.json files in the helpers dir itself, so it never sees them.
	logDirName = "log"
	// logLongMax and logTextMax bound the text one log entry keeps.
	logLongMax = 4000
	logTextMax = 2000
)

// Entry types written to a task's log.
const (
	entrySteer  = "steer"
	entryTurn   = "turn"
	entryResult = "result"
	entryState  = "state"
	entryReport = "report"
)

// Usage is the token counts of one model call, as Dwar reports them.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// head is the part every log entry starts with. The journal fills Seq and At.
type head struct {
	Seq   int64  `json:"seq"`
	At    string `json:"at"`
	Type  string `json:"type"`
	Agent string `json:"agent"`
}

// header returns h so every row type is an entry.
func (h *head) header() *head { return h }

// entry is a log row; every row type embeds head.
type entry interface{ header() *head }

// steerRow is a steer: who sent it, whether it was held while Ankur drove, and whether it started the task.
type steerRow struct {
	head
	From    string `json:"from"`
	Message string `json:"message"`
	Held    bool   `json:"held"`
	Start   bool   `json:"start"`
}

// callRow is a tool call the model asked for, its input as clipped compact JSON.
type callRow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

// turnRow is one model call.
type turnRow struct {
	head
	Ms         int64     `json:"ms"`
	StopReason string    `json:"stop_reason"`
	Usage      Usage     `json:"usage"`
	Thinking   string    `json:"thinking"`
	Text       string    `json:"text"`
	Calls      []callRow `json:"calls"`
}

// resultRow is one tool call's outcome.
type resultRow struct {
	head
	ToolUseID string `json:"tool_use_id"`
	Name      string `json:"name"`
	IsError   bool   `json:"is_error"`
	Chars     int    `json:"chars"`
	Head      string `json:"head"`
	Ms        int64  `json:"ms"`
}

// stateRow is a lifecycle transition.
type stateRow struct {
	head
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// reportRow is a report made for the task.
type reportRow struct {
	head
	State string   `json:"state"`
	Text  string   `json:"text"`
	Files []string `json:"files"`
}

// newTurnRow is the log row of one model answer that took the given time.
func newTurnRow(resp chatResponse, took time.Duration) *turnRow {
	row := &turnRow{Ms: took.Milliseconds(), StopReason: resp.StopReason, Usage: resp.Usage, Calls: []callRow{}}
	var thinking, text []string
	for _, b := range resp.Content {
		switch b["type"] {
		case "thinking":
			s, _ := b["thinking"].(string)
			thinking = append(thinking, s)
		case "text":
			s, _ := b["text"].(string)
			text = append(text, s)
		case "tool_use":
			id, _ := b["id"].(string)
			name, _ := b["name"].(string)
			row.Calls = append(row.Calls, callRow{ID: id, Name: name, Input: compactJSON(b["input"], logTextMax)})
		}
	}
	row.Thinking = clipRunes(strings.Join(thinking, "\n"), logLongMax)
	row.Text = clipRunes(strings.Join(text, "\n"), logTextMax)
	return row
}

// TaskUsage is one task's model and tool activity since a point in time.
type TaskUsage struct {
	Task                     string `json:"task"`
	Kind                     string `json:"kind"`
	Owner                    string `json:"owner"`
	Steps                    int    `json:"steps"`
	Calls                    int    `json:"calls"`
	InputTokens              int    `json:"input_tokens"`
	OutputTokens             int    `json:"output_tokens"`
	CacheReadInputTokens     int    `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int    `json:"cache_creation_input_tokens"`
	FirstAt                  string `json:"first_at"`
	LastAt                   string `json:"last_at"`
}

// journal is the durable, append-only activity log of every helper task: one JSONL file per
// task under dir, one entry per line, numbered from 1 per task.
type journal struct {
	dir       string
	mu        sync.Mutex
	seqs      map[string]int64
	lastPrune time.Time
}

// newJournal returns a journal writing under dir, which it creates.
func newJournal(dir string) (*journal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("helper log dir: %w", err)
	}
	return &journal{dir: dir, seqs: map[string]int64{}}, nil
}

// clipRunes shortens s to max runes, marking a cut with an ellipsis; it never splits a rune.
func clipRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return clip(s, max)
}

// compactJSON is v as compact JSON, clipped to max runes.
func compactJSON(v any, max int) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return clipRunes(fmt.Sprintf("%v", v), max)
	}
	return clipRunes(string(raw), max)
}

// file is the log path of kind/id, false when the name could leave the log dir.
func (j *journal) file(kind, id string) (string, bool) {
	if strings.ContainsAny(kind+id, `/\`) || kind == "" || id == "" {
		return "", false
	}
	return filepath.Join(j.dir, kind+"-"+id+".jsonl"), true
}

// countLines is the number of complete lines in the file at path; a missing file has none.
func countLines(path string) (int64, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var n int64
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			n++
		}
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// append writes e as the next line of task kind/id's log, stamping its seq and time.
func (j *journal) append(kind, id string, e entry) error {
	path, ok := j.file(kind, id)
	if !ok {
		return fmt.Errorf("invalid task %q/%q", kind, id)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	seq, seeded := j.seqs[path]
	if !seeded {
		n, err := countLines(path)
		if err != nil {
			return fmt.Errorf("count log lines: %w", err)
		}
		seq = n
	}
	h := e.header()
	h.Seq = seq + 1
	h.At = time.Now().UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode log entry: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	_, werr := f.Write(append(raw, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write log: %w", werr)
	}
	j.seqs[path] = h.Seq
	return nil
}

// read returns up to limit entries of kind/id's log with seq greater than afterSeq, oldest
// first, and the last seq returned when a later entry exists (nil otherwise). exists is false
// when the task has no log.
func (j *journal) read(kind, id string, afterSeq int64, limit int) (entries []json.RawMessage, next *int64, exists bool, err error) {
	path, ok := j.file(kind, id)
	if !ok {
		return nil, nil, false, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("open log: %w", err)
	}
	defer f.Close()
	entries = []json.RawMessage{}
	r := bufio.NewReader(f)
	var seq int64
	for {
		line, rerr := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			seq++
			if seq > afterSeq {
				if len(entries) == limit {
					last := seq - 1
					return entries, &last, true, nil
				}
				entries = append(entries, json.RawMessage(bytes.TrimSpace(line)))
			}
		}
		if rerr == io.EOF {
			return entries, nil, true, nil
		}
		if rerr != nil {
			return nil, nil, true, fmt.Errorf("read log: %w", rerr)
		}
	}
}

// usage sums the model and tool activity of every logged task over entries at or after
// since, newest activity first.
func (j *journal) usage(since time.Time) ([]TaskUsage, error) {
	files, err := os.ReadDir(j.dir)
	if err != nil {
		return nil, fmt.Errorf("read helper log dir: %w", err)
	}
	out := []TaskUsage{}
	for _, de := range files {
		name, ok := strings.CutSuffix(de.Name(), ".jsonl")
		if !ok {
			continue
		}
		cut := strings.LastIndex(name, "-")
		if cut < 0 {
			continue
		}
		u := TaskUsage{Task: "task/" + name[:cut] + "/" + name[cut+1:], Kind: name[:cut]}
		var newest time.Time
		f, err := os.Open(filepath.Join(j.dir, de.Name()))
		if err != nil {
			return nil, fmt.Errorf("open log %s: %w", de.Name(), err)
		}
		r := bufio.NewReader(f)
		for {
			line, rerr := r.ReadBytes('\n')
			if len(line) > 0 && line[len(line)-1] == '\n' {
				var row struct {
					At    string `json:"at"`
					Type  string `json:"type"`
					Agent string `json:"agent"`
					Usage Usage  `json:"usage"`
				}
				if err := json.Unmarshal(line, &row); err != nil {
					_ = f.Close()
					return nil, fmt.Errorf("parse log %s: %w", de.Name(), err)
				}
				at, err := time.Parse(time.RFC3339Nano, row.At)
				if err != nil {
					_ = f.Close()
					return nil, fmt.Errorf("parse log %s time: %w", de.Name(), err)
				}
				if !at.Before(since) {
					switch row.Type {
					case entryTurn:
						u.Calls++
						u.InputTokens += row.Usage.InputTokens
						u.OutputTokens += row.Usage.OutputTokens
						u.CacheReadInputTokens += row.Usage.CacheReadInputTokens
						u.CacheCreationInputTokens += row.Usage.CacheCreationInputTokens
					case entryResult:
						u.Steps++
					}
					if u.FirstAt == "" || row.At < u.FirstAt {
						u.FirstAt = row.At
					}
					if !at.Before(newest) {
						newest, u.Owner, u.LastAt = at, row.Agent, row.At
					}
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				_ = f.Close()
				return nil, fmt.Errorf("read log %s: %w", de.Name(), rerr)
			}
		}
		_ = f.Close()
		if u.LastAt != "" {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].LastAt > out[b].LastAt })
	return out, nil
}

// prune removes the logs last written more than logRetention before now.
func (j *journal) prune(now time.Time) error {
	files, err := os.ReadDir(j.dir)
	if err != nil {
		return fmt.Errorf("read helper log dir: %w", err)
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, de := range files {
		if !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			return fmt.Errorf("stat log %s: %w", de.Name(), err)
		}
		if now.Sub(info.ModTime()) <= logRetention {
			continue
		}
		path := filepath.Join(j.dir, de.Name())
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove log %s: %w", de.Name(), err)
		}
		delete(j.seqs, path)
	}
	return nil
}

// pruneDue prunes when pruneEvery has passed since the last prune.
func (j *journal) pruneDue(now time.Time) error {
	j.mu.Lock()
	due := now.Sub(j.lastPrune) >= pruneEvery
	if due {
		j.lastPrune = now
	}
	j.mu.Unlock()
	if !due {
		return nil
	}
	return j.prune(now)
}
