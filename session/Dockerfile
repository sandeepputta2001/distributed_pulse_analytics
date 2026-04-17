FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY shared/ ./shared/
COPY session/ ./session/
WORKDIR /workspace/session
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/session ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/session /session
COPY --from=builder /workspace/session/configs/config.yaml /configs/config.yaml
EXPOSE 8083 9092
ENV CONFIG_PATH=/configs/config.yaml
ENTRYPOINT ["/session"]
