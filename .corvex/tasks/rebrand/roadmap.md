# Rebrand do corvex — roadmap

> Documento vivo, fechado em 4 rodadas de grill conversacional (não `corvex grill`).
> Branch: `rebrand`. Insumos: `f0-fluxo.md`, `ui-prompt.md`, canvas de design (turno 2, 2a–2h).

## Tese

Corvex deixa de ser "orquestrador de IA que decompõe spec em DAG" e passa a ser:

**Runner de workflow declarativo sobre tools tipadas, com gates aplicados por custódia de
credencial e exit code, estado que sobrevive à sessão, e uma UI que é onde o gate humano
acontece de verdade.**

O modelo de referência do *fluxo* é a `autopilot` do smartcare (que provou o formato dos
gates). O corvex **não** a substitui e não se acopla a ela — fará algo parecido, sem se
resumir a isso.

## O que só o corvex pode dar

O runtime da autopilot é a sessão do Claude Code. Isso impõe limites que mais JS não resolve:

1. **Estado além da sessão** — `resumeFromRunId` é por-sessão; resume entre sessões queimou
   669k tokens na 59459. Corvex: checkpoint git + ledger em disco.
2. **Teto de custo aplicado pelo runner** — mata o run. Workflow só tem orçamento se o
   humano digitar "+500k".
3. **Contabilidade entre runs** — custo por natureza de step, qual gate mais reprova,
   quantas interrupções por feature.
4. **Re-execução de um estágio isolado** sem refazer o resto.
5. **Gate que o agente não contorna** — consentimento na UI, credencial no runner.

**Não é diferencial:** determinismo via tools tipadas. Como MCP, a autopilot ganha o mesmo
sem uma linha de Go — por isso o catálogo tem valor independente do corvex.

---

## Arquitetura alvo

### 1. O substantivo muda: `project` → `run`

Hoje tudo é escopado por *project* (um diretório em `.corvex/tasks/`), o repo é implícito
(cwd) e **não existe identidade de run** — o ledger vai appendando e dois runs do mesmo
projeto são indistinguíveis. A UI inteira é escopada por `run_8f21`, com repo explícito.

Consequência: **toda a superfície de comando e o schema do ledger mudam.**

### 2. Camada de operações (pré-condição da UI)

A regra de paridade (2h: a UI registra suas ações como comandos de CLI, "disparado pela UI")
exige que UI e CLI chamem **a mesma função**. Hoje isso é impossível:

| Problema | Evidência |
|---|---|
| Lógica dentro da camada cobra | `cmd/` = 3.475 linhas; `cmd/validate.go` (556) tem `setupValidationStack`, `startDBContainer`, `startApp` |
| God object | `Orchestrator.Run` = 412 linhas (`orchestrator.go:146-558`); `executeTask` = 317 |
| God struct | `Orchestrator` com 20+ campos misturando config, colaboradores, estado e 3 "mirror of Options.X" |
| God package | `internal/orchestrator` = 4.009 linhas |

Alvo: **`internal/ops`** — operações puras, sem cobra, sem stdout. `cmd/` vira parsing de
flag + formatação. O servidor HTTP chama as mesmas funções. Nenhuma regra de negócio em
`cmd/` ou em handler.

### 3. Contrato de evidência (o que faz a tela de gate funcionar)

A 2b mostra *"migration aplicada e revertida no shadow db"*, *"suíte 312/312"*, *"plano de
query muda de seq scan para index scan"*. Isso é evidência **de domínio**, produzida por
steps — mas a tela é do runner, que precisa ser genérico (nada de Azure/SmartCare no binário).

Sem contrato, ou a tela é genérica e pobre, ou o binário vira específico. O step devolve:

```yaml
evidence:
  - kind: test_output | verdict | diff | sql | link
    label: "Testes"
    status: pass | warn | fail
    required_reading: true        # arma a trava do botão "Aprovar"
    content: "..."
```

O runner só transporta e renderiza. `required_reading` é o que produz o *"falta ler o
veredito"* da 2b. **Decisão de F2 (taxonomia), não de F8.**

### 4. Sem daemon

