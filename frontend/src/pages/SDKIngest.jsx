import React, { useState } from 'react'
import { Database, Send, CheckCircle, Copy, Code } from 'lucide-react'
import Layout from '../components/Layout.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { useToast } from '../context/ToastContext.jsx'
import { ingestEvents, trackEvent, identifyUser } from '../api/gateway.js'

const JS_SDK_EXAMPLE = `import { PulseClient } from './sdk/js';

const pulse = new PulseClient({
  baseUrl: 'https://gateway.pulse-analytics.io',
  apiKey:  'pk_live_YOUR_KEY',
  appId:   'app_abc123',
  deviceId: getDeviceId(),
});

// Track an event
pulse.track('purchase_completed', {
  item_id: 'sku_999',
  price: 29.99,
}, 29.99);

// Identify user after login
await pulse.identify('usr_xyz', {
  email: 'alice@example.com',
  plan: 'pro',
});

// Flush on page unload
window.addEventListener('beforeunload', () => pulse.shutdown());`

const GO_SDK_EXAMPLE = `import pulse "github.com/pulse-analytics/sdk/go"

client := pulse.New(
    "https://gateway.pulse-analytics.io",
    "pk_live_YOUR_KEY",
    "app_abc123",
    "device-uuid-here",
    pulse.WithMaxBatch(200),
    pulse.WithFlushInterval(2*time.Second),
)
defer client.Close(ctx)

client.Track(ctx, pulse.Event{
    EventName: "purchase_completed",
    Props:     map[string]any{
        "item_id": "sku_999",
        "price":   29.99,
    },
    Revenue: 29.99,
})

client.Identify(ctx, "usr_xyz", map[string]any{
    "email": "alice@example.com",
    "plan":  "pro",
})

client.Flush(ctx)`

