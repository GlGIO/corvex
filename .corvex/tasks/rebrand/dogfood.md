# Dogfood — o primeiro `corvex run` de verdade deste repositório

> Recipe `cost-split`, run `run_fb04`, 36 minutos, **falhou**. Escrita como um usuário
> escreveria: a partir do exemplo da `f2-design.md`, sem consultar o código. A tarefa era real —
> a dívida de custo roll-up que duas frentes da F5 e a auditoria acharam sozinhas.

## Por que falhar foi melhor que passar

Um run verde teria confirmado o que os testes já diziam. Este produziu **três defeitos que
nenhuma auditoria de leitura pegaria**, porque os três vivem na fronteira entre o que o código
acredita e o que o mundo faz.

## O que funcionou

- Recipe **validou de primeira**, escrita a partir do documento.
- `run start` compilou a recipe sozinho, e o preflight da F9 passou (`bin: go`).
- Identidade de run visível de outro processo; `run show` desenhando um run **vivo**.
- Telemetria da F5 chegando ao ledger: `phase` em toda linha, ferramenta por **nome só**.
- O **reviewer recusou o trabalho por um motivo correto** — e citou a regra da casa sobre
  golden de comando legado. O gate inferencial fez exatamente o que existe para fazer.
- O ledger deste run está commitado e tem **zero path absoluto**: o conserto da auditoria
  segurou em campo, e nem precisou da camada de redação.

## Os três defeitos

### 1. `tool_result` nunca disparava em produção
O parser despachava por um `type` de topo `"tool_result"`. O CLI manda o resultado como
mensagem **`user`** com um bloco `tool_result` dentro, carregando o `tool_use_id`. O run
produziu **11 linhas `tool_use` e zero `tool_result`**.

A F5 vendeu "início, fim, duração"; em campo só existia o início, `PerTool.DurationMs` era
sempre zero, e a guarda de memória do pareamento estava fazendo todo o trabalho de um recurso
que nunca acontecia. **Parser e teste concordavam um com o outro enquanto o mundo dizia outra
coisa** — a forma exata de defeito que sobrevive a teste verde e a auditoria de código.

De quebra, a dívida do pareamento FIFO estava registrada como *"o conserto certo é carregar o
`tool_use_id`, que o parser já lê e joga fora"*. Ele não lia: ignorava a linha inteira.

### 2. Um run que falha reportava `$0.00`
Trinta e seis minutos, duas tentativas de worker, um reviewer — e a tela dizia zero. Custo só
alcançava o ledger no caminho de sucesso, então a contabilidade ficava cega **exatamente no run
que alguém está tentando entender**. É o diferencial #3 do roadmap ("contabilidade entre runs")
falhando no caso que mais importa.

### 3. `SKIPPED` contava como feito
"4/5 steps" para um run cujo primeiro step falhou e cujos quatro dependentes foram pulados: um
run que não fez nada lendo como quase pronto.

## O que o run também ensinou, e ainda não virou conserto

- **O teto de 20 minutos por task é curto para trabalho real neste repo.** A tentativa 2 estava
  trabalhando (`last activity: tool Bash`) quando o relógio a matou. O teto é de config
  (`execution.task_timeout_minutes`), não de step — e um step `code` que edita Go e um step
  `test` que roda `go test ./...` têm perfis de tempo completamente diferentes. **A recipe não
  tem como dizer isso.** É a próxima coisa que eu faria.
- **O `task_warn` de 5 minutos funcionou** e ninguém estava olhando: a mensagem é para uma tela
  que só a UI da F7 tem.
- **Escrever a recipe foi fácil; calibrar o tamanho da tarefa não foi.** S01 pedia uma mudança
  de contabilidade em três arquivos com quatro critérios — na fronteira do que uma tentativa
  faz sem estourar o relógio.

## O que o reviewer viu, e vale registrar

Ele recusou porque a mudança faria dois goldens do `run show` mudarem de forma — e estava
certo, inclusive sobre o mecanismo (o custo do reviewer, antes invisível, passa a somar em
`PerPhase` e infla o total do gráfico acima do total do cabeçalho). O que ele não tinha como
saber é que essa mudança de golden **era a entrega**, não um vazamento. A regra que ele citou
está escrita para comando **legado**; `run show` é superfície nova da F4.

Isso é instrução para a próxima recipe: quando a mudança implica regravar golden, **a recipe
tem de dizer isso no critério**, senão o gate inferencial vai parar o trabalho por obedecer uma
regra que não se aplica.
