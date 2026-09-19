# Harness — o corvex substituindo /pilot-feature e /pilot-incident

> **Esta é a FONTE DE ESTADO desta obra.** Números aqui valem com o comando que os
> mede ao lado. Prosa sem comando apodrece — a lição está em
> `.corvex/tasks/rebrand/fechamento.md` e não vai ser reaprendida aqui.

## O alvo, em uma frase

O corvex lê o board, monta o DAG, executa em ondas com isolamento por item, e o
humano opera tudo pela UI — sem skill, sem sessão de chat no meio. Hoje, no
SmartCare, isso é a `/pilot-feature` (128 linhas de prosa + 764 de workflow.js).

## Medido em 2026-09-18

| coisa | comando | estado |
|---|---|---|
| suíte do corvex | `go test ./...` | verde |
| navegador de verdade | `CORVEX_REQUIRE_BROWSER=1 go test ./e2e/` | verde (Chrome presente) |
| tools do smartcare | `cd ~/projects/yandeh/smartcare && ./scripts/flow/test.sh` | 472/472 |
| ciclo dispatch→gate→approve→done pela UI | curl na API, recipe de shell puro | funciona, $0.00 |

## Rodada 1 — FECHADA (commit 31ec91a, branch `harness/ui-dispatch`)

Três defeitos, cada um com controle positivo medido antes da correção:

1. **dispatch no diretório errado** — a UI rodava no próprio WorkDir; projeto com
   worktree morria no guard e a tela dizia `ok`. Agora despacha para dentro do
   worktree, com `here` como escotilha explícita.
2. **o dispatch mentia** — run que morre antes de se registrar não deixava rastro
   algum. Agora o `cmd.Wait` grava exit status + a linha `Error:`, existe
   `GET /api/runs/logs/{name}`, a caixa mostra o dispatch morto, e o fingerprint
   do stream o inclui (senão a tela só desenharia quando outra coisa mudasse).
3. **dois workers na mesma árvore** — medido: 3 steps `code` editando o checkout
   ao mesmo tempo numa onda paralela. Agora serializam num lock; tool/test/repro
   continuam sobrepondo.

## Rodada 2 — FECHADA

1. **O run registrado que falha agora diz onde e por quê.** A tela do run nomeia
   o step que a derrubou (`failed at S02`) e TODO step abre: descrição, critérios,
   a linha do tempo do ledger e o tail do que o comando imprimiu (que já existia
   em `?step=` e no CLI, e a tela nunca pedia). Controle positivo: contra a página
   anterior o teste de navegador expira — a tela mostrava só as pílulas PASSED /
   FAILED, sem nada clicável.
2. **Multi-repo no dispatch.** `GET /api/repos` devolve a MESMA lista que a caixa
   de gates já agrega (o repo aberto + os que o índice global viu), o formulário
   escolhe o repositório, as recipes são lidas do repositório escolhido, e o card
   do run carrega o nome do repo. Um repositório que a máquina nunca rodou é
   RECUSADO com a instrução de como entrar na lista — esta superfície existe para
   spawnar um binário, e aceitar caminho livre a transformaria em spawnar em
   qualquer lugar que o corpo do POST nomear.

## Rodada 3 — FECHADA

**Board → DAG existe.** `scripts/flow/az-feature-dag.sh` no smartcare (branch
`flow/feature-dag`, commit `66739581`): lê a Feature, pega as filhas, filtra as
terminais, lê os links de precedência e emite o array JSON com `wave` por story —
o contrato exato do fan-out. 14 casos offline; a suíte deles foi de 472 para 486.

A suíte do smartcare cobrou três integrações no instante em que o arquivo
apareceu no disco (eixo do `pr-review.sh`, inventário de custódia do
`config.yaml`, pin do `--help`). Todas feitas. É a melhor propaganda que aquele
harness tem.

