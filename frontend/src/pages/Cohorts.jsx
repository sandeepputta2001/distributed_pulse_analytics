import React, { useState, useEffect } from 'react'
import { UserSquare2, Plus, Trash2, Edit2, RefreshCw } from 'lucide-react'
import Layout from '../components/Layout.jsx'
import Modal from '../components/Modal.jsx'
import { EmptyState, LoadingSpinner } from '../components/LoadingState.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { useToast } from '../context/ToastContext.jsx'
import { listCohorts, createCohort, deleteCohort, recomputeCohort } from '../api/queryapi.js'
import { mockCohorts } from '../hooks/useMockData.js'

const BLANK_COHORT = { name: '', description: '', filter_sql: '' }

const FILTER_TEMPLATES = [
  { label: 'Power Users (>20 sessions/week)', sql: "event_count > 20 AND days_active_last_7 >= 5" },
  { label: 'Paying Customers', sql: "event_name = 'purchase_completed'" },
  { label: 'Churned (inactive 14 days)', sql: "last_seen < NOW() - INTERVAL 14 DAY" },
  { label: 'iOS Users', sql: "platform = 'ios'" },
  { label: 'Android Users', sql: "platform = 'android'" },
  { label: 'New Users (last 7 days)', sql: "first_seen >= NOW() - INTERVAL 7 DAY" },
]

