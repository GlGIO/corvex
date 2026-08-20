# Prompt de continuação — estado em 2026-08-19, 21h

> Cole em sessão nova, modo autônomo. Autocontido: assume que quem lê não viu nada.
> **Tudo aqui foi MEDIDO em `8c6c1af` (corvex) e `57b17f98` (smartcare), não herdado de
> documento.** Onde um doc do repo contradiz este arquivo, o doc está vencido — está
> anotado abaixo quais e onde.

---

**GOAL: (1) devolver o gate de review de produto à `develop` do smartcare — ele está
calado agora; (2) fechar a dívida medida abaixo. Não abrir frente nova.**

Dois repositórios:
- `~/projects/corvex` — o binário. `main` = `8c6c1af`, **empurrado**, árvore limpa.
- `~/projects/yandeh/smartcare` — o domínio. `chore/flow-tools` = `57b17f98`,
  **empurrado**. `origin/develop` está **1 commit atrás** dela.

O binário instalado (`~/go/bin/corvex`) é **byte-idêntico** ao build do HEAD
(sha256 `1550792c…`, `vcs.revision=8c6c1af`, `vcs.modified=false`). **Não rode
`go install` achando que é pré-requisito** — já está feito.

---

## Invariantes — quebrar qualquer um destes é falha da tarefa

1. `go test ./... -count=1` verde no corvex, e `scripts/flow/test.sh` verde no
   smartcare. **Use `-count=1`**: sem ele a suíte volta do cache e não prova nada.
2. Cobertura combinada não cai de **82,9%**:
   `go test ./cmd/ -coverpkg=./cmd/...,./internal/ops/...,./internal/stack/...,./internal/wizard/... -cover -count=1`.
   Esse é o `coverpkg` **canônico** do roadmap; outras receitas dão outros números e
   um agente já se confundiu com isso.
3. `grep -rn -iE "azure|smartcare|yandeh|dev\.azure\.com" --include="*.go" cmd internal`
   (sem comentário, sem teste) → **vazio**; e
   `go build -o /tmp/inv4 . && strings /tmp/inv4 | grep -iE "azure|smartcare|yandeh"` → **vazio**.
4. Regra do repo do smartcare: **mudou a regra, mudou a tool E o teste**, no mesmo commit.
5. Bug se **anota**, não se conserta de passagem.
6. **Todo conserto entra com CONTROLE NEGATIVO rodado**: mute a implementação, mostre o
   teste ficando VERMELHO, restaure, mostre verde, cole a saída dos dois lados.

### Números de partida, medidos
| | |
|---|---|
| corvex `go test ./... -count=1` | verde, 22 pacotes com teste + 6 sem |
| cobertura combinada | **82,9%** (piso 60) |
| invariante 4 (a) e (b) | vazio / vazio |
| `gofmt -l .` | **1** arquivo: `e2e/corvex_test.go` |
| smartcare `scripts/flow/test.sh` | **433 passaram, 0 falharam** |
| `corvex recipe validate ship` / `board` | 7 tasks / 4 tasks, exit 0 |

---

## 1. URGENTE — a `develop` está sem gate de review de produto

**Medido:** `origin/develop:scripts/pr-review.sh` tem
`axis_list() = 'fronteira tools recipes prosa testes'`, **zero** ocorrências de
`produto` e **zero** de `--cover`. Com uma lista contendo
`backend/src/x.js` + `backend/test/x.test.js`, os cinco eixos dão **ZERO** e o script
cai em `"nenhum arquivo mudado pertence a um eixo de revisão … Nada a revisar"`,
**exit 2**. Ou seja: **todo PR de produto neste monorepo não recebe review nenhum
agora.**

Como nasceu, porque a lição importa mais que o conserto: a revisão por eixo foi
desenhada particionando **um** diff (o de tooling que estava na frente do autor) e
transformando aquela partição na estrutura permanente do gate — `backend/` e
`frontend/` não entravam em eixo nenhum. E o modo de falha escolhido foi
`"nada a revisar"` em vez de `"não sei revisar isto"`: o primeiro se cala, o segundo
bloqueia.

**O conserto já existe em `origin/chore/flow-tools`** (`ab27be19`): eixo `produto`
catch-all (o **complemento** dos outros, não uma lista de caminhos), com o charter
original de volta — **isolamento multi-tenant**, que havia *desaparecido* quando os
eixos foram especializados —, mais uma checagem de exaustividade que roda antes de
tudo e **BLOQUEIA** se sobrar arquivo sem eixo, mais exclusão **declarada** e impressa
para `fixtures/` e `.corvex/tasks/` (dado gerado, não autorado).

**Faça:** abrir e concluir PR `chore/flow-tools → develop`. Confirme com o humano antes
do merge — é a única ação outward que fecha uma linha compartilhada. Depois, confirme
por medição que um PR de produto volta a receber review (`--cover` com caminhos de
`backend/`).

