# Frontend — High-Level Design

## Purpose

Browser-based dashboard for PulseAnalytics. Provides real-time and historical views of event counts, funnels, retention, session metrics, and A/B experiment results. Manages app configuration, alert rules, and cohort definitions.

## Architecture

```
Browser
  │
  ▼ HTTPS :443 (nginx / CDN)
nginx
  ├─► Static assets (JS/CSS/fonts) — cached 1 year
  ├─► /index.html (SPA fallback)
  └─► /api/*  ──proxy──► Gateway :8080
```

## Module Layout

```
src/
  components/     # Reusable UI components (charts, tables, forms)
  pages/          # Route-level pages
    Dashboard/
    Funnels/
    Retention/
    Sessions/
    Experiments/
    Alerts/
    Settings/
  hooks/          # Custom React hooks (useEventCount, useFunnel, etc.)
  api/            # API client (axios wrappers per service)
  store/          # Global state (React Context or Zustand)
  utils/
```

## API Integration

All API calls route through the nginx `/api/` proxy to the Gateway. Authentication uses JWTs stored in `localStorage` (or HttpOnly cookies in production).

## Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| nginx SPA fallback | Client-side routing with React Router |
| Multi-stage Docker build | ~20 MB image; no Node.js at runtime |
| Vite | Fast HMR in development; tree-shaken production bundle |
| Proxy via nginx | Avoids CORS issues; single origin for browser |
