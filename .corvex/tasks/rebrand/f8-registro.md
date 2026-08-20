# F8 — Catálogo de tools (registro de fecho)

> 🗃️ **REGISTRO HISTÓRICO.** Os números e status deste arquivo valiam quando ele foi
> escrito e **não são estado corrente** — não decida por eles. O estado atual, com a
> instrução de como remedir cada número, está em `fechamento.md`.

> A única fase do rebrand cujo trabalho **não acontece neste repositório**. O código está em
> `~/projects/yandeh/smartcare` (`scripts/flow/`, `.corvex/recipes/ship.yaml`, e as cinco skills
> reescritas), commitado em `bb7c03bf`, `62368efe` e `981169aa`, no `develop`. Aqui fica só o
> registro — porque o invariante 4 diz que o binário não conhece o domínio de ninguém.

## Por que fora daqui, em uma frase

As regras que a F0 achou (`ship` branch→target, `ship` merge-por-nível, `release`
versão↔tag↔ambiente) são feitas de Azure DevOps e do trem de release da Yandeh. Escrevê-las em
Go quebraria o invariante 4, que tem teste: `strings <binário> | grep -i azure` sai vazio. O que
o corvex acrescenta não é a tool — é a **fronteira** (`security.disallowed_tools`) e o **encaixe**
(`kind: tool`/`kind: test`), ambos já entregues antes desta fase.

## O que a tradução produziu — e não foi transcrição

O roadmap descreve a F8 como *"84 regras em prosa → assinaturas"*, o que soa mecânico. As três
funções puras somam ~40 linhas de lógica. O caro foi outra coisa: **traduzir força a decidir o
que a prosa deixava conviver.**

### Contradição 1 — para onde vai a `feature/*`

Quatro lugares, e a maioria estava do lado que perdeu:

| Skill | Dizia |
|---|---|
| `ship` (tabela) | `feature/* → develop` |
| `dev-maestri` | `feature/<featId>` → `develop` |
| `pilot-feature` (+ `.workflow.js`) | *"UM PR feature→develop"*, `A.target \|\| 'develop'` |
| `release` §1 | em negrito, **inegociável**: `feature/*` → `release/X.Y.0`, nunca develop |

**A `release` ganhou, 1 contra 3.** Prosa repetida não é evidência independente — as três
primeiras herdaram a mesma tabela. O que decidiu: a `release` trata `feature→develop` como
**erro a reparar** (a ação `feature-in` porta só o diff quando a feature "por erro" foi pra
develop). Ninguém escreve procedimento de reparo para o comportamento que recomenda. E o reparo
já existia no `origin`: `feature/59927-resto-on-release-1.5.0`.

A tool ficou **mais estrita que as quatro**: sem minor aberta ela **recusa**, em vez de cair
para develop. Fallback silencioso reintroduz o bug que a regra existe para impedir.

### Contradição 2 — o gate humano da `main` já estava caído

Esta apareceu *por causa* da primeira, e é a mais grave.

**2a.** O passo 8 do `ship` definia auto-merge por **exclusão** (*"tudo que não é develop/main"*).
Seguro enquanto `feature/*` ia para develop; com `feature/* → release/X.Y.0`, a mesma frase
autoriza a automação a concluir merge **dentro do trem que vai para PRD**. Virou allowlist
fail-closed: só `feature/<featId>-*`. **Sem flag de override** — fronteira contornável por
argumento é sugestão.

**2b.** O passo 7 mandava votar `approve` sempre. A `az/SKILL.md` já tinha registrado
(2026-07-31) que na `main` o `Required reviewers` é bloqueante com mínimo 1 e o token é o do
dev — logo **o voto satisfaz a política e destrava o merge**, gravando aprovação humana que o
dev pode não ter dado. O passo 8 parava antes da `main`; o passo 7 removia a única política que
a protegia. Implementado fechado (vota só onde também conclui). **⚠️ Espera ratificação do
Giovanni** — é a única decisão da fase que é política, não leitura.

