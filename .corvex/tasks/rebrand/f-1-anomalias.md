# F-1 — Anomalias congeladas pela rede de caracterizacao

> **LEIA ISTO PRIMEIRO.**
>
> Tudo neste documento descreve comportamento **CONGELADO** pelos golden tests de
> `cmd/`, **NAO CONSERTADO**. A F-1 e uma rede de seguranca: ela grava o que a CLI
> faz HOJE, inclusive o que esta errado. Um golden que registra saida errada esta
> **correto** para os fins desta fase — e exatamente essa propriedade que permite a
> F0 provar "zero mudanca de comportamento" contra o comportamento real, e nao
> contra o comportamento ideal.
>
> **Consertar qualquer item desta lista e decisao posterior, fora da F-1 e fora da
> F0.** Quando um destes bugs for corrigido de proposito, o golden correspondente
> vai ficar vermelho — isso e o sistema funcionando. Regrave o golden no mesmo
> commit da correcao, nunca antes.
>
> Zero arquivo de producao foi alterado nesta fase (`git status`: 257 adicoes, 0
> modificacoes).

## Nota de procedencia

Os cinco agentes de caracterizacao reportaram seus achados em campos
`bugsObserved` estruturados, mas **esses campos nao chegaram no manifesto entregue
ao passo de integracao** (o manifesto trouxe `filesCreated`, `commandsCovered`,
`gaps` e `productionFilesTouched`). As anomalias abaixo foram reconstruidas a
partir de (a) as anotacoes que os agentes deixaram nos proprios arquivos de teste,
(b) as referencias cruzadas "see bugsObserved" dentro desses arquivos, e (c)
conferencia de cada `arquivo:linha` contra o codigo de producao.

Consequencia honesta: esta lista e **verificada mas possivelmente incompleta**. As
numeracoes originais dos agentes (ex. "bugsObserved #9") sobrevivem apenas como
referencia nos comentarios dos testes. Se a lista completa for necessaria, ela
esta nos transcripts dos cinco agentes, nao aqui.

---

## cmd/run.go

### Renderer nunca e drenado ate o fim
- **Local:** `cmd/run.go:171` — `go renderer.Drain(events)`
- **Observado:** a goroutine de drain nunca e aguardada. `runRun` retorna assim que
  `orc.Run` termina, entao as linhas do `PlainRenderer` chegam ou nao ao stdout
  conforme o escalonador do Go.
- **Esperado:** o drain deveria ser aguardado (WaitGroup / canal de conclusao) antes
  do retorno, para que a saida do run seja deterministica.
- **Impacto na rede:** por causa disso o stdout do renderer **nao esta dentro de
  nenhum golden**. Ele e verificado por `runAssertRendererLines`, que exige apenas
  que o que chegou seja PREFIXO exato do esperado. Uma mudanca de texto/ordem das
  linhas e pega; uma regressao que silencie o renderer por completo **passaria**.

### `--dry-run` ignora silenciosamente `--task`
- **Local:** `cmd/run.go` (ramo de dry-run, antes do orquestrador ver `TargetTask`)
- **Observado:** `corvex run <p> --dry-run --task S99` imprime o DAG completo. O
  `--task` e descartado sem aviso, inclusive quando o ID nem existe.
- **Esperado:** ou respeitar o escopo (`--task`) no preview, ou recusar a combinacao
  com erro explicito.
- **Golden:** `cmd/testdata/golden/run_dryrun_ignores_task.txt`

### Replan renomeia tasks.md ANTES de planejar; se o planner falha, o arquivo se perde
- **Local:** `cmd/run.go` (replan) — backup `tasks.md` → `tasks.md.bak-<timestamp>`
- **Observado:** o backup acontece antes do planner rodar. Quando o planner falha, o
  projeto fica **sem `tasks.md` nenhum**, e a mensagem de erro nao menciona isso.
- **Esperado:** escrever o novo `tasks.md` so depois do sucesso do planner (write
  temporario + rename atomico), ou restaurar o backup no caminho de falha.
- **Golden:** `cmd/testdata/golden/run_replan_backup_side_effect.txt` (grava o
  estado do diretorio depois da falha)