`corvex ui` sobe um servidor que **spawna processos de run desacoplados** e os acompanha
pelo registro em disco. O run continua processo independente; a UI é supervisora, não dona.
Preserva o CLI e faz o "em paralelo" sair de graça.
**Auth desde a v1:** token gerado no `corvex ui`, embutido na URL. Servidor em localhost que
executa comando com credencial precisa disso (CSRF / DNS rebinding). Retrofitar é pior.

---

## Superfície de comandos

Ponto de partida (não o alvo final — a F3 é o passo de redesenho).
Coluna do meio = como aparece no canvas; à direita = esquema substantivo→verbo proposto.

| Hoje | No canvas | Alvo (F3) | Tela |
|---|---|---|---|
| `run <project>` | `run <recipe> --repo --branch` | `run start <recipe> --repo --branch` | 2c, 2h |
| `list` (lista *projetos*) | `runs --since 2d` | `run list --since 2d` | 2e |
| `inspect <project>` | `show <runId>` | `run show <id>` | 2f |
| — | `watch <runId>` | `run watch <id>` | 2d |
| — (interno do TUI) | `pause` / `kill` | `run pause\|kill <id>` | 2d |
| — | `gates` | `gate list` | 2a |
| — (`human-gate` reservado, nunca implementado) | `gate approve <id> --step S` | `gate approve\|reject <id> --step S` | 2b, 2h |
| — | `answer <id> --choice C` | `gate answer <id> --choice C` | 2g, 2h |
| `recipe <name>` | — | `recipe list\|show\|validate` | — |
| — | — | `ui` | todas |

**Colisão a corrigir:** `run` (executa) × `runs` (lista) diferem por uma letra e fazem coisas
opostas. Com autocomplete piora — TAB devolve duas opções quase idênticas, uma destrutiva.
É o motivo de a F3 existir como passo próprio.

**Aliases fixos e poucos** (só caminho quente): `corvex run <recipe>` = `run start`;
`corvex gates` = `gate list`.
**Sobrevivem:** `init`, `doctor`, `plan`, `grill`, `validate`, `version`.
**Absorvidos:** `status` e `logs` → `run show` / `run watch`.
**Compatibilidade:** `run <project>` continua válido (o caminho `spec.md` ficou na rodada 3),
resolvendo para um recipe implícito.

---

## Decidido

| Tema | Decisão |
|---|---|
| **Público** | Só o Giovanni por enquanto; Yandeh depois. **Nada de Azure/SmartCare no binário, desde a linha 1.** Sem RBAC/multi-usuário agora. |
| **Rebrand** | Reposicionamento, não renome. Continua `corvex`. |
| **Autopilot** | Independente, no repo dela. Zero acoplamento. Sem ponte de ledger. |
| **Origem do DAG** | Board **ou** `spec.md`. Os dois vivem → `planner`/`griller`/`anchor` sobrevivem. |
| **UI** | **Executa**: dispara run, aprova gate, responde pergunta, pausa, mata. Fora do Go (Go serve JSON), embutida no binário via `embed.FS`. Localhost, um usuário, token. |
| **Paridade** | Todo botão tem equivalente em CLI; nenhum caminho existe só na UI. A UI registra suas ações como comandos (2h). |
| **Sandbox** | **Rebaixado, não deletado.** Reenquadrado como *ambiente de execução do run* — a dor "rodar com Postgres" é a mesma máquina de container. |
| **Multi-repo** | (i) N repos com runs independentes numa UI = **fazer**; (iii) run em A que **lê** B e C = **fazer** (`WorktreeConfig.Link` é o pé); (ii) um run **atravessando** repos = **adiar** (quebra checkpoint, worktree, DAG). |
| **Índice global** | `~/.corvex/runs.jsonl`, fora dos repos. Ledger detalhado continua em cada `.corvex/`. |
| **Grill** | Sem regra de parada = **bug ativo** (28 perguntas / 2 semanas no `host-inheritance`). Ganha teto. |

## Taxonomia

**Steps** — 4 tipos:

| Tipo | Modo | Gate natural |
|---|---|---|
| `code` | inferencial | review independente |
| `tool` | computacional | exit code |
| `test` | computacional | exit code |
| `repro` | computacional **temporal** | reproduz antes, não reproduz depois |

