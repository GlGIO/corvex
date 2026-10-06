# Archon vs. Corvex: o que trazer

Fonte: `github.com/coleam00/Archon`, clone raso em `scratchpad/archon-src`, HEAD `d4a23a7` (2026-10-06, "Freeze AI configuration for each workflow run (#3827)"). Nada do clone foi executado. Os caminhos do Archon são relativos ao clone. Os do Corvex são relativos a `o checkout do corvex` (commit `288d666`).

## 0. Correção de premissa (leia primeiro)

O projeto é mesmo o `coleam00/Archon`, mas a descrição do pedido mistura vocabulário do Corvex com o do Archon:

- **Archon não tem "recipes"**. O nome lá é *workflows*, agrupados em *packs* (`.archon/workflows/<pack>/<workflow>/`).
- **Archon não tem `kind: tool` / `kind: test`.** Nós determinísticos são escritos como `bash:` ou `script:` (com `runtime: bun|uv`) e normalizam para `kind: 'exec'` (`packages/workflows/src/schemas/dag-node.ts:382-397`).
- `kind: tool|test|repro|code` é vocabulário do **Corvex** (`internal/recipe/recipe.go:61`, `.corvex/recipes/cost-split.yaml:61,73`).
- Gate humano e worktrees existem nos dois.

Também confirmei que o "Archon v1" (RAG, task management, Python) é outro produto. Está arquivado no branch `archive/v1-task-management-rag` (README e `docs-web/.../getting-started/what-archon-is-not.md`).

---

## 1. O que o Archon é

### 1.1 Identidade e arquitetura

O Archon se descreve como "workflow engine for AI coding agents" ou "governed agentic automation engine" (README:22, `what-archon-is-not.md`). É escrito em TypeScript, roda em Bun e é um monorepo (`packages/`):

| Pacote | Papel |
|---|---|
| `workflows` | Engine: loader, validator, `dag-executor.ts` (12.155 linhas), dry-run, fixtures |
| `core` | Orchestrator de conversas, config, DB, resolvedor de isolamento de sub-runs |
| `providers` | Claude (Agent SDK), Codex (`codex app-server`), Pi, mais Copilot e OpenCode, ambos deprecated (CHANGELOG, "Deprecated") |
| `isolation` | Backends worktree, in-place e container (`isolation/src/backends/*`) |
| `adapters` | Web, Slack, Telegram, Discord, GitHub |
| `server`, `web` | API (Hono/OpenAPI) e console web |
| `cli` | `archon workflow run/resume/approve/reject/respond/wait/...` |
| `forge`, `plugins/forge-github` | Operações de PR (checks, merge, draft) desacopladas atrás de um contrato de "forge" |

Diagrama (README:280-310): adapters → Orchestrator → {Command Handler, Workflow Executor, AI clients} → SQLite/PostgreSQL (14 tabelas: runs, events, node sessions, isolation environments, users e outras).

O agente não é chamado por API direta. O Archon dirige runtimes de assistente: o Claude Agent SDK, o Codex app-server e o Pi.

### 1.2 Modelo de execução

- **O workflow é um DAG.** O loader calcula camadas topológicas (`GraphPlan { nodes, layers, sinks }`, `schemas/workflow.ts:122-134`).
- **Execução por camada.** `runLayers` (`dag-executor.ts:9530`) executa cada camada concorrentemente. Uma camada roda serializada só quando mistura nós `mutates_checkout: false` com nós que escrevem (`dag-executor.ts:9542-9560`).
- **Sem cap de paralelismo por camada.** Não achei limite configurável dentro de uma camada. Os caps que existem são:
  - por provider, install-wide: `concurrency.providers.<id>` (`core/src/config/provider-concurrency.ts:28-35`);
  - `MAX_CONCURRENT_CONVERSATIONS` (`core/src/config/config-loader.ts:596`);
  - `fan_out.max_parallel`, default 5 (`dag-node.ts:895`).
- **Contexto de sessão** é fresh por padrão. Nós sequenciais podem compartilhar sessão. `context: {resume: <node>}` faz fork da sessão de um nó nomeado (`dag-node.ts:155-159`). Uma camada paralela zera a sessão compartilhada (`dag-executor.ts:9543`).
- **Runs** podem ser foreground, `--detach` (processo filho) ou disparadas por trigger (schedule ou webhook GitHub, `guides/workflow-triggers.md`).
- **Resume** (`--resume`) pula nós já completos. `always_run: true` força a reexecução de um nó (`dag-node.ts:239`).
- **Configuração de IA congelada.** Desde #3827, a configuração de IA de cada run é congelada na metadata e reusada no resume (`workflows/src/run-ai-configuration.ts`).

### 1.3 Formato do workflow (YAML real)

`.archon/workflows/sdlc/deliver/archon-deliver.yaml` (813 linhas) é o exemplo mais rico:

