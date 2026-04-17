import React, { useState, useEffect, useCallback } from 'react'
import { RotateCcw, Download } from 'lucide-react'
import Layout from '../components/Layout.jsx'
import DateRangeFilter, { defaultDateRange } from '../components/DateRangeFilter.jsx'
import { LoadingSpinner, ErrorState, EmptyState } from '../components/LoadingState.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { getRetention } from '../api/queryapi.js'
import { mockRetention } from '../hooks/useMockData.js'

const DEFAULT_DAYS = [1, 3, 7, 14, 30]

function retentionColor(rate) {
  if (rate === null || rate === undefined) return 'var(--bg-elevated)'
  if (rate >= 0.4) return '#064e3b'
  if (rate >= 0.3) return '#065f46'
  if (rate >= 0.2) return '#047857'
  if (rate >= 0.15) return '#059669'
  if (rate >= 0.1) return '#10b981'
  if (rate >= 0.06) return '#34d399'
  if (rate >= 0.03) return '#6ee7b7'
  return '#a7f3d0'
}

function retentionTextColor(rate) {
  if (rate === null || rate === undefined) return 'var(--text-muted)'
  return rate >= 0.15 ? '#f0fdf4' : '#064e3b'
}

export default function Retention() {
  const { selectedApp } = useAuth()
  const [dateRange, setDateRange] = useState(defaultDateRange(60))
  const [dayNs, setDayNs]         = useState([...DEFAULT_DAYS])
  const [data, setData]           = useState(null)
  const [loading, setLoading]     = useState(false)
  const [error, setError]         = useState(null)
  const [customDay, setCustomDay] = useState('')

  const fetchData = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const body = { app_id: selectedApp, from_ms: dateRange.from_ms, to_ms: dateRange.to_ms, day_ns: dayNs }
      let res
      try { res = await getRetention(body) }
      catch { res = mockRetention() }
      setData(res)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }, [selectedApp, dateRange, dayNs])

  useEffect(() => { fetchData() }, [fetchData])

  function addDay() {
    const d = parseInt(customDay)
    if (d > 0 && d <= 365 && !dayNs.includes(d)) {
      setDayNs(prev => [...prev, d].sort((a, b) => a - b))
      setCustomDay('')
    }
  }

  function removeDay(d) {
    if (dayNs.length <= 1) return
    setDayNs(prev => prev.filter(x => x !== d))
  }

  function downloadCSV() {
    if (!data) return
    const headers = ['install_date', 'cohort_size', ...dayNs.map(d => `day_${d}`)]
    const rows = data.cohorts.map(c => [
      c.install_date, c.cohort_size,
      ...dayNs.map(d => (c.day_n_rates[`day_${d}`] !== undefined ? (c.day_n_rates[`day_${d}`] * 100).toFixed(1) + '%' : '—')),
    ])
    const csv = [headers, ...rows].map(r => r.join(',')).join('\n')
    const blob = new Blob([csv], { type: 'text/csv' })
    const url  = URL.createObjectURL(blob)
    const a    = document.createElement('a')
    a.href = url; a.download = `retention-${selectedApp}.csv`; a.click()
    URL.revokeObjectURL(url)
  }

  /* Avg column retention for quick insights */
  const avgByDay = {}
  if (data?.cohorts?.length) {
    dayNs.forEach(d => {
      const rates = data.cohorts.map(c => c.day_n_rates[`day_${d}`]).filter(r => r !== undefined)
      avgByDay[d] = rates.length ? rates.reduce((s, r) => s + r, 0) / rates.length : null
    })
  }

  return (
    <Layout pageTitle="Retention" onRefresh={fetchData} loading={loading}>
      <div className="page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Retention Analysis</h1>
            <p className="page-subtitle">
              Day-N cohort retention — % of users who returned after install date
            </p>
          </div>
          <div style={{ display: 'flex', gap: '0.75rem' }}>
            <DateRangeFilter value={dateRange} onChange={setDateRange} />
            <button className="btn btn-secondary btn-sm" onClick={downloadCSV} disabled={!data}>
              <Download size={13} /> Export
            </button>
          </div>
        </div>

        {/* Day-N configurator */}
        <div className="card" style={{ marginBottom: '1rem', padding: '1rem' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', flexWrap: 'wrap' }}>
            <span style={{ fontSize: '0.857rem', fontWeight: 500, color: 'var(--text-secondary)' }}>
              Retention days:
            </span>
            {dayNs.map(d => (
              <span key={d} className="tag" style={{ cursor: 'pointer' }} onClick={() => removeDay(d)}>
                Day {d} ×
              </span>
            ))}
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', marginLeft: '0.25rem' }}>
              <input
                className="form-input"
                type="number"
                min="1" max="365"
                placeholder="Day N"
                value={customDay}
                onChange={e => setCustomDay(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && addDay()}
                style={{ width: 70, padding: '0.3rem 0.5rem' }}
              />
              <button className="btn btn-secondary btn-sm" onClick={addDay}>Add</button>
            </div>
            <button
              className="btn btn-ghost btn-sm"
              style={{ marginLeft: 'auto' }}
              onClick={() => { setDayNs([...DEFAULT_DAYS]); fetchData() }}
            >
              Reset
            </button>
          </div>
        </div>

        {/* Average row */}
        {data && (
          <div style={{
            display: 'flex', gap: '0.75rem', marginBottom: '1rem', flexWrap: 'wrap',
          }}>
            {dayNs.map(d => (
              <div key={d} className="card-sm" style={{ textAlign: 'center', minWidth: 80 }}>
                <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.2rem' }}>
                  Day {d}
                </div>
                <div style={{
                  fontSize: '1.143rem', fontWeight: 700,
                  color: avgByDay[d] > 0.2 ? 'var(--success)' : avgByDay[d] > 0.1 ? 'var(--warning)' : 'var(--text-primary)',
                }}>
                  {avgByDay[d] !== null ? `${(avgByDay[d] * 100).toFixed(1)}%` : '—'}
                </div>
                <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>avg</div>
              </div>
            ))}
          </div>
        )}

        {/* Heatmap */}
        <div className="card">
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '1.25rem', alignItems: 'center' }}>
            <div>
              <div style={{ fontWeight: 600, marginBottom: '0.2rem' }}>Cohort Retention Heatmap</div>
              <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>
                Each row is an install cohort. Cells show % returning on day N.
              </div>
            </div>
            {/* Legend */}
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.35rem' }}>
              <span style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>Low</span>
              {[0.02, 0.06, 0.1, 0.15, 0.2, 0.3, 0.4].map(v => (
                <div key={v} style={{
                  width: 18, height: 18, borderRadius: 3,
                  background: retentionColor(v),
                }} />
              ))}
              <span style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>High</span>
            </div>
          </div>

          {loading ? (
            <LoadingSpinner text="Computing retention cohorts…" />
          ) : error ? (
            <ErrorState error={error} onRetry={fetchData} />
          ) : !data?.cohorts?.length ? (
            <EmptyState icon={RotateCcw} title="No cohort data" description="No install events found in the selected range." />
          ) : (
            <div style={{ overflowX: 'auto' }}>
              <table style={{ minWidth: '100%', borderCollapse: 'separate', borderSpacing: 3 }}>
                <thead>
                  <tr>
                    <th style={{ textAlign: 'left', padding: '0.4rem 0.75rem', fontWeight: 600, fontSize: '0.786rem', color: 'var(--text-muted)' }}>
                      Install Date
                    </th>
                    <th style={{ textAlign: 'right', padding: '0.4rem 0.75rem', fontWeight: 600, fontSize: '0.786rem', color: 'var(--text-muted)' }}>
                      Cohort
                    </th>
                    {dayNs.map(d => (
                      <th key={d} style={{ textAlign: 'center', padding: '0.4rem 0.75rem', fontWeight: 600, fontSize: '0.786rem', color: 'var(--text-muted)', minWidth: 72 }}>
                        Day {d}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {data.cohorts.map((cohort, i) => (
                    <tr key={i}>
                      <td style={{ padding: '0.4rem 0.75rem', fontSize: '0.857rem', fontWeight: 500, whiteSpace: 'nowrap' }}>
                        {cohort.install_date}
                      </td>
                      <td style={{ padding: '0.4rem 0.75rem', textAlign: 'right', fontSize: '0.857rem', color: 'var(--text-secondary)' }}>
                        {cohort.cohort_size.toLocaleString()}
                      </td>
                      {dayNs.map(d => {
                        const rate = cohort.day_n_rates[`day_${d}`]
                        return (
                          <td key={d} style={{ padding: '0.25rem' }}>
                            <div style={{
                              background: retentionColor(rate),
                              borderRadius: 6,
                              padding: '0.4rem 0.5rem',
                              textAlign: 'center',
                              fontSize: '0.786rem',
                              fontWeight: 600,
                              color: retentionTextColor(rate),
                              minWidth: 60,
                            }}>
                              {rate !== undefined ? `${(rate * 100).toFixed(1)}%` : '—'}
                            </div>
                          </td>
                        )
                      })}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </Layout>
  )
}
