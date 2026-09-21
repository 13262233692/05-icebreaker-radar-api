.PHONY: build run simulator test fmt vet up down logs

build:
	go build -o bin/server ./cmd/server && go build -o bin/simulator ./cmd/simulator

run: build
	./bin/server

simulator: build
	./bin/simulator

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

up:
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f api
