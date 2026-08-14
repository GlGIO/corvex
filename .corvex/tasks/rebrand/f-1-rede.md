# F-1 — A rede de caracterizacao de `cmd/` (leia antes de comecar a F0)

Voce vai refatorar `cmd/`. Esta rede e a unica coisa que separa "refactor
mecanico" de "regressao silenciosa em producao". Este documento diz o que ela
cobre, como rodar sem se enganar, e onde ela **nao** cobre.

Documento irmao: [`f-1-anomalias.md`](f-1-anomalias.md) — as anomalias que os
goldens congelam **de proposito**. Leia antes de "corrigir" qualquer saida
estranha que um golden mostre.

---

## 1. Regra de execucao (a parte que mais importa)

```
go test ./cmd/              # o unico verde que vale como evidencia
go test ./cmd/ -count=2     # obrigatorio antes de commitar fase
```

1. **Sempre o pacote inteiro.** `go test ./cmd/`. Nada menos que isso conta como
   prova de nao-regressao.
2. **Nunca julgue regressao por subconjunto.** `-run` serve para duas coisas:
   iterar rapido enquanto voce mexe num comando, e regravar golden. Nao serve
   para decidir se a fase pode commitar. Mesmo `-run TestCharacterize`, que hoje
   cobre 229/229 testes de caracterizacao, deixa de fora os testes unitarios
   antigos de `cmd/` (`doctor_test.go`, `status_test.go`, `list_test.go`, ...),
   que tambem travam saida.
3. **Nunca `GOMAXPROCS=1`.** Ver secao 4 — sob um unico P, praticamente todos os
   15 pontos que checam o stdout do renderer do `run` passam **sem verificar
   nada** (medido: 2 de 200 invocacoes produzem saida). A unica excecao e
   `TestCharacterizeRunRendererNotSilenced`, que foi construido para valer em
   qualquer escalonamento.
4. **`go test ./cmd/ -count=2` antes de commitar fase.** Dois passes na mesma
   invocacao pegam ordem-dependencia e estado global vazado entre testes (cwd,
   env, flags de cobra). Uma rede que so passa no primeiro pass nao e rede.

A mesma regra esta no header de `cmd/characterize_test.go`, secao
"COMO RODAR ESTA REDE (F0)", para quem chega pelo codigo em vez do markdown.

### Historico: o falso verde que essa regra existe para impedir

Na entrega original da F-1, 54 testes usavam o prefixo `TestCharStatuslogs`,
que `-run TestCharacterize` **nao pegava**. Mutacoes em `cmd/status.go:130` e
`cmd/logs.go:87` passavam VERDES. Hoje todos os 229 usam o prefixo
`TestCharacterize` e as mesmas mutacoes ficam VERMELHAS (14 testes acusam).
A licao nao e "o prefixo estava errado" — e que **qualquer** regex de `-run`
e uma oportunidade de falso verde. Por isso a regra 1.

---

## 2. Como regravar golden

```
go test ./cmd/ -run TestCharacterize -update-golden          # tudo
go test ./cmd/ -run TestCharacterizeStatuslogs -update-golden # um grupo
```

- `-update-golden` (nunca `-update`) reescreve `cmd/testdata/golden/*.txt` a
  partir da saida observada, em vez de comparar.
- **Regravar e um ato deliberado.** Golden vermelho depois de um refactor que
  devia ser mecanico e a rede funcionando: leia o `git diff` do golden antes de
  regravar. Se o diff mostra uma mudanca que voce nao pretendia fazer, o bug
  esta no seu refactor, nao no golden.
- Regrave no mesmo commit da mudanca de comportamento que justifica a
  regravacao. Nunca antes, nunca "para limpar".
- Os nomes dos goldens sao **strings literais** nos `goldenAssert(t, "...")`,
  independentes do nome da funcao de teste. Renomear teste nao renomeia golden.

---

## 3. O que a rede cobre, por comando

249 goldens, 229 funcoes `TestCharacterize*`, `cmd/` a 88.3% de cobertura.
Todos os testes rodam **in-process** (`rootCmd.Execute()` de verdade, num temp
dir), nunca chamam rede, IA ou docker.

| Grupo (arquivo) | Comandos | Testes | Goldens |
|---|---|---|---|
| `char_statuslogs_test.go` + `_logs` + `_write` + `_errors` | `status`, `logs`, `list`, `recipe`, `reset`, `init` | 54 | 64 |
| `char_validate_test.go` + `_cmd` + `_stack` + `_wizard` | `validate` (+ wizard, stack, chrome/CDP) | 54 | 54 |
| `char_doctorinspect_test.go` | `doctor`, `inspect` | 45 | 45 |
| `char_run_test.go` | `run` (dry-run, replan, gate, `--ab` flags) | 42 | 42 |
| `char_planstart_test.go` | `plan`, `start` | 30 | 41 |
| `char_run_drain_test.go` | o guarda do drain: falha se o renderer do `run` for SILENCIADO | 1 | 0 |
| `characterize_test.go` | o harness em si (3 auto-testes) | 3 | 3 |