Consequência colateral, para não te confundir no meio: enquanto a develop estiver
velha, o S04 da `ship.yaml` morre com *"pr-review.sh não devolveu veredito"* — porque
`die_usage` escreve em stderr e o `$(...)` do stage fica vazio. Falha fechada, mensagem
que aponta para o lugar errado.

---

## 2. Dívida aberta e medida — smartcare

Leia `scripts/flow/README.md` §"Dívida aberta", **mas confira contra o código**: essa
prosa já afirmou o contrário do código várias vezes nesta base. Medido agora:

- **`az-transition-plan.sh states` não emite newline final** em nenhum dos cinco tipos
  (`tail -c 1` dá `d`/`y`). Consumidor com `while read` **come a última linha**:
  `states --type Bug | while read` imprime New/In Progress/Code Review/QA e perde
  `Closed`. **Aberto.**
- **`plan --from QA --to QA --json` sai sem a chave `incomplete_refs`** (keys
  `[from,steps,to,type]` contra `[from,incomplete_refs,steps,to,type]` no caso
  `from != to`). Schema instável entre dois caminhos da mesma flag. **Aberto.**
- **`Microsoft.VSTS.Common.ValueArea` e `Custom.Enviadodiretoparaprod`** são
  `alwaysRequired` e não estão modelados (`grep ValueArea scripts/flow/*.sh` → nada).
  Só existem como registro em `az/SKILL.md:359`. **Aberto.**
- **`README.md:890` afirma "Anotado, não consertado"** sobre a URL base dupla da
  `az/SKILL.md` — que **está consertada**, e até na develop (`SKILL.md:41` define
  `API=…/_apis` sem `/wit`). Prosa contra código: conserte a prosa.
- **`pr-review.sh` engole falha de escrita do PR Status** (`&& note …`): se gravar o
  status falhar, o erro some e a tool sai 0 — o gate diz "verde" sem ter gravado o
  status que a Branch Policy lê. **Aberto, e é o mais sério da lista.**
- **Lacunas que ainda batem em `disallowed_tools: ["Bash(az:*)"]`**, com a sexta tool
  já entregue: (a) buscar por critério (`az boards query --wiql`); (b) `relation add`
  em item **já criado** (a criação cobre a relação, a edição não); (c) anexar arquivo
  (`POST /wit/attachments`). Fora do eixo de board, a mesma proibição pega
  `az repos pr list/create/set-vote/update`, e **não há tool tipada de PR**.
  Mudar `System.State` **não** é lacuna: é recusa deliberada da `az-edit-workitem.sh`
  (exit 2), porque estado é da `az-transition.sh`.
- **`$S` da `board.yaml` ainda cruza fios** entre dois runs de entradas idênticas —
  e isso é **registro por decisão escrita**, não a fazer: ver §4.

## 3. Dívida aberta e medida — corvex

- **Risco 3 do roadmap (snooze é vazamento): latente.** `grep -rniE "snooze|adiar"` em
  `internal/server/web/`, `cmd/`, `internal/ops/` não acha nada. Não há "adiar" na UI,
  logo não há adiado invisível. **O risco reabre no dia em que alguém puser o botão** —
  e nesse dia o número de adiados tem de ficar visível.
- **`gofmt -l .` acusa `e2e/corvex_test.go`.** Um arquivo, dívida herdada da F0 (eram 7).
- **`e2e/cdp_test.go:233` pula sem Chrome.** Nesta máquina rodou (0 skips); numa CI sem
  navegador, metade da prova de UI não prova nada.
- **Evidência inerte passa em silêncio**: `evidence:` sem `required_reading` num gate
  não-humano é aceita e nunca coletada. O argumento e a recusa da alternativa estão em
  `internal/recipe/validate_stage.go:225-243`. Um aviso exigiria canal de warning até
  `cmd/`, que não existe.

## 4. Três coisas que parecem trabalho e NÃO são — não repita

- **`CORVEX_RUN_ID` já existe** (`59c6f45`): `runShell` o exporta de
  `Run.Identity.RunID`, e há teste afirmando que o ambiente ganhou **exatamente duas**
  variáveis `CORVEX_`. **Mas a premissa que o pediu foi refutada no consumidor:** o id é
  único **por tentativa**, então chavear o diretório de estado por ele faz a **retomada**
  procurar `$S/<id>` onde a tentativa anterior nunca escreveu — deixando work item criada
  e abandonada. Há caso no `test.sh` do smartcare que **reprova** quem trocar a chave.
  A linha foi entregue; o problema segue aberto por outro motivo.
