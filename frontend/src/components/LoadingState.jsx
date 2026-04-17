import React from 'react'
import { AlertCircle, RefreshCw } from 'lucide-react'

export function LoadingSpinner({ size = 20, text }) {
  return (
    <div className="loading-overlay" style={{ flexDirection: 'column', gap: '0.75rem' }}>
      <div className="spinner" style={{ width: size, height: size }} />
      {text && <span style={{ color: 'var(--text-muted)', fontSize: '0.857rem' }}>{text}</span>}
    </div>
  )
}

export function ErrorState({ error, onRetry }) {
  return (
    <div className="empty-state">
      <AlertCircle size={40} color="var(--danger)" style={{ opacity: 0.8 }} />
      <h3 style={{ color: 'var(--text-secondary)' }}>Failed to load data</h3>
      <p style={{ fontSize: '0.857rem', maxWidth: 360 }}>{error}</p>
      {onRetry && (
        <button className="btn btn-secondary btn-sm" onClick={onRetry} style={{ marginTop: '0.5rem' }}>
          <RefreshCw size={13} />
          Try again
        </button>
      )}
    </div>
  )
}

export function EmptyState({ icon: Icon, title, description, action }) {
  return (
    <div className="empty-state">
      {Icon && <Icon size={40} />}
      {title && <h3>{title}</h3>}
      {description && <p style={{ fontSize: '0.857rem' }}>{description}</p>}
      {action}
    </div>
  )
}

export function SkeletonCard() {
  return (
    <div className="card" style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      <div className="skeleton" style={{ height: 14, width: '40%' }} />
      <div className="skeleton" style={{ height: 36, width: '60%' }} />
      <div className="skeleton" style={{ height: 12, width: '30%' }} />
    </div>
  )
}

export function SkeletonChart() {
  return (
    <div className="card" style={{ minHeight: 280 }}>
      <div className="skeleton" style={{ height: 14, width: '30%', marginBottom: '1rem' }} />
      <div style={{
        display: 'flex',
        alignItems: 'flex-end',
        gap: 6,
        height: 200,
        padding: '0 1rem',
      }}>
        {Array.from({ length: 14 }).map((_, i) => (
          <div
            key={i}
            className="skeleton"
            style={{
              flex: 1,
              height: `${30 + Math.random() * 70}%`,
              borderRadius: '3px 3px 0 0',
            }}
          />
        ))}
      </div>
    </div>
  )
}
