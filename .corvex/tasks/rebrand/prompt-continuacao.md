# Porta de entrada — o rebrand está FECHADO

> Se você é uma sessão nova nesta pasta: **não há trabalho de rebrand pendente.**
> As dez fases (F0–F9) estão concluídas. Este arquivo era um handoff de tarefas com
> números escritos à mão; virou índice, porque a lição desta obra é que número em
> prosa apodrece — e este apodreceu duas vezes antes de eu aprender.

## Onde ler o quê

| se você quer… | vá para |
|---|---|
| **o estado atual, e o COMANDO para medir cada número** | `fechamento.md` — a única fonte de estado desta pasta |
| o desenho: o que se decidiu construir e por quê | `roadmap.md` (documento de desenho, entregue) |
| o registro de uma fase específica | `f*-registro.md`, `f2-design.md`, `f3-cli.md` — todos selados como histórico |
| a auditoria adversarial | `auditoria.md` |
| o primeiro run de verdade, que falhou | `dogfood.md` |
| a condição de parada que foi acionada e resolvida | `BLOCKED.md` |
| **as dívidas de teste ainda abertas, com a mutação medida** | `~/projects/yandeh/smartcare/scripts/flow/README.md` §"Dívida conhecida" |

## As três coisas que valem saber antes de mexer em qualquer coisa

1. **Se o item é executável, execute-o.** Revisão por leitura não acha o que
   execução acha — nove rodadas de review não viram que cinco dos sete steps de uma
   recipe nunca haviam rodado. Uma execução viu.
2. **Não escreva estado do mundo em prosa de guard-rail.** Cinco sítios afirmavam,
   com data, que não havia release aberta; ela foi cortada horas depois e isso mudou
   **qual ramo de código executa**. Escreva o contrato, não o board de hoje.
3. **Controle de uma guarda tem de passar pela PORTA da guarda.** Um controle que
   reimplementa a regra mede a si mesmo, e cegar a regra o deixa verde.

Os seis modos de falha completos, com a medição de cada, estão no `fechamento.md`.

## O que sobrou, e é decisão — não trabalho

Sete decisões de contrato esperando o dono (o `400` em `exit 3`, o `pr-review.sh`
engolindo falha de PR Status, o schema de `plan --json`, …) e um PR em voo no
smartcare. **Lista completa, com o porquê de cada uma, no `fechamento.md`.**

**Não invente fase nova. Não mexa em `main` do smartcare. Não rode migration. Não
conclua merge em linha protegida sem confirmar.**
