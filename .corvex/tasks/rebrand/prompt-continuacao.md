# Prompt de continuação — estado em 2026-08-20, 01h

> Cole em sessão nova, modo autônomo. Autocontido: assume que quem lê não viu nada.
> **Tudo aqui foi MEDIDO em `b10ad38` (corvex) e `a7995f79` (smartcare), não herdado de
> documento.** Onde um doc do repo contradiz este arquivo, o doc está vencido.
>
> ⚠️ A versão anterior deste arquivo dizia que o conserto do gate "está na branch e
> precisa de um merge com sua confirmação". **Estava errado, e é a lição da sessão:**
> a branch não estava mergeável. O gate de review dela, rodado, devolveu 🔴 nos SEIS
> eixos, com **11 críticos confirmados por execução** — 4 deles causados por a
> `release/1.6.0` ter sido cortada horas depois de a prosa ser escrita. Nenhuma
> quantidade de leitura tinha achado isso; uma execução do gate achou.

---

> ⚠️ **LEIA `.corvex/tasks/rebrand/BLOCKED.md` PRIMEIRO.** Depois deste arquivo ser
> escrito, o gate rodou 21 vezes no PR #42110 e a contagem de 🔴 foi de **11 para 10**
> — cinco levas de conserto, redução líquida de um. 23 commits novos no smartcare,
> suíte de 433 → 468 casos. A causa está medida e é interação entre consertos: cinco
> dos meus consertos criaram o defeito que o gate achou na rodada seguinte. **A tarefa
> não é mais "consertar o que o gate achou" — é decidir a regra de parada.**

**GOAL: concluir o PR #42110 (`chore/flow-tools` → `develop`) do smartcare — o merge é
o único passo que falta, e é decisão do humano. Depois, fechar as quatro decisões de
contrato listadas em §4. Não abrir frente nova.**

Dois repositórios:
- `~/projects/corvex` — o binário. `main` = `b10ad38`, **empurrado**, árvore limpa.
- `~/projects/yandeh/smartcare` — o domínio. `chore/flow-tools` = `a7995f79`,
  **empurrado**. PR **#42110** aberto para `develop`.

---

## 1. O ÚNICO passo que falta: concluir o PR #42110

**Medido:** `mergeStatus: succeeded`, sem conflito. A divergência é DISJUNTA — a
`develop` avançou 88 commits só em `backend/` (45 arquivos) e `frontend/` (29); a branch
só em `scripts/`. **Interseção de arquivos: vazia.** (O handoff anterior dizia
"origin/develop está 1 commit atrás"; era falso.)

**Por que o merge importa:** `origin/develop:scripts/pr-review.sh` particiona o diff em
cinco eixos e o fechamento de nenhum contém `backend/` ou `frontend/`. Rodado com dois
arquivos reais de produto: os cinco eixos dão **ZERO**, o script cai em *"nada a
revisar"*, **exit 2**. Ou seja: **todo PR de produto deste monorepo não recebe review
nenhum enquanto a develop estiver assim.** Nesta branch: `produto:2`, exit 0.

**`ship-may-complete.sh` recusa `develop` por desenho e não tem override.** Então
concluir é ato humano, no Azure DevOps. Confirme com o Giovanni antes.

Depois de mergear, **meça** que voltou: `pr-review.sh --cover` com caminhos de
`backend/` na develop tem de dar `produto:N`, não `SEM-EIXO`.

## 2. O que esta sessão fez, e o que prova cada coisa

14 commits no smartcare, um por achado, cada um com CONTROLE NEGATIVO rodado e colado
na mensagem. Os 11 🔴 do gate, todos verificados por execução antes de consertar:

| eixo | achado | commit |
|---|---|---|
| fronteira | a dica do S01 ficou inalcançável quando `ACTIVE` virou não-vazio | `0fd5f307` |
| tools | a sexta tool caía no catch-all `produto` (charter errado) | `03c2fc81` |
| prosa | asserção de estado do board, com data, em 5 sítios | `c78e16ae` |
| prosa | o `.workflow.js` prescrevia a forma proibida; o pin não o lia | `d85fa768` |
| prosa | o template do README prescrevia o que o README condena | `2d3aa081` |
| prosa | a tabela dizia documentar 8 arms e listava 5 | `def6f2be` |
| testes | `^[a-z_]+\(\)` — nome com DÍGITO escapava da regra inteira | `b8bf5bd3` |
| testes | o controle do SIGPIPE media a si mesmo, não a regra | `715a9133` |
| testes | a sétima porta de saída passava pelo caso "uma porta de saída" | `e5710864` |
| testes | "é fonte?" cobria 2 dos 7 caminhos que o arquivo lê | `64566cbc` |
| recipes 🟡 | exemplo de `BOARD_FIELDS` com 3 campos onde a escada exige 6 | `3a8b6a2c` |