**E o produtor achou um defeito no consumidor**: `wave_by` ordenava as chaves com
`sort.Strings`, então a onda 10 rodava antes da onda 2 — dependência invertida em
silêncio, só em grafos grandes demais para alguém conferir na mão. Corrigido no
corvex (`9d027ab`) com controle positivo.

**NÃO MEDIDO, e é o próximo risco a fechar**: `Dependency-Reverse` = Predecessor
vem da documentação do Azure, não de uma resposta do board do SmartCare. Se o
board não modela precedência, tudo cai na onda 0 (comportamento correto na
ausência de links, e é o que a skill manda perguntar ao humano). **Um `--dry-run`
seguido de um GET de leitura numa Feature real fecha isso em um minuto — precisa
do dono, porque é a identidade dele no Azure.**

## Rodada 21 — FECHADA (o passo que a UI não sabia dar)

O dono apontou a falha de desenho: a tela dispara runs, mas o **worktree** em
que a run acontece tinha de ser criado à mão, num terminal, com
`git worktree add -b hotfix/73960 ../smartcare-73960 main`. O primeiro passo do
modelo de operação — um worktree por trabalho, todos coordenados de uma página —
acontecia fora do produto. E é o passo em que se escolhe o nome da branch, que é
o que roteia o PR depois.

Agora a tela faz os dois lados:

- **`POST /api/worktrees`** corta o checkout com a branch que o chamador nomeia
  (`ops.SetupWorktreeOn`), recusa nome que seja caminho, devolve a frase do
  próprio git quando ele recusa, e avisa quando a base local está atrás do
  upstream — o `git pull` que a versão manual desse fluxo sempre inclui e esta
  não pode fazer sozinha.
- **Os campos que a receita declarou** (`requires: - env:`) aparecem no
  formulário, com o motivo que o autor escreveu. Viajam no **ambiente** do
  processo, nunca no argv (`ps -Ao args` é legível por qualquer processo da
  máquina), e um nome listado em `security.runner_only_env` é recusado ali.

Quatro defeitos medidos no caminho, cada um com controle positivo:

1. **A regra de `runner_only_env` era lida do diretório do PROCESSO**, não do
   repositório em que a run vai acontecer. Uma UI fala por vários checkouts; um
   dispatch para outro era julgado pela lista errada — e para uma lista de nomes
   que nunca devem passar, ser julgado pela lista errada é passar. Medido: a URL
   de produção aceita como campo de formulário.
2. **A linha gravada punha a atribuição antes do `cd`**:
   `INCIDENT_ID=73960 cd /path && corvex run start incident` define a variável
   para o `cd` e entrega nada ao corvex. Colada, morre no preflight que o campo
   existe para satisfazer — e só quando há `cd`, que é todo dispatch para
   worktree.
3. **Um worktree recém-criado não era um repositório válido para dispatch.** O
   índice só conhece um checkout depois que algo rodou nele, então a tela recusava
   o checkout que ela mesma acabara de criar. Agora um worktree de um repositório
   conhecido é conhecido — perguntando ao git, não casando o padrão do caminho.
4. **Medido no navegador:** o formulário criava o worktree, a branch aparecia na
   lista, e ele não conseguia selecioná-la — o create respondia `/var/…` e o
   `git worktree list` dizia `/private/var/…`. Atribuir um valor que nenhuma
   opção carrega não seleciona nada, em silêncio, e o clique seguinte despacharia
   para o que estivesse selecionado antes. Duas grafias de um diretório bastam
   para mandar uma run para o checkout errado.

O exercício de navegador (`e2e/ui_worktree_test.go`) anda o caminho inteiro:
abrir o formulário, cortar `hotfix/73960-carrinho` a partir de `main`, escolher a
receita, digitar o `INCIDENT_ID`, rodar — e conferir que o step escreveu **no
worktree**, na branch certa, com o valor digitado, e **nada** no checkout
principal.