**Gates** — 4 naturezas (a recipe hoje só modela `task` e `command`):
`computacional` (exit code) · `inferencial` (agente independente, nunca o autor) ·
`humano` (bloqueia até aprovação) · `política` (contador, teto, nível de branch).

**Primitivo faltante — fan-out dinâmico.** A recipe é DAG **estático** de stages
(`Stages []Stage` + `DependsOn`, tudo no YAML). A forma da autopilot precisa de N itens
descobertos em runtime, cada um pelo mesmo pipeline, com dependência **entre itens**
(ondas), depois consolidação. É o que separa recipe de pipeline de verdade.

---

## Como executar este roadmap

Escrito para ser executado de uma vez, de forma autônoma. As travas abaixo valem **mais**
que qualquer instrução de prompt — se houver conflito, a que está aqui ganha.

### Marcos

| Marco | Fases | O que é |
|---|---|---|
| **v2** | F-1 → F4 | Corvex com identidade de run, gates de verdade e superfície de comando nova. Utilizável e soltável. |
| **v3** | F5 → F7 | Telemetria, ambiente por run, servidor + UI. |
| **flutuam** | F8, F9 | F8 não toca em Go e pode rodar a qualquer momento. F9 depende da F8. |

### Invariantes — conferidos ao FIM DE CADA FASE, sem exceção

1. `go test ./...` verde.
2. Cobertura de `./cmd/` **não cai** abaixo do piso estabelecido na F-1 (60%).
3. `corvex run <project>` (caminho `spec.md` legado) continua funcionando ponta a ponta.
4. **Zero domínio no binário.** Domínio mora em recipe e tools no repo do usuário.
   ```sh
   grep -rn -iE "azure|smartcare|yandeh|dev\.azure\.com" --include="*.go" cmd internal \
     | grep -vE ':[0-9]+:[[:space:]]*//' | grep -v "_test.go"
   ```
   → deve ser **vazio**. (Comentários que citam Azure como exemplo são permitidos; fixture
   de teste também. Hoje retorna **uma** linha — `internal/orchestrator/worker.go:179`,
   o `"AZURE_"` cravado em `authEnvPrefixes` — que a F0 remove.)
5. Nenhum segredo, token ou credencial em log, ledger ou commit.
6. Um commit por fase, no mínimo. Mensagem descrevendo o que mudou e o que **não** mudou.

### Autonomia por fase

| Fase | Autonomia | Motivo |
|---|---|---|
| F-1 | ✅ autônoma | aditiva, não muda comportamento |
| F0 | ✅ autônoma | arquitetura **prescrita** — é mover código para destino nomeado |
| F1 | ✅ autônoma | schema + índice, mecânico |
| F2 | ⚠️ **gate ao fim** | taxonomia, contrato de evidência e fan-out dinâmico são design novo. Ao terminar, escreva o desenho adotado em `f2-design.md` e **pare**. |
| F3 | ⚠️ **gate ao fim** | entrega **documento**, zero Go. Ao terminar, pare — a tabela de comandos precisa de aprovação antes de virar código. |
| F4 | ✅ autônoma | implementa a F3 já aprovada; fan-out sobre comandos independentes |
| F5 | ✅ autônoma | mecânica |
| F6 | ✅ autônoma | reusa `internal/stack` da F0 |
| F7 | ⚠️ **contrato primeiro** | fixe API + design system num único passo, **depois** paralelize as telas. Oito telas em paralelo sem contrato = oito estilos. |
| F8 | ✅ autônoma | N tools independentes — é onde fan-out mais paga |
| F9 | ⚠️ **gate ao fim** | segurança. Custódia de credencial e `DisallowedTools` não se aprovam sozinhos. |

### Condições de parada (PARE, não decida)

Ao bater em qualquer uma: escreva o caso em `.corvex/tasks/rebrand/BLOCKED.md`
(o que aconteceu, o que você faria, quais são as opções) e **interrompa a fase**.
Não escolha por conta.

- Um golden test da F-1 **precisa** mudar durante a F0 → o comportamento mudou.
- Um invariante acima falha e o conserto exige mudar escopo.
- Uma fase exige uma das **decisões em aberto** abaixo.
- Custo acumulado passa do teto que você recebeu no goal.
- A mesma fase falhou 2 vezes seguidas (política de 2 tentativas — a mesma da autopilot).

