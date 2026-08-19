# F4 — Implementar a superfície + inteligência (registro de fecho)

> Escrito depois da implementação, sobre a F3 (`6d411e3`) e a F2 (`a89ab89`).
> As decisões são as 13 da `f3-cli.md`; aqui está o que existe em disco, o que
> divergiu do desenho, e como cada parte foi provada.

## O que foi construído, em 4 ondas

| Onda | Commit | Entrega |
|---|---|---|
| 1 | `79d00a3` | substantivo `run` (`start\|list\|show\|watch\|retry\|kill`), tela única, legado escondido |
| 2 | `8025cb8` | substantivo `recipe` (`list\|show\|validate\|compile`), resolução recipe×projeto |
| 3 | `f876906` | `gate list` absorve escalation; aprovar/rejeitar uma vira comando |
| 4 | `7a96e1e` | `corvex` sem argumento = estado; completion só do que espera decisão |

## O que divergiu do desenho da F3

Três pontos, todos no sentido de mexer menos ou de fechar um buraco que o desenho
não tinha visto:

1. **`run kill` ficou mais conservador do que a D13 pedia.** O desenho dizia
   "SIGTERM ao pid do record, exigindo `machine` igual". Implementado assim, ele
   seria o primeiro consumidor destrutivo da leitura de liveness — e a F1 tinha
   aceitado a janela de reuso de pid com uma justificativa **literal**: *"nada
   destrutivo pende desse bit (nunca matamos nem reescrevemos com base nele)"*.
   Então ele não herda essa aceitação: antes de sinalizar, espera o **heartbeat
   do próprio run avançar**. Ninguém além do run escreve aquele arquivo, então um
   pid herdado por outro processo não consegue produzir a prova. Custo: ~13s numa
   ação que se digita raramente; `--now` pula. É a fronteira que a F1 mandou
   revisitar, revisitada no momento em que ela passou a importar.

2. **`--no-recompile` não estava no desenho.** A D4 previa parar e pedir
   `--recompile`. Faltava a outra metade: quem quer rodar o DAG compilado como
   está, sabendo que a recipe mudou. Sem ela, o único caminho para frente seria
   destrutivo — e uma parede com uma porta só, que dá no precipício, não é uma
   parede.

3. **A sugestão de comando desconhecido passou a enxergar comando escondido.**
   Efeito colateral que o desenho não previu: `SuggestionsFor` do cobra pula
   comandos `Hidden`, então no dia em que `status` virou legado `corvex stauts`
   deixaria de sugerir qualquer coisa. A sugestão foi reimplementada sobre
   `ops.SuggestFrom` (a mesma regra que já sugeria nome de projeto) e anota o
   substituto quando o alvo é um comando depreciado.

## A decisão mais load-bearing, agora medida

A D7 (aviso de depreciação **só em TTY**) foi justificada no papel com "senão
reescreveria dezenas de goldens". Isso deixou de ser retórica: tornando o aviso
incondicional, **69 testes de `cmd/` ficam vermelhos**. É a diferença entre ter a
rede da F-1 como oráculo durante a fase que renomeia tudo e não ter.

O outro lado está testado também: um teste em `cmd/` e um em `e2e/` (binário
real) falham se um aviso vazar para pipe.

## Goldens: 4 regravados, 32 novos

Regravados, e cada um por mudança **declarada** de superfície, não de
comportamento:

| Golden | Por quê |
|---|---|
| `harness_root_help` | seis comandos saíram da listagem (legado escondido) e o root ganhou `corvex [flags]` ao virar executável |
| `run_help` | `run` ganhou verbos e duas flags (`--recompile`, `--no-recompile`) |
| `gate_help` | os verbos passaram a cobrir escalation |
| `gate_list_empty` | "No gates are waiting." → "Nothing is waiting on you.", que cobre as duas coisas que a caixa agora tem |

**Nenhum golden de comportamento mudou.** Os comandos legados imprimem os mesmos
bytes sob pipe — é a regra que a §6 da F3 fixou, e a fronteira entre entrega e
vazamento: golden novo para comando novo é entrega; golden legado que mude é
vazamento e manda parar.

