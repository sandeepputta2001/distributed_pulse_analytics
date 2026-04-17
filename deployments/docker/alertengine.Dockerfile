FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION:-dev}" \
    -o /bin/alertengine ./cmd/alertengine

# ─── Runtime ──────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/alertengine /alertengine
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

ENV PULSE_SERVICE_ENVIRONMENT=production

# Alert engine has no public HTTP port; scrape target exposed via internal metrics server
EXPOSE 9091

ENTRYPOINT ["/alertengine"]
