# Auth Service — Low-Level Design

## Package Structure
```
auth/
├── cmd/main.go                   # Wire-up and HTTP server
├── internal/
│   ├── handler/handler.go        # HTTP handlers
│   ├── service/service.go        # Auth business logic
│   └── repo/repo.go              # Postgres data access
└── configs/config.yaml
```

## handler.Handler
- `Register` → `service.RegisterOrgApp` → 201 with orgID, appID, apiKey, token
- `Token` → `service.ExchangeAPIKey` → 200 with token
- `Refresh` → extract Bearer → `service.RefreshToken` → 200 with new token
- `Validate` → extract Bearer → `service.ValidateToken` → 200 with claims (used by other services)
- `RotateAPIKey` → JWT-protected → `service.RotateAPIKey` → 200 with new key

## service.AuthService
- `RegisterOrgApp`: calls `repo.CreateOrgAndApp` (Postgres transaction) → `jwt.GenerateToken`
- `ExchangeAPIKey`: calls `jwt.ValidateAPIKey` (Redis cache → Postgres) → `jwt.GenerateToken`
- `RefreshToken`: `jwt.ValidateToken` → `jwt.GenerateToken` with same claims
- `RotateAPIKey`: `generateAPIKey()` → `repo.RotateAPIKey` → invalidate Redis cache

## repo.Repo
- Thin wrapper over `postgres.Client` exposing only auth-relevant methods:
  - `GetAppByAPIKey`, `CreateOrgAndApp`, `RotateAPIKey`

## Security Notes
- JWT secret must be ≥ 32 characters in production
- API keys cached in Redis for 5 min — rotation takes effect within cache TTL
- Passwords/secrets never logged
- Bearer tokens validated on every protected request (stateless)
