# Session Engine — Low-Level Design

## Package Layout

```
session/
├── cmd/main.go
├── configs/config.yaml
├── internal/
│   ├── handler/
│   │   └── handler.go         # Kafka consume loop + health HTTP
│   ├── service/
│   │   └── service.go         # SessionService.Process
│   └── repo/
│       └── repo.go            # Redis session state CRUD
└── docs/
```

## Key Types

### SessionService
```go
type SessionService struct {
    repo      *repo.Repo
    publisher *kafka.Producer
    outTopic  string
    timeout   time.Duration     // 30 min
    log       *zap.Logger
}

type ProcessResult struct {
    SessionEnd   *models.SessionEvent   // non-nil if session ended
    SessionStart *models.SessionEvent   // non-nil if new session started
    Current      *models.SessionEvent   // updated current session
}

func (s *SessionService) Process(ctx, event models.EnrichedEvent) (*ProcessResult, error)
```

### Repo
```go
type Repo struct { redis *redis.Client; log *zap.Logger }
func (r *Repo) GetSession(ctx, appID, deviceID) (*models.SessionState, error)
func (r *Repo) SaveSession(ctx, state *models.SessionState) error  // TTL=35min
func (r *Repo) DeleteSession(ctx, appID, deviceID) error
```

## Process Logic

```
state = GetSession(appID, deviceID)

if state == nil:                        // first event
    state = newSession(event)
    publish session_start
elif event.ts - state.LastTs > 30min:  // inactivity
    publish session_end (duration=state.LastTs-state.StartTs)
    state = newSession(event)
    publish session_start
else:                                   // same session
    state.LastTs = event.ts
    state.EventCount++

SaveSession(state)
```

## SessionEvent Kafka Message

```json
{
  "session_id": "sess-1234",
  "app_id": "app-xyz",
  "device_id": "d-abc",
  "event_name": "session_start",
  "start_ts": 1700000000000,
  "end_ts": null,
  "duration_s": null,
  "event_count": 1
}
```

Topic: `session-events` | Key: `device_id`