### Decisões em aberto — NÃO invente

1. **`repro` sem consumidor.** O tipo foi aprovado, mas a `pilot-incident` (único fluxo que
   o usava) foi removida em 13/08. Implemente o tipo; **não** ressuscite eixo de incidente.
2. **Herança de MCP do host.** O corvex hoje exige redeclarar `mcp_servers` no `config.yaml`
   em vez de herdar do Claude Code. **Não está decidido.** Não implemente herança.
3. **`gate answer` × `answer` no topo.** A escolha atual é do assistente, não confirmada
   pelo usuário. Mantenha `gate answer`; se a F3 concluir o contrário, registre e pare.
4. **Aliases.** Só os dois nomeados (`run <recipe>`, `gates`). Não crie outros.
5. **Multi-repo (ii)** — um run **atravessando** repos está **adiado**. Não implemente.
6. Qualquer coisa que ponha Azure/SmartCare/Yandeh no binário → viola o invariante 4.

---

## Fases

### F-1 — Rede de caracterização (bloqueia a F0)
**Por quê:** `cmd/` está em **36% de cobertura** e é o pacote que a F0 esvazia (3.475 linhas).
Refatorar isso sem rede é o cenário clássico de "compila, testa verde, comportamento mudou".
O aceite da F0 (*"zero mudança de comportamento"*) não é verificável sem isto.

Testes de caracterização (golden) sobre o comportamento **observável** de cada comando que
vai se mover — exit code, forma da saída, arquivos escritos, efeito no `tasks.md`/ledger:
`run` (incl. `--dry-run`, `--task`, tree sujo), `plan`, `validate` (stack up/down, teardown
por process-group), `doctor`, `inspect`, `status`, `logs`, `start`.

- **Aceite mecânico:**
  - `go test ./cmd/ -cover` ≥ **60%** (baseline atual: 36%).
  - `go test ./... ` verde.
  - Cada comando acima tem ao menos um teste golden que falha se a saída mudar.
- **Autonomia:** ✅ autônoma. É trabalho aditivo, não muda comportamento.

### F0 — Limpeza e modularização
**Por quê:** pré-condição física da UI, não estética. A regra de paridade (2h) exige que UI
e CLI chamem a mesma função, e hoje `setupValidationStack` só é alcançável pelo cobra.

**A arquitetura alvo está prescrita abaixo de propósito** — para que esta fase seja
mecânica (mover código para destinos nomeados) em vez de exigir julgamento sobre onde
passam as costuras. Não invente uma decomposição diferente.

| Destino | O que vai pra lá | Origem verificada |
|---|---|---|
| `internal/ops/` | Operações: sem cobra, sem stdout, sem `os.Exit`. Uma função por operação, retornando valor + erro. É o que o CLI **e** o servidor HTTP (F7) chamam. | toda a regra hoje em `cmd/` |
| `internal/stack/` | `setupValidationStack`, `startDBContainer`, `startApp`, `portInUse`, `loadEnvFileVars`, `cleanupFn` | `cmd/validate.go:296-463` |
| `internal/wizard/` | `runValidateWizard`, `inferValidateConfig`, `manualValidateWizard`, `wizardPrompt`, `confirmUncertain`, `manualOverride`, `applyFieldOverride`, `printDetected` | `cmd/validate.go:106-283` |
| `internal/orchestrator/` (fica) | **só o escalonador**: laço de `Run`, ondas, pausa/skip, drain de comandos, emissão de evento | `orchestrator.go:146-558` |
| `internal/step/` | execução de um step: worker, review, command-stage, human-gate, escalonamento de erro | `executeTask` (`orchestrator.go:558-875`), `runCommandStage`, `runHumanGate`, `runInvestigation` |
| `internal/planning/` | `planner.go`, `griller.go`, `brainstormer.go`, `advisor.go`, `configurer.go` | movidos inteiros de `internal/orchestrator/` |
| `cmd/` | só cobra: flags, args, chamada de `ops`, formatação da saída | — |

Além disso:

- Quebrar `Orchestrator.Run` (412 linhas) e `executeTask` (317) em funções nomeadas.
  A struct `Orchestrator` perde os três campos "mirror of Options.X" (lê de `Options`).
