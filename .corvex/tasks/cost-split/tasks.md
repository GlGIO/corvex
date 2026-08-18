---
generated_by: corvex-recipe:cost-split
dag:
    S01: []
    S02:
        - S01
    S03:
        - S02
    S04:
        - S03
    S05:
        - S04
---

## S01 — Emitir o custo do reviewer na própria linha ❌ FAILED

```yaml
type: backend
kind: code
gates:
    - nature: policy
      max_attempts: 2
    - nature: inferential
      label: Review do diff de contabilidade
```

### O que fazer
Hoje `internal/step/ai_task.go` soma `attemptCost = workerCost + reviewerCost` e emite
esse número na linha `task_complete` (phase=worker), enquanto a linha `review_result`
do mesmo caminho sai SEM custo. Consequência: a agregação por fase (`internal/activity`,
`Summary.PerPhase`) atribui o dinheiro do reviewer ao balde `worker`, e a barra de custo
por natureza sub-reporta `review`.

Mude para: `review_result` carrega `CostUSD` = custo do reviewer daquela tentativa, e
`task_complete` carrega apenas o custo do worker. NÃO mude a semântica do teto de custo:
`e.charge` continua cobrando worker+reviewer do acumulado da task e do run.

O caminho do gate inferencial (`internal/step/gates.go`) já emite `review_result` com
custo e já cai no balde certo — não mexa nele, e não deixe o número ser contado duas vezes.

### Critérios de sucesso
- [ ] review_result do caminho de retry carrega o custo do reviewer
- [ ] task_complete carrega apenas o custo do worker
- [ ] o teto de custo por task e por run continua somando worker+reviewer
- [ ] nenhum custo é contado duas vezes na agregação

---

## S02 — Manter o custo por task inteiro na leitura ⏭️ SKIPPED

```yaml
type: backend
kind: code
depends_on: [S01]
gates:
    - nature: policy
      max_attempts: 2
    - nature: inferential
      label: Review dos dois leitores
```

### O que fazer
Com S01, quem lê `task_complete` como "o que a task custou" passa a ver só o worker.
Dois leitores dependem disso e têm de continuar mostrando worker+review por task:
`internal/ops/inspect.go` (BuildInspectReport, contrato do `inspect --json`) e
`internal/ops/run_show.go` (buildRunTaskRows, a tela do `run show`).

Some o custo de `review_result` no mesmo balde da task nos dois. A soma por FASE
continua separada — é a linha que decide, não o agregado por task.

### Critérios de sucesso
- [ ] inspect --json continua mostrando worker+review no custo da task
- [ ] run show continua mostrando worker+review por step
- [ ] a barra por natureza mostra review > 0 quando houve reviewer pago

---

## S03 — Suíte inteira verde ⏭️ SKIPPED

```yaml
type: general
kind: test
command: "go test ./... -count=1"
depends_on: [S02]
```

---

## S04 — Invariantes de tamanho e domínio ⏭️ SKIPPED

```yaml
type: general
kind: test
command: "test -z \"$(for f in cmd/*.go; do case \"$f\" in *_test.go) continue;; esac; n=$(wc -l < \"$f\"); [ \"$n\" -gt 150 ] && echo \"$f\"; done)\" && test -z \"$(grep -rn -iE 'azure|smartcare|yandeh' --include='*.go' cmd internal | grep -vE ':[0-9]+:[[:space:]]*//' | grep -v _test.go)\""
depends_on: [S03]
```

---

## S05 — Commitar a mudança de formato do ledger ⏭️ SKIPPED

```yaml
type: general
kind: tool
command: "git add -A && git commit -m 'fix(ledger): custo do reviewer sai da linha do worker'"
depends_on: [S04]
gates:
    - nature: human
      when: before
      label: Aprovar mudança de formato do ledger
      prompt: Isto muda o que a linha `task_complete` significa em todo ledger novo. Ledgers antigos continuam legíveis, mas a coluna passa a querer dizer outra coisa a partir daqui. Aprova?
evidence:
    - kind: diff
      label: Diff
      required_reading: true
      from: git diff --stat
    - kind: test_output
      label: Suíte
      from: go test ./internal/activity/ ./internal/step/ ./internal/ops/ -count=1
```
