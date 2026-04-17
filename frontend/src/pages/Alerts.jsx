import React, { useState, useEffect, useRef, useCallback } from 'react'
import {
  Bell, Plus, Trash2, Edit2, ToggleLeft, ToggleRight,
  AlertTriangle, CheckCircle2, Clock, Webhook, Mail,
} from 'lucide-react'
import Layout from '../components/Layout.jsx'
import Modal from '../components/Modal.jsx'
import { EmptyState, LoadingSpinner } from '../components/LoadingState.jsx'
import { useAuth } from '../context/AuthContext.jsx'
import { useToast } from '../context/ToastContext.jsx'
import { listAlerts, createAlert, updateAlert, deleteAlert } from '../api/queryapi.js'
import { mockAlerts } from '../hooks/useMockData.js'

const CONDITIONS = [
  { value: 'gt',  label: 'greater than (>)' },
  { value: 'lt',  label: 'less than (<)' },
  { value: 'gte', label: 'greater or equal (≥)' },
  { value: 'lte', label: 'less or equal (≤)' },
  { value: 'eq',  label: 'equal to (=)' },
]

const COMMON_METRICS = [
  'error_rate', 'p95_latency_ms', 'p99_latency_ms',
  'dau', 'kafka_lag', 'event_count', 'session_count',
  'revenue_total', 'funnel_conversion_rate', 'bounce_rate',
]

const BLANK_RULE = {
  name: '', metric_name: '', condition: 'gt', threshold: '',
  window_mins: 60, channels: [], webhook_url: '', email_to: '', active: true,
}