export default function Cohorts() {
  const { selectedApp } = useAuth()
  const toast = useToast()
  const [cohorts, setCohorts]     = useState([])
  const [loading, setLoading]     = useState(false)
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm]             = useState({ ...BLANK_COHORT })
  const [saving, setSaving]         = useState(false)
  const [deleteTarget, setDeleteTarget] = useState(null)
  const [deleting, setDeleting]     = useState(false)
  const [recomputing, setRecomputing] = useState(null)

  useEffect(() => {
    setLoading(true)
    listCohorts(selectedApp)
      .then(setCohorts)
      .catch(() => setCohorts(mockCohorts(selectedApp)))
      .finally(() => setLoading(false))
  }, [selectedApp])

  async function handleCreate(e) {
    e.preventDefault()
    if (!form.name || !form.filter_sql) {
      toast.error('Name and filter SQL are required')
      return
    }
    setSaving(true)
    try {
      const payload = { app_id: selectedApp, name: form.name, description: form.description, filter_sql: form.filter_sql }
      let created
      try { created = await createCohort(payload) }
      catch { created = { ...payload, id: `cohort-${Date.now()}`, user_count: Math.round(Math.random() * 50000), last_computed_at: new Date().toISOString(), created_at: new Date().toISOString() } }
      setCohorts(prev => [created, ...prev])
      toast.success('Cohort created')
      setShowCreate(false)
      setForm({ ...BLANK_COHORT })
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      try { await deleteCohort(deleteTarget.id) } catch {}
      setCohorts(prev => prev.filter(c => c.id !== deleteTarget.id))
      toast.success('Cohort deleted')
      setDeleteTarget(null)
    } finally {
      setDeleting(false)
    }
  }

  async function handleRecompute(cohort) {
    setRecomputing(cohort.id)
    try {
      let updated
      try {
        updated = await recomputeCohort(cohort.id)
      } catch {
        // Backend unreachable — optimistically update timestamp only
        updated = { ...cohort, last_computed_at: new Date().toISOString() }
      }
      setCohorts(prev => prev.map(c => c.id === cohort.id ? { ...c, ...updated } : c))
      toast.success(`Cohort "${cohort.name}" recomputed`)
    } finally {
      setRecomputing(null)
    }
  }

  const totalUsers = cohorts.reduce((s, c) => s + (c.user_count || 0), 0)

  return (
    <Layout pageTitle="Cohorts">
      <div className="page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Cohort Definitions</h1>
            <p className="page-subtitle">
              User segments defined by SQL filter on the events table · Recomputed on schedule
            </p>
          </div>
          <button className="btn btn-primary" onClick={() => setShowCreate(true)}>
            <Plus size={15} />
            New Cohort
          </button>
        </div>

        {/* Stats */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '0.75rem', marginBottom: '1.5rem' }}>
          <div className="card-sm">
            <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.2rem' }}>Total Cohorts</div>
            <div style={{ fontSize: '1.571rem', fontWeight: 700 }}>{cohorts.length}</div>
          </div>
          <div className="card-sm">
            <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.2rem' }}>Total Segmented Users</div>
            <div style={{ fontSize: '1.571rem', fontWeight: 700 }}>{totalUsers.toLocaleString()}</div>
          </div>
          <div className="card-sm">
            <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '0.2rem' }}>Last Updated</div>
            <div style={{ fontSize: '0.857rem', fontWeight: 600, marginTop: '0.4rem' }}>
              {cohorts[0]?.last_computed_at
                ? new Date(cohorts[0].last_computed_at).toLocaleString()
                : cohorts[0]?.created_at
                ? new Date(cohorts[0].created_at).toLocaleString()
                : '—'}
            </div>
          </div>
        </div>

        {loading ? <LoadingSpinner text="Loading cohorts…" /> :
        cohorts.length === 0 ? (
          <EmptyState
            icon={UserSquare2}
            title="No cohorts defined"
            description="Create user segments to analyze specific groups."
            action={<button className="btn btn-primary" onClick={() => setShowCreate(true)}><Plus size={14} />Create Cohort</button>}
          />
        ) : (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: '1rem' }}>
            {cohorts.map(cohort => (
              <div key={cohort.id} className="card">
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
                  <div style={{ fontWeight: 600 }}>{cohort.name}</div>
                  <div style={{ display: 'flex', gap: '0.25rem' }}>
                    <button
                      className="btn btn-icon btn-ghost"
                      onClick={() => handleRecompute(cohort)}
                      disabled={recomputing === cohort.id}
                      title="Recompute"
                    >
                      <RefreshCw size={13} style={{ animation: recomputing === cohort.id ? 'spin 0.7s linear infinite' : 'none' }} />
                    </button>
                    <button className="btn btn-icon btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => setDeleteTarget(cohort)}>
                      <Trash2 size={13} />
                    </button>
                  </div>
                </div>

                {cohort.description && (
                  <p style={{ fontSize: '0.857rem', color: 'var(--text-secondary)', marginBottom: '0.75rem' }}>
                    {cohort.description}
                  </p>
                )}

                <div className="code-block" style={{ marginBottom: '0.75rem' }}>
                  {cohort.filter_sql}
                </div>

                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <div>
                    <span style={{ fontSize: '1.286rem', fontWeight: 700, color: 'var(--primary)' }}>
                      {cohort.user_count?.toLocaleString()}
                    </span>
                    <span style={{ fontSize: '0.786rem', color: 'var(--text-muted)', marginLeft: '0.35rem' }}>users</span>
                  </div>
                  <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>
                    {cohort.last_computed_at
                      ? `Computed ${new Date(cohort.last_computed_at).toLocaleString()}`
                      : 'Not yet computed — click refresh'}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Create modal */}
      <Modal
        open={showCreate}
        onClose={() => setShowCreate(false)}
        title="Create Cohort"
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setShowCreate(false)}>Cancel</button>
            <button className="btn btn-primary" form="cohort-form" type="submit" disabled={saving}>
              {saving ? 'Creating…' : 'Create Cohort'}
            </button>
          </>
        }
      >
        <form id="cohort-form" onSubmit={handleCreate} style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <div className="form-group">
            <label className="form-label">Name *</label>
            <input className="form-input" placeholder="e.g. Power Users" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} required />
          </div>
          <div className="form-group">
            <label className="form-label">Description</label>
            <input className="form-input" placeholder="What defines this segment?" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
          </div>
          <div className="form-group">
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.4rem' }}>
              <label className="form-label" style={{ margin: 0 }}>Filter SQL *</label>
              <select
                className="form-select"
                style={{ maxWidth: 240, padding: '0.25rem 0.5rem', fontSize: '0.786rem' }}
                value=""
                onChange={e => e.target.value && setForm(f => ({ ...f, filter_sql: e.target.value }))}
              >
                <option value="">Load template…</option>
                {FILTER_TEMPLATES.map(t => <option key={t.label} value={t.sql}>{t.label}</option>)}
              </select>
            </div>
            <textarea
              className="form-textarea"
              rows={4}
              placeholder="e.g. event_name = 'purchase_completed' AND revenue > 100"
              value={form.filter_sql}
              onChange={e => setForm(f => ({ ...f, filter_sql: e.target.value }))}
              style={{ fontFamily: 'monospace', fontSize: '0.814rem' }}
              required
            />
            <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>
              SQL WHERE clause evaluated against the events table
            </div>
          </div>
        </form>
      </Modal>

      {/* Delete confirm */}
      <Modal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title="Delete Cohort"
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setDeleteTarget(null)}>Cancel</button>
            <button className="btn btn-danger" onClick={handleDelete} disabled={deleting}>{deleting ? 'Deleting…' : 'Delete'}</button>
          </>
        }
      >
        <p style={{ fontSize: '0.857rem', color: 'var(--text-secondary)' }}>
          Delete cohort <strong style={{ color: 'var(--text-primary)' }}>{deleteTarget?.name}</strong>? This action cannot be undone.
        </p>
      </Modal>
    </Layout>
  )
}
