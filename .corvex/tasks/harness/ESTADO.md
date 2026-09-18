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

## O que falta, na ordem em que eu pretendo atacar

1. **Motivo da falha de um run REGISTRADO** — a correção da rodada 1 cobre o
   dispatch que morre cedo. Um run que registra e falha depois ainda mostra
   `failed` sem dizer por quê.
2. **Multi-repo na UI** — o histórico já é global (`~/.corvex/runs.jsonl`); o
   DISPATCH é preso ao WorkDir. Para "coordeno vários repos de uma tela" falta
   uma lista de repos conhecidos e um dispatch que aceite qual.
3. **Board → DAG** — não existe tool que leia uma Feature e devolva as stories
   folha com dependências. O contrato do fan-out (`produces: items` + array JSON
   + `wave_by`) é o formato que essa tool tem de emitir. Vive no smartcare.
4. **Isolamento por item do fan-out** — um worktree por story. Sem isso, story em
   paralelo é serial (rodada 1) ou perigosa (antes dela).

## Regras desta obra

- Todo defeito afirmado tem **controle positivo**: o teste falha contra o código
  antigo, medido, antes de eu dizer que consertei.
- Nada de escrita no Azure, merge em linha protegida, migration, ou run de IA
  pago sem o dono mandar. Isso é decisão, não trabalho.
- Cada rodada termina em commit + este arquivo atualizado.
