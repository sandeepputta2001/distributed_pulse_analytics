.PHONY: build run test lint docker-build clean

SERVICE   := auth
IMAGE     := pulse-analytics/$(SERVICE)
IMAGE_TAG ?= latest
ECR       ?= ""

build:
	go build -ldflags="-s -w" -o bin/$(SERVICE) ./cmd/

run:
	CONFIG_PATH=configs/config.yaml go run ./cmd/

test:
	go test ./... -race -timeout 60s

lint:
	golangci-lint run ./...

docker-build:
	docker build -f Dockerfile -t $(IMAGE):$(IMAGE_TAG) ..

docker-push:
	docker tag $(IMAGE):$(IMAGE_TAG) $(ECR)/$(IMAGE):$(IMAGE_TAG)
	docker push $(ECR)/$(IMAGE):$(IMAGE_TAG)

clean:
	rm -rf bin/
