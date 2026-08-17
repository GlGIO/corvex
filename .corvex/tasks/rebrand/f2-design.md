# F2 — Taxonomia, gates e evidência (desenho)

> Documento de gate, em duas camadas.
>
> As **seções 1–16** são o desenho, escrito antes de qualquer linha de Go e aprovado como
> está — cinco decisões mudam comportamento observável e uma invade a superfície que a F3
> desenha. Ficam aqui na forma em que foram aprovadas, sem edição retroativa.
>
> A **seção 17** é o registro de fecho: onde a implementação divergiu do desenho e por quê,
> como cada parte foi provada, e o que ficou de dívida. **Leia a §17 antes de agir sobre
> qualquer detalhe das §§1–16** — três decisões mudaram na direção de mexer menos.
>
> Base: `0a119ac` (F1 fechada + auditoria adversarial). Branch `rebrand`.

## O que esta fase decide

| # | Assunto | Origem |
|---|---|---|
| 1 | `kind: code\|tool\|test\|repro` na recipe | roadmap §Taxonomia |
| 2 | 4 naturezas de gate como cidadãos de primeira classe | `f0-fluxo.md` §Catálogo |
| 3 | Contrato de evidência com `required_reading` | roadmap §3 (Arquitetura alvo) |
| 4 | Fan-out dinâmico com ondas | roadmap §Taxonomia, "primitivo faltante" |
| 5 | `human-gate` que bloqueia de verdade e é liberado por outro processo | aceite da F2 |
| 6 | `StatusParked` ganha produtor e consumidor | dívida registrada da F1 |
| 7 | `Identity.Recipe` deixa de ser sempre vazio | dívida registrada da F1 |

**O que esta fase NÃO decide:** a superfície de comando (F3), telemetria de leitura de
evidência (F5), ambiente de execução (F6), a tela (F7), as tools tipadas (F8), custódia de
credencial (F9). Nenhuma das seis decisões em aberto do roadmap é invadida — ver §14.

---

## 1. Achados de campo que restringem o desenho

Cinco fatos do código de hoje. Cada um foi conferido, não lembrado, e cada um mata pelo
menos uma solução que pareceria óbvia no papel.

### 1a. O id de task é `S\d+` — e uma recipe que desobedeça é silenciosamente comida

`internal/task/parser.go:14`:

```go
headingRe = regexp.MustCompile(`^##\s+(S\d+)\s*[-—–]\s*(.+?)\s+(⬜|🔄|✅|❌|⏭️|⏭)\s*([A-Za-z-]+)\s*$`)
looseHeadingRe = regexp.MustCompile(`^##\s+S\d+\b`)
```

`recipe.Validate()` **não** impõe essa forma. Uma recipe com `id: migrate` compila sem erro,
`WriteTasksFile` escreve `## migrate — … ⬜ PENDING`, e na volta o parser não casa nem
`headingRe` nem `looseHeadingRe` — a task **desaparece sem erro**. Todo teste do pacote usa
`S01`/`S02`/`S03`, então a rede nunca tocou nisso.

Consequência para a F2: **não existe espaço de nome para id gerado**. `migrate/003/apply`,
`S07[3]`, qualquer coisa que não seja `S` + dígitos é indexável só em memória. Fan-out precisa
persistir nós novos; logo, ou o id gerado cabe em `S\d+`, ou o regex abre. Ver Decisão 6c.

### 1b. `activity.jsonl` entra na história do usuário

Estabelecido na correção pós-fecho da F1, com prova no histórico deste repo
(`101a40d corvex: checkpoint S01`). O `auto_commit` do próprio corvex commita o ledger.
`TestEntry_JSONKeysAreAnAllowlist` é a tripwire.

Consequência: **conteúdo de evidência não pode passar pelo ledger.** Um `test_output` carrega
path absoluto na primeira linha de stack trace; um `diff` carrega o que estiver no working
tree; um `sql` carrega nome de schema de produção. O ledger recebe só metadado de gate.

Vale registrar o precedente ruim ao lado: `.corvex/escalations/` (`internal/step/escalation.go:59`)
**não** é gitignored e grava o resumo do reviewer em arquivo. Não é dívida da F2 e não vou
consertá-la aqui, mas é exatamente o erro que a evidência não pode repetir.

### 1c. `SetStatus` appenda no índice global

`internal/run/registry.go:169-185` — toda mudança de status escreve o record **e** appenda um
snapshot em `$CORVEX_HOME/runs.jsonl`. Isso vale de graça: um run que entra em `parked`
aparece no índice global, que é o único arquivo que enxerga run de **outro repositório**.

Consequência: a caixa de gates cross-repo (tela 2a, a mais importante do produto) sai de
**zero estado global novo** — varre o índice por `status == parked`, e para cada um lê o
arquivo de gate em `record.Repo`. Nenhum registro de gate em `$CORVEX_HOME`.

### 1d. `Identity.Recipe` nunca é preenchido, mas a informação já está em disco

`ops.startIdentity` grava `Project`, com comentário explícito de que o caminho legado não tem
recipe. Só que `DAGSpec.GeneratedBy` já vale `"corvex-recipe:<nome>"` (`recipe.go:171`), e a
frontmatter do `tasks.md` a persiste (`task/writer.go:84-88`).

Consequência: fechar a dívida da F1 não precisa de campo novo em lugar nenhum. Ver Decisão 9.

### 1e. `human-gate` hoje mata o run

`internal/step/human_gate.go:22` devolve `Fatal(...)`. Não bloqueia: aborta, e a liberação é
*re-rodar o run inteiro* com `--approve-gates`. O texto está num golden
(`cmd/testdata/golden/run_help.txt:11`).

