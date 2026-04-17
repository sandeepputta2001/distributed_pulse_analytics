package main

// mgmt_handlers.go — CRUD management endpoints mounted on the Query API.
//
// Routes (all require JWT BearerAuth except /v1/auth/login):
//
//	POST   /v1/auth/login               Exchange API key → JWT
//
//	GET    /v1/apps                     List all apps
//	POST   /v1/apps                     Create a new application
//	PUT    /v1/apps/{id}                Update app settings
//	DELETE /v1/apps/{id}                Deactivate an application
//
//	GET    /v1/alerts                   List alert rules for app_id
//	POST   /v1/alerts                   Create an alert rule
//	PUT    /v1/alerts/{id}              Update an alert rule
//	DELETE /v1/alerts/{id}              Delete an alert rule
//
//	GET    /v1/cohorts                  List cohort definitions for app_id
//	POST   /v1/cohorts                  Create a cohort definition
//	DELETE /v1/cohorts/{id}             Delete a cohort definition
//
//	GET    /v1/experiments              List experiments for app_id
//	POST   /v1/experiments              Create an experiment
//	PUT    /v1/experiments/{id}         Update experiment
//	DELETE /v1/experiments/{id}         Delete an experiment
//
//	GET    /v1/orgs                     List organisations
//	POST   /v1/orgs                     Create organisation
//	PUT    /v1/orgs/{id}                Update organisation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/auth"
	"github.com/pulse-analytics/internal/models"
)

// mountMgmtRoutes registers all management routes on the /v1 sub-router.
// /v1/auth/login is registered separately in main.go as a public route.
func (h *queryHandler) mountMgmtRoutes(r chi.Router) {
	r.Get("/apps", h.listApps)
	r.Post("/apps", h.createApp)
	r.Put("/apps/{id}", h.updateApp)
	r.Delete("/apps/{id}", h.deleteApp)
	r.Post("/apps/{id}/rotate-key", h.rotateAPIKey)

	r.Get("/alerts", h.listAlerts)
	r.Post("/alerts", h.createAlert)
	r.Put("/alerts/{id}", h.updateAlert)
	r.Delete("/alerts/{id}", h.deleteAlert)

	r.Get("/cohorts", h.listCohorts)
	r.Post("/cohorts", h.createCohort)
	r.Delete("/cohorts/{id}", h.deleteCohort)

	r.Get("/experiments", h.listExperiments)
	r.Post("/experiments", h.createExperiment)
	r.Put("/experiments/{id}", h.updateExperiment)
	r.Delete("/experiments/{id}", h.deleteExperiment)

	r.Get("/orgs", h.getOrg)
	r.Post("/orgs", h.createOrg)
	r.Put("/orgs/{id}", h.updateOrg)
}

// ─── Auth ──────────────────────────────────────────────────────────────────────

// authLogin godoc
// @Summary     Exchange API key for JWT
// @Description Validates an API key and returns a signed JWT for use with all authenticated endpoints.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      object  true  "{ \"api_key\": \"pk_live_...\" }"
// @Success     200   {object}  map[string]string
// @Failure     400   {object}  map[string]string
// @Failure     401   {object}  map[string]string
// @Router      /v1/auth/login [post]
func (h *queryHandler) authLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "api_key required")
		return
	}

	app, err := h.pg.GetAppByAPIKey(r.Context(), req.APIKey)
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
		"app_id":  app.ID,
		"org_id":  app.OrgID,
		"expires": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
	})
}

// ─── Apps ──────────────────────────────────────────────────────────────────────

// listApps godoc
// @Summary     List applications
// @Tags        apps
// @Produce     json
// @Success     200  {array}  models.App
// @Security    BearerAuth
// @Router      /v1/apps [get]
func (h *queryHandler) listApps(w http.ResponseWriter, r *http.Request) {
	apps, err := h.pg.ListApps(r.Context())
	if err != nil {
		h.log.Error("list apps", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to list apps")
		return
	}
	if apps == nil {
		apps = []*models.App{}
	}
	writeJSON(w, http.StatusOK, apps)
}

// createApp godoc
// @Summary     Create application
// @Tags        apps
// @Accept      json
// @Produce     json
// @Param       body  body      object  true  "App definition"
// @Success     201   {object}  models.App
// @Security    BearerAuth
// @Router      /v1/apps [post]
func (h *queryHandler) createApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID string `json:"org_id"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	app := &models.App{
		ID:        models.NewEventID(),
		OrgID:     req.OrgID,
		Name:      req.Name,
		APIKey:    fmt.Sprintf("pk_live_%s", models.NewEventID()[:20]),
		RPS:       10000,
		Burst:     50000,
		Active:    true,
		CreatedAt: time.Now(),
	}
	if err := h.pg.CreateApp(r.Context(), app); err != nil {
		h.log.Error("create app", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create app")
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

// updateApp godoc
// @Summary     Update application settings
// @Tags        apps
// @Accept      json
// @Produce     json
// @Param       id    path      string  true  "Application ID"
// @Param       body  body      object  true  "Fields to update"
// @Success     200   {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/apps/{id} [put]
func (h *queryHandler) updateApp(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Name  string  `json:"name"`
		RPS   float64 `json:"rps"`
		Burst int     `json:"burst"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.pg.UpdateApp(r.Context(), id, req.Name, req.RPS, req.Burst); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "updated"})
}

