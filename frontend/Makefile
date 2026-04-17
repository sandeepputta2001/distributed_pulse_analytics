.PHONY: install dev build lint docker-build docker-run

SERVICE := frontend
IMAGE   := pulse-analytics/$(SERVICE)
TAG     ?= latest

install:
	npm ci

dev:
	npm run dev

build:
	npm run build

lint:
	npm run lint

docker-build:
	docker build -t $(IMAGE):$(TAG) .

docker-run:
	docker compose up --build

clean:
	rm -rf dist/ node_modules/
