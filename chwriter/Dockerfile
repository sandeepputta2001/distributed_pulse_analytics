FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY shared/ ./shared/
COPY chwriter/ ./chwriter/
WORKDIR /workspace/chwriter
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/chwriter ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /bin/chwriter /chwriter
COPY --from=builder /workspace/chwriter/configs/config.yaml /configs/config.yaml
EXPOSE 8088 9097
ENV CONFIG_PATH=/configs/config.yaml
ENTRYPOINT ["/chwriter"]
