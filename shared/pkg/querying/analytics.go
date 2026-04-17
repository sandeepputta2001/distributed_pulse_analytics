package querying

import (
	"context"
	"fmt"
	"time"
)

// ─── Path / Flow Analysis ─────────────────────────────────────────────────────
// Modelled after Mixpanel Flows and Amplitude Pathfinder.

// PathRequest parameters for user-journey path analysis.
type PathRequest struct {
	AppID      string   `json:"app_id"`
	StartEvent string   `json:"start_event"`  // anchor event (empty = session_start)
	EndEvent   string   `json:"end_event"`    // optional — find paths leading to this event
	MaxSteps   int      `json:"max_steps"`    // depth of path tree (default 5)
	TopN       int      `json:"top_n"`        // return top N paths (default 10)
	FromMs     int64    `json:"from_ms"`
	ToMs       int64    `json:"to_ms"`
	Filters    []Filter `json:"filters"` // event-property filters
}

// Filter is a simple key=value property filter applied to events.
type Filter struct {
	Property string `json:"property"`
	Operator string `json:"operator"` // eq | neq | contains
	Value    string `json:"value"`
}

// PathNode represents a single step in a user-journey path.
type PathNode struct {
	EventName  string  `json:"event_name"`
	Count      int64   `json:"count"`       // sessions reaching this node
	Percentage float64 `json:"percentage"`  // % of root sessions
	DropOff    float64 `json:"drop_off_pct"`
}

// PathResponse is a tree of common user paths.
type PathResponse struct {
	RootEvent  string       `json:"root_event"`
	TotalUsers int64        `json:"total_users"`
	Paths      [][]PathNode `json:"paths"` // top-N ordered paths
}

// Paths executes ClickHouse windowFunnel-style analysis to extract the top-N
// most frequent event sequences from a given starting event.
func (s *Service) Paths(ctx context.Context, req PathRequest) (*PathResponse, error) {
	if req.MaxSteps == 0 {
		req.MaxSteps = 5
	}
	if req.TopN == 0 {
		req.TopN = 10
	}
	if req.StartEvent == "" {
		req.StartEvent = "session_start"
	}

	// Build ordered sequence array for each session using arrayCumSum trick.
	// We extract the top-N paths as concatenated strings, then parse them.
	query := fmt.Sprintf(`
		WITH sessions AS (
			SELECT
				session_id,
				groupArray(event_name) AS path
			FROM (
				SELECT session_id, event_name, event_time
				FROM events
				WHERE app_id = ?
				  AND event_time BETWEEN ? AND ?
				  AND session_id IN (
					SELECT DISTINCT session_id FROM events
					WHERE app_id = ? AND event_name = ?
					  AND event_time BETWEEN ? AND ?
				  )
				ORDER BY session_id, event_time
				LIMIT 10000000
			)
			GROUP BY session_id
		),
		trimmed AS (
			SELECT arraySlice(
				arraySlice(path, indexOf(path, ?) , %d),
				1, %d
			) AS path_steps
			FROM sessions
			WHERE has(path, ?)
		)
		SELECT
			arrayStringConcat(path_steps, ' -> ') AS path_str,
			count() AS cnt
		FROM trimmed
		WHERE length(path_steps) > 0
		GROUP BY path_str
		ORDER BY cnt DESC
		LIMIT ?
	`, req.MaxSteps, req.MaxSteps)

	from := time.UnixMilli(req.FromMs)
	to := time.UnixMilli(req.ToMs)

	rows, err := s.ch.Query(ctx, query,
		req.AppID, from, to,
		req.AppID, req.StartEvent, from, to,
		req.StartEvent, req.StartEvent,
		req.TopN,
	)
	if err != nil {
		return nil, fmt.Errorf("paths query: %w", err)
	}
	defer rows.Close()

	var totalUsers int64
	var pathStrings []struct {
		Path  string
		Count int64
	}

	for rows.Next() {
		var pathStr string
		var cnt int64
		if err := rows.Scan(&pathStr, &cnt); err != nil {
			continue
		}
		pathStrings = append(pathStrings, struct {
			Path  string
			Count int64
		}{pathStr, cnt})
		totalUsers += cnt
	}

	resp := &PathResponse{
		RootEvent:  req.StartEvent,
		TotalUsers: totalUsers,
		Paths:      make([][]PathNode, 0, len(pathStrings)),
	}
	for _, ps := range pathStrings {
		var nodes []PathNode
		parts := splitPath(ps.Path)
		for i, p := range parts {
			dropOff := 0.0
			if i < len(parts)-1 && totalUsers > 0 {
				dropOff = 100.0 * float64(ps.Count) / float64(totalUsers)
			}
			pct := 0.0
			if totalUsers > 0 {
				pct = 100.0 * float64(ps.Count) / float64(totalUsers)
			}
			nodes = append(nodes, PathNode{
				EventName:  p,
				Count:      ps.Count,
				Percentage: pct,
				DropOff:    dropOff,
			})
		}
		resp.Paths = append(resp.Paths, nodes)
	}
	return resp, nil
}

