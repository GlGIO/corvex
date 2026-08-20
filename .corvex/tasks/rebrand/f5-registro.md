# F5 — Telemetria da UI (registro de fecho)

> 🗃️ **REGISTRO HISTÓRICO.** Os números e status deste arquivo valiam quando ele foi
> escrito e **não são estado corrente** — não decida por eles. O estado atual, com a
> instrução de como remedir cada número, está em `fechamento.md`.

> Escrito depois da implementação. A fase foi executada como **fan-out de 4 agentes**, cada um
> num worktree próprio a partir de `rebrand`, com **posse de arquivo disjunta**; a integração
> foi por `cherry-pick` na ordem de dependência (`activity` → `step` → `orchestrator` → `gate/cmd`).

## O achado que define a fase

`activity.Entry.Phase` existe desde antes da F1, documentado como
`worker | review | plan | recovery` — e **nunca ninguém escreveu nele**. Portanto **todo custo em
todo ledger em disco está sem atribuição**. A "barra de custo por natureza" da tela 2f não era
uma agregação faltando: era um dado que nunca foi gravado. O mesmo vale para o relógio da
espera humana (2g) e para a telemetria de ferramenta (2d) — os eventos de `tool_use` existiam
só em memória, para a TUI, e o `emit` os descartava todos.

## O que existe em disco agora

| Dado | Onde | Quem escreve |
|---|---|---|
| Fase de cada evento (`worker`/`review`/`gate`/`validate`/…) | `activity.jsonl`, coluna `phase` | `internal/step` preenche, `orchestrator` copia |
| Início e fim de cada chamada de ferramenta, com duração | `activity.jsonl`, tipos `tool_use`/`tool_result` | `orchestrator.emit` |
| Espera humana de cada gate | `activity.jsonl`, `duration_ms` na linha `gate_decided` | `internal/step/human_gate.go` |
| Marca de leitura de evidência, persistente | arquivo do gate (`.corvex/runs/gates/`, gitignored) | `corvex gate ack` |
| Agregação por fase, por ferramenta e espera humana | `activity.Summary` | `internal/activity` |
| Ambiente do run | record por run (F6) | — |

E as duas superfícies que leem: `corvex run show` imprime a barra por natureza e o relógio
humano; a UI (F7) mostra os mesmos números no detalhe do run.

## Regras que a fase manteve, e por quê

- **Só o NOME da ferramenta vai para o ledger.** `StreamEvent` também carrega o resumo do
  input e o path do arquivo; nenhum dos dois entra na linha `tool_use`. `activity.jsonl` é
  commitado, input de `Read` é path absoluto sob `/Users/<username>`, input de `Bash` é linha
  de comando, e qualquer um dos dois pode carregar segredo exportado.

  > **CORREÇÃO PÓS-FECHO (auditoria).** A frase que estava aqui — *"o teste falha por valor e
  > por chave: contrabandear o path dentro de `message` também fica vermelho"* — **era falsa**.
  > O tripwire só cobria o caminho de stream. Dois outros produtores punham os mesmos bytes em
  > `Entry.Message`: o watchdog (`streamSummary` embutia o resumo do input na mensagem de
  > `task_timeout`) e o laço de tentativa (`err.Error()` cru, com o stderr do provider, na linha
  > de `retry`). Um auditor levou `sk-ant-CANARY111` e `/Users/victim/...` até dentro de um
  > commit que o próprio `auto_commit` criou. Fechado em `4017dcf`: o watchdog publica só o
  > nome, o diagnóstico foi partido em dois campos (`diagnosis` para o prompt, `reason` para a
  > linha), e o ledger passou a redigir path absoluto na entrada como segunda camada — que
  > **não** procura segredo, e diz isso.
- **Texto e chunk continuam descartados.** É regra, não detalhe: uma tentativa emite milhares.
- **Teto de linhas de ferramenta por tarefa (200), não amostragem.** Amostrar enviesa em
  silêncio exatamente os dois consumidores que motivam o dado — um ledger amostrado não
  consegue dizer o que rodou num segundo dado, e totais por ferramenta calculados dele estão
  errados de um jeito que nenhum leitor detecta. Ao bater o teto sai **uma** linha dizendo que
  truncou: o rabo de uma tarefa patológica se perde, mas alto.
