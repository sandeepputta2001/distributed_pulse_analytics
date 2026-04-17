FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY shared/ ./shared/
COPY query-api/ ./query-api/
WORKDIR /workspace/query-api
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/query-api ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/query-api /query-api
COPY --from=builder /workspace/query-api/configs/config.yaml /configs/config.yaml
EXPOSE 8085 9094
ENV CONFIG_PATH=/configs/config.yaml
ENTRYPOINT ["/query-api"]
