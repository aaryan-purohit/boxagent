VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test test-int lint cover

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/boxagent ./cmd/boxagent

test:
	go test -race ./...

test-int:
	go test -race -p 1 -tags integration ./... # -p 1: the prune test removes every sandbox

lint:
	golangci-lint run

cover:
	go test -race -coverprofile=cover.out ./...
	go tool cover -func=cover.out
