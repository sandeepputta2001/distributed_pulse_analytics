import React, { useState } from 'react'
import Sidebar from './Sidebar.jsx'
import Header  from './Header.jsx'

export default function Layout({ children, pageTitle, onRefresh, loading }) {
  const [collapsed, setCollapsed] = useState(false)
  const sidebarWidth = collapsed ? 64 : 240

  return (
    <div style={{ display: 'flex', minHeight: '100vh', background: 'var(--bg-base)' }}>
      <Sidebar collapsed={collapsed} onToggle={() => setCollapsed(c => !c)} />

      <div style={{
        marginLeft: sidebarWidth,
        flex: 1,
        display: 'flex',
        flexDirection: 'column',
        minWidth: 0,
        transition: 'margin-left 0.2s ease',
      }}>
        <Header
          onToggleSidebar={() => setCollapsed(c => !c)}
          pageTitle={pageTitle}
          onRefresh={onRefresh}
          loading={loading}
        />
        <main style={{ flex: 1, overflowY: 'auto' }}>
          {children}
        </main>
      </div>
    </div>
  )
}
