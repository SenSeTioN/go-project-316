.PHONY: install build test run lint

install:
	go mod download

build:
	go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler

test:
	go test ./...

run:
	go run ./cmd/hexlet-go-crawler $(URL)

lint:
	golangci-lint run
