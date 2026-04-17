.PHONY: build run test lint docker-build fmt

SERVICE := alert-engine
IMAGE   := pulse-analytics/$(SERVICE)
TAG     ?= latest

build:
	go build -ldflags="-s -w" -o bin/$(SERVICE) ./cmd/

run:
	CONFIG_PATH=configs/config.yaml go run ./cmd/

test:
	go test ./... -race -count=1

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

docker-build:
	docker build -f Dockerfile -t $(IMAGE):$(TAG) ..

docker-run:
	docker compose up --build

clean:
	rm -rf bin/
