import React from 'react'
import { Menu, RefreshCw, ExternalLink, AlertCircle, CheckCircle2 } from 'lucide-react'

export default function Header({ onToggleSidebar, pageTitle, onRefresh, loading }) {
  const [gatewayOk, setGatewayOk] = React.useState(true)
  const [queryOk, setQueryOk]     = React.useState(true)

  React.useEffect(() => {
    async function checkHealth() {
      try {
        await fetch('/api/gateway/health')
        setGatewayOk(true)
      } catch {
        setGatewayOk(false)
      }
      try {
        await fetch('/api/query/health')
        setQueryOk(true)
      } catch {
        setQueryOk(false)
      }
    }
    checkHealth()
    const interval = setInterval(checkHealth, 30000)
    return () => clearInterval(interval)
  }, [])

  return (
    <header style={{
      height: 'var(--header-height)',
      background: 'var(--bg-surface)',
      borderBottom: '1px solid var(--border)',
      display: 'flex',
      alignItems: 'center',
      padding: '0 1.5rem',
      gap: '1rem',
      position: 'sticky',
      top: 0,
      zIndex: 100,
    }}>
      <button className="btn btn-icon btn-ghost" onClick={onToggleSidebar}>
        <Menu size={18} />
      </button>

      <div style={{ flex: 1 }}>
        <span style={{ fontSize: '1rem', fontWeight: 600, color: 'var(--text-primary)' }}>
          {pageTitle}
        </span>
      </div>

      {/* Service health indicators */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: '0.5rem',
        padding: '0.3rem 0.75rem',
        background: 'var(--bg-elevated)',
        borderRadius: 'var(--radius-sm)',
        border: '1px solid var(--border)',
      }}>
        <ServicePill label="Gateway" ok={gatewayOk} />
        <div style={{ width: 1, height: 14, background: 'var(--border-strong)' }} />
        <ServicePill label="Query API" ok={queryOk} />
      </div>

      {onRefresh && (
        <button
          className="btn btn-secondary btn-sm"
          onClick={onRefresh}
          disabled={loading}
        >
          <RefreshCw size={13} style={{ animation: loading ? 'spin 0.7s linear infinite' : 'none' }} />
          Refresh
        </button>
      )}

      <a
        href="http://localhost:8082/swagger/index.html"
        target="_blank" rel="noopener noreferrer"
        className="btn btn-ghost btn-sm"
        title="Open Swagger UI"
      >
        <ExternalLink size={13} />
        API Docs
      </a>
    </header>
  )
}

function ServicePill({ label, ok }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: '0.35rem' }}>
      {ok
        ? <CheckCircle2 size={12} color="var(--success)" />
        : <AlertCircle  size={12} color="var(--danger)"  />
      }
      <span style={{
        fontSize: '0.75rem',
        color: ok ? 'var(--text-secondary)' : 'var(--danger)',
        fontWeight: 500,
      }}>{label}</span>
    </div>
  )
}
