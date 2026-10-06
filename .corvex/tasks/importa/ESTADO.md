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

## Lote 1 — em execução

| # | Item | Origem | Evidência nesta base | Estado |
|---|---|---|---|---|
| 1 | Gate `after` roda ANTES do PASSED e do checkpoint | Bernstein (achado) | `step/ai_task.go:44-53` chama `runAITask`, que dentro de `attempt` já fez `commitPassedTask` (`step/passed.go:101-149`: status PASSED, anchor, `MarkCheckpoint`, `Completed=true`); só depois `runGates(GateAfter)`. O comentário em `ai_task.go:41-43` afirma o contrário. | pendente |
| 2 | Reviewer não pode alterar a árvore | Archon `mutates_checkout: false` | `step/reviewer.go:56` dá `Bash` ao reviewer e nada confere a árvore depois | pendente |
| 3 | Falha de infraestrutura não gasta o orçamento de retry semântico, e espera antes de repetir | Archon + Bernstein | `step/ai_task.go` — worker/reviewer que erra cai no mesmo `attempt++`, sem backoff | pendente |
| 4 | Gate `after` de comando que recusa devolve o diagnóstico ao worker, uma vez | Bernstein (gate→tarefa de reparo) | hoje `markGateFailure` mata a tarefa | pendente (depende do 1) |

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