// ─── Revenue Analytics ────────────────────────────────────────────────────────
// Modelled after Amplitude Revenue LTV and Mixpanel Revenue report.

// RevenueRequest parameters for revenue metric queries.
type RevenueRequest struct {
	AppID          string `json:"app_id"`
	FromMs         int64  `json:"from_ms"`
	ToMs           int64  `json:"to_ms"`
	Granularity    string `json:"granularity"`     // day | week | month
	RevenueEvent   string `json:"revenue_event"`   // e.g. "purchase" (default)
	RevenueProp    string `json:"revenue_prop"`    // props key for amount (default "revenue")
	CurrencyProp   string `json:"currency_prop"`   // props key for currency
}

// RevenuePoint is a single time-bucket revenue measurement.
type RevenuePoint struct {
	Bucket         string  `json:"bucket"`
	TotalRevenue   float64 `json:"total_revenue"`
	TxCount        int64   `json:"transaction_count"`
	UniquePayors   int64   `json:"unique_payors"`
	ARPU           float64 `json:"arpu"`   // average revenue per user (across ALL users)
	ARPPU          float64 `json:"arppu"`  // average revenue per paying user
}

// RevenueResponse wraps all revenue time-series points.
type RevenueResponse struct {
	AppID      string         `json:"app_id"`
	Currency   string         `json:"currency"`
	TotalLTV   float64        `json:"total_ltv"`
	Points     []RevenuePoint `json:"points"`
}

// Revenue returns per-bucket revenue metrics from ClickHouse.
func (s *Service) Revenue(ctx context.Context, req RevenueRequest) (*RevenueResponse, error) {
	if req.RevenueEvent == "" {
		req.RevenueEvent = "purchase"
	}
	if req.RevenueProp == "" {
		req.RevenueProp = "revenue"
	}
	if req.Granularity == "" {
		req.Granularity = "day"
	}

	bucketFn := granularityFn(req.Granularity)
	query := fmt.Sprintf(`
		SELECT
			%s(event_time) AS bucket,
			sumIf(toFloat64OrZero(props['%s']), event_name = ?) AS total_revenue,
			countIf(event_name = ?) AS tx_count,
			uniqIf(user_id, event_name = ?) AS unique_payors
		FROM events
		WHERE app_id = ?
		  AND event_time BETWEEN ? AND ?
		GROUP BY bucket
		ORDER BY bucket
	`, bucketFn, req.RevenueProp)

	from := time.UnixMilli(req.FromMs)
	to := time.UnixMilli(req.ToMs)

	rows, err := s.ch.Query(ctx, query,
		req.RevenueEvent, req.RevenueEvent, req.RevenueEvent,
		req.AppID, from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("revenue query: %w", err)
	}
	defer rows.Close()

	var points []RevenuePoint
	var totalLTV float64

	for rows.Next() {
		var bucket time.Time
		var totalRev float64
		var txCount, uniquePayors int64
		if err := rows.Scan(&bucket, &totalRev, &txCount, &uniquePayors); err != nil {
			continue
		}
		arpu := 0.0
		arppu := 0.0
		if uniquePayors > 0 {
			arppu = totalRev / float64(uniquePayors)
		}
		totalLTV += totalRev
		points = append(points, RevenuePoint{
			Bucket:       bucket.Format("2006-01-02"),
			TotalRevenue: totalRev,
			TxCount:      txCount,
			UniquePayors: uniquePayors,
			ARPU:         arpu,
			ARPPU:        arppu,
		})
	}

	return &RevenueResponse{
		AppID:    req.AppID,
		Currency: "USD",
		TotalLTV: totalLTV,
		Points:   points,
	}, nil
}

