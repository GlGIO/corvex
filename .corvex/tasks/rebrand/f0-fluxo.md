# F0 — A espinha do fluxo de dev

> 🗃️ **REGISTRO HISTÓRICO.** Os números e status deste arquivo valiam quando ele foi
> escrito e **não são estado corrente** — não decida por eles. O estado atual, com a
> instrução de como remedir cada número, está em `fechamento.md`.

> Extraído de `~/projects/yandeh/smartcare/.claude/skills/` em 2026-08-13:
> `refine` (4.4 KB), `autopilot` (13 KB + 40.7 KB de workflow.js), `ship` (4.3 KB),
> `release` (9.4 KB), `az` (24.8 KB), `dev-maestri` (7.8 KB).
> Insumo da F1 (taxonomia) e da F4 (catálogo de tools).

## Os 4 eixos

O fluxo não é linear — são quatro eixos com entradas próprias, ligados por artefatos.

```
IDEIA ──/refine──▶ BACKLOG ──/refine create──▶ BOARD (Feature>Story>Task)
                                                  │
                                                  ▼
                            ┌── /autopilot <featId> ──▶ feature branch  (feature inteira, background)
                            └── /dev-maestri <storyId> ▶ story branch   (uma story, interativo)
                                                  │
                                                  ▼
                                              /ship ──▶ PR + gate IA + merge por nível
                                                  │
                                                  ▼
                                             /release ──▶ tags → STG → UAT → PRD
```

`/az` é transversal: toda escrita no board passa por ela, dos quatro eixos.

| Eixo | Skill | Unidade | Termina em |
|---|---|---|---|
| Escopo | `refine` / `refine-maestri` | Feature | work items no board |
| Execução | `autopilot` (feature) / `dev-maestri` (story) | Feature ou Story | branch com código |
| Fechamento | `ship` | Branch | PR votado |
| Promoção | `release` | Versão | tag → ambiente |

## Catálogo de gates reais (o que já existe, tipado)

Esta é a evidência empírica do que "gate" significa neste fluxo. Quatro naturezas distintas:

| Gate | Natureza | Onde | Quem decide | Falha → |
|---|---|---|---|---|
| Brief vago | inferencial | `refine` 0 | modelo pergunta | pausa |
| Materializar no board | **humano** | `refine` B1 | humano diz "criar" | não cria |
| `classify_migration.sh` | **computacional** | autopilot | exit do script | park |
| Review de migration (agent `dba`) | inferencial **independente** (≠ autor) | autopilot | IA adversarial | park |
| Schema pós-migrate (MCP `information_schema`) | **computacional** | autopilot | query | needsFix |
| `stg-validator` | **computacional** | autopilot | roda o fluxo real | needsFix |
| `pr-review.sh` | inferencial embalado em script | ship / autopilot | IA vota | não vota, para |
| Merge por nível | **estrutural** | ship 8 | regra de branch | — |
| Loop de fix cap 2 | **política** (contador) | autopilot | contador | needsFix |
| Push de tag | **humano** | release 4 | humano confirma | não pusha |
| Migration antes de UAT/PRD | **humano** | release 4 | humano roda | pausa |
| `main`/`develop` | **humano** | ship 8 / release 4 | humano mergeia | para |

**Quatro naturezas, não duas:** computacional, inferencial, **humano** e **política**
(regra do runner: contador, teto, nível de branch). A recipe do corvex hoje só modela
`task` e `command` — falta humano e política como cidadãos de primeira classe.

**Padrão canônico do Fowler, já implementado:** o gate de migration encadeia
computacional → inferencial independente → aplica no ambiente barato → verifica
computacionalmente → senão humano. É o único gate do fluxo em que há confiança sem ressalva.

## Steps tipados — mapeamento do fluxo real

| Tipo | Modo | Ocorrências no fluxo |
|---|---|---|
| `code` | inferencial | implementer, fixer do loop cap 2, investigator |
| `tool` | computacional | escritas `az`, git (branch/push/merge), PR ops, tags, `npm run migrate` |
| `test` | computacional | `stg-validator`, testes de unidade, `pr-review.sh` |
| `repro` | computacional (**temporal**) | — *ver lacuna abaixo* |

**Lacuna:** `repro` não tem ocorrência no fluxo atual porque a `pilot-incident`
(diagnóstico-primeiro) foi **removida** em 2026-08-13. O tipo foi aprovado na rodada 2,
mas o fluxo que o usava não existe mais. Decidir: ressuscitar o eixo de incidente, ou
tirar `repro` da taxonomia até haver consumidor.

## Funções puras escritas como prosa (alvos diretos da F4)

Além das 84 regras da `az`, o fluxo tem **três tabelas de decisão determinísticas
escritas em português pra um modelo interpretar**. Todas são `switch`:

1. **`ship` §Branching** — `feat/*`→feature branch, `feature/*`→develop, `hotfix|fix/*`→main+tag+back-merge, `release/*`→main+tag+back-merge. Entrada: nome da branch. Saída: target + pós-merge. **Função pura.**
2. **`ship` passo 8 — merge por nível** — target≠develop/main ⇒ a skill completa o merge; target∈{develop,main} ⇒ para. **Função pura**, e é a regra de segurança mais importante do fluxo inteiro.
3. **`release` §1-2 — versão e ambiente** — hotfix⇒PATCH no `main`; train⇒MINOR; `vX.Y.Z-beta`⇒STG de `release/*`; `uX.Y.Z`⇒UAT; `vX.Y.Z`⇒PRD do `main`. **Tabela.**

Hoje o modelo acerta quase sempre. "Quase sempre" numa regra que decide *se mergeia em
main* é a definição de risco desnecessário — e o custo de acertar é uma função de 20 linhas.

## As 5 cicatrizes — status

| # | Cicatriz | Mitigação hoje | Estado |
|---|---|---|---|
| 1 | MCP declarada que não existe (85k tokens) | gate de preflight de tools | **REMOVIDO em 13/08** ⚠️ |
| 2 | Resume entre sessões (669k tokens, 59459) | regra "não resuma cruzando sessão" | prosa; limite estrutural do runtime |
| 3 | Context-loss Planner→Worker (59337) | "coeso ⇒ implementer único, sem fragmentar" | prosa no investigator |
| 4 | Reviewer passou 🔴 (59337) | reviewer=opus + `pr-review.sh` autoritativo | parcial |
| 5 | Work item no projeto errado (60189) | `--org`/`--project` explícitos em toda chamada | prosa (84 regras) |

**Só a #5 tem solução determinística óbvia (tool tipada). A #1 tinha e foi apagada.
A #2 é o limite que justifica o corvex existir.**

## O que ficou de fora desta F0 (deliberadamente)

- `refine-maestri` e `dev-maestri` lidos só pelo papel, não em detalhe — são variações
  (painel orquestrado / story interativa) dos eixos já mapeados.
- Os 40.7 KB do `autopilot.workflow.js` não foram lidos linha a linha; a extração usou
  o `SKILL.md`, o `meta.phases` e greps de gate.
- Modelo de estados do board (`/az` §7, fase→estado) — relevante pra F4, não pra F1.
- Custos reais por fase — não há dado agregado hoje (é justamente a dor 1).