### A dívida que a tipagem expôs

Os campos custom aparecem na prosa como `Custom.3ef8a79a-…`, `Custom.88184e68-…`,
`Custom.2d6a77b7-…`, `Custom.a5c5ea7e-…`. **O `…` está no texto** — o reference name completo
nunca foi anotado. Em prosa isso passa; numa assinatura, não. As tools emitem esses refs com
sufixo `-INCOMPLETO` e marcam `"incomplete_refs": true`, para ninguém usar achando que está
completo.

## O que existe

Seis tools em bash 3.2, sem node/npm/jq/rede. Contrato: `0` = sim, `1` = a regra nega (resultado
esperado, não bug), `2` = chamada inválida; stdout legível por máquina, prosa em stderr.

| Tool | Função | Puro? |
|---|---|---|
| `ship-may-complete.sh` | `mayComplete(target, verdict)` | sim |
| `ship-may-vote.sh` | `voteFor(target, verdict)` | sim |
| `ship-target.sh` | `target(branch)` | sim |
| `flow-active-release.sh` | a minor aberta | não (lê `git`) |
| `release-version.sh` | `nextVersion` / `tagFor` / `sourceBranch` | sim |
| `az-transition-plan.sh` | escada de estados, campos por degrau, fase→estado | sim |

Rede: `scripts/flow/test.sh` — **56 casos, offline, ~1s**.

`flow-active-release.sh` é o único impuro **de propósito**: é o que mantém `ship-target.sh`
testável sem git. Ela aceita `--last-prd-tag` + `--branches-file` e então não executa git nenhum.

## Aceite

O roadmap pede: *"validar **na autopilot** antes de o corvex consumir"*.

- As cinco skills (`ship`, `release`, `az`, `dev-maestri`, `pilot-feature`) **chamam** as tools
  em vez de repetir as tabelas. É o fluxo que já existe, não um artefato com forma de corvex.
- `pilot-feature.workflow.js` perdeu o `A.target || 'develop'`: default silencioso é a
  contradição virando comportamento. Agora `args.target` é obrigatório.
- A tool pegou estado real na primeira execução: com PRD em `v1.5.9` e `release/1.6.0` ainda não
  cortada, **não há minor aberta** e qualquer `feature/*` é recusada — corretamente.
- `.corvex/recipes/ship.yaml` (7 stages) executa o fluxo pelo runner: gate `policy branch_not`
  em S01 e gate `computational` (mayComplete) em S06. `corvex recipe validate ship` verde.

## Como o corvex enxerga isso — e o que ele NÃO faz

Ele **não descobre tool nenhuma**. Acha *recipes* (`.corvex/recipes/`) e *skills*
(`.corvex/skills/` → symlink em `.claude/skills/`), por convenção. Uma tool existe para ele
porque uma recipe nomeou o `command`. Não há registry, e não deve haver: descobrir é oferecer
escolha ao modelo, e para `mayComplete` o que se quer é o oposto de escolha.

O que dá para fazer é **declarar**: `requires: bin:` aceita caminho, não só nome em PATH
(`exec.LookPath` resolve qualquer nome com barra direto no arquivo — verificado). Com as cinco
tools declaradas, renomear um script falha no **preflight**, antes do primeiro token, em vez de
no S06 depois de um review de 40 minutos já pago. É a cicatriz #1 do roadmap.

*Ressalva (corrigida depois — o mecanismo escrito aqui antes estava errado):* `exec.LookPath`
não busca no PATH quando o nome tem barra: ele faz `stat` do caminho contra o **CWD do
processo**, e o `workDir` que `PreflightRequirements` recebia servia só para achar a recipe.
Os dois lados quebravam: falso-negativo (o script está em `workDir/scripts/deploy.sh`, o check
diz "not on PATH" e recusa um run pronto) e falso-positivo (não está no workDir, está no CWD,
o check libera e grava no ledger um caminho relativo que não significa nada fora daquele CWD).

