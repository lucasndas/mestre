# Trava no GitHub

> **Assunto:** a trava de época da Guilda num repositório privado do GitHub: refs `refs/mestre/epoca-N`, quem vence a criação da época nova, confirmação antes de toda ação externa e o `409` do Telegram como sinal · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Dar à Guilda uma testemunha de fora que só uma máquina consegue ganhar, para que nunca haja duas Cidadelas agindo para fora, com qualquer número de máquinas (inclusive duas). A regra de fencing está em [época e fencing](cidadela-e-torres.md#época-e-fencing); este documento diz como a trava funciona.

## Comportamento

### Repositório e refs

- **Repositório:** privado, `<login>/mestre-guilda-<guild_id curto>`, criado pelo Mestre na configuração do GitHub, com o token de instalação do App "Grão-Mestre" ([repositório da trava](../github/identidades-e-apps.md#repositório-da-trava)). Só guarda o commit inicial, os commits dos pretendentes e as refs.
- **Trava da época N:** a ref `refs/mestre/epoca-N`, apontando para um commit. A época atual é o **maior N** entre as refs listadas por prefixo (`GET /repos/{o}/{r}/git/matching-refs/mestre/epoca-`); a ordem da resposta não é garantida, então o motor calcula o máximo.
- **Primeira ref:** ao criar o repositório, a Cidadela cria `refs/mestre/epoca-<época atual>` apontando para o commit dela (abaixo). Até lá a Guilda não tem promoção.
- As requisições da trava vão em série, nunca em paralelo (limite secundário do GitHub).

### Promoção pela trava

1. O pretendente lista as refs e calcula N.
2. Grava um **commit próprio** no repositório (`POST …/git/commits` sobre a árvore do commit inicial), com a mensagem `guild_id`, `machine_id`, época pretendida N+1 e horário.
3. Tenta criar `refs/mestre/epoca-(N+1)` apontando para esse commit (`POST …/git/refs`). Criar uma ref que já existe é recusado com `422`.
4. Qualquer que seja a resposta (`201` ou `422`, que também cobre "spammed"), **lê a ref** (`GET …/git/ref/mestre/epoca-(N+1)`). Só vence se ela aponta para o commit dele; senão perdeu, suspende-se e avisa o Dono.

O passo 4 não depende de a criação ser atômica no servidor: vence quem a ref nomeia.

### Confirmação antes de ação externa

- Antes de chamar o GitHub ou o Telegram (inclusive cada `getUpdates`), a Cidadela lista o prefixo com requisição condicional (`If-None-Match` com o `ETag` da última listagem). A época está **confirmada** se o maior N listado é a época dela.
- A confirmação vale **no máximo 2 s**; as ações dentro desse intervalo compartilham a mesma confirmação.
- Época maior na lista: a Cidadela se suspende e segue a certidão nova ([volta da antiga](cidadela-e-torres.md#volta-da-antiga-cidadela)). GitHub inalcançável: não confirma e fica [suspensa](cidadela-e-torres.md#cidadela-suspensa), sem agir no GitHub nem no Telegram.
- O motor só renova a [concessão do Guardião](../autocura/guardiao.md#concessão-do-motor) com uma confirmação válida.

### O `409` do Telegram

- Um `getUpdates` longo novo aborta o anterior com `409 Conflict`; `sendMessage` não conflita, e um `getUpdates` que encontra updates prontos responde sem derrubar o outro. Por isso o `409` não prova nada sozinho.
- Quem recebe um `409` confere a trava na hora: com a época mais recente, segue; sem ela, suspende-se.
- O modo reduzido da Torre condutora não usa a trava ([exceção](cidadela-e-torres.md#época-e-fencing)).

## Regras e erros

- `422` sem a ref apontar para o próprio commit é derrota, nunca erro a repetir. Outro erro (rede, `5xx`, `403`) antes da leitura da ref: o pretendente não vence, avisa o Dono e pode tentar de novo só com nova aprovação.
- Uma ref de época com número menor ou igual ao da época atual nunca é criada; o Mestre nunca apaga refs da trava.
- Repositório da trava apagado ou sem acesso: nenhuma confirmação sai, a Cidadela fica suspensa e o checklist explica ([checklist](../plataforma/checklist.md)).

## Critérios de aceite

- **CA-TRAVA-01** · *rápida* — **Dado** duas máquinas que pretendem a época 3 no GitHub falso, **quando** as duas criam o commit e a ref, **então** só uma é nomeada pela ref e vira Cidadela, e a outra se suspende sem nenhuma ação externa.
- **CA-TRAVA-02** · *rápida* — **Dado** uma Cidadela confirmada há 1,5 s (relógio falso), **quando** sai uma segunda ação externa, **então** ela não lista as refs de novo; **dado** 2,1 s, **então** lista, com `If-None-Match`, e o GitHub falso responde `304` sem gastar cota.
- **CA-TRAVA-03** · *rápida* — **Dado** a ref `refs/mestre/epoca-4` criada por outra máquina, **quando** a Cidadela da época 3 vai comentar no GitHub ou chamar `getUpdates`, **então** nenhuma chamada sai e ela se suspende.
- **CA-TRAVA-04** · *rápida* — **Dado** uma Cidadela que recebe `409 Conflict` no `getUpdates`, **quando** a trava ainda mostra a época dela, **então** continua; **quando** mostra uma época maior, **então** suspende-se.
- **CA-TRAVA-05** · *rápida* — **Dado** o GitHub falso inalcançável, **quando** a Cidadela precisa responder no Telegram, **então** não responde, fica suspensa e retira a concessão do Guardião.

## Depende de

- [Cidadela e Torres](cidadela-e-torres.md) · [Identidades e Apps](../github/identidades-e-apps.md)

## Verificar na implementação

- **Verificar na implementação:** duas criações simultâneas da mesma ref no repositório da trava: uma recebe `201` e a outra `422` (a documentação oficial não garante a atomicidade; a comunidade a usa como mutex). **Padrão:** a regra do passo 4, que não depende disso; o teste só informa. **Se falhar:** nada muda na regra; o resultado fica registrado nas gravações do GitHub falso. **Onde:** à mão no sandbox `lucasndas/mestre-sandbox` ([projetos de treino](../qualidade/projetos-de-treino.md#sandbox-real-no-github)).
- **Verificar na implementação:** `GET …/git/matching-refs/mestre/epoca-` devolve `ETag` e o `304` não gasta cota, e o prefixo fora de `heads/` e `tags/` funciona. **Padrão:** sim. **Se falhar:** a janela de confirmação sobe de 2 s para 10 s e o painel mostra o custo em cota. **Onde:** no sandbox, como acima.

## Fontes

- Refs (criar, listar por prefixo, códigos 201/409/422): https://docs.github.com/en/rest/git/refs?apiVersion=2022-11-28
- Requisições condicionais e em série: https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api
- Ref como trava, na comunidade: https://github.com/dnd-it/action-lock
- `409` do `getUpdates` no servidor da Bot API (`Client.cpp`, `do_get_updates` e `abort_long_poll`): https://github.com/tdlib/telegram-bot-api
