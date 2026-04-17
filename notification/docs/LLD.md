# Notification Service — Low-Level Design

## Package Layout

```
notification/
├── cmd/main.go
├── configs/config.yaml
├── internal/
│   ├── handler/
│   │   ├── kafka_handler.go        # Kafka consume loop
│   │   └── http_handler.go         # Health endpoint
│   ├── service/
│   │   └── notification_service.go # Deliver, sendWebhook, sendEmail
│   └── repo/
│       └── repo.go                 # GetApp, GetAlertRule, SaveDelivery
└── docs/
```

## Key Types

### KafkaHandler
```go
type KafkaHandler struct {
    consumer *kafka.Consumer
    svc      *service.NotificationService
    topic    string
    log      *zap.Logger
}
func (h *KafkaHandler) Run(ctx context.Context)  // blocking consume loop
```

### NotificationService
```go
type NotificationService struct {
    repo       *repo.Repo
    httpClient *http.Client
    log        *zap.Logger
}
func (s *NotificationService) Deliver(ctx, AlertFiredEvent) error
func (s *NotificationService) sendWebhook(ctx, url, evt) error
func (s *NotificationService) sendEmail(ctx, to, evt) error
```

### Repo
```go
type Repo struct { pg *postgres.Client; mongo *mongo.Client; log *zap.Logger }
func (r *Repo) GetApp(ctx, appID) (*models.App, error)
func (r *Repo) GetAlertRule(ctx, ruleID) (*models.AlertRule, error)
func (r *Repo) SaveDelivery(ctx, *models.NotificationDelivery) error
```

## Kafka Message Contract

Consumed from topic: `alert-fired`

```json
{
  "rule_id": "a-1234",
  "app_id": "app-xyz",
  "metric": "error_rate",
  "value": 0.12,
  "threshold": 0.05,
  "operator": ">",
  "fired_at": "2026-01-01T00:00:00Z"
}
```

## NotificationDelivery MongoDB Document

```json
{
  "_id": "nd-1234567890",
  "rule_id": "a-1234",
  "app_id": "app-xyz",
  "channel": "webhook",
  "status": "delivered",
  "sent_at": "2026-01-01T00:01:00Z",
  "error": "",
  "payload": { ... }
}
```

## App Notification Fields (PostgreSQL)

| Column | Description |
|--------|-------------|
| `notification_channel` | `webhook` or `email` |
| `webhook_url` | Target URL for webhook delivery |
| `notification_email` | Email address for email delivery |