## Aceite — provado com binário real e processos separados

`e2e/run_surface_test.go`, três testes:

- **Invariante 3 + aceite da F4 numa tacada:** a invocação **legada**
  (`corvex run demo`, caminho `spec.md`, sem recipe nenhuma) roda ponta a ponta;
  depois `run list` acha o id, `run show <id>` mostra o step e o custo,
  `--step S01` mostra o fluxo de eventos, e **de um diretório que não é o
  repositório** o mesmo `run show <id>` continua respondendo — que é a
  propriedade pela qual o índice global existe.
- **Legado calado sob pipe:** `status`, `list` e `inspect` no binário real, zero
  ocorrência de "deprecated".
- **`corvex` sem argumento responde com estado**, incluindo a linha de próximo
  comando.

## Controle positivo — cada verde sabe ficar vermelho

| Mutação | Efeito |
|---|---|
| aviso de depreciação incondicional | **69** testes de `cmd/` vermelhos |
| `waitForBeat` vira no-op (prova do kill) | 2 vermelhos em `internal/ops` |
| drift de recipe deixa de parar o run | 1 vermelho (e o teste checa que o `PASSED` sobreviveu) |

## Invariantes no fecho

| # | Comando | Estado |
|---|---|---|
| 1 | `go test ./... -count=1`, `-race`, `./cmd/ -shuffle=on`, `./e2e/` | **verdes** |
| 2 | coverpkg do roadmap | **83,2%** (piso 60; F2 fechou 85,1). A queda é superfície nova de leitura — `watch`, confirmação do `kill`, fallbacks da tela de estado |
| 3 | binário real, fixture `spec.md` legado | `corvex run demo` exit 0 e endereçável por `run show` de outro diretório (e2e) |
| 4a / 4b | grep em fonte / `strings` no binário | **vazios** |
| 6 | cobra em `internal/` | **vazio** |
| 7 | `fmt.Print`/`os.Exit` em `internal/ops` | **vazio** |
| 8 | maior fonte de produção em `cmd/` | **150** (`plain_renderer.go`, pré-existente) |
| 9 | maior fonte de produção em `internal/` | **370** |
| 10 | `internal/tui` em `internal/ops` | **vazio** |

Dívida de gofmt: nenhuma nova (os 5 arquivos pré-existentes seguem os mesmos).
Os 14 bugs congelados da `f-1-anomalias.md` não foram tocados.

## Dívidas que esta fase deixa

- **Drift de recipe é por mtime.** Um `git checkout` pode deixar a YAML mais nova
  sem mudança de conteúdo, e o run para pedindo `--recompile`/`--no-recompile`. A
  assimetria é deliberada (custa uma interrupção, nunca um reset silencioso), mas
  o conserto certo é hash da recipe na frontmatter do `tasks.md` — que muda bytes
  de arquivo compilado e por isso não entrou aqui.
- ~~**`run pause` continua fora**~~ (D13) — **pago no fechamento** (`5d03e80`, ver
  `fechamento.md` e a dívida reescrita em `f7-registro.md`). O que estava escrito aqui, e que
  continua sendo a razão de ter ficado para depois: falta arquivo de controle e ponto de
  leitura na barreira pós-`wg.Wait()`. F7.
- **`run watch` é poll de 1s sobre disco**, sem inotify e sem stream. Suficiente
  para terminal, provavelmente insuficiente para a UI da F7, que vai querer SSE.
- **`gate answer` segue só um nome.** O eixo "agente pergunta" não tem evento em
  disco até a F5.
- **Escalation não tem `run_id`.** Ela é endereçada por projeto+step porque é
  isso que o nome do arquivo codifica; ligar escalation ao run que a gerou é
  trabalho da F5, quando a telemetria por run existir.
- **`corvex` sem argumento faz três leituras a cada invocação** (índice, records,
  ledger por projeto). Barato hoje com dezenas de runs; com milhares o índice
  precisa de um sumário.
- **A tela nova da D3 não tem oráculo prévio** (risco declarado na F3, §9). Os
  três comandos legados continuam vivos e testados, então a comparação é
  possível — foi feita à mão nesta fase e não está automatizada.
