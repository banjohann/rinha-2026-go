# Prévia 01 — primeira execução

**Commit testado**: `0cf9fe3`
**Data**: 2026-05-09
**Score final**: `-6000` (piso absoluto, ambos os componentes capados)

## Resultado bruto

```json
{
  "expected": {
    "total": 54100,
    "fraud_count": 24058,
    "legit_count": 30042,
    "fraud_rate": 0.4447,
    "legit_rate": 0.5553,
    "edge_case_count": 797,
    "edge_case_rate": 0.0147
  },
  "p99": "2001.96ms",
  "scoring": {
    "breakdown": {
      "false_positive_detections": 0,
      "false_negative_detections": 0,
      "true_positive_detections": 49,
      "true_negative_detections": 43,
      "http_errors": 39466
    },
    "failure_rate": "99.77%",
    "weighted_errors_E": 197330,
    "error_rate_epsilon": 4.988372,
    "p99_score": { "value": -3000, "cut_triggered": true },
    "detection_score": { "value": -3000, "cut_triggered": true },
    "final_score": -6000
  }
}
```

Runtime confirmado pelo engine: 1 CPU total, 350 MB total, 1 LB + 2 APIs em rede bridge — checklist da Rinha OK.

## Diagnóstico

A boa notícia: **o algoritmo está correto**. Das 92 respostas bem-sucedidas, todas acertaram (49 TP + 43 TN, zero FP, zero FN).

A má notícia: **só conseguimos responder 0.17% das requisições**. O resto (99.77%) virou HTTP error — quase certamente timeout do HAProxy ou do test runner (k6 com `timeout: 2001ms`).

### Por que estamos lentos

Conta de guardanapo:

- 54.100 requisições no teste, distribuídas (estimando) em ~60 s → **~900 RPS**
- Brute-force k-NN sobre 3M referências × 14 dimensões = **~42 M ops por query**
- Orçamento: 0.45 CPU × 2 instâncias = **0.9 CPU efetivo**
- A ~1 G ops/s/core integer, cada query custa **~40–80 ms wall-clock** sob essa quota
- Throughput máximo teórico: **~12–22 RPS** sequencial — ~50× abaixo dos 900 RPS

Quando a chegada (900 RPS) excede a capacidade (12–22 RPS), as requisições enfileiram em queue invisível. A 50ª requisição em fila espera 50 × 50ms = 2500 ms — bate o timeout, vira HTTP error. **É um pile-up clássico**.

O p99 = 2001.96ms é exatamente o teto do timeout do test runner. Ou seja, p99 está saturando o limite — nem é uma medida real do nosso processamento, é o cap.

## Causas-raiz, ordenadas por impacto

| # | Causa | Impacto | Esforço para resolver |
|---|---|---|---|
| 1 | Brute-force O(N) sobre 3M refs por query | ~50× lento | Alto (trocar por ANN: HNSW, VP-tree) |
| 2 | Sem paralelização da varredura dentro da query | 2–4× | Baixo |
| 3 | Sem limite de concorrência → pile-up vira timeout em massa | causa direta dos 39k errors | Baixo |
| 4 | JSON parsing no caminho quente | 1.2–1.5× | Médio (`encoding/json/v2` ou parser custom) |
| 5 | Alocação de buffers a cada request (pressão de GC) | 1.1–1.3× | Baixo (`sync.Pool`) |

Causa #1 é quem dita o teto absoluto. Causas #2–#5 são amortecedores que só ajudam se #1 já estiver razoável; sem isso, o algoritmo é lento demais pra qualquer otimização menor reverter o piso.

## Plano de melhoria

### Fase 1 — quick wins (~2–4h, ROI moderado)

**1.1 Paralelizar k-NN dentro de uma query**
Split das refs em N chunks (N = `min(GOMAXPROCS, 4)`), cada um com heap local de 5, merge final. Sob CPU quota estrita, ganho real é menor que parece (a quota é em CPU-time agregado), mas distribui o trabalho entre cores e reduz tempo de ocupação de uma core específica. Esperado: 1.5–2.5× wall-clock.

**1.2 Limitar concorrência (semáforo)** ⏸ adiado
Buffered channel `make(chan struct{}, MaxInflight)` no handler. Se cheio, retorna 503 imediatamente — **trocar timeout (peso 5 nos errors) por shed rápido**. Está provavelmente mais perto da causa-raiz dos 39k errors do que 1.1, mas estamos adiando pra medir o efeito de 1.1 + 1.3 isolados primeiro.

**1.3 `sync.Pool` para request buffers**
Pool de `*detector.Request` e do array `[14]float32`. Reduz pressão de GC. Esperado: 1.1–1.3×.

### Fase 2 — IVF (escolhida sobre HNSW por orçamento de memória) ✅ implementada

**Decisão**: IVF (Inverted File com k-means), não HNSW. HNSW com parâmetros decentes (M≥8) precisaria de ≥96 MB pro grafo do layer 0 — não cabe nos ~43 MB de headroom por instância. IVF se encaixa em ~6 MB.

**Implementação**:
- `cmd/preprocess` faz k-means K=1024 sobre os 3M vetores (10 iters, ~4min 30s no host de dev), reordena os vetores por cluster pra leitura contígua, e serializa centroids + offsets em `index.bin`
- Runtime: query encontra os P=3 centroids mais próximos, brute-force só nesses 3 clusters (~9000 records vs 3M = **~330× menos trabalho**)

**Medições locais**:
- Build (preprocess): 4 min 30 s — uma vez por imagem
- Query latency: **0.5–0.85 ms** (vs 12–28 ms brute-force) — **25–50× speedup**
- Cold start: 207 ms (inalterado — quem demora é o k-means, que roda no build)
- Memória extra runtime: ~6 MB (centroids 28 KB + cluster IDs 6 MB + offsets 4 KB)
- Correção: ambos exemplos da spec (legit e fraud) retornam o resultado correto

### Fase 3 — squeeze final (4–8h)

- 3.1 SIMD-like via `unsafe` (ler 4 uint16 como uint64 e calcular paralelo)
- 3.2 JSON parser mais rápido (json/v2 ou sonic)
- 3.3 Tunning HAProxy (http-reuse, maxconn, timeouts mais agressivos)

## O que vai virar a Prévia 02

Implementar **1.1 + 1.3** (skip 1.2 por enquanto). Rodar nova prévia. Se o p99 ainda saturar e os HTTP errors continuarem altos, isso confirma que **1.2 + Fase 2 são obrigatórios**.

Se a melhoria for marginal e os HTTP errors caírem só um pouco, a hipótese vira: **a quota de CPU é o gargalo absoluto e o único caminho real é Fase 2**.

### Hipóteses a testar na Prévia 02

| Hipótese | Como confirmaria |
|---|---|
| H1: Paralelização ajuda mesmo sob quota | Se p99 < 1500ms e errors < 30k |
| H2: Quota de CPU é o gargalo absoluto | Se p99 ainda em 2000ms e errors > 30k |
| H3: GC era um contribuinte significativo | Se errors caem só por causa do `sync.Pool` (improvável isolado) |