**Achado que não consertei:** `go test ./e2e/ -count=2` já falhava antes desta
rodada (`TestRunSurface_BareCorvexAnswersWithState`,
`TestUI_PauseAndResumeFromTheScreen`). Os testes compartilham o índice global de
runs, então a segunda execução vê as linhas da primeira. É defeito de isolamento
do próprio suite, não do produto — mas é a regra desta obra rodar `-count=2`, e
hoje ela não é verdade aqui.

## Rodada 22 — FECHADA (a tela estava feia, e não era gosto)

O dono mandou a captura: "a UI ta toda cagada, nao ta seguindo os padroes do
nosso mockup". Fui ler o mockup (artifact "Rebrand do corvex") e ele diz, no
próprio CSS, que **a UI da F7 usa exatamente aqueles tokens**. Não usava mais.

Montei um fotógrafo descartável — um teste que sobe a UI com conteúdo real,
dirige o Chrome por CDP e grava PNG de cada tela — e olhei. O que eu vi:

- **Tudo em monospace**, inclusive parágrafos. Uma frase em mono lê como despejo
  de log, não como o produto falando. Mono é para texto de máquina: id de run,
  caminho, status, dinheiro, id de step — o que alguém compara caractere a
  caractere ou cola em outro lugar.
- **Tema claro não existia** (`color-scheme: dark`, sem tokens claros). O mockup
  é claro primeiro, escuro pelo `prefers-color-scheme`. Agora os dois existem, e
  o escuro é praticamente o que já era — a diferença visível é tipografia,
  espaço e hierarquia.
- **Nomes de token divergentes**: `--bg` contra `--ground`, e nada de `--sunk`,
  `--faint`, `--shadow`, `--ai`. Produto cujos textos e cujas telas usam cinzas
  diferentes lê como dois produtos.
- **Pills fora do padrão**: cada variante com a própria cor de borda, que podia
  discordar do texto. Agora é uma forma só, `border:1px solid currentColor`.
- **O formulário de dispatch sem hierarquia**: seis caixas sem rótulo, e a que
  nomeia a branch — a que roteia o PR — indistinguível das outras. Agora cada
  campo tem rótulo, e o criador de worktree é visivelmente um painel dentro do
  painel: cortar branch é barato e reversível, disparar run não é.

Três defeitos de verdade achados pela foto, não pela leitura:

1. **O motivo que o autor da receita escreve saía em CAIXA ALTA monoespaçada** —
   `.field > span` pegava todo span do campo, inclusive a frase. `:first-child`.
2. **`direction:rtl` reordenava o caminho.** Serve para pôr a reticência na
   frente, mas embaralha a corrida de barras em volta: `/private/var/…/repo`
   renderizava `private/var/…/repo/` — um caminho que não é o caminho, no único
   lugar onde a pessoa está comparando dois checkouts. A cabeça é cortada em JS
   agora.
3. **`done` era pill neutra**, do mesmo cinza de `finished`. Um fim bom e um
   fato sem cor liam igual.

Quatro testes e2e quebraram e **os quatro estavam certos em quebrar**:
`innerText` respeita `text-transform`, então `includes('failed')` deixou de
casar quando a pill virou `FAILED`; e o `h2` da história passou a carregar o
próprio botão, então `textContent === 'history'` deixou de ser igualdade. As
asserções viraram insensíveis a caixa e a `startsWith` — elas afirmam qual tela
está no ar e que ela reporta a falha, não a caixa das letras.

## Rodada 23 — FECHADA (o ⌘K que só olhava)

O dono abriu o ⌘K esperando digitar um comando. Não dava: o painel nasceu como
**log de paridade** — o que esta UI fez, escrito como as linhas de CLI que fazem
o mesmo — e nunca teve campo. Conferi o histórico: um único commit tocou nele
desde que nasceu (`7cf19eb`), e ali já era o que era hoje. A memória dele era da
frase do documento do rebrand ("o ⌘K mostra comandos"), que descreve o log.

