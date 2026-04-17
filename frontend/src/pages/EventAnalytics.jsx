import React, { useState, useEffect, useCallback } from 'react'
import {
  AreaChart, Area, BarChart, Bar, LineChart, Line,
  XAxis, YAxis, CartesianGrid, ResponsiveContainer, Tooltip, Legend,
} from 'recharts'
import { Filter, Download, Plus, X } from 'lucide-react'
import Layout from '../components/Layout.jsx'
import MetricCard from '../components/MetricCard.jsx'
import DateRangeFilter, { defaultDateRange } from '../components/DateRangeFilter.jsx'
import { PulseTooltip, formatNumber } from '../components/ChartTooltip.jsx'
import { LoadingSpinner, ErrorState } from '../components/LoadingState.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { getEventCount } from '../api/queryapi.js'
import { mockEventCount } from '../hooks/useMockData.js'

const GRANULARITIES = ['minute', 'hour', 'day', 'week', 'month']
const CHART_TYPES   = ['area', 'bar', 'line']

const COMMON_EVENTS = [
  'app_opened', 'purchase_completed', 'user_registered', 'page_viewed',
  'button_clicked', 'feature_used', 'session_started', 'cart_added',
]

export default function EventAnalytics() {
  const { selectedApp } = useAuth()
  const [dateRange, setDateRange]   = useState(defaultDateRange(30))
  const [granularity, setGranularity] = useState('day')
  const [chartType, setChartType]   = useState('area')
  const [eventFilters, setEventFilters] = useState([{ event_name: '', color: 'var(--chart-1)' }])
  const [data, setData]             = useState([])
  const [multiData, setMultiData]   = useState([])
  const [loading, setLoading]       = useState(false)
  const [error, setError]           = useState(null)
  const [total, setTotal]           = useState(0)

  const COLORS = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)']

  const fetchData = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const activeFilters = eventFilters.filter(f => f.event_name)
      if (activeFilters.length === 0) {
        /* Fetch all events combined */
        const params = {
          app_id: selectedApp,
          granularity,
          from_ms: dateRange.from_ms,
          to_ms:   dateRange.to_ms,
        }
        let result
        try {
          result = await getEventCount(params)
        } catch {
          result = mockEventCount({ ...params })
        }
        const fmt = (ts) => {
          const d = new Date(ts)
          if (granularity === 'hour' || granularity === 'minute') return d.toLocaleTimeString('en', { hour: '2-digit', minute: '2-digit' })
          if (granularity === 'month') return d.toLocaleString('en', { month: 'short', year: '2-digit' })
          return d.toLocaleDateString('en', { month: 'short', day: 'numeric' })
        }
        const points = result.points.map(p => ({ ...p, label: p.label || fmt(p.timestamp_ms) }))
        setData(points)
        setTotal(result.total || points.reduce((s, p) => s + p.value, 0))
        setMultiData([])
      } else {
        /* Multi-event overlay */
        const allResults = await Promise.all(
          activeFilters.map(f =>
            getEventCount({ app_id: selectedApp, event_name: f.event_name, granularity, from_ms: dateRange.from_ms, to_ms: dateRange.to_ms })
              .catch(() => mockEventCount({ granularity, from_ms: dateRange.from_ms, to_ms: dateRange.to_ms }))
          )
        )
        /* Merge all into one timeline keyed by timestamp */
        const map = {}
        const fmt = (ts) => new Date(ts).toLocaleDateString('en', { month: 'short', day: 'numeric' })
        allResults.forEach((res, i) => {
          const name = activeFilters[i].event_name
          res.points.forEach(p => {
            const key = p.timestamp_ms
            if (!map[key]) map[key] = { timestamp_ms: key, label: fmt(key) }
            map[key][name] = p.value
          })
        })
        setMultiData(Object.values(map).sort((a, b) => a.timestamp_ms - b.timestamp_ms))
        setData([])
        setTotal(allResults.reduce((s, r) => s + (r.total || 0), 0))
      }
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [selectedApp, dateRange, granularity, eventFilters])

  useEffect(() => { fetchData() }, [fetchData])

  function addFilter() {
    setEventFilters(f => [...f, { event_name: '', color: COLORS[f.length % COLORS.length] }])
  }

  function removeFilter(i) {
    setEventFilters(f => f.filter((_, idx) => idx !== i))
  }

  function updateFilter(i, value) {
    setEventFilters(f => f.map((item, idx) => idx === i ? { ...item, event_name: value } : item))
  }

  const activeFilters = eventFilters.filter(f => f.event_name)
  const chartData     = activeFilters.length > 0 ? multiData : data

  function downloadCSV() {
    const headers = ['timestamp', 'label', ...(activeFilters.length > 0 ? activeFilters.map(f => f.event_name) : ['value'])]
    const rows = chartData.map(p => [p.timestamp_ms, p.label, ...(activeFilters.length > 0 ? activeFilters.map(f => p[f.event_name] || 0) : [p.value])])
    const csv = [headers, ...rows].map(r => r.join(',')).join('\n')
    const blob = new Blob([csv], { type: 'text/csv' })
    const url  = URL.createObjectURL(blob)
    const a    = document.createElement('a')
    a.href = url; a.download = `event-counts-${selectedApp}.csv`; a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Layout pageTitle="Event Analytics" onRefresh={fetchData} loading={loading}>
      <div className="page">
        {/* Header */}
        <div className="page-header">
          <div>
            <h1 className="page-title">Event Analytics</h1>
            <p className="page-subtitle">
              Event counts over time from ClickHouse Materialized Views
            </p>
          </div>
          <div style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap' }}>
            <DateRangeFilter value={dateRange} onChange={setDateRange} />
            <button className="btn btn-secondary btn-sm" onClick={downloadCSV}>
              <Download size={13} />
              Export CSV
            </button>
          </div>
        </div>

        {/* Filter bar */}
        <div className="card" style={{ marginBottom: '1rem', padding: '1rem' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', flexWrap: 'wrap' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', color: 'var(--text-muted)' }}>
              <Filter size={14} />
              <span style={{ fontSize: '0.857rem', fontWeight: 500 }}>Filters</span>
            </div>

            {/* Event name filters */}
            {eventFilters.map((f, i) => (
              <div key={i} style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
                <div style={{ width: 8, height: 8, borderRadius: '50%', background: f.color, flexShrink: 0 }} />
                <select
                  className="form-select"
                  style={{ minWidth: 180, padding: '0.35rem 0.6rem' }}
                  value={f.event_name}
                  onChange={e => updateFilter(i, e.target.value)}
                >
                  <option value="">All events</option>
                  {COMMON_EVENTS.map(ev => (
                    <option key={ev} value={ev}>{ev}</option>
                  ))}
                </select>
                {eventFilters.length > 1 && (
                  <button className="btn btn-icon btn-ghost" onClick={() => removeFilter(i)}>
                    <X size={12} />
                  </button>
                )}
              </div>
            ))}

            {eventFilters.length < 5 && (
              <button className="btn btn-ghost btn-sm" onClick={addFilter}>
                <Plus size={13} />
                Add event
              </button>
            )}

            <div style={{ marginLeft: 'auto', display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
              {/* Granularity */}
              <div style={{ display: 'flex', gap: '0.2rem' }}>
                {GRANULARITIES.map(g => (
                  <button
                    key={g}
                    className={`btn btn-sm ${granularity === g ? 'btn-primary' : 'btn-ghost'}`}
                    style={{ padding: '0.25rem 0.6rem' }}
                    onClick={() => setGranularity(g)}
                  >
                    {g}
                  </button>
                ))}
              </div>

              {/* Chart type */}
              <div style={{ display: 'flex', gap: '0.2rem', marginLeft: '0.5rem' }}>
                {CHART_TYPES.map(t => (
                  <button
                    key={t}
                    className={`btn btn-sm ${chartType === t ? 'btn-primary' : 'btn-ghost'}`}
                    style={{ padding: '0.25rem 0.6rem', textTransform: 'capitalize' }}
                    onClick={() => setChartType(t)}
                  >
                    {t}
                  </button>
                ))}
              </div>
            </div>
          </div>
        </div>

        {/* KPI row */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '0.75rem', marginBottom: '1rem' }}>
          <div className="card-sm" style={{ textAlign: 'center' }}>
            <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.25rem', textTransform: 'uppercase', letterSpacing: '0.05em' }}>Total Events</div>
            <div style={{ fontSize: '1.571rem', fontWeight: 700 }}>{formatNumber(total)}</div>
          </div>
          <div className="card-sm" style={{ textAlign: 'center' }}>
            <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.25rem', textTransform: 'uppercase', letterSpacing: '0.05em' }}>Daily Average</div>
            <div style={{ fontSize: '1.571rem', fontWeight: 700 }}>
              {data.length > 0 ? formatNumber(Math.round(total / data.length)) : '—'}
            </div>
          </div>
          <div className="card-sm" style={{ textAlign: 'center' }}>
            <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.25rem', textTransform: 'uppercase', letterSpacing: '0.05em' }}>Peak Day</div>
            <div style={{ fontSize: '1.571rem', fontWeight: 700 }}>
              {data.length > 0 ? formatNumber(Math.max(...data.map(p => p.value))) : '—'}
            </div>
          </div>
        </div>

        {/* Main chart */}
        <div className="card">
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '1.25rem', alignItems: 'flex-start' }}>
            <div>
              <div style={{ fontWeight: 600, marginBottom: '0.2rem' }}>
                {activeFilters.length === 0 ? 'All Events' : activeFilters.map(f => f.event_name).join(' vs ')}
              </div>
              <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>
                {dateRange.label} · {granularity} granularity · {chartData.length} buckets
              </div>
            </div>
            <span className="badge badge-primary">ClickHouse MV</span>
          </div>

          {loading ? (
            <LoadingSpinner text="Querying ClickHouse…" />
          ) : error ? (
            <ErrorState error={error} onRetry={fetchData} />
          ) : chartData.length === 0 ? (
            <div className="empty-state" style={{ minHeight: 280 }}>
              <p>No data for the selected range</p>
            </div>
          ) : (
            <ResponsiveContainer width="100%" height={320}>
              {activeFilters.length > 0 ? (
                /* Multi-event chart */
                chartType === 'bar' ? (
                  <BarChart data={multiData}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval="preserveStartEnd" />
                    <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                    <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                    <Legend wrapperStyle={{ fontSize: '0.786rem' }} />
                    {activeFilters.map((f, i) => (
                      <Bar key={f.event_name} dataKey={f.event_name} fill={COLORS[i]} radius={[3, 3, 0, 0]} />
                    ))}
                  </BarChart>
                ) : (
                  <LineChart data={multiData}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval="preserveStartEnd" />
                    <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                    <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                    <Legend wrapperStyle={{ fontSize: '0.786rem' }} />
                    {activeFilters.map((f, i) => (
                      <Line key={f.event_name} type="monotone" dataKey={f.event_name} stroke={COLORS[i]} strokeWidth={2} dot={false} />
                    ))}
                  </LineChart>
                )
              ) : (
                /* Single-event chart */
                chartType === 'bar' ? (
                  <BarChart data={data}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval="preserveStartEnd" />
                    <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                    <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                    <Bar dataKey="value" name="Events" fill="var(--chart-1)" radius={[3, 3, 0, 0]} />
                  </BarChart>
                ) : chartType === 'line' ? (
                  <LineChart data={data}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval="preserveStartEnd" />
                    <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                    <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                    <Line type="monotone" dataKey="value" name="Events" stroke="var(--chart-1)" strokeWidth={2} dot={false} />
                  </LineChart>
                ) : (
                  <AreaChart data={data}>
                    <defs>
                      <linearGradient id="area-grad" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%"  stopColor="var(--chart-1)" stopOpacity={0.25} />
                        <stop offset="95%" stopColor="var(--chart-1)" stopOpacity={0} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval="preserveStartEnd" />
                    <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                    <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                    <Area type="monotone" dataKey="value" name="Events" stroke="var(--chart-1)" fill="url(#area-grad)" strokeWidth={2} dot={false} />
                  </AreaChart>
                )
              )}
            </ResponsiveContainer>
          )}
        </div>
      </div>
    </Layout>
  )
}
