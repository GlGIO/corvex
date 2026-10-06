# Importa — o que Archon, Bernstein e Symphony têm que o corvex não tem

> FONTE DE ESTADO desta obra. Base: `harness/ui-dispatch` @ `3d77a30`, branch
> `importa/ferramentas`. As três pesquisas em `pesquisa/` foram feitas contra o
> `main` (`288d666`), **48 commits atrás** desta base — toda ausência que elas
> apontam foi reconferida aqui antes de entrar no plano, e duas já não eram
> ausência (ver "Descartado por já existir").

## Regra de parada (declarada ANTES do laço)

- Lote 1 = os itens abaixo, em ordem. Cada um fecha com teste + controle positivo
  (mutar a implementação, contar os vermelhos, desfazer) e `go test ./...` sem
  vermelho NOVO em relação à linha de base.
- Um item que falha o próprio critério duas vezes seguidas vira **BLOCKED** aqui,
  com o porquê, e o lote segue para o próximo.
- Fim do lote 1 → para e reavalia com o dono. Não há "até acabar os créditos".

## Linha de base (antes de tocar em nada)

| comando | vermelho que JÁ existia |
|---|---|
| `go test ./e2e/` | `TestUI_PasteNeverOverwritesWhatWasTyped` (e, numa das duas execuções, `TestGate_ParksAndIsReleasedByASecondProcess`) |
| `go test ./cmd/` | `TestCharacterizeStackEnvFileSourced` |
| `go test ./internal/...` | verde |
| `go test ./e2e/ -run TestUI_RetryReExecutesOnlyTheFailedStep` | intermitente sob carga (2/8 na branch, 0/5 na base isolada, 6/6 na branch isolada): `ui_step_test.go:138` clica em `Open` assim que "failed" aparece, sem esperar o botão. Corrida do teste, não do produto; o arquivo tem edição em voo no checkout principal, então NÃO foi tocado |

## O veredito por ferramenta, em uma linha

- **Archon** (`coleam00/Archon`) — a mais rica. O que vale é o motor de recipe:
  saída estruturada, `when:`/`trigger_rule`, saídas entre stages, retry por classe
  de falha, `mutates_checkout: false` verificado, fixtures de recipe com stubs. O
  vocabulário `kind: tool/test` da tabela original é do corvex, não do Archon.
- **Bernstein** (`sipyourdrink-ltd/bernstein`) — o "replay byte a byte" não
  reproduz o trabalho dos agentes, e a trilha assinada não serve a um operador
  único. O valor real veio de rebate: a leitura dele achou dois defeitos no
  corvex (gate-after depois do commit; árvore dividida no paralelo).
- **Symphony** (`openai/symphony`) — nada para *executar* melhor; o valor é a
  camada de cima, "ler o board" (adaptador de 2 operações + claim + reconciliação).
  Ordem certa: medir o primeiro run real contra o board ANTES de automatizar o
  despacho.

## Lote 1 — FECHADO (aguardando o dono para o lote 2)

| # | Item | Origem | Evidência nesta base | Estado |
|---|---|---|---|---|
| 1 | Gate `after` roda ANTES do PASSED e do checkpoint | Bernstein (achado) | `step/ai_task.go:44-53` chama `runAITask`, que dentro de `attempt` já fez `commitPassedTask` (`step/passed.go:101-149`: status PASSED, anchor, `MarkCheckpoint`, `Completed=true`); só depois `runGates(GateAfter)`. O comentário em `ai_task.go:41-43` afirma o contrário. **Pior que o relatado:** o prompt do revisor manda "check git diff", e depois do checkpoint o diff é vazio — todo gate inferencial after julgava nada. | **feito** `beb0d0f`. Controle: cegar → 1 vermelho (só o novo: nenhum teste antigo cobria gate after em passo de código) |
| 2 | Reviewer não pode alterar a árvore | Archon `mutates_checkout: false` | `step/reviewer.go:56` dá `Bash` ao reviewer e nada confere a árvore depois | **feito** `2a00b5c`. Falso positivo MEDIDO e fechado: o runner escreve `.corvex/` (versionado) durante a revisão — 5 caracterizações de `cmd/` ficaram vermelhas até excluí-lo. Controles: 1/1/1 |
| 3 | Falha de infraestrutura não gasta o orçamento de retry semântico, e espera antes de repetir | Archon + Bernstein | `step/ai_task.go` — worker/reviewer que erra cai no mesmo `attempt++`, sem backoff | **feito** `ffb7175`. 10s×2ⁿ até 2 min, máx. 3 por task, desconhecido segue o caminho antigo. Controles: 2/2 |
| 4 | Gate `after` de comando que recusa devolve o diagnóstico ao worker, uma vez | Bernstein (gate→tarefa de reparo) | hoje `markGateFailure` mata a tarefa | **feito** `2c8cd7e`. Só gate computacional, 1×, só com tentativa sobrando; evidência da tentativa descartada é retirada. Controles: 2/1 |

