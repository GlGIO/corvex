# Bernstein vs Corvex: o que vale trazer

## Resumo

1. **O "replay byte a byte" não refaz o trabalho dos agentes.** O Bernstein grava só as chamadas que passam pelo `call_llm` interno: planner, queue review, janitor `llm_judge`. O tráfego dos agentes CLI (Claude Code, Codex…), que é onde o código é escrito, não é gravado em nenhum caminho. Com o default `internal_llm_provider: none`, uma run não grava nada. O que é de fato determinístico é a sequência de decisões de agendamento sobre resultados já gravados, verificada por uma cadeia de hashes.
2. **"Sem LLM no loop de coordenação" tem uma exceção.** Há uma exceção documentada no próprio repositório: o *manager queue review*. Ele roda a cada falha ou a cada 3 conclusões e pode reatribuir, cancelar, repriorizar e criar tarefas. Isso vale quando há um provider LLM interno configurado; veja a seção 4.
3. **O maior ganho para o Corvex é isolamento, não auditoria.** Vale trazer o isolamento por worktree, porque ele corrige um bug real do modo paralelo do Corvex. A trilha assinada é o recurso mais vistoso e o menos útil para o Corvex.

---

## 0. Repositório

- **Upstream canônico:** `github.com/sipyourdrink-ltd/bernstein`. O clone de `github.com/chernistry/bernstein` aponta para o mesmo projeto.
- **Snapshot lido:** commit `2ad8460` (2026-10-06), versão `3.21.0`, licença Apache-2.0.
- **Clonado em:** `.../scratchpad/bernstein-src`. Nada do repositório foi executado.
- **Escala:** cerca de 5.600 arquivos `.py` e 853 mil linhas em `src/`. O projeto é beta e mantido por uma pessoa só. Exemplos de tamanho: `core/security/audit_chain.py` tem 10.174 linhas e `core/tasks/task_lifecycle.py` tem 5.762.
- **Convenção de caminhos:** os caminhos do Bernstein abaixo são relativos a `src/bernstein/`. Os do Corvex são relativos a `o checkout do corvex`.

---

## 1. Arquitetura e mecanismos concretos do Bernstein

### 1.1 Fluxo

- **Decompor:** o `ManagerAgent.plan` (`core/orchestration/manager.py:243`) faz uma chamada LLM, em `manager.py:314`.
  - Pode haver mais duas chamadas: uma de pesquisa (`:290`) e um re-prompt quando a verificação de "vertical slice" falha (`:365-381`).
  - Cada tarefa sai com `role`, `owned_files`, `depends_on` e `completion_signals`.
- **Spawn:** cada tarefa ganha um worktree em `.sdd/worktrees/<session>`, no branch `agent/<session>` (`core/git/worktree.py:1057`).
- **Verificar:** primeiro roda o janitor; depois, em cascata, quality gates, rule enforcement, cross-model e formal (`core/tasks/task_lifecycle.py:3440-3466`).
- **Aprovar e fazer merge:**
  - Se a verificação falhou, `skip_merge=True` (`task_lifecycle.py:3687-3702`, chamado em `:5192`).
  - Se não, o merge acontece no reap (`task_lifecycle.py:3924`).

### 1.2 Scheduler (tick loop)

- **Ordem de seleção** (`group_by_role`, `core/orchestration/tick_pipeline.py:501-640`):
  - As tarefas são agrupadas por role.
  - Dentro de cada role, a ordem segue a chave `(prioridade efetiva, -custo estimado, prioridade original, -age_boost, task.id)` (`:592-622`).
  - O `task.id` no fim desempata, o que torna a ordenação total.
- **Afinidade por arquivo** (`:405-438`): tarefas cujos `owned_files` se sobrepõem, mesmo por transitividade, vão para o mesmo grupo e caem no mesmo agente. Assim, duas tarefas que mexem no mesmo arquivo nunca rodam em paralelo.
- **Intercalação:** os lotes são intercalados em round-robin entre roles, e roles "famintos" vêm primeiro (`:368-402`).
- **Ressalva de determinismo:** o *age boost* usa `time.time()` (`:575`, `:602-607`). A decisão é determinística dados os inputs, mas o relógio de parede é um desses inputs.
- **LLM dentro do loop:**
  - `_should_trigger_manager_review` (`orchestrator.py`, logo antes de `:5132`) dispara com ≥3 conclusões, com qualquer falha ou após 5 min.
  - `_run_manager_queue_review` (`core/orchestration/orchestrator.py:5132-5180`, chamado em `:2235`) aplica `reassign/cancel/change_priority/add_task`.
  - O doc `docs/architecture/WHY_DETERMINISTIC.md` admite isso. Mesmo assim, o README afirma "no model in the coordination loop".

