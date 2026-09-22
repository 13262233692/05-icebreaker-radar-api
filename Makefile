.PHONY: build test vet fmt run infra sim down

build:
	go build -o bin/server ./cmd/server
	go build -o bin/radar_sim ./cmd/radar_sim

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

infra:
	docker compose up -d postgres redis

run:
	go run ./cmd/server

sim:
	go run ./cmd/radar_sim

down:
	docker compose down
