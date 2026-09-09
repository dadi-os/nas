package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type ctxKeyRequestID struct{}

// newLogger returns the canonical JSON slog logger for nas
// (time RFC3339, level, service, msg; optional code / request fields via attrs).
func newLogger() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.MessageKey:
				a.Key = "msg"
			case slog.TimeKey:
				a.Key = "time"
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.String("time", t.UTC().Format(time.RFC3339Nano))
				}
			case slog.LevelKey:
				if level, ok := a.Value.Any().(slog.Level); ok {
					return slog.String("level", strings.ToLower(level.String()))
				}
			}
			return a
		},
	})
	return slog.New(handler).With("service", "nas")
}

func requestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyRequestID{}).(string); ok {
		return id
	}
	return ""
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID{}, id)
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000")))
	}
	return hex.EncodeToString(b[:])
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack unwraps the underlying ResponseWriter so CDP WebSocket upgrades work.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

// Flush unwraps Flusher when present (SSE / streaming).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withRequestLog logs one structured request summary per HTTP call.
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = newRequestID()
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		ctx := withRequestID(r.Context(), id)
		next.ServeHTTP(rec, r.WithContext(ctx))

		attrs := []any{
			"request_id", id,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if rec.status >= 500 {
			slog.Error("request", attrs...)
			return
		}
		if rec.status >= 400 {
			slog.Warn("request", attrs...)
			return
		}
		slog.Info("request", attrs...)
	})
}