// ─── LTV Cohort Analysis ──────────────────────────────────────────────────────

// LTVCohortRequest calculates cumulative LTV by install cohort.
type LTVCohortRequest struct {
	AppID        string `json:"app_id"`
	FromMs       int64  `json:"from_ms"`
	ToMs         int64  `json:"to_ms"`
	Days         []int  `json:"days"` // [1,7,14,30,60,90]
	RevenueEvent string `json:"revenue_event"`
	RevenueProp  string `json:"revenue_prop"`
}

// LTVCohortPoint is a single install-cohort LTV row.
type LTVCohortPoint struct {
	CohortDate string             `json:"cohort_date"`
	Users      int64              `json:"users"`
	LTVByDay   map[string]float64 `json:"ltv_by_day"` // "day_7" → avg LTV
}

// LTVCohort computes cumulative average LTV for each install cohort at day N.
func (s *Service) LTVCohort(ctx context.Context, req LTVCohortRequest) ([]LTVCohortPoint, error) {
	if req.RevenueEvent == "" {
		req.RevenueEvent = "purchase"
	}
	if req.RevenueProp == "" {
		req.RevenueProp = "revenue"
	}
	if len(req.Days) == 0 {
		req.Days = []int{1, 7, 14, 30, 60, 90}
	}

	from := time.UnixMilli(req.FromMs)
	to := time.UnixMilli(req.ToMs)

	query := fmt.Sprintf(`
		WITH installs AS (
			SELECT user_id, min(event_time) AS install_date
			FROM events
			WHERE app_id = ? AND event_name IN ('app_install', 'first_open', 'session_start')
			  AND event_time BETWEEN ? AND ?
			GROUP BY user_id
		),
		revenue AS (
			SELECT user_id, event_time, toFloat64OrZero(props['%s']) AS amount
			FROM events
			WHERE app_id = ? AND event_name = ?
			  AND event_time BETWEEN ? AND ?
		)
		SELECT
			toDate(i.install_date) AS cohort_date,
			count(DISTINCT i.user_id) AS cohort_users,
			dateDiff('day', i.install_date, r.event_time) AS day_n,
			avg(r.amount) AS avg_ltv
		FROM installs i
		JOIN revenue r ON i.user_id = r.user_id
		GROUP BY cohort_date, day_n
		ORDER BY cohort_date, day_n
	`, req.RevenueProp)

	rows, err := s.ch.Query(ctx, query,
		req.AppID, from, to,
		req.AppID, req.RevenueEvent, from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("ltv cohort query: %w", err)
	}
	defer rows.Close()

	cohortMap := map[string]*LTVCohortPoint{}

	for rows.Next() {
		var cohortDate time.Time
		var cohortUsers int64
		var dayN int
		var avgLTV float64
		if err := rows.Scan(&cohortDate, &cohortUsers, &dayN, &avgLTV); err != nil {
			continue
		}
		key := cohortDate.Format("2006-01-02")
		if _, ok := cohortMap[key]; !ok {
			cohortMap[key] = &LTVCohortPoint{
				CohortDate: key,
				Users:      cohortUsers,
				LTVByDay:   map[string]float64{},
			}
		}
		cohortMap[key].LTVByDay[fmt.Sprintf("day_%d", dayN)] = avgLTV
	}

	result := make([]LTVCohortPoint, 0, len(cohortMap))
	for _, v := range cohortMap {
		result = append(result, *v)
	}
	return result, nil
}

