.PHONY: build run test lint docker-build docker-push swag fmt

SERVICE   := query-api
IMAGE     := pulse-analytics/$(SERVICE)
TAG       ?= latest
PORT      := 8085

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

swag:
	swag init -g cmd/main.go -o docs/swagger

docker-build:
	docker build -f Dockerfile -t $(IMAGE):$(TAG) ..

docker-push:
	docker push $(IMAGE):$(TAG)

docker-run:
	docker compose up --build

clean:
	rm -rf bin/
