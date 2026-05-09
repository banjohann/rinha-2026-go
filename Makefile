.PHONY: build test run preprocess docker compose-up compose-down clean

build:
	go build -trimpath -ldflags="-s -w" -o ./bin/api ./cmd/api

test:
	go test ./...

# Generate data/index.bin from data/references.json.gz. Required before `make run`
# unless an index.bin already exists. The Dockerfile builder runs the same step.
preprocess:
	go run ./cmd/preprocess data/references.json.gz data/index.bin

run: build preprocess
	DATA_DIR=./data LISTEN_ADDR=:8000 ./bin/api

docker:
	DOCKER_BUILDKIT=1 docker build --platform=linux/amd64 -t banjohann/rinha-2026-go:v1 .

compose-up: docker
	docker compose up -d

compose-down:
	docker compose down

clean:
	rm -rf ./bin
