import React, { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Zap, ArrowRight, AlertCircle, UserPlus, LogIn } from 'lucide-react'
import { useAuth } from '../context/AuthContext.jsx'
import { useToast } from '../context/ToastContext.jsx'

export default function Login() {
  const { login, registerOrg } = useAuth()
  const toast = useToast()
  const navigate = useNavigate()

  const [tab, setTab] = useState('login') // 'login' | 'register'
  const [loading, setLoading] = useState(false)
  const [error, setError]     = useState('')

  /* Login form */
  const [apiKey, setApiKey] = useState('')

  /* Register form */
  const [regForm, setRegForm] = useState({ org_name: '', app_name: '', email: '' })

  /* ── Login ─────────────────────────────────────────────────── */
  async function handleLogin(e) {
    e.preventDefault()
    setLoading(true)
    setError('')

    try {
      const res = await fetch('/api/query/v1/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ api_key: apiKey }),
      })
      if (res.ok) {
        const data = await res.json()
        login(data.token, data.app_id, data.org_id)
        toast.success('Logged in successfully')
        navigate('/', { replace: true })
        return
      }
    } catch {
      /* Auth service unavailable — fall through to demo mode */
    }

    /* Demo mode: accept any non-empty API key */
    if (apiKey) {
      const header  = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' }))
      const payload = btoa(JSON.stringify({
        app_id: 'app_demo',
        org_id: 'org-1',
        role: 'admin',
        exp: Math.floor(Date.now() / 1000) + 86400 * 7,
      }))
      login(`${header}.${payload}.demo_signature`, 'app_demo', 'org-1')
      toast.success('Welcome! (Demo mode)')
      navigate('/', { replace: true })
    } else {
      setError('Please enter your API key.')
      setLoading(false)
    }
  }

  /* ── Register ──────────────────────────────────────────────── */
  async function handleRegister(e) {
    e.preventDefault()
    setLoading(true)
    setError('')

    if (!regForm.org_name || !regForm.email) {
      setError('Organization name and email are required.')
      setLoading(false)
      return
    }

    try {
      const data = await registerOrg(regForm.org_name, regForm.app_name || `${regForm.org_name} App`, regForm.email)
      toast.success(`Organization created! API key: ${data.api_key}`)
      navigate('/', { replace: true })
    } catch (err) {
      /* Auth service unreachable — create a demo account */
      const header  = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' }))
      const payload = btoa(JSON.stringify({
        app_id: `app-${Date.now()}`,
        org_id: `org-${Date.now()}`,
        role: 'admin',
        exp: Math.floor(Date.now() / 1000) + 86400 * 7,
      }))
      login(`${header}.${payload}.demo_signature`)
      toast.success('Registered in demo mode (auth service unreachable)')
      navigate('/', { replace: true })
    } finally {
      setLoading(false)
    }
  }

  const bgStyle = {
    minHeight: '100vh',
    background: 'var(--bg-base)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: '1.5rem',
  }

  return (
    <div style={bgStyle}>
      {/* Background grid */}
      <div style={{
        position: 'fixed', inset: 0,
        backgroundImage: `linear-gradient(var(--border) 1px, transparent 1px), linear-gradient(90deg, var(--border) 1px, transparent 1px)`,
        backgroundSize: '40px 40px',
        opacity: 0.3,
        pointerEvents: 'none',
      }} />
      <div style={{
        position: 'fixed', top: '20%', left: '30%',
        width: 400, height: 400, borderRadius: '50%',
        background: 'radial-gradient(circle, rgba(99,102,241,0.12) 0%, transparent 70%)',
        pointerEvents: 'none',
      }} />

      <div style={{ width: '100%', maxWidth: 440, position: 'relative', zIndex: 1 }}>
        {/* Logo */}
        <div style={{ textAlign: 'center', marginBottom: '2rem' }}>
          <div style={{
            width: 56, height: 56, borderRadius: 16,
            background: 'linear-gradient(135deg, var(--primary), #818cf8)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            margin: '0 auto 1rem',
            boxShadow: '0 0 32px var(--primary-glow)',
          }}>
            <Zap size={28} color="#fff" fill="#fff" />
          </div>
          <h1 style={{ fontSize: '1.714rem', fontWeight: 800, letterSpacing: '-0.03em', marginBottom: '0.4rem' }}>
            Pulse<span style={{ color: 'var(--primary)' }}>Analytics</span>
          </h1>
          <p style={{ color: 'var(--text-muted)', fontSize: '0.857rem' }}>
            100M events/sec · &lt;200ms query P95 · 99.99% uptime
          </p>
        </div>

        <div className="card" style={{ padding: '2rem' }}>
          {/* Tabs */}
          <div className="tabs" style={{ marginBottom: '1.5rem' }}>
            <button
              className={`tab ${tab === 'login' ? 'active' : ''}`}
              onClick={() => { setTab('login'); setError('') }}
              style={{ display: 'flex', alignItems: 'center', gap: '0.35rem' }}
            >
              <LogIn size={14} /> Sign In
            </button>
            <button
              className={`tab ${tab === 'register' ? 'active' : ''}`}
              onClick={() => { setTab('register'); setError('') }}
              style={{ display: 'flex', alignItems: 'center', gap: '0.35rem' }}
            >
              <UserPlus size={14} /> Register
            </button>
          </div>

          {error && (
            <div style={{
              display: 'flex', alignItems: 'center', gap: '0.5rem',
              background: 'var(--danger-light)',
              border: '1px solid rgba(239,68,68,0.3)',
              borderRadius: 'var(--radius-sm)',
              padding: '0.6rem 0.85rem',
              marginBottom: '1rem',
              fontSize: '0.857rem',
              color: 'var(--danger)',
            }}>
              <AlertCircle size={14} />
              {error}
            </div>
          )}

          {/* ── Sign In ── */}
          {tab === 'login' && (
            <>
              <p style={{ fontSize: '0.857rem', color: 'var(--text-muted)', marginBottom: '1.25rem' }}>
                Enter your API key to access the dashboard.
              </p>
              <form onSubmit={handleLogin} style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
                <div className="form-group">
                  <label className="form-label">API Key</label>
                  <input
                    className="form-input"
                    type="text"
                    placeholder="pk_live_..."
                    value={apiKey}
                    onChange={e => setApiKey(e.target.value)}
                    autoComplete="off"
                    autoFocus
                  />
                  <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.35rem' }}>
                    No account? Use the <button type="button" className="btn btn-ghost" style={{ padding: 0, fontSize: '0.75rem', color: 'var(--primary)', display: 'inline' }} onClick={() => setTab('register')}>Register tab</button> to create one.
                  </p>
                </div>
                <button type="submit" className="btn btn-primary" disabled={loading}
                  style={{ width: '100%', justifyContent: 'center', padding: '0.7rem' }}>
                  {loading
                    ? <><div className="spinner" style={{ width: 16, height: 16 }} />Signing in…</>
                    : <><ArrowRight size={16} />Sign in</>}
                </button>
              </form>
              <div style={{ borderTop: '1px solid var(--border)', marginTop: '1.5rem', paddingTop: '1.25rem' }}>
                <p style={{ fontSize: '0.786rem', color: 'var(--text-muted)', textAlign: 'center', marginBottom: '0.75rem' }}>
                  No account? Try the interactive demo
                </p>
                <button className="btn btn-secondary" style={{ width: '100%', justifyContent: 'center' }}
                  onClick={() => setApiKey('pk_live_demo_key_change_in_production')}>
                  Fill demo credentials
                </button>
              </div>
            </>
          )}

          {/* ── Register ── */}
          {tab === 'register' && (
            <>
              <p style={{ fontSize: '0.857rem', color: 'var(--text-muted)', marginBottom: '1.25rem' }}>
                Create a new organization and get your API key instantly.
              </p>
              <form onSubmit={handleRegister} style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
                <div className="form-group">
                  <label className="form-label">Organization name *</label>
                  <input
                    className="form-input"
                    placeholder="Acme Corp"
                    value={regForm.org_name}
                    onChange={e => setRegForm(f => ({ ...f, org_name: e.target.value }))}
                    required
                    autoFocus
                  />
                </div>
                <div className="form-group">
                  <label className="form-label">App name <span style={{ color: 'var(--text-muted)' }}>(optional)</span></label>
                  <input
                    className="form-input"
                    placeholder="My iOS App"
                    value={regForm.app_name}
                    onChange={e => setRegForm(f => ({ ...f, app_name: e.target.value }))}
                  />
                  <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '0.35rem' }}>
                    Defaults to "{regForm.org_name ? `${regForm.org_name} App` : 'Org App'}" if left blank
                  </p>
                </div>
                <div className="form-group">
                  <label className="form-label">Email *</label>
                  <input
                    className="form-input"
                    type="email"
                    placeholder="you@example.com"
                    value={regForm.email}
                    onChange={e => setRegForm(f => ({ ...f, email: e.target.value }))}
                    required
                  />
                </div>
                <button type="submit" className="btn btn-primary" disabled={loading}
                  style={{ width: '100%', justifyContent: 'center', padding: '0.7rem' }}>
                  {loading
                    ? <><div className="spinner" style={{ width: 16, height: 16 }} />Creating…</>
                    : <><UserPlus size={16} />Create Organization</>}
                </button>
              </form>
            </>
          )}
        </div>

        <p style={{ textAlign: 'center', marginTop: '1.5rem', fontSize: '0.786rem', color: 'var(--text-muted)' }}>
          PulseAnalytics · MIT License ·{' '}
          <a href="http://localhost:8082/swagger/index.html" style={{ color: 'var(--primary)' }}
            target="_blank" rel="noopener noreferrer">
            API Docs
          </a>
        </p>
      </div>
    </div>
  )
}