O guarda do drain e o unico teste da rede **sem golden**: ele compara o stdout do
renderer linha a linha em memoria (a saida do renderer nunca entra em golden —
ver `runRendererPlaceholder`) e repete a invocacao N vezes, porque o que ele
mede — "chegou alguma linha?" — depende do escalonador, nao do texto. Os outros
grupos travam texto e ordem; este travam a existencia da saida. Ler o cabecalho
do arquivo antes de mexer nele.

Cada golden e um **transcript**: os args, o stdout, o stderr e o erro
retornado, na mesma ordem, passados por `scrub()`. O `scrub()` remove **apenas**
nao-determinismo genuino (relogios, temp paths, hashes) — nunca normaliza saida
que a CLI de fato imprime, inclusive espaco em branco a direita e desalinhamento.

Alem do transcript, alguns grupos travam efeito colateral em disco
(`runListProjectDir` renderiza o conteudo de `.corvex/tasks/<projeto>/` para
dentro do golden), entao um refactor que pare de escrever um arquivo tambem fica
vermelho.

### Nao renomeie estes dois

- `TestValidateHelperAppServer` (`char_validate_test.go:315`) **nao e teste** —
  e o processo servidor HTTP que `setupValidationStack` sobe, re-executando o
  proprio binario de teste com `-test.run=^TestValidateHelperAppServer$`.
  Ele fica de proposito **fora** do prefixo `TestCharacterize`. Renomear para o
  prefixo faria `-run TestCharacterize` tentar executa-lo no processo pai.
- `-update-golden`, e nao `-update`, para nunca colidir com a flag de outro
  pacote.

---

## 4. Anomalia que afeta como voce le o resultado

**O stdout do renderer do `run` nao esta em golden nenhum.**
`cmd/run.go:171` faz `go renderer.Drain(events)` e nunca espera a goroutine, e
`runRun` retorna assim que `orc.Run` termina. As linhas do `PlainRenderer`
chegam ao stdout conforme o escalonador do Go. Consequencia:

- essas linhas sao checadas por `runAssertRendererLines`, que exige apenas que
  o que chegou seja **prefixo exato** do esperado;
- mudanca de texto ou de ordem das linhas e pega;
- e sob pouco paralelismo o check vira vacuo. Medido nesta maquina (10 CPUs),
  200 amostras por configuracao:

  | escalonamento | amostras com alguma linha no stdout |
  |---|---|
  | `GOMAXPROCS=1`, ociosa | 2/200 |
  | `GOMAXPROCS=1`, 8 CPU hogs | 0/200 |
  | `GOMAXPROCS=2`, ociosa | 199/200 |
  | `GOMAXPROCS=2`, 8 CPU hogs | 2/200 |
  | `GOMAXPROCS=10`, ociosa | 199-200/200 |

  A taxa e funcao da **saturacao de CPU**, nao so de `GOMAXPROCS`, e chega a zero.
  Se `go test ./cmd/ -v` imprimir `no PlainRenderer lines reached stdout`, aquele
  teste **nao verificou** o stdout do renderer naquela execucao. Trate como
  nao-executado, nao como verde.

**O buraco de "silenciou tudo" esta fechado** — por um teste, nao pelo conserto do
bug. `TestCharacterizeRunRendererNotSilenced` (`cmd/char_run_drain_test.go`) usa
`runCLIAwait`, que espera as linhas antes de o harness fechar o pipe de captura, e
por isso vale em qualquer escalonamento (medido 800/800, incluindo as duas linhas
de p=0 acima). Ele cobre duas familias de evento de proposito: os de ciclo de vida
(`plan ready`, `done`, 64 iteracoes, ~1.2s) e os **por task** (`task start`,
`task complete`, 6 iteracoes, ~2.3s) — porque uma refatoracao de `executeTask` que
silencie so o progresso por task deixaria a primeira familia verde. Detalhe da
calibragem, da conta do N e das duas mutacoes que provaram o vermelho: header do
proprio arquivo e a entrada "Renderer nunca e drenado ate o fim" em
`f-1-anomalias.md` (marcada **BACKLOG POS-F0** com a razao de nao dar para
consertar dentro da F0).

Nao transforme o `t.Logf` de `runAssertRendererLines` em `t.Errorf`. Com as taxas
da tabela acima, isso faz ~15 pontos ficarem vermelhos em proporcao a carga do CI,
e suite que falha sozinha ensina o refatorador a ignorar vermelho — pior que o
buraco que fecharia.

