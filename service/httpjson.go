package main

import (
	"encoding/json"
	"errors"
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

// apiError is a failure that carries its HTTP status and stable error code, for helpers
// shared by several handlers.
type apiError struct {
	status  int
	code    string
	message string
}

// Error returns the message.
func (e apiError) Error() string {
	return e.message
}

// writeAPIError writes err with its own status and code when it is an apiError, and as
// internal_error otherwise.
func writeAPIError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr apiError
	if errors.As(err, &apiErr) {
		writeError(w, r, apiErr.status, apiErr.code, apiErr.message)
		return
	}
	writeError(w, r, http.StatusInternalServerError, CodeInternal, err.Error())
}

// writeJSON writes v as JSON with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