- **Tirar o domínio da allowlist de env.** `authEnvPrefixes` (`internal/orchestrator/worker.go:166-180`)
  tem `"AZURE_"` cravado em Go — única violação atual do invariante 4. O padrão está errado
  por si: adicionar credencial nova não pode exigir recompilar o binário. Mover para
  `sandbox.env_allowlist` no `config.yaml`, **unida** com os defaults genéricos que ficam em
  Go (`ANTHROPIC_`, `AWS_`, `OPENAI_`, `CORVEX_`). Config **acrescenta**, nunca remove default.
  *(Achado preservado do grill arquivado do `host-inheritance`.)*

- **Aceite mecânico** (cada item é um comando):
  - `go test ./...` verde e `./cmd/` **não abaixo** dos 60% da F-1.
  - `grep -rl "spf13/cobra" internal/` → **vazio**. Cobra só existe em `cmd/`.
  - `grep -rnE "fmt\.Print|os\.Exit" internal/ops/` → **vazio**.
  - Nenhum arquivo em `cmd/` acima de **150 linhas**.
  - Nenhum arquivo em `internal/` acima de **400 linhas**.
  - `internal/ops/` não importa `internal/tui`.
  - Os golden tests da F-1 passam **sem alteração**. Se um precisar mudar, o
    comportamento mudou → **PARE** (ver Condições de parada).
- **Autonomia:** ✅ autônoma **enquanto os golden tests da F-1 não precisarem mudar**.

### F1 — Identidade de run
- `run_id`, `repo`, `recipe` no ledger; registro de run em disco (pid, início, status) +
  heartbeat; índice global em `~/.corvex/runs.jsonl`.
- **Aceite:** dois runs do mesmo projeto são distinguíveis; um segundo processo consegue
  listar o que está vivo.

### F2 — Taxonomia, gates e evidência
- `kind: code|tool|test|repro` na recipe; 4 naturezas de gate; contrato de evidência
  (`required_reading`); **fan-out dinâmico com ondas**.
- `human-gate` implementado de verdade (hoje é `kind` reservado).
- **Aceite:** um recipe expressa a forma da autopilot; um gate humano bloqueia o processo
  e é liberado por `corvex gate approve`.

### F3 — Redesenho da superfície de CLI (só desenho — entrega documento)
Passo próprio, sem código. Hoje os comandos não têm gramática: verbo e substantivo
misturados (`init`, `plan`, `run`, `grill`, `start`, `status`, `list`, `logs`, `inspect`)
e **cinco** formas de olhar a mesma coisa (`status`, `logs`, `inspect`, + `show`, `watch`).

**Defeito concreto a corrigir:** `corvex run` e `corvex runs` diferem por uma letra e fazem
coisas opostas — um executa, o outro lista. Com autocomplete piora: TAB devolve duas opções
quase idênticas, uma destrutiva.

- **Esquema alvo: substantivo → verbo** (convenção `gh`/`kubectl`):
  `run start|list|show|watch|pause|kill` · `gate list|approve|reject|answer` ·
  `recipe list|show|validate`.
- **Aliases fixos e poucos**, só para caminho quente: `corvex run <recipe>` = `run start`;
  `corvex gates` = `gate list`. Alias para tudo reproduz o erro do Docker
  (`docker run` × `docker container run`).
- **Regra de crescimento:** poucos substantivos (`run`, `gate`, `recipe`), verbos
  convencionais, **todo o resto é flag**. Comando novo justifica por que não é flag.
- Tudo em **inglês**.
- **Custo aceito:** os rodapés do canvas mudam (`corvex gates` → `corvex gate list`).
- **Aceite:** documento com a tabela completa antiga→nova, aliases, e o mapeamento de cada
  tela do canvas para o comando equivalente. Nenhuma linha de Go.

### F4 — Implementar comandos + inteligência
- Implementar o desenho da F3. `run <project>` continua funcionando.
- **Completion dinâmica** (determinística, cobra): `run show <TAB>` completa com run ids
  reais do índice da F1; `gate approve <TAB>` completa **só com gates pendentes**.
