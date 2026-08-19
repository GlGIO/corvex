# F7 — Servidor + UI (registro de fecho)

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
  (de até 5s para até 1s) e requisição (de 12 por minuto para uma conexão aberta); o que se
  pagou é 5x mais leitura de disco por página aberta. Não é arquitetura nova, e não é push:
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
- **`run pause` continua fora** (F3, D13): falta arquivo de controle e ponto de leitura na
  barreira de onda.
- **Um repositório por servidor.** Listagens são cross-repo (índice global), mas dispatch e
  escalation são do repositório em que o `corvex ui` subiu.
- **Sem CSP.** A página é embutida e não carrega nada externo, mas um header explícito é
  barato e não foi posto.
