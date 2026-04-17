# Notification Service

Consumes `AlertFiredEvent` messages from the Kafka `alert-fired` topic and delivers notifications via configured channels (webhook, email).

## Channels

| Channel | Delivery method |
|---------|----------------|
| `webhook` | HTTP POST to `app.webhook_url` |
| `email` | Stub — integrate with SendGrid/SES |

All delivery attempts are recorded in MongoDB (`notification_deliveries` collection) for audit.

## Development

```bash
make run
make test
make docker-run
```

## Architecture

```
Kafka (alert-fired)
      │
      ▼
KafkaHandler.Run (consume loop)
      │
      ▼
NotificationService.Deliver
      ├─► sendWebhook  (HTTP POST)
      └─► sendEmail    (stub)
            │
            ▼
      MongoDB (delivery history)
```