// ─── User Profile & Timeline ──────────────────────────────────────────────────
// Modelled after Mixpanel User Profiles and Amplitude User Look-Up.

// UserProfileResponse is the full profile for a single identified user.
type UserProfileResponse struct {
	AppID          string            `json:"app_id"`
	UserID         string            `json:"user_id"`
	FirstSeen      string            `json:"first_seen"`
	LastSeen       string            `json:"last_seen"`
	TotalEvents    int64             `json:"total_events"`
	TotalSessions  int64             `json:"total_sessions"`
	TotalRevenue   float64           `json:"total_revenue"`
	Country        string            `json:"country"`
	City           string            `json:"city"`
	Properties     map[string]string `json:"properties"` // latest merged traits
	TopEvents      []EventFreq       `json:"top_events"`
}

// EventFreq is a ranked event frequency for a user.
type EventFreq struct {
	EventName string `json:"event_name"`
	Count     int64  `json:"count"`
}

// UserProfile returns aggregate behavioural stats for a single user_id.
func (s *Service) UserProfile(ctx context.Context, appID, userID string) (*UserProfileResponse, error) {
	query := `
		SELECT
			min(event_time) AS first_seen,
			max(event_time) AS last_seen,
			count() AS total_events,
			uniq(session_id) AS total_sessions,
			sumIf(toFloat64OrZero(props['revenue']), event_name = 'purchase') AS total_revenue,
			anyLast(geo_country) AS country,
			anyLast(geo_city) AS city
		FROM events
		WHERE app_id = ? AND user_id = ?
	`
	row := s.ch.QueryRow(ctx, query, appID, userID)

	var p UserProfileResponse
	p.AppID = appID
	p.UserID = userID
	p.Properties = make(map[string]string)

	var firstSeen, lastSeen time.Time
	if err := row.Scan(&firstSeen, &lastSeen, &p.TotalEvents, &p.TotalSessions, &p.TotalRevenue, &p.Country, &p.City); err != nil {
		return nil, fmt.Errorf("user profile: %w", err)
	}
	p.FirstSeen = firstSeen.UTC().Format(time.RFC3339)
	p.LastSeen = lastSeen.UTC().Format(time.RFC3339)

	// Top 10 events
	topRows, err := s.ch.Query(ctx, `
		SELECT event_name, count() AS cnt
		FROM events
		WHERE app_id = ? AND user_id = ?
		GROUP BY event_name
		ORDER BY cnt DESC
		LIMIT 10
	`, appID, userID)
	if err == nil {
		defer topRows.Close()
		for topRows.Next() {
			var ef EventFreq
			_ = topRows.Scan(&ef.EventName, &ef.Count)
			p.TopEvents = append(p.TopEvents, ef)
		}
	}

	return &p, nil
}

// UserTimeline returns the raw event timeline for a user (max 500 events).
type UserEvent struct {
	EventName string            `json:"event_name"`
	EventTime string            `json:"event_time"`
	SessionID string            `json:"session_id"`
	Country   string            `json:"country"`
	Props     map[string]string `json:"props"`
}