Agora digita. As duas metades são a mesma lista de propósito: toda entrada é um
comando que o servidor já sabe gravar, então rodar pelo teclado escreve a mesma
linha de auditoria que o clique escreve — o lançador não pode virar um canal
privado para dentro do runner.

**O que NÃO entrou, e é a decisão que importa:** aprovar ou rejeitar gate. A
tela de gate tem a trava de leitura — evidência marcada `required_reading`
precisa ser aberta antes de o Approve destravar, e o reconhecimento é gravado
com carimbo de hora. Uma entrada `gate approve run_0575` seria uma tecla
atravessando isso pelo meio: sem evidência, e o ledger registrando uma leitura
que não houve. O painel leva ATÉ o gate e para ali. Tem teste que segura isso
para quando alguém achar que aprovar pelo teclado seria conveniente — controle
positivo medido: acrescentar a linha deixa o teste vermelho.

Duas outras regras que valem registro:

- **Receita que declara `env:` não dispara pelo painel.** Os valores são o ponto
  (`INCIDENT_ID`), e um run sem eles morre no próprio preflight. Então ela abre
  o formulário com a receita já escolhida — o painel entrega em vez de fingir.
- **Casamento por substring, não fuzzy.** Numa lista cujo Enter para um job em
  execução, recall comprado com fuzzy se paga casando coisa que ninguém quis.
  `run kill` também é o único item com pill vermelha, e continua passando pelo
  `confirm` que o botão Stop já usava.

## Rodada 24 — FECHADA (uma linha colada, três campos)

"Tá meio chato preencher tudo." A linha que a pessoa tem na mão quando um
incidente começa é a do board — `Incident 73607: Pedido duplicado na aba
Financeiro (Visão 360°)` — e o formulário pedia que ela redigitasse três pedaços
dela: o id duas vezes (nome do diretório e o `INCIDENT_ID` da receita) e um slug
do título, à mão, dentro do campo que decide para onde o PR vai.

Esse último é o motivo disto existir em vez de ser conveniência. Digitar nome de
branch de memória no começo de um incidente é de onde vem `hotfix/Incident-73607`
— git válido, e fora da convenção que o `ship-target.sh` lê para rotear. Medido
na tela do dono, antes de eu escrever uma linha.

Agora o campo aceita a linha inteira e tira os pedaços. Nada é buscado: o texto
já está na área de transferência, e uma leitura do board daqui seria chamada de
rede, credencial e discussão de custódia para algo que a pessoa já tem.

O prefixo sai da convenção **deste** repositório, conferida no origin
(`hotfix/61761-…`, `feature/59101-…`): Incident e Bug → `hotfix`, Feature →
`feature`, e um tipo que o mapa não conhece **não ganha branch nenhuma** em vez
de ganhar um palpite. Os campos são só PREENCHIDOS — nunca limpos, nunca
sobrescritos depois que alguém digitou: um parse que apaga a branch que a pessoa
corrigiu é pior que nenhum parse, porque a correção é a única parte disso que a
máquina não sabe conferir.

**Rodada 24b — o colar que não colava.** O dono voltou dizendo "eu deveria poder
colar isso e ele autopreencher". Duas causas, e a segunda é falha minha de teste:

1. A UI dele tinha subido às 19:59 e o binário era de 20:18 — rodava o de antes.
2. **Meu teste provava o parser e nada do caminho da pessoa.** Ele setava o
   `value` do campo e disparava `change`; isso não é um paste. O handler lia o
   campo DEPOIS, esperando que o navegador já tivesse inserido o texto — e teria
   continuado passando mesmo se ele parasse de olhar a área de transferência.

