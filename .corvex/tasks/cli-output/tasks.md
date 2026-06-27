---
dag:
    S01: []
    S02:
        - S01
    S03:
        - S01
        - S02
    S04: []
    S05: []
    S06:
        - S05
---

## S01 — Renderer plain limpo para eventos do orchestrator ✅ PASSED

```yaml
type: backend
```

### O que fazer
Criar um renderer plain limpo para eventos do orchestrator, usado pelo caminho
`--plain` (e pelo auto-fallback não-interativo). Hoje `drainEvents` em
`cmd/run.go` roteia os eventos pelo logger default do charmbracelet/log,
produzindo linhas ruidosas com timestamp e `key=val`. O novo renderer deve
emitir um formato limpo e escaneável, sem timestamps e sem ruído `key=val`.

> [verbatim from spec.md §Objective A]
> - Add a clean plain renderer for orchestrator events used by the `--plain` path
>   (or non-interactive auto-fallback). Format suggestions (no timestamps, no
>   key=val noise):
>     `▶ S02  Implement endpoints` (task start)
>     `✓ S02  passed · 1m12s · $0.41` (task complete passed)
>     `✗ S02  failed · <message>` (failed)
>     `↻ S02  retry 1` (retry)
>     `⏭ S04  skipped (depends on failed S02)`
>     `■ planning…`, `✓ plan ready (N tasks)`, `✓ done` etc.
>   Use the existing tui styles where helpful, but it must degrade to plain ASCII
>   when color is disabled (NO_COLOR / not a TTY).

- Implementar o renderer como uma função/tipo que consome o canal de
  `orchestrator.Event` (substituindo o uso de `log.Info/Warn` em `drainEvents`
  de `cmd/run.go`), mapeando cada `EventType` para uma linha limpa:
  - `EventTaskStart` → `▶ <ID>  <título>` (sem timestamp, sem `attempt=`).
  - `EventTaskComplete` (passed) → `✓ <ID>  passed · <duração> · <custo>`
    usando `tui.FormatDuration` e `tui.FormatCost`.
  - `EventTaskComplete` (failed) / `EventError` → `✗ <ID>  failed · <message>`.
  - `EventRetry` → `↻ <ID>  retry <attempt>`.
  - skip de tarefa → `⏭ <ID>  skipped (...)` (quando houver evento de skip).
  - `EventPlanStart` → `■ planning…`; `EventPlanComplete`/`EventDAGResolved`
    → `✓ plan ready (N tasks)` usando `ev.Total`; `EventDone` → `✓ done`.
- Onde fizer sentido, reusar estilos/glyphs de `internal/tui/styles.go`
  (`GlyphPassed`, `GlyphFailed`, `tui.FormatDuration`, `tui.FormatCost`, etc.),
  **mas** deve degradar para ASCII plano quando a cor está desabilitada
  (`NO_COLOR` setado ou saída não é TTY). A flag `--no-color` e a env `NO_COLOR`
  já chamam `lipgloss.SetColorProfile(termenv.Ascii)` no `PersistentPreRun` de
  `rootCmd` — apoiar-se nisso para a degradação de cor.
- Manter `--plain` retrocompatível o suficiente para que scripts ainda vejam os
  desfechos de tarefa (start/passed/failed); apenas mais limpo.
- O caminho TUI (default em TTY, via `runWithTUI`) permanece **inalterado**.

#### Anti-padrão
NÃO emitir timestamps nem pares `key=val` (ex.: `task=S02 cost=$0.41`) — o
formato é limpo, sem ruído. NÃO mudar o caminho da TUI: a condição
`if !runPlain && isInteractive()` que chama `runWithTUI` continua igual; só o
ramo plain (`drainEvents`) muda. NÃO assumir que os glyphs Unicode sempre
aparecem coloridos — eles devem renderizar como ASCII/plano quando a cor está
desativada. NÃO remover a emissão dos desfechos passed/failed (scripts dependem
deles).

---

## S02 — Flag persistente `-q/--quiet` em rootCmd ⬜ PENDING

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
Adicionar uma flag persistente `-q/--quiet` em `rootCmd` e fazer o renderer
plain (S01) respeitá-la: em modo quiet, suprimir o progresso por-tarefa e
imprimir apenas falhas/erros e o resumo final.

> [verbatim from spec.md §Objective A]
> - Add a persistent `-q/--quiet` flag on rootCmd: when set, the plain renderer
>   prints only failures/errors and the final summary (suppress per-task progress).

- Registrar a flag persistente em `rootCmd` com shorthand `q` e nome longo
  `quiet` (ex.: `rootCmd.PersistentFlags().BoolP("quiet", "q", false, ...)`),
  análoga à flag persistente `--no-color` já existente em `cmd/root.go`.
- Propagar o estado de quiet ao renderer plain de S01 (ex.: parâmetro/campo
  `quiet bool` lido da flag em `runRun`/`drainEvents`).
- Quando quiet está **setado**: o renderer imprime **apenas** falhas/erros
  (`✗ ... failed`, `EventError`) e o resumo final (`✓ done` / resumo final);
  todo o progresso por-tarefa (start, passed por-tarefa, retry, plan…) é
  suprimido.
- Quando quiet **não** está setado: comportamento completo de S01 (todas as
  linhas de progresso).

#### Anti-padrão
NÃO suprimir falhas/erros nem o resumo final em modo quiet — quiet suprime
**apenas** o progresso por-tarefa; falhas/erros e o resumo final SEMPRE
aparecem. NÃO usar outro shorthand que não `-q` nem outro nome que não
`--quiet`. NÃO tornar a flag local a um subcomando — ela é **persistente** em
`rootCmd` (aplica a todos). NÃO afetar o caminho da TUI.