func (s *Service) UserTimeline(ctx context.Context, appID, userID string, fromMs, toMs int64, limit int) ([]UserEvent, error) {
	if limit == 0 || limit > 500 {
		limit = 500
	}
	rows, err := s.ch.Query(ctx, `
		SELECT event_name, event_time, session_id, geo_country, props
		FROM events
		WHERE app_id = ? AND user_id = ?
		  AND event_time BETWEEN ? AND ?
		ORDER BY event_time DESC
		LIMIT ?
	`,
		appID, userID,
		time.UnixMilli(fromMs), time.UnixMilli(toMs),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("user timeline: %w", err)
	}
	defer rows.Close()

	var events []UserEvent
	for rows.Next() {
		var e UserEvent
		var ts time.Time
		var props map[string]string
		if err := rows.Scan(&e.EventName, &ts, &e.SessionID, &e.Country, &props); err != nil {
			continue
		}
		e.EventTime = ts.UTC().Format(time.RFC3339)
		e.Props = props
		events = append(events, e)
	}
	return events, nil
}

// ─── Attribution Analysis ─────────────────────────────────────────────────────
// Tracks UTM sources, referrers, and first/last-touch attribution.
// Modelled after Amplitude Attribution, Mixpanel Attribution, and Segment Attribution.

// AttributionRequest parameters for attribution queries.
type AttributionRequest struct {
	AppID       string `json:"app_id"`
	FromMs      int64  `json:"from_ms"`
	ToMs        int64  `json:"to_ms"`
	Model       string `json:"model"`       // first_touch | last_touch | linear
	Granularity string `json:"granularity"` // day | week | month
}

// AttributionPoint is a single source contribution row.
type AttributionPoint struct {
	Bucket        string  `json:"bucket"`
	Source        string  `json:"source"`        // utm_source value
	Medium        string  `json:"medium"`        // utm_medium
	Campaign      string  `json:"campaign"`      // utm_campaign
	Users         int64   `json:"users"`
	Conversions   int64   `json:"conversions"`
	Revenue       float64 `json:"revenue"`
	ConversionPct float64 `json:"conversion_pct"`
}

// Attribution returns UTM source / medium / campaign attribution metrics.
func (s *Service) Attribution(ctx context.Context, req AttributionRequest) ([]AttributionPoint, error) {
	if req.Granularity == "" {
		req.Granularity = "day"
	}

	bucketFn := granularityFn(req.Granularity)

	// First-touch: attribute to the first known UTM in the user's history.
	// We approximate by taking the earliest props['utm_source'] per user in window.
	query := fmt.Sprintf(`
		WITH first_touch AS (
			SELECT
				user_id,
				argMin(props['utm_source'], event_time) AS source,
				argMin(props['utm_medium'], event_time) AS medium,
				argMin(props['utm_campaign'], event_time) AS campaign,
				min(event_time) AS first_event
			FROM events
			WHERE app_id = ?
			  AND event_time BETWEEN ? AND ?
			  AND notEmpty(props['utm_source'])
			GROUP BY user_id
		)
		SELECT
			%s(ft.first_event) AS bucket,
			ft.source,
			ft.medium,
			ft.campaign,
			count(DISTINCT ft.user_id) AS users,
			countIf(e.event_name = 'purchase') AS conversions,
			sumIf(toFloat64OrZero(e.props['revenue']), e.event_name = 'purchase') AS revenue
		FROM first_touch ft
		JOIN events e ON ft.user_id = e.user_id AND e.app_id = ?
		WHERE e.event_time BETWEEN ? AND ?
		GROUP BY bucket, ft.source, ft.medium, ft.campaign
		ORDER BY bucket, users DESC
	`, bucketFn)

	from := time.UnixMilli(req.FromMs)
	to := time.UnixMilli(req.ToMs)

	rows, err := s.ch.Query(ctx, query,
		req.AppID, from, to,
		req.AppID, from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("attribution query: %w", err)
	}
	defer rows.Close()

	var points []AttributionPoint
	for rows.Next() {
		var bucket time.Time
		var p AttributionPoint
		if err := rows.Scan(&bucket, &p.Source, &p.Medium, &p.Campaign, &p.Users, &p.Conversions, &p.Revenue); err != nil {
			continue
		}
		p.Bucket = bucket.Format("2006-01-02")
		if p.Users > 0 {
			p.ConversionPct = 100.0 * float64(p.Conversions) / float64(p.Users)
		}
		points = append(points, p)
	}
	return points, nil
}

