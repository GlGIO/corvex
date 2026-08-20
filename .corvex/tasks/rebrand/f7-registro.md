# F7 — Servidor + UI (registro de fecho)

> 🗃️ **REGISTRO HISTÓRICO.** Os números e status deste arquivo valiam quando ele foi
> escrito e **não são estado corrente** — não decida por eles. O estado atual, com a
> instrução de como remedir cada número, está em `fechamento.md`.

> Duas levas: o contrato (`9861029`) e as telas (`7cf19eb`). A ordem é a que o roadmap exige —
> *"fixe API + design system num único passo, **depois** paralelize as telas. Oito telas em
> paralelo sem contrato = oito estilos."*

## A regra que determina o formato de todo handler

Paridade (2h): todo botão tem equivalente em CLI e nenhum caminho existe só na UI. Isso não é
slogan — é a razão física de a F0 ter arrancado a regra de dentro do `cmd/`. Por isso **todo
handler tem três linhas**: decodifica, chama `internal/ops`, codifica. Handler com regra de
negócio aqui é defeito: significa que CLI e UI podem divergir, e o primeiro sintoma de
divergência é um gate aprovado por um e não pelo outro.

As formas na rede são os tipos de `ops` **verbatim** (`RunRow`, `RunReport`, `Inbox`,
`GateView`), os mesmos que o `--json` do CLI imprime. Não há DTO: um DTO seria o lugar onde as
duas superfícies começariam a discordar sobre o que é um run.

## Auth: três checagens, e o que cada uma para

| Checagem | O que ela para |
|---|---|
| token (URL → cookie HttpOnly) | qualquer origem que nunca viu a URL que o corvex imprimiu |
| `Host` de loopback | **DNS rebinding** — nome hostil que resolve para 127.0.0.1; sobrevive ao token se o atacante também o roubou |
| `SameSite=Strict` | o navegador nem anexa o cookie num POST cross-site |

O token sai da barra de endereço na primeira carga, trocado por cookie: token em URL acaba em
screenshot, histórico de shell e link colado. O roadmap fixou *"auth desde a v1"* porque
retrofitar significa enviar uma janela em que uma página web aprova migration de produção.

## Run disparado é desacoplado, não filho

`Setsid` + `Release`, nunca `Wait`. A armadilha está escrita na F1: filho que ninguém reapa
vira zumbi, zumbi responde a sinal 0, e a liveness passaria a reportar run morto como vivo.
É também o que faz "fechar a UI e o run continua" ser verdade — o servidor **supervisiona**
runs, nunca é dono deles, que é o "sem daemon" do roadmap.

## O defeito que o próprio teste achou

A primeira versão gravava no log de ação `corvex gate **approved** run_8f21` — o veredito em
disco no lugar do verbo do CLI. A linha *parecia* paridade e não era executável. O conserto
veio com guarda própria: todo comando gravado tem de começar por um verbo que existe. É o
tipo de defeito que só aparece quando o teste afirma o valor exato em vez de "não vazio".

## O que a UI mostra, e por que assim

- **2a** caixa única (gates humanos + escalations), como a F4 já unificou no CLI.
- **2b** gate com a **trava de leitura**: Aprovar só habilita depois de marcar cada evidência
  `required_reading` — a mesma trava que o `--ack` aplica no terminal, e desde a F5 ela aceita
  ter sido lida ontem (`corvex gate ack`).
- **2c** disparo, **2d/2f** run com steps/custo/retries e a barra por natureza (F5),
  **2e** histórico, **2h** ⌘K mostrando **comandos**, não "ações".
- Step inferencial é roxo, determinístico é verde. Não é enfeite: é a tese do produto, e é a
  única coisa que a tela precisa deixar óbvia à primeira vista.

Vanilla, sem build step, embutida via `embed.FS`: é uma interface que um usuário roda com as
próprias credenciais e que aprova migration em produção. Superfície que ninguém consegue
auditar no momento em que precisa confiar nela não entra. Os assets ficam **atrás da mesma
auth** da API — página que carrega sem token e depois falha em toda chamada é pior que página
que não carrega, porque a falha parece a ferramenta quebrada.

