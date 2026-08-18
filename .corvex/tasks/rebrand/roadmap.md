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
2. **Cobertura não cai** abaixo do piso estabelecido na F-1 (60%) — medida sobre a regra,
   onde ela estiver:
   ```sh
   go test ./cmd/ -coverpkg=./cmd/...,./internal/ops/...,./internal/stack/...,./internal/wizard/... -cover
   ```
   *Por que não é mais só `./cmd/`:* o piso original media `./cmd/` porque era lá que a regra
   morava. A F0 mudou isso — `cmd/` virou casca fina (84.2%, mas de pouca coisa) e a regra
   desceu para `internal/ops` (nasceu com 24.1%). Medir só `./cmd/` a partir daqui é medir o
   lugar que esvaziou: passaria verde com `ops` inteiro descoberto. A rede golden **atravessa**
   a fronteira de pacote (provado por mutação em `internal/ops`), então o número existe — só
   precisa do `-coverpkg` para aparecer. Reporte os dois números; o piso vale para o combinado.
3. `corvex run <project>` (caminho `spec.md` legado) continua funcionando ponta a ponta.
4. **Zero domínio no binário.** Domínio mora em recipe e tools no repo do usuário.
   ```sh
   # (a) fonte de produção
   grep -rn -iE "azure|smartcare|yandeh|dev\.azure\.com" --include="*.go" cmd internal \
     | grep -vE ':[0-9]+:[[:space:]]*//' | grep -v "_test.go"
   # (b) o binário de verdade — pega o que entra por go:embed
   go build -o /tmp/corvex-inv4 . && strings /tmp/corvex-inv4 | grep -iE "azure|smartcare|yandeh"
   ```
   → **os dois** devem ser vazios. (Comentário em `.go` citando Azure como exemplo é
   permitido; fixture de teste também. Comentário em arquivo **embedado** não é: ele vai
   para o binário.)
   *Por que (b) existe:* na F0 o `(a)` passou verde enquanto `strings corvex | grep Azure`
   achava uma linha — o `"AZURE_"` saiu de `authEnvPrefixes` em Go e voltou como exemplo
   comentado em `templates/config.yaml`, que entra no binário via `embed.FS`. Um invariante
   que só olha `*.go` é cego para `templates/`, e foi assim que o domínio voltou dentro da
   fase que o removeu. Corrigido em `9e15335`.
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

### F0 — Limpeza e modularização — ✅ CONCLUÍDA
**Concluída em `e6d06dc`** (`refactor(f0): cmd/ vira cobra puro, regra desce para
internal/ops`), sobre a rede da F-1 (`9d09810`). Waves anteriores: `a0886f5`, `68bf111`.

Estado no fecho — os dez invariantes conferidos um comando cada:

| # | Invariante | Estado |
|---|---|---|
| 1 | rede verde | `go test ./...`, `./cmd/ -count=2` e `./cmd/ -shuffle=on` verdes; `go build`/`go vet` limpos |
| 2 | cobertura de `cmd/` | **84.2%** (piso 60). `internal/ops/` nasceu com 24.1% |
| 3 | caminho legado | binário real em fixture real: `corvex run alpha --dry-run` sai **byte-idêntico** ao golden `run_dryrun` (mesmo md5), exit 0, stderr vazio, sem chamar IA |
| 4 | zero domínio | 0 linhas em fonte de produção |
| 5 | zero segredo | a `Entry` do ledger não tem campo de env — estruturalmente não dá para gravar credencial; a allowlist não é logada (nem por valor, nem por prefixo) |
| 6 | cobra só em `cmd/` | `grep -rl "spf13/cobra" internal/` → vazio |
| 7 | `ops` silencioso | `grep -rnE "fmt.Print\|os.Exit" internal/ops/` → vazio |
| 8 | `cmd/` ≤ 150 linhas | nenhuma fonte de produção acima (os seis violadores foram fatiados) |
| 9 | `internal/` ≤ 400 linhas | nenhuma fonte de produção acima |
| 10 | `ops` sem `tui` | `go list -deps ./internal/ops` sem `internal/tui` e sem cobra |

**Zero mudança de comportamento, provado e não assumido:** os 249 goldens são
byte-idênticos a um `git archive` de `9d09810` (conteúdo, nomes e quantidade); o conjunto
de labels que os testes referenciam é o mesmo e nenhum golden ficou órfão; os testes de
caracterização ficam em exatamente 82 asserts, 8 skips e 5 `t.Log`; **nenhuma função de
teste que existia em `9d09810` desapareceu** (asserts do repo subiram 1479 → 1515). Os 14
bugs congelados de `f-1-anomalias.md` foram movidos junto, byte a byte, não consertados —
truncagem e formatação ficaram em `cmd/` justamente por isso.

**Estado global da allowlist de env eliminado.** `activeEnvMu`, `activeEnvAllowlist`,
`SetActiveEnvAllowlist` e `ActiveEnvAllowlist` saíram de `internal/config`, e o publish
dentro de `config.Load` foi removido. A allowlist virou **campo de `Worker`**, propagado no
`clone()` e consumido em `collectAuthEnv`; `NewWorker` ganhou o parâmetro para que o
compilador force todo construtor a fornecê-lo, e os dois construtores leem do `Config` que
aquele run já carregou. O que chega ao sandbox passa a ser o que **o config daquele run**
declarou, não o que algum `Load` anterior no processo publicou — propriedade que o global
não conseguia oferecer, e que agora tem teste próprio
(`TestWorkerForwardsItsOwnAllowlistToSandbox`). Isto fecha também o invariante 4: o
`"AZURE_"` cravado em `authEnvPrefixes` não existe mais.

**Fica para depois** (não bloqueia F1; nenhum é violação de invariante):
- `internal/ops/doctor_env.go` ainda importa `internal/orchestrator` só por
  `orchestrator.RepoSkills(workDir)` — a camada de regra dependendo do executor.
  `RepoSkills` deveria descer para `ops` ou para um pacote `skills` próprio.
- `ops.LinkWorktreePaths` relata progresso pelo logger global, idêntico ao que
  `cmd/helpers.go` fazia em `9d09810` — preservado de propósito por LEI A. Trocar por
  `io.Writer`/callback é mudança de comportamento; é a dívida que a **F7** paga quando a
  operação passar a ser chamada por HTTP.
- Dívida de gofmt pré-existente em 7 arquivos — subconjunto estrito dos 10 de `68bf111`.
  A F0 não criou dívida nova e limpou 3; todo arquivo novo da fase está gofmt-limpo.

