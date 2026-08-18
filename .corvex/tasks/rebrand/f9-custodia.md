# F9 — Custódia de credencial (mecanismo entregue, **default é seu**)

> A F9 é gate humano no roadmap: *"segurança. Custódia de credencial e `DisallowedTools`
> não se aprovam sozinhos."* Esta leva entrega **o mecanismo**, com default idêntico ao de
> hoje. A decisão que precisa de você é **virar o default** — e é o §4.

## O que existe agora

> **CORREÇÃO PÓS-FECHO (auditoria).** Na primeira versão o mecanismo era **inerte na
> configuração default**: `collectAuthEnv` é um filtro de mapa puro, e filtrar o conjunto
> *encaminhado* não diz nada sobre o que o **filho** herda — todo sandbox e o exec direto
> montam o ambiente a partir de `os.Environ()`. A credencial negada chegava ao worker assim
> mesmo (e `sandbox.env_allowlist` também nunca restringiu nada nesses caminhos). Corrigido em
> `4017dcf`: a negação viaja no `RunRequest`/`ExecuteRequest` e é aplicada **onde o processo
> nasce**, vencendo inclusive uma entrada explícita em `Env`. O teste que faltava roda um
> processo de verdade e lê o próprio ambiente de volta.

### 1. `security.runner_only_env` — a credencial fica com o runner
Nomes **exatos** que nunca são repassados ao worker, mesmo quando um prefixo de
`sandbox.env_allowlist` casaria com eles. A negação vence qualquer prefixo, e isso tem teste
próprio: uma credencial que o runner deveria guardar não pode depender de qual prefixo casou
primeiro.

*Por que exatos e não prefixos:* um prefixo que nega mais do que o autor quis **falha fechado
na direção confusa** — o run quebra em algum lugar sem relação e a causa fica invisível.

*O argumento de custódia:* credencial no ambiente do worker é credencial a uma chamada de
`Bash` de sair da máquina, e nenhum gate depois disso desfaz. Guardá-la com o runner mantém
o segredo no processo que um humano de fato autorizou.

### 2. `security.disallowed_tools` — o catálogo vira fronteira
Padrões de ferramenta que o worker nunca pode chamar, **unidos** ao bloqueio embutido dos
arquivos de estado do corvex. Config **acrescenta e nunca encurta**: fechar `Bash` é fechar
uma porta, e nenhum valor de config abre uma que o corvex decidiu manter fechada.

É o que separa catálogo de sugestão: se o caminho cru continua aberto, o agente que achar a
tool tipada inconveniente simplesmente shella, e o catálogo vira documentação.

### 3. `requires:` na recipe — preflight antes do primeiro token
```yaml
requires:
  - bin: az
    why: é como o ship abre o PR
  - env: AZURE_DEVOPS_EXT_PAT
```
Roda **antes da prévia de custo**. A cicatriz #1 do roadmap é exatamente isto: um run que
descobre no meio que falta uma CLI **já pagou** tudo até ali, e a falha chega vestida de
falha de task — o agente tenta de novo, falha de novo, e o orçamento de retry vai embora num
problema que modelo nenhum resolve.

O check **nunca imprime o valor** da variável, só "set"/"missing" — um preflight que ecoasse a
credencial seria o vazamento que ele existe para evitar. Tem teste.

Declarar `bin` e `env` no mesmo item é erro de recipe, não item ignorado: **declaração não
aplicada é pior que declaração nenhuma**, porque o autor acredita que está aplicada.

## 4. A decisão que é sua

Hoje o worker recebe **toda** variável que casa com a allowlist. O mecanismo acima permite
tirar credencial do worker, mas **o default continua o de hoje** — nada quebra, e nada está
protegido até alguém escrever a lista.

Virar o default significa: **o worker não recebe credencial nenhuma além da do modelo, a menos
que a recipe declare que aquele step precisa**. É a forma que o roadmap descreve
(*"credencial no runner, nunca no ambiente do worker"*), e o custo é real:

- toda recipe que hoje faz `az`/`gh`/`psql` **dentro de um step de worker** para de funcionar
  até declarar o que precisa;
- a alternativa que não quebra nada — o worker pedir ao runner que execute a tool com a
  credencial — **é a F8** (catálogo de tools tipadas), que ainda não existe;
- sem a F8, virar o default hoje troca "credencial exposta" por "fluxo bloqueado", e a saída
  natural do usuário sob pressão é desligar a proteção.

**Recomendação:** manter o default até a F8 dar aos steps um caminho para pedir a operação em
vez da credencial, e nesse momento virar o default junto com ela. Enquanto isso, o mecanismo
já permite fechar caso a caso o que dói mais.

**O que eu preciso de você:** aprovar essa ordem (mecanismo agora, default junto com a F8),
ou mandar virar o default já — o que é defensável se você preferir fluxo quebrado a segredo
exposto, e nesse caso a lista de exceções vira trabalho de configuração explícito.

## Fora de escopo, registrado

- **`DisallowedTools` não alcança MCP.** A política vale para as ferramentas do provider; um
  servidor MCP declarado no `config.yaml` é outro caminho e não é filtrado aqui.
- **O preflight checa presença, não permissão.** `az` na PATH não quer dizer sessão válida;
  checar isso exige executar a CLI, o que é lento e tem efeito colateral.
- **`runner_only_env` protege o worker, não o `command` de um step `tool`.** Um stage
  computacional roda com o ambiente do runner por construção — é o runner executando. É a
  fronteira certa, e vale escrever que ela existe.