### 1.3 Janitor

- **O que é:** o janitor não é um "lint/tipos/testes" fixo. Ele avalia os **completion signals** que o planner declarou para cada tarefa: `verify_task` (`core/quality/janitor.py:494-525`) despacha para `evaluate_signal` (`:290-350`).
- **Tipos de sinal:**
  - `path_exists`, `glob_exists`, `test_passes`, `file_contains`
  - `llm_review`, `llm_judge`, `absence_verified`
  - sinais de artefato: `schema_valid`, `criteria_match`, `hash_stable`, `figures_grounded`
- **`test_passes`** (`janitor.py:2269-2316`):
  - Roda `subprocess.run(command, shell=True, cwd=workdir, timeout=120)` e passa se o exit code for 0.
  - O comando vem da saída do planner, que é um LLM. O comentário diz que não é input de usuário, mas o texto foi gerado por LLM.
- **Guarda contra "rubber stamp"** (`janitor.py:191-215`, `:1110-1213`):
  - Atribui commits à tarefa via `git log --grep <task_id>`. O fallback é o último commit filtrado por `owned_files`.
  - Diff vazio é rejeitado, a não ser que um sinal "não trivial" tenha passado. A lista desses sinais está em `_NONTRIVIAL_SIGNAL_TYPES` (`:110`).
- **Lint, tipos e testes de verdade** ficam no `GateRunner` (`core/quality/gate_runner.py`):
  - Os gates `lint`, `type_check`, `tests`, `security_scan`, `mutation_testing` etc. ficam em `:497-512`.
  - Gates condicionais usam a expressão `changed_files.any('.py')` (`:84`).
  - Há cache de veredito cuja chave é `sha256(config do gate + hash dos arquivos alterados)` (`:1899-1918`). Vereditos `inconclusive` também são cacheados (`:200-212`).
- **Reparo de gate** (`_maybe_schedule_gate_repair`, `task_lifecycle.py:3345`):
  - Quando um quality gate falha, o Bernstein cria **uma** tarefa de reparo no mesmo branch, com o worktree preservado.
  - A tarefa de reparo é marcada com `gate_repair_attempted=True`, então não há uma segunda.

### 1.4 Replay determinístico

São quatro mecanismos diferentes, que convém não confundir.

**(a) Gravação e replay de respostas do LLM** (`core/orchestration/deterministic.py`)

- **Chave:** `sha256(model \x00 prompt \x00 provider \x00 repr(temperature) \x00 max_tokens)` (`:142-170`).
- **Gravação:** `record()` faz append em `.sdd/runs/<run_id>/llm_calls.jsonl` (`:258-299`). A linha leva `ts, key, model, provider, temperature, max_tokens, prompt_len, response`; o prompt em si não é salvo.
- **Replay:** `get_replay()` mantém uma FIFO por chave (`:301-346`). A N-ésima chamada recebe a N-ésima resposta.
- **Modo estrito (default):**
  - Um miss levanta `ReplayMissError`.
  - Consumir uma chave mais vezes do que ela foi gravada também conta como miss.
  - Uma run sem gravação nenhuma levanta `ReplayRecordingMissingError` antes de qualquer spawn (`:426-450`).
- **Ponto de interceptação:** só o `call_llm` (`core/routing/llm.py:243-297`). Os agentes CLI não passam por ali.
- **Consequência prática:** o prompt do planner inclui a árvore de arquivos, o README e as tarefas existentes. Qualquer mudança no repositório vira miss.

**(b) Journal Merkle-encadeado, sempre ligado** (`core/replay/journal.py`)

