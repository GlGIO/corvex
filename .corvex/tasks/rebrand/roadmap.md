# Rebrand do corvex — roadmap

> Documento vivo. Fechado por rodadas de grill conversacional (não `corvex grill`).
> Branch: `rebrand`. Status: **rodada 1 aberta**.

## Tese

Corvex deixa de ser "orquestrador de IA que decompõe spec em DAG" e passa a ser:

**Runner de workflow declarativo sobre um catálogo de tools tipadas, com gates
aplicados por custódia de credencial e exit code, e estado que sobrevive à sessão.**

O modelo de referência é o `pilot-feature` do smartcare — ele já provou o formato
dos gates. O corvex não o substitui: absorve o que a sessão do Claude Code não
consegue segurar.

## O que só o corvex pode dar (o pilot não pode, estruturalmente)

O runtime do pilot é a sessão do Claude Code. Isso impõe limites que mais JS não resolve:

1. **Estado além da sessão** — `resumeFromRunId` é por-sessão; resume entre sessões
   queimou 669k tokens na 59459 (`pilot-feature/SKILL.md:94`). Teto real no tamanho
   de qualquer automação. Corvex: checkpoint git + `activity.jsonl` em disco.
2. **Teto de custo aplicado pelo runner** — `max_cost_usd` mata o run. O pilot só
   tem orçamento se o humano digitar "+500k".
3. **Contabilidade entre runs** — custo por tipo de task, qual gate mais reprova,
   A/B de modelo com estatística acumulada.
4. **Re-execução de um estágio isolado** — `--task S03`, sem refazer o resto.
5. *(fase Yandeh)* **Rodar sem sessão** — CI, cron, evento de PR.

### O que NÃO é diferencial do corvex
**Determinismo via tools tipadas.** Se o catálogo for MCP, o pilot ganha o mesmo
benefício sem uma linha de Go. Por isso o catálogo vem primeiro e vale mesmo que
o corvex seja descontinuado depois.

## Decidido

- **Público:** só o Giovanni por enquanto; Yandeh depois.
  → Consequência dura desde a linha 1: **nada de Azure/SmartCare dentro do binário.**
    Domínio mora em recipe + tools no repo do usuário. Retrofitar genericidade é caro.
  → Não construir agora: RBAC, multi-usuário, dashboard.
- **Rebrand = reposicionamento**, não renome. Continua `corvex`. Sem fase de naming.
- **Sandbox: rebaixado, não deletado.** O default já é `local` (`config.go:296`) — ou
  seja, hoje ele já não está no caminho. Deletar não resolve nenhum problema de acesso
  atual, custa um refactor grande (docker/nix/devcontainer/factory/worktree/config/testes)
  e queima a opção de rodar sem humano num servidor. Ação: documentar como experimental,
  parar de investir. Reavaliar só depois da F2.
- **Não usar `corvex grill` pra fechar este roadmap.** Evidência: no `host-inheritance`
  ele rodou 28 perguntas em 2 dias, nunca convergiu, e a partir da 26 passou a auditar
  as próprias respostas. Ele não tem regra de parada (`griller.go`). Grill entra depois,
  por fase, com teto — e o teto é candidato a feature do próprio corvex.

## As dores reais (rodada 1 — do usuário, não inferidas)

1. **Não vejo o que já rodei.** Sem visão de todos os runs, sem histórico fácil.
2. **Não vejo o que está rodando agora** — nem quais tools estão vivas no momento.
3. **Não consigo ser determinístico em algumas integrações.**
4. **Não consigo escolher o ambiente do run** — rodar simples, ou subindo um Postgres.
5. **Steps não são tipados**: código, ferramenta, teste e reprodução são a mesma coisa
   pro runner. *(Dita de passagem, mas é a espinha — ver abaixo.)*
6. **Visão de board**: clicar numa feature, ver se foi refinada, aprovar. *(Ver escopo.)*

### Estado do que já existe (verificado)
- `inspect` já lê o ledger e dá timeline/duração/retries/custo por task; `Summarize`
  agrega; `list`/`status`/`logs` existem. **Dor 1 é quase só falta de índice global.**
- `liveness.go` rastreia task viva **só em memória, no processo do run** — outro processo
  (a UI) não enxerga. Falta registro de run em disco + heartbeat. **(Dor 2a)**
- `ledger.go:6` descarta eventos de alto volume de propósito — e junto foram as
  **chamadas de tool**, que é exatamente o dado da dor 2b. Persistir `tool_use`
  início/fim (não os chunks) resolve.
- `validate.database` (`type/image/migrate_command/env`) já sobe banco. **Dor 4 é meio
  caminho andado — e a outra metade é o código docker do sandbox.**

## Taxonomia de step — a espinha

`kind` da recipe, generalizado, mapeando direto no Fowler:

| Tipo de step | Modo | Gate natural |
|---|---|---|
| `tool` | computacional | exit code |
| `test` | computacional | exit code |
| `repro` | computacional | reproduz ou não |
| `code` | **inferencial** | review independente |

