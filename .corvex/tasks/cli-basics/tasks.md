---
dag:
    S01: []
    S02: []
    S03: []
    S04:
        - S03
---

## S01 — `version` subcommand ✅ PASSED

```yaml
type: backend
```

### O que fazer
Adicionar um subcomando `version` (sem dashes) em `cmd/version.go`. Hoje
`corvex --version` funciona via `rootCmd.Version`, mas `corvex version` retorna
"unknown command".

> [verbatim from spec.md §Objective A]
> Add a `version` subcommand in `cmd/version.go` that prints the same string the
> version template produces: `corvex <version>` where version is
> `github.com/giovannialves/corvex/internal/types`.Version.

- O comando deve imprimir exatamente `corvex <version>`, lendo a versão de
  `internal/types`.Version (mesma fonte usada pelo template do `--version`).
- `Args: cobra.NoArgs`.
- Registrar via `rootCmd.AddCommand(...)` dentro de um `init()` em
  `cmd/version.go`.

#### Anti-padrão
NÃO inventar uma nova string de versão nem hardcodar um número. A versão DEVE
vir de `internal/types`.Version. NÃO usar `Args` diferente de `cobra.NoArgs`
(ex.: `cobra.ArbitraryArgs`) — o spec pede `cobra.NoArgs`.

---

## S02 — Respeitar NO_COLOR + flag global `--no-color` ✅ PASSED

```yaml
type: backend
```

### O que fazer
Tornar a supressão de cor um comportamento padrão de primeira classe. Corvex
colore saída via lipgloss (TUI) e charmbracelet/log (`--plain`).

> [verbatim from spec.md §Objective B]
> Add a PERSISTENT flag `--no-color` on rootCmd (so it applies to every command).
> In rootCmd's PersistentPreRun (add one if absent), disable color when EITHER
> the `--no-color` flag is set OR the `NO_COLOR` environment variable is non-empty
> (the de-facto standard, https://no-color.org). Disable by setting lipgloss's
> color profile to ascii (e.g. `lipgloss.SetColorProfile(termenv.Ascii)`) and
> turning off the charmbracelet/log default logger's color/styles if applicable.

- Adicionar flag PERSISTENTE `--no-color` em `rootCmd`.
- Adicionar/estender `PersistentPreRun` (ou `PersistentPreRunE`) em `rootCmd`.
- Desabilitar cor quando **OU** a flag `--no-color` está setada **OU** a env
  `NO_COLOR` é não-vazia.
- Desabilitar com `lipgloss.SetColorProfile(termenv.Ascii)` e desligar
  cor/estilos do logger default do charmbracelet/log quando aplicável.
- Não quebrar a TUI quando a cor ESTÁ habilitada.

#### Anti-padrão
NÃO exigir que AMBOS (flag E env) estejam setados — a condição é **OR**: qualquer
um dos dois desabilita a cor. NÃO tratar `NO_COLOR` vazio como "desabilitar" — só
desabilita se a env for **não-vazia** (presença com conteúdo). NÃO remover o
`PersistentPreRun` existente de subcomandos se houver; estender, não substituir.

---

## S03 — Helpers compartilhados `projectNames` e `suggestProject` ✅ PASSED

```yaml
type: backend
```

### O que fazer
Criar helpers compartilhados em `cmd/helpers.go` para listar projetos e sugerir
o nome mais próximo, refatorando `list.go` para reusar.

> [verbatim from spec.md §Objective C]
> Add a shared helper in `cmd/helpers.go`, e.g.
> `projectNames(workDir string) []string` that returns the directory names under
> `.corvex/tasks/` that contain a spec.md or tasks.md (reuse the listing logic
> that cmd/list.go already has — refactor list.go to use the shared helper too,
> no behavior change).

> [verbatim from spec.md §Objective C]
> Add a helper `suggestProject(workDir, name string) string` that returns the
> closest existing project name (simple case-insensitive prefix/substring match,
> or Levenshtein distance <= 2) or "" if none is close.

- `projectNames(workDir string) []string`: retorna nomes de diretórios sob
  `.corvex/tasks/` que contêm `spec.md` **ou** `tasks.md`.
- Refatorar `cmd/list.go` para usar `projectNames` — **sem mudança de
  comportamento**.
- `suggestProject(workDir, name string) string`: retorna o nome de projeto
  existente mais próximo via match case-insensitive de prefixo/substring **ou**
  distância de Levenshtein **<= 2**; retorna `""` se nenhum for próximo.

#### Anti-padrão
NÃO usar threshold de Levenshtein diferente de `<= 2` (ex.: `< 2` ou `<= 3`). O
match de prefixo/substring é **case-insensitive** — não fazer comparação
sensível a maiúsculas. `projectNames` inclui diretório com `spec.md` **OU**
`tasks.md` (qualquer um), não exige ambos. NÃO alterar a saída/ordenação atual do
`list` ao refatorar.

---

## S04 — "did you mean" para projeto inexistente em `run` ✅ PASSED

```yaml
type: backend
depends_on: [S03]
```

### O que fazer
Tornar o erro de projeto inexistente útil em `corvex run`, listando projetos
disponíveis e sugerindo o mais próximo.

> [verbatim from spec.md §Objective C]
> In `corvex run` (cmd/run.go), when the project's spec.md/tasks.md does not
> exist, return an error that lists available projects and, when a close match
> exists, adds "did you mean <X>?".

- Em `cmd/run.go`, quando `spec.md`/`tasks.md` do projeto não existe, retornar
  erro que **lista os projetos disponíveis** (via `projectNames`) e, **quando há
  match próximo** (via `suggestProject`), adiciona `did you mean <X>?`.
- Reusar os helpers de S03.

#### Anti-padrão
NÃO mostrar `did you mean` quando `suggestProject` retorna `""` — só anexar a
sugestão quando há match próximo. NÃO trocar o comportamento de sucesso do `run`;
a mudança é apenas no caminho de erro (projeto inexistente).