- **Hash do payload:** `payload_hash = sha256(JSON canônico do payload sem ts/elapsed_s)` (`:195-218`).
- **Hash do evento:** `event_hash = sha256(JSON{prev_hash, event_type, payload_hash, index})` (`:221-260`).
- **Gênese:** `""` (`:112`).
- **Escrita:** append feito sob um `RLock` (`:547-620`).
- **Falha de escrita só gera warning** (`:612-621`). O journal é *fail-open*.
- **Hash sem chave:** quem controla o arquivo consegue recalcular a cadeia inteira. Por isso a "identidade" do journal exige um selo externo, e sem selo o veredito é `unverifiable` (`docs/operations/deterministic-replay.md`).

**(c) `replay --re-derive`** (`core/replay/rederive.py`)

- Recebe o plano gravado e os resultados gravados e passa os dois por um `_CoordinationState` próprio (`:151-240`). Esse estado aplica 4 regras: `dependency_not_completed`, `task_already_claimed`, `task_not_in_plan`, `outcome_for_unclaimed_task` (`:94-103`).
- **Não importa o scheduler real.** Os únicos imports são `replay.diff` e `replay.journal` (`:36-53`). Ou seja, ele prova que a sequência é *admissível*, não que ela é *a* sequência que o `tick_pipeline` teria escolhido.
- É o caso "controle que reimplementa a guarda mede a si mesmo".

**(d) WAL de decisões** (`core/persistence/wal.py`)

- É outra cadeia de hash, com `fsync` a cada entrada (`:377-620`) e um índice de entradas não commitadas para recuperação de crash (`:78-110`).
- **Redundância:** junto com o journal, a audit chain e o lineage spine, são quatro logs encadeados que se sobrepõem.

### 1.5 Trilha assinada

**Audit chain com HMAC** (opt-in, `BERNSTEIN_AUDIT=1`; `core/security/audit.py`)

- **Cálculo:** `hmac_sha256(key, domain_prefix + prev_hmac + json.dumps(entry, sort_keys=True))` (`:394-402`).
- **Chave:**
  - Fica em `$BERNSTEIN_AUDIT_KEY_PATH`, ou em `$XDG_STATE_HOME/bernstein/audit.key`, ou em `~/.local/state/bernstein/audit.key` (`:165-180`). Fica fora de `.sdd/` de propósito.
  - O modo `0600` é exigido (`:71`, `:183-210`).
- **Verificação:** linha a linha, com `compare_digest` em `prev_hmac` e no MAC (`:852-908`).

**Run receipt** (`core/replay/run_receipt.py`)

- **O que amarra:**
  - a projeção do journal sem timestamps, com a head;
  - o lineage spine sem os tags HMAC;
  - opcionalmente, uma fatia da audit chain re-encadeada.
- **Assinatura:** Ed25519 sobre DSSE PAE do "binding block" canônico (`:357-361`, `:597-718`). A chave pública vai embutida como JWK OKP (`:806-818`).
- **Verificação** (`:821-1085`):
  - recalcula todas as heads a partir dos bytes do recibo;
  - reconstrói o binding;
  - só então verifica a assinatura (`:1083`).
- **Sem `--public-key`, o resultado é só "integrity-only".** A chave embutida é TOFU, então quem controla o arquivo pode assinar de novo.
- **Rotação e revogação:** existe uma cadeia de sucessão de chaves (`core/security/receipt_key_chain.py`).

### 1.6 Política de retry (`core/tasks/task_lifecycle.py`)

- **Teto:** `effective_max = min(task.max_retries, …, _MAX_REGULAR_TASK_RETRIES=2)` (`:108-121`, `:715-717`). São 3 tentativas no total.
- **Backoff:** `min(base_delay·2^n, 300s)`, com `base_delay=30s` por padrão (`:741-743`).
- **Limite por motivo da falha** (`:898-926`, `:971-990`):
  - um marcador transitório (`rate limit`, `timeout`, `503`, …) dá limite 3;
  - um marcador fatal (`syntaxerror`, `fatal`) dá limite 0.