// deleteApp godoc
// @Summary     Deactivate application
// @Tags        apps
// @Param       id  path  string  true  "Application ID"
// @Success     204
// @Security    BearerAuth
// @Router      /v1/apps/{id} [delete]
func (h *queryHandler) deleteApp(w http.ResponseWriter, r *http.Request) {
	if err := h.pg.DeactivateApp(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "deactivate failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *queryHandler) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	newKey := "pk_live_" + models.NewEventID()[:32]
	if err := h.pg.RotateAPIKey(r.Context(), id, newKey); err != nil {
		writeError(w, http.StatusInternalServerError, "rotation failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"app_id": id, "api_key": newKey})
}

// ─── Alerts ────────────────────────────────────────────────────────────────────

// listAlerts godoc
// @Summary     List alert rules
// @Tags        alerts
// @Produce     json
// @Param       app_id  query  string  true  "Application ID"
// @Success     200  {array}  models.AlertRule
// @Security    BearerAuth
// @Router      /v1/alerts [get]
func (h *queryHandler) listAlerts(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}
	rules, err := h.pg.ListAlertRules(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list alerts")
		return
	}
	if rules == nil {
		rules = []*models.AlertRule{}
	}
	writeJSON(w, http.StatusOK, rules)
}

// createAlert godoc
// @Summary     Create alert rule
// @Tags        alerts
// @Accept      json
// @Produce     json
// @Param       body  body      models.AlertRule  true  "Alert rule definition"
// @Success     201   {object}  models.AlertRule
// @Security    BearerAuth
// @Router      /v1/alerts [post]
func (h *queryHandler) createAlert(w http.ResponseWriter, r *http.Request) {
	var rule models.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if rule.AppID == "" || rule.Name == "" || rule.MetricName == "" {
		writeError(w, http.StatusBadRequest, "app_id, name, metric_name required")
		return
	}
	rule.ID = models.NewEventID()
	rule.Active = true
	rule.CreatedAt = time.Now()
	if err := h.pg.CreateAlertRule(r.Context(), &rule); err != nil {
		h.log.Error("create alert", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create alert")
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// updateAlert godoc
// @Summary     Update alert rule
// @Tags        alerts
// @Accept      json
// @Produce     json
// @Param       id    path      string  true  "Alert rule ID"
// @Param       body  body      models.AlertRule  true  "Updated fields"
// @Success     200   {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/alerts/{id} [put]
func (h *queryHandler) updateAlert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var rule models.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rule.ID = id
	if err := h.pg.UpdateAlertRule(r.Context(), &rule); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "updated"})
}

// deleteAlert godoc
// @Summary     Delete alert rule
// @Tags        alerts
// @Param       id  path  string  true  "Alert rule ID"
// @Success     204
// @Security    BearerAuth
// @Router      /v1/alerts/{id} [delete]
func (h *queryHandler) deleteAlert(w http.ResponseWriter, r *http.Request) {
	if err := h.pg.DeleteAlertRule(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Cohorts ───────────────────────────────────────────────────────────────────

// listCohorts godoc
// @Summary     List cohort definitions
// @Tags        cohorts
// @Produce     json
// @Param       app_id  query  string  true  "Application ID"
// @Success     200  {array}  models.CohortDefinition
// @Security    BearerAuth
// @Router      /v1/cohorts [get]
func (h *queryHandler) listCohorts(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}
	cohorts, err := h.pg.ListCohorts(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list cohorts")
		return
	}
	if cohorts == nil {
		cohorts = []*models.CohortDefinition{}
	}
	writeJSON(w, http.StatusOK, cohorts)
}

// createCohort godoc
// @Summary     Create cohort definition
// @Tags        cohorts
// @Accept      json
// @Produce     json
// @Param       body  body      models.CohortDefinition  true  "Cohort definition"
// @Success     201   {object}  models.CohortDefinition
// @Security    BearerAuth
// @Router      /v1/cohorts [post]
func (h *queryHandler) createCohort(w http.ResponseWriter, r *http.Request) {
	var cohort models.CohortDefinition
	if err := json.NewDecoder(r.Body).Decode(&cohort); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if cohort.AppID == "" || cohort.Name == "" {
		writeError(w, http.StatusBadRequest, "app_id and name required")
		return
	}
	cohort.ID = models.NewEventID()
	cohort.CreatedAt = time.Now()
	if err := h.pg.CreateCohort(r.Context(), &cohort); err != nil {
		h.log.Error("create cohort", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create cohort")
		return
	}
	writeJSON(w, http.StatusCreated, cohort)
}

// deleteCohort godoc
// @Summary     Delete cohort definition
// @Tags        cohorts
// @Param       id  path  string  true  "Cohort ID"
// @Success     204
// @Security    BearerAuth
// @Router      /v1/cohorts/{id} [delete]
func (h *queryHandler) deleteCohort(w http.ResponseWriter, r *http.Request) {
	if err := h.pg.DeleteCohort(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Experiments ───────────────────────────────────────────────────────────────

// listExperiments godoc
// @Summary     List A/B experiments
// @Tags        experiments
// @Produce     json
// @Param       app_id  query  string  true  "Application ID"
// @Success     200  {array}  models.Experiment
// @Security    BearerAuth
// @Router      /v1/experiments [get]
func (h *queryHandler) listExperiments(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}
	experiments, err := h.pg.ListExperiments(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list experiments")
		return
	}
	if experiments == nil {
		experiments = []*models.Experiment{}
	}
	writeJSON(w, http.StatusOK, experiments)
}

// createExperiment godoc
// @Summary     Create A/B experiment
// @Tags        experiments
// @Accept      json
// @Produce     json
// @Param       body  body      models.Experiment  true  "Experiment definition"
// @Success     201   {object}  models.Experiment
// @Security    BearerAuth
// @Router      /v1/experiments [post]
func (h *queryHandler) createExperiment(w http.ResponseWriter, r *http.Request) {
	var exp models.Experiment
	if err := json.NewDecoder(r.Body).Decode(&exp); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if exp.AppID == "" || exp.Name == "" {
		writeError(w, http.StatusBadRequest, "app_id and name required")
		return
	}
	exp.ID = models.NewEventID()
	exp.Status = "draft"
	exp.CreatedAt = time.Now()
	if err := h.pg.CreateExperiment(r.Context(), &exp); err != nil {
		h.log.Error("create experiment", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create experiment")
		return
	}
	writeJSON(w, http.StatusCreated, exp)
}

// updateExperiment godoc
// @Summary     Update experiment
// @Tags        experiments
// @Accept      json
// @Produce     json
// @Param       id    path      string  true  "Experiment ID"
// @Param       body  body      models.Experiment  true  "Updated fields"
// @Success     200   {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/experiments/{id} [put]
func (h *queryHandler) updateExperiment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var exp models.Experiment
	if err := json.NewDecoder(r.Body).Decode(&exp); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	exp.ID = id
	if err := h.pg.UpdateExperiment(r.Context(), &exp); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "updated"})
}

// deleteExperiment godoc
// @Summary     Delete experiment
// @Tags        experiments
// @Param       id  path  string  true  "Experiment ID"
// @Success     204
// @Security    BearerAuth
// @Router      /v1/experiments/{id} [delete]
func (h *queryHandler) deleteExperiment(w http.ResponseWriter, r *http.Request) {
	if err := h.pg.DeleteExperiment(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Orgs ──────────────────────────────────────────────────────────────────────

// getOrg godoc
// @Summary     List organisations
// @Tags        orgs
// @Produce     json
// @Success     200  {array}  models.Org
// @Security    BearerAuth
// @Router      /v1/orgs [get]
func (h *queryHandler) getOrg(w http.ResponseWriter, r *http.Request) {
	orgs, err := h.pg.ListOrgs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list orgs")
		return
	}
	if orgs == nil {
		orgs = []*models.Org{}
	}
	writeJSON(w, http.StatusOK, orgs)
}

// createOrg godoc
// @Summary     Create organisation
// @Tags        orgs
// @Accept      json
// @Produce     json
// @Param       body  body      models.Org  true  "Organisation definition"
// @Success     201   {object}  models.Org
// @Security    BearerAuth
// @Router      /v1/orgs [post]
func (h *queryHandler) createOrg(w http.ResponseWriter, r *http.Request) {
	var org models.Org
	if err := json.NewDecoder(r.Body).Decode(&org); err != nil || org.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	org.ID = models.NewEventID()
	org.Plan = "free"
	org.CreatedAt = time.Now()
	if err := h.pg.CreateOrg(r.Context(), &org); err != nil {
		h.log.Error("create org", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to create org")
		return
	}
	writeJSON(w, http.StatusCreated, org)
}

// updateOrg godoc
// @Summary     Update organisation
// @Tags        orgs
// @Accept      json
// @Produce     json
// @Param       id    path      string  true  "Organisation ID"
// @Param       body  body      models.Org  true  "Updated fields"
// @Success     200   {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/orgs/{id} [put]
func (h *queryHandler) updateOrg(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var org models.Org
	if err := json.NewDecoder(r.Body).Decode(&org); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	org.ID = id
	if err := h.pg.UpdateOrg(r.Context(), &org); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "updated"})
}

// authSvcCheck is a compile-time assertion that auth.Service is wired correctly.
var _ *auth.Service = (*auth.Service)(nil)
