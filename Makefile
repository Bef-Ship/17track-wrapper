.PHONY: test build run tidy docker

IMAGE ?= tracking-service:local

test:
	go test ./... -count=1

build:
	go build -o bin/tracking-api ./cmd/api

run:
	go run ./cmd/api

tidy:
	go mod tidy

docker:
	docker build -t $(IMAGE) .
