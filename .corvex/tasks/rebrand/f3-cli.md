# F3 — Redesenho da superfície de CLI

> 🗃️ **REGISTRO HISTÓRICO.** Os números e status deste arquivo valiam quando ele foi
> escrito e **não são estado corrente** — não decida por eles. O estado atual, com a
> instrução de como remedir cada número, está em `fechamento.md`.

> Entrega da F3: **documento, zero Go**. As decisões abaixo são o que a F4 implementa.
> Escrito sobre a F2 (`a89ab89`). Insumos: `roadmap.md` (superfície de comandos, decisões em
> aberto), `ui-prompt.md` (telas 2a–2h), `f2-design.md` (gates, evidência, fan-out),
> e a superfície real de hoje (`corvex <cmd> --help` de cada comando, lida do binário).

## O que esta fase decide

1. A gramática: quais substantivos existem, quais verbos cada um aceita, e o que vira flag.
2. O destino de **cada** comando de hoje — inclusive os três que a tabela do roadmap não menciona
   (`reset`, `review`, `recipe <name>` compilando).
3. A regra de desambiguação do alias `corvex run <x>`, que é onde o esquema substantivo→verbo
   colide com a compatibilidade prometida no invariante 3.
4. Como o legado morre: o que continua funcionando, por quanto tempo, e **onde** o aviso aparece
   — a decisão que protege os 258 goldens da F-1 de virar churn.
5. O mapeamento tela do canvas → comando (aceite explícito da fase).

**O que ela não decide:** nada sobre HTTP/UI (F7), nada sobre custódia de credencial (F9), e
nenhuma implementação. Onde uma decisão exige mecanismo que não existe (pausa cross-process),
está escrito o que falta e para qual fase vai.

---

## 1. Achados de campo que restringem o desenho

Cada um foi lido do código ou do binário de hoje, não assumido.

### 1a. `run` é `ExactArgs(1)` e o argumento é um projeto
`cmd/run.go:23` — `Use: "run <project>"`, `ValidArgsFunction: completeProjectArg`. Transformar `run`
em substantivo com verbos significa que **cobra resolve o primeiro argumento como subcomando antes
de tratá-lo como projeto**. Um projeto chamado `list` deixa de ser alcançável por `corvex run list`.
Isso não é hipotético: `.corvex/tasks/` aceita qualquer nome de diretório, e `list`, `show` e
`start` são nomes plausíveis de projeto.

### 1b. `status`, `logs` e `inspect` são escopados por **projeto**, não por run
Os três recebem `<project>` e leem `.corvex/tasks/<project>/` (`tasks.md` para `status`/`logs`,
`activity.jsonl` para `inspect`). O `run_id` só existe no ledger a partir da F1, e `FilterByRun`
existe. Ou seja: `run show <id>` é **factível**, mas absorver os três não é renome — é fundir três
visões (DAG, anchor, timeline) numa tela só, com duas chaves de entrada possíveis.

### 1c. `recipe <name>` **compila**; a tabela alvo não tem verbo para isso
Hoje: `corvex recipe <name>` lê `.corvex/recipes/<name>.yaml` e escreve
`.corvex/tasks/<name>/tasks.md`. A tabela do roadmap só prevê `recipe list|show|validate`. Falta
verbo, e faltando verbo o passo desaparece.

### 1d. `tasks.md` é **estado**, não forma
Achado caro da F2 (§17, `persistExpansion`): os status vivem no arquivo, e reescrevê-lo a partir da
memória devolve todo mundo a `PENDING`. Qualquer decisão sobre "recompilar a recipe no start"
esbarra nisso: recompilar = apagar progresso.

### 1e. Sob TUI, um run que falha sai **0**
`cmd/run.go:109-121` — o retorno do `Execute` é descartado e só o erro da TUI sobe. Preservado de
propósito pela F1 (LEI A), com a dívida escrita: *"quando a F3/F4 decidir que run falho sob TUI deve
sair ≠ 0, essa acrobacia desaparece"*. É decisão desta fase.

### 1f. Os goldens da F-1 capturam **stdout e stderr no mesmo transcript**
`cmd/characterize_test.go:591` (`transcript`) concatena os dois blocos, com o comentário explícito de
que isso existe para *"fazer um golden falhar quando a saída migra silenciosamente de stdout para
stderr"*. Consequência direta: **qualquer** linha nova de aviso num comando legado reescreve golden.
Existe uma costura pronta para escapar disso — `isInteractive()` (`cmd/run_render.go:24`), falsa sob
pipe, que é como o harness roda.