### Linha de falha termina em `"failed . "` com separador pendurado
- **Local:** `cmd/PlainRenderer` (evento de falha sem `Message`)
- **Observado:** o renderer imprime o separador `.` e um espaco final
  incondicionalmente; quando o evento de falha nao carrega `Message`, sai
  literalmente `failed . ` (com espaco a direita).
- **Esperado:** omitir separador e espaco quando nao ha mensagem.

### `--force` descarta trabalho nao commitado em SILENCIO
- **Local:** `cmd/PlainRenderer` — sem `case` para `EventRecoveryResult`
- **Observado:** o orquestrador emite `EventRecoveryResult` com
  `"--force: discarded N uncommitted change(s)"`, mas o `PlainRenderer` nao trata
  esse evento. O passo **destrutivo** nao aparece na saida.
- **Esperado:** um passo que descarta alteracoes do usuario deve ser sempre visivel.
- **Golden:** `cmd/testdata/golden/run_dirty_tree_force.txt`

---

## cmd/status.go

### Truncagem de titulo corta rune UTF-8 no meio (mojibake)
- **Local:** `cmd/status.go:155` — `title = title[:maxTitleLen-1] + "…"`
- **Observado:** o corte e por **byte**, nao por rune. Com titulo acentuado, o byte
  39 cai dentro do `ç` de "configuração" e a saida ganha o caractere de
  substituicao: `Migração para o padrão de configura�…`
- **Esperado:** cortar por rune (`[]rune(title)[:n]`) ou usar largura de display.
- **Golden:** `cmd/testdata/golden/statuslogs_status_split_rune_title.txt` (o
  golden contem o byte invalido de proposito)
- **Nota:** o mesmo bug de medicao aparece em `cmd/status.go:138-142`, onde
  `len(t.Title)` mede bytes para calcular a largura da coluna.

---

## cmd/inspect.go

### Titulo truncado em 50 bytes (mesmo mojibake) — so no modo humano
- **Local:** `cmd/inspect.go:209-210` — `title = title[:49] + "…"`
- **Observado:** corte por byte outra vez; titulo acentuado sai corrompido.
- **Esperado:** corte por rune.
- **Golden:** par `doctorinspect_inspect_human_all_statuses.txt` (corrompido) vs
  `doctorinspect_inspect_json_all_statuses.txt` (intacto) — o par prova que o corte
  e problema de renderizacao, nao de dados.

### Coluna de status desalinha porque `%-8s` conta runes, nao celulas
- **Local:** `cmd/inspect.go:212` — `fmt.Printf("%-5s %-8s ...", s.ID, statusGlyph, ...)`
- **Observado:** o glyph de status e um emoji de **duas celulas** de terminal, mas
  `%-8s` faz padding contando runes. As colunas nao alinham.
- **Esperado:** padding por largura de display (ex. `go-runewidth`).
- **Golden:** `doctorinspect_inspect_human_all_statuses.txt`

### Entrada de ledger para task ausente do tasks.md e descartada em silencio
- **Local:** `cmd/inspect.go` (join ledger × tasks)
- **Observado:** uma entry `task_complete` com `TaskID: S99` que nao existe no
  `tasks.md` e ignorada por completo — o custo dela (no fixture, $99.99) **nao
  entra em nenhum total**.
- **Esperado:** no minimo contabilizar o custo, ou avisar sobre a entry orfa.

---

## cmd/validate.go

### Teardown mata so o filho direto — netos sobrevivem e seguram a porta
- **Local:** `cmd/validate.go:359` — `appCmd.Process.Kill()`
- **Observado:** o cleanup **nao** e um kill de process group. Um `start_command`
  que gera seu proprio worker (`npm run start` → node, `sh -c "… &"`) deixa esse
  worker rodando depois do teardown — e segurando a porta, o que faz a proxima
  execucao falhar no preflight.
- **Esperado:** matar o grupo de processos (`Setpgid` + `Kill(-pgid)`).
- **Golden:** `cmd/testdata/golden/validate_stack_teardown_grandchild.txt` — a
  assertion **codifica o bug de proposito**, para que a F0 nao possa mudar a
  semantica de teardown sem ser notada.