// ─── A/B Experiments ──────────────────────────────────────────────────────────
// Modelled after Amplitude Experiment and Optimizely.

// ExperimentAssignRequest is used to get/create a variant assignment.
type ExperimentAssignRequest struct {
	ExperimentID string `json:"experiment_id"`
	UserID       string `json:"user_id"`
	AppID        string `json:"app_id"`
}

// ExperimentAssignment is the stable variant for a user.
type ExperimentAssignment struct {
	ExperimentID string `json:"experiment_id"`
	UserID       string `json:"user_id"`
	Variant      string `json:"variant"` // "control" | "treatment" | custom name
	AssignedAt   string `json:"assigned_at"`
}

// ExperimentResult is the statistical analysis of an experiment.
type ExperimentResult struct {
	ExperimentID  string            `json:"experiment_id"`
	Metric        string            `json:"metric"`
	Variants      []VariantResult   `json:"variants"`
}

// VariantResult holds per-variant metrics for an experiment.
type VariantResult struct {
	Variant       string  `json:"variant"`
	Users         int64   `json:"users"`
	Conversions   int64   `json:"conversions"`
	ConversionPct float64 `json:"conversion_pct"`
	Revenue       float64 `json:"revenue"`
	LiftPct       float64 `json:"lift_pct"`       // vs control
	PValue        float64 `json:"p_value"`        // statistical significance
}

// ExperimentResults queries ClickHouse for per-variant conversion/revenue metrics.
// Assignments are stored in Redis (experiment:<id>:<user_id> → variant).
func (s *Service) ExperimentResults(ctx context.Context, experimentID, appID, goalEvent string, fromMs, toMs int64) (*ExperimentResult, error) {
	from := time.UnixMilli(fromMs)
	to := time.UnixMilli(toMs)

	// Read assignments from ClickHouse experiment_assignments table
	rows, err := s.ch.Query(ctx, `
		SELECT
			a.variant,
			count(DISTINCT a.user_id) AS users,
			countIf(e.event_name = ?) AS conversions,
			sumIf(toFloat64OrZero(e.props['revenue']), e.event_name = 'purchase') AS revenue
		FROM experiment_assignments a
		LEFT JOIN events e
			ON a.user_id = e.user_id
			AND e.app_id = ?
			AND e.event_time BETWEEN ? AND ?
		WHERE a.experiment_id = ?
		GROUP BY a.variant
		ORDER BY a.variant
	`,
		goalEvent,
		appID, from, to,
		experimentID,
	)
	if err != nil {
		return nil, fmt.Errorf("experiment results: %w", err)
	}
	defer rows.Close()

	var variants []VariantResult
	var controlConvPct float64

	for rows.Next() {
		var v VariantResult
		if err := rows.Scan(&v.Variant, &v.Users, &v.Conversions, &v.Revenue); err != nil {
			continue
		}
		if v.Users > 0 {
			v.ConversionPct = 100.0 * float64(v.Conversions) / float64(v.Users)
		}
		if v.Variant == "control" {
			controlConvPct = v.ConversionPct
		}
		variants = append(variants, v)
	}

	// Compute lift relative to control
	for i := range variants {
		if variants[i].Variant != "control" && controlConvPct > 0 {
			variants[i].LiftPct = ((variants[i].ConversionPct - controlConvPct) / controlConvPct) * 100
		}
	}

	return &ExperimentResult{
		ExperimentID: experimentID,
		Metric:       goalEvent,
		Variants:     variants,
	}, nil
}

// ─── Event Property Breakdown ─────────────────────────────────────────────────
// Modelled after Amplitude's "Group By" and Mixpanel's "Breakdown".