export default function SDKIngest() {
  const { selectedApp } = useAuth()
  const toast = useToast()
  const [tab, setTab] = useState('test')

  /* Test ingest form */
  const [apiKey, setApiKey]     = useState('pk_live_demo_key')
  const [deviceId, setDeviceId] = useState('device-001')
  const [userId, setUserId]     = useState('')
  const [eventName, setEventName] = useState('purchase_completed')
  const [revenue, setRevenue]   = useState('')
  const [props, setProps]       = useState('{"item_id": "sku_999", "price": 29.99}')
  const [mode, setMode]         = useState('batch')
  const [sending, setSending]   = useState(false)
  const [lastResult, setLastResult] = useState(null)

  async function handleSend(e) {
    e.preventDefault()
    setSending(true)
    setLastResult(null)
    try {
      let parsedProps = {}
      try { parsedProps = JSON.parse(props) } catch { toast.error('Invalid JSON in props'); setSending(false); return }

      const event = {
        event_id: crypto.randomUUID?.() || `evt-${Date.now()}`,
        event_name: eventName,
        event_time: Date.now(),
        props: parsedProps,
        ...(revenue ? { revenue: parseFloat(revenue) } : {}),
      }

      let result
      if (mode === 'batch') {
        result = await ingestEvents({
          app_id: selectedApp,
          device_id: deviceId,
          ...(userId ? { user_id: userId } : {}),
          sent_at_ms: Date.now(),
          events: [event],
        }, apiKey).catch(() => ({ accepted: 1, filtered: 0, _demo: true }))
      } else {
        result = await trackEvent({ ...event, app_id: selectedApp, device_id: deviceId }, apiKey)
          .catch(() => ({ accepted: 1, _demo: true }))
      }

      setLastResult(result)
      toast.success(`Event sent! ${result._demo ? '(demo mode)' : ''}`)
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSending(false)
    }
  }

  async function handleIdentify(e) {
    e.preventDefault()
    setSending(true)
    try {
      await identifyUser({ user_id: userId, traits: { identified_at: new Date().toISOString() } }, apiKey)
        .catch(() => null)
      toast.success('Identify event sent')
    } finally {
      setSending(false)
    }
  }

  function copyText(text) {
    navigator.clipboard?.writeText(text)
    toast.success('Copied to clipboard')
  }

  return (
    <Layout pageTitle="SDK & Ingest">
      <div className="page">
        <div className="page-header">
          <div>
            <h1 className="page-title">SDK &amp; Ingest</h1>
            <p className="page-subtitle">
              Test event ingestion · Gateway API reference · Client SDK examples
            </p>
          </div>
        </div>

        <div className="tabs">
          <button className={`tab ${tab === 'test' ? 'active' : ''}`} onClick={() => setTab('test')}>Test Events</button>
          <button className={`tab ${tab === 'js' ? 'active' : ''}`} onClick={() => setTab('js')}>JS SDK</button>
          <button className={`tab ${tab === 'go' ? 'active' : ''}`} onClick={() => setTab('go')}>Go SDK</button>
          <button className={`tab ${tab === 'api' ? 'active' : ''}`} onClick={() => setTab('api')}>API Reference</button>
        </div>

        {tab === 'test' && (
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
            <div className="card">
              <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Send Test Event</div>
              <form onSubmit={handleSend} style={{ display: 'flex', flexDirection: 'column', gap: '0.85rem' }}>
                <div className="form-group">
                  <label className="form-label">API Key</label>
                  <input className="form-input" value={apiKey} onChange={e => setApiKey(e.target.value)} placeholder="pk_live_…" />
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem' }}>
                  <div className="form-group">
                    <label className="form-label">Device ID</label>
                    <input className="form-input" value={deviceId} onChange={e => setDeviceId(e.target.value)} />
                  </div>
                  <div className="form-group">
                    <label className="form-label">User ID (optional)</label>
                    <input className="form-input" value={userId} onChange={e => setUserId(e.target.value)} placeholder="usr_xyz" />
                  </div>
                </div>
                <div className="form-group">
                  <label className="form-label">Event Name</label>
                  <input className="form-input" value={eventName} onChange={e => setEventName(e.target.value)} required />
                </div>
                <div className="form-group">
                  <label className="form-label">Revenue (optional)</label>
                  <input className="form-input" type="number" step="0.01" value={revenue} onChange={e => setRevenue(e.target.value)} placeholder="29.99" />
                </div>
                <div className="form-group">
                  <label className="form-label">Props (JSON)</label>
                  <textarea
                    className="form-textarea"
                    rows={4}
                    value={props}
                    onChange={e => setProps(e.target.value)}
                    style={{ fontFamily: 'monospace', fontSize: '0.814rem' }}
                  />
                </div>
                <div style={{ display: 'flex', gap: '0.5rem' }}>
                  {['batch', 'track'].map(m => (
                    <label key={m} style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', cursor: 'pointer', fontSize: '0.857rem' }}>
                      <input type="radio" name="mode" value={m} checked={mode === m} onChange={() => setMode(m)} style={{ accentColor: 'var(--primary)' }} />
                      POST /v1/{m === 'batch' ? 'events' : 'track'}
                    </label>
                  ))}
                </div>
                <button type="submit" className="btn btn-primary" disabled={sending} style={{ justifyContent: 'center' }}>
                  {sending ? <><div className="spinner" style={{ width: 14, height: 14 }} /> Sending…</> : <><Send size={14} /> Send Event</>}
                </button>
              </form>
            </div>

            <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
              {lastResult && (
                <div className="card" style={{ borderLeft: '3px solid var(--success)' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.75rem', color: 'var(--success)' }}>
                    <CheckCircle size={16} />
                    <span style={{ fontWeight: 600 }}>Event accepted</span>
                  </div>
                  <pre style={{
                    fontFamily: 'monospace', fontSize: '0.786rem',
                    color: 'var(--text-secondary)', whiteSpace: 'pre-wrap',
                    background: 'var(--bg-base)', padding: '0.75rem', borderRadius: 6,
                  }}>
                    {JSON.stringify(lastResult, null, 2)}
                  </pre>
                </div>
              )}

              {/* Identify form */}
              <div className="card">
                <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Identify User</div>
                <form onSubmit={handleIdentify} style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
                  <div className="form-group">
                    <label className="form-label">User ID *</label>
                    <input className="form-input" value={userId} onChange={e => setUserId(e.target.value)} placeholder="usr_xyz" required />
                  </div>
                  <button type="submit" className="btn btn-secondary" disabled={sending || !userId} style={{ justifyContent: 'center' }}>
                    <Send size={13} /> POST /v1/identify
                  </button>
                </form>
              </div>

              {/* Gateway info */}
              <div className="card-sm" style={{ borderLeft: '3px solid var(--primary)' }}>
                <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)', marginBottom: '0.4rem' }}>Gateway constraints</div>
                <div style={{ fontSize: '0.857rem', display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
                  <div>• Max 500 events per batch</div>
                  <div>• Default 10,000 RPS · Burst 50,000</div>
                  <div>• gzip via Content-Encoding header</div>
                  <div>• Returns 202 immediately (async)</div>
                </div>
              </div>
            </div>
          </div>
        )}

        {tab === 'js' && (
          <div className="card">
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '1rem' }}>
              <div style={{ fontWeight: 600 }}>TypeScript / JavaScript SDK</div>
              <button className="btn btn-secondary btn-sm" onClick={() => copyText(JS_SDK_EXAMPLE)}>
                <Copy size={13} /> Copy
              </button>
            </div>
            <pre className="code-block" style={{ fontSize: '0.814rem', lineHeight: 1.6 }}>{JS_SDK_EXAMPLE}</pre>
            <div style={{ marginTop: '1rem', padding: '0.75rem', background: 'var(--bg-elevated)', borderRadius: 'var(--radius-sm)', fontSize: '0.857rem', color: 'var(--text-secondary)' }}>
              <strong style={{ color: 'var(--text-primary)' }}>Features:</strong> Browser + Node.js · keepalive for page-unload delivery · Auto-batching · TypeScript types
            </div>
          </div>
        )}

        {tab === 'go' && (
          <div className="card">
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '1rem' }}>
              <div style={{ fontWeight: 600 }}>Go SDK</div>
              <button className="btn btn-secondary btn-sm" onClick={() => copyText(GO_SDK_EXAMPLE)}>
                <Copy size={13} /> Copy
              </button>
            </div>
            <pre className="code-block" style={{ fontSize: '0.814rem', lineHeight: 1.6 }}>{GO_SDK_EXAMPLE}</pre>
            <div style={{ marginTop: '1rem', padding: '0.75rem', background: 'var(--bg-elevated)', borderRadius: 'var(--radius-sm)', fontSize: '0.857rem', color: 'var(--text-secondary)' }}>
              <strong style={{ color: 'var(--text-primary)' }}>Features:</strong> Thread-safe · Background flush goroutine · gzip compression · Flush on shutdown
            </div>
          </div>
        )}

        {tab === 'api' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
            {[
              { method: 'POST', path: '/v1/events', service: 'Gateway :8080', auth: 'X-API-Key', desc: 'Ingest event batch (up to 500 events). Returns 202 async.' },
              { method: 'POST', path: '/v1/track',  service: 'Gateway :8080', auth: 'X-API-Key', desc: 'Track a single event shorthand.' },
              { method: 'POST', path: '/v1/identify', service: 'Gateway :8080', auth: 'X-API-Key', desc: 'Update user profile traits in MongoDB.' },
              { method: 'GET',  path: '/v1/events/count', service: 'Query API :8082', auth: 'JWT Bearer', desc: 'Event counts over time with granularity (minute/hour/day/week/month).' },
              { method: 'GET',  path: '/v1/dau', service: 'Query API :8082', auth: 'JWT Bearer', desc: 'Daily/Weekly/Monthly active users via HyperLogLog.' },
              { method: 'POST', path: '/v1/funnels/query', service: 'Query API :8082', auth: 'JWT Bearer', desc: 'Funnel conversion analysis using windowFunnel().' },
              { method: 'POST', path: '/v1/retention', service: 'Query API :8082', auth: 'JWT Bearer', desc: 'Day-N cohort retention analysis.' },
              { method: 'GET',  path: '/v1/sessions/metrics', service: 'Query API :8082', auth: 'JWT Bearer', desc: 'Session aggregates: avg duration, total sessions, events/session.' },
              { method: 'POST', path: '/v1/funnels', service: 'Query API :8082', auth: 'JWT Bearer', desc: 'Create named funnel definition.' },
              { method: 'GET',  path: '/v1/funnels/{app_id}', service: 'Query API :8082', auth: 'JWT Bearer', desc: 'List all funnel definitions for an app.' },
            ].map(ep => (
              <div key={ep.path} className="card-sm" style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
                <span style={{
                  background: ep.method === 'GET' ? 'var(--info-light)' : 'var(--primary-light)',
                  color: ep.method === 'GET' ? 'var(--info)' : 'var(--primary)',
                  padding: '0.2rem 0.6rem', borderRadius: 4, fontWeight: 700, fontSize: '0.786rem',
                  minWidth: 50, textAlign: 'center', flexShrink: 0,
                }}>{ep.method}</span>
                <code style={{ color: 'var(--text-primary)', flex: 1, fontSize: '0.857rem' }}>{ep.path}</code>
                <span className="tag" style={{ flexShrink: 0 }}>{ep.service}</span>
                <span style={{ fontSize: '0.786rem', color: 'var(--text-muted)', flex: 2 }}>{ep.desc}</span>
              </div>
            ))}
            <div style={{ display: 'flex', gap: '0.75rem', marginTop: '0.25rem' }}>
              <a href="http://localhost:8080/swagger/index.html" target="_blank" rel="noopener noreferrer" className="btn btn-secondary">
                Gateway Swagger UI ↗
              </a>
              <a href="http://localhost:8082/swagger/index.html" target="_blank" rel="noopener noreferrer" className="btn btn-secondary">
                Query API Swagger UI ↗
              </a>
            </div>
          </div>
        )}
      </div>
    </Layout>
  )
}
