/* Realistic mock data for demo/development mode */

import { generateTimeSeries, generateHourlySeries } from './useQuery.js'

export function mockEventCount(params = {}) {
  const { granularity = 'day', from_ms, to_ms } = params
  const start = from_ms ? new Date(Number(from_ms)) : new Date(Date.now() - 30 * 86400000)
  const end   = to_ms   ? new Date(Number(to_ms))   : new Date()
  const diffMs = end - start
  let buckets, intervalMs, fmt

  if (granularity === 'hour') {
    buckets = Math.min(Math.ceil(diffMs / 3600000), 72)
    intervalMs = 3600000
    fmt = (ts) => new Date(ts).toLocaleTimeString('en', { hour: '2-digit', minute: '2-digit' })
  } else if (granularity === 'week') {
    buckets = Math.min(Math.ceil(diffMs / 604800000), 52)
    intervalMs = 604800000
    fmt = (ts) => 'W' + Math.ceil(new Date(ts).getDate() / 7)
  } else if (granularity === 'month') {
    buckets = 12
    intervalMs = 30 * 86400000
    fmt = (ts) => new Date(ts).toLocaleString('en', { month: 'short' })
  } else {
    buckets = Math.min(Math.ceil(diffMs / 86400000), 90)
    intervalMs = 86400000
    fmt = (ts) => new Date(ts).toLocaleDateString('en', { month: 'short', day: 'numeric' })
  }

  const points = Array.from({ length: buckets }, (_, i) => {
    const ts = start.getTime() + i * intervalMs
    const trend = 1 + i * 0.005
    const noise = 1 + (Math.random() - 0.5) * 0.3
    return { timestamp_ms: ts, value: Math.round(142000 * trend * noise), label: fmt(ts) }
  })

  return { points, total: points.reduce((s, p) => s + p.value, 0) }
}

export function mockDAU(params = {}) {
  const { granularity = 'day' } = params
  const days = granularity === 'day' ? 30 : granularity === 'week' ? 12 : 6
  const base  = granularity === 'day' ? 45000 : granularity === 'week' ? 120000 : 380000
  const interval = granularity === 'day' ? 86400000 : granularity === 'week' ? 604800000 : 30 * 86400000
  const now = Date.now()
  const points = Array.from({ length: days }, (_, i) => {
    const ts = now - (days - 1 - i) * interval
    return {
      timestamp_ms: ts,
      value: Math.round(base * (1 + i * 0.008) * (1 + (Math.random() - 0.5) * 0.2)),
    }
  })
  return { points, total: points[points.length - 1]?.value || base }
}

export function mockFunnelQuery(steps = []) {
  const names = steps.length >= 2 ? steps : ['app_installed', 'user_registered', 'onboarding_complete', 'purchase_completed']
  const total = 100000
  let count = total
  return {
    steps: names.map((name, i) => {
      if (i > 0) count = Math.round(count * (0.5 + Math.random() * 0.35))
      const conversion_rate = i === 0 ? 1 : count / total
      return {
        event_name:      name,
        user_count:      count,
        conversion_rate,
        drop_off_rate:   1 - conversion_rate,
        step_conversion: i === 0 ? 1 : count / Math.round(count / (0.5 + Math.random() * 0.35)),
      }
    }),
  }
}

export function mockRetention() {
  const cohorts = Array.from({ length: 8 }, (_, w) => {
    const install_date = new Date(Date.now() - (8 - w) * 7 * 86400000)
      .toISOString().split('T')[0]
    const cohort_size  = Math.round(10000 + Math.random() * 8000)
    const base         = [0.42, 0.28, 0.19, 0.13, 0.08]
    const day_n_rates  = {}
    ;[1, 3, 7, 14, 30].forEach((d, i) => {
      day_n_rates[`day_${d}`] = +(base[i] * (1 + (Math.random() - 0.5) * 0.15)).toFixed(3)
    })
    return { install_date, cohort_size, day_n_rates }
  })
  return { cohorts }
}

export function mockSessionMetrics() {
  return {
    avg_session_duration_s:    187.4 + (Math.random() - 0.5) * 30,
    median_session_duration_s: 142.0 + (Math.random() - 0.5) * 20,
    total_sessions:            4200000 + Math.round(Math.random() * 500000),
    avg_events_per_session:    12.3  + (Math.random() - 0.5) * 2,
    sessions_today:            18400 + Math.round(Math.random() * 3000),
    bounce_rate:               0.24  + (Math.random() - 0.5) * 0.05,
  }
}

export function mockAlerts(appId) {
  return [
    { id: 'alert-1', app_id: appId, name: 'High Error Rate', metric_name: 'error_rate', condition: 'gt', threshold: 0.01, window_mins: 5,  channels: ['webhook'], active: true,  last_fired_at: new Date(Date.now() - 3600000).toISOString() },
    { id: 'alert-2', app_id: appId, name: 'Gateway P95 Latency', metric_name: 'p95_latency_ms', condition: 'gt', threshold: 200, window_mins: 15, channels: ['email'], active: true,  last_fired_at: null },
    { id: 'alert-3', app_id: appId, name: 'DAU Drop', metric_name: 'dau', condition: 'lt', threshold: 30000, window_mins: 60, channels: ['webhook', 'email'], active: false, last_fired_at: null },
    { id: 'alert-4', app_id: appId, name: 'Kafka Lag Critical', metric_name: 'kafka_lag', condition: 'gt', threshold: 1000000, window_mins: 10, channels: ['webhook'], active: true,  last_fired_at: new Date(Date.now() - 7200000).toISOString() },
  ]
}