O cenário de "worktree isolado" que estava escrito aqui **não era o gatilho**. Em toda rota de
produção hoje `workDir == CWD` no instante do preflight: `cmd/run.go` tira o workDir de
`ops.LoadConfig()` (= `os.Getwd()`), o único `os.Chdir` de produção (`cmd/start_worktree.go`)
acontece **antes** do `LoadConfig`, o servidor spawna `corvex run` com `cmd.Dir = WorkDir`, e
rodar `corvex run` de fora do worktree é barrado por `checkWorktreeMismatch`. Ou seja: acertava
por **coincidência**. Os gatilhos reais eram um caller in-process com `workDir != CWD`
(`ops.LoadConfigAt` existe exatamente para isso — "a server handling a request for a repo") ou
qualquer refactor que movesse o preflight para depois de um `Chdir`.

**Consertado** em `internal/ops/preflight.go`: nome sem separador continua PATH, nome relativo
resolve contra o `workDir` — o mesmo diretório que o executor usa (`sh -c` com
`cmd.Dir = workDir`) —, o `Detail` passou a ser caminho absoluto, e arquivo que existe sem bit
de execução passou a dizer "found but not executable" em vez de mentir "not on PATH". Par
controle-positivo/controle-negativo em `preflight_test.go`. Fica de dívida um caso irmão que
**não** é o mesmo bug: `internal/ops/doctor_config.go` usa o mesmo `LookPath` para o binário do
provider, onde workDir não entra na conta.

## Escopo: o que ficou de fora, e por quê

O roadmap escreve *"84 regras em prosa → assinaturas"*. **Não viraram 84 tools, e não deviam.**
O corpo da fase nomeia as três funções puras que a F0 achou, e é isso que está feito, mais uma
quarta (a escada de estados do `/az`, o gotcha que mais quebra na prática).

Fora, com o motivo:

- **`scripts/pr-review.sh`** — é julgamento, não função pura. Continua prompt por natureza.
- **Sonda `validateOnly=true`** — é tool, mas **tier 2**: fala com a rede, não é determinística,
  não cobrível offline. Quando for escrita, tem de encodar o gotcha: detectar pela `.message`,
  nunca por `.id` (a resposta não traz `.id`, então `jq -e '.id'` dá falso-negativo em tudo).
- **Inventário de migration pelo banco** — tier 2, depende de MCP por ambiente.
- **Checklist de altitude do `/az` §6** — a parte mecânica (*"contém termo proibido?"*) daria
  uma tool; a reescrita é semântica.
- **MCPs existentes / AWS CLI** — não tocados.

## Dívidas

- **`ship-may-vote.sh` espera ratificação.** Já está no `develop` com o comportamento fechado —
  mudou o que o `/ship` faz para quem puxar.
- **Classes de branch que a tool não conhece:** o `origin` usa `chore/`, `ci/` e `backmerge/`, e
  a tabela traduzida não cobre nenhuma. Ela recusa fail-closed (a prosa também não cobria; a
  diferença é que agora falha em vez de adivinhar). Decidir o target de `chore/*` e `ci/*` é uma
  linha no `case` mais um teste.
- **A base da feature branch.** `pilot-feature` e `dev-maestri` cortam a `feature/*` de
  `origin/develop`, mas ela passa a mergear em `release/X.Y.0`. Se a develop estiver à frente, o
  PR carrega diff que não é da feature — o caso que `feature-in` conserta portando só o diff.
  Mudar a base é comportamento de dois orquestradores, não regra: decisão do dono do trem.
- **`pr-review.sh` tem veredito binário.** Não expõe 🟡, então o voto é sempre `approve` e a
  distinção `approve-with-suggestions` que a skill descreve não existe na prática.
- **GUIDs `-INCOMPLETO`** (acima).
