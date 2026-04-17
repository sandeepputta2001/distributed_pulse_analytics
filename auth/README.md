# Auth Service

Standalone JWT authentication and API key management microservice for PulseAnalytics.

## Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/v1/auth/register` | — | Create org + app, receive API key + JWT |
| `POST` | `/v1/auth/token` | — | Exchange API key → JWT |
| `POST` | `/v1/auth/refresh` | Bearer | Refresh an expiring JWT |
| `GET`  | `/v1/auth/validate` | Bearer | Introspect a JWT (internal use) |
| `POST` | `/v1/auth/apikey/rotate` | Bearer | Rotate app API key |
| `GET`  | `/health` | — | Liveness probe |
| `GET`  | `/metrics` | — | Prometheus metrics |

## Quick Start

```bash
make run          # local dev
make docker-build # build image
make test
```

## Architecture
See [docs/HLD.md](docs/HLD.md) and [docs/LLD.md](docs/LLD.md).