- **Escalonamento de modelo e effort** (`_choose_retry_escalation`, `:447-492`; ladder em `:381`):

  | Situação | Modelo | Effort |
  |---|---|---|
  | `error_max_turns` | mantém | sobe |
  | `blocking_limit` | `opus` | `max` |
  | role `architect` ou `security`, ou scope `LARGE` | `opus` | `max` |
  | 1º retry (demais casos) | mantém | sobe |
  | 2º retry em diante | sobe um degrau no ladder `haiku → sonnet → opus` | `high` |

  Um modelo fixado pelo operador nunca é substituído.
- **Quarentena entre runs:** por título de tarefa (`:666-735`). Uma tarefa esgotada vai para a DLQ (`runtime/dlq.jsonl`).

### 1.7 Merge

- **Fluxo** (`merge_with_conflict_detection`, `core/git/git_pr.py:290`, chamado em `core/agents/spawner_merge.py:1020`):
  - roda `git merge --no-commit --no-ff <branch>`;
  - faz checagem de sintaxe e aborta se falhar;
  - aplica uma deny-list no conjunto staged, abortando se algum arquivo de runtime entrou;
  - aborta em conflito e devolve a lista de arquivos;
  - commita.
- **Probe sem working tree:** `git merge-tree --write-tree --name-only -z` (`core/git/merge_tree_probe.py:359-448`, requer git ≥ 2.38).
- **Fila de merge:** `core/git/merge_queue.py:200`.
- **"Read-set admission" não está ligada.** A ideia é recusar o merge se arquivos que o agente *leu* mudaram na base.
  - `git_pr.py:319` só checa se `task_id`, `journal_path` e `worktree_root` vierem preenchidos.
  - O único chamador de produção (`spawner_merge.py:1020-1024`) não passa nenhum deles.
  - Além disso, o diff usado é `git diff --name-only HEAD <branch>` (`core/git/read_set_admission.py`, dentro de `check_read_set_changed`), não `merge-base..HEAD`.

---

## 2. O que trazer para o Corvex (ordenado por valor/custo)

Custo: **P** = até ~1 dia, **M** = alguns dias, **G** = semanas.

