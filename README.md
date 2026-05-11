# rinha-2026-go

Submissão da [Rinha de Backend 2026](https://github.com/zanfranceschi/rinha-de-backend-2026) — uma API de detecção de fraude por busca vetorial em transações de cartão.

**Stack**: Go (stdlib pura, sem frameworks) + HAProxy. Sem banco de dados, sem vector DB — todas as 3 milhões de referências ficam em RAM, em formato compactado.

## Arquitetura

```
                Cliente
                  │
                  ▼
         ┌──────────────────┐
         │  HAProxy :9999   │  round-robin, sem lógica de detecção
         └────────┬─────────┘
                  │
        ┌─────────┴─────────┐
        ▼                   ▼
   ┌──────────┐        ┌──────────┐
   │  api-1   │        │  api-2   │  Go, net/http, sem framework
   │  :8000   │        │  :8000   │
   │          │        │          │  Cada uma carrega as 3M
   │  Store   │        │  Store   │  referências em RAM no startup
   │ (uint16) │        │ (uint16) │
   └──────────┘        └──────────┘
```

Limites do desafio: **1 CPU e 350 MB de RAM** distribuídos entre todos os containers.

| Serviço | CPU  | Memória |
|---------|------|---------|
| HAProxy | 0.20 | 30 MB   |
| api-1   | 0.40 | 160 MB  |
| api-2   | 0.40 | 160 MB  |
| **Total** | **1.00** | **350 MB** |

A alocação de CPU foi ajustada na Prévia 05 após instrumentação por pprof revelar que o HAProxy era o gargalo a 0.10 CPU — as APIs estavam ociosas (~19% da própria quota) enquanto a fila de conexões no LB acumulava.

## Endpoints

- `GET /ready` — retorna **503** enquanto o índice está sendo carregado (~200 ms desde a Fase 2, que moveu o preprocess pra build time); **200** depois.
- `POST /fraud-score` — recebe a transação, retorna `{ approved, fraud_score }`.

Contrato completo do payload em [API.md](https://github.com/zanfranceschi/rinha-de-backend-2026/blob/main/docs/br/API.md).

## Etapas macro de uma requisição

```
1. POST /fraud-score com payload de transação
2. Decode JSON       → struct Request               (encoding/json + sync.Pool)
3. Vetoriza          → [14]float32                  (REGRAS_DE_DETECCAO.md)
4. Quantiza          → [14]uint16                   (mesmo formato do Store)
5. Busca k-NN (IVF)  → top-P clusters, depois top-5  (~8.8K refs varridos)
6. Vota              → fraud_score = #fraud / 5
7. Decide            → approved = fraud_score < 0.6
8. Encode JSON       → resposta
```

## Como funciona a busca vetorial

### 1. Vetorização (14 dimensões)

Cada transação é mapeada para um vetor de 14 floats em `[0, 1]`, seguindo as fórmulas da especificação. As constantes (`max_amount`, `max_km`, etc.) vêm de `data/normalization.json`.

| Índice | Dimensão | Fórmula |
|---|---|---|
| 0  | `amount`               | `clamp(amount / 10000)` |
| 1  | `installments`         | `clamp(installments / 12)` |
| 2  | `amount_vs_avg`        | `clamp((amount / customer.avg_amount) / 10)` |
| 3  | `hour_of_day`          | `hour / 23` (UTC) |
| 4  | `day_of_week`          | `dayOfWeek / 6` (Mon=0..Sun=6) |
| 5  | `minutes_since_last_tx`| `clamp(min/1440)` ou **`-1`** se `last_transaction == null` |
| 6  | `km_from_last_tx`      | `clamp(km/1000)`  ou **`-1`** se `last_transaction == null` |
| 7  | `km_from_home`         | `clamp(km / 1000)` |
| 8  | `tx_count_24h`         | `clamp(count / 20)` |
| 9  | `is_online`            | `0 ou 1` |
| 10 | `card_present`         | `0 ou 1` |
| 11 | `unknown_merchant`     | `1` se merchant não conhecido pelo cliente |
| 12 | `mcc_risk`             | lookup em `mcc_risk.json` (default `0.5`) |
| 13 | `merchant_avg_amount`  | `clamp(avg / 10000)` |

O sentinel **`-1`** nas dimensões 5 e 6 sinaliza ausência de transação anterior. É preservado em todo o pipeline (não é tratado como zero) para não confundir "sem histórico" com "histórico recente e próximo".

Implementação em [`internal/detector/vector.go`](internal/detector/vector.go).

### 2. Quantização (`uint16`)

Manter 3M × 14 floats × 4 bytes em RAM custaria **168 MB por instância** — ultrapassa o limite de 350 MB para duas réplicas. A solução é quantizar:

- Cada dimensão `[0, 1]` → `[0, 65534]` (uint16, 16 bits)
- Sentinel `-1` → `65535`
- Erro de quantização: ~1.5×10⁻⁵ por dimensão (efetivamente lossless para a precisão dos dados)
- Memória resultante: **~84 MB por instância** (3M × 14 × 2 bytes vetores + 3M × 1 byte labels)

A distância euclidiana ao quadrado entre dois quantizados pode exceder `int32`, então o cálculo usa `uint64` no acumulador e diferença absoluta em `uint32` antes de elevar ao quadrado — evita overflow silencioso.

Implementação em [`internal/detector/quantize.go`](internal/detector/quantize.go).

### 3. Storage in-memory

```go
type Store struct {
    Vectors []uint16  // flat: N*14, vetor i ocupa [i*14 : (i+1)*14]
    Labels  []uint8   // 0 = legit, 1 = fraud
    N       int
}
```

Layout flat (não `[][]uint16`) para reduzir overhead de slice headers e melhorar localidade de cache durante o scan.

O carregamento usa `compress/gzip` + `json.Decoder` em modo streaming — decodifica registro por registro e quantiza inline, sem materializar o array completo de 3M `referenceRecord` na memória ao mesmo tempo.

Implementação em [`internal/detector/store.go`](internal/detector/store.go).

### 4. Busca k-NN com IVF (k=5, P=3 probes)

A primeira versão fazia brute-force sobre as 3M referências. Bateu em p99 alto demais sob 0.40 CPU. A versão atual usa **IVF (Inverted File Index)**:

**Build-time** (em `cmd/preprocess`):
1. K-means com **K=1024 clusters** sobre os 3M vetores quantizados (10 iterações, init aleatória).
2. Cada referência é atribuída ao seu centroide mais próximo.
3. Vetores são reordenados no disco agrupados por cluster, junto com um array `Offsets[K+1]` indicando os ranges.

**Query-time** (em `internal/detector/knn.go`):
```
1. topPCentroids(query, P=3)         → 3 clusters mais próximos
2. para cada cluster c em top-P:
       scanRange(query, Offsets[c], Offsets[c+1])
3. Heap final de 5 → conta fraudes
```

Em média **~8.788 referências** são varridas por query (3M / 1024 × 3) — ~340× menos que o brute-force. Recall@5 medido em ~99.94% (30 erros de classificação em 54K queries na Prévia 05).

- **Distância**: euclidiana ao quadrado (sem `sqrt` — métrica monotônica, ranking idêntico)
- **Sentinel**: `contrib(sentinel, sentinel) = 0` (match perfeito); `contrib(sentinel, valor) = QuantScale²` (custo máximo, separa os dois grupos)
- **Heap**: max-heap fixo de tamanho 5 — o pior elemento (maior distância) fica no topo, então comparação contra `heap[0]` decide se vale a pena processar a referência inteira
- **Early termination**: a soma das contribuições de cada dimensão é monotonicamente crescente — assim que `d² ≥ pior_no_heap`, abortamos a iteração da referência atual e pulamos para a próxima

Implementação em [`internal/detector/knn.go`](internal/detector/knn.go) e [`cmd/preprocess/main.go`](cmd/preprocess/main.go).

### 5. Decisão

```go
fraudCount := store.TopKFraudCount(quantizedQuery)
score      := float32(fraudCount) / 5.0
approved   := score < 0.6
```

Como `k = 5`, `fraud_score` só pode assumir 6 valores discretos: `0.0, 0.2, 0.4, 0.6, 0.8, 1.0`.

## Layout do projeto

```
cmd/api/                  entrypoint: carrega index.bin, sobe HTTP server
cmd/preprocess/           build-time: gera index.bin (k-means K=1024 + reorder por cluster)
internal/detector/
  vector.go               vetorização (14 dimensões)
  quantize.go             float32 ↔ uint16 com sentinel
  store.go                Store em RAM (vetores, labels, centroides, offsets)
  knn.go                  IVF k-NN com max-heap fixo (P=3 probes)
  mcc.go, normalization.go  loaders dos JSONs auxiliares
  types.go                Request/Response (também usado pelo server)
internal/server/
  server.go               http.Server, atomic.Pointer[Store], pprof/metrics opcionais
  handlers.go             /ready (503/200) e /fraud-score com sync.Pool
  metrics.go              instrumentação atrás de METRICS=1 / PPROF=1
data/                     normalization.json, mcc_risk.json, index.bin
haproxy/haproxy.cfg       round-robin, http-reuse always, health check em /ready
Dockerfile                multi-stage; roda preprocess no builder, ~70 MB final
docker-compose.yml        haproxy + 2 APIs com limits de CPU/RAM
```

## Como rodar

### Testes unitários

```sh
make test
```

Cobre: vetorização (com os exemplos da spec), round-trip de quantização (incluindo sentinel), k-NN comparado a um oráculo naïve em 50 queries randômicas, sentinel clustering, loaders de gzip, handlers (`httptest`).

### Local, sem container

```sh
DATA_DIR=./data make run
curl -i localhost:8000/ready          # 503 enquanto carrega, 200 depois
```

### Stack completa via docker-compose

```sh
make docker         # builda a imagem amd64
docker compose up -d
curl -i localhost:9999/ready
curl -X POST localhost:9999/fraud-score \
  -H 'Content-Type: application/json' \
  -d @../rinha-de-backend-2026/resources/example-payloads.json
```

## Decisões de projeto e trade-offs

- **Sem framework HTTP**: `net/http` da stdlib é suficiente; cada handler é uma goroutine, sem middleware no caminho quente.
- **Sem vector DB**: para 3M × 14 dims, o overhead de IPC + serialização de qualquer DB externo (pgvector, Qdrant) seria maior que o ganho. Em RAM com layout flat, o k-NN é cache-friendly.
- **IVF ao invés de brute-force ou HNSW**: brute-force foi a v1 e bateu em p99 alto demais. HNSW tem construção cara e overhead de grafo de vizinhança que estoura o orçamento de memória. IVF com K=1024 e P=3 entrega ~99.94% de recall a um custo de ~8.8K refs varridos por query.
- **Quantização uint16 ao invés de uint8 ou float32**: `uint8` (255 níveis) introduzia ruído mensurável no ranking dos top-5; `float32` estourava memória; `uint16` é o ponto ótimo.
- **Pré-processamento no build, não no startup**: a Fase 2 moveu k-means + quantização + reorder para `cmd/preprocess`, executado no builder stage do Dockerfile. Imagem runtime lê o `index.bin` mmap-friendly e fica pronta em ~200 ms.
- **HAProxy com `option httpchk GET /ready`**: garante que requisições não cheguem nas APIs antes do Store estar pronto.
- **HAProxy a 0.20 CPU**: descoberto via pprof na Prévia 05 — abaixo disso, o LB vira gargalo silencioso enquanto as APIs ficam ociosas.
- **Instrumentação atrás de feature flag**: `METRICS=1` liga histogramas por estágio e `/debug/metrics`; `PPROF=1` liga `/debug/pprof/*`. Default off, custo zero em produção.

## Resultado atual (Prévia 05)

- `final_score = +4671.39`
- `p99 = 5.83 ms` no engine oficial
- `FP = 16`, `FN = 14`, `errors = 0` em 54.100 requests (99.94% de acerto)
- Detalhes em [`docs/history.md`](docs/history.md)

## Próximos passos

Os planos abaixo estão detalhados em `docs/`:

- **Plano 03 — Latência** (alvo p99 < 2 ms): `GOAMD64=v3` + `unsafe.Slice` + loop unroll no `scanRange`; re-tunar IVF para K=2048 P=3; eventualmente SWAR no inner loop. Headroom: até +767 pontos. ([`docs/03-finetuning.md`](docs/03-finetuning.md))
- **Plano 04 — Precisão** (alvo FP+FN ≤ 10): subir IVF probes de 3 pra 5, probes adaptativos por proximidade à borda do cluster, fallback brute-force para edge cases. Headroom: até +562 pontos. ([`docs/04-precisao.md`](docs/04-precisao.md))

Ordem: latência primeiro (Plano 03), precisão depois (Plano 04) — porque aumentar probes adiciona trabalho por query, e queremos baseline rápido antes de pagar esse custo.

## Licença

MIT
