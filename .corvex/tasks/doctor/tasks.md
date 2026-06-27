---
generated_by: corvex-planner
dag:
    S01: []
    S02:
        - S01
    S03:
        - S02
---

## S01 — Scaffold `corvex doctor` command + check framework ✅ PASSED

```yaml
type: backend
```

### O que fazer
Criar o arquivo `cmd/doctor.go` com um novo comando cobra registrado no mesmo
padrão dos comandos existentes (ver `cmd/validate.go`: `var <name>Cmd = &cobra.Command{...}`
e `func init() { rootCmd.AddCommand(<name>Cmd) }`).

O comando deve:
- Ser invocável como `corvex doctor` (sem argumentos: `Args: cobra.NoArgs`).
- Carregar a config via o helper existente `loadConfig()` de `cmd/helpers.go`
  (retorna `(*config.Config, workDir string, error)`). Não reimplementar a leitura de YAML.
- Definir a infraestrutura de "checks": um tipo de resultado de check com um
  status entre os três níveis (pass / fail / warning) e uma mensagem, uma forma
  de acumular esses resultados, e a impressão de cada um como uma linha prefixada.

Prefixos exatos das linhas (um caractere Unicode por nível):

> [verbatim from spec.md §Requirements]
> printing each as a line prefixed with `✓` (pass), `✗` (fail), or `⚠` (warning).

Linha de resumo final, formato exato:

> [verbatim from spec.md §Requirements]
> Print a final summary line, e.g. `doctor: N checks, X passed, Y warnings, Z failed`.

Onde `N` = total de checks, `X` = passados (✓), `Y` = warnings (⚠), `Z` = falhas (✗).
Note que `N = X + Y + Z`.

Semântica do exit code (retorno de `RunE`):

> [verbatim from spec.md §Requirements]
> Exit non-zero (return an error from RunE) if ANY check fails (✗). Warnings (⚠) do not cause a non-zero exit.

Os checks em si (provider, models, sandbox, escalation, cost) ficam para a S02 —
nesta task entregue o esqueleto do comando, o tipo de resultado, o acumulador, a
impressão por linha, o resumo e a lógica de retorno de erro. Pode deixar a lista
de checks vazia ou com um stub que será preenchido na S02.

### Critérios de sucesso
- [ ] `cmd/doctor.go` existe com `doctorCmd` registrado via `rootCmd.AddCommand` em `init()`.
- [ ] `corvex doctor` é invocável e usa `loadConfig()`.
- [ ] Existe um tipo/estrutura de resultado de check com os três níveis e mensagem.
- [ ] A linha de resumo segue exatamente `doctor: N checks, X passed, Y warnings, Z failed`.
- [ ] `RunE` retorna um `error` (não-nil) sse houver qualquer ✗; warnings não causam erro.
- [ ] `go build ./...` passa.

---

## S02 — Implementar os cinco checks do doctor ✅ PASSED

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
Preencher a lista de checks executados por `corvex doctor` (sobre o framework da
S01). Cada check é independente e produz uma ou mais linhas de resultado. São cinco
grupos de checks:

**1. provider**

> [verbatim from spec.md §Checks to perform]
> 1. **provider**: `provider.default` is a known provider (currently only `claude-cli`). The provider binary is resolvable on PATH (the claude binary, honoring the `CORVEX_CLAUDE_BIN` env var the same way `internal/provider/claude` does).

- Provider conhecido: atualmente **apenas** `claude-cli`. Pode reusar
  `provider.NewProvider(cfg.Provider.Default, cfg)` (retorna
  `provider.ErrUnknownProvider` para desconhecidos) ou comparar diretamente — mas
  o registry (`internal/provider/registry.go`) normaliza com
  `strings.TrimSpace(strings.ToLower(...))` e aceita `"claude"` e `"claude-cli"`.
- Resolução do binário: replicar exatamente a lógica de `internal/provider/claude`
  (`claude.New`): ler `os.Getenv("CORVEX_CLAUDE_BIN")`; se vazio, usar `"claude"`.
  Depois `exec.LookPath(bin)` para verificar se está no PATH. Falha (✗) se não resolver.

**2. models**

> [verbatim from spec.md §Checks to perform]
> 2. **models**: `provider.models.planner`, `.worker`, and `.reviewer` are all non-empty.

Falha (✗) se qualquer um de `cfg.Provider.Models.Planner`, `.Worker`, `.Reviewer`
for string vazia.

**3. sandbox**

> [verbatim from spec.md §Checks to perform]
> 3. **sandbox**: `sandbox.type` is one of `local`/`docker`, or `sandbox.profile` is one of `nix`/`devcontainer`. If `docker`, warn (not fail) when the `docker` binary is not on PATH. If `sandbox.mount` is set, it must be parseable.