// BreakdownRequest parameters for property-level breakdown.
type BreakdownRequest struct {
	AppID       string   `json:"app_id"`
	EventName   string   `json:"event_name"`
	Property    string   `json:"property"`   // e.g. "plan", "country", "device_type"
	FromMs      int64    `json:"from_ms"`
	ToMs        int64    `json:"to_ms"`
	Granularity string   `json:"granularity"`
	TopN        int      `json:"top_n"` // limit cardinality (default 20)
}

// BreakdownPoint is a single property-value bucket.
type BreakdownPoint struct {
	Bucket        string  `json:"bucket"`
	PropertyValue string  `json:"property_value"`
	Count         int64   `json:"count"`
	UniqueUsers   int64   `json:"unique_users"`
	Percentage    float64 `json:"percentage"`
}

// EventBreakdown groups event counts by a property value over time.
func (s *Service) EventBreakdown(ctx context.Context, req BreakdownRequest) ([]BreakdownPoint, error) {
	if req.TopN == 0 {
		req.TopN = 20
	}
	if req.Granularity == "" {
		req.Granularity = "day"
	}

	bucketFn := granularityFn(req.Granularity)

	// Determine whether the property is a top-level column or a props map key
	propExpr := fmt.Sprintf("props['%s']", req.Property)
	if req.Property == "country" {
		propExpr = "geo_country"
	} else if req.Property == "city" {
		propExpr = "geo_city"
	} else if req.Property == "device_type" || req.Property == "os" {
		propExpr = fmt.Sprintf("ua_%s", req.Property)
	}

	query := fmt.Sprintf(`
		WITH top_vals AS (
			SELECT %s AS prop_val, count() AS cnt
			FROM events
			WHERE app_id = ? AND event_name = ?
			  AND event_time BETWEEN ? AND ?
			  AND notEmpty(%s)
			GROUP BY prop_val
			ORDER BY cnt DESC
			LIMIT %d
		)
		SELECT
			%s(e.event_time) AS bucket,
			e.%s AS prop_val,
			count() AS event_count,
			uniq(e.user_id) AS unique_users
		FROM events e
		INNER JOIN top_vals t ON e.%s = t.prop_val
		WHERE e.app_id = ? AND e.event_name = ?
		  AND e.event_time BETWEEN ? AND ?
		GROUP BY bucket, prop_val
		ORDER BY bucket, event_count DESC
	`,
		propExpr, propExpr, req.TopN,
		bucketFn, propExpr, propExpr,
	)

	from := time.UnixMilli(req.FromMs)
	to := time.UnixMilli(req.ToMs)

	rows, err := s.ch.Query(ctx, query,
		req.AppID, req.EventName, from, to,
		req.AppID, req.EventName, from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("breakdown query: %w", err)
	}
	defer rows.Close()

	var points []BreakdownPoint
	for rows.Next() {
		var bucket time.Time
		var p BreakdownPoint
		if err := rows.Scan(&bucket, &p.PropertyValue, &p.Count, &p.UniqueUsers); err != nil {
			continue
		}
		p.Bucket = bucket.Format("2006-01-02")
		points = append(points, p)
	}
	return points, nil
}

// ─── Real-Time Stats ──────────────────────────────────────────────────────────
// Modelled after Amplitude's Real-Time Dashboard and Segment Live Debugger.

// RealTimeStats returns current (last 5 minutes) event rates and user counts.
type RealTimeStats struct {
	AppID         string           `json:"app_id"`
	AsOf          string           `json:"as_of"`
	EventsPerMin  float64          `json:"events_per_minute"`
	ActiveUsers   int64            `json:"active_users_5m"`
	TopEvents     []EventFreq      `json:"top_events"`
}

