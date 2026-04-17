// Package querying implements the analytics query service.
// Translates structured query requests into ClickHouse SQL,
// applies multi-tier caching, enforces tenant isolation, and returns results.
//
// # Caching Architecture
//
//	L1 — in-process sync.Map (this pod only, ~60s TTL, sub-microsecond reads)
//	     └─ stale-while-revalidate: returns stale data, triggers async refresh
//	L2 — Redis cluster (shared across pods, 5min TTL, ~1ms reads)
//	L3 — ClickHouse materialized views (pre-aggregated, ~10ms reads)
//	L4 — ClickHouse raw events table (full scan, ~100ms–2s reads)
//
// # Additional Resilience Patterns
//
//   - Single-flight (singleflight.Group): concurrent identical queries are
//     coalesced — only one ClickHouse call is made; all waiters share the result.
//   - Circuit Breaker: ClickHouse calls are wrapped; after 5 consecutive
//     failures the breaker opens for 30s to give ClickHouse recovery time.
//   - Bulkhead: per-tenant concurrency cap (default 20) plus a global cap
//     (default 200) to prevent noisy tenants from starving others.
package querying

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/pulse-analytics/internal/bulkhead"
	"github.com/pulse-analytics/internal/cache"
	chchlient "github.com/pulse-analytics/internal/clickhouse"
	"github.com/pulse-analytics/internal/circuitbreaker"
	"github.com/pulse-analytics/internal/metrics"
	redisclient "github.com/pulse-analytics/internal/redis"
)

const (
	cacheL2TTL    = 5 * time.Minute
	cacheL1TTL    = 60 * time.Second
	cacheL1Stale  = 45 * time.Second // SWR window: 45–60s is the revalidation zone
	l1MaxEntries  = 10_000
	l1JanitorEvery = 30 * time.Second
)

// Service handles analytics queries with caching and tenant isolation.
type Service struct {
	ch    *chchlient.Client
	redis *redisclient.Client
	m     *metrics.Registry
	log   *zap.Logger

	// L1 in-process cache (this pod only).
	l1 *cache.L1Cache

	// single-flight deduplicates concurrent identical ClickHouse queries.
	sf singleflight.Group

	// chBreaker wraps all ClickHouse calls with a circuit breaker.
	chBreaker *circuitbreaker.Breaker

	// bh enforces per-tenant and global concurrency limits.
	bh *bulkhead.Bulkhead
}

