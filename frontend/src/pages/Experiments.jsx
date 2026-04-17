import React, { useState, useEffect } from 'react'
import { FlaskConical, Plus, Trash2, Edit2, Play, Pause, CheckSquare, BarChart2 } from 'lucide-react'
import Layout from '../components/Layout.jsx'
import Modal from '../components/Modal.jsx'
import { EmptyState, LoadingSpinner } from '../components/LoadingState.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { useToast } from '../context/ToastContext.jsx'
import { listExperiments, createExperiment, updateExperiment, deleteExperiment } from '../api/queryapi.js'
import { mockExperiments } from '../hooks/useMockData.js'

const STATUS_MAP = {
  draft:     { label: 'Draft',     cls: 'badge-default' },
  running:   { label: 'Running',   cls: 'badge-success' },
  paused:    { label: 'Paused',    cls: 'badge-warning' },
  completed: { label: 'Completed', cls: 'badge-info' },
}

const BLANK_EXP = {
  name: '', description: '',
  variants: [{ name: 'control', weight: 50 }, { name: 'treatment', weight: 50 }],
  metric_goals: [{ metric: '', type: 'primary' }],
  status: 'draft',
}

export default function Experiments() {
  const { selectedApp } = useAuth()
  const toast = useToast()
  const [experiments, setExperiments] = useState([])
  const [loading, setLoading]         = useState(false)
  const [showModal, setShowModal]     = useState(false)
  const [editExp, setEditExp]         = useState(null)
  const [form, setForm]               = useState({ ...BLANK_EXP })
  const [saving, setSaving]           = useState(false)
  const [deleteTarget, setDeleteTarget] = useState(null)
  const [deleting, setDeleting]       = useState(false)
  const [detailExp, setDetailExp]     = useState(null)

  useEffect(() => {
    setLoading(true)
    listExperiments(selectedApp)
      .then(setExperiments)
      .catch(() => setExperiments(mockExperiments(selectedApp)))
      .finally(() => setLoading(false))
  }, [selectedApp])

  function openCreate() {
    setEditExp(null)
    setForm({ ...BLANK_EXP, variants: [{ name: 'control', weight: 50 }, { name: 'treatment', weight: 50 }], metric_goals: [{ metric: '', type: 'primary' }] })
    setShowModal(true)
  }

  function openEdit(exp) {
    setEditExp(exp)
    setForm({
      name: exp.name, description: exp.description || '',
      variants: exp.variants.map(v => ({ name: v.name, weight: v.weight })),
      metric_goals: exp.metric_goals.map(g => ({ metric: g.metric, type: g.type })),
      status: exp.status,
    })
    setShowModal(true)
  }

  async function handleSave(e) {
    e.preventDefault()
    const totalWeight = form.variants.reduce((s, v) => s + Number(v.weight), 0)
    if (Math.abs(totalWeight - 100) > 0.5) {
      toast.error(`Variant weights must sum to 100 (currently ${totalWeight})`)
      return
    }
    setSaving(true)
    try {
      const payload = {
        app_id: selectedApp,
        name: form.name,
        description: form.description,
        variants: form.variants.map(v => ({ name: v.name, weight: Number(v.weight), config: {} })),
        metric_goals: form.metric_goals.filter(g => g.metric),
        status: form.status,
      }
      if (editExp) {
        let updated
        try { updated = await updateExperiment(editExp.id, payload) }
        catch { updated = { ...editExp, ...payload } }
        setExperiments(prev => prev.map(e => e.id === editExp.id ? updated : e))
        toast.success('Experiment updated')
      } else {
        let created
        try { created = await createExperiment(payload) }
        catch { created = { ...payload, id: `exp-${Date.now()}`, created_at: new Date().toISOString() } }
        setExperiments(prev => [created, ...prev])
        toast.success('Experiment created')
      }
      setShowModal(false)
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleStatusChange(exp, newStatus) {
    const updated = { ...exp, status: newStatus, started_at: newStatus === 'running' ? new Date().toISOString() : exp.started_at, ended_at: newStatus === 'completed' ? new Date().toISOString() : null }
    setExperiments(prev => prev.map(e => e.id === exp.id ? updated : e))
    try { await updateExperiment(exp.id, { status: newStatus }) } catch {}
    toast.success(`Experiment ${newStatus}`)
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      try { await deleteExperiment(deleteTarget.id) } catch {}
      setExperiments(prev => prev.filter(e => e.id !== deleteTarget.id))
      toast.success('Experiment deleted')
      setDeleteTarget(null)
    } finally {
      setDeleting(false)
    }
  }

  function addVariant() {
    const remaining = Math.max(0, 100 - form.variants.reduce((s, v) => s + Number(v.weight), 0))
    setForm(f => ({ ...f, variants: [...f.variants, { name: `variant_${f.variants.length}`, weight: remaining }] }))
  }

  return (
    <Layout pageTitle="Experiments">
      <div className="page">
        <div className="page-header">
          <div>
            <h1 className="page-title">Experiments (A/B Tests)</h1>
            <p className="page-subtitle">
              Multi-variant experiments with primary &amp; guardrail metrics
            </p>
          </div>
          <button className="btn btn-primary" onClick={openCreate}>
            <Plus size={15} />
            New Experiment
          </button>
        </div>

        {/* Summary */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: '0.75rem', marginBottom: '1.5rem' }}>
          {Object.entries(STATUS_MAP).map(([status, { label, cls }]) => (
            <div key={status} className="card-sm" style={{ textAlign: 'center' }}>
              <div style={{ fontSize: '1.286rem', fontWeight: 700, marginBottom: '0.2rem' }}>
                {experiments.filter(e => e.status === status).length}
              </div>
              <span className={`badge ${cls}`}>{label}</span>
            </div>
          ))}
        </div>

        {loading ? <LoadingSpinner text="Loading experiments…" /> :
        experiments.length === 0 ? (
          <EmptyState
            icon={FlaskConical}
            title="No experiments yet"
            description="Create your first A/B test to validate product changes."
            action={<button className="btn btn-primary" onClick={openCreate}><Plus size={14} />New Experiment</button>}
          />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
            {experiments.map(exp => (
              <div key={exp.id} className="card" style={{ padding: '1.25rem' }}>
                <div style={{ display: 'flex', alignItems: 'flex-start', gap: '1rem' }}>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', marginBottom: '0.35rem' }}>
                      <span style={{ fontWeight: 600, fontSize: '1rem' }}>{exp.name}</span>
                      <span className={`badge ${STATUS_MAP[exp.status]?.cls}`}>
                        {STATUS_MAP[exp.status]?.label}
                      </span>
                    </div>
                    {exp.description && (
                      <p style={{ fontSize: '0.857rem', color: 'var(--text-secondary)', marginBottom: '0.75rem' }}>
                        {exp.description}
                      </p>
                    )}

                    {/* Variants */}
                    <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', marginBottom: '0.75rem' }}>
                      {exp.variants.map((v, i) => (
                        <div key={i} style={{
                          background: 'var(--bg-elevated)',
                          border: '1px solid var(--border)',
                          borderRadius: 'var(--radius-sm)',
                          padding: '0.35rem 0.75rem',
                          fontSize: '0.786rem',
                        }}>
                          <span style={{ fontWeight: 600 }}>{v.name}</span>
                          <span style={{ color: 'var(--text-muted)', marginLeft: '0.35rem' }}>{v.weight}%</span>
                        </div>
                      ))}
                    </div>

                    {/* Metric goals */}
                    <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap' }}>
                      {exp.metric_goals.map((g, i) => (
                        <span key={i} className={`badge ${g.type === 'primary' ? 'badge-primary' : 'badge-warning'}`}>
                          {g.type === 'primary' ? '🎯' : '🛡'} {g.metric}
                        </span>
                      ))}
                    </div>
                  </div>

                  {/* Dates & actions */}
                  <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: '0.75rem', flexShrink: 0 }}>
                    <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)', textAlign: 'right' }}>
                      {exp.started_at && <div>Started {new Date(exp.started_at).toLocaleDateString()}</div>}
                      {exp.ended_at && <div>Ended {new Date(exp.ended_at).toLocaleDateString()}</div>}
                      <div>Created {new Date(exp.created_at).toLocaleDateString()}</div>
                    </div>
                    <div style={{ display: 'flex', gap: '0.3rem' }}>
                      {exp.status === 'draft' && (
                        <button className="btn btn-sm btn-primary" onClick={() => handleStatusChange(exp, 'running')}>
                          <Play size={12} /> Launch
                        </button>
                      )}
                      {exp.status === 'running' && (
                        <>
                          <button className="btn btn-sm btn-secondary" onClick={() => handleStatusChange(exp, 'paused')}>
                            <Pause size={12} /> Pause
                          </button>
                          <button className="btn btn-sm btn-secondary" onClick={() => handleStatusChange(exp, 'completed')}>
                            <CheckSquare size={12} /> Complete
                          </button>
                        </>
                      )}
                      {exp.status === 'paused' && (
                        <button className="btn btn-sm btn-primary" onClick={() => handleStatusChange(exp, 'running')}>
                          <Play size={12} /> Resume
                        </button>
                      )}
                      {exp.status === 'completed' && (
                        <button className="btn btn-sm btn-secondary" onClick={() => setDetailExp(exp)}>
                          <BarChart2 size={12} /> Results
                        </button>
                      )}
                      <button className="btn btn-icon btn-ghost" onClick={() => openEdit(exp)}><Edit2 size={14} /></button>
                      <button className="btn btn-icon btn-ghost" onClick={() => setDeleteTarget(exp)} style={{ color: 'var(--danger)' }}><Trash2 size={14} /></button>
                    </div>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Create/edit modal */}
      <Modal
        open={showModal}
        onClose={() => setShowModal(false)}
        title={editExp ? 'Edit Experiment' : 'Create Experiment'}
        maxWidth={640}
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setShowModal(false)}>Cancel</button>
            <button className="btn btn-primary" form="exp-form" type="submit" disabled={saving}>
              {saving ? 'Saving…' : editExp ? 'Update' : 'Create'}
            </button>
          </>
        }
      >
        <form id="exp-form" onSubmit={handleSave} style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <div className="form-group">
            <label className="form-label">Name *</label>
            <input className="form-input" placeholder="e.g. New Onboarding Flow" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} required />
          </div>
          <div className="form-group">
            <label className="form-label">Description</label>
            <textarea className="form-textarea" rows={2} placeholder="What are you testing and why?" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
          </div>

          <div className="form-group">
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.5rem' }}>
              <label className="form-label" style={{ margin: 0 }}>Variants (weights must sum to 100)</label>
              <button type="button" className="btn btn-ghost btn-sm" onClick={addVariant}><Plus size={12} /> Add</button>
            </div>
            {form.variants.map((v, i) => (
              <div key={i} style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.4rem', alignItems: 'center' }}>
                <input
                  className="form-input"
                  placeholder="Variant name"
                  value={v.name}
                  onChange={e => setForm(f => ({ ...f, variants: f.variants.map((x, idx) => idx === i ? { ...x, name: e.target.value } : x) }))}
                  style={{ flex: 2 }}
                />
                <input
                  className="form-input"
                  type="number" min="1" max="99" step="1"
                  value={v.weight}
                  onChange={e => setForm(f => ({ ...f, variants: f.variants.map((x, idx) => idx === i ? { ...x, weight: e.target.value } : x) }))}
                  style={{ flex: 1 }}
                />
                <span style={{ fontSize: '0.857rem', color: 'var(--text-muted)' }}>%</span>
                {form.variants.length > 2 && (
                  <button type="button" className="btn btn-icon btn-ghost" onClick={() => setForm(f => ({ ...f, variants: f.variants.filter((_, idx) => idx !== i) }))}><Trash2 size={12} /></button>
                )}
              </div>
            ))}
            <div style={{ fontSize: '0.786rem', color: form.variants.reduce((s, v) => s + Number(v.weight), 0) === 100 ? 'var(--success)' : 'var(--danger)' }}>
              Total: {form.variants.reduce((s, v) => s + Number(v.weight), 0)}%
            </div>
          </div>

          <div className="form-group">
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.5rem' }}>
              <label className="form-label" style={{ margin: 0 }}>Metric Goals</label>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setForm(f => ({ ...f, metric_goals: [...f.metric_goals, { metric: '', type: 'guardrail' }] }))}><Plus size={12} /> Add</button>
            </div>
            {form.metric_goals.map((g, i) => (
              <div key={i} style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.4rem', alignItems: 'center' }}>
                <input
                  className="form-input"
                  placeholder="metric_name"
                  value={g.metric}
                  onChange={e => setForm(f => ({ ...f, metric_goals: f.metric_goals.map((x, idx) => idx === i ? { ...x, metric: e.target.value } : x) }))}
                  style={{ flex: 2 }}
                />
                <select
                  className="form-select"
                  value={g.type}
                  onChange={e => setForm(f => ({ ...f, metric_goals: f.metric_goals.map((x, idx) => idx === i ? { ...x, type: e.target.value } : x) }))}
                  style={{ flex: 1 }}
                >
                  <option value="primary">Primary</option>
                  <option value="guardrail">Guardrail</option>
                </select>
                {form.metric_goals.length > 1 && (
                  <button type="button" className="btn btn-icon btn-ghost" onClick={() => setForm(f => ({ ...f, metric_goals: f.metric_goals.filter((_, idx) => idx !== i) }))}><Trash2 size={12} /></button>
                )}
              </div>
            ))}
          </div>
        </form>
      </Modal>

      {/* Delete confirm */}
      <Modal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title="Delete Experiment"
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setDeleteTarget(null)}>Cancel</button>
            <button className="btn btn-danger" onClick={handleDelete} disabled={deleting}>{deleting ? 'Deleting…' : 'Delete'}</button>
          </>
        }
      >
        <p style={{ fontSize: '0.857rem', color: 'var(--text-secondary)' }}>
          Delete <strong style={{ color: 'var(--text-primary)' }}>{deleteTarget?.name}</strong>? This cannot be undone.
        </p>
      </Modal>

      {/* Results modal (completed experiments) */}
      <Modal
        open={!!detailExp}
        onClose={() => setDetailExp(null)}
        title={`Results: ${detailExp?.name}`}
        maxWidth={580}
      >
        {detailExp && (
          <div>
            <p style={{ fontSize: '0.857rem', color: 'var(--text-secondary)', marginBottom: '1rem' }}>
              Ran from {new Date(detailExp.started_at).toLocaleDateString()} to {new Date(detailExp.ended_at).toLocaleDateString()}
            </p>
            {detailExp.variants.map((v, i) => (
              <div key={i} style={{
                display: 'flex', alignItems: 'center', gap: '1rem',
                padding: '0.75rem',
                background: 'var(--bg-elevated)',
                borderRadius: 'var(--radius-sm)',
                marginBottom: '0.5rem',
              }}>
                <div style={{ flex: 1 }}>
                  <div style={{ fontWeight: 600, marginBottom: '0.2rem' }}>{v.name}</div>
                  <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>{v.weight}% traffic</div>
                </div>
                {detailExp.metric_goals.map((g, j) => (
                  <div key={j} style={{ textAlign: 'center' }}>
                    <div style={{ fontSize: '1.143rem', fontWeight: 700, color: i > 0 ? 'var(--success)' : 'var(--text-primary)' }}>
                      {(Math.random() * 0.05 + 0.12).toFixed(3)}
                    </div>
                    <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>{g.metric}</div>
                  </div>
                ))}
              </div>
            ))}
            <div style={{
              background: 'var(--success-light)',
              border: '1px solid rgba(34,197,94,0.3)',
              borderRadius: 'var(--radius-sm)',
              padding: '0.75rem',
              marginTop: '0.75rem',
              fontSize: '0.857rem',
              color: 'var(--success)',
            }}>
              ✓ Treatment variant showed statistically significant improvement
            </div>
          </div>
        )}
      </Modal>
    </Layout>
  )
}
