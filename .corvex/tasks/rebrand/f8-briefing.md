# F8 — Catálogo de tools: briefing autocontido

> Escrito para ser colado numa sessão **nova**, que não viu nada do rebrand. O trabalho da F8
> **não acontece neste repositório** — ele acontece no repo do SmartCare, e é por isso que ele
> ficou de fora quando as outras fases fecharam.

## Em uma frase

Transformar as regras que hoje vivem em **prosa** dentro das skills do SmartCare
(`~/projects/yandeh/smartcare/.claude/skills/`) em **tools tipadas** — comandos com assinatura,
determinísticos, cujo veredito é o exit code — para que um agente as *chame* em vez de as
*interpretar*.

## Por que isso importa (e por que não é sobre o corvex)

O roadmap do corvex diz, com todas as letras, que isto **não é diferencial do corvex**:

> *"Não é diferencial: determinismo via tools tipadas. Como MCP, a autopilot ganha o mesmo sem
> uma linha de Go — por isso o catálogo tem valor independente do corvex."*

O que o corvex acrescenta é a **fronteira**: com `security.disallowed_tools` (já implementado) o
worker pode ser proibido de shellar, e aí o catálogo deixa de ser sugestão. Sem catálogo, fechar
o caminho cru só quebra o fluxo — motivo pelo qual a decisão de virar o default da custódia de
credencial está **explicitamente esperando a F8**.

E há a razão mais simples: **prosa não tem exit code.** Uma regra em prosa é reinterpretada a
cada execução, por um modelo diferente, com um contexto diferente. A regra mais perigosa do
fluxo hoje é assim.

## As três funções puras que a F0 achou, com a prosa de origem

### 1. `ship`: branch → target  (`skills/ship/SKILL.md`, tabela "Branching (2 níveis)")

| Branch origem | Target | Pós-merge |
|---|---|---|
| `feat/<storyId>-*` (story) | **feature branch** (`feature/<featId>-*`) | nada |
| `feature/<featId>-*` | `develop` | nada |
| `hotfix/*`, `fix/*` | `main` | tag `v<versão>` + back-merge em `develop` |
| `release/*` | `main` | tag `v<versão>` + back-merge em `develop` |

Função pura: `target(branch) → branch`. Hoje: um agente lê a tabela e decide.

⚠️ Note a divergência que já existe **dentro da própria prosa**: a tabela do `ship` manda
`feature/* → develop`, e a `release/SKILL.md` diz, em negrito e chamando de *inegociável*, que a
feature faz PR **sempre para `release/X.Y.0`, nunca para develop**. Duas skills, duas respostas
para a mesma pergunta. Uma tool tipada não consegue ter as duas — **resolver essa contradição é
parte do trabalho**, e é o melhor argumento isolado a favor da F8.

### 2. `ship`: merge por nível  (`skills/ship/SKILL.md`, passo 8)

- Target é **feature/fix branch** → a skill aprova **e completa** o merge.
- Target é **`develop` ou `main`** → **NÃO** completa: vota, deixa verde e para. Só humano mergeia.
- Veredito 🔴 → nunca vota, nunca completa.

Função pura: `mayComplete(target, verdict) → bool`. **É a regra de segurança mais importante do
fluxo** — o roadmap a nomeia assim — e hoje ela é um parágrafo que um modelo pode ler com pressa.

### 3. `release`: versão ↔ tag ↔ ambiente  (`skills/release/SKILL.md`, §1 e §2)

| Tag | Ambiente | Cortada de |
|---|---|---|
| `vX.Y.Z-beta` | STG | `release/X.Y.0` |
| `uX.Y.Z` | UAT | `release/X.Y.0` |
| `vX.Y.Z` | PRD | `main` (após merge release→main) |

Mais as regras de bump: hotfix → PATCH **direto no `main`** (nunca pela `release/*` dormente de
uma minor já shippada), trem → MINOR, breaking → MAJOR.

Função pura: `nextVersion(kind, current) → version` e `tagFor(version, env) → tag`.

### E o resto: `skills/az/SKILL.md`, 329 linhas