Tipar o step é o que permite gate por tipo, política de retry por tipo, custo por tipo e
**renderização por tipo na UI**. Sem isso a UI é um visualizador de log.

**Consequência de ordem: taxonomia antes da UI.**

## Escopo da UI (decidido)

Fora do Go (Go serve JSON), com três restrições:
1. **v1 read-only** — só ledger + registro de runs. Sem escrita, sem board, sem auth.
2. **Local**: `corvex ui` abre localhost. Sem servidor, sem deploy.
3. **Embutida no binário** (`embed.FS`) apesar do fonte separado — importa pra fase Yandeh.

**Não construir clone do board.** O Azure DevOps ganha sempre em listar/filtrar/editar
work item, e "discutir uma story" é trabalho do Claude Code. O que o board **não** sabe é
o estado dos seus runs → construir a **caixa de entrada de gates**: migration parkada,
PR com 🔴, `human-gate` aguardando, story em `needsFix`. Pequeno, específico, insubstituível.

Escrita (aprovar gate pela UI) fica na v2, apoiada na custódia de credencial: a UI aprova,
o corvex executa com a credencial que o agente nunca teve.

## Sandbox — reenquadrado

Deletar está **descartado**: a dor 4 ("subir um Postgres") é a mesma máquina de container.
O que muda é o nome e o propósito: deixa de ser "sandbox de isolamento" e vira
**ambiente de execução do run**, escolhível por run.

## Fases

| # | Fase | Entrega | Depende de |
|---|------|---------|-----------|
| **F0** | Extrair a espinha do fluxo | Fluxo real de dev a partir de `refine → pilot-* → ship → release` | — |
| **F1** | Taxonomia de step | `kind: code\|tool\|test\|repro` na recipe + gate por tipo | F0 |
| **F2** | Telemetria pra UI | Registro de run em disco + heartbeat; persistir eventos `tool_use`; índice global de runs | F1 |
| **F3** | UI v1 read-only | Histórico de runs, run ao vivo, tools ao vivo, caixa de gates | F2 |
| **F4** | Catálogo de tools MCP | Operações determinísticas do `/az`, validadas **no pilot** primeiro | F0 |
| **F5** | Ambiente de execução por run | "simples" × "com Postgres" — reaproveita o código docker | F1 |
| **F6** | Corvex consome o catálogo | Custódia de credencial; negar o caminho cru (`DisallowedTools`) | F4 |

F4 é paralelizável com F1–F3 (não toca em Go; valida no pilot).

## Decidido na rodada 2

- **Integrações da F4:** Azure (`az`) por enquanto; MCPs existentes; possivelmente AWS CLI.
  → A F0 achou mais: `ship` e `release` têm **três tabelas de decisão determinísticas
    escritas em prosa** (branch→target, merge por nível, versão↔tag↔ambiente). São `switch`.
    A F4 não é "wrapper de az", é **externalizar as funções puras do fluxo**.
- **`repro` é tipo próprio** (gate temporal: reproduz antes, não reproduz depois).
  ⚠️ Mas a `pilot-incident`, único consumidor, foi **removida em 13/08**. Sem consumidor,
  o tipo fica especificado e não implementado até o eixo de incidente voltar.
- **Multi-repo:** ver seção abaixo.

## Multi-repo — três leituras, custos diferentes

| Leitura | O que é | Custo | Decisão |
|---|---|---|---|
| **(i)** N repos, runs independentes, **uma UI** | só falta índice global de runs | baixo | **fazer na F2/F3** |
| **(ii)** um run **atravessando** N repos | quebra checkpoint (commita em qual?), worktree, DAG, validate | alto | **adiar** |
| **(iii)** run no repo A que **lê** B e C | contrato de contexto: caminhos que o agente pode ler | médio | **fazer** — `WorktreeConfig.Link` (`config.go:38`) já é o pé |

"Contexto dos 3 juntos" quase sempre significa (iii), não (ii). Índice global mora em
`~/.corvex/runs.jsonl` (fora dos repos), com o ledger detalhado continuando em cada `.corvex/`.

## Achados da F0 que mudam o roadmap

1. **Gates têm 4 naturezas, não 2:** computacional, inferencial, **humano** e **política**
   (contador/teto/nível de branch). A recipe modela só `task` e `command` → a F1 precisa
   dos quatro.
2. **A F4 cresceu:** além das 84 regras da `az`, `ship` §Branching, `ship` passo 8 (merge
   por nível — a regra de segurança mais importante do fluxo) e `release` §1-2 são funções
   puras interpretadas por modelo.
3. **O gate de preflight de tools foi removido** da `autopilot` em 13/08 — era a mitigação
   da cicatriz #1 (85k tokens numa MCP inexistente). Vira requisito: preflight de
   dependência declarada é responsabilidade do runner, não de prosa numa skill.

## Decidido na rodada 3

- **Autopilot e corvex são independentes.** A autopilot fica no repo dela, sem relação com
  o corvex. O corvex fará *algo parecido* — mas não se resume a isso.
  → **Consequência:** o corvex precisa da FORMA da autopilot como primitivo genérico
    (fan-out sobre itens de runtime, ondas topológicas, consolidação, gate), **não** de
    integração com Azure. O domínio entra por tools/adapters, nunca no binário.