## Aceite

`e2e/ui_test.go`, binário real: sobe, imprime uma URL com a porta que o SO escolheu, a página
carrega e devolve cookie, a API responde, e requisição sem token (401) ou com `Host`
estrangeiro (403) é recusada. O invariante 4 vale para a SPA (ela entra no binário): teste
varrendo o que é servido, e `strings` do binário em 0.

## Dívidas

- ~~**Poll de 5s, não stream.**~~ **Pago em parte, e o resto está escrito.** `GET /api/events`
  é SSE atrás da mesma auth (`internal/server/stream.go`). Mas **o poll não sumiu: trocou de
  lado.** O argumento acima continua verdadeiro — o servidor supervisiona runs e nunca é dono
  deles (`Setsid` + `Release`, nunca `Wait`), então não existe evento em memória para
  transmitir. Quem observa o disco agora é o servidor, a cada 1s, e ele só escreve para o
  navegador quando a impressão digital do que leu **muda**. O que se ganhou é latência de tela
  (de até 5s para até 1s) e requisição (de 12 por minuto para uma conexão aberta **enquanto o
  stream vive**); o que se pagou é 5x mais leitura de disco por página aberta — **e mais
  requisição no modo degradado, que esta conta não tinha:** com o stream caído a página faz
  ~42/min (24 tentativas de reconexão a 2s + 10 leituras de estado, medido em 50s com o socket
  do `/api/events` sendo derrubado), contra as 12/min do poll que ela substituiu. A conta
  original só descrevia o caso feliz, o que é a metade que faz a mudança parecer só ganho. Não é arquitetura nova, e não é push:
  vigiar o filesystem (fsnotify) nem resolveria, porque a mudança que mais importa — um run que
  morreu — não escreve nada em disco, é o `kill -0` que descobre. A UI degrada: o poll de 5s
  continua no `app.js` e volta a trabalhar assim que o stream para de provar que está vivo.
- ~~**`gate answer` (2g) continua só um nome.**~~ **Pago em parte, e o resto está escrito.**
  O eixo agora tem substrato: uma quinta natureza de gate (`question`, `internal/types/step.go`)
  escrita no **mesmo arquivo, mesmo diretório e mesma escrita atômica** dos gates humanos, com a
  resposta num campo novo e opcional — `gate.Decision.Answer`. A forma foi escolhida pelo critério
  "o `gate audit` e a caixa de entrada continuam corretos **sem caso especial**": como campo, o
  audit vê a linha pela natureza que já viajava com ela e a caixa lista a pergunta sem saber que
  ela existe; como tipo irmão, `gate.List` precisaria de uma segunda varredura do mesmo diretório e
  `ops.Inbox` de uma terceira fatia. E o veredito **não** ganhou um quarto valor: responder é
  `approved`, recusar é `reject`, não responder é `expired` — o invariante em que o audit se apoia
  (`Decided == Approved+Rejected+Expired`) fica intacto.
  Superfícies: `corvex gate answer <id> --step S --text "…"`, `POST /api/gates/{id}/answer`
  (handler de três linhas, comando gravado no log de ação começando por um **verbo que existe**),
  e a tela 2g na UI — a pergunta ganha caixa de texto e perde o botão Aprovar, porque o servidor
  recusa aprovar uma pergunta e botão sempre recusado é pior que botão nenhum.
  **O que falta, exatamente.** Quem produz a pergunta hoje é a *receita* (um gate `nature:
  question` num step), não o agente. Para o **worker** perguntar no meio da execução falta mudar o
  protocolo do provider, que é um request/response de um tiro só: `Provider.Execute`
  (`internal/provider/provider.go:10`) devolve `types.ExecuteResult`
  (`internal/types/types.go:189`), que não tem como dizer "parei, preciso saber X" — e
  `Worker.Execute` (`internal/step/worker.go:126`) não tem ponto de retomada depois de uma
  resposta. Ou isso, ou uma ferramenta que o agente chame. Não mexi: é mudança de protocolo.
  Menor, junto: o renderer imprime `human-gate` também para uma pergunta
  (`cmd/plain_renderer.go:108`), porque o tipo de evento `HumanGate` significa "parado esperando
  uma pessoa" e renomeá-lo mexeria no vocabulário do ledger, que é commitado.
  **Decisões que continuam do dono** (roadmap, "Decisões em aberto — NÃO invente"): (i) `gate
  answer` × `answer` no topo — implementei sob o nome que já estava fixado, sem alias; (ii) o
  roadmap esboça `--choice C` e eu entreguei `--text`, porque uma escolha pressupõe um conjunto de
  opções declarado no gate, campo que não existe e que é decisão de schema; `--choice` continua
  expressível em cima disto depois, sem migração.
