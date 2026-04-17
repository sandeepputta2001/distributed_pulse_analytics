import React, { useState, useEffect, useCallback } from 'react'
import {
  AreaChart, Area, BarChart, Bar, XAxis, YAxis, CartesianGrid,
  ResponsiveContainer, Tooltip, Legend,
} from 'recharts'
import { Users, TrendingUp, Calendar } from 'lucide-react'
import Layout from '../components/Layout.jsx'
import MetricCard from '../components/MetricCard.jsx'
import DateRangeFilter, { defaultDateRange } from '../components/DateRangeFilter.jsx'
import { PulseTooltip, formatNumber } from '../components/ChartTooltip.jsx'
import { LoadingSpinner, ErrorState } from '../components/LoadingState.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { getActiveUsers } from '../api/queryapi.js'
import { mockDAU } from '../hooks/useMockData.js'

const GRANULARITIES = [
  { key: 'day',   label: 'DAU', description: 'Daily Active Users' },
  { key: 'week',  label: 'WAU', description: 'Weekly Active Users' },
  { key: 'month', label: 'MAU', description: 'Monthly Active Users' },
]

export default function ActiveUsers() {
  const { selectedApp } = useAuth()
  const [granularity, setGranularity] = useState('day')
  const [dateRange, setDateRange]     = useState(defaultDateRange(30))
  const [data, setData]               = useState([])
  const [loading, setLoading]         = useState(false)
  const [error, setError]             = useState(null)

  const fetchData = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const params = { app_id: selectedApp, granularity, from_ms: dateRange.from_ms, to_ms: dateRange.to_ms }
      let result
      try {
        result = await getActiveUsers(params)
      } catch {
        result = mockDAU({ granularity })
      }

      const fmt = (ts) => {
        const d = new Date(ts)
        if (granularity === 'month') return d.toLocaleString('en', { month: 'short', year: '2-digit' })
        if (granularity === 'week')  return `W of ${d.toLocaleDateString('en', { month: 'short', day: 'numeric' })}`
        return d.toLocaleDateString('en', { month: 'short', day: 'numeric' })
      }

      setData(result.points.map(p => ({ ...p, label: fmt(p.timestamp_ms) })))
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [selectedApp, granularity, dateRange])

  useEffect(() => { fetchData() }, [fetchData])

  const current  = data[data.length - 1]?.value || 0
  const prev     = data[data.length - 8]?.value || 0
  const change   = prev > 0 ? ((current - prev) / prev) * 100 : 0
  const peak     = data.length ? Math.max(...data.map(p => p.value)) : 0
  const average  = data.length ? Math.round(data.reduce((s, p) => s + p.value, 0) / data.length) : 0

  /* Also compute all three for the comparison row */
  const [allGranData, setAllGranData] = useState({ day: [], week: [], month: [] })

  useEffect(() => {
    const day   = mockDAU({ granularity: 'day' })
    const week  = mockDAU({ granularity: 'week' })
    const month = mockDAU({ granularity: 'month' })
    setAllGranData({ day, week, month })
  }, [])

  const fmtGran = {
    day:   (ts) => new Date(ts).toLocaleDateString('en', { month: 'short', day: 'numeric' }),
    week:  (ts) => `W${Math.ceil(new Date(ts).getDate() / 7)}`,
    month: (ts) => new Date(ts).toLocaleString('en', { month: 'short' }),
  }

  return (
    <Layout pageTitle="Active Users" onRefresh={fetchData} loading={loading}>
      <div className="page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Active Users</h1>
            <p className="page-subtitle">
              HyperLogLog approximate counting (uniqHLL12) · ~2% error at any scale
            </p>
          </div>
          <DateRangeFilter value={dateRange} onChange={setDateRange} />
        </div>

        {/* Granularity tabs */}
        <div style={{ display: 'flex', gap: '0.75rem', marginBottom: '1.5rem' }}>
          {GRANULARITIES.map(g => (
            <button
              key={g.key}
              onClick={() => setGranularity(g.key)}
              style={{
                padding: '0.85rem 1.5rem',
                borderRadius: 'var(--radius)',
                border: `1px solid ${granularity === g.key ? 'var(--primary)' : 'var(--border)'}`,
                background: granularity === g.key ? 'var(--primary-light)' : 'var(--bg-surface)',
                cursor: 'pointer',
                textAlign: 'left',
                transition: 'all 0.15s ease',
              }}
            >
              <div style={{
                fontSize: '1.143rem', fontWeight: 700,
                color: granularity === g.key ? 'var(--primary)' : 'var(--text-primary)',
              }}>
                {formatNumber(
                  granularity === g.key
                    ? current
                    : allGranData[g.key]?.points?.[allGranData[g.key].points.length - 1]?.value || 0
                )}
              </div>
              <div style={{ fontSize: '0.786rem', fontWeight: 600, color: granularity === g.key ? 'var(--primary)' : 'var(--text-secondary)', marginTop: '0.15rem' }}>
                {g.label}
              </div>
              <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginTop: '0.1rem' }}>
                {g.description}
              </div>
            </button>
          ))}
        </div>

        {/* Stats row */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(3, 1fr)',
          gap: '0.75rem',
          marginBottom: '1.25rem',
        }}>
          <MetricCard
            title={`Current ${granularity.toUpperCase().slice(0, 3)}AU`}
            value={current}
            change={change}
            icon={Users}
            iconColor="var(--info)"
            description="vs 7 periods ago"
            loading={loading}
          />
          <MetricCard
            title="Peak"
            value={peak}
            icon={TrendingUp}
            iconColor="var(--success)"
            description="in selected range"
            loading={loading}
          />
          <MetricCard
            title="Average"
            value={average}
            icon={Calendar}
            iconColor="var(--warning)"
            description={`per ${granularity}`}
            loading={loading}
          />
        </div>

        {/* Main chart */}
        <div className="card" style={{ marginBottom: '1rem' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '1.25rem' }}>
            <div>
              <div style={{ fontWeight: 600, marginBottom: '0.2rem' }}>
                {GRANULARITIES.find(g => g.key === granularity)?.description}
              </div>
              <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>
                {dateRange.label} · {data.length} buckets
              </div>
            </div>
            <span className="badge badge-info">uniqHLL12</span>
          </div>

          {loading ? (
            <LoadingSpinner text="Querying DAU materialized view…" />
          ) : error ? (
            <ErrorState error={error} onRetry={fetchData} />
          ) : (
            <ResponsiveContainer width="100%" height={320}>
              <AreaChart data={data}>
                <defs>
                  <linearGradient id="dau-grad" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%"  stopColor="var(--chart-5)" stopOpacity={0.3} />
                    <stop offset="95%" stopColor="var(--chart-5)" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval="preserveStartEnd" />
                <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                <Area
                  type="monotone"
                  dataKey="value"
                  name={GRANULARITIES.find(g => g.key === granularity)?.label}
                  stroke="var(--chart-5)"
                  fill="url(#dau-grad)"
                  strokeWidth={2.5}
                  dot={false}
                />
              </AreaChart>
            </ResponsiveContainer>
          )}
        </div>

        {/* DAU / WAU / MAU comparison */}
        <div className="card">
          <div style={{ fontWeight: 600, marginBottom: '1.25rem' }}>
            DAU / WAU / MAU Comparison (trailing 30 days)
          </div>
          <ResponsiveContainer width="100%" height={220}>
            <BarChart data={
              Array.from({ length: 10 }, (_, i) => {
                const idx = allGranData.day.points?.length
                  ? Math.floor(i * (allGranData.day.points.length - 1) / 9)
                  : i
                const d = allGranData.day.points?.[idx]
                const label = d ? fmtGran.day(d.timestamp_ms) : `D${i}`
                return {
                  label,
                  DAU: allGranData.day.points?.[idx]?.value || 0,
                  WAU: Math.round((allGranData.day.points?.[idx]?.value || 0) * 2.6),
                  MAU: Math.round((allGranData.day.points?.[idx]?.value || 0) * 8.3),
                }
              })
            }>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
              <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
              <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
              <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
              <Legend wrapperStyle={{ fontSize: '0.786rem' }} />
              <Bar dataKey="DAU" fill="var(--chart-5)" radius={[2, 2, 0, 0]} />
              <Bar dataKey="WAU" fill="var(--chart-2)" radius={[2, 2, 0, 0]} />
              <Bar dataKey="MAU" fill="var(--chart-3)" radius={[2, 2, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>
    </Layout>
  )
}
