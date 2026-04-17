package main

// analytics_handlers.go — Industry-grade analytics endpoints added to the Query API.
//
// New routes (all require JWT via BearerAuth):
//
//   GET  /v1/realtime                      Real-time event rate and active users (last 5 min)
//   POST /v1/paths                         User-journey path / flow analysis
//   GET  /v1/revenue                       Revenue metrics (total, ARPU, ARPPU)
//   POST /v1/ltv                           LTV cohort analysis
//   GET  /v1/users/{user_id}               Individual user profile
//   GET  /v1/users/{user_id}/events        User event timeline
//   GET  /v1/attribution                   UTM source / medium / campaign attribution
//   POST /v1/events/breakdown              Break down event counts by property
//   POST /v1/segments/count                Evaluate dynamic segment and return user count
//   GET  /v1/experiments/{id}/results      A/B experiment results with statistical lift
//
// These endpoints complement Amplitude, Mixpanel, CleverTap, and Segment feature sets.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/pulse-analytics/internal/querying"
)

// mountAnalyticsRoutes registers all industry analytics routes on the given router.
// Call this from main.go inside the /v1 route group.
func (h *queryHandler) mountAnalyticsRoutes(r chi.Router) {
	r.Get("/realtime", h.realtime)
	r.Post("/paths", h.paths)
	r.Get("/revenue", h.revenue)
	r.Post("/ltv", h.ltv)
	r.Get("/attribution", h.attribution)
	r.Post("/events/breakdown", h.eventBreakdown)
	r.Post("/segments/count", h.segmentCount)
	r.Get("/experiments/{id}/results", h.experimentResults)
	r.Get("/users/{user_id}", h.userProfile)
	r.Get("/users/{user_id}/events", h.userTimeline)
}