- **Custo de disco medido, não adjetivado:** ~110–130 bytes por linha de ferramenta (o teste
  quebra acima de 160). Um run de 30 tarefas vai de ~60 KB para ~230 KB no caso real e ~780 KB
  no pior caso. **Dívida:** o comentário de topo de `internal/activity` ainda promete
  "~200–500 entradas por run", e a primeira metade dessa frase ficou errada.
- **Linha sem fase cai num balde nomeado (`unattributed`), nunca em `worker`.** Dobrar o
  passado no balde mais provável faria a barra mentir sobre runs anteriores à fase. `run show`
  de um ledger pré-F5 mostra literalmente `unattributed`, que é a resposta honesta.

## O defeito que duas frentes independentes acharam sozinhas

`task_complete` de um step de IA carrega **worker + reviewer num número só**
(`internal/step/ai_task.go`), e o `review_result` desse caminho sai sem custo. Consequência: o
dinheiro do reviewer é atribuído ao balde `worker` e a barra da 2f **sub-reporta review**.

Não há dupla contagem, e **desenrolar do lado da leitura é impossível**: `review_result` não
carrega `attempt`, então com retry não dá para casar a linha de review com a tentativa que
sobreviveu. O conserto pertence ao emissor — emitir custo de worker e custo de reviewer como
linhas próprias — e muda o formato do ledger, então **não foi feito aqui**. Registrado nos dois
lugares onde alguém tropeça nele (`internal/activity/summary.go` e `internal/step/phase.go`).

O caminho do gate inferencial já cai certo: `chargeGate` cobra do run e a linha de veredito
carrega o número.

## Controle positivo (mutação, por frente)

| Frente | Mutações | Vermelhos |
|---|---|---|
| fases + relógio humano | 3 | 8 |
| agregação | 5 | 17 |
| ferramenta no ledger | (do próprio autor, no worktree) | — |
| leitura de evidência | (idem) | — |

## Invariantes no fecho

Rede verde: `go test ./...`, `-race` nos quatro pacotes tocados, `./cmd/ -shuffle=on`.
Cobertura **82,8%** no coverpkg do roadmap (piso 60). 4a/4b/6/7/10 em zero; 8 em **150** e
9 em **370** — dois arquivos de `cmd/` passaram do teto durante a fase (`grill_loop.go` 153,
`run_show_render.go` 176) e foram fatiados. Goldens: **1 regravado**
(`run_identity_inspect_task_json`, que ganhou a coluna `phase` — aditivo, é a entrega da fase)
mais os do `run show` que ganharam a barra, e 7 novos vindos da frente de leitura de evidência.

## Flake consertado de passagem

`TestCharacterizeStackEnvFileSourced` falhava sob carga: o app do stub **escreve o dump do
ambiente e só então faz `exec sleep`**, então `stack.Setup` podia voltar (health passou) com
aquela escrita ainda no ar, e o teardown matava o processo antes de ela pousar. Consertado no
lado do teste (espera limitada pelo arquivo ter conteúdo), sem tocar em produção — a mesma
forma que a F-1 já usava para a corrida do drain.

## Dívidas registradas

- **Custo roll-up de worker+reviewer** (acima) — a maior, e a única que faz um número exibido
  estar errado.
- **Tentativa que falha emite `task_complete` sem custo**: o teto é cobrado, o ledger não
  registra. Gasto de retry é invisível em todas as colunas.
- **`duration_ms` está sobrecarregado**: em `gate_decided` é relógio humano, no resto é relógio
  de máquina. Somar a coluna entre tipos de evento inventa wall time.
- **Pareamento de ferramenta é FIFO por tarefa.** Exato para o provider de hoje (uma chamada,
  um resultado); com ferramentas em paralelo a duração de *uma linha* pode ir para a chamada
  errada — o total por tarefa não muda. O conserto é carregar o `tool_use_id`, que o parser já
  lê e joga fora.
- **`gate_decided` como proxy de espera humana** depende de continuar saindo de um lugar só.
- **`internal/planning` não emite evento nenhum**, então `PhasePlan` não tem produtor: atribuir
  custo de planejamento exige injetar um emitter no Planner/Griller — desenho novo, não
  preenchimento de campo.
- **`runInvestigation` descarta `result.CostUSD`**: o gasto do investigador não é cobrado nem
  emitido.
