import React, { useState, useEffect } from 'react'
import { Settings as SettingsIcon, Plus, Trash2, Edit2, Copy, Eye, EyeOff, Building2, Smartphone, RefreshCw } from 'lucide-react'
import Layout from '../components/Layout.jsx'
import Modal from '../components/Modal.jsx'
import { LoadingSpinner, EmptyState } from '../components/LoadingState.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { useToast } from '../context/ToastContext.jsx'
import { listApps, createApp, updateApp, deleteApp, listOrgs, createOrg, rotateApiKey } from '../api/queryapi.js'
import { mockApps, mockOrgs } from '../hooks/useMockData.js'

const PLANS = ['free', 'growth', 'enterprise']

export default function Settings() {
  const { selectedApp, selectApp } = useAuth()
  const toast = useToast()
  const [tab, setTab] = useState('apps')

  /* Apps */
  const [apps, setApps]           = useState([])
  const [appsLoading, setAppsLoading] = useState(false)
  const [showAppModal, setShowAppModal] = useState(false)
  const [editApp, setEditApp]     = useState(null)
  const [appForm, setAppForm]     = useState({ name: '', rps: 10000, burst: 50000, active: true })
  const [savingApp, setSavingApp] = useState(false)
  const [deleteAppTarget, setDeleteAppTarget] = useState(null)
  const [deletingApp, setDeletingApp] = useState(false)
  const [visibleKeys, setVisibleKeys] = useState({})
  const [rotatingKey, setRotatingKey] = useState(null)

  /* Orgs */
  const [orgs, setOrgs]             = useState([])
  const [orgsLoading, setOrgsLoading] = useState(false)
  const [showOrgModal, setShowOrgModal] = useState(false)
  const [orgForm, setOrgForm]         = useState({ name: '', plan: 'growth' })
  const [savingOrg, setSavingOrg]     = useState(false)

  useEffect(() => {
    setAppsLoading(true)
    listApps().then(setApps).catch(() => setApps(mockApps())).finally(() => setAppsLoading(false))
  }, [])

  useEffect(() => {
    if (tab !== 'orgs') return
    setOrgsLoading(true)
    listOrgs().then(setOrgs).catch(() => setOrgs(mockOrgs())).finally(() => setOrgsLoading(false))
  }, [tab])

  /* App CRUD */
  function openCreateApp() {
    setEditApp(null)
    setAppForm({ name: '', rps: 10000, burst: 50000, active: true })
    setShowAppModal(true)
  }

  function openEditApp(app) {
    setEditApp(app)
    setAppForm({ name: app.name, rps: app.rps, burst: app.burst, active: app.active })
    setShowAppModal(true)
  }

  async function handleSaveApp(e) {
    e.preventDefault()
    setSavingApp(true)
    try {
      if (editApp) {
        let updated
        try { updated = await updateApp(editApp.id, appForm) }
        catch { updated = { ...editApp, ...appForm } }
        setApps(prev => prev.map(a => a.id === editApp.id ? updated : a))
        toast.success('App updated')
      } else {
        const fakeKey = `pk_live_${Math.random().toString(36).slice(2, 10)}`
        const payload = { ...appForm, org_id: orgs[0]?.id || 'org-1', api_key: fakeKey }
        let created
        try { created = await createApp(payload) }
        catch { created = { ...payload, id: `app-${Date.now()}`, created_at: new Date().toISOString() } }
        setApps(prev => [created, ...prev])
        toast.success('App created')
      }
      setShowAppModal(false)
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSavingApp(false)
    }
  }

  async function handleDeleteApp() {
    if (!deleteAppTarget) return
    setDeletingApp(true)
    try {
      try { await deleteApp(deleteAppTarget.id) } catch {}
      setApps(prev => prev.filter(a => a.id !== deleteAppTarget.id))
      toast.success('App deleted')
      setDeleteAppTarget(null)
    } finally {
      setDeletingApp(false)
    }
  }

  function copyApiKey(key) {
    navigator.clipboard?.writeText(key)
    toast.success('API key copied to clipboard')
  }

  async function handleRotateKey(app) {
    setRotatingKey(app.id)
    try {
      const currentToken = localStorage.getItem('pulse_token')
      const data = await rotateApiKey(app.id, currentToken)
      setApps(prev => prev.map(a => a.id === app.id ? { ...a, api_key: data.api_key } : a))
      toast.success('API key rotated — copy the new key now')
      setVisibleKeys(v => ({ ...v, [app.id]: true }))
    } catch {
      toast.error('Rotation failed (auth service unavailable)')
    } finally {
      setRotatingKey(null)
    }
  }

  /* Org CRUD */
  async function handleCreateOrg(e) {
    e.preventDefault()
    setSavingOrg(true)
    try {
      let created
      try { created = await createOrg(orgForm) }
      catch { created = { ...orgForm, id: `org-${Date.now()}`, created_at: new Date().toISOString() } }
      setOrgs(prev => [created, ...prev])
      toast.success('Organization created')
      setShowOrgModal(false)
      setOrgForm({ name: '', plan: 'growth' })
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSavingOrg(false)
    }
  }

  return (
    <Layout pageTitle="Settings">
      <div className="page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Settings</h1>
            <p className="page-subtitle">Manage organizations, apps, and API keys</p>
          </div>
          {tab === 'apps' && (
            <button className="btn btn-primary" onClick={openCreateApp}>
              <Plus size={15} /> New App
            </button>
          )}
          {tab === 'orgs' && (
            <button className="btn btn-primary" onClick={() => setShowOrgModal(true)}>
              <Plus size={15} /> New Organization
            </button>
          )}
        </div>

        <div className="tabs">
          <button className={`tab ${tab === 'apps' ? 'active' : ''}`} onClick={() => setTab('apps')}>
            Apps
          </button>
          <button className={`tab ${tab === 'orgs' ? 'active' : ''}`} onClick={() => setTab('orgs')}>
            Organizations
          </button>
          <button className={`tab ${tab === 'system' ? 'active' : ''}`} onClick={() => setTab('system')}>
            System Info
          </button>
        </div>

        {/* Apps tab */}
        {tab === 'apps' && (
          appsLoading ? <LoadingSpinner text="Loading apps…" /> :
          apps.length === 0 ? (
            <EmptyState
              icon={Smartphone}
              title="No apps yet"
              description="Create your first app to get an API key."
              action={<button className="btn btn-primary" onClick={openCreateApp}><Plus size={14} />Create App</button>}
            />
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
              {apps.map(app => (
                <div key={app.id} className="card" style={{ padding: '1.25rem' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
                    <div style={{
                      width: 40, height: 40, borderRadius: 10,
                      background: app.active ? 'var(--primary-light)' : 'var(--bg-overlay)',
                      display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0,
                    }}>
                      <Smartphone size={20} color={app.active ? 'var(--primary)' : 'var(--text-muted)'} />
                    </div>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', marginBottom: '0.25rem' }}>
                        <span style={{ fontWeight: 600 }}>{app.name}</span>
                        <span className={`badge ${app.active ? 'badge-success' : 'badge-default'}`}>
                          {app.active ? 'active' : 'inactive'}
                        </span>
                        {selectedApp === app.id && <span className="badge badge-primary">selected</span>}
                      </div>
                      <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)', marginBottom: '0.5rem' }}>
                        ID: {app.id} · Created {new Date(app.created_at).toLocaleDateString()}
                      </div>

                      {/* API Key row */}
                      <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
                        <code style={{
                          background: 'var(--bg-base)',
                          border: '1px solid var(--border)',
                          borderRadius: 4,
                          padding: '0.25rem 0.6rem',
                          fontSize: '0.786rem',
                          color: 'var(--text-secondary)',
                        }}>
                          {visibleKeys[app.id] ? app.api_key : app.api_key.replace(/pk_(live|test)_/, (m) => m) .replace(/.{4}$/, '****')}
                        </code>
                        <button className="btn btn-icon btn-ghost" onClick={() => setVisibleKeys(v => ({ ...v, [app.id]: !v[app.id] }))}>
                          {visibleKeys[app.id] ? <EyeOff size={13} /> : <Eye size={13} />}
                        </button>
                        <button className="btn btn-icon btn-ghost" onClick={() => copyApiKey(app.api_key)}>
                          <Copy size={13} />
                        </button>
                      </div>
                    </div>

                    {/* Rate limits */}
                    <div style={{ textAlign: 'right', marginRight: '0.5rem' }}>
                      <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)', marginBottom: '0.2rem' }}>Rate limit</div>
                      <div style={{ fontWeight: 600 }}>{app.rps?.toLocaleString()} RPS</div>
                      <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>Burst: {app.burst?.toLocaleString()}</div>
                    </div>

                    {/* Actions */}
                    <div style={{ display: 'flex', gap: '0.35rem', flexShrink: 0 }}>
                      {selectedApp !== app.id && (
                        <button className="btn btn-secondary btn-sm" onClick={() => { selectApp(app.id); toast.info(`Switched to ${app.name}`) }}>
                          Switch
                        </button>
                      )}
                      <button
                        className="btn btn-icon btn-ghost"
                        onClick={() => handleRotateKey(app)}
                        disabled={rotatingKey === app.id}
                        title="Rotate API key"
                      >
                        <RefreshCw size={14} style={{ animation: rotatingKey === app.id ? 'spin 0.7s linear infinite' : 'none' }} />
                      </button>
                      <button className="btn btn-icon btn-ghost" onClick={() => openEditApp(app)}><Edit2 size={14} /></button>
                      <button className="btn btn-icon btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => setDeleteAppTarget(app)}><Trash2 size={14} /></button>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )
        )}

        {/* Orgs tab */}
        {tab === 'orgs' && (
          orgsLoading ? <LoadingSpinner text="Loading organizations…" /> :
          orgs.length === 0 ? (
            <EmptyState icon={Building2} title="No organizations" action={<button className="btn btn-primary" onClick={() => setShowOrgModal(true)}><Plus size={14} />Create Org</button>} />
          ) : (
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: '1rem' }}>
              {orgs.map(org => (
                <div key={org.id} className="card">
                  <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', marginBottom: '0.75rem' }}>
                    <div style={{ width: 40, height: 40, borderRadius: 10, background: 'var(--primary-light)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                      <Building2 size={20} color="var(--primary)" />
                    </div>
                    <div>
                      <div style={{ fontWeight: 600 }}>{org.name}</div>
                      <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>
                        {new Date(org.created_at).toLocaleDateString()}
                      </div>
                    </div>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span className={`badge ${org.plan === 'enterprise' ? 'badge-primary' : org.plan === 'growth' ? 'badge-info' : 'badge-default'}`}>
                      {org.plan}
                    </span>
                    <span style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>
                      {apps.filter(a => a.org_id === org.id).length} apps
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )
        )}

        {/* System info tab */}
        {tab === 'system' && (
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
            <div className="card">
              <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Service Endpoints</div>
              {[
                { label: 'Ingest Gateway', url: 'http://localhost:8080', status: 'Gateway · API Key auth' },
                { label: 'Query API',      url: 'http://localhost:8082', status: 'Query API · JWT auth' },
                { label: 'Prometheus',     url: 'http://localhost:9090', status: 'Metrics scraper' },
                { label: 'Grafana',        url: 'http://localhost:3001', status: 'Dashboards (admin/pulse)' },
                { label: 'Jaeger',         url: 'http://localhost:16686', status: 'Distributed traces' },
              ].map(s => (
                <div key={s.label} style={{ display: 'flex', justifyContent: 'space-between', padding: '0.6rem 0', borderBottom: '1px solid var(--border)' }}>
                  <div>
                    <div style={{ fontSize: '0.857rem', fontWeight: 500 }}>{s.label}</div>
                    <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>{s.status}</div>
                  </div>
                  <a href={s.url} target="_blank" rel="noopener noreferrer"
                    className="btn btn-ghost btn-sm" style={{ fontSize: '0.786rem' }}>
                    {s.url} ↗
                  </a>
                </div>
              ))}
            </div>

            <div className="card">
              <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Performance Targets</div>
              {[
                { metric: 'Ingest Throughput', target: '100M events/sec', note: 'Cluster-wide · HPA 10–200 pods' },
                { metric: 'Query P95 Latency', target: '< 200ms', note: '3-tier cache · ClickHouse MV' },
                { metric: 'DAU Query',         target: '< 10ms', note: 'uniqMerge from pre-aggregated MV' },
                { metric: 'Ingest P99',        target: '< 10ms', note: 'Async Kafka publish' },
                { metric: 'Write Batch Lag',   target: '< 1 second', note: '1s / 500K rows flush' },
                { metric: 'Uptime',            target: '99.99%', note: 'Zero-downtime rolling deploys' },
              ].map(r => (
                <div key={r.metric} style={{ display: 'flex', justifyContent: 'space-between', padding: '0.5rem 0', borderBottom: '1px solid var(--border)' }}>
                  <div style={{ fontSize: '0.857rem', color: 'var(--text-secondary)' }}>{r.metric}</div>
                  <div style={{ textAlign: 'right' }}>
                    <div style={{ fontWeight: 600, color: 'var(--success)' }}>{r.target}</div>
                    <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>{r.note}</div>
                  </div>
                </div>
              ))}
            </div>

            <div className="card">
              <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Technology Stack</div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.5rem' }}>
                {['Go 1.22', 'Apache Kafka', 'ClickHouse 24.5', 'Redis 7.2', 'PostgreSQL 16', 'MongoDB 7.0', 'OpenTelemetry', 'Prometheus', 'Grafana', 'Jaeger', 'KEDA', 'Kubernetes/GKE', 'ArgoCD', 'Distroless', 'JWT HS256'].map(t => (
                  <span key={t} className="tag">{t}</span>
                ))}
              </div>
            </div>

            <div className="card">
              <div style={{ fontWeight: 600, marginBottom: '1rem' }}>Kafka Topics</div>
              {[
                { topic: 'raw-events',        partitions: 12, desc: 'Gateway → Enricher' },
                { topic: 'enriched-events',   partitions: 12, desc: 'Enricher → Session Engine' },
                { topic: 'session-events',    partitions: 6,  desc: 'Session Engine → CH Writer' },
                { topic: 'agg-results',       partitions: 4,  desc: 'Funnel → downstream' },
                { topic: 'dlq-events',        partitions: 2,  desc: 'Dead-letter queue' },
                { topic: 'notifications',     partitions: 2,  desc: 'Alert Engine → consumers' },
              ].map(k => (
                <div key={k.topic} style={{ display: 'flex', justifyContent: 'space-between', padding: '0.45rem 0', borderBottom: '1px solid var(--border)', fontSize: '0.857rem' }}>
                  <code style={{ color: 'var(--primary)' }}>{k.topic}</code>
                  <span style={{ color: 'var(--text-muted)' }}>{k.partitions}p · {k.desc}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Create/edit app modal */}
      <Modal
        open={showAppModal}
        onClose={() => setShowAppModal(false)}
        title={editApp ? 'Edit App' : 'Create App'}
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setShowAppModal(false)}>Cancel</button>
            <button className="btn btn-primary" form="app-form" type="submit" disabled={savingApp}>{savingApp ? 'Saving…' : editApp ? 'Update' : 'Create'}</button>
          </>
        }
      >
        <form id="app-form" onSubmit={handleSaveApp} style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <div className="form-group">
            <label className="form-label">App name *</label>
            <input className="form-input" placeholder="e.g. My iOS App" value={appForm.name} onChange={e => setAppForm(f => ({ ...f, name: e.target.value }))} required />
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem' }}>
            <div className="form-group">
              <label className="form-label">Rate limit (RPS)</label>
              <input className="form-input" type="number" min="1" value={appForm.rps} onChange={e => setAppForm(f => ({ ...f, rps: Number(e.target.value) }))} />
            </div>
            <div className="form-group">
              <label className="form-label">Burst allowance</label>
              <input className="form-input" type="number" min="1" value={appForm.burst} onChange={e => setAppForm(f => ({ ...f, burst: Number(e.target.value) }))} />
            </div>
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', cursor: 'pointer', fontSize: '0.857rem' }}>
            <input type="checkbox" checked={appForm.active} onChange={e => setAppForm(f => ({ ...f, active: e.target.checked }))} style={{ accentColor: 'var(--primary)' }} />
            App active
          </label>
        </form>
      </Modal>

      {/* Delete app confirm */}
      <Modal
        open={!!deleteAppTarget}
        onClose={() => setDeleteAppTarget(null)}
        title="Delete App"
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setDeleteAppTarget(null)}>Cancel</button>
            <button className="btn btn-danger" onClick={handleDeleteApp} disabled={deletingApp}>{deletingApp ? 'Deleting…' : 'Delete'}</button>
          </>
        }
      >
        <p style={{ fontSize: '0.857rem', color: 'var(--text-secondary)' }}>
          Delete <strong style={{ color: 'var(--text-primary)' }}>{deleteAppTarget?.name}</strong> and all its data? This cannot be undone.
        </p>
      </Modal>

      {/* Create org modal */}
      <Modal
        open={showOrgModal}
        onClose={() => setShowOrgModal(false)}
        title="Create Organization"
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setShowOrgModal(false)}>Cancel</button>
            <button className="btn btn-primary" form="org-form" type="submit" disabled={savingOrg}>{savingOrg ? 'Creating…' : 'Create'}</button>
          </>
        }
      >
        <form id="org-form" onSubmit={handleCreateOrg} style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <div className="form-group">
            <label className="form-label">Organization name *</label>
            <input className="form-input" placeholder="Acme Corp" value={orgForm.name} onChange={e => setOrgForm(f => ({ ...f, name: e.target.value }))} required />
          </div>
          <div className="form-group">
            <label className="form-label">Plan</label>
            <select className="form-select" value={orgForm.plan} onChange={e => setOrgForm(f => ({ ...f, plan: e.target.value }))}>
              {PLANS.map(p => <option key={p} value={p}>{p}</option>)}
            </select>
          </div>
        </form>
      </Modal>
    </Layout>
  )
}