```yaml
# archon-deliver.yaml:25-35
requires: [github]
inputs:
  work:
    default: ""
    description: >-
      What to deliver — a plan path, findings, ...
...
returns: flip-ready                      # :63 — nó cujo output é o "resultado" do workflow

nodes:
  - id: impl                             # :66 — composição estática
    include: archon-implement
    with: { work: "$INPUTS.work" }

  - id: gate-green                       # :76-95 — gate determinístico (script TS)
    script: gate-green
    runtime: bun
    output_type: green-gate              # artefato tipado
    output_format: &green_gate           # JSON Schema imposto pela engine
      type: object
      properties:
        gate: { type: string, enum: [green] }
        red_cause: { type: string }
        ...
      required: [gate, red_cause, stage, summary]
    depends_on: [impl]
    with:
      green: "$impl.output.green"        # binding de output upstream
      red_cause: "$impl.output.red_cause"

  - id: reshape                          # :109-112 — condicional
    include: archon-implement
    depends_on: [shape]
    when: "$shape.output.findings == true"

  - id: pr                               # :139-144 — join tolerante a ramo pulado
    include: archon-pr
    depends_on: [shape, gate-reshape]
    trigger_rule: none_failed_min_one_success

  - id: classify                         # :168-189 — nó de IA barato com saída estruturada
    command: classify-review-scope
    model: small                         # tier, não modelo literal
    mutates_checkout: false              # engine falha o nó se ele tocar a árvore
    output_format: { type: object, properties: { tier: {enum: [focused, full]}, ... } }

  - id: corrections                      # :244-359 — laço multi-nó
    when: "$review.output.action == 'correct'"
    loop_group:
      until_bash: |                      # condição de saída determinística
        value=$recheck.output.action
        test "$value" != "correct"
      max_iterations: 5
      fresh_context: true
      nodes:
        - id: ci-note
          timeout: 45000
          on_timeout: skip               # timeout vira skip, não falha
          script: ci-note
          runtime: bun
        - id: ci-evidence
          trigger_rule: all_done
          with:
            note: { from: "$ci-note.output", if_skipped: "No CI evidence ..." }
        - id: fix          ... include: archon-implement
        - id: gate-correction-green ... script: gate-green
        - id: recheck      ... include: archon-review

  - id: await-checks                     # :507-... — espera de CI durável, em laço
    loop_group:
      max_iterations: 13
      until_bash: |
        value=$ci-probe.output.state
        test "$value" != "pending"
```

O README (linhas 46-75) traz a forma curta: `prompt:`, `bash:`, `loop: {until, fresh_context, interactive}`.

### 1.4 Tipos de nó

`dag-node.ts`. O `kind` é interno; quem escreve o YAML usa a chave açúcar.

| Chave YAML | `kind` | Linha | Descrição |
|---|---|---|---|
| `prompt:` / `command:` | `agent` | 337 | Nó de IA. `command:` carrega um `.md` de `commands/` com `$INPUTS.*` |
| `bash:` / `script:` (+`runtime: sh\|bun\|uv`, `deps:`) | `exec` | 382 | Determinístico, sem IA. `output_format` validado contra o stdout |
| `loop:` | `loop` | 491 | Prompt repetido até `until` (sinal na última linha), `until_bash` (exit 0) ou `until_field` (booleano do output) |
| `loop_group:` | `loop_group` | 549 | Sub-DAG reexecutado inteiro a cada iteração. Aninhável |
| `approval:` | `gate` | 673 | Pausa humana com decisões declaradas |
| `cancel:` | `halt` | 688 | Termina a run com motivo |
| `wait:` | `wait` | 776 | Espera durável: `duration_ms`, `until` (timestamp), `event`+`deadline_ms`, `attention` |
| `include:` | (expandido no load) | 832 | Composição estática: os nós do outro workflow entram na mesma run |
| `include:` + `fan_out:` | `compose_fan_out` | 956 | Composição expandida em runtime, uma instância por item |
| `workflow:` | `workflow` | 927 | Sub-run filha governada (run, custo e gates próprios). `isolation: inherit\|worktree`, `fan_out: {items, as, max_parallel, join}` |

Campos comuns a todos os nós (`dagNodeBaseSchema`, `dag-node.ts:176-263`):

- **Fluxo:** `depends_on`, `when`, `trigger_rule` (`all_success|one_success|none_failed_min_one_success|all_done`, :53).
- **Modelo e custo:** `model`, `provider`, `effort`, `fallbackModel`, `maxBudgetUsd`.
- **Contexto e saída:** `context`, `output_format`, `output_type`, `persist_session`.
- **Ferramentas e capacidades:** `allowed_tools`/`denied_tools`, `mcp` (arquivo por nó), `skills`, `plugins`, `agents` (sub-agentes inline), `hooks`, `sandbox` (settings do SDK), `settingSources`.
- **Execução:** `idle_timeout`, `retry`, `always_run`, `mutates_checkout`.

### 1.5 Gate humano

Documentado em `docs-web/.../guides/approval-nodes.md`. Implementado em `executeApprovalNode`, `dag-executor.ts:7091`.

