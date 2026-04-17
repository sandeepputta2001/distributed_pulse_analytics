import React, { useState } from 'react'
import { Calendar, ChevronDown } from 'lucide-react'

const PRESETS = [
  { label: 'Last 24h', days: 1 },
  { label: 'Last 7 days', days: 7 },
  { label: 'Last 14 days', days: 14 },
  { label: 'Last 30 days', days: 30 },
  { label: 'Last 90 days', days: 90 },
]

export default function DateRangeFilter({ value, onChange }) {
  const [open, setOpen] = useState(false)
  const { from_ms, to_ms, label } = value

  function applyPreset(preset) {
    const to   = Date.now()
    const from = to - preset.days * 86400000
    onChange({ from_ms: from, to_ms: to, label: preset.label })
    setOpen(false)
  }

  function applyCustom(e) {
    e.preventDefault()
    const fd = new FormData(e.target)
    const from = new Date(fd.get('from')).getTime()
    const to   = new Date(fd.get('to')).getTime()
    if (from && to && from < to) {
      onChange({ from_ms: from, to_ms: to, label: 'Custom range' })
      setOpen(false)
    }
  }

  return (
    <div style={{ position: 'relative' }}>
      <button
        className="btn btn-secondary btn-sm"
        onClick={() => setOpen(o => !o)}
      >
        <Calendar size={13} />
        {label || 'Last 30 days'}
        <ChevronDown size={12} style={{ marginLeft: 2 }} />
      </button>

      {open && (
        <>
          <div
            style={{ position: 'fixed', inset: 0, zIndex: 299 }}
            onClick={() => setOpen(false)}
          />
          <div style={{
            position: 'absolute',
            top: 'calc(100% + 6px)',
            right: 0,
            background: 'var(--bg-surface)',
            border: '1px solid var(--border-strong)',
            borderRadius: 'var(--radius)',
            padding: '0.5rem',
            zIndex: 300,
            minWidth: 240,
            boxShadow: 'var(--shadow-lg)',
          }}>
            <div style={{ marginBottom: '0.5rem', padding: '0 0.5rem' }}>
              <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.35rem', textTransform: 'uppercase', letterSpacing: '0.05em', fontWeight: 600 }}>
                Quick ranges
              </div>
              {PRESETS.map(p => (
                <button
                  key={p.days}
                  className="btn btn-ghost"
                  style={{ width: '100%', justifyContent: 'flex-start', padding: '0.4rem 0.5rem', borderRadius: 4, marginBottom: '0.1rem' }}
                  onClick={() => applyPreset(p)}
                >
                  {p.label}
                </button>
              ))}
            </div>

            <div style={{ borderTop: '1px solid var(--border)', padding: '0.5rem' }}>
              <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', marginBottom: '0.5rem', textTransform: 'uppercase', letterSpacing: '0.05em', fontWeight: 600 }}>
                Custom range
              </div>
              <form onSubmit={applyCustom} style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem' }}>
                <div className="form-group">
                  <label className="form-label">From</label>
                  <input
                    name="from"
                    type="datetime-local"
                    className="form-input"
                    defaultValue={from_ms ? new Date(from_ms).toISOString().slice(0, 16) : ''}
                  />
                </div>
                <div className="form-group">
                  <label className="form-label">To</label>
                  <input
                    name="to"
                    type="datetime-local"
                    className="form-input"
                    defaultValue={to_ms ? new Date(to_ms).toISOString().slice(0, 16) : ''}
                  />
                </div>
                <button type="submit" className="btn btn-primary btn-sm">Apply</button>
              </form>
            </div>
          </div>
        </>
      )}
    </div>
  )
}

/* Initialize with last 30 days */
export function defaultDateRange(days = 30) {
  const to   = Date.now()
  const from = to - days * 86400000
  return { from_ms: from, to_ms: to, label: `Last ${days} days` }
}
