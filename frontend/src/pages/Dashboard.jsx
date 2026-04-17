import React, { useState, useEffect } from 'react'
import {
  AreaChart, Area, BarChart, Bar, XAxis, YAxis, CartesianGrid,
  ResponsiveContainer, Tooltip,
} from 'recharts'
import {
  Activity, Users, Clock, DollarSign, Zap, AlertTriangle,
  TrendingUp, Server, Database,
} from 'lucide-react'
import Layout from '../components/Layout.jsx'
import MetricCard from '../components/MetricCard.jsx'
import { PulseTooltip, formatNumber, formatTs } from '../components/ChartTooltip.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import {
  mockDashboardStats, mockEventCount, mockDAU,
  mockFunnelQuery, mockSessionMetrics, mockAlerts,
} from '../hooks/useMockData.js'

export default function Dashboard() {
  const { selectedApp } = useAuth()
  const [loading, setLoading]   = useState(true)
  const [stats, setStats]       = useState(null)
  const [eventData, setEventData] = useState([])
  const [dauData, setDauData]   = useState([])
  const [alerts, setAlerts]     = useState([])
  const [funnel, setFunnel]     = useState(null)

  useEffect(() => {
    setLoading(true)
    setTimeout(() => {
      setStats(mockDashboardStats())
      const ec = mockEventCount({ granularity: 'day' })
      setEventData(ec.points.map(p => ({
        ...p,
        label: new Date(p.timestamp_ms).toLocaleDateString('en', { month: 'short', day: 'numeric' }),
      })))
      const dau = mockDAU({ granularity: 'day' })
      setDauData(dau.points.map(p => ({
        ...p,
        label: new Date(p.timestamp_ms).toLocaleDateString('en', { month: 'short', day: 'numeric' }),
      })))
      setAlerts(mockAlerts(selectedApp))
      setFunnel(mockFunnelQuery(['app_installed', 'user_registered', 'onboarding_complete', 'purchase_completed']))
      setLoading(false)
    }, 600)
  }, [selectedApp])

  function refresh() {
    setLoading(true)
    setTimeout(() => {
      setStats(mockDashboardStats())
      setLoading(false)
    }, 400)
  }

  const activeAlerts = alerts.filter(a => a.active && a.last_fired_at)

  return (
    <Layout pageTitle="Dashboard" onRefresh={refresh} loading={loading}>
      <div className="page">
        {/* KPI cards */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
          gap: '1rem',
          marginBottom: '1.5rem',
        }}>
          <MetricCard
            title="Events Today"
            value={stats?.total_events_today}
            change={stats?.total_events_change}
            icon={Activity}
            iconColor="var(--primary)"
            description="vs. yesterday"
            loading={loading}
          />
          <MetricCard
            title="Daily Active Users"
            value={stats?.dau}
            change={stats?.dau_change}
            icon={Users}
            iconColor="var(--info)"
            description="vs. yesterday"
            loading={loading}
          />
          <MetricCard
            title="Sessions Today"
            value={stats?.total_sessions_today}
            change={stats?.sessions_change}
            icon={Clock}
            iconColor="var(--success)"
            description="vs. yesterday"
            loading={loading}
          />
          <MetricCard
            title="Revenue Today"
            value={stats?.revenue_today}
            unit="USD"
            change={stats?.revenue_change}
            icon={DollarSign}
            iconColor="var(--warning)"
            description="vs. yesterday"
            loading={loading}
          />
        </div>

        {/* System metrics row */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))',
          gap: '0.75rem',
          marginBottom: '1.5rem',
        }}>
          <SystemMetric icon={Zap}          label="Gateway P95"     value={`${stats?.p95_latency_ms || '—'} ms`}  ok={stats?.p95_latency_ms < 200} />
          <SystemMetric icon={AlertTriangle} label="Error Rate"      value={`${((stats?.error_rate || 0) * 100).toFixed(2)}%`} ok={stats?.error_rate < 0.01} />
          <SystemMetric icon={Server}        label="Kafka Lag"        value={formatNumber(stats?.kafka_lag)}        ok={stats?.kafka_lag < 1000000} />
          <SystemMetric icon={Database}      label="Avg Session"     value={`${stats?.avg_session_duration || '—'} s`} ok />
        </div>

        {/* Charts row */}
        <div style={{
          display: 'grid',
          gridTemplateColumns: '2fr 1fr',
          gap: '1rem',
          marginBottom: '1rem',
        }}>
          {/* Events over time */}
          <div className="card">
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '1.25rem' }}>
              <div>
                <div style={{ fontWeight: 600, marginBottom: '0.2rem' }}>Event Volume (30d)</div>
                <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>
                  Total events ingested per day
                </div>
              </div>
              <div style={{ textAlign: 'right' }}>
                <div style={{ fontSize: '1.286rem', fontWeight: 700 }}>
                  {formatNumber(eventData.reduce((s, p) => s + p.value, 0))}
                </div>
                <div style={{ fontSize: '0.786rem', color: 'var(--success)' }}>+12.4% this month</div>
              </div>
            </div>
            {loading ? (
              <div className="skeleton" style={{ height: 200 }} />
            ) : (
              <ResponsiveContainer width="100%" height={200}>
                <AreaChart data={eventData}>
                  <defs>
                    <linearGradient id="ev-grad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%"  stopColor="var(--chart-1)" stopOpacity={0.3} />
                      <stop offset="95%" stopColor="var(--chart-1)" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                  <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} interval={4} />
                  <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 10 }} tickLine={false} axisLine={false} />
                  <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                  <Area type="monotone" dataKey="value" name="Events" stroke="var(--chart-1)" fill="url(#ev-grad)" strokeWidth={2} dot={false} />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </div>

          {/* DAU mini chart */}
          <div className="card">
            <div style={{ fontWeight: 600, marginBottom: '0.25rem' }}>Active Users (DAU)</div>
            <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)', marginBottom: '1rem' }}>
              Daily unique active users
            </div>
            {loading ? (
              <div className="skeleton" style={{ height: 180 }} />
            ) : (
              <ResponsiveContainer width="100%" height={180}>
                <BarChart data={dauData.slice(-14)}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                  <XAxis dataKey="label" tick={{ fill: 'var(--text-muted)', fontSize: 9 }} tickLine={false} axisLine={false} interval={3} />
                  <YAxis tickFormatter={formatNumber} tick={{ fill: 'var(--text-muted)', fontSize: 9 }} tickLine={false} axisLine={false} />
                  <Tooltip content={<PulseTooltip formatter={formatNumber} />} />
                  <Bar dataKey="value" name="DAU" fill="var(--chart-2)" radius={[3, 3, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>

        {/* Bottom row: funnel + alerts */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
          {/* Quick funnel */}
          <div className="card">
            <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Onboarding Funnel</div>
            {funnel?.steps.map((step, i) => (
              <div key={i} style={{ marginBottom: '0.75rem' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.3rem', fontSize: '0.857rem' }}>
                  <span style={{ color: 'var(--text-secondary)', fontWeight: 500 }}>{step.event_name}</span>
                  <span style={{ fontWeight: 600 }}>
                    {formatNumber(step.user_count)}
                    {i > 0 && (
                      <span style={{ marginLeft: 8, color: 'var(--text-muted)', fontWeight: 400 }}>
                        ({(step.conversion_rate * 100).toFixed(1)}%)
                      </span>
                    )}
                  </span>
                </div>
                <div className="progress-bar">
                  <div className="progress-fill" style={{
                    width: `${step.conversion_rate * 100}%`,
                    background: i === 0 ? 'var(--chart-1)'
                      : step.conversion_rate > 0.6 ? 'var(--chart-2)'
                      : step.conversion_rate > 0.3 ? 'var(--chart-3)'
                      : 'var(--chart-4)',
                  }} />
                </div>
              </div>
            ))}
          </div>

          {/* Active alerts */}
          <div className="card">
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '1rem' }}>
              <div style={{ fontWeight: 600 }}>Alert Status</div>
              {activeAlerts.length > 0 && (
                <span className="badge badge-danger">{activeAlerts.length} fired</span>
              )}
            </div>
            {alerts.length === 0 ? (
              <div className="empty-state" style={{ minHeight: 120 }}>
                <p>No alert rules configured</p>
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '0.6rem' }}>
                {alerts.map(a => (
                  <div key={a.id} style={{
                    display: 'flex', alignItems: 'center', gap: '0.75rem',
                    padding: '0.6rem 0.75rem',
                    background: 'var(--bg-elevated)',
                    borderRadius: 'var(--radius-sm)',
                    border: `1px solid ${a.last_fired_at ? 'rgba(239,68,68,0.3)' : 'var(--border)'}`,
                  }}>
                    <div style={{
                      width: 8, height: 8, borderRadius: '50%', flexShrink: 0,
                      background: a.active && a.last_fired_at ? 'var(--danger)' : a.active ? 'var(--success)' : 'var(--text-muted)',
                      boxShadow: a.active && a.last_fired_at ? '0 0 6px var(--danger)' : 'none',
                    }} />
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontSize: '0.857rem', fontWeight: 500 }}>{a.name}</div>
                      <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>
                        {a.metric_name} {a.condition} {a.threshold}
                      </div>
                    </div>
                    <span className={`badge ${a.active && a.last_fired_at ? 'badge-danger' : a.active ? 'badge-success' : 'badge-default'}`}>
                      {a.active && a.last_fired_at ? 'FIRED' : a.active ? 'OK' : 'OFF'}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </Layout>
  )
}

function SystemMetric({ icon: Icon, label, value, ok }) {
  return (
    <div className="card-sm" style={{
      display: 'flex', alignItems: 'center', gap: '0.75rem',
      borderLeft: `3px solid ${ok ? 'var(--success)' : 'var(--danger)'}`,
    }}>
      <Icon size={16} color={ok ? 'var(--success)' : 'var(--danger)'} />
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)', marginBottom: '0.15rem' }}>{label}</div>
        <div style={{ fontWeight: 700, fontSize: '1rem' }}>{value}</div>
      </div>
    </div>
  )
}
