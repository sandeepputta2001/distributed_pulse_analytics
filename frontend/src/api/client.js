/* Base API client with JWT auth, auto-refresh, and error handling */

const GATEWAY_BASE  = '/api/gateway'
const QUERY_BASE    = '/api/query'
const AUTH_BASE     = '/api/auth'

function getToken() {
  return localStorage.getItem('pulse_token')
}

function tokenExpiresAt() {
  const t = getToken()
  if (!t) return 0
  try {
    const payload = JSON.parse(atob(t.split('.')[1]))
    return (payload.exp || 0) * 1000
  } catch {
    return 0
  }
}

/* Refresh token if it expires within the next 2 minutes */
async function maybeRefresh() {
  const expiresAt = tokenExpiresAt()
  if (!expiresAt || expiresAt - Date.now() > 2 * 60 * 1000) return
  const token = getToken()
  if (!token) return
  try {
    const res = await fetch(`${AUTH_BASE}/v1/auth/refresh`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}` },
    })
    if (res.ok) {
      const data = await res.json()
      if (data.token) localStorage.setItem('pulse_token', data.token)
    }
  } catch {
    /* silently ignore refresh errors */
  }
}

async function request(baseUrl, path, options = {}) {
  await maybeRefresh()
  const url = `${baseUrl}${path}`
  const token = getToken()

  const headers = {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...options.headers,
  }

  const res = await fetch(url, { ...options, headers })

  if (res.status === 401) {
    localStorage.removeItem('pulse_token')
    window.location.href = '/login'
    throw new Error('Unauthorized')
  }

  const text = await res.text()
  const data = text ? JSON.parse(text) : {}

  if (!res.ok) {
    throw new Error(data.error || `HTTP ${res.status}`)
  }

  return data
}

export function gatewayGet(path, params = {}) {
  const qs = new URLSearchParams(params).toString()
  return request(GATEWAY_BASE, qs ? `${path}?${qs}` : path)
}

export function queryGet(path, params = {}) {
  const filtered = Object.fromEntries(
    Object.entries(params).filter(([, v]) => v !== null && v !== undefined && v !== '')
  )
  const qs = new URLSearchParams(filtered).toString()
  return request(QUERY_BASE, qs ? `${path}?${qs}` : path)
}

export function queryPost(path, body) {
  return request(QUERY_BASE, path, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export function gatewayPost(path, body, apiKey) {
  return request(GATEWAY_BASE, path, {
    method: 'POST',
    headers: apiKey ? { 'X-API-Key': apiKey } : {},
    body: JSON.stringify(body),
  })
}

export function queryDelete(path) {
  return request(QUERY_BASE, path, { method: 'DELETE' })
}

export function queryPut(path, body) {
  return request(QUERY_BASE, path, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export function authPost(path, body, token) {
  return request(AUTH_BASE, path, {
    method: 'POST',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    body: JSON.stringify(body),
  })
}

export function authGet(path) {
  return request(AUTH_BASE, path)
}

/* ─── Convenience: login ──────────────────────────────────── */
// Exchanges an API key for a JWT. The Query API exposes this at /v1/auth/login.
export async function login(apiKey) {
  const res = await fetch('/api/query/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ api_key: apiKey }),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || 'Login failed')
  if (data.token) {
    localStorage.setItem('pulse_token', data.token)
    localStorage.setItem('pulse_app_id', data.app_id || '')
    localStorage.setItem('pulse_org_id', data.org_id || '')
  }
  return data
}