### 1g. `--approve-gates` mudou de sentido na F2
Antes: o gate humano **matava** o run e a flag era a única forma de atravessar. Depois: o gate
**parka** e espera decisão de outro processo; a flag agora é "não parke, aprove sozinho". Mesmo nome,
semântica nova — e agora ela é a única forma de rodar recipe com gate humano em CI.

### 1h. Não existe canal cross-process para pausar
A F1 deixou `pid`, heartbeat e `canceling` no record; a F2 criou o primeiro protocolo de arquivo
entre processos (`.corvex/runs/gates/`). Pausa não tem nem um nem outro: `pause` é hoje um
`orchestrator.Command` interno da TUI, no mesmo processo. `kill` tem tudo de que precisa (pid +
machine-id); `pause` precisa de um ponto de leitura no laço.

---

## 2. A gramática

**Três substantivos: `run`, `gate`, `recipe`.** Nada mais vira substantivo nesta fase.

| Substantivo | Verbos | O que é |
|---|---|---|
| `run` | `start` · `list` · `show` · `watch` · `retry` · `kill` · (`pause`, F7) | uma execução |
| `gate` | `list` · `show` · `approve` · `reject` · (`answer`, F5+) | uma decisão pendente |
| `recipe` | `list` · `show` · `validate` · `compile` | um workflow declarado |

**Fora da gramática, e de propósito:** `init`, `doctor`, `plan`, `grill`, `start`, `validate`,
`version`. São verbos soltos porque não pertencem a nenhum dos três substantivos — `init` cria o
`.corvex/`, `doctor` olha a máquina, e `plan`/`grill`/`start` operam sobre um **projeto**, que é
justamente o substantivo que esta fase decide **não** promover (§4).

**Regra de crescimento (herdada do roadmap, agora com teste):** comando novo precisa responder *"por
que isto não é uma flag?"*. A resposta só é aceitável se o comando tiver **verbo próprio sobre um dos
três substantivos** ou for uma operação de máquina (`init`, `doctor`). Do contrário é flag.

---

## 3. Tabela completa antiga → nova

Toda linha de `corvex --help` de hoje aparece aqui. "Legado" = continua funcionando, escondido do
help, byte-idêntico sob pipe (§6).

| Hoje | Alvo | Destino do antigo |
|---|---|---|
| `run <project>` | `run start <recipe\|project>` | **alias permanente** — `corvex run <x>` = `run start <x>` |
| `list` | `run list --projects` | legado |
| `status <project>` | `run show <project>` | legado |
| `inspect <project>` | `run show <project>` (mesma tela) | legado |
| `inspect <project> --task S05` | `run show <project> --step S05` | legado |
| `logs <project> [task]` | `run show <project> [--step S05]` | legado |
| `reset <project> <task>` | `run retry <project> --step S05` | legado |
| `review [project]` | `gate list` (unificada) · `gate show` | legado |
| `recipe <name>` | `recipe compile <name>` | legado |
| — | `run watch <id\|project>` | novo |
| — | `run kill <id>` | novo |
| — | `recipe list` · `recipe show <name>` · `recipe validate <name>` | novo |
| `gate list\|show\|approve\|reject` | idem (F2 já entregou) | — |
| `init`, `doctor`, `plan`, `grill`, `start`, `validate`, `version` | inalterados | — |

**`gate show` fica com o nome que a F2 deu.** Era a dívida aberta ("a F3 pode renomear"): não
renomeio. `show` é o verbo que `run` e `recipe` também usam para "me mostre um", e trocar por
`gate read`/`gate detail` gastaria a única chance de ter um verbo com sentido idêntico nos três
substantivos.

