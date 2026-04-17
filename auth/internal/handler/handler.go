// Package handler implements HTTP handlers for the Auth service.
// Routes: POST /v1/auth/register, /token, /refresh, GET /v1/auth/validate, POST /v1/auth/apikey/rotate
package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/metrics"

	"github.com/pulse-analytics/auth/internal/service"
)

// Handler holds HTTP handler dependencies for the Auth service.
type Handler struct {
	svc *service.AuthService
	m   *metrics.Registry
	log *zap.Logger
}

// New creates an auth Handler.
func New(svc *service.AuthService, m *metrics.Registry, log *zap.Logger) *Handler {
	return &Handler{svc: svc, m: m, log: log}
}

// Health returns 200 OK for liveness checks.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "auth"})
}

// Register creates a new org + app and returns API key + JWT.
// POST /v1/auth/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgName string `json:"org_name"`
		AppName string `json:"app_name"`
		Email   string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OrgName == "" || req.Email == "" {
		writeError(w, http.StatusBadRequest, "org_name and email are required")
		return
	}
	if req.AppName == "" {
		req.AppName = req.OrgName + " App"
	}

	orgID, appID, apiKey, token, err := h.svc.RegisterOrgApp(r.Context(), req.OrgName, req.AppName, req.Email)
	if err != nil {
		h.log.Error("register failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{
		"org_id":  orgID,
		"app_id":  appID,
		"api_key": apiKey,
		"token":   token,
		"expires": time.Now().Add(h.svc.JWTExpiry()).UTC().Format(time.RFC3339),
	})
}

// Token exchanges an API key for a JWT.
// POST /v1/auth/token
func (h *Handler) Token(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "api_key required")
		return
	}

	token, app, err := h.svc.ExchangeAPIKey(r.Context(), req.APIKey)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid api_key")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"token":   token,
		"org_id":  app.OrgID,
		"app_id":  app.ID,
		"expires": time.Now().Add(h.svc.JWTExpiry()).UTC().Format(time.RFC3339),
	})
}

// Refresh issues a new JWT from a still-valid existing JWT.
// POST /v1/auth/refresh
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	tokenStr := extractBearer(r)
	if tokenStr == "" {
		writeError(w, http.StatusUnauthorized, "missing Bearer token")
		return
	}

	newToken, claims, err := h.svc.RefreshToken(tokenStr)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"token":   newToken,
		"org_id":  claims.OrgID,
		"app_id":  claims.AppID,
		"expires": time.Now().Add(h.svc.JWTExpiry()).UTC().Format(time.RFC3339),
	})
}

// Validate introspects a JWT (used by internal services).
// GET /v1/auth/validate
func (h *Handler) Validate(w http.ResponseWriter, r *http.Request) {
	tokenStr := extractBearer(r)
	claims, err := h.svc.ValidateToken(tokenStr)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":   true,
		"org_id":  claims.OrgID,
		"app_id":  claims.AppID,
		"role":    claims.Role,
		"expires": claims.ExpiresAt.Time.UTC().Format(time.RFC3339),
	})
}

// RotateAPIKey generates a new API key for an app.
// POST /v1/auth/apikey/rotate
func (h *Handler) RotateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID string `json:"app_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AppID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}

	newKey, err := h.svc.RotateAPIKey(r.Context(), req.AppID)
	if err != nil {
		h.log.Error("rotate api key", zap.Error(err), zap.String("app_id", req.AppID))
		writeError(w, http.StatusInternalServerError, "rotation failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"app_id":  req.AppID,
		"api_key": newKey,
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func extractBearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(v, "Bearer "); ok {
		return after
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