Mais três que não vieram do gate: o `states` que comia a última linha de todo
`while read` (`c4b006c2`), o fail-closed do classificador que bloqueava calado
(`7c92853d`), e a prosa vencida do README (`a7995f79`).

No corvex: gofmt zerado (`20d86a7`), `CORVEX_REQUIRE_BROWSER` para a prova de UI parar
de mentir de verde numa CI sem navegador (`b10ad38`), e a errata do `fechamento.md`
(`e388a21`).

**Números medidos agora:**

| | |
|---|---|
| smartcare `scripts/flow/test.sh` | **441 passaram, 0 falharam** (eram 433) |
| corvex `go test ./... -count=1` | verde |
| cobertura combinada (piso 60) | **82,9 %** |
| invariante 3 (a) e (b) | vazio / vazio (as 3 ocorrências fora de teste são comentários, conferidas) |
| `gofmt -l .` | **vazio** |
| `recipe validate ship` / `board` | 7 tasks / 4 tasks, exit 0 |
| `corvex gate audit --all` | 3 gates em 13 repositórios, 3 decididos, 0 abertos |

## 3. Invariantes — quebrar qualquer um destes é falha da tarefa

1. `go test ./... -count=1` verde no corvex, `scripts/flow/test.sh` verde no smartcare.
   **Use `-count=1`**: sem ele a suíte volta do cache e não prova nada.
2. Cobertura combinada não cai de **82,9%**:
   `go test ./cmd/ -coverpkg=./cmd/...,./internal/ops/...,./internal/stack/...,./internal/wizard/... -cover -count=1`.
   Esse é o `coverpkg` **canônico**; outras receitas dão outros números.
3. `grep -rn -iE "azure|smartcare|yandeh|dev\.azure\.com" --include="*.go" cmd internal`
   (sem comentário, sem teste) → **vazio**; e
   `go build -o /tmp/inv4 . && strings /tmp/inv4 | grep -iE "azure|smartcare|yandeh"` → **vazio**.
4. Regra do smartcare: **mudou a regra, mudou a tool E o teste**, no mesmo commit.
5. Bug se **anota**, não se conserta de passagem.
6. **Todo conserto entra com CONTROLE NEGATIVO rodado**: mute a implementação, mostre o
   teste ficando VERMELHO, restaure, mostre verde, cole a saída dos dois lados.

## 4. O que NÃO é seu — decisões do Giovanni. Pergunte, não decida

1. **Merge em linha compartilhada** — inclusive o do §1.
2. **O `400` continua em `exit 3`, que significa "re-rode".** O crítico nº4 desta leva
   tirou o `404` do `exit 3` com o argumento certo e não o aplicou ao `400`, que é como
   o Azure devolve toda violação de regra. Anotado no README com a medição. Mudar isso
   muda a superfície de erro das SEIS tools de uma vez, e o `403` está no mesmo balaio.
3. **`pr-review.sh` engolindo falha de escrita do PR Status** (`&& note …`): o gate diz
   verde sem ter gravado o status que a Branch Policy lê. O mais sério da lista.
4. **Schema de `plan --json`:** o caso `from == to` sai sem a chave `incomplete_refs`
   (medido: `[from,steps,to,type]` contra `[from,incomplete_refs,steps,to,type]`).
   Acrescentar a chave estabiliza o schema, mas quem detecta o caso por AUSÊNCIA de
   chave quebra.
5. **`Microsoft.VSTS.Common.ValueArea` não modelado** (`grep ValueArea scripts/flow/*.sh`
   → nada). ⚠️ Correção ao handoff anterior: `Custom.Enviadodiretoparaprod` **ESTÁ**
   modelado (`az-transition-plan.sh:129`); só o `ValueArea` não está.
6. **Durabilidade da história do gate** no corvex: `activity.Entry` não tem chave de
   gate. Tornar durável = chave nova num arquivo que entra no git do usuário.
7. **Chave de agregação do `gate audit`** — hoje `(recipe, step_id)`, sufixo de fan-out
   não colapsado (`internal/ops/gate_audit.go:49`).
8. **`gate answer`: `--text` vs `--choice`** — `--choice` pressupõe conjunto de opções
   declarado no gate, campo que não existe.
9. **Quem produz a pergunta** segue sendo a recipe: `types.ExecuteResult` não tem campo
   para "parei, preciso saber X".
10. Qualquer coisa que ponha Azure/SmartCare/Yandeh no binário → viola o invariante 3.

## 5. Dívida ainda aberta no corvex

