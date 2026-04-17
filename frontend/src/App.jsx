import React from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider, useAuth } from './context/AuthContext.jsx'
import { ToastProvider } from './context/ToastContext.jsx'

import Login        from './pages/Login.jsx'
import Dashboard    from './pages/Dashboard.jsx'
import EventAnalytics from './pages/EventAnalytics.jsx'
import ActiveUsers  from './pages/ActiveUsers.jsx'
import Funnels      from './pages/Funnels.jsx'
import Retention    from './pages/Retention.jsx'
import Sessions     from './pages/Sessions.jsx'
import Alerts       from './pages/Alerts.jsx'
import Experiments  from './pages/Experiments.jsx'
import Cohorts      from './pages/Cohorts.jsx'
import SDKIngest    from './pages/SDKIngest.jsx'
import Settings     from './pages/Settings.jsx'

function PrivateRoute({ children }) {
  /* In demo mode (no token) we still allow access — the app works with mock data */
  return children
}

function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/"               element={<PrivateRoute><Dashboard /></PrivateRoute>} />
      <Route path="/events"         element={<PrivateRoute><EventAnalytics /></PrivateRoute>} />
      <Route path="/active-users"   element={<PrivateRoute><ActiveUsers /></PrivateRoute>} />
      <Route path="/funnels"        element={<PrivateRoute><Funnels /></PrivateRoute>} />
      <Route path="/retention"      element={<PrivateRoute><Retention /></PrivateRoute>} />
      <Route path="/sessions"       element={<PrivateRoute><Sessions /></PrivateRoute>} />
      <Route path="/alerts"         element={<PrivateRoute><Alerts /></PrivateRoute>} />
      <Route path="/experiments"    element={<PrivateRoute><Experiments /></PrivateRoute>} />
      <Route path="/cohorts"        element={<PrivateRoute><Cohorts /></PrivateRoute>} />
      <Route path="/sdk"            element={<PrivateRoute><SDKIngest /></PrivateRoute>} />
      <Route path="/settings"       element={<PrivateRoute><Settings /></PrivateRoute>} />
      <Route path="*"               element={<Navigate to="/" replace />} />
    </Routes>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <ToastProvider>
          <AppRoutes />
        </ToastProvider>
      </AuthProvider>
    </BrowserRouter>
  )
}
