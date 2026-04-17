// Package handler implements the HTTP handlers for the Ingest Gateway.
// Routes: POST /v1/events, POST /v1/identify, POST /v1/track
package handler

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/auth"
	"github.com/pulse-analytics/shared/pkg/geo"
	"github.com/pulse-analytics/shared/pkg/health"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/models"
	"github.com/pulse-analytics/shared/pkg/tracing"

	"github.com/pulse-analytics/gateway/internal/repo"
	"github.com/pulse-analytics/gateway/internal/service"
)

const maxBatchSize = 500

// Handler holds all HTTP handler dependencies.
type Handler struct {
	svc         *service.IngestService
	repo        *repo.Repo
	geo         *geo.Resolver
	authSvc     *auth.Service
	metrics     *metrics.Registry
	log         *zap.Logger
	ready       *health.Checker
	maxBodyBytes int64
}

// New creates a Handler.
func New(
	svc *service.IngestService,
	r *repo.Repo,
	geoRes *geo.Resolver,
	authSvc *auth.Service,
	m *metrics.Registry,
	log *zap.Logger,
	ready *health.Checker,
	maxBodyBytes int64,
) *Handler {
	return &Handler{
		svc:          svc,
		repo:         r,
		geo:          geoRes,
		authSvc:      authSvc,
		metrics:      m,
		log:          log,
		ready:        ready,
		maxBodyBytes: maxBodyBytes,
	}
}

// Health returns 200 OK for liveness probes.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "gateway"})
}

// Ready runs deep dependency checks for readiness probes.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	h.ready.Handler()(w, r)
}

// HandleIngest accepts a batch of up to 500 events, deduplicates, and publishes to Kafka.
// POST /v1/events
func (h *Handler) HandleIngest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx, span := tracing.Tracer("gateway").Start(r.Context(), "ingest")
	defer span.End()

	app, _ := auth.AppFromContext(ctx)

	body, err := readBody(r, h.maxBodyBytes)
	if err != nil {
		h.metrics.IngestErrors.WithLabelValues("body_read").Inc()
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	var batch models.EventBatch
	if err := json.Unmarshal(body, &batch); err != nil {
		h.metrics.IngestErrors.WithLabelValues("parse").Inc()
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(batch.Events) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(batch.Events) > maxBatchSize {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("max %d events per batch", maxBatchSize))
		return
	}

	batch.AppID = app.ID
	clientIP := geo.ExtractIP(r.Header.Get("X-Forwarded-For"), r.RemoteAddr)

	span.SetAttributes(
		attribute.String("app_id", app.ID),
		attribute.Int("event_count", len(batch.Events)),
		attribute.String("client_ip", clientIP),
	)

	result, err := h.svc.ProcessBatch(ctx, app.ID, batch.DeviceID, clientIP, batch.Events)
	if err != nil {
		h.log.Error("ingest failed", zap.String("app_id", app.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Archive to MongoDB asynchronously
	if result.Accepted > 0 {
		go func() {
			mgoCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := h.repo.InsertRawBatch(mgoCtx, app.ID, batch.Events); err != nil {
				h.log.Warn("mongo archive failed", zap.Error(err))
			}
		}()
	}

	h.metrics.IngestRequests.WithLabelValues("202").Inc()
	h.metrics.IngestLatency.WithLabelValues("ingest").Observe(time.Since(start).Seconds())

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": result.Accepted,
		"filtered": result.Filtered,
	})
}

// HandleIdentify upserts a user profile and emits a user_updated event.
// POST /v1/identify
func (h *Handler) HandleIdentify(w http.ResponseWriter, r *http.Request) {
	app, _ := auth.AppFromContext(r.Context())

	var payload struct {
		UserID string         `json:"user_id"`
		Traits map[string]any `json:"traits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id required")
		return
	}

	if err := h.svc.PublishIdentify(r.Context(), app.ID, payload.UserID, payload.Traits); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.repo.UpsertUserProfile(ctx, app.ID, payload.UserID, payload.Traits)
	}()

	w.WriteHeader(http.StatusAccepted)
}

// HandleTrack accepts and publishes a single event.
// POST /v1/track
func (h *Handler) HandleTrack(w http.ResponseWriter, r *http.Request) {
	app, _ := auth.AppFromContext(r.Context())

	var evt models.Event
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := h.svc.PublishTrack(r.Context(), app.ID, evt); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// PrometheusMiddleware wraps every request with latency/status metrics.
func PrometheusMiddleware(m *metrics.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			status := fmt.Sprintf("%d", ww.Status())
			m.IngestRequests.WithLabelValues(status).Inc()
			m.IngestLatency.WithLabelValues(r.URL.Path).Observe(time.Since(start).Seconds())
		})
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	var reader io.Reader = http.MaxBytesReader(nil, r.Body, maxBytes)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}
	return io.ReadAll(reader)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
