import React from 'react'
import { TrendingUp, TrendingDown, Minus } from 'lucide-react'

export default function MetricCard({ title, value, unit, change, icon: Icon, iconColor, description, loading }) {
  const positive = change > 0
  const neutral  = change === 0 || change === null || change === undefined

  function formatValue(v) {
    if (v === null || v === undefined) return '—'
    if (typeof v === 'number') {
      if (v >= 1_000_000) return (v / 1_000_000).toFixed(1) + 'M'
      if (v >= 1_000)     return (v / 1_000).toFixed(1) + 'K'
      if (v < 1 && v > 0) return (v * 100).toFixed(2) + '%'
      return v.toLocaleString()
    }
    return v
  }

  return (
    <div className="card" style={{ position: 'relative', overflow: 'hidden' }}>
      {/* Subtle gradient top-right glow */}
      <div style={{
        position: 'absolute', top: -20, right: -20,
        width: 80, height: 80, borderRadius: '50%',
        background: iconColor ? `${iconColor}18` : 'var(--primary-light)',
        filter: 'blur(20px)',
        pointerEvents: 'none',
      }} />

      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: '0.75rem' }}>
        <span style={{ fontSize: '0.786rem', fontWeight: 600, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
          {title}
        </span>
        {Icon && (
          <div style={{
            width: 36, height: 36, borderRadius: 'var(--radius-sm)',
            background: iconColor ? `${iconColor}18` : 'var(--primary-light)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <Icon size={18} color={iconColor || 'var(--primary)'} />
          </div>
        )}
      </div>

      {loading ? (
        <div className="skeleton" style={{ height: 36, width: '60%', borderRadius: 6, marginBottom: '0.5rem' }} />
      ) : (
        <div style={{ display: 'flex', alignItems: 'baseline', gap: '0.35rem', marginBottom: '0.4rem' }}>
          <span style={{ fontSize: '1.857rem', fontWeight: 700, letterSpacing: '-0.02em', lineHeight: 1 }}>
            {formatValue(value)}
          </span>
          {unit && (
            <span style={{ fontSize: '0.857rem', color: 'var(--text-secondary)', fontWeight: 500 }}>
              {unit}
            </span>
          )}
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
        {!neutral && !loading && (
          <span className={`stat-chip ${positive ? 'up' : 'down'}`}>
            {positive ? <TrendingUp size={11} /> : <TrendingDown size={11} />}
            {positive ? '+' : ''}{typeof change === 'number' ? change.toFixed(1) : change}%
          </span>
        )}
        {description && (
          <span style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>
            {description}
          </span>
        )}
      </div>
    </div>
  )
}
