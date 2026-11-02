package main

import (
	"net/http"
	"strings"

	"github.com/dadi-os/nas/service/helpers"
)

// takeoverRoutes are the writes Ankur makes into a live view, with the kind of task whose
// resource each one reaches. The helpers use CDP and /terminals/{id}/exec instead, so these
// routes are his alone.
var takeoverRoutes = []struct{ prefix, suffix, kind string }{
	{"/browsers/", "/input", "browser"},
	{"/browsers/", "/navigate", "browser"},
	{"/terminals/", "/keys", "terminal"},
}

// takeoverResource reports which task kind and resource r writes into when it is one of
// Ankur's takeover routes.
func takeoverResource(r *http.Request) (kind, resource string, ok bool) {
	if r.Method != http.MethodPost {
		return "", "", false
	}
	for _, route := range takeoverRoutes {
		rest, found := strings.CutPrefix(r.URL.Path, route.prefix)
		if !found {
			continue
		}
		resource, found = strings.CutSuffix(rest, route.suffix)
		if found && resource != "" && !strings.Contains(resource, "/") {
			return route.kind, resource, true
		}
	}
	return "", "", false
}

// withTakeover pauses a helper, whether working, idle or asking, the first time Ankur acts on
// the browser or terminal it holds, before the input reaches the resource, and announces the
// change on bus. Every later input only counts as activity, which keeps the task awake while
// he drives.
func withTakeover(runner *helpers.Runner, bus *changeBus, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if kind, resource, ok := takeoverResource(r); ok && runner.Drive(kind, resource) {
			bus.publish()
		}
		next.ServeHTTP(w, r)
	})
}
