FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION:-dev}" \
    -o /bin/notificationservice ./cmd/notificationservice

# ─── Runtime ──────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/notificationservice /notificationservice
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

ENV PULSE_SERVICE_ENVIRONMENT=production

# 8084 — Campaign management REST API; 9091 — Prometheus metrics
EXPOSE 8084 9091

ENTRYPOINT ["/notificationservice"]
