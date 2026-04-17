FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY shared/ ./shared/
COPY notification/ ./notification/
WORKDIR /workspace/notification
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/notification ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/notification /notification
COPY --from=builder /workspace/notification/configs/config.yaml /configs/config.yaml
EXPOSE 8087 9096
ENV CONFIG_PATH=/configs/config.yaml
ENTRYPOINT ["/notification"]