// RealTime queries ClickHouse for the last 5 minutes of activity.
func (s *Service) RealTime(ctx context.Context, appID string) (*RealTimeStats, error) {
	since := time.Now().Add(-5 * time.Minute)

	row := s.ch.QueryRow(ctx, `
		SELECT
			count() AS total_events,
			uniq(user_id) AS active_users
		FROM events
		WHERE app_id = ? AND event_time >= ?
	`, appID, since)

	var stats RealTimeStats
	stats.AppID = appID
	stats.AsOf = time.Now().UTC().Format(time.RFC3339)

	var totalEvents, activeUsers int64
	if err := row.Scan(&totalEvents, &activeUsers); err != nil {
		return nil, fmt.Errorf("realtime stats: %w", err)
	}
	stats.EventsPerMin = float64(totalEvents) / 5.0
	stats.ActiveUsers = activeUsers

	topRows, err := s.ch.Query(ctx, `
		SELECT event_name, count() AS cnt
		FROM events
		WHERE app_id = ? AND event_time >= ?
		GROUP BY event_name
		ORDER BY cnt DESC
		LIMIT 10
	`, appID, since)
	if err == nil {
		defer topRows.Close()
		for topRows.Next() {
			var ef EventFreq
			_ = topRows.Scan(&ef.EventName, &ef.Count)
			stats.TopEvents = append(stats.TopEvents, ef)
		}
	}

	return &stats, nil
}

// ─── User Segments ────────────────────────────────────────────────────────────
// Modelled after Amplitude Cohorts, CleverTap Segments, and Mixpanel Cohorts.

// SegmentCountRequest returns the count of users matching a segment.
type SegmentCountRequest struct {
	AppID      string   `json:"app_id"`
	Filters    []Filter `json:"filters"`    // event + property filters
	HasEvents  []string `json:"has_events"` // must have performed these events
	FromMs     int64    `json:"from_ms"`
	ToMs       int64    `json:"to_ms"`
}

// SegmentCountResponse is the number of users matching the segment definition.
type SegmentCountResponse struct {
	AppID       string `json:"app_id"`
	MatchedUsers int64 `json:"matched_users"`
	EvaluatedAt string `json:"evaluated_at"`
}

// SegmentCount evaluates a dynamic segment and returns the matching user count.
func (s *Service) SegmentCount(ctx context.Context, req SegmentCountRequest) (*SegmentCountResponse, error) {
	from := time.UnixMilli(req.FromMs)
	to := time.UnixMilli(req.ToMs)

	whereClause := "app_id = ? AND event_time BETWEEN ? AND ?"
	args := []interface{}{req.AppID, from, to}

	for _, ev := range req.HasEvents {
		whereClause += fmt.Sprintf(" AND user_id IN (SELECT DISTINCT user_id FROM events WHERE app_id = ? AND event_name = ? AND event_time BETWEEN ? AND ?)")
		args = append(args, req.AppID, ev, from, to)
	}

	for _, f := range req.Filters {
		switch f.Operator {
		case "eq":
			whereClause += fmt.Sprintf(" AND props['%s'] = ?", f.Property)
			args = append(args, f.Value)
		case "neq":
			whereClause += fmt.Sprintf(" AND props['%s'] != ?", f.Property)
			args = append(args, f.Value)
		case "contains":
			whereClause += fmt.Sprintf(" AND props['%s'] ILIKE ?", f.Property)
			args = append(args, "%"+f.Value+"%")
		}
	}

	query := fmt.Sprintf("SELECT uniq(user_id) FROM events WHERE %s", whereClause)
	row := s.ch.QueryRow(ctx, query, args...)

	var matched int64
	if err := row.Scan(&matched); err != nil {
		return nil, fmt.Errorf("segment count: %w", err)
	}

	return &SegmentCountResponse{
		AppID:       req.AppID,
		MatchedUsers: matched,
		EvaluatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func splitPath(path string) []string {
	// path looks like "event_a -> event_b -> event_c"
	var parts []string
	start := 0
	for i := 0; i < len(path)-3; i++ {
		if path[i:i+4] == " -> " {
			parts = append(parts, path[start:i])
			start = i + 4
		}
	}
	parts = append(parts, path[start:])
	return parts
}
