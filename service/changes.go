package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// changesHeartbeat is how often an idle GET /events stream writes a comment so proxies and
// clients see the connection is alive.
const changesHeartbeat = 20 * time.Second

// changeBus fans "the browser or terminal lists changed" out to GET /events subscribers.
// A subscriber that has not read the last signal yet gets no second one: it will refetch
// once for both.
type changeBus struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newChangeBus() *changeBus {
	return &changeBus{subs: map[chan struct{}]struct{}{}}
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

func (b *changeBus) subscribe() (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}

// handleEvents is GET /events: a Server-Sent Events stream that emits {"type":"changed"}
// whenever a browser or terminal is created, deleted or driven (see affectsSessions).
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

	signals, unsubscribe := b.subscribe()
	defer unsubscribe()
	heartbeat := time.NewTicker(changesHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-signals:
			fmt.Fprintf(w, "data: {\"type\":\"changed\",\"at\":%q}\n\n", time.Now().UTC().Format(time.RFC3339Nano))
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}

// affectsSessions reports whether r can change what GET /browsers or GET /terminals
// return: creating or closing a browser, and any write to a terminal (running a command
// flips busy, keys move the working directory). Browser input is left out: it fires
// constantly while someone remote-controls a browser and changes neither list.
func affectsSessions(r *http.Request) bool {
	if r.Method == http.MethodGet {
		return false
	}
	path := r.URL.Path
	switch {
	case path == "/browsers":
		return true
	case strings.HasPrefix(path, "/browsers/"):
		return r.Method == http.MethodDelete
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
