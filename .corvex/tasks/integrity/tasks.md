---
generated_by: corvex-planner
generated_at: "2026-06-26T00:00:00Z"
dag:
    S01: []
    S02: []
    S03: []
    S04:
        - S01
        - S02
        - S03
---

## S01 — Fail on truncated provider output (no result line) ✅ PASSED

```yaml
type: backend
```

### O que fazer
Endurecer o provider Claude para que a ausência da linha NDJSON final `result`
seja tratada como falha, e não como sucesso silencioso com saída parcial.

Hoje, em `internal/provider/claude/claude.go`, tanto `ExecuteWithProgress`
(linhas ~63-144) quanto `ParseFullOutput` (linhas ~464-501) detectam a linha
`result` via `raw.Type == "result"` mas **nunca registram se ela apareceu**. Se
o stream termina sem essa linha e o processo saiu com código 0, ambos retornam
um `*types.ExecuteResult` de sucesso com o `Output` parcial acumulado.

> [verbatim from spec.md §Objective A]
> The Claude CLI emits a final NDJSON line `{"type":"result", ...}` carrying the
> cost/tokens and the final result text. If the stream ends WITHOUT a `result`
> line (process killed, pipe truncated, network cut), today the provider returns
> a success with whatever partial output it accumulated — a silent false success.

> [verbatim from spec.md §Objective A]
> If the command exited 0 but NO `result` line was observed, return a non-nil
> error (e.g. "claude cli produced no result line (truncated output?)") instead
> of a successful `*types.ExecuteResult`. When the command already exited
> non-zero, keep the existing error behavior.

Passos:
1. Em `ExecuteWithProgress`: declarar uma flag `sawResult bool`. No bloco que faz
   `json.Unmarshal(line, &raw)` e checa `raw.Type == "result"`, setar
   `sawResult = true` quando a linha `result` for vista (independente de o
   `resultLine` interno desserializar com sucesso — o que importa é a presença
   do tipo `result`).
2. Após `cmd.Wait()`: manter o comportamento atual para `waitErr != nil`
   (retorna `result` + erro com exit code). Quando `waitErr == nil` mas
   `!sawResult`, retornar erro não-nil com a mensagem
   `"claude cli produced no result line (truncated output?)"`.
3. Repetir a mesma lógica em `ParseFullOutput`: rastrear `sawResult` no loop e,
   ao final, se `exitCode == 0 && !sawResult`, retornar erro. Quando
   `exitCode != 0`, preservar o comportamento existente (não introduzir o novo
   erro de "no result line").
4. Em `claude_test.go`, adicionar/estender testes provando os dois caminhos.

#### Anti-padrão
- NÃO transformar uma saída com exit code != 0 no novo erro "no result line":
  quando o comando já saiu non-zero, o comportamento de erro existente deve ser
  preservado exatamente. O novo erro só vale para `exit 0 && !sawResult`.
- NÃO marcar `sawResult` dentro do `if json.Unmarshal(line, &res) == nil` interno
  (que parseia os campos de custo/tokens). A flag deve subir assim que a linha
  de tipo `result` é detectada, mesmo que o parse dos campos internos falhe.
- NÃO usar o conteúdo de `Output` (string vazia ou não) como proxy para "viu
  result". O sinal é a linha `result`, não a presença de texto.

### Critérios de sucesso
- [ ] `ExecuteWithProgress` retorna erro não-nil quando o processo sai 0 sem linha `result`, com a mensagem `claude cli produced no result line (truncated output?)`.
- [ ] `ParseFullOutput` retorna erro não-nil quando `exitCode == 0` e nenhuma linha `result` foi observada.
- [ ] Saídas com exit code != 0 mantêm exatamente o comportamento de erro atual em ambas as funções.
- [ ] Um stream contendo a linha `result` continua retornando sucesso com custo/tokens preenchidos.
- [ ] Teste novo/estendido em `claude_test.go` cobre: (a) stream com linha `result` → sucesso; (b) stream que termina sem linha `result` mas com exit 0 → erro.
- [ ] `go build ./...` e `go test ./...` passam.

### Arquivos
- **Modificar:** `internal/provider/claude/claude.go`
- **Modificar:** `internal/provider/claude/claude_test.go`

---

## S02 — Atomic tasks.md writes via temp file + rename ✅ PASSED

```yaml
type: backend
```

### O que fazer
Tornar a escrita de `tasks.md` atômica para que um crash no meio da escrita não
corrompa o arquivo e quebre um resume posterior.

