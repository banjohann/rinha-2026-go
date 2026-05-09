FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/api /api

# Reference data is mounted at /data via the docker-compose volume.
# This keeps the 50 MB references.json.gz out of the image.
ENV DATA_DIR=/data LISTEN_ADDR=:8000
EXPOSE 8000

ENTRYPOINT ["/api"]
