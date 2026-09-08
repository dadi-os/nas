package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type errorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

// writeError writes {error: {type, message}} and logs at error/warn with code.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	attrs := []any{"code", code, "status", status, "msg_detail", message}
	if id := requestIDFrom(r.Context()); id != "" {
		attrs = append(attrs, "request_id", id)
	}
	if status >= 500 {
		slog.Error("handler error", attrs...)
	} else {
		slog.Warn("handler error", attrs...)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Error: errorBody{Type: code, Message: message},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