Consequência: o aceite da F2 ("bloqueia o processo e é liberado por `corvex gate approve`") é
**mudança de comportamento**, não implementação de reservado. Ver Decisão 5 e §13.

---

## 2. Decisão 1 — dois eixos, não um

O roadmap descreve steps de 4 tipos **e** gates de 4 naturezas. Hoje o campo `kind` mistura os
dois: `task` e `command` são mecanismos de execução, `human-gate` é uma decisão.

**Adotado: `kind` passa a ser a natureza do *trabalho*; a decisão vira um bloco `gates:` próprio.**

| Eixo | Pergunta que responde | Campo |
|---|---|---|
| **Step** | o que este nó *faz* | `kind: code \| tool \| test \| repro` |
| **Gate** | quem decide se o run *avança* | `gates: [{nature, when, …}]` |

Por que dois eixos e não um enum maior: o catálogo empírico da `f0-fluxo.md` tem doze gates e
**nenhum** é um nó sem trabalho. "Push de tag — humano confirma → não pusha" é um step `tool`
guardado por um gate humano; "materializar no board" é um step `tool` guardado por um gate
humano; "merge por nível" é um step `tool` guardado por um gate de política. Modelar gate como
tipo de step obriga a inventar um nó vazio ao lado de cada ação real, e aí a dependência entre
gate e ação vira convenção de nome em vez de estrutura.

**Consequência de desenho que cai daí: gate tem posição.**

| `when` | Semântica | Natureza típica |
|---|---|---|
| `before` | decide se o trabalho **acontece** | humano (consentimento), política (teto, nível de branch) |
| `after` | decide se o resultado **passa** | computacional (exit), inferencial (review), política (contador de tentativa) |

Sem `when`, "aprovar antes de mergear em main" e "aprovar o diff depois de escrito" viram a
mesma coisa — e a diferença entre as duas é se o efeito colateral já aconteceu quando você
reprovou. Default por natureza: `human` → `before`, resto → `after`. Declarável sempre.

---

## 3. Decisão 2 — taxonomia de step

```yaml
- id: S03
  kind: code | tool | test | repro     # default: code
```

| `kind` | Modo | Executor | Custo | Gate implícito |
|---|---|---|---|---|
| `code` | inferencial | worker de IA + `TASK-REPORT` | token | review inferencial (o que `Reviewer` já é) |
| `tool` | computacional | shell hoje; assinatura tipada na F8 | zero | exit code |
| `test` | computacional | shell | zero | exit code |
| `repro` | computacional **temporal** | shell, duas vezes | zero | falha antes **e** passa depois |

`tool` × `test` executam pelo mesmo caminho e diferem **só na semântica**. Isso é de propósito
e vale defender: a distinção é o que a UI usa para pintar "isto mudou o mundo" × "isto só
observou o mundo", é o que a contabilidade da F5 usa para separar custo de ação de custo de
verificação, e é o que a F9 usa para decidir onde credencial pode chegar. Um enum que colapsa
os dois hoje custa uma migração de recipe depois.

### Compatibilidade

`kind` de hoje continua aceito, mapeado no compilador, sem regravar recipe de ninguém:

| Escrito hoje | Compila para |
|---|---|
| ausente / `task` | `kind: code` |
| `command` | `kind: tool` |
| `human-gate` | `kind: tool` com `command: ":"` (no-op) + `gates: [{nature: human, when: before}]` |

O terceiro é açúcar preservado, não tipo vivo: quem escreveu `human-gate` queria um nó que só
espera gente, e continua tendo um. O compilador emite a forma nova; a recipe antiga não muda.

**Tripwire que falta e que a F2 escreve:** `recipe.Validate()` passa a rejeitar id que o parser
de `tasks.md` não consegue ler de volta (achado 1a). Hoje isso é perda silenciosa de stage.

---

## 4. Decisão 3 — as 4 naturezas de gate

```yaml
gates:
  - nature: computational
    when: after
    command: "psql -c '\\d+ users' | grep -q idx_users_email"
    label: "Schema pós-migrate"

  - nature: inferential
    when: after
    reviewer: dba              # opcional: skill/agent do repo do usuário
    model: opus                # opcional: default = provider.models.reviewer
    label: "Review de migration"

  - nature: human
    when: before
    prompt: "Aplicar esta migration em STG?"
    label: "Aprovação de STG"

  - nature: policy
    when: after
    max_attempts: 2            # loop de fix cap 2
    max_cost_usd: 3.0          # teto deste step
    branch_not: [main, develop] # merge por nível
```

| Natureza | Quem decide | Token? | Falha → |
|---|---|---|---|
| `computational` | exit code de um shell | não | `FAILED` |
| `inferential` | agente independente | sim | `FAILED` com categoria (alimenta escalation) |
| `human` | pessoa, por outro processo | não | `FAILED` (reject) ou run parado (timeout) |
| `policy` | regra do runner | não | `FAILED`, mensagem nomeando o knob |

### `inferential` — "nunca o autor" é estrutural, não uma checagem de modelo

`Reviewer.Review` (`internal/step/reviewer.go:52`) já faz uma chamada nova ao provider, com
prompt próprio e ferramentas só de leitura (`Read`, `Glob`, `Grep`, `Bash`). É isso que compra
independência: **contexto zerado**, não modelo diferente. Um gate inferencial no mesmo modelo,
com contexto novo e prompt adversarial, é independente; o mesmo modelo continuando a conversa
do worker não é, mesmo trocando o nome do modelo.

Então a F2 **não** valida "modelo do gate ≠ modelo do step". Ela garante estruturalmente que o
gate inferencial nunca recebe o histórico do worker, e escreve isso no doc do pacote. Validar
desigualdade de modelo daria uma sensação de segurança que a propriedade real não depende dela.

