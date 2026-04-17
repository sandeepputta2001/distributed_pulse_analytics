# Auth Service — High-Level Design

## Overview
The **Auth Service** is a dedicated microservice for all authentication concerns: JWT issuance,
API key validation with caching, org/app registration, and API key rotation.

## Responsibilities
| Concern | Mechanism |
|---------|-----------|
| JWT issuance | HS256 signed tokens, configurable expiry (default 24h) |
| API key auth | Redis L1 cache (5min) → Postgres lookup |
| Registration | Org + App creation in Postgres transaction |
| Key rotation | Generate new `pk_live_*` key, update Postgres, invalidate Redis cache |
| Token refresh | Validate existing JWT, issue new one |

## Architecture
```
Client
  │  POST /v1/auth/token  { api_key }
  ▼
┌──────────────────────────────────────┐
│         Auth Service (:8083)         │
│                                      │
│  Service Layer                       │
│    ├─ ValidateAPIKey → Redis cache   │
│    │                 → Postgres      │
│    ├─ GenerateToken (JWT HS256)      │
│    ├─ RotateAPIKey → Postgres        │
│    └─ RefreshToken → validate + sign │
└──────────────────────────────────────┘
         │               │
         ▼               ▼
       Redis           Postgres
   (API key cache)   (orgs, apps)
```

## JWT Claims
```json
{
  "org_id": "uuid",
  "app_id": "uuid",
  "role": "admin | analyst | viewer",
  "exp": 1700000000,
  "iat": 1699913600,
  "iss": "pulse-analytics"
}
```
