.PHONY: test build run tidy worker

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/tracking ./cmd/tracking

run:
	go run ./cmd/tracking

worker:
	TRACKING_ROLE=worker go run ./cmd/tracking

tidy:
	go mod tidy