- `cfg.Sandbox.Type` ∈ {`local`, `docker`} **OU** `cfg.Sandbox.Profile` ∈ {`nix`, `devcontainer`}.
- Se `type == docker` e `docker` não está no PATH (`exec.LookPath("docker")`):
  **warning (⚠), não falha**.
- Se `cfg.Sandbox.Mount` != "": deve ser parseável. Reusar a mesma lógica de
  `resolveMountPath` em `internal/sandbox/docker.go` (faz `strings.SplitN(mount, ":", 2)`
  e `filepath.Abs` no host path); falha (✗) se o parse retornar erro.

**4. escalation**

> [verbatim from spec.md §Checks to perform]
> 4. **escalation**: every policy under `review.escalation` has a known `action` (`upgrade-model`, `spawn-investigation`, `human-prompt`) and `after >= 1`; `upgrade-model` policies must set `to`.

Para cada policy em `cfg.Review.Escalation` (map de `string` → `config.EscalationPolicy`):
- `Action` deve ser um dos três literais: `upgrade-model`, `spawn-investigation`,
  `human-prompt`. Qualquer outro valor → falha (✗).
- `After` deve ser `>= 1`. Falha (✗) caso contrário.
- Se `Action == "upgrade-model"`, o campo `To` deve ser não-vazio. Falha (✗) caso contrário.

**5. cost ceilings**

> [verbatim from spec.md §Checks to perform]
> 5. **cost ceilings**: warn if `execution.max_cost_usd` is 0 (no cap) and warn if `max_cost_per_task_usd` is 0.

- `cfg.Execution.MaxCostUSD == 0` → **warning (⚠)** ("no cap").
- `cfg.Execution.MaxCostPerTaskUSD == 0` → **warning (⚠)**.

### Critérios de sucesso
- [ ] Os cinco grupos de checks são executados e impressos com prefixo ✓/✗/⚠.
- [ ] provider desconhecido → ✗; binário não resolvível no PATH (honrando `CORVEX_CLAUDE_BIN`) → ✗.
- [ ] qualquer model vazio → ✗.
- [ ] sandbox type/profile inválido → ✗; docker ausente do PATH → ⚠; mount não-parseável → ✗.
- [ ] action desconhecida ou `after < 1` → ✗; `upgrade-model` sem `to` → ✗.
- [ ] `max_cost_usd == 0` → ⚠; `max_cost_per_task_usd == 0` → ⚠.
- [ ] Config default (`config.Default()`) passa todos os checks de falha (só pode gerar warnings).
- [ ] `go build ./...` passa.

---

## S03 — Testes table-driven para `corvex doctor` ⬜ PENDING

```yaml
type: review
depends_on: [S02]
```

### O que fazer
Criar `cmd/doctor_test.go` com testes table-driven no estilo dos `cmd/*_test.go`
existentes (ver `cmd/status_test.go`: helper de setup que cria `.corvex/` num
`t.TempDir()`, faz `os.Chdir` e devolve cleanup; helper `captureStdout` para
capturar a saída).

Casos obrigatórios exigidos pela spec:

> [verbatim from spec.md §Validation]
> A new test file `cmd/doctor_test.go` covers: a valid config passes; an unknown provider fails; an `upgrade-model` policy missing `to` fails; a missing model fails. Use table-driven tests in the style of the existing `cmd/*_test.go`.

Ou seja, no mínimo quatro cenários:
1. config válida → `RunE` retorna `nil` (sem erro).
2. provider desconhecido → `RunE` retorna erro (não-nil).
3. policy `upgrade-model` sem `to` → `RunE` retorna erro.
4. model faltando (vazio) → `RunE` retorna erro.

Cada caso monta uma `.corvex/config.yaml` apropriada num tempdir, executa
`runDoctor` (ou o `RunE` do comando) e verifica o retorno de erro / a saída.

Para o caso "valid config passes" garanta que o binário do provider resolva no
PATH durante o teste — defina `CORVEX_CLAUDE_BIN` (via `t.Setenv`) apontando para
um binário garantidamente presente (ex.: `os.Args[0]` ou um stub criado no tempdir
com bit de execução) para que o check de provider não falhe espuriamente no CI.

### Critérios de sucesso
- [ ] `cmd/doctor_test.go` existe com testes table-driven.
- [ ] Cobre: config válida passa; provider desconhecido falha; `upgrade-model` sem `to` falha; model faltando falha.
- [ ] `go test ./...` passa.
- [ ] `go build ./...` passa.
