# Histórico de prévias

Cada prévia é uma execução do `rinha/test` na engine oficial. Linha de baseline: começamos no piso (-6000) e o objetivo é subir.

## Resumo geral

| # | Data | Commit | Score final | p99 | HTTP errors | TP+TN (acertos) | Total | Stack na prévia |
|---|------|--------|-------------|-----|-------------|------------------|-------|------------------|
| 01 | 2026-05-09 | `0cf9fe3` | **-6000** | 2001.96 ms (cap) | 39.466 | 92 | 54.100 | brute-force k-NN sequencial, uint16, dados em `/data` |
| 02 | 2026-05-09 | `0cf9fe3` | **-6000** | 2001.89 ms (cap) | 45.964 | 105 | 54.100 | _idem 01 — imagem não foi republicada, código novo não foi testado_ |
| 03 | 2026-05-09 | `f133c09` | **-6000** | 2001.90 ms (cap) | 43.273 | 118 | 54.100 | preprocess no build + k-NN paralelo + `sync.Pool` (`:v2`) |

## Detalhes

### Prévia 01 — 2026-05-09 (`0cf9fe3`)

Primeira submissão. O algoritmo está correto mas a throughput é absurdamente baixa pra carga do teste — basicamente nenhum request é respondido a tempo.

| Métrica | Valor |
|---|---|
| Total de requests | 54.100 |
| Acertos TP (fraude detectada) | 49 |
| Acertos TN (legit aprovado) | 43 |
| Falsos positivos (FP) | 0 |
| Falsos negativos (FN) | 0 |
| HTTP errors (timeout/etc) | 39.466 |
| Failure rate | 99.77 % |
| Weighted errors E (1·FP + 3·FN + 5·Err) | 197.330 |
| Error rate ε | 4.988 |
| p99 | 2001.96 ms (cap do test runner) |
| score_p99 | **-3000** (cut, p99 > 2000 ms) |
| score_det | **-3000** (cut, failure_rate > 15 %) |
| **score_final** | **-6000** |

**Configuração**:
- Brute-force k-NN sobre 3M referências, sequencial (1 goroutine por query)
- Quantização `uint16` em RAM, dados copiados pra `/data` no Dockerfile (`references.json.gz` + 2 JSONs pequenos)
- Sem `sync.Pool`, sem limite de concorrência, sem paralelização, sem pré-processamento binário
- Cold start: ~10 s (gunzip + JSON decode de 3M registros)

**Diagnóstico**: ver [`01-previa.md`](./01-previa.md). TL;DR: throughput estimado de ~12–22 RPS vs. carga de ~900 RPS → pile-up → timeout em massa. O algoritmo é correto mas O(N) sobre 3M é incompatível com 0.45 CPU por instância.

**Mudanças entre 01 e 02** (aplicadas no working tree, **ainda não pushadas no momento da Prévia 02**):
- Pré-processamento no build → cold start 10 s → 200 ms
- k-NN paralelizado em até 4 goroutines (capado pelo `GOMAXPROCS`)
- `sync.Pool` de `*detector.Request` pra reduzir pressão de GC

---

### Prévia 02 — 2026-05-09 (`0cf9fe3`)

**Importante**: rodou contra **o mesmo commit e a mesma imagem** da Prévia 01. As mudanças de paralelização + `sync.Pool` + preprocess ainda não tinham sido pushadas pro Docker Hub quando a issue foi aberta. Resultado é, na prática, uma reexecução do mesmo binário.

| Métrica | Valor | Δ vs Prévia 01 |
|---|---|---|
| Total de requests | 54.100 | = |
| Acertos TP | 54 | +5 |
| Acertos TN | 51 | +8 |
| FP | 0 | = |
| FN | 0 | = |
| HTTP errors | 45.964 | +6.498 |
| Failure rate | 99.77 % | = |
| Weighted errors E | 229.820 | +32.490 |
| Error rate ε | 4.989 | ≈ |
| p99 | 2001.89 ms | ≈ |
| score_p99 | -3000 (cut) | = |
| score_det | -3000 (cut) | = |
| **score_final** | **-6000** | = |