### `portInUse` binda wildcard e nao ve servidor em 127.0.0.1 (no darwin)
- **Local:** `cmd/validate.go` — `portInUse`
- **Observado:** `portInUse` binda o endereco **wildcard**. Se ha um dev server
  bindado apenas em `127.0.0.1`, o darwin permite o bind do wildcard e `portInUse`
  responde `false`. Ou seja: no darwin o preflight **nao ve** o caso comum que ele
  foi escrito para pegar. No Linux o kernel recusa e o preflight funciona.
- **Esperado:** testar tambem `127.0.0.1:<port>`.
- **Por que nao esta em golden:** a diferenca e politica de kernel; um golden unico
  flakaria entre plataformas. Esta assertado por SO (`t.Errorf` so no darwin) em
  `TestCharacterizePortInUse`. **E o comportamento mais importante do grupo e o
  unico que nao deu para congelar em golden.**

### Porta 0 nunca e reportada como em uso
- **Local:** `cmd/validate.go` — `portInUse` / preflight de `setupValidationStack`
- **Observado:** `portInUse(0)` e sempre `false`, porque bindar `:0` sempre da certo.
  O preflight compensa guardando em `Port != 0` em vez de confiar em `portInUse`.
- **Esperado:** `portInUse` deveria rejeitar 0 como entrada invalida.

### Resposta invalida no wizard zera `ready_timeout` e compra 30s de espera calada
- **Local:** `cmd/validate.go:202` — `parseIntDefault(value, v.Stack.ReadyTimeout)`,
  combinado com `cmd/validate.go:496-499`
- **Observado:** o wizard mostra um palpite (`30`) para `stack.ready_timeout`. Se o
  usuario responde algo nao numerico, `parseIntDefault` cai no **valor atual do
  config (0)**, nao no palpite exibido. Depois, `waitForHealth` transforma 0 em 30s
  de default implicito. O usuario ve "30" na tela, responde lixo, e leva 30s de
  espera sem nunca ver esse numero ter sido gravado.
- **Esperado:** cair no palpite exibido, ou reperguntar.
- **Golden:** `cmd/testdata/golden/validate_confirm_uncertain.txt`
- **Nota:** `stack.bogus` (campo que `applyFieldOverride` nao conhece) e respondido
  pelo usuario e **descartado sem aviso** — mesma familia de problema.

---

## internal/orchestrator/worker.go

### `AZURE_` residual no filtro de env
- **Local:** `internal/orchestrator/worker.go:179` — `"AZURE_"`
- **Observado:** unica ocorrencia de dominio antigo que sobrou no codigo de
  producao (`cmd` + `internal`, fora de testes e comentarios).
- **Esperado:** removido pela F0.
- **Status:** deixado no lugar de proposito nesta fase. E o valor 1 esperado do
  grep de invariante do roadmap.

---

# Lacunas declaradas

Caminhos que a rede **nao** cobre. Uma lacuna declarada vale mais que um numero
maquiado — se a F0 mexer em algo desta lista, nao ha golden para pegar.

## Transversal aos 5 grupos

- **Exit code e prefixo `Error: ` nao estao travados.** O harness chama
  `rootCmd.Execute()` e devolve o erro; o wrapper `cmd.Execute()`
  (`cmd/root.go:30-35`, `"Error: %s"` + `os.Exit(1)`) nunca roda, porque `os.Exit`
  mataria o binario de teste. Os 249 goldens travam a **string do erro retornado**,
  nao o codigo de saida do processo nem o prefixo. Cobrir isso exige um teste
  por-exec (`go build` + rodar o binario); vale como **um** smoke test futuro, nao
  como duplicacao em cada golden.
- **`os.Getwd()` falhando** (em `loadConfig`: `status.go:54`, `logs.go:31`,
  `list.go:36`, `reset.go:32`, `recipe.go:32`, `init.go:27`) exigiria remover o cwd
  por baixo do processo em execucao. Sem ponto de injecao, e a corrida deixaria o
  teste flaky. Nao coberto de proposito.
