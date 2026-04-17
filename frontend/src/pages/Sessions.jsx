import React, { useState, useEffect, useCallback } from 'react'
import {
  BarChart, Bar, LineChart, Line, AreaChart, Area,
  XAxis, YAxis, CartesianGrid, ResponsiveContainer, Tooltip,
} from 'recharts'
import { Clock, Layers, Zap, Target } from 'lucide-react'
import Layout from '../components/Layout.jsx'
import MetricCard from '../components/MetricCard.jsx'
import DateRangeFilter, { defaultDateRange } from '../components/DateRangeFilter.jsx'
import { PulseTooltip, formatNumber } from '../components/ChartTooltip.jsx'
import { LoadingSpinner, ErrorState } from '../components/LoadingState.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { getSessionMetrics } from '../api/queryapi.js'
import { mockSessionMetrics, mockEventCount } from '../hooks/useMockData.js'

export default function Sessions() {
  const { selectedApp } = useAuth()
  const [dateRange, setDateRange] = useState(defaultDateRange(30))
  const [metrics, setMetrics]     = useState(null)
  const [sessionTrend, setSessionTrend] = useState([])
  const [durationDist, setDurationDist] = useState([])
  const [loading, setLoading]     = useState(false)
  const [error, setError]         = useState(null)

  const fetchData = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const params = { app_id: selectedApp, from_ms: dateRange.from_ms, to_ms: dateRange.to_ms }
      let m
      try { m = await getSessionMetrics(params) }
      catch { m = mockSessionMetrics() }
      setMetrics(m)

      /* Session volume trend */
      const ec = mockEventCount({ granularity: 'day', ...params })
      setSessionTrend(ec.points.map(p => ({
        label: new Date(p.timestamp_ms).toLocaleDateString('en', { month: 'short', day: 'numeric' }),
        sessions: Math.round(p.value * 0.13),
        avg_duration: Math.round(150 + Math.random() * 80),
      })))

      /* Duration distribution buckets */
      setDurationDist([
        { bucket: '0-30s',    count: 12400 },
        { bucket: '30-60s',   count: 18700 },
        { bucket: '1-2 min',  count: 24300 },
        { bucket: '2-5 min',  count: 31200 },
        { bucket: '5-10 min', count: 19800 },
        { bucket: '10-20 min', count: 8400 },
        { bucket: '20-30 min', count: 3200 },
        { bucket: '>30 min',   count: 1100 },
      ])
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [selectedApp, dateRange])

  useEffect(() => { fetchData() }, [fetchData])

  function fmtDuration(s) {
    if (!s && s !== 0) return '—'
    const m = Math.floor(s / 60)
    const sec = Math.round(s % 60)
    return m > 0 ? `${m}m ${sec}s` : `${sec}s`
  }

  return (
    <Layout pageTitle="Sessions" onRefresh={fetchData} loading={loading}>
      <div className="page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Session Metrics</h1>
            <p className="page-subtitle">
              30-minute inactivity timeout · Session state in Redis · Aggregated in ClickHouse
            </p>
          </div>
          <DateRangeFilter value={dateRange} onChange={setDateRange} />
        </div>

        {/* KPI cards */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(210px, 1fr))',
          gap: '1rem',
          marginBottom: '1.5rem',
        }}>
          <MetricCard
            title="Total Sessions"
            value={metrics?.total_sessions}
            icon={Layers}
            iconColor="var(--primary)"
            loading={loading}
          />
          <MetricCard
            title="Avg Duration"
            value={metrics ? fmtDuration(metrics.avg_session_duration_s) : null}
            icon={Clock}
            iconColor="var(--info)"
            loading={loading}
          />
          <MetricCard
            title="Median Duration"
            value={metrics ? fmtDuration(metrics.median_session_duration_s) : null}
            icon={Target}
            iconColor="var(--success)"
            loading={loading}
          />
          <MetricCard
            title="Events/Session"
            value={metrics ? metrics.avg_events_per_session?.toFixed(1) : null}
            icon={Zap}
            iconColor="var(--warning)"
            loading={loading}
          />
        </div>

        {/* Secondary stats */}
        {metrics && (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '0.75rem', marginBottom: '1.25rem' }}>
            <div className="card-sm">
              <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.2rem', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                Sessions Today
              </div>
              <div style={{ fontSize: '1.286rem', fontWeight: 700 }}>
                {formatNumber(metrics.sessions_today)}
              </div>
            </div>
            <div className="card-sm">
              <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.2rem', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                Bounce Rate
              </div>
              <div style={{
                fontSize: '1.286rem', fontWeight: 700,
                color: (metrics.bounce_rate || 0) > 0.3 ? 'var(--danger)' : 'var(--success)',
              }}>
                {((metrics.bounce_rate || 0) * 100).toFixed(1)}%
              </div>
            </div>
            <div className="card-sm">
              <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.2rem', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
                Avg Duration (P50)
              </div>
              <div style={{ fontSize: '1.286rem', fontWeight: 700 }}>
                {fmtDuration(metrics.median_session_duration_s)}
              </div>
            </div>
          </div>
        )}

        {/* Charts row */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem', marginBottom: '1rem' }}>
          {/* Session volume trend */}
          <div className="card">
            <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Daily Session Volume</div>
            {loading ? <LoadingSpinner /> : error ? <ErrorState error={error} onRetry={fetchData} /> : (
              <ResponsiveContainer width="100%" height={220}>
                <AreaChart data={sessionTrend}>
                  <defs>
                    <linearGradient id="sess-grad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%"  stopColor="var(--chart-1)" stopOpacity={0.25} />
                      <stop offset="95%" stopColor="var(--chart-1)" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                  <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval={4} />
                  <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                  <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                  <Area type="monotone" dataKey="sessions" name="Sessions" stroke="var(--chart-1)" fill="url(#sess-grad)" strokeWidth={2} dot={false} />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </div>

          {/* Duration distribution */}
          <div className="card">
            <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Session Duration Distribution</div>
            <ResponsiveContainer width="100%" height={220}>
              <BarChart data={durationDist}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                <XAxis dataKey="bucket" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                <Bar dataKey="count" name="Sessions" fill="var(--chart-2)" radius={[3, 3, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </div>

        {/* Avg duration trend */}
        <div className="card">
          <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Average Session Duration Trend (seconds)</div>
          {loading ? <LoadingSpinner /> : (
            <ResponsiveContainer width="100%" height={180}>
              <LineChart data={sessionTrend}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval={4} />
                <YAxis tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                <Tooltip content={<PulseTooltip />} />
                <Line type="monotone" dataKey="avg_duration" name="Avg Duration (s)" stroke="var(--chart-3)" strokeWidth={2} dot={false} />
              </LineChart>
            </ResponsiveContainer>
          )}
        </div>
      </div>
    </Layout>
  )
}