---

## 5. Lacunas declaradas

Onde a rede nao pega. Se a F0 mexer em algo desta lista, **nao ha golden para
avisar** — revise a mao. Lista completa e comentada em `f-1-anomalias.md`,
secao "Lacunas declaradas"; resumo operacional:

**Transversal**
- **Exit code e prefixo `Error: ` nao estao travados.** O harness chama
  `rootCmd.Execute()`; o wrapper `cmd.Execute()` (`cmd/root.go:30-35`,
  `"Error: %s"` + `os.Exit(1)`) nunca roda. Os goldens travam a string do erro
  retornado, nao o codigo de saida do processo.
- `os.Getwd()` falhando (em `loadConfig`) — sem ponto de injecao.
- `corvex completion` — fora de alcance.

**run / review**
- **Caminho da TUI** (`run.go:163-164` → `runWithTUI`) inalcancavel in-process
  (`isInteractive()` ve pipe). Zero golden, inclusive para o seed de
  DAG/metricas (`run.go:219-268`).
- **Ramo interativo do `confirmRun`** (`run_gate.go:64-76`): prompt, leitura de
  stdin e `"aborted by user"`. So a auto-aprovacao esta coberta.
- **Execucao real de `--ab`** (dois worktrees, merge, `ab-stats.json`). So a
  validacao das flags esta travada.
- **Cost ceiling** e **escalacao/upgrade do reviewer** nao caracterizados.

**validate**
- Timeout de 60s do `startDBContainer`, default implicito de 30s do
  `waitForHealth`, timeout de CDP de 10s do `startChrome`: todos custam tempo
  real de suite, todos descobertos de proposito.
- **Porta 9222 hardcoded:** dois testes fazem `t.Skip` se ela estiver ocupada.
  Numa maquina com Chrome em modo debug esses goldens nao rodam.
- `validate` alcancado via `corvex run --validate` compartilha
  `validateProject`, mas so foi caracterizado pelo comando `validate`.

**plan / start**
- `readBrainstormAnswer` (ramo de falha do `/ask`), `promptBaseBranch` (EOF →
  `"main"`), e os ramos de erro de I/O de `writeMinimalSpec` / `setupWorktree` /
  `anchor.Save`.
- `linkWorktreePaths` com `worktree.link` populado nao e exercitado pelo
  `start`.
- `plan.context_command` nao e exercitado por nenhum golden.
- **`planstart_start_new_worktree_plan.txt` embute a saida do `git worktree
  add`** e depende da **versao do git instalada**. Noutra maquina, regrave.

**doctor / inspect**
- `printJSON` falhando, e o ramo de erro de `filepath.Abs` em `checkSandbox`:
  inalcancaveis.
- **`checkModels` em estado fail via CLI e impossivel** —
  `config.applyDefaults` sempre preenche planner/worker/reviewer.
- **Ordem de iteracao de map em `checkEscalation`/`checkSkills` com mais de uma
  entrada invalida** deliberadamente nao caracterizada: a saida atual e
  nao-deterministica. Cada golden usa exatamente 1 entrada. *(Isso e, em si, uma
  anomalia do doctor.)*

**status / logs / list / recipe / reset / init**
- Guardas `t == nil` (`status.go:109-111`, `:148-150`) e o ramo de erro de
  `logs.go:65-67`: inalcancaveis sem fake maquiado.
- Falhas de `os.WriteFile` em `init.go` e do `fs.ReadFile` do FS embedado.
- `recipe.go:62-64` (erro de `WriteTasksFile` pos-compile): alcancavel, mas a
  mensagem e texto cru de syscall e **difere entre kernels**. O caso foi escrito,
  visto, e **removido** — golden que muda de SO e pior que lacuna declarada.
- **Ordem de projetos no `list`** vem de `os.ReadDir`; nao ha teste travando a
  ordenacao em si, mas trocar por algo com ordem de map deixa
  `statuslogs_list_human`/`_json` vermelhos.

---

## 6. Checklist da F0

- [ ] `go test ./cmd/` verde antes de comecar (baseline).
- [ ] Refactor. Zero regravacao de golden "de passagem".
- [ ] `go test ./...` verde.
- [ ] `go test ./cmd/ -count=2` verde.
- [ ] Se algum golden mudou: `git diff` do golden lido linha por linha, e a
      mudanca de comportamento justificada por escrito no commit.
- [ ] Nada da secao 5 (lacunas) foi tocado sem revisao manual.
- [ ] `TestCharacterizeRunRendererNotSilenced` verde. Se ele ficar vermelho, o
      refactor emudeceu o renderer (total ou por task) — a mensagem de falha diz
      qual dos dois. Nao "conserte" aumentando o timeout nem baixando o N.