- **`corvex completion`** segue fora de alcance.

## run / review

- **Caminho da TUI** (`cmd/run.go:163-164` → `runWithTUI`, `tea.WithAltScreen`) e
  inalcancavel in-process: `isInteractive()` olha `os.Stdout.Stat()` e no harness
  stdout e pipe. Zero golden. O seed do DAG/metricas na TUI (`run.go:219-268`) fica
  sem rede.
- **Ramo interativo do `confirmRun`** (`cmd/run_gate.go:64-76`): o prompt
  `"Proceed? [y/N] "`, a leitura do stdin e o `"aborted by user"` sao inalcancaveis
  pela mesma razao. So o ramo de auto-aprovacao esta coberto.
- **Execucao real de `--ab`** (`internal/orchestrator/runAB`): sobe DOIS git
  worktrees em goroutines paralelas, faz merge do vencedor e escreve
  `.corvex/ab-stats.json`. Saida e estado nao-deterministicos. So a **validacao das
  flags** (`--ab` sem escopo, lista malformada) esta travada.
- **Cost ceiling** (`max_cost_usd` / `max_cost_per_task_usd` estourando) e
  **escalacao/upgrade de modelo do reviewer** nao caracterizados: exigiriam provider
  fake com custo alto e varias iteracoes com a mesma categoria de falha. O provider
  fake de `run_task_passes` (linha assistant + linha result no protocolo
  stream-json) e a base pronta para quem for fazer isso.

## validate

- **Timeout de 60s do `startDBContainer`** (`cmd/validate.go:411-429`, cauda
  `stopFn(); return nil, "database did not become ready within 60s"`): o loop dorme
  500ms por um minuto inteiro. Um teste de 60s foi julgado inaceitavel na suite.
  Unico ramo descoberto da funcao (87.0%).
- **Default implicito de 30s do `waitForHealth`** (`cmd/validate.go:496-499`,
  `ReadyTimeout == 0`): mesma razao. Todo golden seta `ready_timeout` explicito. A
  consequencia esta na anomalia do `ready_timeout` acima.
- **Timeout de CDP do `startChrome`** (`cmd/validate.go:545-555`: Chrome sobe mas
  nunca responde na 9222 → poll de 10s, `Kill`, erro): custo fixo de 10s. O caminho
  de sucesso e o de binario ausente estao cobertos.
- **Porta 9222 hardcoded:** `TestCharacterizeStartChromeReady` e
  `TestCharacterizeStackUISuccess` fazem `t.Skip` quando a 9222 ja esta ocupada.
  Numa maquina com sessao real de debug do Chrome esses dois goldens nao rodam.
- **`validate.stack.port` com tipo errado no YAML** (erro de unmarshal do
  `config.Load`) nao caracterizado — e territorio de `config`, nao de
  `cmd/validate.go`.
- O `validate` alcancado por `corvex run --validate` compartilha `validateProject`,
  mas so foi caracterizado pelo comando `validate`.

## plan / start

- **`readBrainstormAnswer`, ramo de falha do `/ask`** (`cmd/start.go:281-283`,
  `"(ask failed: %v)"`): o stub e estatico; um stub que falha derrubaria o
  `Interview` antes de chegar ao `/ask`. Precisaria de stub com estado.
- **`promptBaseBranch`** (`cmd/start.go:344-345`): ramo de EOF que devolve `"main"`
  nao coberto.
- **Ramos de erro de I/O:** `writeMinimalSpec` (MkdirAll/WriteFile falhando),
  `setupWorktree` (`os.Symlink` falhando), `anchor.Save`/`SpecHash` falhando dentro
  de `runPlan` e `reanchorSpec`. Exigem filesystem read-only ou injecao de falha.
  (`runPlan` 92.5%, `reanchorSpec` 90.0%, `writeMinimalSpec` 66.7%.)
- **`linkWorktreePaths` com `worktree.link` populado** nao e exercitado a partir do
  `start`: o fixture nao seta a chave, entao a funcao roda com lista vazia. Ha teste
  unitario (`start_test.go:TestLinkWorktreePaths`), mas nenhum golden mostra as
  linhas `INFO linked into worktree` no fluxo real.