Hoje, `internal/task/writer.go` `WriteTasksFile` (linha ~27) chama
`os.WriteFile(path, []byte(b.String()), 0644)` diretamente sobre o destino.

> [verbatim from spec.md §Objective B]
> Write to a temp file in the SAME directory (so rename is atomic on the same
> filesystem), then `os.Rename` it over the destination. Use a temp name
> derived from the destination (e.g. `<path>.tmp-<pid>` or `os.CreateTemp` in
> `filepath.Dir(path)`). Preserve 0644 perms. On any error, clean up the temp.

Passos:
1. Substituir o `os.WriteFile` final por: criar arquivo temporário no MESMO
   diretório do destino (`filepath.Dir(path)`), escrever o conteúdo, garantir
   permissão `0644`, e então `os.Rename(temp, path)`.
2. Em QUALQUER erro (criação, escrita, rename), remover o arquivo temporário
   antes de retornar o erro (ex.: `defer`/`os.Remove` no caminho de falha), para
   não deixar lixo no diretório.
3. Adicionar teste em `writer_test.go` provando que uma escrita bem-sucedida não
   deixa nenhum arquivo temporário no diretório.

#### Anti-padrão
- NÃO criar o arquivo temporário em `os.TempDir()` ou em `/tmp`: o rename precisa
  ocorrer no MESMO filesystem do destino para ser atômico. O temp vai em
  `filepath.Dir(path)`.
- NÃO mudar a permissão final do arquivo: deve continuar `0644` (mesmo octal de
  hoje). Atenção ao umask se usar `os.CreateTemp` (que cria 0600) — ajustar via
  `Chmod(0644)` ou `os.WriteFile` no temp com 0644.
- NÃO deixar o temp para trás em caso de erro de rename/escrita.

### Critérios de sucesso
- [ ] `WriteTasksFile` escreve em arquivo temporário no diretório de destino e faz `os.Rename` sobre o destino.
- [ ] O arquivo final mantém permissões `0644`.
- [ ] Qualquer erro intermediário remove o arquivo temporário e retorna o erro.
- [ ] Os testes existentes de `writer_test.go` continuam passando sem alteração de expectativa.
- [ ] Novo teste verifica que após uma escrita bem-sucedida não há arquivos temporários remanescentes no diretório.
- [ ] `go build ./...` e `go test ./...` passam.

### Arquivos
- **Modificar:** `internal/task/writer.go`
- **Modificar:** `internal/task/writer_test.go`

---

## S03 — Cost ceilings cover failed attempts and retries ✅ PASSED

```yaml
type: backend
```

### O que fazer
Fazer com que os tetos de custo (`MaxCostUSD` cumulativo e `MaxCostPerTaskUSD`
por tarefa) contabilizem o custo de TODA tentativa — incluindo retries e
tentativas que falham — e não apenas a tentativa que dá PASS.

Hoje, em `internal/orchestrator/orchestrator.go` `executeTask`, a acumulação de
custo e os dois checks de teto vivem DENTRO do bloco
`if reviewResult.Verdict == VerdictPass` (linhas ~507-523). Tentativas falhas e
retries gastam orçamento sem nunca serem contados ou limitados.

> [verbatim from spec.md §Objective C]
> Accumulate the cost of EVERY attempt (worker result cost + reviewer result
> cost when available) into a per-task running total and into `*totalCostUSD`,
> regardless of pass/fail. A failed worker attempt may have a nil result —
> guard against nil (cost 0 in that case).

> [verbatim from spec.md §Objective C]
> After each attempt (pass OR fail), check both ceilings against the updated
> totals and return the same actionable error the PASS branch returns today
> when a ceiling is exceeded. Do not double-count the PASS attempt (move the
> accounting so each attempt is counted exactly once).

Mensagens de erro atuais a reusar (verbatim do código em orchestrator.go):
- Per-task: `task %s cost $%.2f exceeded per-task ceiling $%.2f (configure execution.max_cost_per_task_usd to raise)`
- Cumulative: `run aborted: cumulative cost $%.2f exceeded ceiling $%.2f (configure execution.max_cost_usd to raise)`

Passos:
1. Introduzir um total por-tarefa (ex.: `var taskTotalCostUSD float64`) no início
   de `executeTask`, fora do loop de tentativas.
2. Em cada iteração do loop, após obter `result` (worker) e — quando houver —
   `reviewResult` (reviewer), somar o custo de cada componente exatamente UMA vez:
   custo do worker (`result.CostUSD`, tratando `result == nil` como 0) mais custo
   do reviewer quando disponível (`reviewResult.CostUSD`). Acumular em
   `taskTotalCostUSD` e em `*totalCostUSD`.
