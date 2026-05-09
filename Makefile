.PHONY: build test run docker compose-up compose-down clean

build:
	go build -trimpath -ldflags="-s -w" -o ./bin/api ./cmd/api

test:
	go test ./...

run: build
	DATA_DIR=./data LISTEN_ADDR=:8000 ./bin/api

docker:
	docker build -t rinha-2026-go:latest .

compose-up:
	docker compose up --build -d

compose-down:
	docker compose down

clean:
	rm -rf ./bin
