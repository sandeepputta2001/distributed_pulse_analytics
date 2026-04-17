FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY shared/ ./shared/
COPY auth/ ./auth/
WORKDIR /workspace/auth
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /bin/auth ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/auth /auth
COPY --from=builder /workspace/auth/configs/config.yaml /configs/config.yaml
EXPOSE 8081
ENV CONFIG_PATH=/configs/config.yaml
USER nonroot:nonroot
ENTRYPOINT ["/auth"]
