# Prompt de fechamento do rebrand

> Cole em sessão nova. Autocontido: assume que quem lê não viu nada do rebrand.

---

**GOAL: fechar o que sobrou do rebrand do corvex. Não abrir frente nova.**

Dois repositórios:
- `~/projects/corvex` — o binário. Branch `main`, já com os 51 commits do rebrand
  (fast-forward feito, **não empurrado** para `origin` = `git@github.com:GlGIO/corvex.git`).
- `~/projects/yandeh/smartcare` — onde a F8 vive, porque o invariante 4 proíbe domínio no
  binário. Branch `develop`.

Leia primeiro, nesta ordem: `.corvex/tasks/rebrand/roadmap.md` (§Fases, §Riscos, invariantes),
`f8-registro.md`, `f9-custodia.md`, `f7-registro.md` §Dívidas.

## Invariantes — quebrar qualquer um destes é falha da tarefa

1. `go test ./...` verde no corvex, e `scripts/flow/test.sh` verde no smartcare.
2. `grep -rn -iE "azure|smartcare|yandeh|dev\.azure\.com" --include="*.go" cmd internal`
   (sem comentário, sem teste) → **vazio**.
3. `go build -o /tmp/inv4 . && strings /tmp/inv4 | grep -iE "azure|smartcare|yandeh"` → **0**.
4. Regra do repo: **mudou a regra, mudou a tool E o teste.** Toda correção de comportamento
   numa tool de `scripts/flow/` entra com caso em `test.sh` no mesmo commit.
5. Bug se **anota**, não se conserta de passagem. Achado fora de escopo vira registro, não
   commit oportunista.

## Trabalho, em ordem de valor

### 1. Empurrar o corvex (5 min, destrava tudo)
`main` local tem 51 commits que `origin/main` não tem, zero divergência. Todo o rebrand está
invisível enquanto isso não sobe. Confirme com o humano antes do `git push origin main` — é a
única ação outward desta lista.

### 2. Tools de ESCRITA do board (destrava a F9)
É o item que mais paga. Hoje `scripts/flow/az-transition-plan.sh` **calcula** a escada de
estados, os campos obrigatórios por degrau e o mapa fase→estado — mas não executa nada. Por
isso `security.disallowed_tools: ["Bash(az:*)"]` **não pode ser ligado**: `disallowed_tools` é
global ao worker (`internal/step/worker.go`), e `skill_routing: board: az` depende do worker
rodar `az` cru para criar/mover/comentar work item. Está escrito no
`smartcare/.corvex/config.yaml`.

Escreva em `smartcare/scripts/flow/`, seguindo o contrato do `README.md` de lá
(exit 0 = sim · 1 = a regra nega · 2 = chamada inválida · stdout máquina · stderr prosa):
- criar work item **já com o pai numa chamada só** (o `create` + `relation add` deixa órfão);
- comentar (HTML, `api-version=7.0-preview.3` — o `-preview.3` não é opcional);
- transicionar **um degrau**, consumindo o plano do `az-transition-plan.sh`;
- **sonda `validateOnly=true`** — detecte pela `.message`, **nunca** por `.id` (a resposta não
  traz `.id`, então `jq -e '.id'` dá falso-negativo em tudo; mordeu na GMUD 1.4).

Depois: uma recipe `board.yaml` que as chame, do mesmo jeito que `ship.yaml` fez para o eixo
de branch — com as tools declaradas em `requires: bin:` (aceita caminho, não só nome em PATH).
**Só então** ligue `disallowed_tools` e reporte.

### 3. Fechar os buracos pequenos das tools do smartcare
- `ship-target.sh` não conhece `chore/*`, `ci/*` nem `backmerge/*`, todas em uso no `origin`.
  Recusa fail-closed. Decida o target de cada uma (ou registre que continua recusando de
  propósito) — uma linha no `case` mais o teste.