3. Aplicar os dois checks de teto contra os totais atualizados após cada
   tentativa (pass OU fail), retornando os mesmos erros acima.
4. Garantir que a tentativa que dá PASS NÃO seja contada duas vezes: mover a
   contabilidade que hoje está dentro do branch PASS para o ponto único por
   tentativa. O branch PASS deve continuar retornando `nil` após o bookkeeping
   (status passed, anchor save, checkpoint, emit) quando nenhum teto for
   estourado.
5. Atenção ao caminho em que o worker falha com `result == nil` (ex.: timeout do
   watchdog / `err != nil`): contabilizar custo 0 para o worker, mas ainda
   executar o check de teto (o reviewer pode não ter rodado — sem custo de
   reviewer nesse caso).
6. Adicionar teste em `internal/orchestrator/` provando que custo acumulado de
   tentativas FALHAS dispara o abort de `MaxCostUSD`, usando o `mockProvider`
   já existente (ver `orchestrator_test.go`, com `executeFn` retornando
   `ExecuteResult` com `CostUSD` definido).

#### Anti-padrão
- NÃO contar o custo do worker e do reviewer duas vezes na tentativa que dá PASS.
  O bloco PASS hoje faz `taskCost := result.CostUSD + reviewResult.CostUSD` e
  acumula — essa soma deve passar a acontecer no ponto único por-tentativa, não
  somada novamente dentro do PASS.
- NÃO desreferenciar `result.CostUSD` sem checar `result != nil`: uma tentativa
  de worker falha pode ter `result` nil → custo 0.
- NÃO inventar novas mensagens de erro: reusar exatamente as duas strings
  actionable já existentes (`...max_cost_per_task_usd...` e
  `run aborted: cumulative cost...max_cost_usd...`).
- NÃO mover o emit de `EventTaskComplete`/checkpoint para fora do branch PASS:
  apenas a contabilidade e os checks de teto migram; o comportamento de PASS
  (retornar nil após bookkeeping) permanece.
- NÃO comparar com `>=`: os checks atuais usam `> cap` (estritamente maior).
  Manter `cap > 0 && total > cap`.

### Critérios de sucesso
- [ ] O custo de cada tentativa (worker + reviewer quando disponível) é somado exatamente uma vez a um total por-tarefa e a `*totalCostUSD`, independente de pass/fail.
- [ ] `result == nil` em tentativa falha é tratado como custo 0 do worker sem panic.
- [ ] Após cada tentativa (pass ou fail), ambos os tetos (`MaxCostPerTaskUSD` e `MaxCostUSD`) são checados contra os totais atualizados, retornando as mensagens actionable existentes.
- [ ] A tentativa PASS não é contada em dobro; PASS sem estouro de teto ainda retorna `nil` após o bookkeeping.
- [ ] Novo teste em `internal/orchestrator/` prova que custo cumulativo de tentativas FALHAS dispara o abort de `MaxCostUSD`, dirigido pelo `mockProvider` existente.
- [ ] Testes existentes de orchestrator continuam passando.
- [ ] `go build ./...` e `go test ./...` passam.

### Arquivos
- **Modificar:** `internal/orchestrator/orchestrator.go`
- **Modificar:** `internal/orchestrator/orchestrator_test.go`

---

## S04 — Validate full build and test suite ⬜ PENDING

```yaml
type: review
depends_on: [S01, S02, S03]
```

### O que fazer
Validação final integrando as três mudanças de hardening (S01, S02, S03).
Confirmar que o repositório compila e que toda a suíte de testes passa, sem
regressões nos pacotes tocados nem nos demais.

> [verbatim from spec.md §Validation]
> - `go build ./...` passes.
> - `go test ./...` passes.
> - No regression in existing orchestrator/provider/task tests.

Passos:
1. Rodar `go build ./...` e confirmar sucesso.
2. Rodar `go test ./...` e confirmar que todos os pacotes passam.
3. Conferir especificamente os pacotes `internal/provider/claude`,
   `internal/task` e `internal/orchestrator` por regressões.
4. Se algo falhar, registrar a saída exata e abrir correção no objetivo
   correspondente (S01/S02/S03) em vez de mascarar aqui.

### Critérios de sucesso
- [ ] `go build ./...` passa.
- [ ] `go test ./...` passa.
- [ ] Sem regressões nos testes de orchestrator, provider e task.

### Arquivos
- **Modificar:** `internal/provider/claude/claude.go`
- **Modificar:** `internal/task/writer.go`
- **Modificar:** `internal/orchestrator/orchestrator.go`