- **Saída de stage que falha já chega ao operador** (`8c6c1af`): pacote
  `internal/stepout` (cauda, 20 linhas / 4096 bytes, arquivo 0600 em
  `.corvex/runs/output/`), renderizada no `--plain` e em `run show --step`. **Não** entra
  no `activity.jsonl`, porque aquele arquivo o `auto_commit` commita.
- **A sexta tool existe**: `scripts/flow/az-edit-workitem.sh` (`f75e6952`).

## 5. Documentos VENCIDOS nos dois repos — corrija ao passar, não confie

- `.corvex/tasks/rebrand/fechamento.md` foi escrito 4 commits atrás e ainda lista como
  pendentes: a sexta tool, o `CORVEX_RUN_ID`, os defeitos do `az-transition-plan.sh`, e
  o "obstáculo operacional" do binário velho. Todos **feitos**. Registra cobertura 82,6%
  (é 82,9%) e suíte de 205 casos no smartcare (são 433).
- `roadmap.md` §Fases: os selos de **F2** ("aguardando gate humano"), **F8** ("espera
  ratificação do `ship-may-vote`") e **F9** ("default aguarda você") estão vencidos — as
  três decisões **já foram tomadas** e estão registradas na tabela do `fechamento.md`.
  É selo de papel, não bloqueio.
- `.corvex/tasks/rebrand/BLOCKED.md` **não existe** — nenhuma condição de parada foi
  acionada, e é lá que ela deve ser escrita se for.

---

## O que NÃO é seu — são decisões do Giovanni. Pergunte, não decida

1. **Merge em linha compartilhada.** `ship-may-complete.sh` recusa `develop`/`main`/
   `release/*` e **não tem override** — por desenho. Confirme antes de concluir qualquer
   merge nessas linhas, inclusive o do §1.
2. **Durabilidade da história do gate.** `activity.Entry` não tem chave de gate (as
   chaves são allowlist, com tripwire). Tornar durável = chave nova num arquivo que
   entra no git do usuário.
3. **Chave de agregação do `gate audit`** — hoje `(recipe, step_id)`, sufixo de fan-out
   **não** colapsado (`internal/ops/gate_audit.go:49`). Mudar exige campo novo em disco.
4. **`gate answer`: `--text` vs `--choice`.** Só `--text` existe, sem alias `answer` no
   topo. `--choice` pressupõe conjunto de opções declarado no gate — campo que não existe.
5. **Quem produz a pergunta** segue sendo a recipe: `types.ExecuteResult` não tem campo
   para "parei, preciso saber X". Mudar isso é mudar o protocolo do provider.
6. **`pr-review.sh` engolindo falha de PR Status** — transformá-lo em falha muda a
   superfície de erro do gate inteiro.
7. Qualquer coisa que ponha Azure/SmartCare/Yandeh no binário → viola o invariante 3.

---

## Como esta base falha — leia antes de trabalhar

Isto foi **medido** na sessão anterior, não é retórica. Quatro modos de falha, em ordem
de quanto custaram:

1. **Revisão por leitura não acha o que execução acha.** Nove rodadas de review e cinco
   eixos leram a `ship.yaml` linha por linha e deram 19 críticos. **Rodar a recipe uma
   vez** revelou que `az repos pr create` exigia `--repository`, que nunca existiu no
   arquivo: **cinco dos sete steps nunca executaram na vida da recipe.** E rodar **duas**
   vezes revelou que ela se auto-bloqueava (o run sujava a árvore e a guarda do S01
   recusava a execução seguinte). **Se o seu item é executável, execute-o.**
2. **Asserção estrutural que casa PROSA.** Seis vezes: greps sem filtro de `#`, e um awk
   cujo `/^ *#/ { next }` estava **depois** das regras — então um comentário *citando*
   `set -o pipefail` **ligava** a guarda. As guardas eram decorativas. **Se você grepar
   fonte, filtre comentário PRIMEIRO e prove com um caso que tenha o texto só no
   comentário.**
3. **Rebaixar o achado que nomeia a classe.** O eixo `testes` reportou 🟡 *"nada prova que
   a união dos eixos cobre a árvore"*. Foi registrado como dívida em vez de consertado —
   e virou o §1 deste documento. **Achado que nomeia uma classe inteira não é amarelo.**
4. **Generalizar de uma amostra de um.** A partição dos eixos veio de um único diff. **Se
   você desenhar regra a partir de um exemplo, teste-a contra um conjunto que não seja
   ele.**

E o padrão macro: naquela sessão, **ao menos 14 de 19 críticos foram introduzidos pela
própria leva que os consertou**, e o dominante foi **interação entre consertos**, não erro
local — dois consertos chegaram a se anular. Consertos pequenos, um por commit, com
controle negativo, e execução antes de declarar pronto.

**Não invente fase nova. Não mexa em `main` do smartcare. Não rode migration. Não conclua
merge em linha protegida sem confirmar.**