### `policy` — quase tudo já existe, disperso

| Regra | Onde vive hoje | Na F2 |
|---|---|---|
| teto de custo do run | `config.Execution.MaxCostUSD` ($25) | continua global; gate `policy` pode apertar por step |
| teto por task | `config.Execution.MaxCostPerTaskUSD` ($5) | vira default do gate `policy` de step |
| máximo de tentativas | `config.Execution.MaxRetries` + `ReviewConfig.Escalation` | `max_attempts` no gate expressa o cap 2 da autopilot **na recipe**, não no config global |
| merge por nível | prosa, na skill do usuário | `branch_not:` — função pura, é a regra de segurança mais importante do fluxo (`f0-fluxo.md` §Funções puras) |

O ponto de `policy` como natureza não é código novo: é **nomear** como gate o que hoje é
`if` espalhado, para que a contabilidade da F5 responda "qual gate mais reprova" incluindo os
determinísticos. Um teto de custo que mata o run é um gate que reprovou.

---

## 5. Decisão 4 — contrato de evidência

### Schema (o que o step devolve)

```yaml
evidence:
  - kind: test_output | verdict | diff | sql | link
    label: "Testes"
    status: pass | warn | fail
    required_reading: true
    content: "..."          # inline
    from: "git diff --stat" # ou: shell que produz o conteúdo
```

`content` e `from` são mutuamente exclusivos; `from` roda no mesmo workDir do step, com o mesmo
teto de tempo, e sua saída vira `content`. Isso é o que permite evidência de **domínio** sem
domínio no binário: quem sabe o que é "plano de query mudou de seq scan para index scan" é o
`from` que o usuário escreveu na recipe dele.

### Produtores

| Produtor | Emite automaticamente |
|---|---|
| gate `computational` | `kind: test_output`, `status` do exit, `content` = saída combinada |
| gate `inferential` | `kind: verdict`, `status` do veredito, `content` = summary + categoria |
| step `code` | `kind: diff`, `status: warn`, `content` = `git diff --stat` do checkpoint |
| step `repro` | dois `verdict` (antes / depois) |
| recipe | qualquer coisa declarada no bloco `evidence:` |

Automático porque evidência que depende de alguém lembrar de declarar é evidência que falta
justamente no gate que ninguém revisou.

### Onde mora — e por que não no ledger

```
<repo>/.corvex/runs/gates/<run_id>-<step_id>.json
```

`<repo>/.corvex/runs/` já nasce com `.gitignore` contendo `*` na primeira escrita (F1,
`ensureRecordsIgnored`), e `*` cobre subdiretório — **conferido em repo de scratch**, não
assumido: `git add -A` não estagia nada e
`git check-ignore -v .corvex/runs/gates/r1-s01.json` responde `.corvex/runs/.gitignore:1:*`.
A evidência herda de graça a única propriedade que ela precisa: **nunca entra na história do
usuário** (achado 1b).

O ledger recebe só metadado — e nenhum campo novo em `activity.Entry`, então
`TestEntry_JSONKeysAreAnAllowlist` continua verde por construção, não por sorte:

| `type` | `task_id` | `status` | `message` |
|---|---|---|---|
| `gate_pending` | step | — | `human: Aprovação de STG` |
| `gate_decided` | step | `approved` / `rejected` | `human: Aprovação de STG (12m34s)` |
| `gate_failed` | step | `failed` | `policy: max_attempts=2` |

`message` carrega natureza + label, que são **vocabulário do usuário na recipe dele** — a mesma
justificativa que deixou `recipe` entrar no ledger. Nunca carrega `content`.

### `required_reading` — a trava, e o que ela realmente prova

No CLI, aprovar exige nomear cada evidência marcada:

```
corvex gate show run_8f21 --step migrate-stg          # imprime as evidências
corvex gate approve run_8f21 --step migrate-stg \
  --ack "Testes" --ack "Plano de query"
```

`approve` sem os `--ack` completos é recusado, listando o que falta. Os labels só são
descobríveis por `gate show`, então aprovar às cegas exige ter aberto a tela.

**Isto é atrito, não prova** — o próprio roadmap diz isso no risco #1, e eu não vou fingir o
contrário. Digitar dois labels não é ler. A prova real é o sensor sobre o sensor (latência de
aprovação, taxa de reprovação por gate), que é F5. O que a trava compra hoje é que aprovar
deixa de ser tecla única, e que existe um lugar canônico onde o "falta ler o veredito" da 2b
já tem dado por trás quando a UI chegar.

Estado de *leitura* (persistente, "fechar a aba não zera") é F5, explicitamente. A F2 grava
**a decisão**: quem, quando, e quais labels foram reconhecidos.

---

## 6. Decisão 5 — human gate que bloqueia de verdade

### Protocolo entre processos

```
runner                                        segundo processo
──────                                        ────────────────
1. escreve <run_id>-<step>.json (pending)
   com prompt + evidências
2. SetStatus(parked)  ────────────────────▶   aparece no índice global (1c)
3. bloqueia; poll do arquivo a cada 2s
   heartbeat SEGUE batendo
                                              corvex gate list      (lê índice + arquivos)
                                              corvex gate show
                                              corvex gate approve   ─┐
4. lê a decisão (temp+rename)  ◀──────────────────────────────────── ┘
5. registra gate_decided no ledger
6. SetStatus(running); segue o DAG
```

Decisões de mecânica, e o porquê de cada uma:

| Decisão | Forma | Por quê |
|---|---|---|
| Escrita da decisão | temp + rename, como o record da F1 | um `approve` morto no meio deixa o arquivo pendente íntegro, nunca meio JSON |
| Leitura pelo runner | poll de 2s | não vale uma dependência de fsnotify para uma espera que dura minutos ou dias; 2s é imperceptível para um humano e barato para o disco |
| Heartbeat durante o park | **continua** | `parked` não é terminal, então `Resolver.Liveness` cai em pid + heartbeat e responde `alive` — que é a resposta certa para "o processo está de pé esperando gente" (a F1 já tinha escrito isso, agora tem produtor) |
| `--approve-gates` | preservado, auto-aprova | é o caminho de CI/`--yes`; sem ele um pipeline trava para sempre |
| Timeout | `expires_after:` **opcional**, sem default | gate que decide sozinho depois de N horas é gate que virou carimbo com atraso. Quem quiser, declara; o default é esperar |
| Rejeição | `gate reject` → step `FAILED` → cascata normal de `recordFailure` | reprovar não é caso especial: é falha de step, e a cascata de SKIPPED dos dependentes já existe e já é testada |
| Run morto enquanto parked | o arquivo de gate sobrevive; `gate approve` num run morto **recusa**, nomeando o run | aprovar um gate que ninguém vai ler é pior que erro: parece que avançou |

### Onde `parked` some e onde fica

`StatusParked` ganha produtor (passo 2) e consumidor (`gate list`). A `Record` **não** ganha
campo: qual gate está pendente é pergunta respondida pelo arquivo de gate, junção por `run_id`.
Manter a struct fechada é a propriedade que a F1 comprou e que não vale gastar aqui.

---

## 7. Decisão 6 — fan-out dinâmico com ondas

### Forma na recipe

```yaml
- id: S05
  kind: tool
  command: "ls -1 migrations/pending/*.sql"
  produces: items                 # uma linha = um item; ou JSON array

- id: S06
  fanout:
    over: S05
    wave_by: item.wave            # opcional; sem isso, tudo em uma onda
    max_items: 50                 # teto obrigatório (default 50)
    max_parallel: 4
    on_item_failure: abort        # abort | continue
    template:
      - id: apply
        kind: tool
        command: "npm run migrate -- {{ item }}"
      - id: verify
        kind: test
        command: "./scripts/schema-check.sh {{ item }}"
        depends_on: [apply]

- id: S07
  kind: code
  depends_on: [S06]
```

### 7a. A ideia central: fan-out **expande para o DAG estático**

Nada no escalonador precisa aprender um conceito novo. Quando `S05` passa, o runner:

1. lê os itens de `S05`,
2. instancia o `template` por item,
3. liga as ondas com `depends_on` comum entre nós expandidos,
4. **reescreve `tasks.md`** com os nós novos e a frontmatter do DAG,
5. reconstrói o `dag.DAG` e continua o laço.

Onda entre itens não é primitivo: é aresta. O item da onda 1 depende do último step do template
do item da onda 0. `walkDAG` já roda nível a nível, `runWaveParallel` já limita concorrência,
`recordFailure` já cascateia SKIPPED. A única máquina nova é **"o grafo cresce uma vez, no
momento da expansão"**.

O preço: `schedule.run.DAG` e `schedule.tasks` são construídos uma vez hoje
(`orchestrator/schedule.go:34`). Precisam de uma revisão in-place, feita entre ondas — **nunca**
com goroutine de step viva. `walkDAG` tem esse ponto de graça: a barreira depois do `wg.Wait()`
já é single-threaded (comentário em `schedule.go:190`).

### 7b. Por que persistir em `tasks.md` e não em memória

