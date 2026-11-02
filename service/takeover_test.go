package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTakeoverResourceNamesOnlyAnkursWrites(t *testing.T) {
	cases := []struct {
		method, path, kind, resource string
		ok                           bool
	}{
		{http.MethodPost, "/browsers/3/input", "browser", "3", true},
		{http.MethodPost, "/terminals/t7/keys", "terminal", "t7", true},
		{http.MethodPost, "/terminals/t7/exec", "", "", false},
		{http.MethodGet, "/browsers/3/input", "", "", false},
		{http.MethodPost, "/browsers//input", "", "", false},
		{http.MethodPost, "/browsers/3/x/input", "", "", false},
		{http.MethodPost, "/tasks/terminal/ab12/steer", "", "", false},
	}
	for _, c := range cases {
		kind, resource, ok := takeoverResource(httptest.NewRequest(c.method, c.path, nil))
		if kind != c.kind || resource != c.resource || ok != c.ok {
			t.Errorf("%s %s = %q %q %v, want %q %q %v", c.method, c.path, kind, resource, ok, c.kind, c.resource, c.ok)
		}
	}
}