export function mockExperiments(appId) {
  return [
    {
      id: 'exp-1', app_id: appId, name: 'New Onboarding Flow', description: 'Testing simplified onboarding vs current',
      status: 'running',
      variants: [{ name: 'control', weight: 50, config: {} }, { name: 'simplified', weight: 50, config: { steps: 3 } }],
      metric_goals: [{ metric: 'conversion_rate', type: 'primary' }, { metric: 'session_duration', type: 'guardrail' }],
      started_at: new Date(Date.now() - 7 * 86400000).toISOString(), ended_at: null,
      created_at: new Date(Date.now() - 8 * 86400000).toISOString(),
    },
    {
      id: 'exp-2', app_id: appId, name: 'Pricing Page CTA Color', description: 'Blue vs Green CTA button',
      status: 'completed',
      variants: [{ name: 'blue', weight: 50, config: { color: '#6366f1' } }, { name: 'green', weight: 50, config: { color: '#22c55e' } }],
      metric_goals: [{ metric: 'purchase_rate', type: 'primary' }],
      started_at: new Date(Date.now() - 30 * 86400000).toISOString(),
      ended_at:   new Date(Date.now() - 14 * 86400000).toISOString(),
      created_at: new Date(Date.now() - 31 * 86400000).toISOString(),
    },
    {
      id: 'exp-3', app_id: appId, name: 'Push Notification Timing', description: 'Morning vs evening push',
      status: 'draft',
      variants: [{ name: '9am', weight: 50, config: {} }, { name: '7pm', weight: 50, config: {} }],
      metric_goals: [{ metric: 'dau', type: 'primary' }],
      started_at: null, ended_at: null,
      created_at: new Date(Date.now() - 86400000).toISOString(),
    },
  ]
}

export function mockCohorts(appId) {
  return [
    { id: 'cohort-1', app_id: appId, name: 'Power Users', description: 'Users with >20 sessions/week', filter_sql: "event_count > 20", user_count: 12450, last_computed_at: new Date(Date.now() - 3600000).toISOString(), created_at: new Date(Date.now() - 30 * 86400000).toISOString() },
    { id: 'cohort-2', app_id: appId, name: 'Churned Users', description: 'No activity in 14 days', filter_sql: "last_seen < NOW() - INTERVAL 14 DAY", user_count: 38200, last_computed_at: new Date(Date.now() - 7200000).toISOString(), created_at: new Date(Date.now() - 60 * 86400000).toISOString() },
    { id: 'cohort-3', app_id: appId, name: 'Paying Customers', description: 'At least one purchase', filter_sql: "event_name = 'purchase_completed'", user_count: 8750, last_computed_at: new Date(Date.now() - 1800000).toISOString(), created_at: new Date(Date.now() - 45 * 86400000).toISOString() },
    { id: 'cohort-4', app_id: appId, name: 'iOS Users', description: 'Platform = iOS', filter_sql: "platform = 'ios'", user_count: 61300, last_computed_at: new Date(Date.now() - 900000).toISOString(), created_at: new Date(Date.now() - 90 * 86400000).toISOString() },
  ]
}

export function mockApps() {
  return [
    { id: 'app-1', org_id: 'org-1', name: 'PulseDemo iOS', api_key: 'pk_live_abc123', rps: 10000, burst: 50000, active: true, created_at: new Date(Date.now() - 90 * 86400000).toISOString() },
    { id: 'app-2', org_id: 'org-1', name: 'PulseDemo Android', api_key: 'pk_live_def456', rps: 10000, burst: 50000, active: true, created_at: new Date(Date.now() - 85 * 86400000).toISOString() },
    { id: 'app-3', org_id: 'org-1', name: 'PulseDemo Web', api_key: 'pk_live_ghi789', rps: 5000, burst: 25000, active: true, created_at: new Date(Date.now() - 60 * 86400000).toISOString() },
    { id: 'app-4', org_id: 'org-2', name: 'Beta App', api_key: 'pk_test_jkl012', rps: 1000, burst: 5000, active: false, created_at: new Date(Date.now() - 14 * 86400000).toISOString() },
  ]
}

export function mockOrgs() {
  return [
    { id: 'org-1', name: 'Acme Corporation', plan: 'enterprise', created_at: new Date(Date.now() - 180 * 86400000).toISOString() },
    { id: 'org-2', name: 'Beta Labs', plan: 'growth', created_at: new Date(Date.now() - 30 * 86400000).toISOString() },
  ]
}

export function mockFunnelDefinitions(appId) {
  return [
    { funnel_id: 'f-1', app_id: appId, name: 'Onboarding Funnel', steps: ['app_installed', 'user_registered', 'profile_complete', 'first_purchase'], window_seconds: 604800, created_at: new Date(Date.now() - 30 * 86400000).toISOString() },
    { funnel_id: 'f-2', app_id: appId, name: 'Checkout Funnel', steps: ['product_viewed', 'cart_added', 'checkout_started', 'purchase_completed'], window_seconds: 3600, created_at: new Date(Date.now() - 14 * 86400000).toISOString() },
    { funnel_id: 'f-3', app_id: appId, name: 'Feature Adoption', steps: ['feature_discovered', 'feature_used', 'feature_shared'], window_seconds: 86400, created_at: new Date(Date.now() - 7 * 86400000).toISOString() },
  ]
}

export function mockDashboardStats() {
  return {
    total_events_today:    4_280_000,
    total_events_change:   +12.4,
    dau:                   48_320,
    dau_change:            +3.8,
    total_sessions_today:  18_750,
    sessions_change:       +7.1,
    revenue_today:         87_420,
    revenue_change:        +15.2,
    avg_session_duration:  187,
    p95_latency_ms:        142,
    error_rate:            0.003,
    kafka_lag:             12400,
  }
}
