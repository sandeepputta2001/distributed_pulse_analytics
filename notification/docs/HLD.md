# Notification Service — High-Level Design

## Purpose

Deliver alert notifications to end-users (app owners) via the channel configured on each app (webhook or email). Consume `AlertFiredEvent` from Kafka to avoid blocking the alert-engine.

## Architecture

```
Alert Engine
     │
     ▼ Kafka: alert-fired
┌────────────────────────┐
│  Notification Service  │
│                        │
│  KafkaHandler          │
│    └► NotificationSvc  │
│         ├─► HTTP POST  │──► External Webhook
│         └─► Email stub │──► SendGrid / SES
│                        │
│  MongoDB               │  (delivery audit log)
│  PostgreSQL            │  (app config lookup)
│                        │
│  :8087  health         │
│  :9096  /metrics       │
└────────────────────────┘
```

## Design Decisions

| Decision | Rationale |
|----------|-----------|
| Kafka consumer | Decoupled from alert-engine; notifications can be retried or replayed |
| MongoDB for delivery log | Flexible schema for varied channel payloads; append-only audit |
| Webhook + email | Covers 90% of production alerting use cases |
| HTTP stub for email | Keeps the service shippable; SES/SendGrid added as a config change |

## Retry / Reliability

- Kafka consumer commits offsets only after successful delivery.
- Failed deliveries are logged to MongoDB with `status: "failed"` for manual review.
- Add dead-letter topic (`alert-fired-dlq`) and exponential backoff consumer for production.