É o maior e o mais mecânico: constantes de org/projeto, roteamento de board, campos obrigatórios
por tipo de work item, e uma seção inteira de **gotchas descobertos na marra** (`Found In`
obrigatório ao criar Bug; a escada de transição de User Story; sondar transição com
`validateOnly=true` em vez de tentativa-e-erro). Cada gotcha desses é uma tool esperando para
existir — hoje eles são conhecimento que só se aplica se o agente lembrar de ler.

## O que "tool tipada" precisa significar, concretamente

Para o corvex conseguir consumir (e para a autopilot ganhar o mesmo sem Go), uma tool é:

1. **Um comando executável** com argumentos nomeados — script em `scripts/`, subcomando, ou
   servidor MCP. Não é um parágrafo, não é um prompt.
2. **Determinístico:** mesma entrada, mesma saída. Sem julgamento.
3. **Veredito por exit code:** `0` = passou. É isso que permite o gate `computational`.
4. **Saída legível por máquina** quando produz dado (JSON), para virar `evidence` ou alimentar
   um fan-out (`produces: items`).
5. **Sem efeito colateral escondido:** se muda o mundo (abre PR, sobe tag, roda migration), diz
   isso no nome e aceita ser guardada por um gate.

No corvex isso vira, numa recipe:

```yaml
requires:
  - bin: az
    why: "az é como o ship abre o PR"

stages:
  - id: S01
    kind: tool
    command: "./scripts/ship-target.sh --branch $(git branch --show-current)"
    produces: items          # se devolve JSON
  - id: S02
    kind: tool
    command: "./scripts/ship-complete.sh --pr $PR_ID"
    gates:
      - nature: policy
        branch_not: [main, develop]   # a regra de merge-por-nível, como POLÍTICA do runner
      - nature: human
        when: before
    evidence:
      - kind: diff
        label: "Diff"
        required_reading: true
        from: "git diff --stat $CORVEX_RUN_BASE"
```

Detalhe que custou um dogfood inteiro para aprender: **ancore diffs em `$CORVEX_RUN_BASE`, não em
`HEAD`.** Com `auto_commit`, o corvex commita a cada step, então `HEAD` no momento do gate é a
papelada do step anterior — e o aprovador acaba obrigado a "ler" um diff que não contém a mudança.

## Aceite (do roadmap)

> *"Validar **na autopilot** antes de o corvex consumir."*

Ou seja: a tool nasce e prova o valor no fluxo que já existe. Se ela só faz sentido dentro do
corvex, ela está no lugar errado.

## Ordem sugerida

1. **`mayComplete`** (merge por nível) — a mais perigosa, a mais curta, e a que mais paga.
2. **`target(branch)`** — e, no caminho, **decidir a contradição** entre `ship` e `release`.
3. **`nextVersion`/`tagFor`** — tabela pequena, regra clara, alto risco de erro humano.
4. **`az`**: comece pelos gotchas de transição de estado, que são os que quebram na prática.

## Armadilhas

- **Nada de domínio no binário do corvex.** É o invariante 4 do roadmap, com teste
  (`strings <binário> | grep -i azure` tem de sair vazio). As tools vivem no repo do usuário.
- **A regra de merge por nível não pode virar "um flag a mais".** Se `mayComplete` puder ser
  contornada por argumento, ela não é fronteira.
- **A prosa das skills tem histórico embutido** (datas, incidentes, "descoberto 2026-08-04").
  Isso é valioso e não deve morrer na tradução — vira comentário/doc da tool, não sai do mundo.

## O que isto destrava aqui

Com o catálogo de pé, o corvex pode virar o default da custódia de credencial (F9): worker deixa
de receber credencial e passa a **pedir a operação** em vez de receber o segredo. Hoje o mecanismo
existe e o default está intocado exatamente porque o step ainda não tem como pedir.

## Onde ler mais (neste repo)

- `.corvex/tasks/rebrand/roadmap.md` — §F8, §F9, "O que só o corvex pode dar", invariantes.
- `.corvex/tasks/rebrand/f9-custodia.md` — o mecanismo que espera a F8, e a decisão em aberto.
- `.corvex/tasks/rebrand/f2-design.md` §10 — recipe completa de exemplo com as quatro naturezas
  de gate.
- `README.md`, seção "Recipes" — o contrato como um autor de recipe o vê.
