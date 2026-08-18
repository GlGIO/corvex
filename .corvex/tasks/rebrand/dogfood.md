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

---

# Segundo dogfood — o gate humano, ponta a ponta

> Recipe `readme-recipes`, run `run_d458`, 7 minutos, **`done`**. A tarefa era real: a seção de
> recipes do README estava desatualizada e, num ponto, **errada** (descrevia o `human-gate`
> como "para o run, re-rode com `--approve-gates`", que a F2 substituiu há duas fases).

## O que ficou provado

O ciclo inteiro da tese do produto, com processos separados:

1. o run **parkou** no gate humano e continuou vivo;
2. `corvex gate list`, de outro processo, mostrou o gate com o rótulo de leitura obrigatória;
3. `gate show` trouxe a evidência **coletada de verdade** — os dois `from:` rodaram;
4. `gate approve` **sem** `--ack` foi recusado nomeando o que faltava;
5. `gate ack` de outro processo gravou a leitura **com carimbo de hora** (o sensor de "gate
   que vira carimbo": lido às 18:23:43, aprovado depois);
6. `gate approve` destravou, o run terminou sozinho, e a ação guardada aconteceu (a tag existe).

E os consertos do primeiro dogfood apareceram funcionando em campo:
**`tool_result` 9 para 9, todos pareados por id, todos com duração** (`Skill 16ms`,
`Bash 714ms`, `Read 33ms`) — o dado da tela 2d que até ontem não existia em lugar nenhum.

A contabilidade também disse algo que ninguém sabia: **review $1,43 contra worker $0,90**,
mais **$0,64 em tentativas superadas**. O reviewer custa mais que o worker neste repo, e isso
era invisível antes de a F5 ser consertada.

## O achado: a evidência tinha uma âncora que anda

O gate exigia ler `git show --stat HEAD` — a coisa óbvia de se escrever. Mas `auto_commit`
faz checkpoint **de cada step**, então no momento em que o gate do S03 abriu, `HEAD` era o
checkpoint do S02: só a papelada do corvex. **O aprovador foi obrigado a reconhecer que leu um
diff que não continha a mudança que ele estava aprovando.**

É o risco #1 do roadmap ("gate que vira carimbo") chegando por um mecanismo que ninguém
previu: não preguiça de quem aprova, mas evidência cuja âncora desliza por baixo dela. E a
trava fecha do mesmo jeito — o que é pior que não ter trava, porque parece que houve leitura.

**Conserto:** `$CORVEX_RUN_BASE` no ambiente de todo comando de evidência — o commit em que o
run começou, que não se move. A recipe passa a dizer
`git diff --stat $CORVEX_RUN_BASE -- README.md` e significar "tudo que este run mudou".
Documentado no README, onde o autor de recipe olha.

## Atrito registrado, ainda sem conserto

- **`run list` mistura histórico e presente.** Automatizar em cima dele exige filtrar por id:
  meu próprio laço de espera casou com o `failed` de um run anterior e saiu em 3 segundos.
  Um `--status` ou um `--json` filtrável resolveria.
- **O `task_warn` de 5 minutos não tem para onde ir** enquanto ninguém está olhando a UI.