**`gate answer` continua reservado** (decisão em aberto #3 do roadmap, mantida): o eixo é o agente
perguntando ao humano, que não existe em disco até a F5 persistir o evento. Nome fixado aqui, corpo
depois.

---

## 4. Decisões, uma a uma

### D1 — O alias `corvex run <x>`: verbo vence, com escape hatch nomeado
`corvex run <x>` continua sendo `run start <x>` (invariante 3, caminho quente). Quando `<x>` for
exatamente um verbo registrado (`start|list|show|watch|retry|kill|pause`), **o verbo vence**.

*Por quê:* a alternativa (projeto vence, verbo precisa de prefixo) transforma o caminho novo em
segunda classe e torna o comportamento dependente do conteúdo de `.corvex/tasks/` — `corvex run list`
faria coisas diferentes em dois repositórios. Ambiguidade resolvida por dado é a pior das duas.

*Custo aceito:* um projeto ou recipe chamado `list` perde o atalho. **Escape hatch:** `corvex run
start list` sempre funciona e nunca é ambíguo — o verbo já foi consumido. A F4 imprime exatamente
essa linha quando detecta um projeto com nome de verbo, em vez de falhar em silêncio.

### D2 — `run show` aceita as duas chaves, e diz qual usou
`run show <run_id>` e `run show <project>` são a mesma tela. Regra: casa `^run_[0-9a-f]{4}$` → id;
senão, projeto (última execução).

*Por quê:* exigir id quebra o caminho legado na prática — quem roda `spec.md` nunca decorou um id, e
linhas de ledger pré-F1 não têm nenhum. Aceitar os dois é o que faz `status`/`inspect` poderem morrer
sem levar o fluxo antigo junto.

*Custo aceito:* um projeto chamado `run_ab12` é lido como id. Aceitável — o formato é sintético e
nenhum humano nomeia projeto assim.

*Reciclagem de id (dívida da F1):* ids têm 4 dígitos e **reciclam**. `run show <id>` resolve para o
mais recente com aquele id e imprime `started_at` na primeira linha — a UI da F7 recebe a mesma
regra. Não cresço o id: o custo real do reuso é ambiguidade visual, e `started_at` a resolve por 4
caracteres em vez de por 8 em toda invocação.

### D3 — `status` + `inspect` + `logs` viram **uma** tela, não três renomeadas
`run show` imprime, em ordem: cabeçalho de identidade (id, projeto/recipe, repo, quando, custo,
estado) → DAG com status por step (o que `status` dava) → linha do tempo com duração/retries/custo
(o que `inspect` dava). `--step S05` troca o corpo pelo detalhe daquele step: descrição, critérios e
arquivos (o que `logs` dava) **mais** o fluxo de eventos (o que `inspect --task` dava).

*Por quê:* "cinco formas de olhar a mesma coisa" é o defeito que a fase existe para corrigir; três
comandos renomeados para `run show-dag`, `run show-timeline` e `run show-step` seriam o mesmo defeito
com prefixo novo. A pergunta do usuário é uma só — *"o que aconteceu nesse run?"* — e a resposta cabe
numa tela com seções.

*Custo aceito:* a saída fica mais longa que qualquer uma das três de hoje. `--json` continua sendo o
caminho de máquina e `--step` é o corte quando a tela cansa.

### D4 — `recipe compile` existe; `run start` compila **só quando não há o que perder**
`recipe compile <name>` é o comando de hoje, com nome. Além disso, `run start <name>` resolve nesta
ordem: (1) existe `.corvex/tasks/<name>/tasks.md` → executa; (2) existe `.corvex/recipes/<name>.yaml`
→ compila e executa.

Se **os dois** existem e a recipe mudou desde a compilação, `run start` **para** e pede
`--recompile` (que recompila e **reseta o DAG**, dizendo quantos steps `PASSED` serão perdidos).

*Por quê:* achado 1d — `tasks.md` é estado. Recompilar por conveniência apagaria progresso em
silêncio, que é exatamente o defeito que a F2 encontrou em si mesma. A forma escolhida é a mesma que
o caminho `spec.md` já usa (`plan --reanchor` para aceitar drift sem replanejar): o usuário decide,
o runner não adivinha.

### D5 — `reset` vira `run retry --step`
`corvex run retry <id|project> --step S05` marca o step como `PENDING` **e** executa só ele
(equivale ao `reset` + `run --task S05` de hoje). `--no-run` marca e para.

*Por quê:* é o diferencial #4 do roadmap ("re-execução de um estágio isolado sem refazer o resto")
tendo um comando pela primeira vez. `reset` descrevia o efeito interno (mudar uma palavra no
`tasks.md`); `retry` descreve o que o usuário quer.

*Custo aceito:* `retry` passa a gastar dinheiro, o que `reset` não fazia. Por isso ele herda a
confirmação de custo do `run start` e o `-y`.

### D6 — `review` é absorvido por `gate list`
Escalation é, literalmente, uma decisão parada esperando uma pessoa — a mesma caixa da tela 2a.
`gate list` passa a ter coluna `kind` (`human` | `escalation`), ordenada por idade. `gate show`
imprime o markdown da escalation como evidência. **`gate approve` numa escalation** significa
"resolvi o problema de fora": apaga o arquivo e devolve o step a `PENDING`. `gate reject` significa
"não vou resolver": apaga o arquivo e deixa o step `FAILED`.

*Por quê:* manter `review` seria um quarto substantivo para uma listagem, e a regra de crescimento
não paga isso. Mais importante: hoje a resolução de escalation é *"apague o arquivo na mão e rode de
novo"* — instrução que só existe no texto de ajuda. Absorver dá corpo a ela.

*Risco nomeado:* é a única decisão desta fase que **muda um fluxo**, não só um nome. Se a F4 achar
que `gate approve` numa escalation é ambíguo demais ao lado de aprovar migration, o fallback é
`gate resolve <id>` — verbo a mais, mesmo substantivo. Registrado, não escolhido.

### D7 — O legado morre **em silêncio sob pipe** e falando no terminal
Todo comando legado (§3) continua funcionando, some do `--help` (`Hidden: true`), e imprime **uma
linha** em stderr — `deprecated: use "corvex run show <project>"` — **somente quando
`isInteractive()`**.

*Por quê:* achado 1f. Os goldens da F-1 comparam stdout **e** stderr, e o harness roda sob pipe.
Avisar sempre reescreveria dezenas de goldens e destruiria a rede como oráculo justamente na fase que
mais precisa dela — a que renomeia tudo. Avisar só no TTY entrega o aviso a quem pode agir (um
humano) e mantém a rede byte-idêntica para quem não pode (um teste, um pipe, um script).

*Custo aceito:* um script que roda `corvex status` num TTY nunca é avisado por logs; e um humano que
faz `corvex status x | less` também não. Menos ruim que o inverso.

*Prazo:* os legados saem na **v3** (marco F5–F7). Escrito aqui para a remoção não ser surpresa.

### D8 — Sob TUI, run falho passa a sair ≠ 0
`run start` devolve o erro do run mesmo quando a TUI desenhou.

*Por quê:* é a dívida 1e, e o motivo dela ("a falha está na tela") deixa de valer quando a UI da F7
puder disparar o run — nesse mundo ninguém está olhando a tela. Exit code é o único canal que
atravessa.

*Custo:* zero em teste (nenhum teste passa por `runWithTUI`: `isInteractive()` é falso sob pipe) e
visível no terminal. É mudança de comportamento **declarada**, não silenciosa, e é a única desta
fase além da D6.

### D9 — `--approve-gates` **não** vira `-y`, e mantém o nome
Continuam sendo duas coisas: `-y/--yes` pula a confirmação de **custo**; `--approve-gates` aprova
**gate humano** sem parkar.

*Por quê:* fundir faria todo script de CI que já usa `-y` passar a aprovar migration em produção.
É a diferença entre "sei quanto custa" e "aprovo o que a migration faz". Renomear (`--auto-approve-gates`)
foi considerado e recusado: o nome atual já está em scripts e a semântica nova (§1g) continua
descrita por ele. **Mexer menos.**

*Acréscimo da F4:* toda aprovação automática por essa flag é gravada no ledger como gate de natureza
`policy` com o motivo `--approve-gates`, para o risco #1 do roadmap ("gate que vira carimbo") ser
mensurável em vez de invisível.

### D10 — Escopo é flag, e o default é cross-repo onde o índice já é
`run list` e `gate list` leem o índice global e mostram **todos os repositórios** (é a tela 2a/2e).
`--repo .` limita ao repositório atual. `run show`/`watch` resolvem pelo índice e não pedem escopo.

*Por quê:* a F1 construiu o índice global exatamente para isso, e a pergunta do produto
(*"o que está esperando por mim"*) não tem fronteira de repo. O caso escopado existe e é uma flag.

### D11 — `run list --projects` é onde a listagem de projetos sobrevive
`run list` lista **runs** (default: 7 dias, todos os repos). `--projects` troca a unidade da linha
para **projeto** (nome, último run, estado do DAG, custo acumulado) — a listagem de hoje, enriquecida.

*Por quê:* um projeto planejado e nunca executado é um estado real (`corvex plan x` e nada mais), e
`run list` sozinho não o mostraria. Promover `project` a substantivo gastaria a regra de crescimento
numa listagem. Uma flag que troca a unidade da linha é o menor preço, e fica visível no help.

*Custo aceito:* é a única flag do desenho que muda a **forma** da linha, não o filtro. Nomeada aqui
para não virar precedente.

### D12 — `run watch` é leitor, não TUI
`run watch <id|project>` é o `run show` redesenhado a cada segundo, alimentado por tail do
`activity.jsonl` + record. **Não** é a TUI de hoje, que é dona do run e vive no mesmo processo.

*Por quê:* watch é por definição de outro processo (o run pode ter sido disparado pela UI ou por
outro terminal), e o único stream que atravessa processo é o que está em disco. Reusar a TUI exigiria
um canal de eventos cross-process que não existe e que o roadmap não pede.

### D13 — `run kill` na F4; `run pause` fica para a F7, com o motivo escrito
`run kill <id>`: SIGTERM ao pid do record, **exigindo `machine` igual** (a F1 já ensinou que pid de
outra máquina não significa nada) e recusando run não-vivo. O caminho de cancelamento já existe e
grava `canceling` (F1).

`run pause` **não** entra na F4. Falta o que a F2 construiu para gates e ninguém construiu para
pausa: um arquivo de controle e um ponto de leitura no laço. O lugar certo é a barreira pós-`wg.Wait()`
— a mesma janela em que a F2 expande o DAG. Fazer isso agora seria abrir a terceira mudança de
comportamento da fase por um botão que só a UI usa.

*Custo aceito:* a tela 2d perde um botão até a F7. `kill` cobre o caso destrutivo, que é o urgente.

---

## 5. Flags — convenções que valem para toda a superfície

| Flag | Onde | Sentido |
|---|---|---|
| `--json` | todo comando de leitura | saída de máquina; forma estável, é contrato |
| `--repo <path>` | `run list`, `gate list` | limita ao repositório; default é cross-repo |
| `--since <dur>` | `run list` | janela (default `7d`) |
| `--step <id>` | `run show/retry`, `gate *` | corta para um step |
| `-y, --yes` | `run start/retry` | pula confirmação de **custo** |
| `--approve-gates` | `run start/retry` | aprova gate humano sem parkar (D9) |
| `--plain` | `run start/retry` | desliga TUI |
| `-q, --quiet`, `--no-color` | globais | inalterados |

Tudo em **inglês**, inclusive mensagens de erro e help. (Documento em PT-BR; produto em inglês —
regra do roadmap.)

---

## 6. Compatibilidade — o contrato exato

1. **`corvex run <project>` funciona igual** (invariante 3). É alias permanente, não depreciado.
2. Todo comando legado da §3 continua funcionando, **byte-idêntico sob pipe**, escondido do help,
   com aviso só em TTY (D7).
3. Nenhum golden da F-1 pode mudar por causa de comando legado. Golden novo é permitido e esperado
   para comando novo. **`harness_root_help` muda** (é o help do topo, e o topo mudou) — junto com
   os goldens de help dos comandos que ganharam subverbo.
4. `--json` de comando legado é forma congelada: quem depende dela não é afetado por D3.

**Esta é a primeira fase em que reescrever golden é entrega, não sintoma** — e é por isso que a
regra acima separa os dois casos por origem (comando novo = golden novo; comando legado = byte
idêntico). Se um golden legado precisar mudar, a mudança vazou para onde não devia: **PARE**.

---

## 7. Telas do canvas → comando (aceite da fase)

| Tela | O que é | Comando equivalente |
|---|---|---|
| **2a** caixa de gates | o que espera por mim, cross-repo | `corvex gate list` (alias `corvex gates`) |
| **2b** aprovar gate | evidência + trava de leitura | `corvex gate show <id>` → `gate approve <id> --ack "<label>"` |
| **2c** disparar run | escolher workflow, alvo, teto | `corvex run start <recipe> --repo --branch` |
| **2d** run ao vivo | step, onda, custo agora | `corvex run watch <id>` (pausa: F7 — D13) |
| **2e** histórico | tudo que rodou, todos os repos | `corvex run list --since 30d` |
| **2f** run encerrado | sequência, gates, retries, custo | `corvex run show <id>` |
| **2g** pergunta no meio | agente precisa de decisão | `corvex gate answer <id> --choice C` (nome fixado; corpo na F5+) |
| **2h** ⌘K | ação de UI vira comando | é a própria tabela acima — toda linha é executável no terminal |

A tela 2h é o teste da fase: se alguma linha não tivesse comando, a regra de paridade estaria
quebrada antes de a UI existir. Só a 2d fica parcial, com motivo escrito (D13).

---

## 8. O que a F4 implementa, em ordem

Ordem escolhida por dependência e por risco decrescente:

1. **Esqueleto de substantivos** — `run`, `recipe` com verbos; legados escondidos + aviso em TTY
   (D7). Nada de lógica nova. É aqui que `harness_root_help` é regravado, uma vez só.
2. **`run list` / `run show` / `run list --projects`** — leitura pura sobre índice (F1) + ledger.
   Absorve `status`/`inspect`/`logs` (D3).
3. **`run start`** — alias, desambiguação (D1), resolução recipe×projeto (D4), exit code (D8).
4. **`recipe list|show|validate|compile`**.
5. **`run retry`** (D5) e **`run kill`** (D13).
6. **`run watch`** (D12).
7. **`gate list` unificado com escalation** (D6) — última porque é a única que muda fluxo.
8. **Completion dinâmica** e **sugestão do próximo comando** (itens próprios do roadmap na F4).

**Aceite mecânico da F4:**
- `go test ./...` verde; cobertura ≥ 60% no coverpkg do roadmap.
- `corvex run <project>` legado byte-idêntico ao golden da F-1 (invariante 3, provado com binário).
- Todo comando da tabela §7 existe e responde no terminal.
- Nenhum golden legado alterado; `harness_root_help` e os helps novos, sim.
- Invariantes 4a/4b/5/6/7/10 em zero; 8 (≤150) e 9 (≤400) respeitados.

---

## 9. Riscos e o que eu cortaria

**O ponto mais fraco deste desenho é a D3** — fundir três telas em uma. As três existem hoje e cada
uma tem golden; a tela nova é a única parte da F4 que não tem oráculo prévio, porque não existe nada
byte-idêntico com que comparar. Mitigação: as três legadas continuam vivas e testadas, então a
comparação é possível **manualmente** no aceite (mesmo fixture, três saídas antigas × uma nova) e a
divergência é achado, não regressão silenciosa.

Dois menores:
- **D6 muda fluxo.** É a única mudança de comportamento sem golden que a proteja (o `review` de hoje
  é read-only). Por isso é o último item da ordem — se o orçamento acabar, ela cai inteira e o
  `review` legado sobrevive mais uma fase.
- **D1 é resolvida por precedência, e precedência é invisível.** O usuário só descobre quando tem um
  projeto chamado `show`. A F4 imprime o escape hatch nesse caso exato; sem isso, a decisão é uma
  armadilha bem documentada.

**O que eu cortaria se o escopo apertar**, em ordem: (1) `run watch` — é a única tela que a TUI já
cobre parcialmente para quem disparou o run; (2) `gate list` unificado (D6); (3) `recipe show`.
**Não cortaria** `run list`/`run show`: sem elas a fase não entrega a tela 2a/2e/2f e a F7 nasce sem
nada para chamar.

---

## 10. Dívidas que este desenho deixa registradas

- ~~**`run pause` sem mecanismo**~~ (D13) — **pago no fechamento** (`5d03e80`): arquivo de
  controle em `RecordsDir` + leitura no topo de `walkDAG`, antes do `expandFanouts`. Foi
  implementado exatamente como o desenho desta seção previa. Ver `fechamento.md`.
  Fase: F7 (ou F4b se a UI antecipar).
- **`gate answer` é só um nome** — o eixo "agente pergunta" não tem evento em disco até a F5.
- **Reciclagem de id continua** (dívida da F1, revisitada aqui): mitigada por `started_at` na saída,
  não resolvida. Se a F5 mostrar colisão real na prática, o id cresce — e aí `run show` precisa
  aceitar prefixo.
- **`--projects` troca a unidade da linha** (D11). Precedente que eu não quero repetir; se aparecer
  um segundo caso, é sinal de que `project` merecia ser substantivo e a decisão deve ser revista.
- **Aviso de depreciação não alcança pipe** (D7). Consciente. Quando os legados forem removidos na
  v3, quem só usa pipe descobre pela quebra — por isso o prazo está escrito aqui e não só no help.
- **A tela nova da D3 não tem oráculo prévio** (§9). A F4 nasce com goldens próprios dela; a
  comparação com as três antigas é manual e feita uma vez.