---

## S03 — Teste do renderer plain (incl. modo quiet) ⬜ PENDING

```yaml
type: backend
depends_on: [S01, S02]
```

### O que fazer
Adicionar um teste para o renderer plain: dado uma sequência de eventos do
orchestrator, asserir as linhas renderizadas; e asserir que o modo quiet
suprime o progresso não-erro.

> [verbatim from spec.md §Objective A]
> - Add a test for the renderer: given a sequence of events, assert the rendered
>   lines (and that quiet mode suppresses non-error progress).

- Construir uma sequência de `orchestrator.Event` cobrindo start, passed,
  failed, retry, skip, plan e done, capturar a saída do renderer (ex.: escrever
  para um `bytes.Buffer`/`io.Writer` injetado em vez de stdout) e asserir as
  linhas esperadas.
- Rodar o renderer com cor desabilitada (ASCII) para que as asserções sejam
  determinísticas (sem códigos ANSI).
- Adicionar um caso em modo **quiet** asserindo que: o progresso não-erro
  (start/passed/retry/plan) **não** aparece, mas falhas/erros e o resumo final
  **aparecem**.

#### Anti-padrão
NÃO asserir contra saída colorida/ANSI — o teste deve rodar em modo ASCII para
ser estável. NÃO afrouxar a asserção de quiet a ponto de não verificar que
falhas/erros AINDA aparecem em quiet.

---

## S04 — Habilitar `completion` + nota no README ⬜ PENDING

```yaml
type: general
```

### O que fazer
Garantir que `corvex completion [bash|zsh|fish|powershell]` funciona (o cobra
fornece por default a menos que desabilitado) e adicionar uma nota curta de uso
no README sob um heading "Shell completion".

> [verbatim from spec.md §Objective B]
> - Ensure `corvex completion [bash|zsh|fish|powershell]` works (cobra provides it
>   by default unless disabled; verify it is enabled and add a short usage note in
>   README under a "Shell completion" heading).

- Verificar que o comando default `completion` do cobra está **habilitado**
  (i.e. não há `rootCmd.CompletionOptions.DisableDefaultCmd = true` em
  `cmd/root.go`); se estiver desabilitado, habilitar.
- Adicionar uma seção curta no README com o heading **"Shell completion"**
  mostrando o uso para os 4 shells suportados (bash, zsh, fish, powershell).

#### Anti-padrão
NÃO reimplementar o comando `completion` do zero — ele vem do cobra; apenas
garantir que está habilitado. O heading no README deve ser exatamente
"Shell completion".

---

## S05 — Completion dinâmica do argumento <project> ⬜ PENDING

```yaml
type: backend
```

### O que fazer
Adicionar completion **dinâmica** para o argumento `<project>` dos comandos que
o recebem (`run`, `status`, `logs`, `reset`): registrar um `ValidArgsFunction`
que retorna os nomes de projeto sob `.corvex/tasks/`, reusando o helper
`projectNames` (já existente em `cmd/helpers.go`, do batch cli-basics).

> [verbatim from spec.md §Objective B]
> - Add DYNAMIC completion for the <project> argument of the commands that take one
>   (run, status, logs, reset): register a ValidArgsFunction that returns the
>   project names under .corvex/tasks/ (reuse the projectNames helper from the
>   cli-basics batch; if that helper does not exist yet, add it). This makes
>   `corvex run <TAB>` complete real project names.

- Definir um `ValidArgsFunction` (ex.: em `cmd/helpers.go`) que resolve o
  `workDir` (via `loadConfig`/`os.Getwd`) e retorna `projectNames(workDir)`
  com a diretiva apropriada do cobra (ex.: `cobra.ShellCompDirectiveNoFileComp`).
- Registrar essa função em `runCmd`, `statusCmd`, `logsCmd` e `resetCmd`
  (campo `ValidArgsFunction`).
- O argumento `<project>` é o **primeiro** argumento posicional em todos os
  quatro comandos; só completar o `<project>` (não os argumentos seguintes,
  como `[task]` de `logs` ou `<task>` de `reset`).
- `projectNames` **já existe** em `cmd/helpers.go` — reusar, não recriar.

#### Anti-padrão
NÃO duplicar a lógica de listagem de projetos — reusar `projectNames` de
`cmd/helpers.go`. NÃO completar o argumento de `<project>` na posição errada:
em `logs <project> [task]` e `reset <project> <task>` o `<project>` é o
primeiro arg; quando `len(args) > 0` não oferecer mais nomes de projeto. NÃO
retornar completion de arquivos no lugar dos nomes de projeto.

---

## S06 — Teste da ValidArgsFunction de projeto ⬜ PENDING

```yaml
type: backend
depends_on: [S05]
```

### O que fazer
Adicionar um teste que verifica que a `ValidArgsFunction` retorna os nomes de
projeto esperados para um layout temporário de `.corvex/tasks/`.

> [verbatim from spec.md §Objective B]
> - Add a test that the ValidArgsFunction returns the expected project names for a
>   temp .corvex/tasks/ layout.

- Montar um layout temporário `.corvex/tasks/<proj>/` com `spec.md`/`tasks.md`
  (similar ao teste existente de `projectNames` em `cmd/helpers_test.go`).
- Chamar a `ValidArgsFunction` e asserir que os nomes retornados batem com os
  projetos criados no diretório temporário, e que a diretiva de completion é a
  esperada (ex.: `NoFileComp`).

#### Anti-padrão
NÃO depender do `.corvex/` real do repositório — usar um diretório temporário
(`t.TempDir()`) como nos testes existentes.
