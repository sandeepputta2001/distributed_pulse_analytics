// Package main implements the Auth Service — a dedicated microservice for
// JWT token issuance, API key management, and organisation/app registration.
//
// Endpoints:
//
//	POST /v1/auth/register        Register new org + app, returns API key + JWT
//	POST /v1/auth/token           Exchange credentials for a JWT (login)
//	POST /v1/auth/refresh         Refresh an expiring JWT
//	POST /v1/auth/apikey/rotate   Rotate an app's API key (requires JWT)
//	GET  /v1/auth/validate        Validate a JWT (internal service-to-service use)
//	GET  /health                  Liveness probe
//	GET  /metrics                 Prometheus scrape target
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/auth"
	"github.com/pulse-analytics/internal/config"
	"github.com/pulse-analytics/internal/metrics"
	"github.com/pulse-analytics/internal/postgres"
	redisclient "github.com/pulse-analytics/internal/redis"
	"github.com/pulse-analytics/internal/tracing"
)

func main() {
	log := newLogger()
	defer log.Sync()

	cfgPath := envOrDefault("CONFIG_PATH", "configs/base.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal("config load", zap.Error(err))
	}
	cfg.Service.Name = "auth-service"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tp, _ := tracing.Init(ctx, &cfg.Telemetry, log)
	defer tp.Shutdown(context.Background())

	m := metrics.NewRegistry(cfg.Service.Name)

	redis, err := redisclient.NewClient(&cfg.Redis, log)
	if err != nil {
		log.Fatal("redis init", zap.Error(err))
	}
	defer redis.Close()

	pg, err := postgres.NewClient(&cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres init", zap.Error(err))
	}
	defer pg.Close()

	authSvc := auth.NewService(pg, redis, &cfg.Auth, log)

	h := &authHandler{authSvc: authSvc, pg: pg, cfg: cfg, m: m, log: log}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
	}))

	r.Get("/health", h.health)
	r.Handle("/metrics", m.Handler())

	r.Route("/v1/auth", func(r chi.Router) {
		// Public — no auth required
		r.Post("/register", h.register)
		r.Post("/token", h.token)
		r.Post("/refresh", h.refresh)

		// Protected — JWT required
		r.Group(func(r chi.Router) {
			r.Use(authSvc.JWTMiddleware)
			r.Get("/validate", h.validate)
			r.Post("/apikey/rotate", h.rotateAPIKey)
		})
	})

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	if cfg.HTTP.Port == 0 {
		addr = ":8083"
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("auth-service starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down auth-service...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
}

// ─── Handler ──────────────────────────────────────────────────────────────────

type authHandler struct {
	authSvc *auth.Service
	pg      *postgres.Client
	cfg     *config.Config
	m       *metrics.Registry
	log     *zap.Logger
}

func (h *authHandler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "auth-service"})
}

// register creates a new organisation + application, returning the API key and
// an initial JWT so the caller can immediately start querying.
//
// POST /v1/auth/register
// Body: { "org_name": "Acme Corp", "app_name": "Mobile App", "email": "admin@acme.com" }
func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
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

	orgID, appID, apiKey, err := h.pg.CreateOrgAndApp(r.Context(), req.OrgName, req.AppName, req.Email)
	if err != nil {
		h.log.Error("register: create org/app", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}

	token, err := h.authSvc.GenerateToken(orgID, appID, "admin")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}

	h.log.Info("org registered", zap.String("org_id", orgID), zap.String("app_id", appID))
	writeJSON(w, http.StatusCreated, map[string]string{
		"org_id":  orgID,
		"app_id":  appID,
		"api_key": apiKey,
		"token":   token,
		"expires": time.Now().Add(h.cfg.Auth.JWTExpiry).UTC().Format(time.RFC3339),
	})
}

// token exchanges org_id + app_id + api_key for a JWT.
//
// POST /v1/auth/token
// Body: { "api_key": "pk_live_..." }
func (h *authHandler) token(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "api_key required")
		return
	}

	app, err := h.authSvc.ValidateAPIKey(r.Context(), req.APIKey)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid api_key")
		return
	}

	token, err := h.authSvc.GenerateToken(app.OrgID, app.ID, "admin")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token generation failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"token":   token,
		"org_id":  app.OrgID,
		"app_id":  app.ID,
		"expires": time.Now().Add(h.cfg.Auth.JWTExpiry).UTC().Format(time.RFC3339),
	})
}

// refresh issues a new JWT from a still-valid existing JWT.
//
// POST /v1/auth/refresh
// Header: Authorization: Bearer <token>
func (h *authHandler) refresh(w http.ResponseWriter, r *http.Request) {
	tokenStr := extractBearer(r)
	if tokenStr == "" {
		writeError(w, http.StatusUnauthorized, "missing Bearer token")
		return
	}

	claims, err := h.authSvc.ValidateToken(tokenStr)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired token")
		return
	}

	newToken, err := h.authSvc.GenerateToken(claims.OrgID, claims.AppID, claims.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token refresh failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"token":   newToken,
		"org_id":  claims.OrgID,
		"app_id":  claims.AppID,
		"expires": time.Now().Add(h.cfg.Auth.JWTExpiry).UTC().Format(time.RFC3339),
	})
}

// validate introspects the JWT in the Authorization header.
// Used by other services for internal token validation.
//
// GET /v1/auth/validate
func (h *authHandler) validate(w http.ResponseWriter, r *http.Request) {
	// JWTMiddleware already ran; if we're here the token is valid
	tokenStr := extractBearer(r)
	claims, _ := h.authSvc.ValidateToken(tokenStr)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":   true,
		"org_id":  claims.OrgID,
		"app_id":  claims.AppID,
		"role":    claims.Role,
		"expires": claims.ExpiresAt.Time.UTC().Format(time.RFC3339),
	})
}

// rotateAPIKey generates a new API key for the app, invalidates the old one in cache.
//
// POST /v1/auth/apikey/rotate
// Body: { "app_id": "..." }
func (h *authHandler) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AppID string `json:"app_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AppID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}

	newKey, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "key generation failed")
		return
	}

	if err := h.pg.RotateAPIKey(r.Context(), req.AppID, newKey); err != nil {
		h.log.Error("rotate api key", zap.Error(err), zap.String("app_id", req.AppID))
		writeError(w, http.StatusInternalServerError, "rotation failed")
		return
	}

	h.log.Info("api key rotated", zap.String("app_id", req.AppID))
	writeJSON(w, http.StatusOK, map[string]string{
		"app_id":  req.AppID,
		"api_key": newKey,
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func generateAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pk_live_" + hex.EncodeToString(b), nil
}

func extractBearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(v, "Bearer "); ok {
		return after
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func newLogger() *zap.Logger {
	if os.Getenv("PULSE_SERVICE_ENVIRONMENT") == "production" {
		l, _ := zap.NewProduction()
		return l
	}
	l, _ := zap.NewDevelopment()
	return l
}
