package handler

import (
	"net/http"

	"go.uber.org/zap"
)

// HTTPHandler exposes health and metrics endpoints.
type HTTPHandler struct {
	log *zap.Logger
}

// NewHTTPHandler creates an HTTPHandler.
func NewHTTPHandler(log *zap.Logger) *HTTPHandler {
	return &HTTPHandler{log: log}
}

// Health returns 200 OK.
func (h *HTTPHandler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","service":"notification"}`))
}
