package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dadi-os/nas/service/helpers"
)

// changesHeartbeat is how often an idle GET /events stream writes a comment so proxies and
// clients see the connection is alive.
const changesHeartbeat = 20 * time.Second

// changesReportBuffer is how many helper reports a slow subscriber may fall behind by before
// one is dropped (and logged); a client refetches GET /tasks on reconnect.
const changesReportBuffer = 64

// changeBus fans "the browser, terminal or task lists changed" and helper reports out to
// GET /events subscribers. A subscriber that has not read the last change signal yet gets no
// second one: it will refetch once for both.
type changeBus struct {
	mu      sync.Mutex
	subs    map[chan struct{}]struct{}
	reports map[chan []byte]struct{}
}

func newChangeBus() *changeBus {
	return &changeBus{subs: map[chan struct{}]struct{}{}, reports: map[chan []byte]struct{}{}}
}

// publishReport sends a helper's report to every subscriber as a helper_report event, and
// signals that the task list changed.
func (b *changeBus) publishReport(report helpers.Report) {
	raw, err := json.Marshal(struct {
		Type string `json:"type"`
		helpers.Report
	}{"helper_report", report})
	if err != nil {
		slog.Error("encode helper report failed", "code", CodeInternal, "err", err)
		return
	}
	b.mu.Lock()
	for ch := range b.reports {
		select {
		case ch <- raw:
		default:
			slog.Warn("events subscriber too slow; helper report dropped", "task", report.Task, "seq", report.Seq)
		}
	}
	b.mu.Unlock()
	b.publish()
}

// publish signals every subscriber without blocking.
func (b *changeBus) publish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (b *changeBus) subscribe() (chan struct{}, chan []byte, func()) {
	ch := make(chan struct{}, 1)
	reports := make(chan []byte, changesReportBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.reports[reports] = struct{}{}
	b.mu.Unlock()
	return ch, reports, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		delete(b.reports, reports)
		b.mu.Unlock()
	}
}

// handleEvents is GET /events: a Server-Sent Events stream that emits {"type":"changed"}
// whenever a browser, terminal or task changes (see affectsSessions), and each helper report
// as {"type":"helper_report", …}.
func (b *changeBus) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, CodeInternal, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	signals, reports, unsubscribe := b.subscribe()
	defer unsubscribe()
	heartbeat := time.NewTicker(changesHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-signals:
			fmt.Fprintf(w, "data: {\"type\":\"changed\",\"at\":%q}\n\n", time.Now().UTC().Format(time.RFC3339Nano))
		case raw := <-reports:
			fmt.Fprintf(w, "data: %s\n\n", raw)
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}

// affectsSessions reports whether r can change what GET /browsers, GET /terminals or
// GET /tasks return: starting or steering a task, and any write to a terminal (running a
// command flips busy, keys move the working directory). Browser input is left out: it fires
// constantly while someone drives a browser and changes no list.
func affectsSessions(r *http.Request) bool {
	if r.Method == http.MethodGet {
		return false
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/tasks/"):
		return true
	default:
		return path == "/terminals" || strings.HasPrefix(path, "/terminals/")
	}
}

// withChangePublish publishes before and after any request that affectsSessions, so a
// command that runs for a while shows busy while it runs and idle once it returns.
func withChangePublish(bus *changeBus, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !affectsSessions(r) {
			next.ServeHTTP(w, r)
			return
		}
		bus.publish()
		defer bus.publish()
		next.ServeHTTP(w, r)
	})
}