| # | Mecanismo (origem no Bernstein) | Corvex hoje | Valor | Custo | Risco |
|---|---|---|---|---|---|
| 1 | **Gates `after` antes do checkpoint.** O Bernstein encadeia janitor → gates → aprovação → merge (`task_lifecycle.py:3440-3466`, `:5151-5192`, `:3687`). | **Bug.** `finishPassedTask` (`internal/step/ai_task.go:207`) já grava PASSED (`internal/step/passed.go:111`) e commita o checkpoint (`passed.go:144`) **antes** de `runGates(After)` (`ai_task.go:44`). O comentário em `ai_task.go:38-40` diz o contrário. Um gate que recusa deixa a mudança commitada. | Alto: corrige uma promessa que hoje é falsa. | P | Baixo. Mexe na ordem; os testes de golden do ledger podem precisar ser regravados. |
| 2 | **Isolamento por tarefa no modo paralelo.** Worktree por sessão (`core/git/worktree.py:1057`), merge `--no-commit --no-ff` com abort (`git_pr.py:290`) e afinidade por `owned_files` (`tick_pipeline.py:405-438`). | **Bug.** Com `execution.parallel`, todas as tarefas da onda compartilham o diretório (`internal/orchestrator/schedule.go:148-181`). Um retry de uma tarefa roda `git checkout . && git clean -fd` (`ai_task.go:108`, depois `internal/recovery/recovery.go:90-130`) e apaga o trabalho em voo das irmãs. O `git add -A` do checkpoint (`recovery.go:134-155`) commita o trabalho das outras como se fosse desta tarefa. | Alto. | **P** (paliativo: forçar serial quando há retry, ou desligar o parallel) / **M** (worktree por tarefa e merge de volta) | O merge de volta cria conflitos explícitos que hoje são silenciosos; é preciso decidir quem os resolve. O symlink `.corvex` precisa existir em cada worktree (`internal/ops/start_worktree.go`). |
| 3 | **Falha de gate vira diagnóstico para uma tentativa extra, só uma.** É o `_maybe_schedule_gate_repair` (`task_lifecycle.py:3345`). | Ausente. A recusa marca a tarefa FAILED e os dependentes SKIPPED, sem retry (`internal/step/gates.go:206-220`, `ai_task.go:45-48`). O canal de diagnóstico já existe ("Previous Attempt Failed", `internal/step/worker.go:305-310`). | Alto. Uma falha de gate computacional é o feedback mais barato e objetivo que existe. | P | Custo extra limitado a 1 tentativa; precisa contar no teto de custo da tarefa. |
| 4 | **O planner emite critérios verificáveis por máquina** (completion signals, `janitor.py:290-350`, `:494`). | Parcial. A infraestrutura existe: `types.Gate` (`internal/types/types.go:108`) e o parser aceita `gates:` por tarefa (`internal/task/parser.go:68`, `:293`). Mas o prompt do planner só pede "Critérios de sucesso" em prosa (`internal/planning/planner.go:205-248`). | Alto. Transforma "o reviewer acha que passou" em "o comando passou". | P–M | O comando é escrito por LLM e roda no host (o Bernstein usa `shell=True`); restrinja a uma allowlist (`go test`, `make <alvo>`, …). O planner também pode escrever checagens triviais: replique a ideia de `_NONTRIVIAL_SIGNAL_TYPES` (`janitor.py:110`) exigindo pelo menos um sinal de execução. |
| 5 | **Retry por classe de falha e com backoff** (`task_lifecycle.py:898-926`, `:971-990`, `:741-743`). | Ausente. Todas as falhas viram retry imediato, até `max_retries+1` vezes (`ai_task.go:54-66`); não há backoff (grep vazio em `internal/`). | Médio. Um rate limit do CLI hoje queima tentativas em sequência; uma falha fatal gasta retries à toa. | P | Classificar por regex no texto de erro é frágil. Use o exit code e os campos do stream-json quando houver. |
| 6 | **Ladder de escalonamento com default ligado** (`task_lifecycle.py:381`, `:447-492`). | Parcial. Existe `upgrade-model` por categoria de FAIL (`internal/step/investigation.go:34-39`), mas o mapa default é vazio (`internal/config/config.go:337-357`). Sem configuração, o modelo nunca muda. | Médio. | P (só o default de config) | Aumenta o custo; o teto de $5 por tarefa (`ai_task.go:298-315`) passa a ser atingido mais cedo. Mantenha opt-out. |
| 7 | **Rejeitar PASS com diff vazio** (`janitor.py:1110-1213`). | Ausente como regra. `ChangedFiles` só alimenta o anchor (`passed.go:116`); o que barra hoje é o reviewer e a exigência de TASK-REPORT/HANDOFF (`passed.go:53-66`). | Médio. Pega "PASS sem trabalho". | P | Tarefas legítimas de investigação ou no-op precisam declarar isso (por exemplo, `type: research`). |
| 8 | **Probe de merge sem tocar no working tree** (`merge_tree_probe.py:359-448`). | Ausente. O único merge é o A/B (`internal/step/abrun.go:238-248`), que só devolve o erro. | Médio, e só depois do item 2. | P | Precisa de git ≥ 2.38. |
| 9 | **Gravar as chamadas de LLM do planner e do reviewer** (`deterministic.py:142-346`). | Ausente: não há gravação, cassette nem cache (grep de `cassette`, `replay` e `record` vazio em código de produção). | Médio-baixo: fixtures para testes de golden do planner/reviewer, e reprodução de um veredito estranho. | M | O reviewer do Corvex é agêntico (Read/Glob/Grep/Bash, `internal/step/reviewer.go:53-77`), então a resposta depende do estado do repo, não só do prompt. O replay só vale com a árvore idêntica, e a chave precisa incluir o tree hash (`git write-tree`). O próprio Bernstein desistiu de gravar agentes CLI. |
| 10 | **Hash chain no ledger** (`journal.py:195-262`, com o timestamp fora do hash). | Ausente: `activity.jsonl` não tem hash (`internal/activity/ledger.go:134-187`), e `Read` pula linhas malformadas em silêncio (`ledger.go:192-214`). | Baixo-médio. O ganho real é detectar linha rasgada ou editada e apontar "o primeiro passo divergente". | P | Sem chave, a cadeia se recalcula toda; só tem valor ancorada em algo externo. O ledger já vai para o histórico git (`ledger.go:16-24`), e o próprio git já é essa âncora. Veja o ponto menos seguro (seção 4). |