---

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
  - Nenhum arquivo em `cmd/` acima de **150 linhas**. **Decidido:** o limite vale para fonte
    de **produção**; arquivo de teste golden não conta (a rede da F-1 é deliberadamente
    verbosa e cortá-la para caber num limite de linha enfraqueceria a prova).
  - Nenhum arquivo em `internal/` acima de **400 linhas**. **Decidido:** o limite **não tem
    exceção** — inclui `internal/tui/model.go` e `internal/provider/claude/claude.go`, os dois
    candidatos naturais a "mas esse é especial"; ambos foram divididos na F0 (wave 1).
  - `internal/ops/` não importa `internal/tui`.
  - Os golden tests da F-1 passam **sem alteração**. Se um precisar mudar, o
    comportamento mudou → **PARE** (ver Condições de parada).
- **Autonomia:** ✅ autônoma **enquanto os golden tests da F-1 não precisarem mudar**.

### F1 — Identidade de run — ✅ CONCLUÍDA
**Concluída em `fe9fcae`** (`feat(f1): identidade de run — run_id/repo/recipe no ledger,
registro em disco com heartbeat, indice global`), sobre a F0 (`e6d06dc`) e a rede da F-1
(`9d09810`).

- `run_id` e `recipe` no ledger (o `repo` **saiu** — ver "Correção pós-fecho" no fim desta
  seção); registro de run em disco (pid, início, status) + heartbeat; índice global em
  `~/.corvex/runs.jsonl`.
- **Aceite:** dois runs do mesmo projeto são distinguíveis; um segundo processo consegue
  listar o que está vivo.

