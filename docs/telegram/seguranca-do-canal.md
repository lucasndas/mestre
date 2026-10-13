# Segurança do canal

> **Assunto:** quem o bot aceita, alerta de intrusos, travamento de emergência e o que nunca passa pelo Telegram · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Garantir que só usuários autorizados, em chat privado, consigam dar ordens pelo Telegram, e que uma emergência possa congelar tudo com um comando.

## Comportamento

### Filtro de entrada

O motor aplica este filtro a cada update, **antes** de qualquer outra lógica, inclusive antes de transcrever áudio ou baixar anexos:

1. **Tipo de chat:** só `chat.type = "private"` passa.
   - Se o bot for adicionado a um grupo, supergrupo ou canal, ele ignora as mensagens, sai com `leaveChat` e o Dono recebe um alerta.
   - A instalação também orienta a desligar "Allow Groups" no BotFather ([parear Telegram](../operacao/parear-telegram.md)).
2. **Identidade:** o remetente é identificado só pelo `from.id` numérico, nunca pelo nome de usuário. O ID precisa estar na lista de usuários ativos ([usuários e níveis](usuarios-e-niveis.md)).
   - A única exceção é a mensagem de pareamento por código.
3. **Não autorizado:** a mensagem é descartada sem resposta. Nada é baixado nem transcrito, e nada entra em fila.
   - O Dono recebe um alerta com ID, nome exibido, @usuário e o início do texto (até 200 caracteres).
   - Os alertas são agrupados: 1 alerta por ID por hora (padrão), com a contagem das tentativas.
