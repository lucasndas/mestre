# Parear o Telegram

> **Assunto:** passo a passo para criar o bot do Telegram, ligá-lo ao Mestre e parear cada usuário por código · **Componente:** Core · **Atualizado:** 2026-10-12

Guia para quem usa. Há duas partes: **ligar o bot** (uma vez, feito pelo Dono) e **parear cada usuário** (inclusive o próprio Dono). As regras de níveis, códigos e PIN estão em [usuários e níveis](../telegram/usuarios-e-niveis.md); o funcionamento do bot, em [bot](../telegram/bot.md).

Antes: tenha feito o [primeiro acesso](primeiro-acesso.md) (Dono criado, com PIN) e tenha o Telegram instalado no celular.

## Parte A: criar e ligar o bot (uma vez)

1. No Telegram, abra a conversa com **@BotFather** (confira o selo de verificado ao lado do nome).
2. Envie `/newbot`.
3. Escolha o **nome de exibição** (ex.: `Mestre do Lucas`).
4. Escolha o **nome de usuário** do bot. Ele precisa ser único e terminar em `bot` (ex.: `lucas_mestre_bot`).
5. O BotFather responde com o **token** do bot. Copie. Não envie esse token a ninguém e não cole em arquivo de projeto.
6. Recomendado: envie `/setjoingroups`, escolha o seu bot e depois `Disable`. O Mestre só atende em chat privado e ignora grupos de qualquer forma ([segurança do canal](../telegram/seguranca-do-canal.md)); isso só evita que alguém adicione o bot a um grupo.
7. No painel, abra o passo **Telegram** dos próximos passos do primeiro acesso (depois, o mesmo segredo fica em **Segredos**, na administração), cole o token e confirme com o seu PIN. O token vai direto para o [cofre](../plataforma/cofre.md).
8. Em alguns segundos o painel mostra o bot como conectado, com o nome de usuário dele. O bot usa só conexão de saída: não é preciso abrir porta nem configurar roteador.

## Parte B: parear um usuário

### O próprio Dono

1. No painel, abra **Usuários**, escolha o seu usuário e clique em **Parear Telegram**.
2. O painel mostra um **código** e um **link**. O código é de uso único e vence em pouco tempo.
3. No celular, toque no link: ele abre a conversa com o bot. Toque em **Iniciar**. Se preferir, abra a conversa privada com o bot e envie `/start <código>` ou só o código.
4. O bot responde com o seu nível, os workspaces e uma mensagem de boas-vindas.
5. Teste: envie `/status`. O bot responde com o estado do Core, dos nós e dos agentes.

### Outro usuário

1. O Dono cadastra a pessoa no painel: **Usuários → Novo**, com nome, nível e workspaces ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)).
2. O Dono clica em **Parear Telegram** no cadastro dela.
3. O Dono passa o link ou o código à pessoa por um canal de confiança (pessoalmente ou em mensagem direta para ela). Nunca em grupo.
4. A pessoa toca no link, ou envia o código ao bot **em chat privado**, antes de vencer.
5. O bot confirma para ela, e o Dono recebe um aviso de que o pareamento foi feito.
6. Para usar o painel, a pessoa faz o primeiro acesso com o código que o Dono gera em **Usuários** e define senha e PIN ([login](../painel/login.md#primeiro-acesso-de-cada-usuário)). Sem PIN, ela não conclui ações que pedem PIN.

## Se algo der errado

| O que acontece | O que fazer |
|---|---|
| O bot responde "código inválido ou expirado" | O código venceu ou já foi usado. O Dono gera outro no painel. |
| O bot não responde mais nada às tentativas | Muitas tentativas erradas: o bot fica em silêncio por um tempo ([regras](../telegram/usuarios-e-niveis.md#pareamento-por-código)). Espere e use um código novo. |
| O código foi enviado num grupo | Mensagens em grupo são ignoradas. Envie de novo na conversa privada com o bot. |
| A pessoa trocou de conta do Telegram | O Dono limpa o ID do Telegram no cadastro dela e gera um código novo. |
| O painel não mostra o bot como conectado | Confira se o token foi colado inteiro e se o Core tem acesso à internet. Os alertas aparecem no painel de saúde ([checklist](../plataforma/checklist.md)). |
| Um Observador escreve e nada acontece | É o esperado: Observador consulta e recebe alertas, mas não conversa com o Grão-Mestre. |

## Se o token vazar

1. No @BotFather, envie `/revoke` e escolha o seu bot. O token antigo para de funcionar na hora.
2. Copie o token novo e troque o segredo do token do bot em **Segredos**, na administração do painel, com o seu PIN.
3. Os pareamentos continuam valendo: eles são ligados ao ID de cada usuário, não ao token.

## Lembretes

- O Telegram não tem criptografia ponta a ponta nas conversas com bots. Nunca envie senhas, tokens ou chaves por lá; o Mestre também não envia.
- Remover ou suspender alguém é feito no painel, em **Usuários** ([regras](../telegram/usuarios-e-niveis.md#suspensão-e-remoção)).
- Como as mensagens escolhem o workspace (`/workspace`, `#nome`): [roteamento](../telegram/roteamento.md).
