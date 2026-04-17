# Enricher

Kafka stream processor: consumes raw events from `raw-events`, enriches each event with GeoIP (country, city) and user-agent parsing (OS, browser, device type), and publishes to `enriched-events`.

## Pipeline

```
Kafka: raw-events
      │
      ▼
EnricherService.Enrich
      ├─► GeoIP lookup (MaxMind GeoLite2)
      └─► UA parsing (ua-parser)
      │
      ▼
Kafka: enriched-events
```

## Development

```bash
make run
make test
make docker-run
```

## Configuration

See `configs/config.yaml`. GeoIP database path defaults to `/etc/geoip/GeoLite2-City.mmdb`.
