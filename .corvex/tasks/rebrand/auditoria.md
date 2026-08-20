# Auditoria adversarial do rebrand (F1–F9)

> 🗃️ **REGISTRO HISTÓRICO.** Os números e status deste arquivo valiam quando ele foi
> escrito e **não são estado corrente** — não decida por eles. O estado atual, com a
> instrução de como remedir cada número, está em `fechamento.md`.

> Rodada depois de fechar F3–F7, F9 e o backlog do grill. **Cinco lentes independentes**
> (vazamento · concorrência · contrato · afirmação-vs-código · qualidade de teste), cada achado
> passado a um **cético cujo trabalho era refutá-lo** — default "não é real", só aceita se
> reproduzir sozinho. **24 achados brutos, 22 confirmados, 2 refutados.**

## O padrão que a auditoria expôs

Quase todos os achados têm a mesma forma: **uma afirmação escrita que o código não cumpria**.
Não é coincidência — esta casa escreve muito "por quê" em comentário, mensagem de commit e
registro de fase, e é justamente isso que torna a afirmação falsa cara: o próximo leitor confia
nela e **para de olhar**. Foi o que aconteceu com o vazamento do ledger, com a custódia da F9 e
com o `Release()` do spawn: nos três, um comentário garantia a propriedade que o código não
tinha, e o teste ao lado media outra coisa.

## Os quatro de severidade alta

| Achado | O que era |
|---|---|
| **Ledger publicava segredo** | A F5 fechou o vetor na linha `tool_use` e escreveu que estava fechado. Dois outros produtores punham input de ferramenta e stderr do provider em `Entry.Message`. Auditor levou canário até dentro de um commit do próprio `auto_commit`. |
| **Custódia da F9 inerte** | `collectAuthEnv` filtra o conjunto encaminhado; o filho herda `os.Environ()`. A credencial negada chegava ao worker na configuração default, com três comentários afirmando o contrário. |
| **Gate sumindo da caixa** | `parked` é escalar, gates não são. Com paralelismo, o primeiro gate decidido despark­ava o run e o segundo desaparecia de `gate list` e da UI, com o run vivo travado nele. |
| **Zumbi do spawn** | `Release()` não reapa nem reparenta. O run disparado pela UI virava `<defunct>` filho do servidor — e zumbi responde a sinal 0, que é como a liveness leria "vivo". |

## Os dois refutados, e por quê importa dizer

- *"Atribuição de fase em eventos de watchdog não é observada"* — a mutação reproduzia, mas
  provava outra coisa; o cético mostrou que o teste apontado não fala de fase.
- *"O clamp de duração negativa não tem teste"* — é teste para código inalcançável: o cético
  reconstruiu a alcançabilidade e ela não existe.

Registrar refutação é o que impede a lista de virar ruído: um achado plausível e não
demonstrado faz o próximo leitor desconfiar dos verdadeiros.

## O que mudou por causa dela

`4017dcf` (os dois vazamentos) e `0223c28` (os outros nove), mais as correções nos registros
de fase — inclusive **apagar da `f5-registro.md` a frase que estava errada**, porque um
registro que mente é pior que registro nenhum.

Três testes que **não sabiam ficar vermelhos** ganharam mordida: o guarda do `run kill` (a
asserção casava com a string do stdlib, não com a do guarda), o preflight da F9 (nada observava
que ele está no caminho do run) e a ordem teardown-antes-do-status da F6.

## O que isto ensina para a próxima fase

1. **Toda afirmação de segurança precisa de um teste que crie um processo.** Filtro de mapa
   prova filtro de mapa. Três dos quatro achados altos passaram por testes verdes que mediam a
   função ao lado da que importava.
2. **O tripwire tem de cobrir o campo, não o caminho.** `Entry.Message` é o único campo de texto
   livre da linha; era por onde qualquer produtor novo podia reabrir o buraco.
3. **Comentário que afirma propriedade é dívida** até existir teste que o derrube. O padrão
   "escreva o porquê" continua certo — mas o porquê tem de ser verificável, e verificado.
