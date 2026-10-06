# OpenAI Symphony vs. Corvex: o que trazer para "o corvex lê o board"

**A verdade incômoda primeiro:** o Symphony não tem nada que o Corvex precise para *executar* melhor. Gates, orçamento, registro durável e fan-out com ondas, o Corvex já tem, e em geral melhor. O que falta ao Corvex é a camada fina do Symphony: um laço de ~300 linhas que lê o board e reconcilia o estado, mais adaptadores. E o `ESTADO.md` diz que **o primeiro run de VERDADE contra o board real ainda não aconteceu** (`Dependency-Reverse` também não foi medido). Construir um daemon que despacha do board antes de existir um único run real medido é atacar a ordem errada.

## Fontes e versões

- Symphony: `github.com/openai/symphony`, clonado em `scratchpad/symphony-src`, HEAD `be10a1b` (2026-09-15). Ele contém `SPEC.md` (2312 linhas, "Draft v1, language-agnostic") e a implementação de referência em Elixir em `elixir/`. Nada foi executado.
  - **O repo já não é "só Linear"**, como dizia o preview. Ele tem adaptadores para `linear`, `jira`, `github`, `gitlab`, `asana` e `memory` (`elixir/lib/symphony_elixir/tracker.ex:13-20`). **Não tem Azure Boards.**
- Corvex: o checkout `benchmark` (HEAD `288d666`). O doc de estado é `.corvex/tasks/harness/ESTADO.md`, que só existe na branch `harness/ui-dispatch` (`3d77a30`, 48 commits à frente do HEAD e **não** ancestral dele). Coisas como `POST /api/worktrees`, `next:` em recipe e `POST /api/runs/{id}/retry` existem só nessa branch.
  - O alvo, em uma frase (ESTADO.md:7-11): *"O corvex lê o board, monta o DAG, executa em ondas com isolamento por item, e o humano opera tudo pela UI — sem skill, sem sessão de chat no meio."*

Abreviações usadas abaixo: `S:` = `SPEC.md`; `O:` = `elixir/lib/symphony_elixir/orchestrator.ex`; `AR:` = `agent_runner.ex`; `WS:` = `workspace.ex`; `AS:` = `codex/app_server.ex`.

---

## 1. Como o Symphony funciona

### 1.1 Leitura do board: só polling, sem webhook
- O spec só define polling: "Poll the issue tracker on a fixed cadence" (S:50). A cadência é `polling.interval_ms`, com padrão de 30000 (S:402-408). O `WORKFLOW.md` de exemplo usa 5000 (`elixir/WORKFLOW.md:17-18`). Não há webhook em lugar nenhum do spec nem do código: `grep -i webhook` não acha nada.
- Um tick faz, nesta ordem: reconciliar → validar a config → `fetch_issues_by_states(active_states)` → ordenar → despachar enquanto houver slot (S:742-752, S:1831-1861). No código: `O:83-125` (o `:tick` agenda o `:run_poll_cycle`) e `O:256-308` (`maybe_dispatch`).
- Existe um gatilho manual: `POST /api/v1/refresh` (S:1609-1622; `symphony_elixir_web/router.ex:36`).
- O adaptador tem dois métodos obrigatórios, ambos de leitura (S:1186-1228; `tracker.ex:22-23`):
  - `fetch_issues_by_states(states)`: a lista de candidatos, também usada na limpeza de terminais.
  - `fetch_issues_by_ids(ids)`: a reconciliação. Uma omissão significa "não visível mais", e um registro malformado aqui é erro, não omissão (S:1221-1225).
- Linear: uma query GraphQL filtrada por `project.slugId` e `state.name in`, paginada de 50 em 50, que traz `inverseRelations` para os bloqueadores (`linear/client.ex:13-56`).
  - `dispatchable` = atribuído ao worker **e** não (está em `Todo` com algum bloqueador não-terminal) (`linear/client.ex:496-513`).
  - O filtro de assignee aceita `"me"` via a query de viewer (`linear/client.ex:546-587`).
- Jira: JQL `project = X AND status IN (...)` em `/rest/api/3/search/jql`, e `/rest/api/3/issue/bulkfetch` para a busca por IDs (`jira/client.ex:95-142, 508-510`).
  - `dispatchable` usa `statusCategory == "new"` mais bloqueadores terminais (`jira/client.ex:337-350`).

