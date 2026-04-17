FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY shared/ ./shared/
COPY alert-engine/ ./alert-engine/
WORKDIR /workspace/alert-engine
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/alert-engine ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/alert-engine /alert-engine
COPY --from=builder /workspace/alert-engine/configs/config.yaml /configs/config.yaml
EXPOSE 8086 9095
ENV CONFIG_PATH=/configs/config.yaml
ENTRYPOINT ["/alert-engine"]