Agora o handler lê `e.clipboardData` direto, e quem escuta é o **card inteiro**:
⌘V em qualquer lugar do formulário preenche, e o painel do worktree se abre
sozinho se estava fechado. Um handler preso a um campo dentro de um painel
fechado pede que a pessoa ache o campo primeiro — que é a maior parte do
trabalho que ele deveria tirar. O teste dispara um `ClipboardEvent` de verdade
com `DataTransfer` e afirma pelo `defaultPrevented`: linha do board é
reivindicada, id sozinho não é (e o navegador insere normalmente). Controle
positivo: tirar o listener do card deixa os dois testes vermelhos.

Mais dois consertos pequenos:

- **`stack (database)` era uma promessa que a opção não cumpre.** O que o
  `stack` sobe é o que o bloco `validate:` do repositório declara, e o smartcare
  tem `database: {type: none}` — sobe servidor de app e nenhum banco. O rótulo
  passou a nomear o bloco em vez de adivinhar o conteúdo.
- **`waitFor` do `dispatch_test.go` tinha prazo de 5s** e ficava vermelho só
  quando o resto da suíte rodava junto. O prazo não é o que o teste afirma — a
  alegação é que o filho reporta o diretório, não que reporta em cinco segundos.
  Teste que falha só sob carga ensina a re-rodar em vez de ler.

## O que falta, na ordem em que eu pretendo atacar
1. **A recipe `pilot.yaml`** — juntar o que já existe: stage que chama a tool,
   fan-out sobre os itens com `wave_by`, template por story, e o `ship` no fim.
   Sem isso as peças existem e ninguém as conecta.
1. **Isolamento por item do fan-out** — um worktree por story. Sem isso, story em
   paralelo é serial (rodada 1) ou perigosa (antes dela).
1. **O que a UI ainda não responde**: quanto um run está custando ENQUANTO roda
   (o card só mostra status/idade), e nada avisa fora da aba — um gate que abre
   com o navegador em outra janela espera o olho humano voltar.

1. **O primeiro run de VERDADE** — com um board real e tokens. Precisa do dono:
   é dinheiro, é a identidade dele no Azure, e é a medição do
   `Dependency-Reverse` que segue não medida.

## Rodada 7 — FECHADA

**A `/pilot-incident` virou recipe** (`incident.yaml`, smartcare `7c97d326`):
contexto+preflight de acesso a PRD → diagnóstico → 🛑 FREIO A na CAUSA →
teste que reproduz → `kind: repro` → conserto → suíte → entrega. Rodei os nove
steps dublados: o freio parou, aprovei pelo CLI com ack, o `before` provou o
defeito, o conserto rodou, o `after` provou que parou.

**E o `repro` tinha um buraco que anulava o propósito dele** (`5fbab26`): com um
defeito que NÃO reproduz, o `before` falhava dizendo "nothing to fix" — e o run
executava o conserto assim mesmo, porque nada no grafo ligava o fixer à pergunta.
Faltava uma aresta. Ninguém tinha rodado o step type desde que ele foi escrito.

Mais o README, que não documentava `wave_by`, `isolate`, `max_parallel`,
`on_item_failure` nem `{{ item.field }}` — tudo entregue nas rodadas anteriores.

## Rodada 8 — FECHADA

**A sonda pós-deploy existe** (`sonda.yaml`, smartcare `0a1dd3b9`): reconsulta o
dado que provou a causa e compara com um número. Três desfechos medidos — ainda
falha (run FALHA com a contagem), parou (gate humano com a reconsulta como
leitura obrigatória), query sem número (exit 2).

**E ela achou um defeito de produto** (`cc3216b`): na SEGUNDA execução, o corvex
pulou o step que já havia passado e foi direto ao gate anunciando sucesso — **com
a query nunca executada**. Resumabilidade é certa para construir e errada para
medir. Agora existe `always: true`: o step e tudo que depende dele voltam a
PENDING em todo run.

**Retry pela UI** na mesma leva: re-executar UM step do card do run ou do detalhe
dele, com recusa 409 se o run está vivo.

