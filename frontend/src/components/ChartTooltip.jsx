import React from 'react'

/* Custom Recharts tooltip wrapper */
export function PulseTooltip({ active, payload, label, formatter, labelFormatter }) {
  if (!active || !payload?.length) return null

  const displayLabel = labelFormatter ? labelFormatter(label) : label

  return (
    <div style={{
      background: 'var(--bg-elevated)',
      border: '1px solid var(--border-strong)',
      borderRadius: 'var(--radius)',
      padding: '0.6rem 0.85rem',
      boxShadow: 'var(--shadow)',
      fontSize: '0.786rem',
    }}>
      {displayLabel && (
        <div style={{ color: 'var(--text-secondary)', marginBottom: '0.4rem', fontWeight: 500 }}>
          {displayLabel}
        </div>
      )}
      {payload.map((entry, i) => {
        const val = formatter ? formatter(entry.value, entry.name) : entry.value
        return (
          <div key={i} style={{
            display: 'flex', alignItems: 'center', gap: '0.5rem',
            marginBottom: i < payload.length - 1 ? '0.2rem' : 0,
          }}>
            <div style={{
              width: 8, height: 8, borderRadius: '50%',
              background: entry.color || entry.fill,
              flexShrink: 0,
            }} />
            <span style={{ color: 'var(--text-secondary)' }}>{entry.name}:</span>
            <span style={{ fontWeight: 600, color: 'var(--text-primary)' }}>
              {Array.isArray(val) ? val[0] : val}
            </span>
          </div>
        )
      })}
    </div>
  )
}

export function formatNumber(v) {
  if (v >= 1_000_000) return (v / 1_000_000).toFixed(1) + 'M'
  if (v >= 1_000)     return (v / 1_000).toFixed(1) + 'K'
  return v?.toLocaleString() || '0'
}

export function formatTs(ts) {
  const d = new Date(ts)
  return d.toLocaleDateString('en', { month: 'short', day: 'numeric' })
}

export const CHART_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
  'var(--chart-7)', 'var(--chart-8)',
]
