# Pedido que bloqueia

> **Assunto:** os campos da mensagem que pede algo ao usuário porque uma mesa não segue sem ele: por que parou, evidência, o que já foi conferido, por que o agente não decide, o que fazer, quem age e o motivo no vocabulário fixo · **Componente:** Motor · **Atualizado:** 2026-10-26

## Objetivo

Deixar o usuário decidir em segundos, no celular, sem abrir nada: a mensagem diz o que falta, mostra a prova, diz o que já foi conferido e o que fazer. O [fluxo](fluxo.md) dá o ciclo de vida do pedido; aqui está o **conteúdo** da mensagem. Vale para o pedido de aprovação e para o **aviso de bloqueio** (credencial que falta, cota, ambiguidade), enviados pelo [bot](../telegram/bot.md#mensagens-de-saída). O aviso não é um tipo da [política](politica.md): não tem catálogo de respostas, nível mínimo nem decisão; só informa e leva os passos que o usuário pode fazer. O painel mostra os mesmos campos ([conversa e aprovações](../painel/conversa-e-aprovacoes.md)).

## Comportamento

### Os campos

| Campo | Para quê | Quem preenche |
|---|---|---|
| Motivo | uma palavra do vocabulário fixo (abaixo), com o ícone | motor, pelo tipo; o agente escolhe entre os valores quando o motor não sabe |
| Por que está parado | o que falta, concreto, numa linha | motor, quando vem do tipo ou do erro; senão o agente |
| Evidência | comando e saída, `arquivo:linha`, erro; nunca paráfrase | **motor**, de dados que ele tem (saída do teste, caminho, hash): a última verificação vermelha da mesa (`quests.Service.LastRed`, em memória até a próxima verde), que `blocking.TestEvidence` reduz a `arquivo:linha → "erro"` e ao comando |
| O que já conferi | para ninguém conferir de novo | agente, marcado como texto dele |
| Por que não decido | a decisão que o agente se recusa a tomar sozinho (cobrança, destruição, intenção) | agente, marcado como texto dele |
| Para destravar | checklist de passos concretos | motor, dos passos que ele conhece (cadastrar no cofre, aprovar o PR); o agente acrescenta os outros |
| Dono | quem age; nunca "alguém" | motor: o usuário de `exigir_usuarios` ou, sem ele, o nível mínimo do tipo ("um Administrador"); no aviso, que não tem tipo, o Dono do workspace para `credencial` (o cofre só aceita o Dono) e um Administrador nos demais |
| `blocked-by` | linha de máquina: números dos épicos ou mesas que precisam andar antes, ou `-` quando só uma pessoa destrava | motor, das dependências declaradas entre as mesas ([dependência entre mesas](../orquestracao/dependencia-entre-mesas.md)); `-` se não há |

Campo sem conteúdo é omitido da mensagem. O texto do agente (o que já conferi, por que não decido e os passos dele) aparece depois dos campos do motor, dentro do bloco "texto do agente, não verificado" ([cadeia](../orquestracao/cadeia-de-aprovacao.md)); o que o motor monta não pode ser reescrito por ele.

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

### Como o agente envia

Só o Grão-Mestre envia, com os campos do agente em flags ([CLI](../cli/grao-mestre.md#mestre-approval)): `--motive <valor>`, `--checked "<o que já conferi>"`, `--why-not "<por que não decido>"` e `--step "<passo>"` (repetível). No pedido de aprovação, vão em `mestre approval ask`; no aviso de bloqueio, em `mestre notify --blocked <valor>` com as mesmas flags. Valor de motivo fora do vocabulário → 2 `usage` com os valores aceitos.

### Como chega ao usuário

Uma mensagem curta (no pedido de aprovação, o motivo com o ícone abre o texto e a linha `Dono:` vem antes do bloco dos agentes, montados em `internal/approvals`), com o cabeçalho de origem ([roteamento](../telegram/roteamento.md#cabeçalho)), os campos do motor e depois o bloco do agente. A evidência vai truncada em 3 linhas, com o resto no painel, e passa pelo mascarador antes de sair. Os botões são os do catálogo de respostas do tipo ([cadeia](../orquestracao/cadeia-de-aprovacao.md)) que **aquele** usuário pode dar, mais um por passo que o bot sabe fazer **e** que o usuário pode fazer, com o nível e o PIN do passo; o aviso, sem catálogo, só tem esses botões e **Ver detalhes**. Cadastrar um segredo exige o Dono, com PIN, e só se faz no painel ([cofre](../plataforma/cofre.md#uso)): o passo vira o botão **Abrir no painel** (um link) para o Dono; para os outros, nenhum botão de cadastro. Exemplo de aviso `credencial`:

```
Loja · Mesa #42 Checkout
🔐 falta a chave STRIPE_TEST_KEY
Evidência: checkout_test.go:22 → "missing STRIPE_TEST_KEY"
Para destravar: [ ] cadastrar a chave no cofre (painel)
Dono: Lucas
— texto do agente, não verificado —
Por que não decido: envolve cobrança, é decisão sua
[Abrir no painel] [Ver detalhes]
```

## Regras e erros

- A evidência nunca contém um segredo: o valor sai mascarado ([segurança](../arquitetura/seguranca.md)).
- Pedido sem evidência que o motor possa montar (só texto do agente) vai marcado "sem evidência verificada".
- O vocabulário e os campos não mudam quem decide nem o risco: isso é da [política](politica.md) e da [classificação de risco](../classes/classificacao-de-risco.md).

- **Implementação:** `internal/blocking` faz a mensagem (`Build` para cada nível, `Full` para o painel e o **Ver detalhes**), o vocabulário (`ParseMotive`, que devolve o erro de uso com os dez valores) e o dono (`OwnerOf`, `NoticeOwner`); `telegram.BlockChannel` guarda o aviso mascarado (`blocking_notices`) e o manda ao Dono e aos Administradores do workspace, cada um com os botões do seu nível (o **Abrir no painel** é um link, só do Dono). No painel (CA-BLOQ-04), `blocking.Store` guarda as partes já mascaradas (`PartsOf`: a evidência inteira, os passos, o dono e o bloco do agente) e a central as mostra com `can_do` por usuário (`CanDo`: o passo do cofre é só do Dono, e o PIN é pedido na tela de Segredos). O texto do agente sai numa linha por campo, sem negrito, código nem link. `mestre notify "<mensagem>" --blocked <motivo>` (`blocking.Handler`, só o Grão-Mestre) manda o aviso pelo `BlockChannel` com as flags `--checked`, `--why-not` e `--step`; a mensagem do agente vai no bloco dele ("Parou porque"), o dono do `credencial` é o Dono (com o passo do cofre) e o dos outros é um Administrador, e `--table` põe a mesa e o `blocked-by` das dependências entre mesas ([dependência entre mesas](../orquestracao/dependencia-entre-mesas.md)). Sem `--blocked` o `notify` ainda não tem remetente e responde `unavailable`. `mestre approval ask --action <id>` leva ao usuário o pedido que espera no Grão-Mestre (a resposta "Subir agora"), com as mesmas flags; as palavras dele ficam no bloco "texto do agente, não verificado" do pedido. O `ask` de uma ação que ainda não é um pedido está na [ligação de produção](../desenvolvimento/ligacao-de-producao.md).

## Critérios de aceite

- **CA-BLOQ-01** · *rápida* — **Dado** um teste da mesa Checkout que falhou com `missing STRIPE_TEST_KEY` em `checkout_test.go:22`, **quando** o agente pede a chave, **então** a mensagem mostra o motivo 🔐, a evidência com esse arquivo, linha e erro montada pelo motor, o checklist com o cofre e o dono; e o "por que não decido" aparece depois, dentro do bloco "texto do agente, não verificado".
- **CA-BLOQ-02** · *rápida* — **Dado** `mestre approval ask --motive urgente`, **quando** o motor o recebe, **então** é recusado com 2 `usage` listando os dez valores; **dado** `mestre notify --blocked credencial --why-not "…" --step "cadastrar a chave"`, **então** a mensagem sai com esses campos do agente no bloco dele; **dado** um pedido `custo_extra` sem motivo, **então** sai 💳.
- **CA-BLOQ-03** · *rápida* — **Dado** um pedido de `merge` sem `exigir_usuarios` e um com `exigir_usuarios: [lucas]`, **quando** a mensagem é montada, **então** o dono do primeiro é o nível mínimo do tipo, o do segundo é `lucas`, e nenhum diz "alguém".
- **CA-BLOQ-04** · *rápida* — **Dado** uma evidência de 10 linhas com um token no formato `ghp_…`, **quando** a mensagem é montada, **então** a evidência vai truncada em 3 linhas, o token sai mascarado, e o painel recebe o texto completo, também mascarado.
- **CA-BLOQ-05** · *rápida* — **Dado** o aviso `credencial` do exemplo, **quando** a mensagem é montada para o Dono e para um Administrador, **então** o Dono vê **Abrir no painel** e o Administrador não vê botão de cadastro, o dono do campo é o Dono, e nenhum botão cadastra o segredo pelo Telegram.
- **CA-BLOQ-06** · *rápida* — **Dado** a mesa 43 que depende da mesa 42, **quando** o aviso `espera` é montado, **então** o `blocked-by` é `42`; **dado** um aviso só de uma pessoa, **então** é `-`.

## Depende de

- [Fluxo de aprovação](fluxo.md)
- [Bot do Telegram](../telegram/bot.md)
- [Política de aprovação](politica.md)