## Rodada 9 — FECHADA

**As recipes foram preflightadas contra o repositório REAL do SmartCare**, o que
nunca tinha sido feito (elas só haviam passado por `recipe validate`, que não
olha a máquina). A `pilot` compila e resolve todas as tools aqui; a `incident`
também, com o repro expandido na ordem certa.

**E isso achou o meu próprio erro**: o guard de acesso a PRD que eu escrevera na
`incident.yaml` fazia `grep '^mcp:'` — e a chave do corvex é `mcp_servers:`,
dentro de `sandbox:`. Ele teria RECUSADO um repo configurado certo e PASSADO num
com a chave errada. Virou `requires: - mcp: prd` (`9bfca1e`), conferido contra a
config resolvida — a porta da regra em vez de uma segunda grafia dela.

**Runbook** em `smartcare/.corvex/recipes/README.md`: o que digitar, o que cada
parada significa, o que fazer depois — para as cinco recipes.

## Rodada 10 — FECHADA

**Exercitei o eixo dinheiro num fan-out** — seis stories caras contra um teto de
$25 — que é o cenário do primeiro run de verdade e nunca tinha sido rodado. O
teto funciona: abortou na sexta story, cinco mergeadas, o worktree da sexta em
disco com o trabalho dela. A retomada é barata (o DAG guarda o que passou).

**Dois defeitos de dinheiro, ambos errando para BAIXO** (`60a14cd`):
1. o gasto que ESTOUROU o teto não ia para o ledger — a chamada aconteceu, foi
   paga, e a tela que responde "para onde foi o dinheiro" era a mais silenciosa
   no momento mais caro. O run abortava dizendo $27,00 e a tela dizia $22,50;
2. a tela do run ignorava `attempt_cost`: o mapa `extra` era declarado, lido no
   fim, e escrito por ninguém. O `inspect` contava; o run, não.

Direção importa: um número baixo demais é o que faz alguém subir um teto achando
que tem folga.

## Rodada 11 — FECHADA

**Exercitei o review reprovando até o teto** — o caminho do dia um. Três defeitos
silenciosos (`158814e`):
1. a ÚLTIMA tentativa reprovada não ia para o ledger (a condição era
   `attempt < maxRetries`): dois workers pagos, um registrado;
2. a tela do run descartava a review de quem nunca completou — e as barras por
   fase, NA MESMA TELA, mostravam o que o cabeçalho não mostrava;
3. a categoria do review sumia por formatação (só era procurada ANTES do
   veredito, e `**CATEGORY:** x` devolvia `** x`) — e categoria vazia significa
   política de escalação nenhuma, em silêncio.

**E a minha prosa estava mentindo**: a `pilot.yaml` prometia "bounce humano" que
dependia de uma seção de config que o smartcare não tinha. Ligada
(`63313378`): `correctness after 2` e `wrong-approach after 1` → `human-prompt`,
medido chegando à caixa de entrada.

## Rodada 12 — FECHADA

**Rejeitar um gate**: o comportamento é certo (o passo irreversível NÃO roda, os
dependentes são pulados) — mas a RAZÃO não sobrevivia: o detalhe do step dizia
`refused by gate` e a frase digitada pela pessoa ficava no terminal que fecha.
Agora vai nas duas linhas do ledger (`fa1a311`).

**Matar um run no meio de um fan-out**: deixa as worktrees em disco, o que é
certo — e o `doctor` dizia `7 checks, 7 passed` com três delas lá. Agora avisa,
nomeia e diz como limpar.

O golden de caracterização pegou o check novo e foi regravado no mesmo commit,
com dois passes (`-count=2`), como a regra daquela rede manda.

## Rodada 13 — FECHADA

**Pausar no meio de um fan-out isolado: VERDE**, medido e congelado em teste. O
pause drena o que está em voo (contrato documentado), segura entre o trabalho do
item e o merge dele, e a retomada traz tudo para casa sem deixar worktree.

