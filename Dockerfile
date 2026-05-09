FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY data ./data

# Pre-process the reference dataset into a compact binary index.
# Cuts cold start from ~10 s (gunzip + JSON decode of 3M records) to ~100 ms
# (single sequential read of a memory-layout-matching binary file).
RUN go run ./cmd/preprocess data/references.json.gz data/index.bin && \
    rm data/references.json.gz

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/api /api
COPY --from=builder /src/data/index.bin /data/index.bin
COPY --from=builder /src/data/mcc_risk.json /data/mcc_risk.json
COPY --from=builder /src/data/normalization.json /data/normalization.json

ENV DATA_DIR=/data LISTEN_ADDR=:8000
EXPOSE 8000

ENTRYPOINT ["/api"]
