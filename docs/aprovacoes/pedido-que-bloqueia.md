# Pedido que bloqueia

> **Assunto:** os campos da mensagem que pede algo ao usuário porque uma mesa não segue sem ele: por que parou, evidência, o que já foi conferido, por que o agente não decide, o que fazer, quem age e o motivo no vocabulário fixo · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Deixar o usuário decidir em segundos, no celular, sem abrir nada: a mensagem diz o que falta, mostra a prova, diz o que já foi conferido e o que fazer. O [fluxo](fluxo.md) dá o ciclo de vida do pedido; aqui está o **conteúdo** da mensagem. Vale para o pedido de aprovação e para o aviso de bloqueio (credencial que falta, cota, ambiguidade), enviados pelo [bot](../telegram/bot.md#mensagens-de-saída). O painel mostra os mesmos campos ([conversa e aprovações](../painel/conversa-e-aprovacoes.md)).

## Comportamento

### Os campos

| Campo | Para quê | Quem preenche |
|---|---|---|
| Motivo | uma palavra do vocabulário fixo (abaixo), com o ícone | motor, pelo tipo; o agente escolhe entre os valores quando o motor não sabe |
| Por que está parado | o que falta, concreto, numa linha | motor, quando vem do tipo ou do erro; senão o agente |
| Evidência | comando e saída, `arquivo:linha`, erro; nunca paráfrase | **motor**, de dados que ele tem (saída do teste, caminho, hash) |
| O que já conferi | para ninguém conferir de novo | agente, marcado como texto dele |
| Por que não decido | a decisão que o agente se recusa a tomar sozinho (cobrança, destruição, intenção) | agente, marcado como texto dele |
| Para destravar | checklist de passos concretos | motor, dos passos que ele conhece (cadastrar no cofre, aprovar o PR); o agente acrescenta os outros |
| Dono | quem age; nunca "alguém" | motor: o usuário de `exigir_usuarios` ou, sem ele, o nível mínimo do tipo ("um Administrador") |
| `blocked-by` | linha de máquina: números dos épicos ou mesas que precisam andar antes, ou `-` quando só uma pessoa destrava | motor |

Campo sem conteúdo é omitido da mensagem. O texto do agente aparece depois dos campos do motor, dentro do bloco "texto do agente, não verificado" ([cadeia](../orquestracao/cadeia-de-aprovacao.md)); o que o motor monta não pode ser reescrito por ele.

### O vocabulário de motivo

Fixo; um valor fora da lista é recusado (2 `usage` com os valores aceitos). O motor deduz o motivo pelo tipo quando pode: `permissao_de_classe` e `configuracao_repositorio` dão 🔑, `custo_extra` dá 💳, `acao_destrutiva` dá 🛡, credencial de site do cofre dá 🔐 e `pr`/`merge` dão 🧑.

| Ícone | Valor | Quando |
|---|---|---|
| 🔐 | `credencial` | falta uma chave ou senha |
| 💳 | `cota` | cota do plano ou cobrança |
| 🔑 | `permissao` | falta uma permissão |
| ❓ | `ambiguidade` | o pedido admite leituras que mudam o resultado |
| 🛡 | `destrutivo` | apaga ou desfaz algo |
| 🧑 | `decisao_humana` | a decisão é do usuário por regra ou por julgamento |
| 🤷 | `fora_de_escopo` | não é desta mesa ou deste projeto |
| 👯 | `duplicado` | já existe ou já foi feito |
| ⏳ | `espera` | depende de outro trabalho |
| 🙋 | `so_voce` | só uma pessoa pode fazer o passo |

### Como chega ao usuário

Uma mensagem curta, com o cabeçalho de origem ([roteamento](../telegram/roteamento.md#cabeçalho)). A evidência vai truncada em 3 linhas, com o resto no painel, e passa pelo mascarador antes de sair. Os botões são os do catálogo de respostas do tipo ([cadeia](../orquestracao/cadeia-de-aprovacao.md)) mais um por passo do checklist que o bot sabe fazer. Exemplo:

```
🔐 Mesa Checkout · falta a chave STRIPE_TEST_KEY
Evidência: checkout_test.go:22 → "missing STRIPE_TEST_KEY"
Por que não decido (agente): envolve cobrança, é decisão sua
Para destravar: [ ] cadastrar a chave no cofre
Dono: Lucas
[Cadastrar agora] [Pular esta etapa]
```

## Regras e erros

- A evidência nunca contém um segredo: o valor sai mascarado ([segurança](../arquitetura/seguranca.md)).
- Pedido sem evidência que o motor possa montar (só texto do agente) vai marcado "sem evidência verificada".
- O vocabulário e os campos não mudam quem decide nem o risco: isso é da [política](politica.md) e da [classificação de risco](../classes/classificacao-de-risco.md).

## Critérios de aceite

- **CA-BLOQ-01** · *rápida* — **Dado** um teste da mesa Checkout que falhou com `missing STRIPE_TEST_KEY` em `checkout_test.go:22`, **quando** o agente pede a chave, **então** a mensagem mostra o motivo 🔐, a evidência com esse arquivo, linha e erro montada pelo motor, o checklist com o cofre e o dono; e o "por que não decido" aparece depois, dentro do bloco "texto do agente, não verificado".
- **CA-BLOQ-02** · *rápida* — **Dado** um pedido com o motivo `urgente`, **quando** o motor o recebe, **então** é recusado com 2 `usage` listando os dez valores; **dado** um pedido `custo_extra` sem motivo, **então** sai 💳.
- **CA-BLOQ-03** · *rápida* — **Dado** um pedido de `merge` sem `exigir_usuarios` e um com `exigir_usuarios: [lucas]`, **quando** a mensagem é montada, **então** o dono do primeiro é o nível mínimo do tipo, o do segundo é `lucas`, e nenhum diz "alguém".
- **CA-BLOQ-04** · *rápida* — **Dado** uma evidência de 10 linhas com um token no formato `ghp_…`, **quando** a mensagem é montada, **então** a evidência vai truncada em 3 linhas, o token sai mascarado, e o painel recebe o texto completo, também mascarado.

## Depende de

- [Fluxo de aprovação](fluxo.md)
- [Bot do Telegram](../telegram/bot.md)
