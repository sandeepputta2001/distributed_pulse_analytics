# Frontend

React + Vite single-page application for the PulseAnalytics dashboard. Served by nginx in production via a multi-stage Docker build.

## Tech Stack

- **React 18** — UI framework
- **Vite** — Build tool and dev server
- **nginx** — Production HTTP server with SPA fallback and API proxy

## Development

```bash
# Install dependencies
npm ci

# Start dev server (hot reload)
npm run dev

# Production build
npm run build

# Docker
make docker-build
make docker-run
```

## Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `VITE_API_URL` | Gateway base URL | `http://localhost:8080` |

## Architecture

```
Browser
  │
  ▼ :3000 (nginx)
nginx
  ├─► /           → dist/index.html (SPA)
  └─► /api/*      → proxy → gateway:8080
```
