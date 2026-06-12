.PHONY: run server tui build test lint tidy docker-up docker-down

run: server

server:
	go run .

tui:
	go run ./cmd/art

build:
	mkdir -p bin
	go build -o bin/art-server .
	go build -o bin/art ./cmd/art

test:
	go test ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l .)" || (echo "run gofmt"; exit 1)

tidy:
	go mod tidy

docker-up:
	docker compose up -d

docker-down:
	docker compose down
