# Frontend — Low-Level Design

## Build Pipeline

```
npm run build
  → Vite bundles src/ → dist/
     dist/index.html
     dist/assets/index-[hash].js
     dist/assets/index-[hash].css
```

## Docker Build Stages

**Stage 1 (builder):** `node:20-alpine`
- `npm ci` — install exact locked deps
- `npm run build` — produce `dist/`

**Stage 2 (runtime):** `nginx:1.27-alpine`
- Copy `dist/` → `/usr/share/nginx/html`
- Copy custom `nginx.conf`
- Final image ≈ 20 MB

## nginx Configuration

```nginx
location /          → try_files $uri $uri/ /index.html   # SPA
location /api/      → proxy_pass http://gateway:8080/    # API proxy
location ~* \.(js|css|...) → expires 1y; Cache-Control immutable
```

## Environment Variables

| Variable | Used by | Description |
|----------|---------|-------------|
| `VITE_API_URL` | Vite build | Gateway URL (baked at build time) |

For runtime injection (without rebuilding), use an init container or nginx `sub_filter` to replace the placeholder in `index.html`.

## Component Hierarchy (pages)

```
App
├── AuthProvider
├── Router
│   ├── /dashboard       → DashboardPage
│   │     ├── EventCountChart
│   │     └── DAUChart
│   ├── /funnels         → FunnelsPage
│   ├── /retention       → RetentionPage
│   ├── /sessions        → SessionsPage
│   ├── /experiments     → ExperimentsPage
│   ├── /alerts          → AlertsPage
│   └── /settings        → SettingsPage
└── Sidebar / TopNav
```

## API Client (src/api/)

```
api/
  events.ts       → GET /v1/events/count, /v1/dau
  funnels.ts      → POST /v1/funnels/query, CRUD /v1/funnels
  retention.ts    → POST /v1/retention
  sessions.ts     → GET /v1/sessions/metrics
  alerts.ts       → CRUD /v1/alerts
  experiments.ts  → CRUD /v1/experiments
  auth.ts         → POST /v1/auth/token, refresh
```
