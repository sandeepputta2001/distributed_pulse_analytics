FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION:-dev}" \
    -o /bin/enricher ./cmd/enricher

# ─── Runtime ──────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/enricher /enricher
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# GeoIP DB mounted via K8s volume at /data/GeoLite2-City.mmdb
ENV PULSE_GEOIP_DBPATH=/data/GeoLite2-City.mmdb
ENV PULSE_SERVICE_ENVIRONMENT=production

EXPOSE 9091

ENTRYPOINT ["/enricher"]
