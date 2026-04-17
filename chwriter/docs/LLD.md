# ClickHouse Writer — Low-Level Design

## Package Layout

```
chwriter/
├── cmd/main.go
├── configs/config.yaml
├── internal/
│   ├── handler/
│   │   └── handler.go         # Kafka consume loop + health HTTP
│   ├── service/
│   │   └── service.go         # Event converters + batch flusher
│   └── repo/
│       └── repo.go            # ClickHouse batch insert
└── docs/
```

## Key Types

### Service
```go
// EnrichedToCHEvent converts an enriched event to a ClickHouse row.
func EnrichedToCHEvent(e models.EnrichedEvent) models.CHEvent

// SessionToCHEvent converts a session lifecycle event to a ClickHouse row.
func SessionToCHEvent(se models.SessionEvent) models.CHEvent
```

### Repo
```go
type Repo struct { ch *clickhouse.Client; log *zap.Logger }
func (r *Repo) BatchInsert(ctx, events []models.CHEvent) error
```

### KafkaHandler (handler layer)
```go
type Handler struct {
    enrichedConsumer *kafka.Consumer
    sessionConsumer  *kafka.Consumer
    repo             *repo.Repo
    buf              []models.CHEvent
    mu               sync.Mutex
    flushTicker      *time.Ticker
    batchSize        int
    log              *zap.Logger
}
func (h *Handler) Run(ctx context.Context)   // starts both consumers + flush loop
func (h *Handler) flush(ctx context.Context) // inserts buffer and resets
```

## Flush Trigger Logic

```
ticker = time.NewTicker(5s)
loop:
  select:
    case msg = <-enrichedConsumer.Messages():
        buf.append(EnrichedToCHEvent(msg))
        if len(buf) >= 10000: flush()
    case msg = <-sessionConsumer.Messages():
        buf.append(SessionToCHEvent(msg))
        if len(buf) >= 10000: flush()
    case <-ticker.C:
        if len(buf) > 0: flush()
```

## CHEvent Model

```go
type CHEvent struct {
    AppID      string
    EventName  string
    DeviceID   string
    UserID     string
    SessionID  string
    EventTime  time.Time
    Props      map[string]string
    Country    string
    City       string
    OS         string
    Browser    string
    DeviceType string
    ServerTs   int64
}
```

## Metrics Exported

| Name | Type | Description |
|------|------|-------------|
| `chwriter_rows_written_total` | Counter | Rows successfully inserted |
| `chwriter_flush_errors_total` | Counter | Failed flush attempts |
| `chwriter_batch_size` | Histogram | Rows per flush |