### 1.2 Modelo normalizado e elegibilidade
- O `Issue` tem estes campos (S:156-197; `tracker/issue.ex:12-28`): `id` (opaco), `identifier` (ABC-123, nomeia o workspace), `state`, `labels` (em minúsculas), `blocked_by`, `priority`, `created_at`, `native_ref` (opaco, só para tools) e **`dispatchable` explícito**, que o adaptador calcula e o scheduler nunca reconstrói (S:1280-1281).
- Para ser elegível, uma issue precisa de: campos obrigatórios, estado ativo e não-terminal, `dispatchable`, todos os `required_labels`, não estar em `running` nem em `claimed`, e slot global e por estado livres (S:754-769; `O:816-870`).
- Ordem: prioridade 1..4, depois a mais antiga, depois o identifier (S:771-775; `O:796-814`).
- Antes do spawn, a issue é revalidada com um fetch por ID, para não despachar um snapshot velho (`O:907-938, 1006-1024`).

### 1.3 Ciclo de vida da issue
- Os estados internos de claim são `Unclaimed → Claimed → Running | RetryQueued → Released` (S:640-660). A implementação acrescenta `blocked` (`O:40`, `O:757-779`): quando o Codex pede input ou aprovação, a issue fica presa em claim, aparece no dashboard e **não** é re-despachada.
  - Esse `blocked` é só em memória: um restart limpa tudo (`elixir/README.md:33-35`).
- O worker é um `Task` supervisionado (`O:953-1004`), e `AgentRunner.run` faz, em ordem (`AR:38-56`):
  - cria ou reusa o workspace;
  - roda o hook `before_run`;
  - roda o laço de turnos;
  - roda o `after_run` no `after`.
- Laço de turnos (`AR:88-140`; S:1932-1993):
  - o 1º turno recebe o prompt renderizado;
  - os seguintes recebem só uma "Continuation guidance" na mesma thread (`AR:144-154`);
  - depois de cada turno, re-busca a issue (`AR:156-171`) e continua enquanto ela estiver ativa e roteável, até `max_turns` (padrão 20).
- **Uma saída normal não significa "pronto".** O orquestrador agenda uma "continuation retry" de 1 s (`O:208-224`, `O:13`). Se a issue ainda estiver ativa, despacha de novo com `attempt=1`.
- O fim do trabalho é **o agente mover a issue** para um estado não-ativo, como `Human Review`. O "Human Review" do exemplo não está em `active_states` (`elixir/WORKFLOW.md:4-15`), então o scheduler simplesmente para de rodar.

### 1.4 Isolamento de workspace
- Um diretório por issue: `<workspace.root>/<workspace_key>` (S:853-866). A chave é o `identifier` com `[^A-Za-z0-9._-]` trocado por `_`. Se a sanitização mudou algo, recebe um sufixo de hash SHA-256 com pelo menos 64 bits (S:307-311; `WS:266-285`).
- Invariantes obrigatórios: o cwd do agente é o workspace, e o workspace fica dentro do root depois de canonicalizar symlinks (S:928-948; `WS:480-500`).
- **Não é git worktree.** O spec não prescreve VCS (S:888-901). O exemplo faz `git clone --depth 1` no hook `after_create` (`elixir/WORKFLOW.md:21-26`).
- Hooks: `after_create` (fatal), `before_run` (fatal para o attempt), `after_run` e `before_remove` (só logados). Têm timeout de 60 s (S:420-442, S:903-926; `WS:398-416`).
- A remoção é `File.rm_rf` (`WS:97-109`).
- A segunda camada de isolamento é o sandbox do próprio Codex: `thread_sandbox: workspace-write`, com `networkAccess: true` no exemplo (`elixir/WORKFLOW.md:31-38`).

### 1.5 O formato do WORKFLOW.md
- É um front matter YAML mais um corpo Markdown, que vira o template do prompt (S:332-353). Chaves: `tracker`, `polling`, `workspace`, `hooks`, `agent`, `codex`, e `server`/`worker` como extensões (S:355-491).
- Template Liquid estrito: variável ou filtro desconhecido falha o attempt (S:493-516). As entradas são `issue` e `attempt`.
- **Hot reload é obrigatório** (S:560-578). A implementação faz polling do arquivo a cada 1 s, comparando mtime, tamanho e hash (`workflow_store.ex:13, 109-172`). Um reload inválido mantém a última config boa.
- `$VAR` só é resolvido quando o valor referencia explicitamente uma variável. Não existe override global por env (S:535-558).
- **Todo o processo vive na prosa do corpo**, umas 300 linhas no exemplo: o mapa de status, o "Completion bar before Human Review" e as regras de PR feedback (`elixir/WORKFLOW.md:41-end`). Nada disso tem ponto de aplicação no código.