- **Sugestão do próximo comando por estado** — o canvas já faz isso em todo rodapé
  (*"⌘K abre o terminal aqui, já com `corvex gates`"*). Run terminou `parked` → imprime
  `→ corvex gate approve run_8f21 --step migrate-stg`. Determinístico, sem token.
- **`corvex` sem argumento mostra estado, não help** — a 2a no terminal. Quem digita
  `corvex` está perguntando "e aí?", não "quais são as flags".
- **Fora de escopo:** linguagem natural → comando (`corvex do "aprova o gate"`). Mais lento
  que TAB, custa token, e erra em silêncio numa superfície que aprova migration. As duas
  formas acima cobrem ~90% do problema de graça. Reavaliar depois da F7.
- **Aceite:** todo comando do canvas existe e funciona no terminal, antes de haver UI.

### F5 — Telemetria da UI
- Persistir eventos `tool_use` (início/fim, não chunks — `ledger.go:6` os descarta hoje).
- Relógio de espera humana separado do relógio do run (2g: *"relógio parado há 6m"*).
- Estado de leitura de evidência (persistente — fechar a aba não zera).
- Custo por natureza (worker / reviewer / determinístico — a barra da 2f).
- **Aceite:** todo dado das telas 2a–2h existe em disco.

### F6 — Ambiente de execução por run
- "simples" × "com Postgres" — reusa `internal/stack` (F0) + o docker do sandbox.

### F7 — Servidor + UI
- `corvex ui`: HTTP sobre `internal/ops`, token de auth, SPA embutida.
- Telas 2a–2h. ⌘K registrando ação de UI como comando.
- **Aceite:** disparar run, aprovar gate e responder pergunta pela UI, com o log da 2h
  batendo com o que o `corvex runs` mostra.

### F8 — Catálogo de tools (paralelizável desde já — não toca em Go)
- Operações da `az` como tools tipadas (84 regras em prosa → assinaturas).
- **Funções puras hoje escritas em prosa** (achado da F0): `ship` branch→target,
  `ship` merge-por-nível (a regra de segurança mais importante do fluxo), `release`
  versão↔tag↔ambiente.
- MCPs existentes; possivelmente AWS CLI.
- Validar **na autopilot** antes de o corvex consumir.

### F9 — Custódia de credencial
- Credencial no runner, nunca no ambiente do worker. `DisallowedTools` fechando o caminho
  cru (senão o catálogo é sugestão, não fronteira).
- Preflight de dependência declarada antes do primeiro token (cicatriz #1).

**Backlog paralelo:** teto no `griller.go`; contrato de contexto cross-repo.

---

## Riscos

1. **Gate que vira carimbo.** É a cicatriz #4 na versão humana. A 2b/1d já força leitura,
   mas isso é atrito, não prova. Mitigação real: **sensor sobre os sensores** — medir
   latência de aprovação e taxa de reprovação por gate. Gate aprovado em 4s, 100% das
   vezes, é teatro: automatiza ou deleta.
2. **A UI nasce vazia.** Sem ponte com a autopilot, só há **um** `activity.jsonl` no repo
   inteiro (em `feat/absorb-feedback`, não mergeada). A F7 só é útil depois da F4.
3. **Snooze é vazamento.** Se a UI ganhar "adiar", o número de adiados precisa ficar
   visível — senão é assim que gate morre em silêncio.
4. **Escopo.** F0–F7 é reescrita grande de um binário que hoje funciona. Cada fase precisa
   manter `go test ./...` verde e o `run <project>` atual funcionando.

## Fontes

- `martinfowler.com/articles/harness-engineering.html` — guides (feedforward) × sensors
  (feedback), computacional × inferencial.
- `.corvex/tasks/rebrand/f0-fluxo.md` — espinha do fluxo, catálogo de 12 gates reais.
- `.corvex/tasks/rebrand/ui-prompt.md` — briefing de design.
- Canvas de design, turno 2 (2a painel · 2b gate · 2c disparar · 2d ao vivo · 2e histórico ·
  2f encerrado · 2g pergunta · 2h ⌘K).
- `~/projects/yandeh/smartcare/.claude/skills/` — `az`, `autopilot`, `refine`, `ship`,
  `release`, `dev-maestri`.
