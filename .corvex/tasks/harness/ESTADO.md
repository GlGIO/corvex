# Harness — o corvex substituindo /pilot-feature e /pilot-incident

> **Esta é a FONTE DE ESTADO desta obra.** Números aqui valem com o comando que os
> mede ao lado. Prosa sem comando apodrece — a lição está em
> `.corvex/tasks/rebrand/fechamento.md` e não vai ser reaprendida aqui.

## O alvo, em uma frase

O corvex lê o board, monta o DAG, executa em ondas com isolamento por item, e o
humano opera tudo pela UI — sem skill, sem sessão de chat no meio. Hoje, no
SmartCare, isso é a `/pilot-feature` (128 linhas de prosa + 764 de workflow.js).

## Medido em 2026-09-18

| coisa | comando | estado |
|---|---|---|
| suíte do corvex | `go test ./...` | verde |
| navegador de verdade | `CORVEX_REQUIRE_BROWSER=1 go test ./e2e/` | verde (Chrome presente) |
| tools do smartcare | `cd ~/projects/yandeh/smartcare && ./scripts/flow/test.sh` | 472/472 |
| ciclo dispatch→gate→approve→done pela UI | curl na API, recipe de shell puro | funciona, $0.00 |

## Rodada 1 — FECHADA (commit 31ec91a, branch `harness/ui-dispatch`)

Três defeitos, cada um com controle positivo medido antes da correção:

1. **dispatch no diretório errado** — a UI rodava no próprio WorkDir; projeto com
   worktree morria no guard e a tela dizia `ok`. Agora despacha para dentro do
   worktree, com `here` como escotilha explícita.
2. **o dispatch mentia** — run que morre antes de se registrar não deixava rastro
   algum. Agora o `cmd.Wait` grava exit status + a linha `Error:`, existe
   `GET /api/runs/logs/{name}`, a caixa mostra o dispatch morto, e o fingerprint
   do stream o inclui (senão a tela só desenharia quando outra coisa mudasse).
3. **dois workers na mesma árvore** — medido: 3 steps `code` editando o checkout
   ao mesmo tempo numa onda paralela. Agora serializam num lock; tool/test/repro
   continuam sobrepondo.

## Rodada 2 — FECHADA

1. **O run registrado que falha agora diz onde e por quê.** A tela do run nomeia
   o step que a derrubou (`failed at S02`) e TODO step abre: descrição, critérios,
   a linha do tempo do ledger e o tail do que o comando imprimiu (que já existia
   em `?step=` e no CLI, e a tela nunca pedia). Controle positivo: contra a página
   anterior o teste de navegador expira — a tela mostrava só as pílulas PASSED /
   FAILED, sem nada clicável.
2. **Multi-repo no dispatch.** `GET /api/repos` devolve a MESMA lista que a caixa
   de gates já agrega (o repo aberto + os que o índice global viu), o formulário
   escolhe o repositório, as recipes são lidas do repositório escolhido, e o card
   do run carrega o nome do repo. Um repositório que a máquina nunca rodou é
   RECUSADO com a instrução de como entrar na lista — esta superfície existe para
   spawnar um binário, e aceitar caminho livre a transformaria em spawnar em
   qualquer lugar que o corpo do POST nomear.

## Rodada 3 — FECHADA

**Board → DAG existe.** `scripts/flow/az-feature-dag.sh` no smartcare (branch
`flow/feature-dag`, commit `66739581`): lê a Feature, pega as filhas, filtra as
terminais, lê os links de precedência e emite o array JSON com `wave` por story —
o contrato exato do fan-out. 14 casos offline; a suíte deles foi de 472 para 486.

A suíte do smartcare cobrou três integrações no instante em que o arquivo
apareceu no disco (eixo do `pr-review.sh`, inventário de custódia do
`config.yaml`, pin do `--help`). Todas feitas. É a melhor propaganda que aquele
harness tem.

**E o produtor achou um defeito no consumidor**: `wave_by` ordenava as chaves com
`sort.Strings`, então a onda 10 rodava antes da onda 2 — dependência invertida em
silêncio, só em grafos grandes demais para alguém conferir na mão. Corrigido no
corvex (`9d027ab`) com controle positivo.

**NÃO MEDIDO, e é o próximo risco a fechar**: `Dependency-Reverse` = Predecessor
vem da documentação do Azure, não de uma resposta do board do SmartCare. Se o
board não modela precedência, tudo cai na onda 0 (comportamento correto na
ausência de links, e é o que a skill manda perguntar ao humano). **Um `--dry-run`
seguido de um GET de leitura numa Feature real fecha isso em um minuto — precisa
do dono, porque é a identidade dele no Azure.**