### Revisão independente do lote 1 — `688cf3c`

Um revisor sem o contexto da implementação leu o diff e achou 8 defeitos reais,
três deles de desenho: o repair consertava uma árvore que o retry já tinha
apagado; repair com gate humano after reabriria um gate decidido (fatal); `529`
solto casava com `152900 tokens`. Os 8 foram corrigidos. Seis têm teste +
controle positivo.

**Dívida (sem teste):** os hooks on-failure/post-task na recusa do gate after, e
o "fingerprint depois ilegível não descarta". Ambos são uma linha de código
cada, mas não há controle que fique vermelho se forem removidos.

**Lição medida:** dois dos meus controles estavam quebrados — a mutação não
compilava, e o script só procurava `--- FAIL`, então "nenhum vermelho" queria
dizer "não rodou". Controle precisa provar que a mutação compilou.

### Linha de chegada medida

| comando | resultado |
|---|---|
| `go test ./internal/... ./cmd/ -count=1` | verde |
| `go test ./e2e/ -count=1`, 2× cada | base `3d77a30`: FAIL 2/2 · esta branch: ok 2/2 — a intermitência é anterior ao lote |
| invariante `internal/` ≤ 400 linhas | `ai_task.go` passou de 400 com as correções; os emissores de custo foram para `attempt_cost.go` (374) |

## Lote 2 — candidatos, NÃO iniciados (reavaliar com o dono)

Em ordem de valor/custo. Detalhes, referências e riscos em `pesquisa/`.

1. Saída estruturada do reviewer via `--json-schema` (Archon #4) — mata o
   INDETERMINATE que vira retry. M.
2. `inputs:` declarados na recipe, validados antes de gastar (Archon #5). P.
3. Congelar a config de IA na run e reusar no resume (Archon #9). P.
4. Board como fonte da caixa, adaptador `kind: command` reaproveitando
   `az-feature-dag.sh` (Symphony 1-5). P — **bloqueado pelo GET real** que o
   ESTADO do harness pede ao dono.
5. Saídas entre stages + `when:`/`trigger_rule` (Archon #6-7). M cada; depende do 1.
   Antes: `runShell` repassa `os.Environ()` inteiro (`step/step.go`) — achado do
   Archon, a resolver antes de passar valor entre stages por env.
6. Fixtures de recipe com stubs (Archon #11). M.
7. Classificar vermelho em introduzido/herdado via merge-base (Archon #12). M.

## Descartado por já existir nesta base

- MCP estrito por papel (Archon #2): feito em `971b8d2` — toda chamada leva
  `--mcp-config` explícito + `--strict-mcp-config`.
- Árvore dividida no paralelo (Bernstein #2): `orchestrator/schedule.go` serializa
  os steps que escrevem a árvore num `treeMu` desde `31ec91a`.

## Descartado de propósito

Recibo Ed25519/HMAC, `--re-derive`, revisão de fila por LLM (Bernstein); processo
como prosa, aprovação como estado do tracker, retry infinito, `rm -rf` de
workspace (Symphony); `evidence_policy` de existência de arquivo, `mcp:` por nó,
`persist_session`, banco/servidor/chat adapters (Archon). Os porquês estão em
cada `pesquisa/*.md`, seção 3.
