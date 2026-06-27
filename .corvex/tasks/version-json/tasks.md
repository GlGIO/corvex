---
dag:
    S01: []
    S02:
        - S01
---

## S01 — Add `--json` flag to the version command ✅ PASSED

```yaml
type: backend
```

### O que fazer
Adicionar um flag booleano `--json` ao subcomando `version` em `cmd/version.go`.

Comportamento exato exigido pela spec:

> [verbatim from spec.md §Requirements]
> - Add a `--json` boolean flag to the `version` command (cmd/version.go).
> - When `--json` is set, print a single JSON object `{"version":"<v>"}` where `<v>`
>   is `github.com/giovannialves/corvex/internal/types`.Version, using encoding/json.
>   Print nothing else.
> - When `--json` is absent, the existing human output is unchanged.

Passos:
1. Registrar o flag booleano `--json` no comando `version` (cobra `Flags().Bool("json", false, ...)` ou equivalente ao padrão já usado no repo).
2. Quando `--json` for verdadeiro, serializar com `encoding/json` o objeto cujo único campo é `version`, com valor igual a `types.Version` (de `github.com/giovannialves/corvex/internal/types`). Usar uma struct com tag `json:"version"` para garantir a chave exata `"version"`. Imprimir somente esse JSON (sem texto adicional, sem prefixo/sufixo humano).
3. Quando `--json` for falso/ausente, manter a saída humana atual byte-a-byte.

---

## S02 — Teste em cmd/ cobrindo JSON e saída padrão ⬜ PENDING

```yaml
type: backend
depends_on: [S01]
```

### O que fazer
Adicionar teste(s) em `cmd/` que cubram os dois caminhos do comando `version`.

> [verbatim from spec.md §Validation]
> - A test in cmd/ covers both the JSON output (contains the version) and that the
>   default output is unchanged.

Passos:
1. Teste do caminho `--json`: executar o comando com o flag e verificar que a saída contém o valor de `types.Version`. Idealmente, fazer `json.Unmarshal` da saída em uma struct `{Version string}` e assertar que `Version == types.Version`, garantindo também que é JSON válido com a chave `version`.
2. Teste do caminho padrão (sem `--json`): verificar que a saída humana permanece inalterada (assertar o conteúdo esperado atual).
3. Seguir o padrão de teste já existente no pacote `cmd/` (captura de stdout / `cmd.SetOut`, conforme convenção do repo).