## O que falta, na ordem em que eu pretendo atacar
1. **A recipe `pilot.yaml`** — juntar o que já existe: stage que chama a tool,
   fan-out sobre os itens com `wave_by`, template por story, e o `ship` no fim.
   Sem isso as peças existem e ninguém as conecta.
1. **Isolamento por item do fan-out** — um worktree por story. Sem isso, story em
   paralelo é serial (rodada 1) ou perigosa (antes dela).
1. **O que a UI ainda não responde**: quanto um run está custando ENQUANTO roda
   (o card só mostra status/idade), e nada avisa fora da aba — um gate que abre
   com o navegador em outra janela espera o olho humano voltar.

1. **O primeiro run de VERDADE** — com um board real e tokens. Precisa do dono:
   é dinheiro, é a identidade dele no Azure, e é a medição do
   `Dependency-Reverse` que segue não medida.

## Rodada 7 — FECHADA

**A `/pilot-incident` virou recipe** (`incident.yaml`, smartcare `7c97d326`):
contexto+preflight de acesso a PRD → diagnóstico → 🛑 FREIO A na CAUSA →
teste que reproduz → `kind: repro` → conserto → suíte → entrega. Rodei os nove
steps dublados: o freio parou, aprovei pelo CLI com ack, o `before` provou o
defeito, o conserto rodou, o `after` provou que parou.

**E o `repro` tinha um buraco que anulava o propósito dele** (`5fbab26`): com um
defeito que NÃO reproduz, o `before` falhava dizendo "nothing to fix" — e o run
executava o conserto assim mesmo, porque nada no grafo ligava o fixer à pergunta.
Faltava uma aresta. Ninguém tinha rodado o step type desde que ele foi escrito.

Mais o README, que não documentava `wave_by`, `isolate`, `max_parallel`,
`on_item_failure` nem `{{ item.field }}` — tudo entregue nas rodadas anteriores.

## Rodada 8 — FECHADA

**A sonda pós-deploy existe** (`sonda.yaml`, smartcare `0a1dd3b9`): reconsulta o
dado que provou a causa e compara com um número. Três desfechos medidos — ainda
falha (run FALHA com a contagem), parou (gate humano com a reconsulta como
leitura obrigatória), query sem número (exit 2).

**E ela achou um defeito de produto** (`cc3216b`): na SEGUNDA execução, o corvex
pulou o step que já havia passado e foi direto ao gate anunciando sucesso — **com
a query nunca executada**. Resumabilidade é certa para construir e errada para
medir. Agora existe `always: true`: o step e tudo que depende dele voltam a
PENDING em todo run.

**Retry pela UI** na mesma leva: re-executar UM step do card do run ou do detalhe
dele, com recusa 409 se o run está vivo.

## Rodada 9 — FECHADA

**As recipes foram preflightadas contra o repositório REAL do SmartCare**, o que
nunca tinha sido feito (elas só haviam passado por `recipe validate`, que não
olha a máquina). A `pilot` compila e resolve todas as tools aqui; a `incident`
também, com o repro expandido na ordem certa.

**E isso achou o meu próprio erro**: o guard de acesso a PRD que eu escrevera na
`incident.yaml` fazia `grep '^mcp:'` — e a chave do corvex é `mcp_servers:`,
dentro de `sandbox:`. Ele teria RECUSADO um repo configurado certo e PASSADO num
com a chave errada. Virou `requires: - mcp: prd` (`9bfca1e`), conferido contra a
config resolvida — a porta da regra em vez de uma segunda grafia dela.

**Runbook** em `smartcare/.corvex/recipes/README.md`: o que digitar, o que cada
parada significa, o que fazer depois — para as cinco recipes.

## O que falta

1. **O primeiro run de VERDADE** — com board real e tokens. É seu: dinheiro, sua
   identidade no Azure, e a medição do `Dependency-Reverse`.
2. **Declarar o MCP de PRD** no `smartcare/.corvex/config.yaml` (template
   comentado já está lá). Sem ele a `incident` recusa no preflight — de propósito.
3. **A sonda pós-deploy** da `/pilot-incident` (Fase 4) — não cabe dentro do run
   por construção: o deploy é humano. Candidata natural a `corvex` agendado.
4. **Conflito entre stories** — hoje o merge falha e para. A skill delega a um
   agente ("Sincronizar"); isso é decisão de produto, não dívida.

## Regras desta obra

- Todo defeito afirmado tem **controle positivo**: o teste falha contra o código
  antigo, medido, antes de eu dizer que consertei.
- Nada de escrita no Azure, merge em linha protegida, migration, ou run de IA
  pago sem o dono mandar. Isso é decisão, não trabalho.
- Cada rodada termina em commit + este arquivo atualizado.
