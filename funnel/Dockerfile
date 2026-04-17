FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY shared/ ./shared/
COPY funnel/ ./funnel/
WORKDIR /workspace/funnel
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/funnel ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/funnel /funnel
COPY --from=builder /workspace/funnel/configs/config.yaml /configs/config.yaml
EXPOSE 8084 9093
ENV CONFIG_PATH=/configs/config.yaml
ENTRYPOINT ["/funnel"]
