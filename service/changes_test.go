package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAffectsSessions(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/browsers", true},
		{http.MethodDelete, "/browsers/10", true},
		{http.MethodPost, "/browsers/10/input", false},
		{http.MethodGet, "/browsers", false},
		{http.MethodPost, "/terminals", true},
		{http.MethodPost, "/terminals/t1/exec", true},
		{http.MethodPost, "/terminals/t1/keys", true},
		{http.MethodDelete, "/terminals/t1", true},
		{http.MethodGet, "/terminals/t1/capture", false},
		{http.MethodPost, "/fs/write", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := affectsSessions(r); got != c.want {
			t.Errorf("%s %s: got %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestEventsStreamPublishesAroundSessionWrites(t *testing.T) {
	bus := newChangeBus()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", bus.handleEvents)
	mux.HandleFunc("POST /terminals/{id}/exec", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /fs/write", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(withChangePublish(bus, mux))
	defer srv.Close()

	stream, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if got := stream.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type %q", got)
	}
	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(stream.Body)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data: ") {
				lines <- scanner.Text()
			}
		}
	}()

	if _, err := http.Post(srv.URL+"/fs/write", "application/json", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-lines:
		t.Fatalf("a file write published %q", line)
	case <-time.After(100 * time.Millisecond):
	}

	if _, err := http.Post(srv.URL+"/terminals/t1/exec", "application/json", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-lines:
		if !strings.Contains(line, `"type":"changed"`) {
			t.Fatalf("unexpected event %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a terminal exec published nothing")
	}
}