- **Pausa.** A run vai para status `paused` e grava `{nodeId, message}` na metadata.
- **Notificação.** A mensagem vai para a plataforma de origem (CLI, Web, Slack com botões, GitHub). Uma run pausada bloqueia o path do worktree para outras runs.
- **Decisões declaradas** (`approval.decisions: [{id, label}]`, `dag-node.ts:583-627`). O output vira `{decision, text}`, roteável por `when: "$gate.output.decision == 'revise'"`. Uma decisão não tem efeito implícito: `cancel` só cancela se o autor ligar a um nó `cancel:`.
  - CLI: `archon workflow respond <run-id> <decision> [text]`.
  - Também: `approve`, `reject`, `--detach`.
- **Legado `on_reject: {prompt, max_attempts ≤10}`.** Roda uma IA de retrabalho com `$REJECTION_REASON` e re-pausa. Cancela quando esgota (`dag-executor.ts:7140-7165`).
- **Laços interativos** (`loop.interactive: true` + `gate_message`) param a cada iteração para input humano (`schemas/loop.ts`).
- **Gates concorrentes na mesma camada:** só um pausa por vez. Os outros reexecutam no resume (#3751).
- **Container write-back gate.** Em projetos-pasta com `--container`, as escritas vão para um overlay. No fim há um gate de aprovação antes de aplicar o diff na árvore real (`guides/container-isolation.md:54-100`). `write_back: auto` pula o gate.
- **Gate de evidência.** `evidence_policy.required: true` recusa `completed` se `$ARTIFACTS_DIR/evidence.json` não existir. Checa só a presença do arquivo, não o conteúdo (`dag-executor.ts:11936-11980`).

### 1.6 Worktrees e isolamento

`docs-web/.../book/isolation.md`, `packages/isolation`.

- Por padrão, cada run ganha um worktree em `~/.archon/workspaces/<owner>/<repo>/worktrees/<branch>`.
- Opções: `--branch`, `--from`, `--base` (corta e é alvo do PR), `--no-worktree`.
- `worktree.enabled` no workflow fixa a política. Também existem `copyFiles` e `initSubmodules` na config do repo.
- Sub-runs com `isolation: worktree` ganham worktree próprio, com nome `...-child-N`.
- Comandos de ciclo de vida: `archon isolation list|cleanup [dias] [--merged] [--include-closed]` e `archon complete <branch>` (remove worktree, branch local e branch remoto). Não remove worktree sujo.
- Backends alternativos: `in-place` para pastas e `container` (overlay Docker).
- **Issues abertas recentes sobre worktree:** #3865 (destruição apaga um clone substituto não relacionado), #3863 e #3866. O subsistema ainda tem bordas.

### 1.7 Retries

`dag-executor.ts:884-1165`, `schemas/retry.ts`.

- **Nós de IA:** padrão de 2 retries, 3s, só para falha `transient`.
- **Nós `bash`/`script`:** zero retry por padrão. É opt-in porque scripts têm efeitos colaterais (`getExplicitNodeRetryConfig`, :972).
- **Config:** `retry: {max_attempts 1-5, delay_ms 1000-60000, on_error: transient|all}`.
- **A decisão vem da classe da falha, nunca do texto do erro** (`retryableFailureClass`, :988; CHANGELOG "Changed").
  - Classes: `fatal` (auth, quota, misconfigured, cancelado) nunca repete. `unknown` só repete com `on_error: all`.
  - Backoff exponencial (`getRetryDelayMs`, `executor-shared.ts:69`).
  - Em `rate_limited`, o orçamento sobe para `RATE_LIMIT_MAX_RETRIES = 5` (`executor-shared.ts:39`).
- **Custo dos retries:** o custo de cada tentativa vira evento próprio e é somado (:1015-1020).
- **Saída estruturada inválida** em provider best-effort: até 3 reasks numa sessão descartável (`STRUCTURED_OUTPUT_MAX_REASKS`, :920).

### 1.8 Paralelismo, condições e laços

- **Paralelismo:** camadas do DAG, `fan_out` sobre um array JSON vindo de um output (`join: all_success|all_done`; `first_success` foi rejeitado deliberadamente, `dag-node.ts:880-897`) e `compose_fan_out`.
- **`when:`** (`condition-evaluator.ts:1-38`) suporta:
  - `==`, `!=`, comparações numéricas, `&&`/`||` sem parênteses, `$node.output.field`, `$INPUTS.x`;
  - expressão malformada → nó pulado (fail-closed);
  - **referência não resolvível → o nó falha** (não pula em silêncio);
  - comparar objeto ou array → erro (#2995).
- **Laços:** `loop`, `loop_group` (aninhável) e `max_iterations` obrigatório. O sinal `until` só vale sozinho na última linha (#2994), para que "not COMPLETE yet" não encerre o laço.

### 1.9 Contexto entre nós

- **Substituição de outputs:** `$node.output` e `$node.output.field`, validados contra o `output_format` do produtor no load.
- **`with:`** em `command`/`script` aceita valores literais ou diretivas `{from, if_skipped}` (`dag-node.ts:284-311`). Sem `if_skipped`, ler um produtor pulado faz o consumidor falhar.
- **Diretórios de artefatos e estado:**
  - `$ARTIFACTS_DIR` é por run;
  - `$STATE_DIR` é estado entre runs, sem lock (`authoring-workflows.md:1377-1440`);
  - artefatos tipados `output_type` geram `nodes/<id>.md` + `<id>.meta.json` e são lidos por tipo via `$TYPED_ARTIFACTS_FILE`.
- **Workflow como função:** `inputs:` declarados (required/default) e `returns:` + `outcome_field`. O workflow pode ser chamado por outro.
- **Sessões:** `persist_session` continua a sessão do provider entre runs da mesma conversa, por fork.

### 1.10 Plataformas

- **Adapters de entrada:** Web, CLI, Telegram, Slack, Discord, GitHub webhooks (README:262).
- **Triggers:** schedule nativo (LaunchAgent no macOS, `archon trigger schedule`) e webhooks GitHub verificados. Cada binding define host, usuário, `resource` e política de `overlap: queue`.
- **Forge:** abstração de PR (abrir, draft/ready, checks, merge fixado, rerun de check falho, #3887) implementada pelo plugin `forge-github`.
- **Router de chat:** o usuário descreve a tarefa e o router escolhe o workflow (README:236).

### 1.11 Persistência, estado e observabilidade

- **Banco:** SQLite por padrão, Postgres opcional. Tabelas `workflow_runs`, `workflow_events` (log de eventos por nó), `workflow_node_sessions`, `isolation_environments`, usuários e chaves.
- **Status de run:** `failed` é retomável e `cancelled` é descartado (CHANGELOG 0.11.0).
- **`archon workflow wait <run-id>`** tem contrato de exit code para hosts: 0 = disse algo, 3 = timeout com run viva, 1 = erro (`reference/cli.md:754-790`).
- **Telemetria PostHog**, opt-out, com classes de falha categóricas (README, seção Telemetry).

### 1.12 Teste de workflows (diferencial forte)

- **Dry-run em memória:** `archon workflow run --dry-run --stubs <yaml>`. Não cria run, worktree nem chamada de provider. Opções:
  - `--stubs-init` gera o esqueleto;
  - `--default-stubs` preenche com placeholders válidos;
  - `--exec-code` executa os scripts reais num worktree scratch;
  - `--pause-at-gates` para no primeiro gate.
- **Fixtures** `<nome>.stubs.yaml` ficam ao lado do workflow (`fixture-runner.ts:1-60`), com `fixture: {expect: completed|failed|paused|cancelled, fail-node, reached, inputs, resolved-text-contains}`.
  - Exemplo: `sdlc/deliver/fixtures/red-introduced.stubs.yaml` stuba a IA dizendo `red_cause: introduced` e afirma que `gate-green` falha.
  - O `deliver` tem 28 fixtures.

### 1.13 UI

Console web via `archon serve`: runs com filtros e progresso ao vivo, detalhe com event log, artefatos e grafo, chat por projeto, settings de providers, tiers e aliases, e um workflow builder visual experimental (README:213-224).

---

## 2. O que trazer para o Corvex, por valor/custo

Legenda de custo: **P** é até ~1 dia, **M** é de 2 a 5 dias, **G** é mais de 1 semana.

| # | Item | Valor | Custo |
|---|---|---|---|
| 1 | Asserção `mutates_checkout: false` | Alto | P |
| 2 | Custódia de MCP verificada, não só configurada | Médio-alto | P |
| 3 | Retry por classe de falha, com backoff | Alto | P-M |
| 4 | Saída estruturada com JSON Schema | Alto | M |
| 5 | `inputs:` declarados na recipe | Médio | P |
| 6 | Outputs entre stages e bindings com `if_skipped` | Alto | M |
| 7 | `when:` e `trigger_rule` | Alto | M |
| 8 | Gate humano com decisões roteáveis | Médio | P |
| 9 | Congelar a configuração de IA na run | Médio | P |
| 10 | Tiers de modelo e `model:` por stage | Médio | P |
| 11 | Dry-run com stubs e fixtures de recipe | Médio-alto | M |
| 12 | Classificação de vermelho com prova determinística | Médio-alto | M |
| 13 | Laço multi-stage (`loop_group`) | Médio | M-G |
| 14 | Isolamento por worktree em ondas paralelas | Médio-alto | G |
| 15 | Composição de recipes (`include`/sub-recipe) | Médio | M-G |
| 16 | `always_run` e `on_timeout: skip` | Baixo-médio | P |
| 17 | Contrato de exit code de `run wait` | Baixo-médio | P |

### 1. Asserção `mutates_checkout: false` (árvore intocada)

- **Archon:** tira um snapshot do `git status --porcelain` antes do nó e falha o nó se a árvore mudou fora dos diretórios da engine (`dag-node.ts:240-249`; `snapshotCheckout`/`assertCheckoutUntouched`, `dag-executor.ts:1193-1300`).
- **Corvex hoje:**
  - o Reviewer tem `Bash` (`internal/step/reviewer.go:56`), que pode escrever;
  - gates inferenciais, stages `test` e `repro` também deveriam ser só-leitura;
  - não há asserção. A restrição é só de ferramentas.
- **Valor alto.** Transforma "o Reviewer não deveria editar" de convenção em invariante verificado. Pega reviewer ou teste que "conserta" algo, um vazamento silencioso para a evidência.
- **Custo P.** São duas chamadas `git status` em torno do step.
- **Risco baixo.** Testes que geram arquivos ignorados pelo `.gitignore` não contam. Arquivos não ignorados gerados pelo teste (cobertura, por exemplo) darão falso positivo. Será preciso uma lista de exclusão.

### 2. Custódia de MCP verificada, não só configurada

- **Archon:** um nó Codex recebe exatamente os MCPs do seu `mcp:`. O Archon verifica os servidores vivos antes do turno e falha o nó como `misconfigured` se algum não declarado estiver ativo (CHANGELOG [Unreleased], "Breaking"). Plugins também passaram a ser opt-in por nó.
- **Corvex hoje:** MCP só no Worker via `--mcp-config` (`internal/provider/claude/command.go:88-156`). Não achei verificação de que Reviewer, Planner e gates sobem sem MCP. O `claude` CLI carrega `.mcp.json` do projeto e config do usuário, a menos que se passe `--strict-mcp-config`.
- **Valor médio-alto.** Fecha a custódia por papel de forma positiva. Combina com a sua regra "controle passa pela porta da regra".
- **Custo P.** Passar `--strict-mcp-config` a todos os papéis. Um teste de controle positivo que planta um `.mcp.json` no repo e afirma que o Reviewer não o vê.
- **Risco baixo.** Pode quebrar quem dependia, sem saber, de MCP de projeto no Worker. Isso é desejável.

### 3. Retry por classe de falha, com backoff exponencial

- **Archon:** a classe da falha decide o retry. `fatal` nunca repete. `transient` e `rate_limited` repetem com backoff, e `rate_limited` amplia o orçamento para 5. Scripts não repetem por padrão. O texto do erro nunca decide (`dag-executor.ts:943-1112`, `executor-shared.ts:39,69`).
- **Corvex hoje:**
  - `execution.max_retries` repete o Worker com o diagnóstico do Reviewer (`internal/step/ai_task.go:53-96`). Isso é retry semântico, e é bom.
  - Não achei backoff, jitter nem tratamento de 429 ou overload. Busca por `backoff|jitter|rate.limit` não deu nada. Laços repetem imediatamente.
  - Erro de infraestrutura (429, auth) e falha de review consomem o mesmo orçamento.
- **Valor alto.** Um 429 hoje queima uma tentativa semântica e custo. Auth expirada repete à toa.
- **Custo P-M.** É preciso tipar o erro do provider claude-cli: o stream-json traz `subtype` ou `is_error` com motivo. Depois, um laço separado para infraestrutura, fora do orçamento semântico.
- **Risco médio.** Classificar errado vira retry infinito de algo fatal. Mitigar com `fatal` como padrão para o desconhecido, como o Archon faz (`unknown` só repete com `on_error: all`).

### 4. Saída estruturada com JSON Schema (`output_format`)

- **Archon:** `output_format` é JSON Schema imposto pela engine. Provider best-effort recebe até 3 reasks (`dag-executor.ts:920`). Script também tem o stdout validado (#2453).
- **Corvex hoje:** `VERDICT: PASS|FAIL`, `CATEGORY:` e `TASK-REPORT:` são parseados de texto livre (`internal/step/reviewer.go:88`, `internal/step/report.go:29`). INDETERMINATE gera retry (`ai_task.go:216`). Verifiquei que o `claude` CLI local expõe `--json-schema <schema>`.
- **Valor alto.**
  - Elimina a classe INDETERMINATE e o custo dela.
  - É pré-requisito dos itens 6 e 7.
  - O veredito do Reviewer, que é evidência na trava, passa a ser um documento validado.
- **Custo M.** Schemas para Reviewer, relatório do Worker e gate inferencial. Fallback para o parser atual.
- **Risco médio.** Muda o formato de evidência gravado, então goldens e caracterização mudam. Pela sua convenção, regravar golden só com controle positivo.

### 5. `inputs:` declarados na recipe

- **Archon:** `inputs: {nome: {required, default, description}}`. `--input k=v` é validado antes de qualquer worktree ou custo de IA (`schemas/workflow.ts:241`, `reference/cli.md:304`).
- **Corvex hoje:** `requires:` cobre bin e env (`internal/recipe/recipe.go:37-52`). Não achei parâmetros de recipe.
- **Valor médio.** Recipes reutilizáveis sem editar o YAML.
- **Custo P.**
- **Risco baixo.** Interage com a detecção de drift (`internal/ops/run_target.go:22-50`): os inputs precisam entrar no hash de compilação.

### 6. Outputs entre stages e bindings com `if_skipped`

- **Archon:** `$node.output.field` é validado contra o schema do produtor no load. `with: {x: {from: "$a.output", if_skipped: ...}}`. Ler um produtor pulado sem default faz o nó falhar alto (`dag-node.ts:284-311`, `condition-evaluator.ts:21-36`).
- **Corvex hoje:** só `{{item}}` no fan-out (`internal/orchestrator/fanout.go:261`), o anchor em prosa (`internal/anchor/anchor.go`) e arquivos via `$CORVEX_RUN_BASE`/`$CORVEX_RUN_ID` (`internal/step/step.go:224-231`).
- **Valor alto.** Permite stages `tool` e `test` que decidem com base em dados, não em arquivos combinados por convenção.
- **Custo M.** Depende do item 4.
- **Risco médio.** Valores interpolados em `sh -c` são injeção de shell. O Archon faz shell-quote e manda para arquivo quando o valor é grande (`shellQuoteOrFile`, `dag-executor.ts:1334`) e passa scripts por env `INPUTS_*`. Copie a abordagem por env, não a interpolação.

### 7. `when:` e `trigger_rule`

- **Archon:** gramática pequena, fail-closed para sintaxe e falha alta para referência ausente (`condition-evaluator.ts`). Quatro trigger rules (`dag-node.ts:53-58`).
- **Corvex hoje:** não há ramo condicional. O `when:` do gate significa before/after (`types/step.go:122-173`). Falha propaga SKIPPED aos dependentes (`internal/orchestrator/schedule.go:286`), o que equivale ao `all_success` implícito.
- **Valor alto.** Ramos do tipo "só investiga se o repro falhou" e "só abre PR se o gate passou", e um join tolerante (`none_failed_min_one_success`).
- **Custo M.** Depende do item 6.
- **Risco médio.** A semântica de skip versus falha é sutil. O próprio Archon mudou `none_failed_min_one_success` de forma breaking na 0.11.0 (CHANGELOG). Adote a regra "referência ausente = falha" desde o início.

### 8. Gate humano com decisões roteáveis

- **Archon:** `approval.decisions: [{id,label}]` vira output `{decision, text}`. A decisão não tem efeito implícito; o autor roteia com `when:` (`dag-node.ts:583-627`, `approval-nodes.md`).
- **Corvex hoje:** approve, reject e o question gate com texto livre (`internal/step/human_gate.go:139`, `internal/gate/gate.go:105`).
- **Valor médio.** "Revisar" vira um ramo explícito em vez de rejeitar e reexecutar.
- **Custo P**, depois dos itens 6 e 7.
- **Risco baixo.** Preserve a trava: nenhuma decisão é aceita com leitura obrigatória pendente (`internal/gate/store.go:97-104`).

### 9. Congelar a configuração de IA na run

- **Archon:** um snapshot de provider, modelos e tiers vai para a metadata da run. O resume usa o snapshot, e `--model` e `--config` são rejeitados no resume (#3827, `run-ai-configuration.ts`).
- **Corvex hoje:** há detecção de drift da recipe (`internal/ops/run_target.go:22-50`). Não achei snapshot de `provider.models.*` nem de tetos de custo na run. A busca por snapshot, freeze ou config_hash em `internal/` não deu nada relevante. O resume (`internal/orchestrator/run.go:193`) relê a config viva.
- **Valor médio.** Uma A/B ou um resume não mudam de modelo nem de teto silenciosamente porque alguém editou `.corvex/config`.
- **Custo P.**
- **Risco baixo.**

### 10. Tiers de modelo (`small|medium|large`) e `model:` por stage

- **Archon:** `model: small` e aliases. `--model large=<spec>` reconfigura por run (`archon-deliver.yaml:172`, `reference/cli.md:305`).
- **Corvex hoje:** há modelo por papel (`internal/config/config.go:97-99`) e `model:` só em gate inferencial. Stages `code` não aceitam override.
- **Valor médio.** Um classificador barato num stage `tool`, ou um Worker caro só no stage difícil. Também é o encaixe natural para um dia consumir `ab-stats.json`, que hoje não é lido.
- **Custo P.**
- **Risco baixo.** Interage com o teto de custo, que já é por task.

### 11. Dry-run com stubs e fixtures de recipe

- **Archon:** `--dry-run --stubs`, `--stubs-init`, `--exec-code` e `*.stubs.yaml` com `expect`, `fail-node` e `reached` (`fixture-runner.ts:1-60`). Exemplo: 28 fixtures em `sdlc/deliver/fixtures/`.
- **Corvex hoje:**
  - `corvex recipe validate|compile` (`cmd/recipe_noun.go:14-40`);
  - `corvex run --dry-run` (`cmd/run_flags.go:36`);
  - goldens de CLI;
  - não há simulação de fluxo com outputs stubados.
- **Valor:** médio hoje e alto depois dos itens 6 e 7. Com ramos condicionais, sem isso não há como testar a recipe sem gastar dinheiro.
- **Custo M.**
- **Risco baixo.**

### 12. Classificação de vermelho (introduced, inherited, environment) com prova determinística

- **Archon:** `gate-green.ts` falha só em vermelho `introduced`. Deixa passar `inherited` e `environment` com a ressalva registrada como artefato tipado (`.archon/workflows/sdlc/deliver/scripts/gate-green.ts`). **Mas a causa é declarada pelo agente.** O script só exige que o resumo não esteja vazio.
- **Corvex hoje:** `repro` compila "falha antes" e "passa depois" (`internal/recipe/repro.go:30`). Não achei comparação com o commit-base para testes que já estavam vermelhos.
- **Valor médio-alto.** Vermelho herdado hoje mata a run ou gera retry inútil.
- **Custo M.**
- **Risco:** não copie a versão do Archon. Faça a determinística: rodar o mesmo comando de teste no merge-base, num worktree descartável, e classificar como `inherited` só se lá também falhar. A versão que confia no agente conflita com a trava de evidência (ver seção 3).

### 13. Laço multi-stage (`loop_group`) com `until` determinístico

- **Archon:** sub-DAG reexecutado até `until_bash` ou `max_iterations` (`dag-node.ts:524-553`; uso em `archon-deliver.yaml:244-359`).
- **Corvex hoje:**
  - `loop{until,max}` só em stage de comando (`internal/recipe/recipe.go:90`, `internal/step/command_stage.go:65-130`);
  - o ciclo Worker→Reviewer→retry é fixo (`internal/step/ai_task.go`).
- **Valor médio.** "Corrigir → gate → re-review" como estrutura de recipe. O ciclo fixo do Corvex já cobre o caso mais comum.
- **Custo M-G.** Mexe no agendador, no estado do `tasks.md` e no resume.
- **Risco alto de interação com o teto de custo.** Exigir `max` obrigatório e somar o custo de cada iteração ao teto.

### 14. Isolamento por worktree em ondas paralelas e fan-out

- **Archon:** worktree por run. Sub-run com `isolation: worktree`. Fan-out sobre o checkout compartilhado só se o filho declarar `mutates_checkout: false` (`dag-node.ts:905-937`).
- **Corvex hoje:** tasks da mesma onda compartilham a árvore de trabalho (`internal/orchestrator/schedule.go:148-181`). Worktree só em A/B (`internal/step/abrun.go:51`).
- **Valor médio-alto.** Paralelismo real sem colisão de arquivos.
- **Custo G.** Exige merge de volta, conflito e ordem, que hoje o commit por task resolve sequencialmente.
- **Risco alto.** O próprio Archon tem issues abertas de destruição de worktree (#3863, #3865, #3866). Uma opção mais barata no meio do caminho: só permitir onda paralela entre tasks com conjuntos de arquivos declarados disjuntos, ou com `mutates_checkout: false`.

### 15. Composição de recipes (`include` ou sub-recipe)

- **Archon:** `include:` estático com `with:`, e `workflow:` como run filha com `returns:` (`dag-node.ts:832-937`).
- **Corvex hoje:** não há composição.
- **Valor médio.** Cresce com o tamanho da biblioteca de recipes.
- **Custo M-G.**
- **Risco médio.** Namespacing de IDs no `tasks.md` e no gate store.

### 16. `always_run` e `on_timeout: skip`

- **Archon:** `always_run: true` reexecuta o nó no resume (`dag-node.ts:239`). `on_timeout: skip` serve para produtores opcionais (`archon-deliver.yaml:266-268`).
- **Corvex hoje:** o resume reaproveita PASSED. Timeout é falha (`internal/step/liveness.go`).
- **Valor baixo-médio.** Stages `tool` que produzem um arquivo consumido depois ficam velhos no resume.
- **Custo P.**
- **Risco baixo.**

### 17. Contrato de exit code de `run wait`

- **Archon:** 0 = estado observado, 3 = timeout com run viva, 1 = erro (`reference/cli.md:754-790`).
- **Corvex hoje:** existe `run watch` (`cmd/run_noun.go`). Não verifiquei se ele tem esse contrato.
- **Valor baixo-médio.** CI e scripts esperam um gate sem fazer polling na mão. Isso conversa com a memória "sleep em foreground é bloqueado".
- **Custo P.**
- **Risco baixo.**

### Achado colateral (não é do Archon, mas apareceu na comparação)

`runShell` passa `os.Environ()` inteiro mais as duas variáveis `CORVEX_RUN_*` (`internal/step/step.go:226-230`). O README:294-296 diz "duas variáveis e nenhuma outra". O Archon, no backend container, não deixa o `process.env` do host atravessar (`guides/container-isolation.md:100`) e redige credenciais no preview de stdout (`execStdoutPreview`, `dag-executor.ts:3300`).

Pela sua convenção, isto se anota e não se conserta aqui. Mas conta antes de qualquer item que leve outputs a stages `tool`.

---

## 3. O que NÃO trazer e por quê

| Item do Archon | Por que não |
|---|---|
| `evidence_policy` (presença de `evidence.json`) | Mais fraco que a trava atual do Corvex, que exige leitura registrada por evidência (`internal/gate/store.go:97-104`, `internal/gate/reading.go:177-191`). Trazer seria regressão disfarçada de feature. |
| `gate-green` confiando no `red_cause` declarado pelo agente | O agente declara a própria absolvição e o script só checa resumo não vazio (`gate-green.ts`). Conflita com a trava de evidência. Ver o item 12 para a versão determinística. |
| Teto de custo por nó (`maxBudgetUsd`) como mecanismo principal | O Archon só repassa ao SDK do Claude e não tem teto por run. O comentário em `dag-executor.ts:8345` diz que `max_parallel` "bounds concurrency, not total spend". O Corvex já tem teto por run e por task, com preview e confirmação (`internal/step/ai_task.go:298-315`, `cmd/run_gate.go:51`). Manter o do Corvex. |
| MCP, skills e plugins declaráveis em qualquer nó | Generalizar `mcp:` por nó abriria MCP para Reviewer e gates, quebrando a custódia por papel. Trazer só a parte da *verificação* (item 2). |
| `persist_session` (sessão do provider entre runs) | Contamina a independência do Reviewer e de gates inferenciais, e acumula contexto, ou seja, custo. O próprio Archon documenta o "Cost caveat" e casos de sessão não restaurável (`authoring-workflows.md:1096-1135`). |
| `on_reject` com IA de retrabalho automática | Deprecated no próprio Archon (`dag-node.ts:604-612`). A substituição é "decisão + `when:`" (item 8). |
| Banco SQLite/Postgres, servidor, multiusuário/RBAC | O Corvex é deliberadamente baseado em arquivos, sem daemon e sem RBAC (`internal/gate/gate.go:21-27`). Custo G com ganho que não se aplica a uma CLI de um operador. |
| Adapters de chat (Slack, Telegram, Discord) e router de chat | Custo G. Ao escolher o workflow por IA, o router tira o determinismo do ponto de entrada. Notificação de gate pode ir num hook `post-*` existente (`internal/hooks/hooks.go:16-24`). |
| Triggers (schedule e webhooks com host, admission e overlap) | Exigem processo residente e um modelo de admissão que o Corvex não tem. Um cron externo chamando `corvex run` cobre o caso. |
| Multi-provider (Codex, Pi, Copilot, OpenCode) | O próprio Archon deprecou dois por falta de dono. Abriria matriz de capacidades e custódia por provider. A A/B do Corvex entre modelos Claude já existe (`internal/step/abrun.go`). Reavaliar só se a A/B precisar comparar vendors. |
| Hooks YAML com resposta estática do SDK por nó | Claude-only. `security.disallowed_tools` já cobre o caso típico de negar uma ferramenta. |
| `fan_out.join: first_success` (corrida) | O próprio Archon rejeitou: acopla o destino de filhos independentes (`dag-node.ts:880-885`). |
| Container overlay com write-back gate | Interessante, mas é para projetos-pasta sem git. O Corvex trabalha em repositório git com commit por task, que já dá o ponto de revisão. Custo G. |
| Telemetria PostHog e builder visual | Fora do escopo. |

---

## 4. Afirmação de que estou menos seguro

**O item 2 pressupõe que o Reviewer, o Planner e os gates do Corvex herdam hoje MCP do projeto ou do usuário.** O fato verificado:

- `grep -rn "strict-mcp" internal cmd` não retorna nada, então o Corvex não passa `--strict-mcp-config` a nenhum papel.

O que não verifiquei:

- se o `claude -p`, no modo e nas flags que o Corvex usa, de fato carrega o `.mcp.json` do projeto ou a config MCP do usuário sem aprovação interativa.

Se não carregar, o item 2 encolhe de "buraco de custódia" para "defesa em profundidade e teste de controle". O teste de controle positivo descrito no item 2 responde a dúvida em minutos.

**Incerteza secundária, sobre o Archon:** não achei limite de paralelismo por camada do DAG, só caps por provider, por conversa e por fan-out. O `dag-executor.ts` tem 12 mil linhas e não li o `runLayers` inteiro.

## Fontes externas consultadas

- [Issues abertas do Archon](https://github.com/coleam00/Archon/issues)
- [Better Stack: Archon YAML workflow engine](https://betterstack.com/community/guides/ai/archon-ai.md)
- [MindStudio: what is Archon](https://mindstudio.ai/blog/what-is-archon-harness-builder-ai-coding). Só serviu para confirmar a identidade do projeto. Nenhuma das fontes menciona `kind: tool` ou `kind: test`.
