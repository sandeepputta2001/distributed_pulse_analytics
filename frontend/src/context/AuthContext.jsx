import React, { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { authPost } from '../api/client.js'

const AuthContext = createContext(null)

function decodePayload(jwt) {
  try {
    return JSON.parse(atob(jwt.split('.')[1]))
  } catch {
    return null
  }
}

export function AuthProvider({ children }) {
  const [token, setToken] = useState(() => localStorage.getItem('pulse_token'))
  const [user, setUser]   = useState(() => {
    const t = localStorage.getItem('pulse_token')
    return t ? decodePayload(t) : null
  })

  const [selectedApp, setSelectedApp] = useState(() => {
    return localStorage.getItem('pulse_app_id') || 'app_demo'
  })

  /* Proactively refresh the token when it has < 5 min remaining */
  useEffect(() => {
    if (!token) return
    const payload = decodePayload(token)
    if (!payload?.exp) return

    const msUntilExpiry = payload.exp * 1000 - Date.now()
    const msUntilRefresh = msUntilExpiry - 5 * 60 * 1000  // refresh 5 min before expiry

    if (msUntilRefresh <= 0) {
      // Already within the refresh window — try immediately
      doRefresh(token)
      return
    }

    const timer = setTimeout(() => doRefresh(token), msUntilRefresh)
    return () => clearTimeout(timer)
  }, [token])

  async function doRefresh(currentToken) {
    try {
      const res = await fetch('/api/auth/v1/auth/refresh', {
        method: 'POST',
        headers: { Authorization: `Bearer ${currentToken}` },
      })
      if (res.ok) {
        const data = await res.json()
        if (data.token) {
          localStorage.setItem('pulse_token', data.token)
          if (data.app_id) localStorage.setItem('pulse_app_id', data.app_id)
          setToken(data.token)
          setUser(decodePayload(data.token))
        }
      }
    } catch {
      /* silently ignore — user will be redirected on next 401 */
    }
  }

  const login = useCallback((jwt, appId, orgId) => {
    localStorage.setItem('pulse_token', jwt)
    if (appId) localStorage.setItem('pulse_app_id', appId)
    if (orgId) localStorage.setItem('pulse_org_id', orgId)
    setToken(jwt)
    setUser(decodePayload(jwt) ?? { sub: 'user' })
    if (appId) setSelectedApp(appId)
  }, [])

  /* Register a new org+app via the auth service, then auto-login */
  const registerOrg = useCallback(async (orgName, appName, email) => {
    const data = await authPost('/v1/auth/register', { org_name: orgName, app_name: appName, email })
    // data: { org_id, app_id, api_key, token, expires }
    login(data.token, data.app_id, data.org_id)
    return data
  }, [login])

  const logout = useCallback(() => {
    localStorage.removeItem('pulse_token')
    localStorage.removeItem('pulse_app_id')
    localStorage.removeItem('pulse_org_id')
    setToken(null)
    setUser(null)
  }, [])

  const selectApp = useCallback((appId) => {
    localStorage.setItem('pulse_app_id', appId)
    setSelectedApp(appId)
  }, [])

  const isDemoMode = !token

  return (
    <AuthContext.Provider value={{ token, user, login, logout, registerOrg, selectedApp, selectApp, isDemoMode }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