// NewService creates a query service.
func NewService(ch *chchlient.Client, redis *redisclient.Client, m *metrics.Registry, log *zap.Logger) *Service {
	chBreaker := circuitbreaker.New("clickhouse", circuitbreaker.Config{
		MaxFailures:      5,
		OpenTimeout:      30 * time.Second,
		SuccessThreshold: 2,
		OnStateChange: func(name string, from, to circuitbreaker.State) {
			log.Warn("circuit breaker state change",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		},
	})

	return &Service{
		ch:        ch,
		redis:     redis,
		m:         m,
		log:       log,
		l1:        cache.New(l1MaxEntries, l1JanitorEvery),
		chBreaker: chBreaker,
		bh:        bulkhead.New(20, 200),
	}
}

// Close releases resources owned by the service.
func (s *Service) Close() {
	s.l1.Close()
}

// ─── Request / Response types ─────────────────────────────────────────────────

type EventCountRequest struct {
	AppID       string            `json:"app_id"`
	EventName   string            `json:"event_name"`
	FromMs      int64             `json:"from_ms"`
	ToMs        int64             `json:"to_ms"`
	Granularity string            `json:"granularity"` // minute | hour | day
	GroupBy     []string          `json:"group_by"`
	Filters     map[string]string `json:"filters"`
}

type DataPoint struct {
	TimestampMs int64             `json:"timestamp_ms"`
	Value       float64           `json:"value"`
	Dimensions  map[string]string `json:"dimensions,omitempty"`
}

type EventCountResponse struct {
	Points []DataPoint `json:"points"`
	Total  int64       `json:"total"`
}

type FunnelRequest struct {
	AppID         string   `json:"app_id"`
	Steps         []string `json:"steps"`
	WindowSeconds int64    `json:"window_seconds"`
	FromMs        int64    `json:"from_ms"`
	ToMs          int64    `json:"to_ms"`
}

type FunnelStep struct {
	EventName      string  `json:"event_name"`
	UserCount      int64   `json:"user_count"`
	ConversionRate float64 `json:"conversion_rate"`
	DropOffRate    float64 `json:"drop_off_rate"`
}

type FunnelResponse struct {
	Steps []FunnelStep `json:"steps"`
}

type RetentionRequest struct {
	AppID  string  `json:"app_id"`
	FromMs int64   `json:"from_ms"`
	ToMs   int64   `json:"to_ms"`
	DayNs  []int32 `json:"day_ns"` // [1, 3, 7, 14, 30]
}

type RetentionCohort struct {
	InstallDate string             `json:"install_date"`
	CohortSize  int64              `json:"cohort_size"`
	DayNRates   map[string]float64 `json:"day_n_rates"` // "day_1" -> 0.45
}

type RetentionResponse struct {
	Cohorts []RetentionCohort `json:"cohorts"`
}

type DAURequest struct {
	AppID       string `json:"app_id"`
	FromMs      int64  `json:"from_ms"`
	ToMs        int64  `json:"to_ms"`
	Granularity string `json:"granularity"` // day | week | month
}

// ─── Query Methods ────────────────────────────────────────────────────────────

// EventCount returns event counts over time with optional grouping.
func (s *Service) EventCount(ctx context.Context, req EventCountRequest) (*EventCountResponse, error) {
	start := time.Now()

	sql, args := s.buildEventCountSQL(req)
	cacheKey := s.cacheKey(sql, args)

	// ── L1 (in-process) ──────────────────────────────────────────────────────
	if res := s.l1.Get(cacheKey); res.Found {
		s.m.QueryCacheHit.WithLabelValues("l1_local").Inc()
		var resp EventCountResponse
		if err := json.Unmarshal(res.Value, &resp); err == nil {
			if res.NeedsRefresh {
				go s.refreshEventCount(cacheKey, sql, args)
			}
			return &resp, nil
		}
	}

	// ── L2 (Redis) ───────────────────────────────────────────────────────────
	var resp EventCountResponse
	if err := s.redis.GetJSON(ctx, cacheKey, &resp); err == nil {
		s.m.QueryCacheHit.WithLabelValues("l2_redis").Inc()
		// Populate L1 so future requests on this pod don't hit Redis.
		if data, err := json.Marshal(resp); err == nil {
			s.l1.Set(cacheKey, data, cacheL1TTL, cacheL1Stale)
		}
		return &resp, nil
	}
	s.m.RedisCacheMisses.WithLabelValues("query").Inc()

	// ── L3/L4 (ClickHouse) — with single-flight + circuit breaker + bulkhead ─
	result, err, _ := s.sf.Do(cacheKey, func() (any, error) {
		return s.execEventCount(ctx, req, cacheKey, sql, args)
	})
	if err != nil {
		s.m.CHQueryErrors.WithLabelValues("event_count").Inc()
		return nil, err
	}

	s.m.CHQueryLatency.WithLabelValues("event_count").Observe(time.Since(start).Seconds())
	return result.(*EventCountResponse), nil
}

func (s *Service) execEventCount(ctx context.Context, req EventCountRequest, cacheKey, sql string, args []any) (*EventCountResponse, error) {
	var resp EventCountResponse

	err := s.bh.Do(req.AppID, func() error {
		return s.chBreaker.Execute(func() error {
			rows, err := s.ch.Query(ctx, sql, args...)
			if err != nil {
				return fmt.Errorf("event count query: %w", err)
			}
			defer rows.Close()

			var points []DataPoint
			var total int64
			for rows.Next() {
				var ts time.Time
				var val float64
				if err := rows.Scan(&ts, &val); err != nil {
					continue
				}
				points = append(points, DataPoint{
					TimestampMs: ts.UnixMilli(),
					Value:       val,
				})
				total += int64(val)
			}
			resp = EventCountResponse{Points: points, Total: total}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	s.populateCaches(ctx, cacheKey, resp)
	return &resp, nil
}

// refreshEventCount runs an async cache refresh for stale-while-revalidate.
func (s *Service) refreshEventCount(cacheKey, sql string, args []any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.ch.Query(ctx, sql, args...)
	if err != nil {
		s.l1.Delete(cacheKey) // force next caller to do a full refresh
		return
	}
	defer rows.Close()

	var points []DataPoint
	var total int64
	for rows.Next() {
		var ts time.Time
		var val float64
		if err := rows.Scan(&ts, &val); err != nil {
			continue
		}
		points = append(points, DataPoint{TimestampMs: ts.UnixMilli(), Value: val})
		total += int64(val)
	}
	resp := EventCountResponse{Points: points, Total: total}
	s.populateCaches(ctx, cacheKey, resp)
}

// Funnel computes multi-step funnel conversion rates.
func (s *Service) Funnel(ctx context.Context, req FunnelRequest) (*FunnelResponse, error) {
	start := time.Now()

	if len(req.Steps) < 2 || len(req.Steps) > 10 {
		return nil, fmt.Errorf("funnel requires 2–10 steps")
	}

	sql, args := s.buildFunnelSQL(req)
	cacheKey := s.cacheKey(sql, args)

	if res := s.l1.Get(cacheKey); res.Found {
		s.m.QueryCacheHit.WithLabelValues("l1_local").Inc()
		var resp FunnelResponse
		if err := json.Unmarshal(res.Value, &resp); err == nil {
			return &resp, nil
		}
	}

	var resp FunnelResponse
	if err := s.redis.GetJSON(ctx, cacheKey, &resp); err == nil {
		s.m.QueryCacheHit.WithLabelValues("l2_redis").Inc()
		if data, err := json.Marshal(resp); err == nil {
			s.l1.Set(cacheKey, data, cacheL1TTL, cacheL1Stale)
		}
		return &resp, nil
	}

	result, err, _ := s.sf.Do(cacheKey, func() (any, error) {
		var r FunnelResponse
		execErr := s.bh.Do(req.AppID, func() error {
			return s.chBreaker.Execute(func() error {
				rows, err := s.ch.Query(ctx, sql, args...)
				if err != nil {
					return fmt.Errorf("funnel query: %w", err)
				}
				defer rows.Close()

				levelCounts := make(map[int]int64)
				for rows.Next() {
					var level int
					var count int64
					if err := rows.Scan(&level, &count); err != nil {
						continue
					}
					levelCounts[level] += count
				}

				steps := make([]FunnelStep, len(req.Steps))
				for i, name := range req.Steps {
					var userCount int64
					for lvl, cnt := range levelCounts {
						if lvl >= i+1 {
							userCount += cnt
						}
					}
					steps[i] = FunnelStep{EventName: name, UserCount: userCount}
				}
				for i := range steps {
					if i == 0 && steps[0].UserCount > 0 {
						steps[i].ConversionRate = 1.0
					} else if i > 0 && steps[i-1].UserCount > 0 {
						steps[i].ConversionRate = float64(steps[i].UserCount) / float64(steps[0].UserCount)
						steps[i-1].DropOffRate = 1.0 - float64(steps[i].UserCount)/float64(steps[i-1].UserCount)
					}
				}
				r = FunnelResponse{Steps: steps}
				return nil
			})
		})
		if execErr != nil {
			return nil, execErr
		}
		s.populateCaches(ctx, cacheKey, r)
		return &r, nil
	})
	if err != nil {
		s.m.CHQueryErrors.WithLabelValues("funnel").Inc()
		return nil, err
	}

	s.m.CHQueryLatency.WithLabelValues("funnel").Observe(time.Since(start).Seconds())
	return result.(*FunnelResponse), nil
}

// DAU returns daily/weekly/monthly active users.
func (s *Service) DAU(ctx context.Context, req DAURequest) (*EventCountResponse, error) {
	start := time.Now()

	sql, args := s.buildDAUSQL(req)
	cacheKey := s.cacheKey(sql, args)

	if res := s.l1.Get(cacheKey); res.Found {
		s.m.QueryCacheHit.WithLabelValues("l1_local").Inc()
		var resp EventCountResponse
		if err := json.Unmarshal(res.Value, &resp); err == nil {
			return &resp, nil
		}
	}

	var resp EventCountResponse
	if err := s.redis.GetJSON(ctx, cacheKey, &resp); err == nil {
		s.m.QueryCacheHit.WithLabelValues("l2_redis").Inc()
		if data, err := json.Marshal(resp); err == nil {
			s.l1.Set(cacheKey, data, cacheL1TTL, cacheL1Stale)
		}
		return &resp, nil
	}

	result, err, _ := s.sf.Do(cacheKey, func() (any, error) {
		var r EventCountResponse
		execErr := s.bh.Do(req.AppID, func() error {
			return s.chBreaker.Execute(func() error {
				rows, err := s.ch.Query(ctx, sql, args...)
				if err != nil {
					return fmt.Errorf("DAU query: %w", err)
				}
				defer rows.Close()

				var points []DataPoint
				for rows.Next() {
					var date time.Time
					var dau uint64
					if err := rows.Scan(&date, &dau); err != nil {
						continue
					}
					points = append(points, DataPoint{TimestampMs: date.UnixMilli(), Value: float64(dau)})
				}
				r = EventCountResponse{Points: points}
				return nil
			})
		})
		if execErr != nil {
			return nil, execErr
		}
		s.populateCaches(ctx, cacheKey, r)
		return &r, nil
	})
	if err != nil {
		s.m.CHQueryErrors.WithLabelValues("dau").Inc()
		return nil, err
	}

	s.m.CHQueryLatency.WithLabelValues("dau").Observe(time.Since(start).Seconds())
	return result.(*EventCountResponse), nil
}

// Retention computes Day-N retention for install cohorts.
func (s *Service) Retention(ctx context.Context, req RetentionRequest) (*RetentionResponse, error) {
	start := time.Now()
	cacheKey := s.cacheKey(fmt.Sprintf("retention:%s:%d:%d", req.AppID, req.FromMs, req.ToMs), nil)

	if res := s.l1.Get(cacheKey); res.Found {
		s.m.QueryCacheHit.WithLabelValues("l1_local").Inc()
		var resp RetentionResponse
		if err := json.Unmarshal(res.Value, &resp); err == nil {
			return &resp, nil
		}
	}

	var resp RetentionResponse
	if err := s.redis.GetJSON(ctx, cacheKey, &resp); err == nil {
		s.m.QueryCacheHit.WithLabelValues("l2_redis").Inc()
		if data, err := json.Marshal(resp); err == nil {
			s.l1.Set(cacheKey, data, cacheL1TTL, cacheL1Stale)
		}
		return &resp, nil
	}

	result, err, _ := s.sf.Do(cacheKey, func() (any, error) {
		var r RetentionResponse
		execErr := s.bh.Do(req.AppID, func() error {
			return s.chBreaker.Execute(func() error {
				sql := `
					SELECT
						toDate(install_time) AS install_date,
						count(DISTINCT user_id) AS cohort_size,
						countDistinctIf(user_id, dateDiff('day', toDate(install_time), toDate(return_time)) = 1) AS day1,
						countDistinctIf(user_id, dateDiff('day', toDate(install_time), toDate(return_time)) = 3) AS day3,
						countDistinctIf(user_id, dateDiff('day', toDate(install_time), toDate(return_time)) = 7) AS day7,
						countDistinctIf(user_id, dateDiff('day', toDate(install_time), toDate(return_time)) = 14) AS day14,
						countDistinctIf(user_id, dateDiff('day', toDate(install_time), toDate(return_time)) = 30) AS day30
					FROM (
						SELECT
							user_id,
							minIf(event_time, event_name = 'app_installed') AS install_time,
							event_time AS return_time
						FROM events
						WHERE
							app_id = ?
							AND event_time BETWEEN ? AND ?
							AND event_name IN ('app_installed', 'app_opened')
						GROUP BY user_id, return_time
					)
					WHERE install_time > 0
					GROUP BY install_date
					ORDER BY install_date`

				rows, err := s.ch.Query(ctx, sql,
					req.AppID,
					time.UnixMilli(req.FromMs),
					time.UnixMilli(req.ToMs),
				)
				if err != nil {
					return fmt.Errorf("retention query: %w", err)
				}
				defer rows.Close()

				var cohorts []RetentionCohort
				for rows.Next() {
					var installDate time.Time
					var cohortSize, d1, d3, d7, d14, d30 int64
					if err := rows.Scan(&installDate, &cohortSize, &d1, &d3, &d7, &d14, &d30); err != nil {
						continue
					}
					if cohortSize == 0 {
						continue
					}
					cohorts = append(cohorts, RetentionCohort{
						InstallDate: installDate.Format("2006-01-02"),
						CohortSize:  cohortSize,
						DayNRates: map[string]float64{
							"day_1":  safeDivide(d1, cohortSize),
							"day_3":  safeDivide(d3, cohortSize),
							"day_7":  safeDivide(d7, cohortSize),
							"day_14": safeDivide(d14, cohortSize),
							"day_30": safeDivide(d30, cohortSize),
						},
					})
				}
				r = RetentionResponse{Cohorts: cohorts}
				return nil
			})
		})
		if execErr != nil {
			return nil, execErr
		}
		// Retention is expensive — keep in L2 longer.
		if data, err := json.Marshal(r); err == nil {
			s.l1.Set(cacheKey, data, cacheL1TTL, cacheL1Stale)
		}
		_ = s.redis.SetJSON(ctx, cacheKey, r, 10*time.Minute)
		return &r, nil
	})
	if err != nil {
		s.m.CHQueryErrors.WithLabelValues("retention").Inc()
		return nil, err
	}

	s.m.CHQueryLatency.WithLabelValues("retention").Observe(time.Since(start).Seconds())
	return result.(*RetentionResponse), nil
}

// ─── Cache helpers ────────────────────────────────────────────────────────────

// populateCaches writes a result to both L1 and L2 caches.
func (s *Service) populateCaches(ctx context.Context, key string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	s.l1.Set(key, data, cacheL1TTL, cacheL1Stale)
	_ = s.redis.SetJSON(ctx, key, value, cacheL2TTL)
}

func (s *Service) cacheKey(sql string, args []any) string {
	h := sha256.New()
	_, _ = h.Write([]byte(sql))
	if args != nil {
		data, _ := json.Marshal(args)
		_, _ = h.Write(data)
	}
	return "qcache:" + hex.EncodeToString(h.Sum(nil))[:16]
}

// ─── SQL Builders ─────────────────────────────────────────────────────────────

func (s *Service) buildEventCountSQL(req EventCountRequest) (string, []any) {
	granFn := granularityFn(req.Granularity)

	sql := fmt.Sprintf(`
		SELECT %s(event_time) AS ts, count() AS cnt
		FROM events
		WHERE app_id = ? AND event_name = ?
		  AND event_time BETWEEN ? AND ?
		GROUP BY ts
		ORDER BY ts`, granFn)

	args := []any{
		req.AppID,
		req.EventName,
		time.UnixMilli(req.FromMs),
		time.UnixMilli(req.ToMs),
	}
	return sql, args
}

func (s *Service) buildFunnelSQL(req FunnelRequest) (string, []any) {
	conditions := make([]string, len(req.Steps))
	for i, step := range req.Steps {
		conditions[i] = fmt.Sprintf("event_name = '%s'", strings.ReplaceAll(step, "'", "''"))
	}
	condStr := strings.Join(conditions, ",\n        ")

	sql := fmt.Sprintf(`
		SELECT
			windowFunnel(%d)(toUnixTimestamp(event_time),
				%s
			) AS level,
			count() AS users
		FROM events
		WHERE
			app_id = ?
			AND event_time BETWEEN ? AND ?
			AND event_name IN (%s)
		GROUP BY user_id`,
		req.WindowSeconds,
		condStr,
		inClause(req.Steps),
	)

	args := []any{
		req.AppID,
		time.UnixMilli(req.FromMs),
		time.UnixMilli(req.ToMs),
	}
	return sql, args
}

func (s *Service) buildDAUSQL(req DAURequest) (string, []any) {
	granFn := granularityFn(req.Granularity)

	sql := fmt.Sprintf(`
		SELECT %s(event_time) AS period, uniqHLL12(user_id) AS dau
		FROM events
		WHERE app_id = ?
		  AND event_name = 'app_opened'
		  AND event_time BETWEEN ? AND ?
		GROUP BY period
		ORDER BY period`, granFn)

	args := []any{
		req.AppID,
		time.UnixMilli(req.FromMs),
		time.UnixMilli(req.ToMs),
	}
	return sql, args
}

// ─── SQL Utilities ────────────────────────────────────────────────────────────

func granularityFn(g string) string {
	switch g {
	case "minute":
		return "toStartOfMinute"
	case "hour":
		return "toStartOfHour"
	case "week":
		return "toStartOfWeek"
	case "month":
		return "toStartOfMonth"
	default:
		return "toDate"
	}
}

func inClause(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = "'" + strings.ReplaceAll(item, "'", "''") + "'"
	}
	return strings.Join(quoted, ", ")
}

// RawQueryRow exposes a raw single-row query for ad-hoc use in handlers.
func (s *Service) RawQueryRow(ctx context.Context, sql string, args ...any) interface{ Scan(...any) error } {
	return s.ch.QueryRow(ctx, sql, args...)
}

func safeDivide(numerator, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