// realtime godoc
// @Summary     Real-time analytics (last 5 minutes)
// @Description Returns live event rate, active user count, and top events for the last 5 minutes.
//
//	Comparable to Amplitude's Real-Time Dashboard.
//
// @Tags        analytics
// @Produce     json
// @Param       app_id  query    string  true   "Application ID"
// @Success     200     {object} querying.RealTimeStats
// @Failure     400     {object} map[string]string
// @Security    BearerAuth
// @Router      /v1/realtime [get]
func (h *queryHandler) realtime(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}

	stats, err := h.querySvc.RealTime(r.Context(), appID)
	if err != nil {
		h.log.Error("realtime query", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// paths godoc
// @Summary     User-journey path analysis
// @Description Identifies the top-N most common event sequences from a starting event.
//
//	Comparable to Mixpanel Flows and Amplitude Pathfinder.
//
// @Tags        analytics
// @Accept      json
// @Produce     json
// @Param       body  body      querying.PathRequest  true  "Path query parameters"
// @Success     200   {object}  querying.PathResponse
// @Failure     400   {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/paths [post]
func (h *queryHandler) paths(w http.ResponseWriter, r *http.Request) {
	var req querying.PathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AppID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}
	if req.FromMs == 0 {
		req.FromMs = defaultFromMs()
	}
	if req.ToMs == 0 {
		req.ToMs = time.Now().UnixMilli()
	}

	resp, err := h.querySvc.Paths(r.Context(), req)
	if err != nil {
		h.log.Error("paths query", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// revenue godoc
// @Summary     Revenue analytics (ARPU, ARPPU, total)
// @Description Returns per-bucket revenue totals, transaction counts, and paying-user metrics.
//
//	Comparable to Amplitude Revenue LTV and Mixpanel Revenue Report.
//
// @Tags        analytics
// @Produce     json
// @Param       app_id         query  string   true   "Application ID"
// @Param       from_ms        query  integer  false  "Start epoch ms"
// @Param       to_ms          query  integer  false  "End epoch ms"
// @Param       granularity    query  string   false  "day | week | month"
// @Param       revenue_event  query  string   false  "Event name for purchase (default: purchase)"
// @Param       revenue_prop   query  string   false  "Props key for amount (default: revenue)"
// @Success     200  {object}  querying.RevenueResponse
// @Failure     400  {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/revenue [get]
func (h *queryHandler) revenue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	appID := q.Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}

	req := querying.RevenueRequest{
		AppID:        appID,
		FromMs:       parseMillis(q.Get("from_ms"), defaultFromMs()),
		ToMs:         parseMillis(q.Get("to_ms"), time.Now().UnixMilli()),
		Granularity:  defaultStr(q.Get("granularity"), "day"),
		RevenueEvent: defaultStr(q.Get("revenue_event"), "purchase"),
		RevenueProp:  defaultStr(q.Get("revenue_prop"), "revenue"),
	}

	resp, err := h.querySvc.Revenue(r.Context(), req)
	if err != nil {
		h.log.Error("revenue query", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// ltv godoc
// @Summary     LTV cohort analysis
// @Description Computes cumulative average LTV per install cohort at day-N intervals.
//
//	Comparable to Amplitude's LTV chart and AppsFlyer LTV cohort.
//
// @Tags        analytics
// @Accept      json
// @Produce     json
// @Param       body  body      querying.LTVCohortRequest  true  "LTV cohort parameters"
// @Success     200   {array}   querying.LTVCohortPoint
// @Failure     400   {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/ltv [post]
func (h *queryHandler) ltv(w http.ResponseWriter, r *http.Request) {
	var req querying.LTVCohortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AppID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}
	if req.FromMs == 0 {
		req.FromMs = defaultFromMs()
	}
	if req.ToMs == 0 {
		req.ToMs = time.Now().UnixMilli()
	}

	resp, err := h.querySvc.LTVCohort(r.Context(), req)
	if err != nil {
		h.log.Error("ltv cohort query", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// userProfile godoc
// @Summary     User profile
// @Description Returns aggregate behavioural stats for a single identified user.
//
//	Comparable to Mixpanel User Profiles and Amplitude User Look-Up.
//
// @Tags        analytics
// @Produce     json
// @Param       user_id  path     string  true  "User ID"
// @Param       app_id   query    string  true  "Application ID"
// @Success     200      {object} querying.UserProfileResponse
// @Failure     400      {object} map[string]string
// @Security    BearerAuth
// @Router      /v1/users/{user_id} [get]
func (h *queryHandler) userProfile(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	appID := r.URL.Query().Get("app_id")
	if appID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, "app_id and user_id required")
		return
	}

	profile, err := h.querySvc.UserProfile(r.Context(), appID, userID)
	if err != nil {
		h.log.Error("user profile", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

// userTimeline godoc
// @Summary     User event timeline
// @Description Returns the most recent events for a single user (max 500).
//
//	Comparable to Mixpanel's per-user Activity Feed.
//
// @Tags        analytics
// @Produce     json
// @Param       user_id  path     string   true   "User ID"
// @Param       app_id   query    string   true   "Application ID"
// @Param       from_ms  query    integer  false  "Start epoch ms"
// @Param       to_ms    query    integer  false  "End epoch ms"
// @Param       limit    query    integer  false  "Max events to return (default 500)"
// @Success     200      {array}  querying.UserEvent
// @Failure     400      {object} map[string]string
// @Security    BearerAuth
// @Router      /v1/users/{user_id}/events [get]
func (h *queryHandler) userTimeline(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	q := r.URL.Query()
	appID := q.Get("app_id")
	if appID == "" || userID == "" {
		writeError(w, http.StatusBadRequest, "app_id and user_id required")
		return
	}

	limit := 500
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	events, err := h.querySvc.UserTimeline(r.Context(), appID, userID,
		parseMillis(q.Get("from_ms"), defaultFromMs()),
		parseMillis(q.Get("to_ms"), time.Now().UnixMilli()),
		limit,
	)
	if err != nil {
		h.log.Error("user timeline", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// attribution godoc
// @Summary     UTM attribution analysis
// @Description Returns conversion and revenue attributed to each UTM source/medium/campaign.
//
//	Comparable to Amplitude Attribution and Mixpanel Attribution add-on.
//
// @Tags        analytics
// @Produce     json
// @Param       app_id       query  string  true   "Application ID"
// @Param       from_ms      query  integer false  "Start epoch ms"
// @Param       to_ms        query  integer false  "End epoch ms"
// @Param       model        query  string  false  "Attribution model: first_touch (default) | last_touch | linear"
// @Param       granularity  query  string  false  "day | week | month"
// @Success     200  {array}  querying.AttributionPoint
// @Failure     400  {object} map[string]string
// @Security    BearerAuth
// @Router      /v1/attribution [get]
func (h *queryHandler) attribution(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	appID := q.Get("app_id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}

	req := querying.AttributionRequest{
		AppID:       appID,
		FromMs:      parseMillis(q.Get("from_ms"), defaultFromMs()),
		ToMs:        parseMillis(q.Get("to_ms"), time.Now().UnixMilli()),
		Model:       defaultStr(q.Get("model"), "first_touch"),
		Granularity: defaultStr(q.Get("granularity"), "day"),
	}

	points, err := h.querySvc.Attribution(r.Context(), req)
	if err != nil {
		h.log.Error("attribution query", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// eventBreakdown godoc
// @Summary     Event property breakdown
// @Description Breaks down event counts by a specified property value over time.
//
//	Comparable to Amplitude's "Group By" and Mixpanel's Property Breakdown.
//
// @Tags        analytics
// @Accept      json
// @Produce     json
// @Param       body  body      querying.BreakdownRequest  true  "Breakdown parameters"
// @Success     200   {array}   querying.BreakdownPoint
// @Failure     400   {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/events/breakdown [post]
func (h *queryHandler) eventBreakdown(w http.ResponseWriter, r *http.Request) {
	var req querying.BreakdownRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AppID == "" || req.EventName == "" || req.Property == "" {
		writeError(w, http.StatusBadRequest, "app_id, event_name, and property required")
		return
	}
	if req.FromMs == 0 {
		req.FromMs = defaultFromMs()
	}
	if req.ToMs == 0 {
		req.ToMs = time.Now().UnixMilli()
	}

	points, err := h.querySvc.EventBreakdown(r.Context(), req)
	if err != nil {
		h.log.Error("event breakdown", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, points)
}

// segmentCount godoc
// @Summary     Evaluate a dynamic user segment
// @Description Counts users matching a combination of event and property filters.
//
//	Comparable to Amplitude Cohort Builder and CleverTap Segments.
//
// @Tags        analytics
// @Accept      json
// @Produce     json
// @Param       body  body      querying.SegmentCountRequest   true  "Segment definition"
// @Success     200   {object}  querying.SegmentCountResponse
// @Failure     400   {object}  map[string]string
// @Security    BearerAuth
// @Router      /v1/segments/count [post]
func (h *queryHandler) segmentCount(w http.ResponseWriter, r *http.Request) {
	var req querying.SegmentCountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.AppID == "" {
		writeError(w, http.StatusBadRequest, "app_id required")
		return
	}
	if req.FromMs == 0 {
		req.FromMs = defaultFromMs()
	}
	if req.ToMs == 0 {
		req.ToMs = time.Now().UnixMilli()
	}

	resp, err := h.querySvc.SegmentCount(r.Context(), req)
	if err != nil {
		h.log.Error("segment count", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// experimentResults godoc
// @Summary     A/B experiment results
// @Description Returns per-variant conversion rates and statistical lift vs control.
//
//	Comparable to Amplitude Experiment and Optimizely Results.
//
// @Tags        analytics
// @Produce     json
// @Param       id          path     string   true   "Experiment ID"
// @Param       app_id      query    string   true   "Application ID"
// @Param       goal_event  query    string   true   "Conversion event name"
// @Param       from_ms     query    integer  false  "Start epoch ms"
// @Param       to_ms       query    integer  false  "End epoch ms"
// @Success     200  {object} querying.ExperimentResult
// @Failure     400  {object} map[string]string
// @Security    BearerAuth
// @Router      /v1/experiments/{id}/results [get]
func (h *queryHandler) experimentResults(w http.ResponseWriter, r *http.Request) {
	experimentID := chi.URLParam(r, "id")
	q := r.URL.Query()
	appID := q.Get("app_id")
	goalEvent := q.Get("goal_event")
	if appID == "" || goalEvent == "" {
		writeError(w, http.StatusBadRequest, "app_id and goal_event required")
		return
	}

	result, err := h.querySvc.ExperimentResults(r.Context(),
		experimentID, appID, goalEvent,
		parseMillis(q.Get("from_ms"), defaultFromMs()),
		parseMillis(q.Get("to_ms"), time.Now().UnixMilli()),
	)
	if err != nil {
		h.log.Error("experiment results", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