### 1.6 Reconciliação
- Roda a cada tick, **antes** do dispatch (S:819-839; `O:310-333`):
  - **A, stall:** se `elapsed > codex.stall_timeout_ms` (5 min) desde o último evento, mata o worker e agenda retry (`O:581-639`). Se o stall foi por pedido de input, a issue vai para `blocked` em vez de retry (`O:612-622`).
  - **B, estado no tracker:** se ficou terminal, mata o worker e **apaga o workspace**. Se ficou não-ativa ou deixou de ser roteável, mata o worker e preserva o workspace. Se continua ativa, atualiza o snapshot. Se sumiu, mata e preserva (`O:421-441, 478-498`). Se o fetch falhar, mantém os workers (`O:327-331`).
- As issues em `blocked` também são reconciliadas (`O:335-358, 456-476`).
- No boot, apaga os workspaces de issues que já estão terminais (S:841-849; `O:1160-1175`).
- Restart: **não existe estado durável.** Timers, sessões e `blocked` se perdem, e a recuperação vem de re-poll mais os workspaces preservados (S:57-58, S:1690-1704). Persistir o retry queue está como TODO (S:2239).

### 1.7 Retries
- Na continuação depois de uma saída normal: 1000 ms fixos. Por falha: `min(10000 * 2^(n-1), max_retry_backoff_ms)`, com teto padrão de 5 min (S:790-817; `O:1232-1243`).
- Quando o timer dispara, re-busca por ID. Se sumiu, solta o claim. Se ficou terminal, limpa e solta. Se continua ativa e roteável, despacha, ou re-enfileira com "no available orchestrator slots" (`O:1093-1136, 1181-1221`).
- **Não há teto de tentativas nem de tokens.** `grep -i 'budget|max_tokens|cost|max_retr'` no `lib/` só acha o teto de *delay*. O número de attempts cresce para sempre, e o ciclo de continuação (até 20 turnos, 1 s de pausa, mais 20 turnos…) só para quando o agente tira a issue do estado ativo.

### 1.8 Concorrência
- Limite global de `max_concurrent_agents` (padrão 10) e mapa `max_concurrent_agents_by_state` (S:777-788, S:444-461; `O:833-851`).
- Um único GenServer serializa todas as mutações de estado (S:637-638, S:725-731).
- Extensão SSH: um pool de hosts com `max_concurrent_agents_per_host`, e uma run nunca troca de host no meio (S:2252-2312; `config/schema.ex:131-139`; `AR:22-23`).

### 1.9 Observabilidade
- Logs `key=value` com `issue_id`, `issue_identifier` e `session_id` obrigatórios (S:1361-1377).
- A contabilidade de tokens prefere totais absolutos e calcula deltas para não contar duas vezes (S:1419-1448). O último snapshot de rate limit é guardado.
- HTTP opcional, só em loopback: dashboard em LiveView mais `GET /api/v1/state`, `GET /api/v1/<identifier>` e `POST /api/v1/refresh` (S:1459-1632; `router.ex:28-38`).
- Dashboard de terminal: `status_dashboard.ex`, 1941 linhas.

