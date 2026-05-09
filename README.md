# rinha-2026-go

Go implementation of the [Rinha de Backend 2026](../rinha-de-backend-2026) fraud-detection challenge.

## Stack

- **Language**: Go (stdlib only — `net/http`, `encoding/json`, `compress/gzip`)
- **Load balancer**: HAProxy (round-robin)
- **Storage**: 3M reference vectors held in RAM per instance, quantized to `uint8`
- **Search**: brute-force k-NN (k=5) with squared Euclidean distance and a fixed-size max-heap

## Layout

```
cmd/api/             entrypoint
internal/detector/   vectorize, quantize, store, k-NN
internal/server/     HTTP handlers (/ready, /fraud-score)
data/                normalization.json, mcc_risk.json, references.json.gz
haproxy/             haproxy.cfg
```

## Run

```sh
# Tests
make test

# Local single instance (no LB)
make run
curl localhost:8000/ready          # 503 while loading, 200 once loaded

# Full stack via docker-compose (HAProxy + 2 APIs)
make compose-up
curl localhost:9999/ready
curl -X POST localhost:9999/fraud-score \
     -H 'content-type: application/json' \
     -d @../rinha-de-backend-2026/resources/example-payloads.json
```

## Memory budget

| Service     | CPU  | Memory |
|-------------|------|--------|
| HAProxy     | 0.10 | 30 MB  |
| api-1       | 0.45 | 160 MB |
| api-2       | 0.45 | 160 MB |
| **Total**   | 1.00 | 350 MB |

Per-instance: ~45 MB references (3M × 14 bytes vectors + 3M × 1 byte labels) + Go runtime overhead.

## Quantization

Each `[0,1]` vector dim maps to `[0, 254]`. The value `255` is reserved as the
`-1` sentinel for dims 5 and 6 when `last_transaction == null`. Distance
contribution between two non-sentinel values is `(a-b)²`; between a sentinel
and a non-sentinel it is `254² = 64516` (max per-dim cost), so sentinel and
non-sentinel records cluster apart.
