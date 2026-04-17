FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY shared/ ./shared/
COPY enricher/ ./enricher/
WORKDIR /workspace/enricher
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/enricher ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/enricher /enricher
COPY --from=builder /workspace/enricher/configs/config.yaml /configs/config.yaml
EXPOSE 8082 9091
ENV CONFIG_PATH=/configs/config.yaml
ENTRYPOINT ["/enricher"]
