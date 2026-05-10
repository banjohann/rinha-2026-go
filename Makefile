IMAGE       := banjohann/rinha-2026-go
TAG         := v5
RINHA       := ../rinha-de-backend-2026
COMPOSE_DEV := docker compose -f docker-compose.yml -f docker-compose.dev.yml

.PHONY: build test run preprocess docker push compose-up compose-down clean test-load dev-up dev-down

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

# Dev stack: docker-compose.yml + docker-compose.dev.yml overlay (build:, ports).
# Overlay is only loaded by the targets that pass `-f` explicitly — never auto-loaded,
# never copied to the submission branch. Production `make compose-up` ignores it.
dev-up:
	$(COMPOSE_DEV) up -d --build

dev-down:
	$(COMPOSE_DEV) down

# Roda o k6 oficial da Rinha contra o stack DEV local (HAProxy :9999 + 2 APIs em :8000
# com /debug/metrics em :8001). Mesmo script que a engine roda na prévia, mesmos
# pesos/cortes — `test/results.json` é comparável 1:1 com o resultado oficial.
#
# Pré-requisitos:
#   - k6 instalado (AUR: k6-bin)
#   - $(RINHA)/test/test-data.json existe (já vem no clone do repo da Rinha)
#   - Para coletar histogramas, rode com METRICS=1 make test-load
test-load:
	@command -v k6 >/dev/null 2>&1 || { echo "k6 not installed (yay -S k6-bin)"; exit 1; }
	@test -f $(RINHA)/test/test-data.json || { echo "missing $(RINHA)/test/test-data.json"; exit 1; }
	$(COMPOSE_DEV) up -d --build
	@echo "waiting for /ready ..."
	@for i in $$(seq 1 60); do \
		code=$$(curl -s -o /dev/null -w '%{http_code}' http://localhost:9999/ready); \
		if [ "$$code" = "200" ]; then echo "ready"; break; fi; \
		sleep 1; \
		if [ $$i = 60 ]; then echo "timeout waiting for /ready"; exit 1; fi; \
	done
	cd $(RINHA) && K6_NO_USAGE_REPORT=true k6 run test/test.js
	@echo "--- results ---"
	@cat $(RINHA)/test/results.json | jq .scoring
	@mkdir -p runs
	@ts=$$(date +%Y%m%d-%H%M%S); \
	  cp $(RINHA)/test/results.json runs/$$ts-results.json; \
	  if curl -fsS -m 1 http://localhost:8001/debug/metrics -o runs/$$ts-metrics.txt 2>/dev/null; then \
	    echo "saved runs/$$ts-results.json + runs/$$ts-metrics.txt"; \
	  else \
	    rm -f runs/$$ts-metrics.txt; \
	    echo "saved runs/$$ts-results.json (metrics endpoint returned non-200 — re-run with METRICS=1 make test-load)"; \
	  fi