- `az-transition-plan.sh` linha ~77: o arm `"Bug/Closed"|"Bug/Validado QA"|"Bug/Homolog"|
  "Bug/Gmud"` é **inalcançável** — `Bug/Closed` já casa no arm de cima, e os outros três não
  estão na escada do Bug, então `index_of` recusa antes. E a correção de `Feature Testes →
  Deploy` (`967aedba`, medida na GMUD 1.5) entrou **sem teste**. Feche os dois.
- `pr-review.sh` tem veredito binário (`APROVADO`/`BLOQUEADO`) e não expõe 🟡, então
  `ship-may-vote.sh` nunca devolve `approve-with-suggestions`. Exponha ou registre a decisão de
  não expor.

### 4. Dívidas registradas da F7 (`f7-registro.md`)
Poll de 5s em vez de SSE · `gate answer` (2g) é só um nome, o eixo "o agente pergunta" não tem
evento em disco · `run pause` fora (F3/D13: falta arquivo de controle e ponto de leitura na
barreira de onda) · um repositório por servidor · sem header CSP. **Pergunte quais valem** antes
de atacar — não são todas óbvias.

### 5. Risco 1 do roadmap, sem mitigação até hoje
*"Sensor sobre os sensores"*: medir **latência de aprovação e taxa de reprovação por gate**. O
roadmap diz que atrito não é prova e que *"gate aprovado em 4s, 100% das vezes, é teatro:
automatiza ou deleta"*. A F5 já grava os dados; falta a leitura. Provavelmente um verbo de
`corvex` ou uma coluna no `inspect --json`.

### 6. Achados menores
- **Preflight**: `exec.LookPath` resolve contra o **CWD do processo**, não contra o `workDir`
  que `PreflightRequirements` recebe (`internal/ops/preflight.go`). Num run com worktree
  isolado ele checaria a cópia errada. Não testado — confirme e conserte ou registre.
- **`DisallowedTools` não alcança MCP** (`f9-custodia.md`, fora de escopo): um servidor MCP
  declarado no `config.yaml` é outro caminho e não é filtrado. É um buraco no nível 3 de
  fronteira.
- **Backlog aberto**: contrato de contexto cross-repo.

## O que NÃO é seu — são decisões do Giovanni. Pergunte, não decida

1. **`ship-may-vote.sh`.** Hoje não vota em `develop`/`main`/`release/*`, porque na `main` o
   `Required reviewers` é bloqueante com mínimo 1 e o token é o do dev: um voto da automação
   **satisfaz a política e destrava o merge**, gravando aprovação humana que ele pode não ter
   dado. Isso **mudou o comportamento do `/ship`** e espera ratificação.
2. **§0b vs §7b do `az/SKILL.md`.** Cobrem o mesmo problema com exemplos diferentes. Estão os
   dois de pé, com ponteiro. Consolidar apaga trabalho — é decisão dele.
3. **A base da feature branch.** `pilot-feature` e `dev-maestri` cortam de `origin/develop`, mas
   a feature mergeia em `release/X.Y.0`. Se a develop estiver à frente, o PR carrega diff que
   não é da feature. Mudar a base é comportamento de dois orquestradores.
4. **Virar o default da F9 no binário** (não só por repo). O roadmap marca a F9 como
   *"⚠️ gate ao fim: segurança não se aprova sozinha"*.

## Aceite

- `origin/main` do corvex com os 51 commits (após o OK dele).
- Board com tools de escrita + `board.yaml`, e `disallowed_tools` ligado **ou** um registro
  dizendo por que ainda não.
- Itens 3 e 6 fechados ou registrados por escrito, com o porquê.
- Os cinco invariantes de pé.
- Um `.corvex/tasks/rebrand/fechamento.md` no corvex: o que foi feito, o que virou registro em
  vez de conserto, e o que continua esperando decisão dele.

**Não invente fase nova. Não mexa em `main` do smartcare. Não rode migration. Não conclua
merge em linha protegida — a `ship-may-complete.sh` existe justamente para isso e não tem
override.**
