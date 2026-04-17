# Funnel Processor — Low-Level Design

## Package Layout

```
funnel/
├── cmd/main.go
├── configs/config.yaml
├── internal/
│   ├── handler/
│   │   └── handler.go         # Kafka consume loop + health HTTP
│   ├── service/
│   │   └── service.go         # FunnelService.Process + Reload
│   └── repo/
│       └── repo.go            # Redis state + Postgres funnel defs
└── docs/
```

## Key Types

### FunnelService
```go
type FunnelService struct {
    repo      *repo.Repo
    publisher *kafka.Producer
    outTopic  string
    funnels   []*models.FunnelDefinition  // in-memory, updated by Reload
    mu        sync.RWMutex
    log       *zap.Logger
}

type FunnelConversion struct {
    FunnelID  string
    UserID    string
    Converted bool
    DroppedAt int
}

func (s *FunnelService) Process(ctx, event models.EnrichedEvent) ([]FunnelConversion, error)
func (s *FunnelService) Reload(funnels []*models.FunnelDefinition)
```

### Repo
```go
type Repo struct {
    redis *redis.Client
    pg    *postgres.Client
    log   *zap.Logger
}
func (r *Repo) GetFunnelState(ctx, funnelID, userID) (*models.FunnelState, error)
func (r *Repo) SaveFunnelState(ctx, state, ttl) error
func (r *Repo) DeleteFunnelState(ctx, funnelID, userID) error
func (r *Repo) ListFunnels(ctx, appID) ([]*models.FunnelDefinition, error)
```

## Hot-Reload Goroutine

Started in `cmd/main.go`:
```go
ticker := time.NewTicker(30 * time.Second)
for range ticker.C {
    funnels, _ := repo.ListFunnels(ctx, "")
    svc.Reload(funnels)
}
```

## FunnelConversionEvent Kafka Message

```json
{
  "funnel_id": "f-1234",
  "app_id": "app-xyz",
  "user_id": "u-abc",
  "converted": true,
  "dropped_at_step": null,
  "started_at": 1700000000000,
  "completed_at": 1700003600000
}
```

Topic: `agg-results` | Key: `funnel_id`