- **Risco 3 do roadmap (snooze é vazamento): latente.** `grep -rniE "snooze|adiar"` em
  `internal/server/web/`, `cmd/`, `internal/ops/` não acha nada. O risco reabre no dia
  em que alguém puser o botão — e nesse dia o número de adiados tem de ficar visível.
- **`e2e/cdp_test.go` pula sem Chrome, mas agora dá para exigir**: com
  `CORVEX_REQUIRE_BROWSER` setado, "sem navegador" é FALHA. Este repo não tem CI; a
  alavanca está pronta para quem puser. Os DOIS caminhos de desistência estão cobertos
  (binário ausente e `cmd.Start()` que falha).
- **Evidência inerte passa em silêncio**: `evidence:` sem `required_reading` num gate
  não-humano é aceita e nunca coletada. Argumento e recusa da alternativa em
  `internal/recipe/validate_stage.go:225-243`. Um aviso exigiria canal de warning até
  `cmd/`, que não existe.
- **Selo da F2 sem rastro:** o `roadmap.md` marcava F2 como "aguardando gate humano".
  Procurei o registro dessa decisão no `fechamento.md` e nos `f*-registro.md` e **não
  existe** — a tabela das quatro decisões não tem linha para a F2. Anotado como selo sem
  rastro, não declarado feito. F8 e F9 têm registro citável e foram atualizadas.

## 6. Três coisas que parecem trabalho e NÃO são — não repita

- **`CORVEX_RUN_ID` já existe** (`59c6f45`), **mas a premissa que o pediu foi refutada no
  consumidor:** o id é único por TENTATIVA, então chavear o `$S` por ele faz a retomada
  procurar um diretório que a tentativa anterior nunca escreveu — item criado em `New` e
  abandonado. Há caso no `test.sh` do smartcare que **reprova** quem trocar a chave.
- **Saída de stage que falha já chega ao operador** (`8c6c1af`, `internal/stepout`).
- **A sexta tool existe** (`az-edit-workitem.sh`, `f75e6952`) — e agora está no eixo
  `tools` do gate, o que ela não estava.

## 7. Como esta base falha — leia antes de trabalhar

Aos quatro modos de falha já medidos, esta sessão acrescentou dois. Todos MEDIDOS.

1. **Revisão por leitura não acha o que execução acha.** Nove rodadas e cinco eixos
   leram a `ship.yaml` linha por linha e deram 19 críticos; **rodar a recipe uma vez**
   revelou que cinco dos sete steps nunca executaram. Nesta sessão: nove rodadas de
   review não viram os 11 críticos que **uma execução do gate** achou.
   **Se o seu item é executável, execute-o.**
2. **Asserção estrutural que casa PROSA.** Seis vezes. **Filtre comentário PRIMEIRO e
   prove com um caso que tenha o texto só no comentário.**
3. **Rebaixar o achado que nomeia a classe.** O 🟡 *"nada prova que a união dos eixos
   cobre a árvore"* virou dívida em vez de conserto — e virou o defeito do §1.
4. **Generalizar de uma amostra de um.** A partição dos eixos veio de um único diff.
5. **NOVO — estado do mundo escrito em prosa de guard-rail.** Cinco sítios afirmavam,
   com data, que não havia minor aberta. A `release/1.6.0` foi cortada horas depois e
   quatro dos 11 críticos nasceram disso — inclusive um que mudou QUAL RAMO de código
   executa. Há pin agora (`prosa_nao_afirma_estado_do_board`). **Regra não envelhece;
   estado do mundo envelhece. Escreva o contrato, não o board de hoje.**
6. **NOVO — guarda cujo controle é uma CÓPIA da guarda.** O controle do SIGPIPE
   regrepava o mesmo regex em vez de chamar a regra: estreitar a regra deixava o
   controle verde. **Controle tem de passar pela PORTA da regra.** Três dos quatro 🔴 do
   eixo `testes` eram desta classe — e a suíte tinha 433 casos verdes.

E o padrão macro: naquela sessão, ao menos 14 de 19 críticos foram introduzidos pela
própria leva que os consertou. Nesta sessão o padrão se repetiu em escala menor e
**visível**: cinco erros meus foram pegos pelo harness, não por mim — pin lendo fonte
crua, `$SHIP_YAML` citado antes de existir (função morria calada sob `set -u`), plante
com dois backslashes onde o JS tem um, glob comparado literalmente com prosa, e o pin do
`states` acusado pela regra de leitura crua que eu tinha acabado de alargar.
**Consertos pequenos, um por commit, com controle negativo, e execução antes de declarar
pronto.**

**Não invente fase nova. Não mexa em `main` do smartcare. Não rode migration. Não
conclua merge em linha protegida sem confirmar.**
