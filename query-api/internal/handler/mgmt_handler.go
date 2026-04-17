// mgmt_handler.go — CRUD endpoints for alerts, cohorts, apps, orgs, experiments, funnels.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/pulse-analytics/shared/pkg/metrics"
	"github.com/pulse-analytics/shared/pkg/models"

	"github.com/pulse-analytics/query-api/internal/repo"
	"github.com/pulse-analytics/query-api/internal/service"
)

// MgmtHandler handles all management CRUD endpoints.
type MgmtHandler struct {
	repo      *repo.PostgresRepo
	analytics *service.AnalyticsService
	m         *metrics.Registry
	log       *zap.Logger
}

// NewMgmtHandler creates a MgmtHandler.
func NewMgmtHandler(r *repo.PostgresRepo, a *service.AnalyticsService, m *metrics.Registry, log *zap.Logger) *MgmtHandler {
	return &MgmtHandler{repo: r, analytics: a, m: m, log: log}
}

// ─── Funnels ──────────────────────────────────────────────────────────────────

func (h *MgmtHandler) CreateFunnel(w http.ResponseWriter, r *http.Request) {
	var f models.FunnelDefinition
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil || f.AppID == "" {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	f.FunnelID = fmt.Sprintf("f-%d", time.Now().UnixNano())
	f.CreatedAt = time.Now()
	if err := h.repo.UpsertFunnel(r.Context(), &f); err != nil {
		writeError(w, http.StatusInternalServerError, "create funnel failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"funnel_id": f.FunnelID})
}

func (h *MgmtHandler) ListFunnels(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "app_id")
	funnels, err := h.repo.ListFunnels(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list funnels failed")
		return
	}
	writeJSON(w, http.StatusOK, funnels)
}

// ─── Apps ─────────────────────────────────────────────────────────────────────

func (h *MgmtHandler) ListApps(w http.ResponseWriter, r *http.Request) {
	apps, err := h.repo.ListApps(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list apps failed")
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

func (h *MgmtHandler) GetApp(w http.ResponseWriter, r *http.Request) {
	app, err := h.repo.GetApp(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	writeJSON(w, http.StatusOK, app)
}

func (h *MgmtHandler) UpdateApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string  `json:"name"`
		RPS   float64 `json:"rps"`
		Burst int     `json:"burst"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.repo.UpdateApp(r.Context(), chi.URLParam(r, "id"), body.Name, body.RPS, body.Burst); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MgmtHandler) DeleteApp(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.DeactivateApp(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Alerts ───────────────────────────────────────────────────────────────────

func (h *MgmtHandler) ListAlerts(w http.ResponseWriter, r *http.Request) {
	rules, err := h.repo.ListAlertRules(r.Context(), r.URL.Query().Get("app_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list alerts failed")
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (h *MgmtHandler) CreateAlert(w http.ResponseWriter, r *http.Request) {
	var rule models.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rule.ID = fmt.Sprintf("a-%d", time.Now().UnixNano())
	rule.CreatedAt = time.Now()
	rule.Active = true
	if err := h.repo.CreateAlertRule(r.Context(), &rule); err != nil {
		writeError(w, http.StatusInternalServerError, "create alert failed")
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (h *MgmtHandler) UpdateAlert(w http.ResponseWriter, r *http.Request) {
	var rule models.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rule.ID = chi.URLParam(r, "id")
	if err := h.repo.UpdateAlertRule(r.Context(), &rule); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *MgmtHandler) DeleteAlert(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.DeleteAlertRule(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Cohorts ──────────────────────────────────────────────────────────────────

func (h *MgmtHandler) ListCohorts(w http.ResponseWriter, r *http.Request) {
	cohorts, err := h.repo.ListCohorts(r.Context(), r.URL.Query().Get("app_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list cohorts failed")
		return
	}
	writeJSON(w, http.StatusOK, cohorts)
}

func (h *MgmtHandler) CreateCohort(w http.ResponseWriter, r *http.Request) {
	var co models.CohortDefinition
	if err := json.NewDecoder(r.Body).Decode(&co); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	co.ID = fmt.Sprintf("c-%d", time.Now().UnixNano())
	co.CreatedAt = time.Now()
	if err := h.repo.CreateCohort(r.Context(), &co); err != nil {
		writeError(w, http.StatusInternalServerError, "create cohort failed")
		return
	}
	writeJSON(w, http.StatusCreated, co)
}

func (h *MgmtHandler) DeleteCohort(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.DeleteCohort(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Experiments ──────────────────────────────────────────────────────────────

func (h *MgmtHandler) ListExperiments(w http.ResponseWriter, r *http.Request) {
	exps, err := h.repo.ListExperiments(r.Context(), r.URL.Query().Get("app_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list experiments failed")
		return
	}
	writeJSON(w, http.StatusOK, exps)
}

func (h *MgmtHandler) CreateExperiment(w http.ResponseWriter, r *http.Request) {
	var e models.Experiment
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	e.ID = fmt.Sprintf("e-%d", time.Now().UnixNano())
	e.CreatedAt = time.Now()
	if e.Status == "" {
		e.Status = "draft"
	}
	if err := h.repo.CreateExperiment(r.Context(), &e); err != nil {
		writeError(w, http.StatusInternalServerError, "create experiment failed")
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (h *MgmtHandler) UpdateExperiment(w http.ResponseWriter, r *http.Request) {
	var e models.Experiment
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	e.ID = chi.URLParam(r, "id")
	if err := h.repo.UpdateExperiment(r.Context(), &e); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (h *MgmtHandler) DeleteExperiment(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.DeleteExperiment(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Cohort Recompute ──────────────────────────────────────────────────────────

// RecomputeCohort runs the cohort's filter_sql against ClickHouse and updates user_count.
// POST /v1/cohorts/{id}/recompute
func (h *MgmtHandler) RecomputeCohort(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	co, err := h.repo.GetCohort(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "cohort not found")
		return
	}

	// Run: SELECT uniqExact(user_id) FROM events WHERE app_id = ? AND (<filter_sql>)
	sql := fmt.Sprintf(
		`SELECT uniqExact(user_id) FROM events WHERE app_id = '%s' AND (%s)`,
		co.AppID, co.FilterSQL,
	)
	var count int64
	if err := h.analytics.RawQueryRow(r.Context(), sql).Scan(&count); err != nil {
		h.log.Warn("cohort recompute query failed", zap.String("cohort_id", id), zap.Error(err))
		count = co.UserCount // keep old count on query error
	}

	now := time.Now()
	if err := h.repo.UpdateCohortCount(r.Context(), id, count, now); err != nil {
		writeError(w, http.StatusInternalServerError, "update cohort count failed")
		return
	}

	co.UserCount = count
	co.LastComputedAt = &now
	writeJSON(w, http.StatusOK, co)
}

// ─── Orgs ─────────────────────────────────────────────────────────────────────

func (h *MgmtHandler) ListOrgs(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.repo.ListOrgs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list orgs failed")
		return
	}
	writeJSON(w, http.StatusOK, orgs)
}

func (h *MgmtHandler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	var o models.Org
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil || o.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	o.ID = fmt.Sprintf("org-%d", time.Now().UnixNano())
	o.CreatedAt = time.Now()
	if o.Plan == "" {
		o.Plan = "free"
	}
	if err := h.repo.CreateOrg(r.Context(), &o); err != nil {
		writeError(w, http.StatusInternalServerError, "create org failed")
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

func (h *MgmtHandler) UpdateOrg(w http.ResponseWriter, r *http.Request) {
	var o models.Org
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	o.ID = chi.URLParam(r, "id")
	if err := h.repo.UpdateOrg(r.Context(), &o); err != nil {
		writeError(w, http.StatusInternalServerError, "update org failed")
		return
	}
	writeJSON(w, http.StatusOK, o)
}