export default function Alerts() {
  const { selectedApp } = useAuth()
  const toast = useToast()
  const [alerts, setAlerts]   = useState([])
  const [loading, setLoading] = useState(false)
  const [showModal, setShowModal] = useState(false)
  const [editRule, setEditRule]   = useState(null)
  const [form, setForm]           = useState({ ...BLANK_RULE })
  const [saving, setSaving]       = useState(false)
  const [deleteTarget, setDeleteTarget] = useState(null)
  const [deleting, setDeleting]   = useState(false)

  const pollRef = useRef(null)

  const fetchAlerts = useCallback((showSpinner = false) => {
    if (showSpinner) setLoading(true)
    listAlerts(selectedApp)
      .then(setAlerts)
      .catch(() => { if (showSpinner) setAlerts(mockAlerts(selectedApp)) })
      .finally(() => { if (showSpinner) setLoading(false) })
  }, [selectedApp])

  useEffect(() => {
    fetchAlerts(true)

    // Poll every 60s to refresh last_fired_at from the alert engine
    pollRef.current = setInterval(() => fetchAlerts(false), 60_000)
    return () => clearInterval(pollRef.current)
  }, [fetchAlerts])

  function openCreate() {
    setEditRule(null)
    setForm({ ...BLANK_RULE })
    setShowModal(true)
  }

  function openEdit(rule) {
    setEditRule(rule)
    setForm({
      name: rule.name,
      metric_name: rule.metric_name,
      condition: rule.condition,
      threshold: rule.threshold,
      window_mins: rule.window_mins,
      channels: rule.channels || [],
      webhook_url: rule.webhook_url || '',
      email_to: (rule.email_to || []).join(', '),
      active: rule.active,
    })
    setShowModal(true)
  }

  async function handleSave(e) {
    e.preventDefault()
    if (!form.name || !form.metric_name || form.threshold === '') {
      toast.error('Name, metric, and threshold are required')
      return
    }
    setSaving(true)
    try {
      const payload = {
        app_id: selectedApp,
        name: form.name,
        metric_name: form.metric_name,
        condition: form.condition,
        threshold: parseFloat(form.threshold),
        window_mins: parseInt(form.window_mins),
        channels: form.channels,
        webhook_url: form.webhook_url || null,
        email_to: form.email_to ? form.email_to.split(',').map(s => s.trim()).filter(Boolean) : [],
        active: form.active,
      }

      if (editRule) {
        let updated
        try { updated = await updateAlert(editRule.id, payload) }
        catch { updated = { ...editRule, ...payload } }
        setAlerts(prev => prev.map(a => a.id === editRule.id ? updated : a))
        toast.success('Alert rule updated')
      } else {
        let created
        try { created = await createAlert(payload) }
        catch { created = { ...payload, id: `alert-${Date.now()}`, created_at: new Date().toISOString(), last_fired_at: null } }
        setAlerts(prev => [created, ...prev])
        toast.success('Alert rule created')
      }
      setShowModal(false)
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleToggle(rule) {
    const updated = { ...rule, active: !rule.active }
    setAlerts(prev => prev.map(a => a.id === rule.id ? updated : a))
    try {
      await updateAlert(rule.id, { active: !rule.active })
    } catch {
      /* optimistic update is fine in demo */
    }
    toast.info(`Alert "${rule.name}" ${updated.active ? 'enabled' : 'disabled'}`)
  }

  async function handleDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      try { await deleteAlert(deleteTarget.id) } catch {}
      setAlerts(prev => prev.filter(a => a.id !== deleteTarget.id))
      toast.success('Alert rule deleted')
      setDeleteTarget(null)
    } finally {
      setDeleting(false)
    }
  }

  function toggleChannel(ch) {
    setForm(f => ({
      ...f,
      channels: f.channels.includes(ch) ? f.channels.filter(c => c !== ch) : [...f.channels, ch],
    }))
  }

  return (
    <Layout pageTitle="Alerts">
      <div className="page">
        <div className="page-header">
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
              <h1 className="page-title">Alert Rules</h1>
              <span className="badge badge-success" style={{ fontSize: '0.714rem' }}>
                <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--success)', display: 'inline-block', marginRight: '0.3rem', animation: 'pulse 2s infinite' }} />
                Live · 60s
              </span>
            </div>
            <p className="page-subtitle">
              Threshold-based alerts evaluated every minute · Webhook + email channels · 30-min cooldown
            </p>
          </div>
          <button className="btn btn-primary" onClick={openCreate}>
            <Plus size={15} />
            New Alert
          </button>
        </div>

        {/* Summary */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '0.75rem', marginBottom: '1.5rem' }}>
          <div className="card-sm" style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
            <Bell size={20} color="var(--primary)" />
            <div>
              <div style={{ fontSize: '1.286rem', fontWeight: 700 }}>{alerts.length}</div>
              <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>Total rules</div>
            </div>
          </div>
          <div className="card-sm" style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
            <CheckCircle2 size={20} color="var(--success)" />
            <div>
              <div style={{ fontSize: '1.286rem', fontWeight: 700 }}>{alerts.filter(a => a.active).length}</div>
              <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>Active rules</div>
            </div>
          </div>
          <div className="card-sm" style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
            <AlertTriangle size={20} color="var(--danger)" />
            <div>
              <div style={{ fontSize: '1.286rem', fontWeight: 700 }}>
                {alerts.filter(a => a.active && a.last_fired_at).length}
              </div>
              <div style={{ fontSize: '0.786rem', color: 'var(--text-muted)' }}>Fired recently</div>
            </div>
          </div>
        </div>

        {loading ? <LoadingSpinner text="Loading alert rules…" /> :
        alerts.length === 0 ? (
          <EmptyState
            icon={Bell}
            title="No alert rules"
            description="Create your first alert rule to monitor key metrics."
            action={<button className="btn btn-primary" onClick={openCreate}><Plus size={14} />Create Alert</button>}
          />
        ) : (
          <div className="table-wrapper">
            <table>
              <thead>
                <tr>
                  <th>Status</th>
                  <th>Name</th>
                  <th>Condition</th>
                  <th>Window</th>
                  <th>Channels</th>
                  <th>Last Fired</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {alerts.map(rule => (
                  <tr key={rule.id}>
                    <td>
                      <div style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
                        <div style={{
                          width: 8, height: 8, borderRadius: '50%',
                          background: rule.active && rule.last_fired_at ? 'var(--danger)'
                            : rule.active ? 'var(--success)' : 'var(--text-muted)',
                          boxShadow: rule.active && rule.last_fired_at ? '0 0 6px var(--danger)' : 'none',
                        }} />
                        <span className={`badge ${rule.active && rule.last_fired_at ? 'badge-danger' : rule.active ? 'badge-success' : 'badge-default'}`}>
                          {rule.active && rule.last_fired_at ? 'FIRED' : rule.active ? 'OK' : 'OFF'}
                        </span>
                      </div>
                    </td>
                    <td style={{ fontWeight: 500 }}>{rule.name}</td>
                    <td>
                      <code style={{
                        background: 'var(--bg-elevated)',
                        padding: '0.2rem 0.5rem',
                        borderRadius: 4,
                        fontSize: '0.786rem',
                        color: 'var(--primary)',
                      }}>
                        {rule.metric_name} {rule.condition} {rule.threshold}
                      </code>
                    </td>
                    <td style={{ color: 'var(--text-secondary)' }}>
                      {rule.window_mins}m
                    </td>
                    <td>
                      <div style={{ display: 'flex', gap: '0.3rem' }}>
                        {(rule.channels || []).map(ch => (
                          <span key={ch} className="tag" style={{ display: 'flex', alignItems: 'center', gap: '0.2rem' }}>
                            {ch === 'webhook' ? <Webhook size={10} /> : <Mail size={10} />}
                            {ch}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td style={{ color: 'var(--text-muted)', fontSize: '0.786rem' }}>
                      {rule.last_fired_at
                        ? new Date(rule.last_fired_at).toLocaleString()
                        : 'Never'}
                    </td>
                    <td>
                      <div style={{ display: 'flex', gap: '0.3rem' }}>
                        <button className="btn btn-icon btn-ghost" onClick={() => handleToggle(rule)} title={rule.active ? 'Disable' : 'Enable'}>
                          {rule.active ? <ToggleRight size={16} color="var(--success)" /> : <ToggleLeft size={16} />}
                        </button>
                        <button className="btn btn-icon btn-ghost" onClick={() => openEdit(rule)}>
                          <Edit2 size={14} />
                        </button>
                        <button className="btn btn-icon btn-ghost" onClick={() => setDeleteTarget(rule)} style={{ color: 'var(--danger)' }}>
                          <Trash2 size={14} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Create/edit modal */}
      <Modal
        open={showModal}
        onClose={() => setShowModal(false)}
        title={editRule ? 'Edit Alert Rule' : 'Create Alert Rule'}
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setShowModal(false)}>Cancel</button>
            <button className="btn btn-primary" form="alert-form" type="submit" disabled={saving}>
              {saving ? 'Saving…' : editRule ? 'Update' : 'Create'}
            </button>
          </>
        }
      >
        <form id="alert-form" onSubmit={handleSave} style={{ display: 'flex', flexDirection: 'column', gap: '0.85rem' }}>
          <div className="form-group">
            <label className="form-label">Rule name *</label>
            <input className="form-input" placeholder="e.g. High Error Rate" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} required />
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '0.75rem' }}>
            <div className="form-group">
              <label className="form-label">Metric *</label>
              <select className="form-select" value={form.metric_name} onChange={e => setForm(f => ({ ...f, metric_name: e.target.value }))} required>
                <option value="">Select metric…</option>
                {COMMON_METRICS.map(m => <option key={m} value={m}>{m}</option>)}
              </select>
            </div>
            <div className="form-group">
              <label className="form-label">Condition *</label>
              <select className="form-select" value={form.condition} onChange={e => setForm(f => ({ ...f, condition: e.target.value }))}>
                {CONDITIONS.map(c => <option key={c.value} value={c.value}>{c.label}</option>)}
              </select>
            </div>
            <div className="form-group">
              <label className="form-label">Threshold *</label>
              <input className="form-input" type="number" step="any" placeholder="0" value={form.threshold} onChange={e => setForm(f => ({ ...f, threshold: e.target.value }))} required />
            </div>
          </div>
          <div className="form-group">
            <label className="form-label">Evaluation window (minutes)</label>
            <input className="form-input" type="number" min="1" max="1440" value={form.window_mins} onChange={e => setForm(f => ({ ...f, window_mins: e.target.value }))} />
          </div>
          <div className="form-group">
            <label className="form-label">Notification channels</label>
            <div style={{ display: 'flex', gap: '0.75rem' }}>
              {['webhook', 'email'].map(ch => (
                <label key={ch} style={{ display: 'flex', alignItems: 'center', gap: '0.4rem', cursor: 'pointer', fontSize: '0.857rem' }}>
                  <input
                    type="checkbox"
                    checked={form.channels.includes(ch)}
                    onChange={() => toggleChannel(ch)}
                    style={{ accentColor: 'var(--primary)' }}
                  />
                  {ch === 'webhook' ? <Webhook size={14} /> : <Mail size={14} />}
                  {ch}
                </label>
              ))}
            </div>
          </div>
          {form.channels.includes('webhook') && (
            <div className="form-group">
              <label className="form-label">Webhook URL</label>
              <input className="form-input" type="url" placeholder="https://hooks.example.com/…" value={form.webhook_url} onChange={e => setForm(f => ({ ...f, webhook_url: e.target.value }))} />
            </div>
          )}
          {form.channels.includes('email') && (
            <div className="form-group">
              <label className="form-label">Email recipients (comma-separated)</label>
              <input className="form-input" type="text" placeholder="ops@example.com, cto@example.com" value={form.email_to} onChange={e => setForm(f => ({ ...f, email_to: e.target.value }))} />
            </div>
          )}
          <label style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', cursor: 'pointer', fontSize: '0.857rem' }}>
            <input type="checkbox" checked={form.active} onChange={e => setForm(f => ({ ...f, active: e.target.checked }))} style={{ accentColor: 'var(--primary)' }} />
            Enable rule immediately
          </label>
        </form>
      </Modal>

      {/* Delete confirm */}
      <Modal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title="Delete Alert Rule"
        footer={
          <>
            <button className="btn btn-secondary" onClick={() => setDeleteTarget(null)}>Cancel</button>
            <button className="btn btn-danger" onClick={handleDelete} disabled={deleting}>
              {deleting ? 'Deleting…' : 'Delete'}
            </button>
          </>
        }
      >
        <p style={{ fontSize: '0.857rem', color: 'var(--text-secondary)' }}>
          Are you sure you want to delete <strong style={{ color: 'var(--text-primary)' }}>{deleteTarget?.name}</strong>? This action cannot be undone.
        </p>
      </Modal>
    </Layout>
  )
}
