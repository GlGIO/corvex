# Prompt para o claude.ai/design — UI do corvex

> Cole em uma sessão nova. Autocontido de propósito: transfere o domínio inteiro,
> deixa as decisões visuais em aberto. Gerado na rodada de rebrand (2026-08-13).

---

Estou construindo a UI de uma ferramenta de desenvolvimento chamada **corvex** e quero explorar o desenho antes de escrever código. Você não conhece nada sobre ela — vou te dar o domínio inteiro.

## O que é o corvex

Um runner que executa workflows de desenvolvimento com agentes de IA, no terminal, sem sessão de chat. Eu declaro um workflow em YAML (uma "recipe"), ele executa os steps, aplica gates entre eles e grava tudo em disco. Rodo ele em vários repositórios diferentes.

A diferença pra um CI comum: um step pode ser um agente de IA escrevendo código, e os gates decidem se aquilo avança — alguns são determinísticos (exit code de um script), outros são um segundo agente julgando, outros param e esperam um humano.

## Vocabulário (use exatamente estes termos)

**Run** — uma execução. Pertence a um repositório e a um projeto. Tem custo em dólares, duração e um resultado.

**Step** — uma unidade dentro do run. Quatro tipos, e essa distinção é o coração do produto:

- `code` — um agente de IA escreve ou corrige código. Caro, lento, não-determinístico.
- `tool` — operação com contrato fixo (criar work item, abrir PR, subir tag, rodar migration). Determinística, barata.
- `test` — roda a suíte, sobe o stack, verifica. Determinística.
- `repro` — reproduz um bug. Determinística com veredito **temporal**: precisa reproduzir ANTES do fix e parar de reproduzir DEPOIS.

Steps têm dependência entre si e rodam em **ondas**: a onda 0 roda em paralelo, as seguintes esperam as anteriores.

**Gate** — o que decide se o run avança. Quatro naturezas:

- **computacional** — exit code de um script. Rápido, confiável.
- **inferencial** — um agente independente julga (nunca o mesmo que produziu o trabalho). Lento, falível.
- **humano** — bloqueia até uma pessoa aprovar. Ex.: mergear em `main`, rodar migration em produção.
- **política** — regra do runner: teto de custo, máximo de 2 tentativas de correção, "não mergeia se o alvo for develop".

**Desfechos possíveis de um step:** `ready`, `parked` (travado esperando humano), `needsFix`, `blocked` (dependência não ficou pronta), `dropped`.

## Dados que realmente existem (não invente campos, use estes)

Cada evento gravado tem: timestamp, tipo, id do step, fase (`worker` / `review` / `plan` / `recovery`), número da tentativa, duração em ms, custo em USD, tokens de entrada, tokens de saída, status, mensagem.

Config do run: teto de custo total (default $25), teto por step ($5), timeout por step (20 min), e modelo por papel — planner, worker e reviewer podem ser modelos diferentes, com preços diferentes.

## As telas, por ordem de importância

1. **Caixa de gates** — o que está travado esperando *mim*, agora, em todos os repositórios: migration parkada, PR reprovado, gate humano aguardando. Se eu abrir a ferramenta e olhar uma tela só, é essa.
2. **Run ao vivo** — o que está rodando neste momento: qual step, qual onda, há quanto tempo, quanto já custou, e qual ferramenta está executando neste segundo.
3. **Histórico** — tudo que já rodei, em todos os repositórios. Quanto custou, o que passou, o que travou, onde.
4. **Um run específico** — abrir um run passado e entender o que aconteceu: sequência de steps, onde cada gate decidiu o quê, onde entraram retries, pra onde foi o dinheiro.

## Restrições

- Roda em **localhost**, aberta por um comando no terminal. Um usuário só, sem login.
- **Os botões EXECUTAM.** A UI dispara runs, aprova gates, pausa e mata run. A ideia é substituir o terminal no dia a dia, mas **em paralelo** com ele: tudo que a UI faz também dá pra fazer por CLI, e nenhum caminho existe só na UI.
- A UI é onde o **gate humano** acontece de verdade: ela guarda o consentimento, o runner guarda a credencial, o agente de IA não tem nenhum dos dois.
- **Vários repositórios ao mesmo tempo** — rodo em 3 repos diferentes e quero ver os três juntos.
- Ferramenta de dev, usada ao lado do terminal, muitas horas por dia. Dark mode não é enfeite.
- **Não é um board de tarefas.** Não substitui Jira/Azure Boards. A pergunta que ela responde é: *"o que meus agentes fizeram, estão fazendo, e o que está esperando por mim"*.

## O que eu quero de você

Explore o desenho — não assuma que eu já sei o que quero. Me mostre **2 ou 3 direções diferentes** pra tela principal antes de refinar uma delas.

Coisas que eu não decidi e onde quero sua opinião:

- Como mostrar visualmente a diferença entre um step **determinístico** e um step de **IA**? Essa distinção é a tese do produto e precisa ser óbvia à primeira vista.
- Como representar as ondas de dependência sem virar um Gantt ilegível?
- Custo: mostro sempre ou só quando importa? Um run de $0,40 e um de $23 precisam parecer diferentes.
- Um run travado há 3 dias esperando aprovação humana deveria gritar. Como?
- Um run é uma linha numa lista, um card, ou outra coisa? Eu tenho poucos runs por dia, não centenas.
- **A tela mais importante é a de APROVAR UM GATE.** Um gate que vira carimbo é pior que gate nenhum. O que a pessoa precisa ver pra aprovar "aplicar esta migration em STG" ou "mergear em develop" com segurança — e o que ela deveria ser *obrigada* a olhar antes de o botão habilitar? Me mostre esse fluxo com atenção.
- Como eu **disparo** um run pela UI: escolher o workflow, o alvo, o teto de custo. Isso é formulário, wizard, ou linha de comando embutida?
- Um run pode **fazer uma pergunta no meio** (o agente precisa de uma decisão minha pra continuar). Como isso aparece sem eu ter que ficar olhando a tela?