**E o clique duplo mata o run** (`308faee`): dois runs do MESMO projeto no mesmo
repo. O segundo lia o `RUNNING` do primeiro, achava que era sobra de run morto,
resetava para PENDING e reexecutava — dois agentes num checkout, estado final por
quem gravasse por último. A proteção que existia era acidental (o guard de árvore
suja, que só pega se o primeiro já sujou algo). Agora há porta, antes de o
segundo run existir, nomeando quem segura o projeto.

## Rodada 14 — FECHADA

**O botão de retry que eu construí na rodada 8 não funcionava para o caso que o
justificou** (`b7cc9c4`). Três camadas, uma atrás da outra:
1. o id era maiusculizado — `S02/000/trabalha` virava `TRABALHA`, inexistente;
2. o reset não arrastava os dependentes — o merge ficava PASSED com dependência
   pendente e o run seguinte abortava por integridade de DAG;
3. e o retry rodava só o step nomeado: o item refazia o trabalho, o run dizia
   `done`, e o merge não rodava. A story não chegava na branch, em silêncio.

Mais a recusa para o item JÁ MERGEADO (worktree removida): nomeia a situação e o
caminho (`--recompile`) em vez de falhar depois num diretório que não existe.

**A lição, de novo:** construí o botão na rodada 8 com a justificativa do
fan-out, escrevi teste para a RECUSA (run vivo) e nunca cliquei nele num item de
fan-out. Teste de recusa não substitui exercitar o caminho feliz.

## Rodada 15 — FECHADA

Método novo, saído da lição da 14: procurar guardas com teste de RECUSA e nenhum
exercício do caminho feliz. Duas encontradas (`2d61642`):

1. **Despachar para outro repositório** nunca tinha rodado. Medido: UI no repoA
   despacha para o repoB, o run executa lá, o repoA fica intocado, o histórico
   mostra. VERDE — congelado em teste.
2. **`nature: question`**: o run perguntava, a pessoa respondia, e o step rodava
   o comando que rodaria sem perguntar. A resposta era evidência que um leitor
   via e o trabalho não usava. Agora chega como `$CORVEX_GATE_ANSWER`.