- **`plan.context_command`** (`Planner.runContextCommand`) nao e exercitado por
  nenhum golden de `plan`: e config-driven e faz `sh -c`; o comando externo e
  dominio do planner (`internal/`), nao de `cmd/plan.go`.
- **`planstart_start_new_worktree_plan.txt` embute a saida do `git worktree add`**
  (o que vai para stdout vs stderr), que **depende da versao do git instalada**.
  Numa maquina com outro git esse golden precisa ser regravado. Os outros casos de
  `start` usam worktree pre-criado justamente para nao depender disso.

## doctor / inspect

- **`printJSON` falhando** (`doctor.go:146`, `inspect.go:88`/`94`): inalcancavel na
  pratica — `json.MarshalIndent` de structs so com string/int/float nunca erra.
  (`runDoctor` 96.3%, `runInspect` 96.2%.)
- **`checkSandbox`, ramo do `filepath.Abs` com erro** (`doctor.go:234-238`):
  `filepath.Abs` so erra quando `os.Getwd` falha. Inalcancavel do harness.
- **`checkModels` em estado fail via CLI e IMPOSSIVEL:**
  `config.applyDefaults` (`internal/config/config.go:316-324`) preenche
  planner/worker/reviewer sempre que vem vazio, entao **nenhum** `config.yaml`
  consegue produzir `✗ models: missing: ...` passando pelo `corvex doctor`. O
  caminho so existe para chamadas diretas de `checkModels`, cobertas por
  `doctor_test.go:171`.
- **`sandbox.mount` absoluto e inexistente:** o check nao faz `Stat` no host path,
  so `filepath.Abs`, entao nao existe diferenca observavel entre mount valido e
  mount apontando pro vazio. Nao ha o que travar.
- **Ordem de iteracao de map em `checkEscalation`/`checkSkills` com MAIS DE UMA
  entrada invalida:** deliberadamente NAO caracterizada — a saida atual e
  nao-deterministica e o golden seria flaky. Cada golden desses usa exatamente 1
  entrada. *(Esta e, ela propria, uma anomalia: a saida do doctor depende da ordem
  de map quando ha varias entradas invalidas.)*

## status / logs / list / recipe / reset / init

- **Guardas `t == nil`** (`cmd/status.go:109-111` e `:148-150`) nao cobertas porque
  sao inalcancaveis: `order` vem sempre dos proprios tasks. Nao foi inventado fake
  para atingir o bloco — seria cobertura maquiada.
- **`cmd/logs.go:65-67`**, ramo de erro dentro do laco de todas as tasks:
  igualmente inalcancavel (`showTaskLog` so erra com ID inexistente e o laco itera
  sobre os proprios tasks).
- **`cmd/init.go:60-66` e `:73-75`:** falhas de `fs.ReadFile(templates.FS, ...)` (FS
  embedado, nunca falha) e de `os.WriteFile` dos 3 templates + do
  `.corvex/.gitignore` (exigiria chmod no meio da execucao do comando).
- **`cmd/recipe.go:62-64`**, erro do `WriteTasksFile` depois de compile bem-sucedido:
  e alcancavel (mkdir de um diretorio em `.corvex/tasks/<n>/tasks.md` derruba o
  rename atomico), mas a mensagem e o texto cru do syscall e difere entre kernels
  (`file exists` no darwin, `directory not empty`/`is a directory` no linux). O caso
  foi escrito, o golden foi visto, e o caso foi **REMOVIDO**: um golden que muda de
  SO e pior que uma lacuna declarada. A explicacao ficou em
  `cmd/char_statuslogs_errors_test.go`.
- **Ordem de projetos no `list`:** `projectNames()` depende de `os.ReadDir`, que
  ordena por nome, e os goldens assumem isso. Nao ha teste que trave a ordenacao em
  si (seria caracterizar a stdlib), mas se alguem trocar `ReadDir` por algo com
  ordem de map, `statuslogs_list_human`/`_json` ficam vermelhos.
