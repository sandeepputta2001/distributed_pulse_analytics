# Reusable multi-service Dockerfile template.
# Set BUILD_CMD to the service cmd path, e.g.:
#   docker build --build-arg BUILD_CMD=./cmd/enricher -t pulse-enricher .
ARG BUILD_CMD=./cmd/gateway
ARG VERSION=dev

# ─── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder
ARG BUILD_CMD
ARG VERSION

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /bin/service ${BUILD_CMD}

# ─── Runtime stage ────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/service /service
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

ENV PULSE_SERVICE_ENVIRONMENT=production

EXPOSE 8080 9091

ENTRYPOINT ["/service"]