**Aceite provado com processos reais, não com chamada de função.** Binário construído,
fixture `spec.md` legado, `CORVEX_HOME` de scratch: dois `corvex run demo` (exit 0 nos dois)
deixaram `run_b70a` (11 linhas de ledger) e `run_69d7` (6 linhas), ids e pids distintos,
ambos `done`. Um terceiro run com stub bloqueante foi listado como `alive` por um **leitor
estrangeiro** — script Python que não compartilha uma linha de Go com o corvex, lendo
`runs.jsonl` + `.corvex/runs/*.json` e sondando com `kill(pid,0)` — enquanto o `ps`
confirmava o processo de pé; depois de `kill -9`, o mesmo leitor passou a dizer `dead`
com o `status` em disco **ainda em `running``. É essa a razão de liveness nunca poder ser
lida do campo `status`. O contrato que atravessa a fronteira de processo é o formato em
disco, não o pacote.

#### Decisões de desenho tomadas
| Decisão | Forma escolhida | Por quê |
|---|---|---|
| Formato do id | `run_` + 4 dígitos hex (`run_8f21`), de `crypto/rand`, gerador **injetável** (`run.IDFunc`) | curto o bastante para digitar de memória e caber em coluna de tabela; 4 dígitos colidem por aniversário perto de 300 ids, então largura vem com **oráculo de unicidade** — índice global + `O_EXCL` no caminho do record, que *reivindica* em vez de só checar |
| Onde mora o registro | um arquivo por run em `<repo>/.corvex/runs/<run_id>.json`, escrito temp+rename | um arquivo por run é o que torna o diretório seguro sob concorrência: dois runs nunca escrevem o mesmo path; rename atômico garante que um run morto no meio da escrita deixa o record anterior íntegro, nunca meio JSON. `.gitignore` com `*` na primeira escrita: pid + path absoluto desta máquina não vão para a história do usuário |
| Semântica de liveness | `status` terminal → `finished`; senão exige **pid vivo E heartbeat fresco**; `dead`/`stale`/`unknown` são estados próprios | os dois sinais falham em direções opostas: `status` é cego a SIGKILL (fica `running` para sempre), pid é cego a reuso de pid. Exigir os dois faz cada um cobrir o outro. Heartbeat de 10s, `stale` após 4 intervalos (3 batidas perdidas) — uma batida perdida é pausa de GC, não morte. **Risco aceito e escrito:** dentro da janela de 40s um pid reciclado é indistinguível; nada destrutivo pende desse bit (nunca matamos nem reescrevemos com base nele) |
| Forma do índice | `$CORVEX_HOME/runs.jsonl` (default `~/.corvex/runs.jsonl`), append-only, **snapshot inteiro por linha**, leitura consolida por `run_id` com última-linha-vence | append-only é suficiente *porque* cada linha é snapshot: o fim do run é uma segunda linha, nunca uma reescrita, então leitor e escritor nunca disputam e um crash no meio do append custa só a última linha. `O_APPEND` + um único `Write` é onde vive a atomicidade. O heartbeat **não** appenda (cresceria sem limite): frescor vem do overlay do record local. `0600` dentro de `0700` — o HOME é mais exposto que o ledger do repo |
| `recipe` no caminho legado | **vazio** (`omitempty`, chave ausente do JSON) | nome inventado é campo que mente: `recipe show <nome>` daria 404 ou — pior — resolveria uma recipe homônima sem relação com o run. Sentinela (`implicit`) quebraria o que o pacote vende ("grepar uma linha e entender"). A informação não se perde: `Record.Project` é campo próprio. E `recipe == ""` é exatamente o discriminador que a F2 vai querer ("quais runs não usaram recipe") |
| Onde nasce e morre o run | nasce em `ops.NewRunner` → `startIdentity`, **depois** de provider/sandbox/`--ab`; morre em `ops.Runner.Execute` | uma invocação recusada por `--ab` malformado não pode deixar registro `running` que ninguém fecha. `finalStatus` checa **ctx antes do erro**: Ctrl-C cancela o ctx e o escalonador devolve o erro do step interrompido — gravar `failed` culparia o trabalho pelo Ctrl-C do operador |
| Falha de registro | reportada (`IdentityErr`), **nunca** aborta o run | HOME read-only ou disco cheio não podem recusar trabalho que o usuário vai pagar. O modo degradado (linhas sem `run_id`) é o que já existe em disco |
| Identidade no ledger | em **toda** linha, não em linha de cabeçalho por run | dois runs do mesmo projeto appendam no mesmo arquivo concorrentemente, então ordem de linha não estabelece posse; cabeçalho truncado orfanaria tudo depois dele |
| `Summarize` | segue **cumulativo por projeto**; `SummarizeRun` é a visão por run | escopar ao run atual reintroduziria o bug do "$0.00 após resume". Contagem dupla já é impossível: métrica é chaveada por task e o último `PASSED` vence |

#### Estado no fecho — os dez invariantes, um comando cada
| # | Comando | Estado |
|---|---|---|
| 1 | `go test ./... -count=1` | **verde**, 20 pacotes. `./cmd/ -count=3` verde (121s), `./cmd/ -shuffle=on` verde, `go test ./... -race` **verde** |
| 2 | `go test ./cmd/ -coverpkg=./cmd/...,./internal/ops/...,./internal/stack/...,./internal/wizard/... -cover` | **86.2%** (piso 60; baseline F0 86.9% → −0.7pp). Com `./internal/run/...` no coverpkg: **82.0%** |
| 3 | binário real, fixture `spec.md` legado, dois `corvex run demo --plain --yes` | **exit 0** nos dois, `S01 passed . 2s . $0.04`, `+ done`; ledger/record/índice corretos |
| 4a | `grep -rn -iE "azure\|smartcare\|yandeh\|dev\.azure\.com" --include="*.go" cmd internal` (sem comentário, sem teste) | **vazio** |
| 4b | `strings <binário construído> \| grep -iE "azure\|smartcare\|yandeh"` | **0 matches** |
| 5 | segredos exportados no ambiente do run (`ANTHROPIC_API_KEY=sk-…`, canário), depois `grep` em `.corvex/runs/`, `activity.jsonl` e `runs.jsonl` | **limpo**; índice `-rw-------` em dir `drwx------`. A `Record` é struct fechada: não há forma para um segredo viajar |
| 6 | `grep -rl "spf13/cobra" internal/` | **vazio** |
| 7 | `grep -rnE "fmt\.Print\|os\.Exit" internal/ops/*.go` (sem testes) | **vazio** |
| 8 | maior fonte de produção em `cmd/` | **150** (`plain_renderer.go`, pré-existente); `run_render.go` 145, `run.go` 141 — teto respeitado |
| 9 | maior fonte de produção em `internal/` | **373** (`task/parser.go`); o maior arquivo novo da fase é `ops/run_identity.go` com 196 |
| 10 | `grep -rn "internal/tui" internal/ops/` | **vazio** |

#### LEI 1 — nenhum golden regravado
`git diff 217f17d -- cmd/testdata/golden/` é **vazio**: os 249 goldens da F-1 estão
byte-idênticos. Isso foi investigado, não celebrado. A razão é que `run_id` só alcança saída
comparada por golden via `inspect --task --json`, e o golden existente daquele comando é
construído por `fixture.AddLedger`, que abre o ledger com `activity.Identity{}` — logo
continua idêntico. **Uma rede que fica verde porque não alcança a mudança é lacuna, não
prova**, e foi fechada de duas formas:

1. **Normalizador `<RUN_ID>`** em `scrub()` (`cmd/characterize_test.go`), registrado
   **antes** do scrubber de hash (um id com 7+ dígitos hex seria comido como `<HASH>`).
   Normalizar > regravar: nenhum id concreto entra em golden, então nenhum golden fica flaky.
2. **Um golden novo**, `run_identity_inspect_task_json.txt` — `inspect --task --json`
   *depois de um run real*, com `run_id: "<RUN_ID>"` e (à época) `repo: "<TMP>"`, `recipe`
   ausente. Par deliberado com o golden pré-F1 do mesmo comando: um diz "run identificado
   ganha os campos", o outro diz "ledger sem identidade mantém os bytes exatos".
   *(As 5 linhas `repo` foram removidas deste golden na correção pós-fecho abaixo — o único
   golden regravado desde `9d09810`, e por deleção de campo, não por mudança de valor.)*

Sensibilidade provada por mutação (feita e desfeita, md5 conferido na volta):
`openLedger` voltando a `activity.Identity{}` → **4 testes de `cmd/` + e2e vermelhos, o
golden novo incluído**; `finalStatus` sempre `StatusDone` → vermelho nas **três** camadas
(`cmd/`, `internal/ops`, e2e); `hb.Stop()` removido → `1 goroutine(s) leaked by Execute`.

#### LEI 2 — os 14 bugs seguem congelados
Nenhum arquivo dos bugs foi tocado (`status_render.go`, `inspect*.go`, `validate.go`,
`plain_renderer.go`, `orchestrator/worker.go`, `recovery/`), e os dois que moram em
`cmd/run.go` sobreviveram ao diff: `--dry-run` continua retornando **antes** de olhar
`--task`, e `--force` continua silencioso. Conferido no código de hoje, não por fé:
`status_render.go:96` ainda corta `title[:maxTitleLen-1]` por byte, `inspect_format.go:52`
ainda corta `title[:49]`, `inspect_render.go:43` ainda usa `%-8s`, e `Setpgid` não existe
em nenhum lugar do repo (teardown segue matando só o filho direto).

#### LEI 4c — HOME real intocado, verificado ativamente
`~/.corvex` **não existia** antes e **não existe** depois de `go test ./...` inteiro (ida e
volta, incluindo a rodada com `-race`) — a melhor canária possível: qualquer escrita no HOME
real teria de criar o diretório. Além disso `cmd/`, `internal/ops/`, `internal/run/` e `e2e/`
ganharam `TestMain` com guarda dupla — `CORVEX_HOME` para scratch antes de qualquer teste, e
fingerprint do `~/.corvex/runs.jsonl` real antes/depois, falhando alto se mexeu — cada um com
um teste provando que a guarda não é vácua. `override por env var` é requisito, não
conveniência: um teste que suja o `~/.corvex` do usuário é defeito de produto.

#### Dívidas e riscos registrados
- **`hostOnce`/`hostName` em `internal/run/liveness.go` é var de pacote.** Memo write-once,
  guardado por `sync.Once`, de um fato imutável do processo, e ambos os consumidores
  (`Registry.Host`, `Resolver.Host`) têm override injetável — não é o modo de falha que a F0
  matou (a allowlist global carregava *configuração* e tornava o comportamento dependente de
  ordem). Fica registrado como a única var de pacote nova da fase.
- **Ordenação `Stop` antes de `SetStatus` não é discriminável por teste** (medido: mover o
  `Stop` para depois mantém verde — a janela tem nanossegundos). Continua sendo o código
  certo; a única consequência é `updated_at` sujo num run finalizado, e status terminal vence
  na liveness de qualquer forma. Está escrito no comentário de
  `TestExecuteLeavesNoHeartbeatBehind` para ninguém ler mais do que ele prova. **O vazamento
  de goroutine, que é a parte que quebra de verdade, É detectado.**
- **Caminho da TUI verificado só por leitura.** `isInteractive()` é falso sob pipe, então
  nenhum teste da rede passa por `runWithTUI`. O comportamento pré-existente ("run que falha
  sob TUI sai 0") foi preservado de propósito, e por isso `runRun` descarta o retorno de
  `Execute` e devolve só `tuiErr`. O receive do outcome é `select`+`default`: esperar ali
  transformaria `q` em travamento. Quando a F3/F4 decidir que run falho sob TUI deve sair
  ≠ 0, essa acrobacia desaparece.
- **`.corvex/runs/.gitignore` com `*` ignora a si mesmo**, então não é commitado. Funcional
  (os records são scratch da máquina que os gerou), mas vale saber antes de alguém depender
  do arquivo estar na história.
- **Reuso de pid dentro da janela de 40s** — analisado, aceito e documentado em
  `liveness.go`. Revisitar se o supervisor da F7 passar a agir automaticamente sobre liveness.
- **`gofmt -l e2e/corvex_test.go`** acusa 1 linha — **pré-existente em `217f17d`**, conferido
  contra `git show 217f17d:e2e/corvex_test.go`. Não formatada para não poluir o diff.
- **Para a F2:** `orchestrator.Options.Identity` é onde `Recipe` passa a ser preenchido;
  `run.StartOptions` já aceita `Recipe`. Um único call site (`ops.startIdentity`).
- **Para a F7:** a UI tem de spawnar runs desacoplados (reapados pelo init), não como filhos
  que ela nunca espera — senão ela mesma cria zumbis que respondem a sinal 0 e *parecem
  vivos*. É a armadilha que o teste de SIGKILL documenta (ele chama `cmd.Wait()` de propósito).

**LEI 3 respeitada:** `git diff 217f17d -- cmd/ | grep -E "cobra.Command|AddCommand"` é
vazio, e nenhuma flag nova. O aceite foi provado com **função + teste**, não com comando —
`corvex runs` continua sendo assunto da F3, que é gate humano.

#### Correção pós-fecho (auditoria adversarial): `repo` sai do ledger
**Defeito.** A F1 passou a gravar `repo` = **path absoluto** em toda linha de
`activity.jsonl` — e `activity.jsonl` é **commitado na prática**, pelo `auto_commit` do
próprio corvex. Prova no histórico deste repo:
`git log --all --diff-filter=A -- '.corvex/tasks/pilot-feedback/activity.jsonl'` → `101a40d
corvex: checkpoint S01`; e `git check-ignore .corvex/tasks/rebrand/activity.jsonl` não casa.
Reproduzido com o binário real (auto_commit ligado): o commit `corvex: checkpoint S01` levava
`{"…","repo":"/Users/<username>/…"}` — **username e layout de máquina publicados para quem
clona**. A própria F1 tinha esse raciocínio para o *registro* (`ensureRecordsIgnored` escreve
`.gitignore` com `*` porque "pid + path absoluto desta máquina não vão para a história do
usuário") e não o aplicou ao único arquivo que de fato entra na história.

**Decisão: o campo é removido, não ofuscado.** `run_id` fica (é o que torna dois runs no
mesmo arquivo distinguíveis, e não descreve a máquina); `recipe` fica (vocabulário do
usuário). `repo` sai, por três razões:
1. **Não compra nada para quem lê o arquivo.** O ledger já mora em
   `<repo>/.corvex/tasks/<projeto>/activity.jsonl`: dentro de um arquivo o valor é constante
   — zero bit de informação, repetido em toda linha.
2. **O argumento que a F1 deu a favor é o argumento contra.** "Uma linha colada num issue
   tem de ser legível sem o path" — colar uma linha num issue é exatamente o momento em que
   `/Users/<username>/…` não pode viajar junto.
3. **Nada se perde.** O path absoluto já está nos dois arquivos que nunca são commitados: o
   record por run em `<repo>/.corvex/runs/` (gitignored com `*`) e o índice global em
   `$CORVEX_HOME` (0600 em dir 0700). `run_id` é a chave de junção, então "em que diretório
   rodou o `run_ab12`?" continua respondível — por quem tem a máquina, que é o único leitor
   que deveria receber resposta.

**Hash e basename foram considerados e rejeitados:** nenhum leitor consegue voltar de um
hash para um path; o único caso de uso que justificariam (agrupar linhas de repos
diferentes) já é servido pelo índice global, que tem os paths de verdade; e campo em que
ninguém consegue agir é convite para o próximo alargar. A "contabilidade entre runs" que o
roadmap pede é *por `run_id`*, não por repo.

**Compatibilidade (três formas literais, fixadas em teste — `TestRead_AllThreeOnDiskLineShapesStayReadable`):**
linha pré-F1 (sem identidade), linha F1 **com `repo` absoluto** (já existe em disco, inclusive
no repo do usuário) e linha pós-correção. As três continuam legíveis, somáveis e endereçáveis
por `FilterByRun`. A chave `repo` passa a ser desconhecida e é ignorada por `encoding/json`;
o efeito colateral é bom: `inspect --json` re-serializa entries verbatim, então uma linha
antiga **deixa de republicar o path** ao ser lida pelo código novo. Linhas já em disco **não
são reescritas** — arquivo append-only já commitado não é nosso para reescrever, e o
`git log -p` mostraria o path de qualquer jeito.

**Tripwire.** `TestEntry_JSONKeysAreAnAllowlist` enumera por reflection as chaves JSON que
`activity.Entry` pode emitir contra uma allowlist com justificativa por chave. Campo novo no
ledger = teste vermelho = alguém tem de escrever por que aquele valor pode ser publicado. O
vazamento da F1 não chegou por um campo chamado "path"; chegou porque ninguém teve esse
momento.

**Varredura do resto da F1:** `repo` era o único campo novo que alcançava arquivo versionado.
`Entry.Message` é o outro vetor possível e está limpo hoje (`recovery` e `orchestrator` só
emitem contagens — "working tree clean", "3 uncommitted change(s)" —, nenhum
`Message: err.Error()` no repo); `host`, `pid` e `machine` só existem no record e no índice.

**Golden:** 1 regravado (`run_identity_inspect_task_json.txt`, −5 linhas `"repo": "<TMP>",`,
zero outras mudanças), pela única razão que justifica regravar: o campo deixou de existir no
schema. Os outros 248 seguem byte-idênticos.

**Canário do invariante 5, refeito com binário real e `auto_commit: true`:**
`ANTHROPIC_API_KEY=sk-ant-CANARY111` + 4 amigos exportados no ambiente do run → run exit 0,
ledger commitado em `corvex: checkpoint S01`, `grep -rn CANARY` em `.corvex/` e no
`CORVEX_HOME` **vazio**, `git grep CANARY` em toda a história **vazio**, e nenhum arquivo
rastreado contém o path do repo. Record e índice seguem com o path absoluto, `0600` em
`drwx------`.

#### Correção pós-fecho (auditoria adversarial): retenção, rotação e a janela cega
A mesma auditoria derrubou quatro coisas além do `repo` no ledger. Todas eram do mesmo
formato: a F1 tratou identidade como se o espaço de ids fosse infinito e o relógio, o
hostname e o pid fossem confiáveis. Nenhuma era teórica — cada uma foi reproduzida com
binário real.

| Decisão | Forma escolhida | Por quê |
|---|---|---|
| **Teto de 65.536 ids** | unicidade deixa de valer contra *todo id já anunciado* e passa a valer contra o conjunto **endereçável**; o índice é **rotacionado** (`retention.go`) | medido: a 94% de ocupação **13 de 20 runs** não conseguiam mintar id, e o ledger voltava calado à forma pré-F1 (linhas sem `run_id`). Largura do id é superfície de UI (F3), então o conserto é do outro lado. **Consequência aceita e escrita:** id **é reciclável** — `run show run_8f21` de um run que fechou mês passado pode não achar nada, ou achar um run novo com o mesmo id. O contrário é uma ferramenta que não minta id nenhum depois do run 65.536 |
| **Rotação, não reescrita** | `os.Rename` para `runs.jsonl.1` + re-append dos snapshots endereçáveis pelo mesmo caminho atômico de todos | reescrever no lugar (tmp+rename) tem janela em que uma linha appendada por fd já aberto cai num inode que vai ser unlinkado: run anunciado e silenciosamente esquecido. `O_APPEND` + um `Write` é a atomicidade que permite 8 processos sem lock. **Uma geração de archive** — archive sem limite anula o motivo de limitar o arquivo |
| **Falha de registro deixa de ser não-fatal** | run **não começa** se não houver id (`ErrIDSpaceExhausted`, sentinela) | invertido de propósito em relação ao fecho da F1 ("reportada, nunca aborta"). Uma linha de ledger que ninguém consegue atribuir é pior que um run que se recusou a começar — e aparece exatamente quando alguém está tentando entender o que deu errado. Verificado no binário: espaço cheio de não-terminais → **exit 1**, mensagem nomeando o knob, **zero** record `*.json`, ledger nem criado |
| **Não-terminal também envelhece** | `addressable` exige `now - freshness < retention * 8` para linha não-terminal (112 dias no default) | sem teto, `worthRotating` recusa rotacionar índice todo endereçável e a máquina fica **permanentemente incapaz de mintar sem knob nenhum** — medido: 65.536 órfãos `running`, índice de 14,5 MB, todo run recusado, e a mensagem mandava ajustar `CORVEX_RUN_RETENTION`, que não tocava em nada disso. Derivado da retenção em vez de duração absoluta **para a mensagem deixar de ser mentira**: verificado que `CORVEX_RUN_RETENTION=1h` resolve o estado doente. Fator generoso porque a idade de uma linha é a idade da última **mudança de status**, não do último sinal de vida — o índice não recebe heartbeat. Fronteira medida: 1/3/7/14/30/60/90/111 dias mantêm o id, 113 solta |
| **Oráculo lê o archive, sempre** | `claimIndexEntries` lê `runs.jsonl` **e** `runs.jsonl.1`, live primeiro | entre o rename e o fim do carry-forward `runs.jsonl` não existe, e o índice global é o **único** oráculo que vê run de outro repositório (`O_EXCL` do record é por repo por construção). Medido: **207 ms** de cegueira por rotação, 2 de 39 claims na janela pegando id de run **vivo**. Fecha **por construção**, não estreitando janela: presença em arquivo nunca era o predicado — `addressable` é, e é aplicado à união. **Ordem é load-bearing:** archive primeiro deixaria uma rotação caber entre as duas leituras e a segunda devolveria o live novo e vazio |
| **Rotação termina a anterior antes de começar a sua** | `resumeCarryForward` imediatamente antes do rename; se não consegue ler o archive, **não renomeia** | o furo real de "ler o archive": com uma geração guardada, o rename **destrói** o archive atual. Inofensivo depois de rotação completa (o carry-forward já devolveu tudo endereçável ao live); **destrutivo** depois de uma morta entre o rename e o carry-forward, quando o conjunto endereçável existe só no archive. Preferido a lock `.rotating` para leitores: lock em leitor tornaria o start do run dependente de um lock desenhado para ser não-bloqueante |
| **`canceling` no record** | status próprio, escrito por um watcher no momento em que o contexto é cancelado — não quando o corpo desenrola | o teardown que mata só o filho direto é **bug congelado**; um neto segurando o stdout mantém o corpo bloqueado indefinidamente. Sem estado para isso, um supervisor lê heartbeat fresco + pid vivo e conclui `alive` — o que a auditoria mediu por 20s e desistiu. É sobre **sinal**, não sobre gate: nada a ver com `StatusParked`, e `LivenessCanceling` vence frescor porque "alguém pediu para parar" não deixa de ser verdade quando o heartbeat envelhece |
| **Relógio clampado nos dois lados** | `freshness` mais de `FutureSkew` (5s) à frente de `now` → `stale`, não `alive` | `now - freshness > staleAfter` é metade do teste: timestamp no futuro dá diferença negativa, nunca cruza o limiar, e o run fica `alive` **para sempre** num pid reciclado. Não precisa de falha exótica — NTP corrigindo drift, snapshot de VM, RTC errado. Verificado: `+2s` → `alive`, `+1h` → `stale`, `-41s` → `stale` |
| **Identidade de máquina** | `$CORVEX_HOME/machine-id`, número aleatório sem relação com hardware/hostname/usuário; hostname normalizado é o fallback | pid de outra máquina não significa nada aqui, e o hostname muda (`box.local` → `box-2.local` em rede). Ausência de identidade **não é evidência de localidade**: record com host e machine vazios agora dá `unknown`, não `alive` — a auditoria viu `host= -> alive` num pid reciclado. Verificado: `box.local` vs `box-2.local` com o mesmo machine-id → `alive`; ambos vazios com pid reciclado → `unknown` |
| **O que o ledger grava** | `run_id` + `recipe`, e nada que descreva a máquina | ver a seção anterior. `host`, `pid`, `machine` e o path absoluto vivem só no record (gitignored com `*`) e no índice (`0600` em `0700`), que nunca entram na história do usuário |

**Não consertado de propósito, e registrado:** a soma de custo agregado depende da ordem de
iteração de um map (`aggregate` em `internal/activity/ledger.go`) — anomalia de BACKLOG em
`f-1-anomalias.md`, porque já era não-determinística antes da F1, nenhum golden a observa, e
ordenar a redução muda comportamento fora do escopo da F1. Enquanto isso **nenhum teste
compara custo agregado com `==`**: `sameCost` (tolerância 1e-9) e um guarda que falha
deterministicamente se alguém trocar o helper por igualdade exata.

**Incoerência nomeada, não escondida:** `ReadIndex` (usada por `Resolver.List`/`Get`) continua
lendo **só** o live — é a visão de **LISTAGEM**, e durante a janela de rotação um `run show`
pode responder "não achei" para um run que o oráculo considera endereçável. Levá-la à união
mudaria semântica de listagem e arrisca golden. Os dois papéis estão nomeados nos docs
(`ReadIndex` = listagem, `claimIndexEntries` = oráculo) em vez de implícitos.

#### Como cada conserto foi provado (auditoria adversarial, com controle positivo)
Um verde só vale se o experimento **sabe ficar vermelho**. Cada linha abaixo foi
reproduzida antes e depois, e as duas mais caras têm controle positivo explícito.

| Achado | Repro (antes) | Prova (depois) |
|---|---|---|
| Teto de ids, terminais antigos | espaço todo anunciado, run recusado, ledger volta a linha sem `run_id` | **binário real**: 65.536 ids anunciados (14,6 MB), o run 65.537 **começa** (`run_003e`), **11/11** linhas de ledger com `run_id` |
| Teto de ids, não-terminais | idem, mas sem saída — nenhum knob resolvia | **binário real**: **exit 1**, erro nomeando `CORVEX_RUN_RETENTION`, **zero** record `*.json`, ledger **nem criado**, índice inalterado, git limpo |
| Máquina bricada por órfãos | 65.536 órfãos `running`, índice de 17,9 MB que não rotaciona, todo run recusado | **binário real**: 65.000 órfãos de 2 anos → run **começa** e o índice rotaciona (14,5 MB → 281 bytes). Órfãos de **1 dia** → recusa (correto: podem estar vivos), e `CORVEX_RUN_RETENTION=1h` **resolve** — a mensagem deixou de ser mentira |
| Limiar não mata run longo legítimo | — | fronteira medida com a política default: **1/3/7/14/30/60/90/111 dias mantêm o id, 113 solta** (112 = 14 × 8) |
| Falso `alive` após Ctrl-C | run fica `running` com `updated_at` avançando por +2/5/10/20s; só fecha quando alguém mata o neto na mão | **binário real**: SIGINT, leitor de **segundo processo** em +0.3/1/2/5/10/20s → **nunca `alive`**, sempre `canceling`, com o corvex **ainda de pé** e o neto órfão vivo com **ppid=1** nas seis amostras (bug congelado intacto) |
| Relógio sem clamp | `updated_at` no futuro → `alive` para sempre num pid reciclado | `+2s` → `alive`, `+1h` → `stale`, `-41s` → `stale`, `-5s` → `alive` (controle) |
| Hostname instável | `host=` vazio → `alive` num pid reciclado | `box.local` vs `box-2.local` com o mesmo machine-id → `alive`; host **e** machine vazios com pid reciclado → `unknown`; machine-ids diferentes → `unknown` |
| `repo` no ledger | commit `corvex: checkpoint S01` levando `/Users/<username>/…` | **binário real com `auto_commit: true`**: o **blob commitado** tem zero `/Users`, zero path do repo, zero hostname/username; chaves JSON = allowlist fechada; `.corvex/runs/` gitignored com `*`; repo ainda resolvível pelo `run_id` |
| Flake de float | suite **vermelha em ~3 de 10** rodadas (`0.44999999999999996` vs `0.45`) | **0 falhas em 40** rodadas do repro original; `./internal/activity/ -count=50` ok. **Controle positivo:** trocando `sameCost` por `==`, o guarda novo fica vermelho **8/8** e o flake original volta a aparecer **1/8** — a não-determinância de produção **não** foi consertada às escondidas |
| Janela cega da rotação | 207 ms por rotação; 2 de 39 claims na janela pegando id de run **vivo**; a 99,6% de ocupação, 41 de 41 | experimento independente a **11% de ocupação** (7.200 runs vivos cross-repo), rotação contínua, **4 processos reais** claimando em repositórios próprios: **1.952 claims, ZERO colisões**, 243 deles com o live abaixo de 256 KiB (menor observado **438 bytes** — o fundo da janela). **Controle positivo:** revertendo o oráculo para ler só o live, **5.606 de 7.456 claims (75%)** pegaram id de run vivo |
| Furo de "uma geração de archive" | rename destrói o archive que guarda o conjunto endereçável de uma rotação morta | **25 ciclos de SIGKILL** no rotator, archive sobrescrito repetidamente, live reconstruído do zero: **0 ids perdidos, 0 mintados** em todos os ciclos. Lock abandonado é limpo depois de 5 min (verificado: 4,0 MB → 130 KB) |
| LOST/TORN não regrediu | garantia da leva 1 | **8 processos appenders reais** durante rotações contínuas: **LOST=0, TORN=0** em 20.320 linhas |
| Guarda da goroutine morde | guarda antigo passa `context.Background()` (`Done()` nil), goroutine nunca criada | **controle positivo:** corpo de `stopWatching` virando no-op → os dois testes novos vermelhos **3/3**, guarda antigo **verde** (era cego, agora está provado que era) |

**Invariantes hoje** (não os do fecho da F1, que ficam acima como registro histórico):
`go test ./... -count=1` verde **20 de 20** rodadas consecutivas; `-race` limpo;
`-shuffle=on` limpo; `./cmd/ -count=2` ok · **86.1%** no coverpkg do roadmap (piso 60),
`internal/run` cobre a si mesma **90.3%**, `internal/activity` **88.9%** · 4a: só 3
comentários pré-existentes usando "Azure DevOps" como **exemplo** de integração; 4b:
`strings` do binário **0 matches** · 6: cobra só em `cmd/` (nem `main.go` importa) ·
8: **150** (`plain_renderer.go`, pré-existente); 9: **373** (`task/parser.go`),
`retention.go` 341 · asserts **1997 → 2239** (+242) e **zero** `t.Skip` removido — os 4
novos são guardas de plataforma (`windows`, `root`, `-short`, "sem hostname"), nenhum
desliga assertion no caminho normal.

#### Dívida registrada para a F2
- **`StatusParked` continua sem produtor nem consumidor.** A constante existe e o `record.go`
  diz explicitamente que **não** é o mesmo eixo que `canceling`: `parked` é gate humano
  (alguém tem de aprovar), `canceling` é sinal (alguém pediu para parar). Quem implementar o
  `human-gate` da F2 escolhe se `parked` ganha liveness própria ou se vira só um status;
  hoje `Resolver.Liveness` o trata como qualquer não-terminal (pid + heartbeat decidem), que é
  a resposta certa para "o processo está de pé esperando gente".
- **Liveness de run que sobrevive à sessão.** Hoje um run é `alive` porque o **pid** existe e
  o heartbeat é fresco — os dois são fatos do processo que começou o run. Quando a F2/F7
  tiver run que sobrevive à sessão que o criou (ou que roda em container/sandbox), pid deixa
  de ser a pergunta certa e `machine` deixa de ser suficiente: um run em container tem pid de
  outro namespace. É o ponto em que `ProcessProbe` precisa de uma segunda implementação, e é
  por isso que ela já é injetável.
- **Reuso de id é observável pela UI.** `run show <id>` pode achar um run diferente do que o
  usuário lembra. A F3, que desenha a superfície, é quem decide se o id fica com 4 dígitos
  (e a UI passa a mostrar `started_at` junto) ou se cresce.
- **`staleNonTerminalFactor = 8` é julgamento, não medição.** Não há dado sobre distribuição
  de duração de run real nesta ferramenta. Se existir caso de uso de run de meses, 8× é
  apertado; se runs nunca passam de dias, o estado doente demora demais a se resolver.
  Derivado de `RetentionEnv` justamente para dar alavanca ao operador hoje. Revisar quando
  houver telemetria (F5).
- **Archive ilegível recusa todo run da máquina.** Assimetria deliberada (`runs.jsonl.1`
  regular mas sem permissão de leitura → o oráculo perdeu metade da evidência e recusa;
  path que não é arquivo regular → tolera, porque nenhuma rotação teve sucesso ali e o live
  está completo). O custo é que um `chmod` errado no archive vira outage até alguém mover o
  arquivo — o erro nomeia "archive" exatamente para isso ser acionável.
- **Ressurreição de snapshot pelo `resumeCarryForward`** (achado desta auditoria, não
  bloqueante): se uma rotação interrompida deixou a linha não-terminal de um run no archive e
  o run grava seu `done` no live **entre** a leitura do live e o append do resume, o snapshot
  antigo volta depois do novo e a consolidação lê `running`. O erro é **conservador em todos
  os caminhos** — o id fica retido 8× mais tempo, nunca é solto cedo — e `Resolver.List`
  corrige a linha pelo overlay do record local, que é mais fresco. Requer rotação
  interrompida **mais** a janela; anotado aqui em vez de consertado numa terceira leva.

### F2 — Taxonomia, gates e evidência — ✅ CONCLUÍDA (aguardando gate humano)
- `kind: code|tool|test|repro` na recipe; 4 naturezas de gate; contrato de evidência
  (`required_reading`); **fan-out dinâmico com ondas**.
- `human-gate` implementado de verdade (era `kind` reservado que **matava** o run).
- **Aceite:** um recipe expressa a forma da autopilot; um gate humano bloqueia o processo
  e é liberado por `corvex gate approve`.

**Desenho e registro de fecho em `f2-design.md`** (§§1–16 desenho aprovado, §17 como foi
construído). Aceite provado com **dois processos reais** em `e2e/gate_test.go`: o run parka,
um segundo processo o vê em `corvex gate list`, `gate approve` sem `--ack` é recusado, e com
`--ack` o run destrava e sai exit 0.

Duas decisões que precisaram de aprovação e foram dadas: implementar
`gate list|show|approve|reject` na F2 em vez de antecipar a F3 (a decisão em aberto #3 já
fixara `gate` como substantivo), e **abrir o `headingRe`** — contrariando a política de
congelar bug, porque fan-out precisa mintar id e mintar sobre um parser que come em silêncio
o que não entende é construir sobre a falha.

Invariantes no fecho: rede verde com `-race` e `-shuffle=on`; **85,1%** no coverpkg (piso 60,
F1 fechou 86,1%); 4a/4b/6/7/10 em zero; 8 em 150 e 9 em 370; 1 golden regravado
(`harness_root_help`, +1 linha) e 8 novos; os 14 bugs congelados intactos.

Dívidas registradas na §16 e no fim da §17 — nenhuma bloqueante. `StatusParked` e
`Identity.Recipe`, as duas dívidas que a F1 deixou para esta fase, estão fechadas.

### F3 — Redesenho da superfície de CLI — ✅ CONCLUÍDA (só desenho — entrega documento)
**Entregue em `.corvex/tasks/rebrand/f3-cli.md`**: gramática de 3 substantivos
(`run`/`gate`/`recipe`), tabela completa antiga→nova cobrindo **todo** comando de hoje
(inclusive `reset`, `review` e o `recipe <name>` que compila — os três que a tabela deste
roadmap não mencionava), 13 decisões numeradas com custo aceito, mapeamento das 8 telas do
canvas, e a ordem de execução da F4 com aceite mecânico. Zero linha de Go.

Três decisões que mudam comportamento e estão declaradas lá: `reset` vira `run retry --step`
(passa a gastar dinheiro, herda a confirmação de custo), `review` é absorvido por `gate list`
(a única que muda **fluxo**, por isso é o último item da F4), e run falho sob TUI passa a sair
≠ 0 (fecha dívida da F1). `gate show` **não** é renomeado. `run pause` fica fora da F4 com o
motivo escrito: falta o arquivo de controle e o ponto de leitura na barreira de onda.

A decisão que protege a rede: comando legado continua **byte-idêntico sob pipe**, escondido do
help, com aviso de depreciação só em TTY — porque os goldens da F-1 capturam stdout **e**
stderr no mesmo transcript (`characterize_test.go:591`). Golden novo para comando novo é
entrega; golden legado que mude é vazamento → PARE.

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

### F4 — Implementar comandos + inteligência — ✅ CONCLUÍDA
**Entregue em 4 ondas** (`79d00a3`, `8025cb8`, `f876906`, `7a96e1e`); registro de
fecho em `f4-registro.md`. Toda a tabela da F3 existe e responde no terminal:
`run start|list|show|watch|retry|kill`, `recipe list|show|validate|compile`,
`gate list|show|approve|reject` cobrindo gate humano **e** escalation, completion
dinâmica, sugestão do próximo comando, e `corvex` sem argumento mostrando estado.

Aceite provado com **binário real e processos separados** (`e2e/run_surface_test.go`):
a invocação legada `corvex run demo` (caminho `spec.md`) roda ponta a ponta e o run
que ela deixou é endereçável por `run show <id>` **de fora do repositório**.

Três divergências do desenho, todas registradas: `run kill` espera o heartbeat do
próprio run antes de sinalizar (fecha a janela de reuso de pid que a F1 mandou
revisitar quando algo destrutivo dependesse dela); `--no-recompile` foi acrescentado
porque a D4 só tinha a saída destrutiva; e a sugestão de comando desconhecido foi
reimplementada para enxergar comando escondido.

Goldens: **4 regravados** (todos help/vazio, por mudança declarada de superfície) e
**32 novos**. Nenhum golden de comportamento mudou — o legado imprime os mesmos bytes
sob pipe. A D7 (aviso só em TTY) deixou de ser retórica: torná-lo incondicional deixa
**69 testes vermelhos**.

Invariantes: 1 verde (incl. `-race` e `-shuffle=on`); 2 em **83,2%** (piso 60);
4a/4b/6/7/10 em zero; 8 em 150 e 9 em 370.

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

### F5 — Telemetria da UI — ✅ CONCLUÍDA
**Registro em `f5-registro.md`.** Executada como fan-out de 4 agentes em worktrees próprios,
integrada por cherry-pick na ordem de dependência.

**O achado que define a fase:** `Entry.Phase` existia desde antes da F1 e **nunca ninguém
escreveu nele** — todo custo em todo ledger em disco está sem atribuição. A barra da 2f não
era agregação faltando, era dado que nunca foi gravado. Idem `tool_use`, que o `emit`
descartava junto com os chunks de texto.

Agora existem em disco: fase por evento, início/fim/duração de ferramenta (só o **nome**,
nunca o input — o arquivo é commitado), espera humana por gate, marca de leitura de evidência
persistente (`corvex gate ack`), e agregação por fase/ferramenta. `run show` e a UI mostram a
barra por natureza e o relógio humano separado do relógio do run.

**Defeito achado por duas frentes independentes, registrado e não consertado:**
`task_complete` carrega worker+reviewer num número só, então a barra sub-reporta `review`.
Desenrolar na leitura é impossível (o `review_result` não carrega `attempt`); o conserto é do
emissor e muda o formato do ledger.

- Persistir eventos `tool_use` (início/fim, não chunks — `ledger.go:6` os descarta hoje).
- Relógio de espera humana separado do relógio do run (2g: *"relógio parado há 6m"*).
- Estado de leitura de evidência (persistente — fechar a aba não zera).
- Custo por natureza (worker / reviewer / determinístico — a barra da 2f).
- **Aceite:** todo dado das telas 2a–2h existe em disco.

### F6 — Ambiente de execução por run — ✅ CONCLUÍDA
`corvex run start <x> --env simple|stack`. `stack` sobe **o mesmo ambiente que o
`corvex validate` já subia** (container de banco, migrations, app, health) pela mesma
função `internal/stack.Setup` e pelo mesmo bloco `validate:` do config — a F6 não
construiu um segundo mecanismo, ela deixou um **run** pedir o que a validação já pedia.

Três decisões, com o custo escrito:
- **Nome desconhecido é erro, não fallback.** Cair para `simple` em silêncio rodaria a
  suíte que precisa de Postgres sem Postgres e a culpa cairia nos testes.
- **O ambiente sobe dentro do `Execute`, não no `NewRunner`.** Montar um run não pode
  subir container: `--dry-run`, flag recusada e projeto inexistente passam por lá.
  Falha ao subir encerra o run **antes do primeiro token**, e o record diz `failed`
  com o motivo.
- **Teardown roda uma vez, em todo caminho, inclusive pânico**, e **antes** da escrita
  do status terminal — quem lê `done` não está mais segurando container. Duas vezes
  seria `docker rm` num container que outro run pode ter acabado de criar.

O ambiente vai para o **record** (scratch da máquina, gitignored), não para o ledger:
"que containers esta máquina subiu" é fato da máquina. `run show` mostra a linha só
quando não é o default. Costura injetável (`RunRequest.StackUp`) para o ciclo de vida
ser testável sem docker; controle positivo: teardown vira no-op → 2 vermelhos.

- "simples" × "com Postgres" — reusa `internal/stack` (F0) + o docker do sandbox.

### F7 — Servidor + UI — ✅ CONCLUÍDA
**Registro em `f7-registro.md`.** Duas levas na ordem que o roadmap exige: contrato primeiro
(`9861029`), telas depois (`7cf19eb`). Handler tem três linhas — decodifica, chama `ops`,
codifica — e as formas na rede são os tipos de `ops` **verbatim**, os mesmos do `--json`:
sem DTO, porque DTO é onde as duas superfícies começariam a discordar sobre o que é um run.

Auth desde a linha 1, com três checagens (token, `Host` de loopback contra DNS rebinding,
`SameSite=Strict`). Run disparado é **desacoplado** (`Setsid`+`Release`), que é o que faz
"fechar a UI e o run continua" ser verdade.

Defeito achado pelo próprio teste: o log de paridade gravava `gate approved` (veredito) onde
vai o verbo do CLI (`approve`) — linha que parecia paridade e não era executável.

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

### F9 — Custódia de credencial — ⚠️ MECANISMO ENTREGUE, DEFAULT AGUARDA VOCÊ
**`f9-custodia.md`.** Entregues com default idêntico ao de hoje (nada quebra):
`security.runner_only_env` (nomes exatos que o runner guarda, vencendo qualquer prefixo da
allowlist), `security.disallowed_tools` (config **acrescenta e nunca encurta** o bloqueio
embutido — é o que separa catálogo de sugestão) e `requires:` na recipe com preflight
**antes da prévia de custo**, que é a cicatriz #1 do roadmap.

**A decisão que sobra é a sua:** virar o default para "worker não recebe credencial".
Recomendação escrita no documento — **esperar a F8**, porque sem catálogo de tools o step não
tem como pedir a operação em vez da credencial, e trocar "segredo exposto" por "fluxo
bloqueado" faz o usuário sob pressão desligar a proteção.

- Credencial no runner, nunca no ambiente do worker. `DisallowedTools` fechando o caminho
  cru (senão o catálogo é sugestão, não fronteira).
- Preflight de dependência declarada antes do primeiro token (cicatriz #1).

**Backlog paralelo:** ~~teto no `griller.go`~~ ✅ **feito** — a regra de parada agora é
**cumulativa e lida do disco** (`--max-questions`, default 12), porque a falha medida foi
cross-sessão: 28 perguntas em duas semanas, nenhuma planejada, e **cada sessão respeitou o
teto de 50 iterações** que já existia — o teto contava o laço, e o que escapou foi o
projeto. `0` restaura o comportamento antigo, que é o bug. O número 12 é julgamento, não
medição, e é por isso que o knob existe. · contrato de contexto cross-repo (aberto).

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
