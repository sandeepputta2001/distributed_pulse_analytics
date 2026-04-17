import React, { useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import {
  BarChart2, Users, GitMerge, RotateCcw, Clock, Bell, FlaskConical,
  UserSquare2, Settings, Zap, ChevronDown, ChevronRight, Activity,
  Database, LogOut,
} from 'lucide-react'
import { useAuth } from '../context/AuthContext.jsx'

const NAV_ITEMS = [
  { icon: BarChart2,     label: 'Dashboard',        path: '/' },
  { icon: Activity,      label: 'Event Analytics',  path: '/events' },
  { icon: Users,         label: 'Active Users',      path: '/active-users' },
  { icon: GitMerge,      label: 'Funnels',           path: '/funnels' },
  { icon: RotateCcw,     label: 'Retention',         path: '/retention' },
  { icon: Clock,         label: 'Sessions',          path: '/sessions' },
  { label: 'divider', divider: true },
  { icon: Bell,          label: 'Alerts',            path: '/alerts' },
  { icon: FlaskConical,  label: 'Experiments',       path: '/experiments' },
  { icon: UserSquare2,   label: 'Cohorts',           path: '/cohorts' },
  { label: 'divider2', divider: true },
  { icon: Database,      label: 'SDK & Ingest',      path: '/sdk' },
  { icon: Settings,      label: 'Settings',          path: '/settings' },
]

export default function Sidebar({ collapsed, onToggle }) {
  const { logout, user, selectedApp, selectApp } = useAuth()
  const location = useLocation()

  const w = collapsed ? 64 : 240

  return (
    <aside style={{
      width: w,
      minWidth: w,
      height: '100vh',
      background: 'var(--bg-surface)',
      borderRight: '1px solid var(--border)',
      display: 'flex',
      flexDirection: 'column',
      transition: 'width 0.2s ease',
      overflow: 'hidden',
      position: 'fixed',
      top: 0,
      left: 0,
      zIndex: 200,
    }}>
      {/* Logo */}
      <div style={{
        height: 'var(--header-height)',
        display: 'flex',
        alignItems: 'center',
        padding: collapsed ? '0 1rem' : '0 1.25rem',
        borderBottom: '1px solid var(--border)',
        gap: '0.75rem',
        cursor: 'pointer',
        flexShrink: 0,
      }} onClick={onToggle}>
        <div style={{
          width: 32, height: 32, borderRadius: '8px',
          background: 'linear-gradient(135deg, var(--primary), #818cf8)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0,
        }}>
          <Zap size={18} color="#fff" fill="#fff" />
        </div>
        {!collapsed && (
          <div>
            <div style={{ fontWeight: 700, fontSize: '1rem', letterSpacing: '-0.02em' }}>
              Pulse<span style={{ color: 'var(--primary)' }}>Analytics</span>
            </div>
            <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>
              v1.0 · enterprise
            </div>
          </div>
        )}
      </div>

      {/* App selector */}
      {!collapsed && (
        <div style={{
          margin: '0.75rem',
          background: 'var(--bg-elevated)',
          border: '1px solid var(--border-strong)',
          borderRadius: 'var(--radius-sm)',
          padding: '0.5rem 0.75rem',
          display: 'flex',
          alignItems: 'center',
          gap: '0.5rem',
          cursor: 'pointer',
        }}>
          <div style={{
            width: 8, height: 8, borderRadius: '50%',
            background: 'var(--success)', flexShrink: 0,
          }} />
          <select
            value={selectedApp}
            onChange={e => selectApp(e.target.value)}
            style={{
              background: 'none', border: 'none', color: 'var(--text-primary)',
              fontSize: '0.857rem', fontWeight: 500, cursor: 'pointer',
              outline: 'none', width: '100%',
            }}
          >
            <option value="app_demo">app_demo</option>
            <option value="app-1">PulseDemo iOS</option>
            <option value="app-2">PulseDemo Android</option>
            <option value="app-3">PulseDemo Web</option>
          </select>
          <ChevronDown size={12} color="var(--text-muted)" />
        </div>
      )}

      {/* Navigation */}
      <nav style={{ flex: 1, overflowY: 'auto', overflowX: 'hidden', padding: '0.5rem 0' }}>
        {NAV_ITEMS.map((item, idx) => {
          if (item.divider) {
            return (
              <div key={item.label} style={{
                height: 1, background: 'var(--border)',
                margin: '0.5rem 0.75rem',
              }} />
            )
          }
          const Icon = item.icon
          const active = item.path === '/'
            ? location.pathname === '/'
            : location.pathname.startsWith(item.path)

          return (
            <NavLink key={item.path} to={item.path} style={{ display: 'block', padding: '0 0.5rem' }}>
              <div style={{
                display: 'flex',
                alignItems: 'center',
                gap: '0.75rem',
                padding: collapsed ? '0.6rem' : '0.6rem 0.85rem',
                borderRadius: 'var(--radius-sm)',
                justifyContent: collapsed ? 'center' : 'flex-start',
                background: active ? 'var(--primary-light)' : 'transparent',
                color:      active ? 'var(--primary)' : 'var(--text-secondary)',
                transition: 'all 0.1s ease',
                marginBottom: '0.1rem',
              }}
              onMouseEnter={e => { if (!active) e.currentTarget.style.background = 'var(--bg-elevated)'; e.currentTarget.style.color = 'var(--text-primary)' }}
              onMouseLeave={e => { e.currentTarget.style.background = active ? 'var(--primary-light)' : 'transparent'; e.currentTarget.style.color = active ? 'var(--primary)' : 'var(--text-secondary)' }}
              >
                <Icon size={17} strokeWidth={active ? 2.2 : 1.8} style={{ flexShrink: 0 }} />
                {!collapsed && (
                  <span style={{
                    fontSize: '0.857rem',
                    fontWeight: active ? 600 : 400,
                    whiteSpace: 'nowrap',
                  }}>{item.label}</span>
                )}
                {active && !collapsed && (
                  <div style={{
                    marginLeft: 'auto',
                    width: 4, height: 16,
                    background: 'var(--primary)',
                    borderRadius: 2,
                  }} />
                )}
              </div>
            </NavLink>
          )
        })}
      </nav>

      {/* User footer */}
      <div style={{
        padding: '0.75rem',
        borderTop: '1px solid var(--border)',
        flexShrink: 0,
      }}>
        {!collapsed ? (
          <div style={{
            display: 'flex', alignItems: 'center', gap: '0.6rem',
            padding: '0.5rem 0.75rem',
            borderRadius: 'var(--radius-sm)',
          }}>
            <div style={{
              width: 28, height: 28, borderRadius: '50%',
              background: 'var(--primary)',
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              fontSize: '0.786rem', fontWeight: 700, color: '#fff', flexShrink: 0,
            }}>
              {(user?.sub || 'A')[0].toUpperCase()}
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontSize: '0.857rem', fontWeight: 500, truncate: true }}>
                {user?.sub || 'Demo User'}
              </div>
              <div style={{ fontSize: '0.714rem', color: 'var(--text-muted)' }}>
                {user?.role || 'admin'}
              </div>
            </div>
            <button className="btn btn-icon btn-ghost" onClick={logout} title="Sign out">
              <LogOut size={15} />
            </button>
          </div>
        ) : (
          <button className="btn btn-icon btn-ghost" onClick={logout}
            style={{ width: '100%', justifyContent: 'center' }}>
            <LogOut size={15} />
          </button>
        )}
      </div>
    </aside>
  )
}
