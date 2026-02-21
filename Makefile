.PHONY: build test lint check bench

build:
	go build ./...

test:
	go test -race -failfast ./...

lint:
	golangci-lint run ./...
	go vet ./...

bench:
	go test -bench=. -benchmem ./mcpotel/

check: lint test
