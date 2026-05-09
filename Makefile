IMAGE := banjohann/rinha-2026-go
TAG   := v2

.PHONY: build test run preprocess docker push compose-up compose-down clean

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

# Build the linux/amd64 image and tag it both with the immutable TAG and :latest.
# Override the tag from the CLI: `make docker TAG=v3`.
docker:
	docker build --platform=linux/amd64 -t $(IMAGE):$(TAG) -t $(IMAGE):latest .

# Build + push both tags. Run this whenever you want a new submission to test.
# Bump TAG in this file (or via CLI) so each preview is testing a fresh digest.
push: docker
	docker push $(IMAGE):$(TAG)
	docker push $(IMAGE):latest

compose-up: docker
	docker compose up -d

compose-down:
	docker compose down

clean:
	rm -rf ./bin