- ~~**`run pause` continua fora**~~ **Pago** (`5d03e80`). O desenho já estava escrito na D13
  (`f3-cli.md`) e foi implementado como estava: **arquivo de controle** (um `.pause` por run em
  `RecordsDir`, escrita atômica tmp+rename) e **leitura na barreira de onda** — no topo de
  `walkDAG`, antes de `expandFanouts`, que é a única janela sem worker vivo. Pausar dentro do step
  mataria chamada de provider já paga. Sem sinal, pelo motivo que a F1 já mediu: zumbi responde a
  sinal 0. A extensão é `.pause` e não `.json` porque `ReadRecords` lê todo `*.json` da pasta, e um
  segundo JSON com o mesmo `run_id` viraria linha duplicada em toda listagem da máquina.
  **Visibilidade:** `StatusPaused` entra no eixo de **status**, não no de liveness — o processo
  *está* de pé e batendo; o que mudou é o que o run reporta de si. `run list` mostra `paused alive`,
  e `--status paused` filtra. Não virou `parked`: parked é "um humano precisa decidir", paused é
  "poderia seguir e mandaram parar", e fundir os dois quebraria a caixa de entrada.
  **Fica de fora:** o canal de pausa do TUI continua desacoplado do arquivo em disco. Unificar
  exige o TUI escrever o arquivo — mudança de comportamento de caminho existente.
- **Um repositório por servidor.** Listagens são cross-repo (índice global), mas dispatch e
  escalation são do repositório em que o `corvex ui` subiu. **Continua aberta** — é a única das
  cinco dívidas da F7 que não foi atacada no fechamento, e não por acaso: as outras quatro são
  mecanismo dentro de um processo, esta é escopo de produto.
- ~~**Sem CSP.**~~ **Pago** (`3ea1ff7`), e sem nenhum `'unsafe-inline'`:
  `default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none';
  form-action 'none'; frame-ancestors 'none'`. O header entra **por fora** do `Auth.Guard`, então
  401 e 403 também o carregam — que são justamente as respostas que um ataque produz.
  O trabalho de verdade não foi a política, foi a **página caber nela**: o `index.html` já estava
  limpo, mas o `app.js` escrevia **atributo** `style` em três pontos (via o helper `el()`), e
  atributo `style` é policiado por `style-src` — sob `'self'` a barra de custo por natureza (2f)
  apagaria **em silêncio**. Consertamos a página, não a política: o helper passou a aplicar
  `style` pelo CSSOM (`Object.assign(node.style, v)`), que o CSP não policia, e quem escrever
  `style:` daqui pra frente nasce compatível.
  Achado de tabela: o ponto de composição do handler estava **duplicado** (`New` e `Handler()`
  montavam a cadeia cada um por si) — era o defeito latente clássico, o próximo handler nasceria
  sem o header. Agora `Handler()` é o único lugar, o que também faz o teste via `httptest`
  exercitar exatamente o que o navegador recebe.
  **Ressalva registrada:** `frame-ancestors 'none'` proíbe qualquer enquadramento, inclusive
  webview de IDE. Hoje ninguém enquadra, e o botão Aprovar vale a proteção — mas é a diretiva que
  quebra primeiro se um dia o `corvex ui` for embutido num editor.