**Bugs do Corvex encontrados no caminho** (fora do Bernstein, mas relevantes):

- **`CmdRetry` durante a run não limpa `s.terminal`** (`internal/orchestrator/commands.go:96-108`, comparado com `schedule.go:106-111`). Um retry pedido no meio da run provavelmente nunca re-executa a tarefa.
- **A/B não commita nada nos worktrees antes do merge** (`abrun.go`, sem `commit`). Se o CLI do worker não commitar sozinho, as edições do vencedor se perdem quando o worktree é removido. Não reproduzi.

---

## 3. O que não trazer, e por quê

- **Recibo Ed25519, DSSE e cadeia de sucessão de chaves** (`run_receipt.py`, `receipt_key_chain.py`): resolvem prova de proveniência para um auditor terceiro. O Corvex é uma CLI de um operador sobre o próprio repo, e o histórico git já serve de âncora. É custo G para um problema que o Corvex não tem.
- **Audit chain com HMAC** (`audit.py`, `audit_chain.py` com 10 mil linhas): mesmo argumento. Além disso, um HMAC com a chave na mesma máquina não protege contra o próprio operador, que é o único ator.
- **Quatro logs encadeados sobrepostos** (journal, WAL, audit, lineage spine): se trouxer algo, traga um só (item 10).
- **`replay --re-derive`:** reimplementa as regras num `_CoordinationState` à parte (`rederive.py:151-240`) em vez de passar pelo scheduler real. Mede a si mesmo. No Corvex, `NextReady` (`internal/dag/dag.go:161-180`) já é puro e ordenado; um teste de unidade sobre ele vale mais.
- **Manager queue review por LLM** (`orchestrator.py:5132`): é justamente a fonte de não determinismo que o Bernstein diz evitar. O Corvex não tem isso, e é melhor assim.
- **Prioridade com age boost, roles e batching por role** (`tick_pipeline.py:501-640`): servem a uma fila aberta e longa com dezenas de agentes. O DAG do Corvex vem de uma spec e já é determinístico (`dag.go:111-225`, `:263-269`). Trazer isso adicionaria dependência do relógio.
- **Cache de veredito de gate por hash dos arquivos alterados** (`gate_runner.py:1899-1918`): não é seguro para testes, cujo resultado depende de arquivos que não mudaram. Ainda por cima cacheia `inconclusive`.
- **Read-set admission** (`git_pr.py:319`, `read_set_admission.py`): a ideia (conflito semântico, "o que eu li mudou") é boa, mas no Bernstein não está ligada em produção, e o Corvex nem registra os arquivos lidos (`tool_telemetry.go` guarda o nome da tool, não o input). Exigiria gravar o input das tools primeiro.
- **50+ adapters, GUI e volunteer compute:** fora de escopo.
- **Quarentena por título entre runs** (`task_lifecycle.py:666-735`): títulos do Corvex mudam a cada replan, então a chave não é estável.

---

## 4. Ponto menos seguro

A afirmação de que estou menos seguro é a do resumo, item 2: que o *manager queue review* põe um LLM no loop **na configuração padrão**.

- **O que está confirmado:** o gatilho é incondicional quanto ao provider. Ele dispara em qualquer falha, com ≥3 conclusões ou após 5 min (`orchestrator.py`, `_should_trigger_manager_review`), e as correções são aplicadas.
- **O que não rastreei:** `_resolve_manager_llm`. Com `internal_llm_provider: none`, a chamada pode simplesmente falhar e cair no `except` que só loga um warning (`orchestrator.py:5132-5180`). Nesse caso, no default, o loop fica sem LLM na prática, e a exceção vale só para quem configurou um provider interno.

A dúvida anterior, se o ledger do Corvex entra no git, foi resolvida: `git ls-files` mostra `.corvex/tasks/cost-split/activity.jsonl` rastreado neste repo. Por isso o item 10 continua com valor baixo.

---

## Fontes

- https://github.com/sipyourdrink-ltd/bernstein (clonado via https://github.com/chernistry/bernstein, commit 2ad8460)
- https://pypi.org/project/bernstein/
- `docs/architecture/WHY_DETERMINISTIC.md` e `docs/operations/deterministic-replay.md` no repositório
