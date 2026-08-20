# BLOCKED — acionado e **RESOLVIDO**

> ✅ **RESOLVIDO em 2026-08-20 pela decisão do dono.** Este arquivo fica como
> registro de uma condição de parada que foi acionada, medida e resolvida — não
> como bloqueio ativo. Se você chegou aqui procurando o que está travado: **nada
> está**. O estado corrente está em `fechamento.md`.
>
> **A regra que resolveu**, declarada e aceita:
>
> > 🔴 que produz comportamento errado EM EXECUÇÃO bloqueia. Guarda que não
> > morderia um defeito hipotético vira dívida anotada.
>
> Por ela, os achados que restavam eram todos da segunda categoria — verificado um
> por um, e em cada caso o código de hoje está CERTO. Foram anotados com a mutação
> que passa verde e o conserto conhecido em `scripts/flow/README.md`
> §"Dívida conhecida" → *"Cinco guardas que NÃO MORDEM"*.
>
> Correção ao que este arquivo dizia quando foi escrito: ele fala de "10 🔴
> restantes". Eram **cinco** — os outros cinco já haviam sido consertados depois da
> rodada que os achou, e eu não cruzei os horários antes de escrever. O erro está
> aqui em vez de apagado porque é o mesmo modo de falha que o resto do documento
> descreve: número em prosa, sem remedir.

---

# Registro da medição que acionou a parada

> 🗃️ Daqui para baixo é o texto original, mantido porque a medição é o que dá valor
> à decisão. **Os números são daquele momento e não são estado corrente** — inclusive
> o "10 🔴", corrigido acima para cinco.

> 2026-08-20, 05h. Escrito porque o roadmap manda escrever aqui quando uma condição
> de parada é acionada. A condição não estava na lista; é esta, e está MEDIDA.

## A medição

23 commits nesta sessão no smartcare (`57b17f98` → `bece92c6`), cada um com um
achado do gate, controle negativo rodado e colado na mensagem. A suíte foi de
**433 para 468 casos**. O gate de review por IA rodou **21 vezes** no PR #42110,
por eixo. Contagem de 🔴 por rodada:

| eixo | rodadas | 🔴 por rodada |
|---|---|---|
| `fronteira` | 4 | 1 → 1 → 1 → **0** ✅ |
| `tools` | 5 | 2 → 2 → 3 → 2 → **2** |
| `recipes` | 4 | 0 → 1 → 1 → **1** |
| `prosa` | 4 | 4 → 1 → 3 → **2** |
| `testes` | 3 | 4 → 2 → **5** |
| `produto` | 1 | 0 (o eixo ficou com zero arquivos) |
| **total** | **21** | **11 → 10** |

**Onze críticos na primeira rodada, dez na última.** Cinco rodadas de conserto,
uma redução de um. O único eixo que fechou foi o `fronteira` — e ele fechou porque
eu **parei de mexer** nos arquivos dele depois do terceiro conserto.

## Por que não converge — e a causa não é o gate estar errado

Os achados são reais. Verifiquei **cada um** por execução antes de consertar, e
nenhum era falso positivo; um único achado do revisor eu refutei com medição (o
rótulo do 🟡 no `pr-review.sh:413`) e a refutação está no commit.

A causa é a que o próprio handoff já registrava, e que agora se aplica a mim:

> "ao menos 14 de 19 críticos foram introduzidos pela própria leva que os
> consertou, e o dominante foi INTERAÇÃO ENTRE CONSERTOS."

Exemplos desta sessão, todos medidos:

1. Consertei a newline final do `states` → **engoli o `exit 2`** do tipo
   desconhecido (`printf "$( … )"` esconde o status da substituição sob `set -e`).
2. Consertei o `BOARD_PARENT` unbound → **destravei o caminho `Feature`** e os dois
   sítios que interpolam a env como PROSA passaram a gravar `pai #` no board.
3. Consertei a prosa do voto nomeando o eixo do veredito → **errei o eixo do
   target** ("✅ vota E conclui" vale em 1 dos 8 arms).
4. Escrevi um pin para a tabela de classes → ele **grepava o arquivo inteiro**, não
   a tabela; apagar a linha de `feature/*` o deixava verde.
5. Neguei `--active-release` fora do arm que a consome → **matei o `/ship` de
   hotfix** em 5 das 6 classes.

Cada um desses foi pego pelo gate na rodada seguinte. É o sistema funcionando — e é
exatamente por isso que ele não termina: a superfície é grande o bastante para que
cada leva de consertos crie trabalho novo na mesma ordem de grandeza.

## O que eu NÃO fiz, de propósito

Não continuei. Mais uma rodada custaria ~50 min de gate e, pelo histórico medido,
devolveria de 2 a 5 críticos novos — parte deles do lote que eu tinha acabado de
consertar. Continuar sem decidir a regra de parada é gastar o orçamento no lugar
errado.

## O que está de pé, medido

| | |
|---|---|
| `scripts/flow/test.sh` | **468 passaram, 0 falharam** (eram 433) |
| `recipe validate ship` / `board` | 7 tasks / 4 tasks, exit 0 |
| PR #42110 `mergeStatus` | `succeeded`, sem conflito |
| eixo `fronteira` | 🟡 `succeeded` — a fronteira do merge está atestada por execução |
| DAG compilado da `board` | recompilado e commitado (era o defeito que tornava os consertos INERTES num clone) |
| corvex `go test ./... -count=1` | verde · cobertura 82,9% · `gofmt -l` vazio |

E o achado que mais importa da sessão, porque invalidava silenciosamente os outros:
**`.corvex/tasks/board/tasks.md` é versionado e é o que o corvex EXECUTA.** Ele
estava com o corpo pré-conserto, e o corvex não recompila quando a recipe não é
mais nova que o compilado (`internal/ops/run_target.go`, critério de mtime) — o que
num `git clone` é o caso normal, porque os dois arquivos nascem juntos e
`.corvex/recipes/` vem antes por ordem de path.

## A decisão que é do Giovanni, e é uma só

**O `chore/flow-tools` é tooling, não produto.** Ele tem 468 casos verdes, o gate
de produto que ele cria funciona (medido: `produto:2` contra `SEM-EIXO` na develop),
e o eixo que guarda o merge está 🟡. Os 10 🔴 restantes são, na maioria, *guardas
que não morderiam um defeito hipotético* — não defeitos em execução. Três opções:

1. **Mergear com os 🟡/🔴 registrados** e abrir dívida nomeada para o resto. É o que
   eu recomendo: a `develop` está SEM gate de review de produto agora, e cada dia
   nesse estado é um PR de produto sem revisão nenhuma. O custo de esperar é maior
   que o custo dos 10 achados abertos.
2. **Definir uma regra de parada e continuar** — por exemplo: "só 🔴 que produza
   comportamento errado em execução bloqueia; guarda-que-não-morde vira dívida
   anotada". Isso fecharia o laço em uma ou duas rodadas.
3. Continuar sem regra. Medido: não termina.

**Nada disso é meu para decidir** — `ship-may-complete.sh` recusa `develop` por
desenho e não tem override.
