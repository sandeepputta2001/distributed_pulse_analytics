// Package handler implements HTTP handlers for analytics queries.
package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/auth"
	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/querying"

	"github.com/pulse-analytics/query-api/internal/service"
)

// AnalyticsHandler handles all analytics query endpoints.
type AnalyticsHandler struct {
	svc *service.AnalyticsService
	m   *metrics.Registry
	log *zap.Logger
}

// NewAnalyticsHandler creates an AnalyticsHandler.
func NewAnalyticsHandler(svc *service.AnalyticsService, m *metrics.Registry, log *zap.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{svc: svc, m: m, log: log}
}

// Health returns 200 OK.
func (h *AnalyticsHandler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "query-api"})
}

// AuthLogin exchanges an API key for a JWT (delegates to auth service in production,
// kept here for backward compatibility).
func (h *AnalyticsHandler) AuthLogin(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "use auth service: POST /v1/auth/token")
}

// EventCount returns event counts bucketed by granularity.
// GET /v1/events/count
func (h *AnalyticsHandler) EventCount(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	appID := q.Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}

	req := querying.EventCountRequest{
		AppID:       appID,
		EventName:   q.Get("event_name"),
		FromMs:      parseMillis(q.Get("from_ms"), defaultFromMs()),
		ToMs:        parseMillis(q.Get("to_ms"), time.Now().UnixMilli()),
		Granularity: defaultStr(q.Get("granularity"), "day"),
	}

	resp, err := h.svc.EventCount(r.Context(), req)
	if err != nil {
		h.log.Error("event count", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	h.m.QueryRequests.WithLabelValues("event_count", "200").Inc()
	writeJSON(w, http.StatusOK, resp)
}

// FunnelQuery computes funnel conversion rates.
// POST /v1/funnels/query
func (h *AnalyticsHandler) FunnelQuery(w http.ResponseWriter, r *http.Request) {
	var req querying.FunnelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AppID == "" {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.FromMs == 0 {
		req.FromMs = defaultFromMs()
	}
	if req.ToMs == 0 {
		req.ToMs = time.Now().UnixMilli()
	}
	if req.WindowSeconds == 0 {
		req.WindowSeconds = 7 * 86400
	}

	resp, err := h.svc.Funnel(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.m.QueryRequests.WithLabelValues("funnel", "200").Inc()
	writeJSON(w, http.StatusOK, resp)
}

// DAU returns daily/weekly/monthly active users.
// GET /v1/dau
func (h *AnalyticsHandler) DAU(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	appID := q.Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}
	req := querying.DAURequest{
		AppID:       appID,
		FromMs:      parseMillis(q.Get("from_ms"), defaultFromMs()),
		ToMs:        parseMillis(q.Get("to_ms"), time.Now().UnixMilli()),
		Granularity: defaultStr(q.Get("granularity"), "day"),
	}
	resp, err := h.svc.DAU(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Retention computes day-N retention cohorts.
// POST /v1/retention
func (h *AnalyticsHandler) Retention(w http.ResponseWriter, r *http.Request) {
	var req querying.RetentionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AppID == "" {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.DayNs == nil {
		req.DayNs = []int32{1, 3, 7, 14, 30}
	}
	resp, err := h.svc.Retention(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// SessionMetrics returns aggregated session KPIs.
// GET /v1/sessions/metrics
func (h *AnalyticsHandler) SessionMetrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	appID := q.Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}
	fromMs := parseMillis(q.Get("from_ms"), defaultFromMs())
	toMs := parseMillis(q.Get("to_ms"), time.Now().UnixMilli())

	sql := `
		SELECT
			avg(duration_s) AS avg_duration,
			median(duration_s) AS median_duration,
			count() AS total_sessions,
			avg(event_count) AS avg_events
		FROM (
			SELECT session_id,
				max(toInt64(props['session_duration_s'])) AS duration_s,
				count() AS event_count
			FROM events
			WHERE app_id = ? AND event_time BETWEEN ? AND ?
			  AND event_name = 'session_end'
			GROUP BY session_id
		)`

	row := h.svc.RawQueryRow(r.Context(), sql,
		appID, time.UnixMilli(fromMs), time.UnixMilli(toMs))

	var avgDur, medDur, avgEvents float64
	var totalSessions int64
	if err := row.Scan(&avgDur, &medDur, &totalSessions, &avgEvents); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"avg_session_duration_s":    avgDur,
		"median_session_duration_s": medDur,
		"total_sessions":            totalSessions,
		"avg_events_per_session":    avgEvents,
	})
}

// GetClaimsAppID extracts app_id from JWT claims or query param.
func GetClaimsAppID(r *http.Request) string {
	if claims, ok := auth.ClaimsFromContext(r); ok {
		return claims.AppID
	}
	return r.URL.Query().Get("app_id")
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseMillis(s string, def int64) int64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}

func defaultFromMs() int64 {
	return time.Now().AddDate(0, 0, -30).UnixMilli()
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
