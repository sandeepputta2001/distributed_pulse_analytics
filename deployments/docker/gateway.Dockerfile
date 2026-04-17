# ─── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with CGO disabled for static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION:-dev}" \
    -o /bin/gateway ./cmd/gateway

# ─── Runtime stage ────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/gateway /gateway
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# GeoIP database (mounted via volume in K8s)
ENV PULSE_GEOIP_DBPATH=/data/GeoLite2-City.mmdb
ENV PULSE_SERVICE_ENVIRONMENT=production

EXPOSE 8080 9091

ENTRYPOINT ["/gateway"]