Porque resume é a razão de o corvex existir (dor #1 do roadmap). Um run que descobre 30 itens,
faz 12 e morre precisa reencontrar os mesmos 30 — não redescobrir, que daria um conjunto
diferente se alguém commitou uma migration nesse meio-tempo. `tasks.md` é o estado de registro
e continua sendo.

### 7c. O id — e o achado 1a

Fan-out precisa mintar id. O parser só lê `S\d+` (achado 1a). Três saídas:

| Opção | Custo |
|---|---|
| (a) abrir `headingRe` para um token de id geral | mudança de comportamento no parser; mexe na detecção de heading malformada, que tem teste |
| (b) mintar dentro de `S\d+` (`S06` → `S06001`, `S06002`) | colide com `S06001` escrito à mão; ilegível; e não sobrevive a template com dois steps |
| (c) estado de fan-out fora do `tasks.md` | dois estados de registro; contradiz 7b |

**Adotado: (a)**, com a forma `S06/003/apply` — `<fanout>/<índice 3 dígitos>/<step do template>`.
Índice, não slug do item: slug de um path é ilegível numa coluna de tabela e muda se o item
mudar de nome. O item vira `title`, que é onde humano lê.

Isso **também** conserta a perda silenciosa do achado 1a, e é por isso que a opção (a) é a
honesta em vez da conveniente: o regex fechado não estava protegendo nada, estava escondendo
que uma recipe com id fora do padrão perde stages sem dizer. Ver §13 — é mudança de
comportamento e tem golden envolvido.

### 7d. Tetos, e a lição da F1

`max_items` é **obrigatório com default 50**, e estourar é falha dura nomeando o knob.

Esta é a mesma classe de erro que a auditoria da F1 encontrou no espaço de ids: a fase tratou
o espaço como infinito. Fan-out sem teto é pior, porque cada item pode ser um step `code` — um
`ls` que devolve 4.000 arquivos vira 4.000 chamadas de LLM. O teto de custo global ($25) é a
rede embaixo, mas rede que pega você depois de gastar $25 não é o mesmo que porta que não abre.

| Caso de borda | Comportamento |
|---|---|
| zero itens | fanout `PASSED`, evidência `warn` dizendo "0 itens", dependentes rodam |
| `> max_items` | `FAILED`, mensagem nomeando `max_items` e quantos vieram |
| item falha, `on_item_failure: abort` | cascata normal: dependentes do item e o fanout falham |
| item falha, `on_item_failure: continue` | subgrafo do item cascateia; fanout passa; evidência lista os itens que caíram |

Zero itens passar (em vez de falhar) é escolha discutível — "nenhuma migration pendente" é o
caso normal do fluxo real. O `warn` na evidência é o que impede isso de virar sucesso silencioso.

---

## 8. Decisão 7 — `repro` expande em dois nós

```yaml
- id: S02
  kind: repro
  command: "go test -run TestBug ./..."
  fixed_by: S03
```

Compila para dois nós, pelo mesmo princípio da Decisão 6a:

| Nó gerado | Depende de | Passa quando |
|---|---|---|
| `S02/before` | o que `S02` dependia | o comando **falha** (o bug reproduz) |
| `S02/after` | `S03` | o comando **passa** (o bug sumiu) |

Se `S02/before` passar (o bug não reproduz), o run falha ali com essa mensagem — é o veredito
mais valioso do tipo: o fix ia ser escrito contra um bug que não existe.

Conforme a decisão em aberto #1 do roadmap: **o tipo é implementado, o eixo de incidente não é
ressuscitado.** A recipe de exemplo vive em `testdata/`, não em `templates/`.

---

## 9. Decisão 8 — `recipe` no ledger (fecha dívida da F1)

`ops.startIdentity` lê a frontmatter do `tasks.md` do projeto e, se `generated_by` casar
`corvex-recipe:<nome>`, preenche `Identity.Recipe`. Best-effort: `tasks.md` ausente (caminho de
planning) mantém `Recipe: ""`, que é exatamente o discriminador que a F1 preservou de propósito.

Sem campo novo, sem estado novo, e o `recipe == ""` continua significando "não usou recipe".

---

## 10. Exemplo completo — a forma da autopilot

É o aceite da fase ("um recipe expressa a forma da autopilot"). Reproduz o gate canônico de
migration da `f0-fluxo.md`: computacional → inferencial independente → aplica no barato →
verifica computacionalmente → senão humano.

```yaml
name: feature-pipeline
description: Uma feature inteira, do board ao STG.

stages:
  - id: S01
    kind: tool
    title: Descobrir stories da feature
    command: "az-stories --feature $FEATURE_ID"
    produces: items

  - id: S02
    title: Implementar cada story
    fanout:
      over: S01
      max_items: 20
      max_parallel: 3
      template:
        - id: impl
          kind: code
          title: "Implementar {{ item }}"
          gates:
            - nature: policy
              max_attempts: 2          # loop de fix cap 2
            - nature: inferential
              label: "Review de código"
        - id: unit
          kind: test
          command: "npm test -- {{ item }}"
          depends_on: [impl]

  - id: S03
    kind: tool
    title: Classificar migration
    command: "./scripts/classify_migration.sh"
    depends_on: [S02]
    gates:
      - nature: inferential
        reviewer: dba
        label: "Review de migration (agente dba)"

  - id: S04
    kind: tool
    title: Aplicar migration no shadow db
    command: "npm run migrate:shadow"
    depends_on: [S03]
    gates:
      - nature: computational
        label: "Schema pós-migrate"
        command: "./scripts/schema-check.sh"

  - id: S05
    kind: test
    title: stg-validator
    command: "./scripts/stg-validator.sh"
    depends_on: [S04]

  - id: S06
    kind: tool
    title: Aplicar migration em STG
    command: "npm run migrate:stg"
    depends_on: [S05]
    gates:
      - nature: human
        when: before
        prompt: "Aplicar esta migration em STG?"
        label: "Aprovação de STG"
    evidence:
      - kind: sql
        label: "Migration"
        required_reading: true
        from: "cat migrations/pending/*.sql"
      - kind: diff
        label: "Plano de query"
        required_reading: true
        from: "./scripts/explain-plan.sh"

  - id: S07
    kind: tool
    title: Merge
    command: "./scripts/ship.sh"
    depends_on: [S06]
    gates:
      - nature: policy
        when: before
        branch_not: [main, develop]     # merge por nível: para se o alvo for protegido
```

Cobre os quatro `kind` menos `repro` (que não tem consumidor no fluxo, por decisão do roadmap),
as quatro naturezas de gate, fan-out com teto, e evidência com `required_reading`.

---

## 11. O que muda em cada pacote

| Pacote | Mudança | Tamanho |
|---|---|---|
| `internal/recipe` | `Stage.Gates`, `Stage.Evidence`, `Stage.Fanout`; validação nova; tripwire de id | médio |
| `internal/types` | `Task` ganha os mesmos campos (é o que atravessa para o `tasks.md`) | pequeno |
| `internal/task` | `headingRe` aberto; writer serializa gates/evidência/fanout | pequeno, **sensível** |
| `internal/gate` (**novo**) | contrato de evidência, arquivo de gate, decisão, leitura por segundo processo | médio |
| `internal/step` | dispatch por `kind`; execução dos 4 gates; produção automática de evidência; `human_gate.go` reescrito | grande |
| `internal/orchestrator` | expansão de fan-out entre ondas; revisão do DAG in-place | médio, **sensível** |
| `internal/ops` | `GateList`, `GateShow`, `GateApprove`, `GateReject`; recipe na identity | médio |
| `cmd` | `corvex gate …` (ver §14) | pequeno |

`internal/gate` como pacote próprio, e não dentro de `step`: quem lê um gate é `ops` (para o
segundo processo) e quem escreve é `step`. Pôr o formato dentro do executor faria o leitor
importar o executor — o mesmo erro que o invariante 10 nomeia para `ops`/`tui`.

---

## 12. Plano de execução — 4 ondas

Cada onda fecha com `go test ./...` verde e os dez invariantes. Ondas 1–3 não mudam nenhum
comportamento observável; a onda 4 é a que muda (§13).

| Onda | Entrega | Prova |
|---|---|---|
| **1** | schema (recipe/types/task), validação, tripwire de id, `headingRe` aberto, `Identity.Recipe` | recipe com id fora de `S\d+` deixa de perder stage silenciosamente; golden de compile mostra os campos novos |
| **2** | `internal/gate` + evidência: schema, produtores automáticos, arquivo em `runs/gates/`, metadado no ledger | canário: evidência com `/Users/<user>` no `content` → `git grep` na história **vazio** |
| **3** | gates computacional / inferencial / política no executor | mutação: gate `policy max_attempts: 1` fica vermelho onde `max_attempts: 2` fica verde |
| **4** | human gate bloqueante + `corvex gate` + fan-out com ondas | **dois processos reais**: um `corvex run` parked, um segundo processo aprovando, run terminando exit 0 |

O aceite da onda 4 é o único que conta como aceite da fase, e ele é cross-process por
construção — pelo mesmo motivo da F1: o contrato que atravessa a fronteira de processo é o
formato em disco, e chamada de função não prova formato em disco.

### Aceite mecânico da fase

- `go test ./...` verde; cobertura combinada **≥ 60%** no `-coverpkg` do roadmap (hoje 86,1%).
- Um `corvex run` com o recipe de §10 chega ao gate humano, entra em `parked` no índice global,
  e um **segundo processo** o vê em `corvex gate list`.
- `corvex gate approve` de outro processo destrava e o run termina exit 0.
- `corvex gate approve` sem `--ack` completo é **recusado**, listando o que falta.
- Fan-out sobre 5 itens descobertos em runtime gera 10 nós em `tasks.md`, roda em 2 ondas,
  e sobrevive a `kill -9` + resume sem redescobrir.
- Canário de segredo (invariante 5) refeito **com evidência**: `ANTHROPIC_API_KEY` e path
  absoluto no `content` de uma evidência → `git grep` na história vazio.
- Invariantes 4a/4b/6/7/8/9/10 em zero. Nada de Azure/SmartCare no binário: o exemplo de §10
  vive em `testdata/`, não em `templates/`.

---

## 13. Mudanças de comportamento visíveis (o que quebra)

Cinco. As três primeiras são inevitáveis para o aceite da fase; as duas últimas eu poderia
evitar e escolhi não evitar — diga se discorda.

| # | Mudança | Golden afetado | Evitável? |
|---|---|---|---|
| 1 | `human-gate` **bloqueia** em vez de matar o run | `run_help.txt` (texto de `--approve-gates`) | não — é o aceite |
| 2 | `--approve-gates` muda de descrição | `run_help.txt` | não |
| 3 | `corvex gate …` existe | goldens de help novos | não — é o aceite (mas ver §14) |
| 4 | `headingRe` aceita id fora de `S\d+` | testes de heading malformada | **sim**, ao custo de fan-out impossível |
| 5 | Recipe com id ilegível passa a **falhar na validação** em vez de perder stage calada | nenhum hoje | **sim**, ao custo de manter uma perda silenciosa |

Sobre a #5: pela política de "caracterização registra o que É", a inclinação natural seria
congelar o bug e anotá-lo. Não congelei porque a F2 **precisa** mintar id, e mintar id em cima
de um parser que come silenciosamente o que não entende é construir sobre a falha. Se você
preferir congelar, o custo é a opção (b) da §7c e ids de fan-out ilegíveis.

Nenhuma toca nos 14 bugs congelados da `f-1-anomalias.md`. Conferido: nenhum deles mora em
`internal/recipe/`, `step/human_gate.go`, `orchestrator/schedule.go` ou `task/writer.go`.
O que a `f-1-anomalias.md` registra perto daqui é outra coisa: uma **lacuna de cobertura
declarada** em `cmd/recipe.go:62-64` (caso escrito, golden visto, caso removido porque a
mensagem do syscall muda entre darwin e linux). Não é bug congelado e não muda com esta fase.

---

## 14. A colisão com a F3 — precisa da sua decisão

**O aceite da F2 exige `corvex gate approve`. A F3 é o gate humano que desenha a superfície de
comando, e ela ainda não rodou.** O roadmap pede as duas coisas.

A F1 resolveu tensão parecida por abstinência (LEI 3: nenhum comando cobra novo, `corvex runs`
é assunto da F3). A F2 não tem essa saída: um gate liberado por outro processo *é* um comando.

O que fundamenta seguir mesmo assim: a decisão em aberto #3 do roadmap já fixou `gate` como
substantivo ("Mantenha `gate answer`; se a F3 concluir o contrário, registre e pare"), e a
tabela de superfície já lista `gate list|approve|reject|answer`. Então implementar
`gate list|show|approve|reject` não inventa vocabulário — aplica o que já está escrito.

`gate show` é o único nome que **não** está na tabela do roadmap. Ele existe porque a trava de
`required_reading` precisa de um lugar para imprimir a evidência, e enfiar isso em `gate list`
faria a listagem cuspir diffs. Registro como acréscimo que a F3 pode renomear.

`gate answer` (a pergunta no meio do run, tela 2g) **fica de fora da F2**: é outro eixo
(o agente pergunta) e não é aceite desta fase.

> **Decisão que preciso de você:** implementar `gate list|show|approve|reject` na F2, ou parar
> e antecipar a F3? Minha recomendação é implementar — mas a alternativa é legítima, e se a
> resposta for "antecipa a F3", esta fase para aqui e o documento vira insumo dela.

---

## 15. Riscos, e o que eu cortaria

**O ponto mais fraco deste desenho é a expansão do DAG in-place (§7a).** Todo o resto adiciona;
essa muda o invariante mais silencioso do escalonador — que `schedule.tasks` e `schedule.run.DAG`
são imutáveis durante o walk. `runWaveParallel` guarda goroutines vivas dentro do `wg.Wait()`,
e `step.Run` compartilha ponteiros (`Anchor`, `TotalCostUSD`, `Completed`) com todos os steps.
A janela para fazer a troca é estreita e correta (pós-barreira), mas "estreita e correta" foi
exatamente a descrição da janela de rotação da F1, que a auditoria mediu em 207 ms de cegueira
real. Espero encontrar algo aqui, e vou atacar com `-race` + SIGKILL no meio da expansão antes
de chamar de pronto.

Outros dois, menores:

- **Evidência sem teto de tamanho.** Um `from: "cat migrations/*.sql"` num repo grande escreve
  megabytes num JSON que a UI vai carregar inteiro. Vou pôr teto por item (256 KiB) com
  truncagem marcada no próprio conteúdo. Não é elegante; é melhor que descobrir na F7.
- **`gate approve` num run morto.** Recusar (§6) depende de liveness, e liveness tem a janela
  de 40s de reuso de pid que a F1 aceitou e escreveu. Nada destrutivo pende disso aqui: o pior
  caso é recusar uma aprovação que era válida, e o usuário repete.

**O que eu cortaria se o escopo apertar**, em ordem: (1) `repro` — o roadmap já registra que
não tem consumidor, então é o tipo com menor retorno por linha; (2) `on_item_failure: continue`,
ficando só `abort`, que é o comportamento de hoje; (3) `wave_by`, ficando fan-out de uma onda
só. Nenhum dos três está no aceite da fase. **Não** cortaria evidência nem o gate bloqueante:
são os dois que a fase existe para entregar.

---

## 16. Dívidas que este desenho deixa registradas

- **`.corvex/escalations/` não é gitignored** e grava resumo de reviewer em arquivo versionável
  (achado 1b). Não é dívida da F2 e não foi tocada. É o mesmo modo de falha do `repo` no ledger.
- **`tool` e `test` são o mesmo executor.** A distinção é semântica até a F8 dar assinatura
  tipada a `tool`. Se a F8 não acontecer, o enum não pagou o preço dele.
- **`ReadIndex` continua sendo a visão de listagem** (incoerência nomeada pela F1). `gate list`
  vai usá-la, então durante a janela de rotação um gate pendente pode não aparecer por
  ~200 ms. Aceito: a próxima invocação acha, e nada destrutivo pende disso.
- **`policy` duplica config.** `max_cost_usd` no gate e `MaxCostPerTaskUSD` no config são o
  mesmo teto em dois lugares; o gate vence quando presente. Precisa de uma passada de
  consolidação, provavelmente na F5, quando a contabilidade por natureza existir.

---

## 17. Como foi construído — registro de fecho

> Escrito depois da implementação. As seções 1–16 são o desenho aprovado; esta é
> a diferença entre ele e o que existe em disco, mais como cada parte foi provada.

### O que mudou em relação ao desenho aprovado

Três coisas, todas na direção de mexer menos:

| Desenho dizia | Ficou | Por quê |
|---|---|---|
| `command` compila para `kind: tool` | **o `kind` declarado é preservado** em `tasks.md`; a normalização acontece no dispatch (`types.NormalizeKind`) | zero golden regravado, e `tasks.md` passa a guardar a palavra que o usuário escreveu em vez de uma tradução |
| `headingRe` aberto para um token de id geral | **alternação ordenada** `S\d+` primeiro, forma geral depois | o Go resolve alternação como um backtracking faria, então a forma histórica ganha e `## S01-Title ⬜ PENDING` continua lendo id `S01`. Sem isso, um id geral guloso engoliria o separador ASCII |
| `looseHeadingRe` aberto junto | **não foi tocado** | "parece um heading de task" deixa de ser decidível quando o id é arbitrário: um `## Notes` numa descrição começaria a falhar o parse. O lugar de recusar id ilegível é onde o usuário escreveu — `recipe.Validate` |

E uma decisão de escopo que o desenho não tinha nomeado: **conteúdo de evidência só é
persistido onde alguém precisa ler** — isto é, no arquivo de um gate humano. Evidência de
step sem gate humano vira metadado no ledger (natureza, label, status) e o conteúdo é
descartado. Mostrar evidência de qualquer step passado é a tela de run da F7, com a
telemetria de leitura da F5; guardar conteúdo que ninguém vai abrir só aumenta a
superfície de vazamento.

### Um defeito real, encontrado pela própria rede

A primeira versão de `expandFanouts` reescrevia `tasks.md` a partir de `s.tasks`. Isso
está errado por um motivo que não é óbvio: **a lista de tasks em memória é forma, não
estado.** Os status são escritos direto no arquivo pelo `Bookkeeper` e nunca espelhados de
volta nas structs, então a reescrita devolvia a todo mundo o status que tinha no load — e
o run seguinte batia em `DAG integrity violation`. Foi o teste de resume que pegou.
Consertado em `persistExpansion`: **disco é a autoridade do status, memória é a autoridade
da forma.**

### Buraco na rede da F-1, exposto e fechado

`resetFlagSet` (`cmd/characterize_test.go`) pulava toda flag de slice, com a nota "cmd/ has
none today". A `--ack` do `gate approve` é a primeira, e o valor vazava de uma invocação
in-process para a seguinte — invisível na ordem natural, vermelho sob `-shuffle=on`. O
conserto foi no harness (`pflag.SliceValue.Replace`), não no teste: rede que erra em
silêncio faz teste passar por acidente.

### Aceite da fase — provado com dois processos reais

`e2e/gate_test.go`, binário construído, `CORVEX_HOME` de scratch:

- `corvex run demo` chega ao gate, escreve `.corvex/runs/gates/<run_id>-S01.json`, grava
  `parked` no record **e** no índice global, e **não termina**;
- um **segundo processo** (`corvex gate list`) o vê, com o label e o required reading;
- `corvex gate approve <id> --step S01` **sem `--ack`** é recusado, nomeando o que falta;
- com `--ack Migration`, o run destrava sozinho e sai **exit 0**, com `S02` PASSED;
- `corvex gate reject` deixa `S01` FAILED e `S02` SKIPPED — pela cascata que já existia;
- `--approve-gates` continua não abrindo gate nenhum (caminho de CI).

### Controle positivo — cada verde sabe ficar vermelho

| Mutação | Efeito |
|---|---|
| `ValidTaskID` sempre `true` | 3 testes vermelhos em `recipe` e `task` |
| `humanGate` devolve `Approved` sem esperar | 2 testes vermelhos — inclusive o de bloqueio, que **na primeira versão passava** e por isso foi reescrito com a asserção de que o run ainda não terminou |
| `MissingAcks` devolve `nil` | vermelho nas **4 camadas**: `gate`, `ops`, golden de `cmd`, e2e |
| teto de `max_items` desligado | `TestFanout_MaxItemsIsADoorNotANet` vermelho |
| `persistExpansion` removido | 4 testes de fan-out vermelhos |

### Canário do invariante 5, refeito com evidência

Binário real, `auto_commit: true`, `ANTHROPIC_API_KEY=sk-ant-CANARY111` e `CANARY_TOKEN`
exportados, e uma evidência cujo `from` é `env | grep -E 'ANTHROPIC|CANARY' ; pwd` — ou
seja, desenhada para capturar segredo **e** path absoluto:

- o arquivo de gate **de fato contém** os dois valores (senão o teste seria vácuo) e está
  em `-rw-------`;
- `git grep sk-ant-CANARY111|CANARY222` em **toda a história**: vazio;
- path absoluto do repo em arquivo rastreado: vazio;
- `.corvex/runs/` inteiro não rastreado;
- `activity.jsonl` commitado tem `gate_pending` e `gate_decided`, e **zero** ocorrências de
  `CANARY` ou `/Users`.

### Invariantes no fecho

| # | Comando | Estado |
|---|---|---|
| 1 | `go test ./...` · `-race` · `-shuffle=on` · `./cmd/ -count=2` | **verdes** |
| 2 | coverpkg do roadmap | **85,1%** (piso 60; F1 fechou em 86,1% → −1,0pp) |
| 3 | caminho legado `corvex run <project>` | verde, e o e2e de identidade da F1 intacto |
| 4a / 4b | domínio em fonte / no binário | **0 / 0** |
| 5 | segredo em log, ledger ou commit | canário acima |
| 6 | cobra fora de `cmd/` | **0** |
| 7 | `fmt.Print`/`os.Exit` em `internal/ops` | **0** |
| 8 | maior fonte de produção em `cmd/` | **150** (`plain_renderer.go`, pré-existente) |
| 9 | maior fonte de produção em `internal/` | **370** (`task/parser.go`) — estourou 435 durante a fase e foi fatiado em `task/id.go` |
| 10 | `internal/ops` sem `internal/tui` | **0** |

**Goldens:** 1 regravado (`harness_root_help.txt`, +1 linha — o comando `gate` aparece no
help) e 8 novos para a superfície nova. Os outros 248 seguem byte-idênticos.
**LEI 2:** os 14 bugs congelados intactos — `git diff` vazio nos arquivos deles, e os
quatro marcadores conferidos no código de hoje (`status_render.go:96`,
`inspect_format.go:52`, `inspect_render.go:43`, `Setpgid` ainda ausente).
**Asserts** 2240 → 2498 (+258). Três `t.Skip` novos, todos guardas de `-short` nos testes
que sobem processo real; nenhum desliga assertion no caminho normal.
**gofmt:** os 6 arquivos sujos são pré-existentes e **intocados** por esta fase
(conferido contra `0a119ac`); nenhuma dívida nova.

### Dívidas registradas para depois

- **`.corvex/runs/` é `0755`.** Os arquivos de gate são `0600`, então o conteúdo está
  protegido, mas os nomes são listáveis por outro usuário da máquina. Herdado da F1
  (`MkdirAll(0o755)`); apertar o diretório é mudança de comportamento da F1 e não foi feita
  aqui.
- **`chargeGate` não entra no acumulador por task.** O custo de um gate inferencial é
  cobrado do run, não das tentativas do worker — de propósito, para a contabilidade de
  retry não mentir sobre o que o worker gastou. Quando a F5 separar custo por natureza,
  esse é o lugar que precisa de um terceiro balde.
- **`gate show` não está na tabela de comandos do roadmap.** Foi acrescentado porque a
  trava de `required_reading` precisa de onde imprimir evidência. A F3 pode renomear.
- **`wave_by` exige item estruturado.** Item de linha não tem campo para agrupar, e o erro
  diz isso; mas significa que usar ondas obriga o step descobridor a emitir JSON.
- **Nada de fan-out aninhado.** Um template não pode conter outro fan-out; a expansão roda
  uma vez sobre a lista original de stages. Não é bloqueante e não foi tentado.
- **`ReadIndex` continua sendo a visão de listagem** (incoerência nomeada pela F1), então
  `gate list` pode não ver um gate durante a janela de rotação do índice. A próxima
  invocação acha.
