.PHONY: build run test lint docker-build docker-push clean swagger

SERVICE     := gateway
IMAGE       := pulse-analytics/$(SERVICE)
IMAGE_TAG   ?= latest
ECR         ?= ""
CONFIG_PATH ?= configs/config.yaml

# ── Build ─────────────────────────────────────────────────────────────────────
build:
	go build -ldflags="-s -w" -o bin/$(SERVICE) ./cmd/

run:
	CONFIG_PATH=$(CONFIG_PATH) go run ./cmd/

# ── Test ──────────────────────────────────────────────────────────────────────
test:
	go test ./... -race -timeout 60s

test-cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# ── Quality ───────────────────────────────────────────────────────────────────
lint:
	golangci-lint run ./...

swagger:
	swag init -g cmd/main.go -o docs/swagger

# ── Docker ────────────────────────────────────────────────────────────────────
docker-build:
	docker build -f Dockerfile -t $(IMAGE):$(IMAGE_TAG) ..

docker-push:
	docker tag $(IMAGE):$(IMAGE_TAG) $(ECR)/$(IMAGE):$(IMAGE_TAG)
	docker push $(ECR)/$(IMAGE):$(IMAGE_TAG)

# ── Local Dev ─────────────────────────────────────────────────────────────────
up:
	docker compose up -d

down:
	docker compose down

logs:
	docker compose logs -f $(SERVICE)

# ── Cleanup ───────────────────────────────────────────────────────────────────
clean:
	rm -rf bin/ coverage.out coverage.html