### 1.10 Fronteira de escrita e credenciais (o ponto mais bem pensado)
- O Symphony só **lê** o tracker. As escritas, como transições e comentários, são feitas **pelo agente** através de "provider-native tools", executadas *no processo host* com o token do host (S:1310-1322).
- O token é removido do ambiente do filho com `unset` antes do `exec codex` (`AS:221-249`; S:1107-1112, S:1748-1758).
- **Mas a autoridade não é estreitada:**
  - `linear_graphql` aceita "a raw GraphQL query or mutation" (`linear/agent_tool.ex:8-19`).
  - `jira_rest` aceita GET/POST/PUT/**DELETE** em qualquer `/rest/api/3/*` (`jira/agent_tool.ex:8-36`).
  - O agente não vê o token, mas pode fazer tudo que o token faz.
- Postura padrão da implementação: um `approval_policy` que rejeita prompts. O exemplo usa `never` mais `shell_environment_policy.inherit=all` (`elixir/WORKFLOW.md:32-33`; `elixir/README.md:159-161`).

---

## 2. O que trazer para o Corvex, em ordem

**Recomendação de desenho: o board é uma *fonte da caixa de entrada*, não um despachante autônomo — na fase 1.** A rodada 27 do ESTADO fixou "Nada dispara sozinho. Todo `next` cruza uma fronteira". Um board que dispara run sozinho viola essa regra. A fase 1 lê, reconcilia e oferece o dispatch com um clique. A fase 2 liga `auto: true` só para rotas declaradas, com orçamento diário.

### Como ficaria (Linear, Jira e Azure Boards)

```yaml
# .corvex/config.yaml
board:
  kind: azure            # linear | jira | azure | command
  provider:              # adapter-owned (padrão S:382-389)
    org: yandeh
    project: SmartCare
    token_env: AZURE_DEVOPS_PAT        # entra em security.runner_only_env automaticamente
  poll_interval: 60s
  active_states: [Approved, Ready for Agent]
  terminal_states: [Done, Closed, Removed]
  required_labels: [corvex]           # Linear labels / Jira labels / Azure System.Tags
  routes:                             # item → recipe; primeiro match ganha
    - match: { type: Incident }
      recipe: incident
      env: { INCIDENT_ID: "{{.ID}}" }
      auto: false                     # fase 1: só aparece na caixa
    - match: { type: Feature }
      recipe: pilot
      env: { FEATURE_ID: "{{.ID}}" }
  max_active_runs: 3
  max_active_by_route: { incident: 1 }
  daily_max_cost_usd: 40              # só vale para auto: true
```

| | Linear | Jira Cloud | Azure Boards |
|---|---|---|---|
| candidatos | GraphQL `issues(filter:{project, state:{name:{in}}})`, paginado (como `linear/client.ex:13-56`) | `POST /rest/api/3/search/jql`, `project = X AND status IN (...) AND labels = corvex` | `POST _apis/wit/wiql` (só devolve IDs) → `POST _apis/wit/workitemsbatch` com os fields |
| por IDs | `issues(filter:{id:{in}})` | `POST /rest/api/3/issue/bulkfetch` | `workitemsbatch` com `ids` |
| bloqueadores → `dispatchable` | `inverseRelations` type `blocks` | issuelinks "is blocked by" + `statusCategory` | relations `System.LinkTypes.Dependency-Reverse` (Predecessor) — **ainda não medido no board do SmartCare** (ESTADO.md:69-75) |
| "é meu" | `assignee` = viewer | `assignee = currentUser()` | `[System.AssignedTo] = @Me` |
| auth | API key | email+token (basic) | PAT (basic) ou `az account get-access-token` |
| tipo para rotear | label/project | `issuetype` | `System.WorkItemType` (Incident/Feature/User Story) |

**Atalho que eu faria primeiro:** um adaptador `kind: command`, cujo contrato é "stdout = JSON array de WorkItem normalizado". O SmartCare já tem `az-feature-dag.sh` e `az-show-workitem.sh` (ESTADO.md:55-58, 616-618). O Corvex já confia nesse padrão em `plan.context_command` (`internal/config/config.go:78-83`) e no `fanout.over` (`internal/types/step.go:291-310`). Linear, Jira e Azure nativos em Go viram fase 2. Com isso, a fase 1 custa P em vez de G.

### Itens, em ordem (valor ÷ custo)

| # | Mecanismo (origem no Symphony) | Equivalente no Corvex hoje | Valor | Custo | Risco |
|---|---|---|---|---|---|
| 1 | **Núcleo de leitura de 2 operações + WorkItem normalizado com `dispatchable` explícito** (S:1186-1228, S:1259-1284; `tracker.ex:22-27`) | **Ausente.** Só existe `plan.context_command`, injetado como texto no Planner (`internal/config/config.go:78-83`; `internal/planning/planner.go:86-89`). Não há modelo de item nem estado. | Base de tudo. Separar "o adaptador decide a elegibilidade" de "o scheduler aplica a regra genérica" impede que a lógica de Azure vaze para o orquestrador. | P (`kind: command`) / M (nativo, por provider) | Contrato ruim congela cedo. Copie o do Symphony quase literal, porque ele já foi forçado por 5 providers. |
| 2 | **Claim por item, durável** (S:725-731; `O:816-831` faz isso em memória) | Existe `run.Record` + claim O_EXCL + índice global + heartbeat (`internal/run/record.go:81-103`; `internal/run/claim.go:34`; `internal/run/heartbeat.go:66`), mas **sem chave externa**: o Record não sabe de que item veio. | Dedupe que sobrevive a restart, o que o Symphony **não** tem (README:33-35). Adicionar `WorkItem{provider,id,key,url}` ao Record e checar "existe run não-terminal para este id?" no índice é o claim. | P | Duas leituras divergentes (UI vs. loop). Use `ops.Inbox`/índice como único oráculo, como fez a rodada 27. |
| 3 | **Roteamento item → recipe + env** (o Symphony tem um único prompt por `WORKFLOW.md`, sem roteamento) | Recipes com `requires: env` (`internal/recipe/recipe.go:30-50`); dispatch via `POST /api/runs` → spawn do binário (`internal/server/spawn.go:38-112`). **Falta** o mapeamento. | É o que transforma "lê o board" em "age". O Corvex já tem a peça mais rica (uma recipe com gates por tipo de item). | P | A env no **ambiente**, nunca no argv (regra já fixada na rodada 21). Recusar uma rota cuja env esteja em `runner_only_env`. |
| 4 | **Workspace por item com chave sanitizada + hash** (S:307-311, S:943-948; `WS:266-285`) | `ops.SetupWorktree` cria `feat/<feature>` em `<repo>-<feature>` **sem sanitizar** (`internal/ops/start_worktree.go:41-49`; `internal/ops/worktree.go:32-34`). O ESTADO pede "um worktree por story" (ESTADO.md "O que falta" item 2). | Isolamento por item, e um nome de branch determinístico a partir do item (`corvex/AB-73960-<slug>`), que é o que roteia o PR depois. | P | Um título do board com `/` ou `..` vira caminho. Duas grafias do mesmo dir (`/var` vs. `/private/var`, ESTADO rodada 21 item 4): canonicalize como `WS:480-500`. |
| 5 | **Revalidação imediatamente antes do spawn** (`O:920-938, 1006-1024`) | Ausente (não há fonte externa). | Evita despachar um item que alguém já moveu nos últimos 60 s. Barato. | P | Nenhum relevante. |
| 6 | **Tick de reconciliação: board manda parar** (S:819-839; `O:421-441`) | `run kill` / `run pause` existem (`cmd/run_kill.go`, `cmd/run_pause.go`; `internal/ops/run_kill.go`, `internal/ops/run_pause.go`), mas só por comando humano. | O humano opera pelo board também: mover para `Removed` mata a run. **Mapeamento proposto:** terminal → `kill` + limpar o worktree **se** não houver commit não-empurrado; não-ativo → `pause` (não kill — o Corvex tem `paused`, o Symphony não); fetch falhou → não fazer nada. | M | Matar uma run no meio de um step que já escreveu fora (Azure, PR). Respeitar `canceling` e nunca matar durante um gate `policy`/`human` aberto sem registrar no ledger. |
| 7 | **Custódia da credencial do board** (S:1107-1112, S:1748-1758; `AS:221-249`) | O mecanismo existe, mas o padrão é aberto: `sandbox.env_allowlist` **encaminha** `AZURE_`/`GH_TOKEN` ao worker (`internal/config/config.go:105-127`), e `security.runner_only_env` vem vazio por padrão (`internal/config/config.go:45-56`). | O token do board nunca chega ao worker. O adaptador roda no runner. Isso deveria ser **automático**: `board.provider.token_env` entra em `runner_only_env` sem o usuário lembrar. | P | Recipes que hoje chamam `az` de dentro de um step `code` quebram. Isso é bom: a escrita no Azure deve ser um step `tool` do runner (próximo item). |
| 8 | **Escrita de volta no board como efeito declarado do runner, não como tool do agente** (o Symphony faz o oposto: S:1310-1322) | Ausente. `post-run` hook recebe `CORVEX_STATUS` (README "Custom Hooks"); `next:` existe na branch harness. | Transições como "In Progress ao despachar, Human Review quando o gate humano abre, comentário com link do PR ao `done`" viram dado: `board_effects: {on_dispatch: {state: Active}, on_gate_open: {comment: ...}}`. Executado pelo runner com o token do runner, auditável no ledger. | M | Escrita no Azure é a fronteira que o dono reservou para si ("Nada de escrita no Azure… sem o dono mandar", ESTADO "Regras desta obra"). Começar desligado e com `--dry-run` que imprime o PATCH. |
| 9 | **Limites de concorrência entre runs: global e por rota/estado** (S:777-788; `O:833-851`) | `execution.max_parallel` limita *dentro* de uma run (`internal/config/config.go:143-145`); `max_cost_usd`/`max_cost_per_task_usd` por run (`internal/config/config.go:147-156`, padrões 25/5 em `:354-355`). **Nada entre runs.** | Necessário assim que existir `auto: true`. O Symphony **não** tem teto de custo; o Corvex deve acrescentar `daily_max_cost_usd` somando os Records do dia. | P (contagem) / M (custo agregado) | Contagem de "ativas" tem que incluir `parked` (o processo está vivo e segurando o worktree). |
| 10 | **Limpeza no boot de worktrees de itens terminais** (S:841-849; `O:1160-1175`) | Ausente. Worktrees são criados e removidos à mão ou por A/B (`internal/sandbox/worktree.go:24-60`). | Evita o acúmulo de `../smartcare-*`. | P | O Symphony faz `rm_rf` cego (`WS:97-109`). **No Corvex: recusar a remoção se houver commit não-empurrado ou `git status` sujo**, e listar na caixa em vez de apagar. |
| 11 | **`POST /refresh` + `GET /api/board` + linha do item no card da run** (S:1496-1632) | `/api/state`, `/api/events` (SSE com ticker por request), `/api/runs`, `/api/gates` (`internal/server/routes.go:21-41`). O server é loopback com token (`internal/server/server.go:101`; `internal/server/auth.go:60-75`). | A caixa de entrada mostra "3 itens elegíveis no board", com rota e motivo de inelegibilidade (bloqueado por X, sem label). Barato e torna o loop visível. | P | O stream relê disco a cada tick (`internal/server/stream.go:27-66`). Não pôr a chamada HTTP ao board dentro do stream; o loop grava um snapshot e o stream lê o snapshot. |
| 12 | **Backoff exponencial para falha de *leitura* do tracker** (S:1286-1308, S:1669-1688) | Ausente (não há leitura). | Rate limit do Azure ou Jira não pode virar um laço quente. | P | Nenhum. **Não** estender isso a re-despacho de run falhada (ver §3). |
| 13 | **Bloqueadores entre itens → `dispatchable=false`** (`linear/client.ex:496-513`; `jira/client.ex:337-350`) | `fanout.wave_by` ordena *dentro* de uma Feature (`internal/types/step.go:298-305`), alimentado pelo `az-feature-dag.sh`. | Mesmo dado, uma camada acima: uma story bloqueada não aparece como elegível. | P (dentro do adaptador) | Depende do `Dependency-Reverse` que ninguém mediu. Se o board não modela precedência, tudo fica elegível. Mostrar isso, não esconder. |
| 14 | **Hot reload da config do board** (S:560-578; `workflow_store.ex:109-172`) | Ausente. Cada `corvex run` lê a config fresca, o que basta para runs. | Baixo. Só importa quando existir o loop de longa duração. "Última config boa + erro visível" é o comportamento certo. | P | Reload que muda `routes` com runs em voo: só vale para dispatches futuros (como o spec manda). |
| 15 | **Workers via SSH** (S:2252-2312) | Ausente. | Baixo hoje. | G | Prematuro. |

---

## 3. O que NÃO trazer, e por quê

1. **O processo como prosa no corpo do WORKFLOW.md.** O "Completion bar before Human Review", "Do not move to Human Review unless…" e "Mandatory gate: execute all ticket-provided Validation" (`elixir/WORKFLOW.md`, seções "Completion bar" e "Step 2" item 5) são *pedidos* ao modelo, e nada no código os verifica. O Corvex tem `Gate{Nature: computational|inferential|human|policy|question}` com `Command`, `MaxAttempts`, `MaxCostUSD` e `BranchNot` (`internal/types/step.go:131-159`), que a infraestrutura aplica. Trazer o modelo do Symphony seria trocar gate por promessa. O `.corvex/tasks/harness/ESTADO.md` já registrou a lição: "Prosa sem comando apodrece".

2. **O gate humano como estado do tracker que o próprio agente pode escrever.** No Symphony, `Human Review → Merging` é a "aprovação". Mas o agente tem `linear_graphql` com mutation livre (`linear/agent_tool.ex:8-19`) e `jira_rest` com DELETE (`jira/agent_tool.ex:9`). Nada técnico impede o agente de se aprovar. **No Corvex, a autoridade de aprovação continua no gate store** (`internal/gate/store.go`; `POST /api/gates/{id}/approve`, `internal/server/routes.go:35`). O estado do board é *sinal* para parar ou pausar, nunca *aprovação*.

3. **Tool genérica de escrita no tracker exposta ao agente**, mesmo com o token escondido. Esconder o token sem estreitar a autoridade só troca "exfiltrar o token" por "usar o token". Se algum dia o worker precisar ler o board, a tool deve ser de leitura e escopada ao item corrente (o próprio S:1789-1792 recomenda isso como hardening, e a implementação não segue).

4. **O laço de continuação "enquanto a issue estiver ativa"** (`AR:118-140`; `O:208-224`), sem teto de attempts nem de custo. É exatamente o padrão da memória "Gate de IA não converge sozinho" (11→10 críticos em 21 rodadas). O Corvex já tem `max_retries` (padrão 2, `internal/step/ai_task.go:54-95`), teto por task e por run, e árvore de escalonamento (`internal/step/escalation.go:15-17`). Uma run falhada vai para a caixa, não volta para a fila.

5. **Retry infinito de falha** com backoff limitado só no *delay* (`O:1240-1243`). Pelo mesmo motivo.

6. **Estado de orquestração só em memória** (S:1690-1704). O Corvex já é durável (Record + índice + heartbeat + liveness, `internal/run/liveness.go:155`). Não regredir para ganhar simplicidade.

7. **`rm -rf` do workspace quando o item fica terminal** (`WS:97-109`; `O:426-428`). Com git worktree, isso apaga commits locais não-empurrados. Ver o item 10 da §2.

8. **Postura `approval_policy: never` + rede + `shell_environment_policy.inherit=all`** (`elixir/WORKFLOW.md:32-38`). É o "trusted environment" que o próprio README avisa. O Corvex tem sandbox por perfil e `disallowed_tools`, e deve manter isso.

9. **"Rework = full approach reset"** (fechar o PR, apagar o workpad, branch nova a partir de `origin/main`; `elixir/WORKFLOW.md`, "Step 4"). São ações destrutivas disparadas por prosa. Se existir, deve ser uma recipe com gate humano antes.

10. **O comentário "Codex Workpad" como fonte de verdade do progresso.** O Corvex tem ledger e `activity.jsonl`. No máximo, um espelho somente-escrita do runner no board (item 8 da §2), nunca lido de volta como estado.

11. **Webhook.** O Symphony também não usa. O server do Corvex é loopback com token e checagem de Host contra DNS rebinding (`internal/server/auth.go:21-75`). Receber webhook exige expor a porta, que é a superfície que o `auth.go` existe para fechar. Polling de 60 s mais `POST /refresh` cobre o caso.

12. **Template Liquid estrito no prompt.** No Corvex, o item chega à recipe por env e por `context_command`/step `tool`. Um template de prompt por board recria o "prompt único que decide tudo".

---

## 4. A afirmação de que estou menos seguro

**Os detalhes da API do Azure Boards na tabela da §2** (WIQL → `workitemsbatch`, `System.LinkTypes.Dependency-Reverse` como "Predecessor", `@Me` em WIQL) vêm da minha memória da documentação da Microsoft. Não verifiquei nada disso nesta sessão, e o Symphony não tem adaptador Azure para servir de referência. O próprio ESTADO.md:69-75 diz que o `Dependency-Reverse` "vem da documentação do Azure, não de uma resposta do board do SmartCare". Antes de escrever o adaptador nativo, um GET real numa Feature do board fecha isso em minutos. É mais um motivo para começar pelo `kind: command` em cima dos scripts `az-*` que já rodaram contra o board real.

(Segunda menor confiança: as referências do Corvex são do checkout `benchmark`, 48 commits atrás de `harness/ui-dispatch`. Alguma ausência que eu apontei, como o lock de steps `code` da rodada 1, pode já existir naquela branch.)