4. **Suspenso:** a mensagem é descartada sem resposta e sem alerta.
5. **Nível e workspace:** passou do filtro, a mensagem segue para o [roteamento](roteamento.md). O nível é checado em cada ação ([níveis](usuarios-e-niveis.md#níveis-e-capacidades)).

- **Mensagem encaminhada:** o autor é sempre quem enviou (`from.id`), não o autor original. O conteúdo encaminhado é tratado como conteúdo trazido pelo usuário.

### Confirmação reforçada

Decretos que afrouxam a segurança pedem o PIN do autor. A lista de casos está em [decretos](decretos.md#confirmação-reforçada-com-pin), e a mecânica do PIN em [usuários e níveis](usuarios-e-niveis.md#pin-pessoal).

### `/travar` e `/destravar`

- **`/travar`** (Dono e Administradores, níveis em [usuários e níveis](usuarios-e-niveis.md)): o motor pede confirmação com um botão e aplica em todos os workspaces, de uma vez:
  - todos os decretos ficam **suspensos**;
  - o `mestre hook` passa a responder **negar** a todo uso de ferramenta, em todos os personagens e provedores;
  - a caixa de entrada para de entregar mensagens a agentes. Elas ficam guardadas;
  - missões, rituais e recrutamentos ficam pausados;
  - nenhuma ação no GitHub é executada pelo motor;
  - os terminais **não** são encerrados. O estado e o contexto dos agentes se mantêm. Única exceção: uma sessão ativa do [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) é encerrada na hora.
- Todos os usuários recebem uma mensagem "Sistema" dizendo quem travou e quando.
- Enquanto está travado:
  - o bot responde só aos comandos de consulta e a `/destravar`;
  - mensagens de conversa ficam guardadas e são entregues ao Grão-Mestre depois de destravar.
- **`/destravar`** (só o Dono): pede o PIN pelo Telegram ou é feito pelo painel. Depois de destravar:
  - os decretos voltam ao estado que tinham antes do travamento (os que estavam suspensos por conflito continuam suspensos);
  - a entrega e as missões são retomadas.
- O estado "travado" fica no banco e sobrevive a reinícios da Cidadela. O Guardião recebe esse estado pela replicação do motor, e o modo reduzido o respeita ([Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md)).

### Segredos

- O token do bot fica no [cofre](../plataforma/cofre.md). Nunca vai para arquivo do projeto, banco em texto puro, log ou ambiente dos agentes.
- **O Mestre nunca envia segredos pelo Telegram** (tokens, senhas, chaves), porque conversas com bots não têm criptografia ponta a ponta.
  - Toda mensagem de saída passa pelo mesmo mascarador dos logs ([segurança](../arquitetura/seguranca.md)).
  - Se algo for mascarado, o motor avisa o remetente (agente ou motor) de que o trecho foi retirado.
- **Segredos mandados por usuários:** credenciais só se cadastram pelo painel. Senha ou token que chega pelo Telegram é tirado antes de chegar ao Grão-Mestre quando o motor reconhece, a mensagem é apagada (`deleteMessage`) e não entra em histórico, caixa de entrada nem log; o Grão-Mestre recusa e recomenda trocar a senha ([credenciais de site](../plataforma/cofre.md#credenciais-de-site)).
- **Segundo fator de login em sites** não é exceção a essa regra: o motor só **recebe** o código (ou a resposta de um captcha) do dono da credencial, digita no navegador de login e apaga as duas mensagens; retenção zero, sem passar por agente ([segundo fator](../plataforma/cofre.md#segundo-fator-e-captcha)).
- **Anexos** são conteúdo trazido pelo usuário. Os agentes os leem, mas nunca os executam: é regra fixa do Mestre ([permissões](../classes/permissoes.md), [anexos](anexos-e-transcricao.md)).

## Regras e erros

- O filtro é do motor. Nenhum agente vê mensagens de não autorizados.
- **Token vazado:** o Dono gera um token novo no BotFather e troca no cofre. O motor reinicia o long polling com o token novo sem perder a fila de saída.
- `/travar` vindo de nível sem permissão é recusado e registrado na auditoria.
- `/destravar` com PIN errado segue as regras de bloqueio do PIN.

## Critérios de aceite

- **CA-CANAL-01** · *rápida* — **Dado** um ID fora da lista, **quando** ele manda texto, áudio ou documento, **então** nada é baixado, transcrito ou enfileirado, o bot não responde e o Dono recebe um alerta (MVP-9).
- **CA-CANAL-02** · *rápida* — **Dado** 30 mensagens do mesmo ID não autorizado em 10 minutos, **quando** o filtro processa, **então** o Dono recebe um único alerta com a contagem.
- **CA-CANAL-03** · *rápida* — **Dado** um usuário autorizado escrevendo num grupo, **quando** o update chega, **então** a mensagem é ignorada, o bot sai do grupo e o Dono é alertado.
- **CA-CANAL-04** · *rápida* — **Dado** `/travar` confirmado por um Administrador, **quando** um personagem tenta usar uma ferramenta, **então** o `mestre hook` nega e todos os decretos aparecem suspensos.
- **CA-CANAL-05** · *rápida* — **Dado** o sistema travado, **quando** o Dono manda `/destravar` com o PIN certo, **então** os decretos voltam ao estado anterior e as mensagens guardadas são entregues.
- **CA-CANAL-06** · *rápida* — **Dado** uma resposta do Grão-Mestre contendo algo com formato de token, **quando** ela sai pelo Telegram, **então** o trecho está mascarado.
- **CA-CANAL-08** · *rápida* — **Dado** uma sessão do Operador do PC ativa e outro personagem trabalhando, **quando** o Dono confirma `/travar`, **então** a sessão do Operador é encerrada na hora (terminal fechado sem a espera de 60 s) e o outro terminal continua aberto, com o hook negando tudo.
- **CA-CANAL-07** · *pesada* — **Dado** o sistema instalado e o Telegram (dublê) com um remetente desconhecido, **quando** ele escreve ao bot, **então** é ignorado e o alerta aparece no Telegram do Dono e no painel (MVP-9).

## Depende de

- [Bot](bot.md)
- [Usuários e níveis](usuarios-e-niveis.md)
- [Cofre](../plataforma/cofre.md)
- [Segurança da arquitetura](../arquitetura/seguranca.md)
- [Adaptador de provedores](../provedores/adaptador.md) (`mestre hook`)
