# ── Build Stage ───────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /workspace

# Copy shared module first (for layer caching)
COPY shared/ ./shared/

# Copy gateway module
COPY gateway/ ./gateway/

# Build binary
WORKDIR /workspace/gateway
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/gateway ./cmd/

# ── Runtime Stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/gateway /gateway
COPY --from=builder /workspace/gateway/configs/config.yaml /configs/config.yaml

EXPOSE 8080 9091

ENV CONFIG_PATH=/configs/config.yaml
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