O pin do ambiente dos steps reprovou a variável nova ("uma variável acrescentada
é uma promessa nova para toda recipe") e foi atualizado no mesmo commit, com o
porquê — não silenciado.

## Rodada 16 — FECHADA

**A resposta da pergunta chega ao agente** (`c62edf4`), fechando a dívida da 15.
O prompt do worker carrega a pergunta e a resposta, com a instrução de tratá-la
como decisão já tomada. Medido com o provider dublado gravando o prompt: o worker
vê a seção e o valor; no controle, zero ocorrências.

Com isso o `nature: question` fica completo nos dois tipos de step — shell por
`$CORVEX_GATE_ANSWER`, IA pelo prompt.

## Rodada 17 — FECHADA

**Os dois verbos de conversa, clicados num navegador** (`7b5d55d`): responder uma
pergunta e rejeitar com motivo. Os dois existiam na tela e nunca tinham sido
usados PELA tela — o caminho conversacional do produto só era alcançável pelo
terminal, que é o oposto do que a UI existe para fazer.

Melhora que saiu daí: declinar uma pergunta manda o texto da caixa como MOTIVO,
em vez de descartá-lo.



## Rodada 18 — FECHADA

**Pause, resume e stop clicados num navegador** (`12c5d6f`). Os testes pinam a
DIFERENÇA entre os verbos — pausar preserva o que está em voo, parar não — e que
a tela troca um botão pelo outro em vez de mostrar os dois.

Nenhum defeito de produto nesta rodada: os três caminhos estavam certos. Dois
enganos MEUS, do mesmo tipo (asserção no lugar errado): esperar a palavra no
texto da página em vez do pill do card — eu esperava o eco do meu próprio clique
—, e usar uma recipe de 3s no teste de Stop, que terminava enquanto o Chrome
subia.

Com isto, **todo verbo da UI tem exercício de navegador**: despachar, aprovar,
rejeitar com motivo, responder, declinar, abrir step, ver log, re-executar step,
pausar, retomar e parar.

## Rodada 19 — FECHADA (a mais importante para o primeiro run real)

Fui exercitar o caminho que o dono vai andar — declarar o MCP de PRD e rodar a
`incident.yaml` — e medi o argv passado ao agente (`7825080`):

1. **O worker local recebia ZERO servidores MCP.** `ExecuteWithProgress` (o
   caminho do sandbox LOCAL, que é o padrão e o que o SmartCare usa) montava os
   próprios argumentos e deixava o `--mcp-config` de fora; só o caminho
   containerizado o acrescentava. O preflight dizia "satisfeito" porque estava
   DECLARADO, e o diagnóstico rodaria cego — a falha de 85k tokens, reintroduzida
   pela distância entre declarar e entregar.
2. **E consertar isso deu produção ao REVIEWER** — medido: duas invocações com a
   flag, a segunda era o juiz. A regra "só o worker recebe MCP" existia na
   documentação e não tinha ponto de aplicação. Agora o pedido carrega
   `AllowMCP`, só o worker marca, e o provider decide.

Mais dois ajustes na `incident.yaml` do smartcare (`9fbf2048`): o `requires:`
pedia `mcp: prd` e o servidor existente chama-se `SmartCarePRD` (recusaria um
ambiente correto), e o repro virou `${REPRO_CMD:-npm test}` para não rodar a
suíte inteira duas vezes.

## Rodada 20 — FECHADA

Preparei o ambiente do dono para o primeiro run de incidente (binário
reinstalado, MCP declarado sem credencial no repo, `mcp.json` ignorado, `SELECT
1` no PRD respondendo) e rodei **o primeiro step real da recipe contra o board
real**, num worktree isolado, sem gastar token: o `az-show-workitem.sh` leu o
incidente 73960 de dentro do run.

E aí um defeito: `run start incident --task S01` rodou 1 step de 9, não falhou
nada, e gravou **`done`** — a palavra que alguém lendo o histórico entende como
"a recipe terminou". Agora existe `partial` (`fc17de0`), que já era a palavra do
hook `post-run` e que o registro nunca usara.

**Esperando o dono:** qual incidente (11 abertos, recomendei 73960 ou 73607) e a
autorização de gasto. A branch `corvex/prova-s01` foi removida com a worktree.

## O que falta

1. **O primeiro run de VERDADE** — com board real e tokens. É seu: dinheiro, sua
   identidade no Azure, e a medição do `Dependency-Reverse`.
2. **Declarar o MCP de PRD** no `smartcare/.corvex/config.yaml` (template
   comentado já está lá). Sem ele a `incident` recusa no preflight — de propósito.
3. **A sonda pós-deploy** da `/pilot-incident` (Fase 4) — não cabe dentro do run
   por construção: o deploy é humano. Candidata natural a `corvex` agendado.
4. **Ligar o `question` na `incident.yaml`** (quando a release ativa não é
   encontrada, perguntar o alvo em vez de parar) — é mudança de DESENHO do fluxo,
   e a decisão é do dono.
5. **Conflito entre stories** — hoje o merge falha e para. A skill delega a um
   agente ("Sincronizar"); isso é decisão de produto, não dívida.

## Regras desta obra

- Todo defeito afirmado tem **controle positivo**: o teste falha contra o código
  antigo, medido, antes de eu dizer que consertei.
- Nada de escrita no Azure, merge em linha protegida, migration, ou run de IA
  pago sem o dono mandar. Isso é decisão, não trabalho.
- Cada rodada termina em commit + este arquivo atualizado.