- **DAG vem do board OU do `spec.md`.** Os dois caminhos vivem.
  → **Consequência:** `planner.go`/`griller.go`/`anchor.go` sobrevivem, e a ausência de
    regra de parada no grill deixa de ser legado e vira **bug ativo** (travou o
    `host-inheritance` com 28 perguntas em 2 semanas). Entra no backlog com teto.
- **Sem ponte com a autopilot.** A UI só mostra dados do corvex.
  → **Consequência dura:** existe **um único `activity.jsonl` no repo inteiro**, em
    `feat/absorb-feedback` (não mergeada). `main` e `rebrand` não têm nenhum. A UI nasce
    vazia e só serve depois que o corvex executar trabalho real. **F3 vai pro fim.**

## Primitivo faltante: fan-out dinâmico

A recipe hoje é **DAG estático de stages** (`Stages []Stage` + `DependsOn []string`, tudo
conhecido no YAML). A forma da autopilot precisa de **fan-out dinâmico**: N itens
descobertos em runtime, cada um pelo mesmo pipeline, com dependência **entre itens**
(ondas topológicas), depois consolidação.

Não se expressa no schema atual. É o que separa "recipe" de pipeline de verdade.

## Fases (reordenadas — rodada 3)

| # | Fase | Entrega | Depende de |
|---|------|---------|-----------|
| ~~F0~~ | ~~Espinha do fluxo~~ | ✅ `f0-fluxo.md` | — |
| **F1** | Taxonomia + fan-out | steps `code\|tool\|test\|repro`; gates nas 4 naturezas (computacional/inferencial/humano/política); **fan-out dinâmico com ondas** | F0 |
| **F2** | Funções puras como tools | `ship` branch→target, merge-por-nível, `release` versão↔tag↔ambiente, ops da `az`. **Não toca em Go** — paga no autopilot antes | F0 |
| **F3** | Ambiente de execução por run | "simples" × "com Postgres" — reaproveita o docker do sandbox, reenquadrado | F1 |
| **F4** | Corvex roda trabalho real | custódia de credencial; `DisallowedTools`; preflight de dependência declarada (cicatriz #1) | F1, F2, F3 |
| **F5** | Telemetria | registro de run em disco + heartbeat; persistir `tool_use`; índice global em `~/.corvex/runs.jsonl` | F1 |
| **F6** | UI **read-write** | caixa de gates, run ao vivo, histórico, disparo de run, aprovação de gate | F5 |

### Revisão: a UI executa (decidido 13/08, após a redação do prompt de design)

A UI **não é read-only**. Ela dispara run, aprova gate, pausa e mata — a intenção é
substituir o terminal no dia a dia, **em paralelo** com ele. Consequências:

- **A UI vira a fronteira de aplicação do gate humano:** ela guarda o consentimento, o
  runner guarda a credencial, o agente não tem nenhum dos dois. Resolve estruturalmente
  o problema de "gate por prompt" que motivou o rebrand inteiro.
- **A F5 deixa de depender da F4 e passa a depender só da F1.** Registro de run em disco +
  heartbeat + eventos `tool_use` viram **pré-requisito duro**: sem eles a UI não consegue
  listar nem supervisionar o que está vivo.
- **Sem daemon.** `corvex ui` sobe um servidor que spawna processos de run desacoplados e
  os acompanha pelo registro em disco. O run continua sendo processo independente; a UI é
  supervisora, não dona. Preserva o CLI e faz o "em paralelo" sair de graça.
- **Regra de paridade:** todo botão tem equivalente em CLI; nenhum caminho existe só na UI.
- **Auth desde o início:** servidor em localhost que executa comando com credencial na mão
  precisa de token (gerado no `corvex ui`, embutido na URL aberta). Retrofitar é pior.
- **Risco central de produto:** a tela de aprovação de gate. Gate que vira carimbo é pior
  que gate nenhum — é a cicatriz #4 (reviewer passou 🔴 na 59337) na versão humana.
  O desenho tem que forçar o olhar antes de habilitar o botão.

**Backlog paralelo:** teto no `griller.go`; contrato de contexto cross-repo
(leitura de B e C a partir de A — `WorktreeConfig.Link` é o pé).

## Em aberto

Nada bloqueante. Próximo passo: spec da F1.

## Fontes

- `martinfowler.com/articles/harness-engineering.html` — guides (feedforward) × sensors
  (feedback), cada um computacional ou inferencial. O gate de migration do pilot é o
  desenho canônico: classificador determinístico → review-IA independente → aplica só em
  STG → verifica por MCP → senão park humano.
- `~/projects/yandeh/smartcare/.claude/skills/` — 88 KB em 8 skills: `az` (24.7 KB, 84
  regras), `pilot-feature` (14.5 KB + 40 KB de workflow.js), `pilot-incident`, `refine`,
  `refine-maestri`, `dev-maestri`, `ship`, `release`.
