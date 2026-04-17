# Enricher — Low-Level Design

## Package Layout

```
enricher/
├── cmd/main.go
├── configs/config.yaml
├── internal/
│   ├── handler/
│   │   └── handler.go              # KafkaHandler + health HTTP
│   └── service/
│       └── service.go              # EnricherService.Enrich
└── docs/
```

## Key Types

### EnricherService
```go
type EnricherService struct {
    geoip    *geo.Resolver
    kafka    *kafka.Producer
    outTopic string
    log      *zap.Logger
}

type IngestMessage struct {
    Batch    models.EventBatch
    ClientIP string
    ServerTs int64
}

func (s *EnricherService) Enrich(msg IngestMessage) []models.EnrichedEvent
```

### EnrichedEvent additions over raw Event
```go
type EnrichedEvent struct {
    models.Event
    Country   string
    City      string
    OS        string
    Browser   string
    DeviceType string
    ServerTs  int64
}
```

## KafkaHandler Flow

```
Consume(raw-events) → unmarshal IngestMessage
  → EnricherService.Enrich(msg) → []EnrichedEvent
  → for each event: producer.PublishJSON(enriched-events, appID, event)
```

## Batch Processing

Events arrive as `EventBatch` (up to 500 events per message). Enrichment is applied per-event in a tight loop. GeoIP lookup is O(1) (radix tree in-memory).

## GeoIP Database

- Path: `/etc/geoip/GeoLite2-City.mmdb` (configurable)
- Updated monthly; loaded at startup with `mmap`
- Falls back to empty strings on lookup failure (does not fail the event)