**O que isso ensina**:
- A engine da Rinha tem **variabilidade de carga entre execuções** — a Prévia 02 mandou aparentemente mais conexões antes do timeout (mais errors absolutos), mas o failure rate ficou idêntico (99.77 %). Útil saber pra interpretar futuras prévias: pequenas variações em TP/TN/errors absolutos são ruído; failure rate e score são os indicadores estáveis.
- Sempre **confirmar o digest da imagem no Docker Hub** antes de abrir a issue de prévia. `docker push` em si não invalida cache no test runner — se o tag `v1` no Hub ainda apontar pro digest velho, a engine puxa o velho. Considerar usar tags imutáveis (`v2`, `v3` etc.) ou tags de commit (`sha-0cf9fe3`).

**Próximo passo concreto**:
1. `git push` das mudanças locais
2. `make docker && docker push banjohann/rinha-2026-go:v1`
3. Verificar o digest com `docker buildx imagetools inspect banjohann/rinha-2026-go:v1`
4. Abrir nova issue `rinha/test`

---

### Prévia 03 — 2026-05-09 (`f133c09`, imagem `:v2`)

Primeiro teste do código com **preprocess no build + k-NN paralelo + `sync.Pool`**. Resultado positivo no nível de throughput, mas insuficiente pra escapar do piso.

| Métrica | Valor | Δ vs Prévia 01 |
|---|---|---|
| Total de requests | 54.100 | = |
| Acertos TP | 59 | +10 |
| Acertos TN | 59 | +16 |
| FP | 0 | = |
| FN | 0 | = |
| HTTP errors | 43.273 | +3.807 |
| Failure rate | 99.73 % | -0.04 pp |
| Weighted errors E | 216.365 | +19.035 |
| Error rate ε | 4.986 | ≈ |
| p99 | 2001.90 ms (cap) | ≈ |
| score_p99 | -3000 (cut) | = |
| score_det | -3000 (cut) | = |
| **score_final** | **-6000** | = |

**Delta de capacidade**: 92 → 118 acertos (**+28 %**). Vem provavelmente de:
- Preprocess no build → cold start 10 s → 200 ms → ~16 % a mais de janela útil de atendimento durante o teste (~60 s)
- Paralelização do k-NN: marginal sob 0.45 CPU quota (como previsto)
- `sync.Pool`: redução pequena de pressão de GC

**Conclusão**: Fase 1 ajudou exatamente como esperado (~modesto), mas **a Fase 2 é obrigatória pra sair do `-6000`**. Brute-force O(N) sobre 3M referências é fundamentalmente incompatível com 0.45 CPU — independente de paralelização. Pra atender ~900 RPS precisamos de ~1 ms por query; brute-force entrega ~50 ms.

**Confirmou-se** a hipótese H2 da análise da Prévia 01: a quota de CPU é o gargalo absoluto.

**Próximo passo**: implementar Fase 2 — substituir brute-force por índice ANN. Ver opções em `01-previa.md` § Fase 2. Recomendação atualizada:

- **HNSW** — ~50–500× speedup, recall@5 ≈ 99 %, ~300–500 linhas. Recomendado se queremos mirar score positivo.
- **VP-tree** — exato (sem perda de recall), ~5–15× speedup em 14 dims, ~150 linhas. Provavelmente insuficiente sozinho, mas combina com Fase 1.2.
- **Fase 1.2 (limite de concorrência)** isolada — não resolve, só troca timeouts (peso 5) por 503s rápidos (peso 5). Não muda o piso de failure_rate.

---

### Mudanças entre 03 e 04 (aplicadas, ainda não testadas)

**Fase 2 implementada com IVF (não HNSW)** — análise de memória mostrou que HNSW com M≥8 estoura o orçamento. IVF se encaixa em ~6 MB.

- `cmd/preprocess` agora roda k-means K=1024 (10 iters, ~4 min 30 s) e reordena os vetores por cluster
- Novo formato binário (`index.bin v2`) com magic + version + N + K + vetores reordenados + labels + centroids + offsets
- `Store` ganhou campos `Centroids`, `Offsets`, `K`
- `TopKFraudCount` agora faz IVF quando `K > 0`: encontra os 3 centroids mais próximos, brute-force só nesses 3 clusters
- Brute-force fica como fallback (`K = 0`) pra testes

Medição local (host de dev, sem CPU quota):
- Cold start: 207 ms
- Query: 0.5–0.85 ms (era 12–28 ms) → **25–50× speedup**
- Em container 0.45 CPU, esperado ~2–3 ms por query → throughput por instância ~400 RPS → 2 instâncias = ~800 RPS, próximo dos 900 RPS da carga

Tag bumpada pra `:v3`.

---

> Atualizar essa tabela toda vez que uma nova prévia rodar. Se um número parece bom demais, dobrar o check antes de comemorar.
