# Trava no GitHub

> **Assunto:** a trava de época da Guilda num repositório privado do GitHub: refs `refs/mestre/epoca-N`, quem vence a época nova, aluguel de 60 s conferido a cada 30 s, espera do promovido, GitHub fora, o `409` do Telegram como sinal e a cota · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Dar à Guilda uma testemunha de fora que só uma máquina consegue ganhar, para que nunca haja duas Cidadelas agindo no GitHub, com qualquer número de máquinas (inclusive duas), sem que uma queda do GitHub pare a Guilda: a Cidadela confirma a época por um aluguel com prazo e, com o GitHub fora, segue no comando e no Telegram. A regra de fencing está em [época e fencing](cidadela-e-torres.md#época-e-fencing); este documento diz como a trava funciona.

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

O passo 4 não depende de a criação ser atômica no servidor: vence quem a ref nomeia. No motor, os passos 1 a 4 são `guild.Lock.Claim`, e a conferência do aluguel é `guild.Lock.Check` ([época e fencing](cidadela-e-torres.md#época-e-fencing)). **Promoção exige o GitHub:** sem criar e ler a ref, ninguém vence, e é isso que impede duas Cidadelas.

### Aluguel

- A Cidadela **confere** a trava a cada **30 s**: lista o prefixo com requisição condicional (`If-None-Match` com o `ETag` da última listagem). A época está **confirmada** se o maior N é a dela (um `304` repete a listagem anterior, que a confirmou).
- Cada confirmação abre um **aluguel de 60 s**, contado do envio da requisição, não da resposta. Ele vence pelo primeiro de dois relógios a passar de 60 s, o monotônico e o de parede, porque o monotônico do Linux não conta o tempo da máquina dormindo.
- **Toda chamada ao GitHub** (envio de branch, comentário, review, merge, criação de issue, consulta de eventos e de releases), menos a própria conferência, só começa com o aluguel válido. Nenhuma ação faz conferência própria: vale o aluguel.
- O Telegram não depende do aluguel, só de a Cidadela não estar [suspensa](cidadela-e-torres.md#cidadela-suspensa) e da regra do [`409`](#o-409-do-telegram).
- Conferência que **lê uma época maior:** a Cidadela se suspende na hora, sem mais nenhuma chamada ao GitHub nem ao Telegram, e segue a certidão nova ([volta da antiga](cidadela-e-torres.md#volta-da-antiga-cidadela)).
- A [concessão do motor](../autocura/guardiao.md#concessão-do-motor) ao Guardião não depende do aluguel nem do GitHub.

### Espera do promovido

- Quem vence a criação da ref **espera 60 s**, contados depois da leitura do passo 4, antes de qualquer ação externa (GitHub e Telegram). A última confirmação da antiga Cidadela foi enviada antes de a ref nova existir, então o aluguel dela vence antes do fim da espera; se ela ainda alcança o GitHub, a conferência seguinte (em até 30 s) lê a época nova e a suspende.
- Durante a espera, o promovido segue os passos internos da [promoção](cidadela-e-torres.md#promoção) (cofre, certidão, banco), e o bot continua com o [modo reduzido](../autocura/telegram-modo-reduzido.md) da condutora.
- Ao fim da espera, confere a trava (o primeiro aluguel dele), reconcilia o GitHub ([GitHub depois da troca](cidadela-e-torres.md#github-depois-da-troca)), o que inclui o que a antiga criou até o fim do aluguel dela, e só então pede a devolução do bot e age para fora. A [troca planejada](cidadela-e-torres.md#troca-planejada) também espera.

### GitHub fora

- **GitHub fora** é a conferência que **falha**: rede, tempo esgotado, `5xx`, limite (`403` ou `429`, respeitando `retry-after`) ou repositório da trava sem acesso. É diferente de ler uma época maior. Enquanto o aluguel vale, nada muda e a conferência segue a cada 30 s.
- **Com o aluguel vencido,** a Cidadela **não se suspende:**
  - segue no comando: Torres, painel e API, `/join`, Grão-Mestre, personagens e a concessão do Guardião (`mestre.local`);
  - segue no Telegram, lendo e enviando, com a regra do [`409`](#o-409-do-telegram);
  - **para só o GitHub:** as ações que mudam dados vão para a fila de saída da Cidadela, cada uma com o [marcador de operação](../github/acoes-por-nivel.md#marcador-de-operação); as consultas de eventos param e retomam pelos cursores ([eventos](../github/eventos.md)). Pela CLI, comando que muda dados sai com 0 e `"queued": true`; leitura (`issue list`, `pr status`) sai com `github_unavailable` ([CLI no GitHub](../cli/coordenacao-github.md#regras-e-erros));
  - **avisa o Dono** pelo Telegram e no painel, uma vez, com o diagnóstico (último erro, desde quando, ações na fila); o painel mostra "GitHub fora" enquanto durar.
- **Volta:** a primeira conferência que confirma renova o aluguel; a fila sai na ordem, e o marcador conferido antes de criar evita duplicar. O Dono recebe um segundo aviso, de volta, com o que saiu da fila.
- Sem GitHub **não há promoção:** o pretendente não cria nem lê a ref ([regras](#regras-e-erros)).

### O `409` do Telegram

- Um `getUpdates` longo novo aborta o anterior com `409 Conflict`; `sendMessage` não conflita, e um `getUpdates` que encontra updates prontos responde sem derrubar o outro. Por isso o `409` não prova nada sozinho.
- **Com o aluguel válido,** o `409` só dispara uma conferência imediata, fora do ciclo de 30 s: com a época dela, segue e registra o alerta; com uma época maior, suspende-se; se a conferência falha, segue (o aluguel ainda vale).
- **Com o aluguel vencido,** o `409` faz a Cidadela **parar o Telegram** (leitura e envio; o que ia sair fica na fila do bot) e mostrar no painel "Telegram parado: outro leitor no bot e GitHub fora", até uma conferência confirmar a época (volta a ler) ou mostrar uma maior (suspende-se).
- Isso fecha a partição em que só a antiga Cidadela perdeu o GitHub e outra máquina foi promovida: depois da espera, a nova lê o bot, a antiga recebe o `409` com o aluguel vencido e sai do Telegram.
- O modo reduzido da Torre condutora não usa a trava ([exceção](cidadela-e-torres.md#época-e-fencing)) e para de ler no `409` ([um só leitor](../autocura/telegram-modo-reduzido.md#um-só-leitor)).

### Cota

- **Conferência:** 1 a cada 30 s = 120 requisições por hora = 2.880 por dia por Cidadela, com o token de instalação do App "Grão-Mestre". Com `ETag` e `304`, elas não contam na cota primária (item a verificar abaixo); sem `ETag`, 120 por hora são 2,4% das 5.000 por hora de uma instalação.
- As ações não fazem conferência própria, então o custo da trava não cresce com o uso.
- **Promoção:** 1 commit, 1 criação e 1 leitura de ref, mais a listagem do passo 1 e a conferência ao fim da espera.

## Regras e erros

- `422` sem a ref apontar para o próprio commit é derrota, nunca erro a repetir. Outro erro (rede, `5xx`, `403`) antes da leitura da ref: o pretendente não vence, avisa o Dono e pode tentar de novo só com nova aprovação.
- Uma ref de época com número menor ou igual ao da época atual nunca é criada; o Mestre nunca apaga refs da trava.
- Repositório da trava apagado ou sem acesso: é [GitHub fora](#github-fora) com esse diagnóstico, sem promoção possível até ele voltar, e o checklist explica ([checklist](../plataforma/checklist.md)).
- Ação começada com o aluguel válido termina mesmo que ele vença no meio. Com a conferência a cada 30 s, o aluguel normal tem sempre mais de 30 s pela frente, e o marcador de operação evita duplicar na reconciliação do promovido.
- **Risco residual (partição dupla):** a Cidadela perde a rede local e o GitHub, mas alcança o Telegram, e a condutora liga o modo reduzido. Até o primeiro `409`, as duas leem e enviam no mesmo bot: um update pode ir a qualquer uma, e o Dono pode receber alertas das duas. A Cidadela, com o aluguel vencido, sai do Telegram no primeiro `409`; o modo reduzido nunca age no GitHub, e uma promoção que o Dono aprovar só vale pela trava. Aceito: no GitHub nunca há duas Cidadelas.

## Critérios de aceite

- **CA-TRAVA-01** · *rápida* — **Dado** duas máquinas que pretendem a época 3 no GitHub falso, **quando** as duas criam o commit e a ref, **então** só uma é nomeada pela ref e vira Cidadela, e a outra se suspende sem nenhuma ação externa.
- **CA-TRAVA-02** · *rápida* — **Dado** uma Cidadela confirmada há 20 s (relógio falso), **quando** saem dez ações no GitHub, **então** nenhuma lista as refs; **dado** 30 s, **então** sai uma conferência com `If-None-Match`, o GitHub falso responde `304` sem gastar cota e o aluguel vai a 60 s a partir dela; **dado** o relógio de parede adiantado 10 min de uma vez (máquina que dormiu), **então** o aluguel vence.
- **CA-TRAVA-03** · *rápida* — **Dado** a ref `refs/mestre/epoca-4` criada por outra máquina, **quando** a conferência da Cidadela da época 3 a lê, **então** ela se suspende na hora e nenhuma chamada ao GitHub ou ao Telegram sai dela depois disso.
- **CA-TRAVA-04** · *rápida* — **Dado** uma Cidadela com o aluguel válido que recebe `409 Conflict` no `getUpdates`, **quando** a conferência imediata mostra a época dela, **então** continua lendo; **quando** mostra uma época maior, **então** suspende-se.
- **CA-TRAVA-05** · *rápida* — **Dado** o GitHub falso respondendo `503` à conferência até o aluguel vencer, **quando** a Cidadela precisa responder no Telegram e comentar no GitHub, **então** responde no Telegram, o comentário vai para a fila de saída e o Dono recebe um aviso único no Telegram e no painel; **dado** o GitHub de volta, **então** a conferência confirma, o comentário sai uma vez e o Dono recebe o aviso de volta.
- **CA-TRAVA-06** · *rápida* — **Dado** uma Torre que vence a criação da época 4 (relógio falso), **quando** passam 59 s depois da leitura da ref, **então** o GitHub e o Telegram falsos não recebem nenhuma ação dela; **dado** 60 s, **então** ela confere a trava, reconcilia e só então lê o bot.
- **CA-TRAVA-07** · *rápida* — **Dado** uma Cidadela com o aluguel vencido pelo GitHub falso fora, **quando** o `getUpdates` recebe `409 Conflict`, **então** ela para de ler e de enviar no Telegram e o painel mostra "Telegram parado"; **dado** o GitHub de volta com a época dela, **então** volta a ler.
- **CA-TRAVA-08** · *completa* — **Dado** a Cidadela e uma Torre como instâncias, com Telegram e GitHub falsos, **quando** o GitHub falso fica fora por 10 min (relógio falso), **então** a Cidadela segue respondendo no Telegram, as ações pedidas no GitHub ficam na fila de saída e a Torre segue a Cidadela; **quando** o GitHub volta, **então** a fila sai uma vez, na ordem.
- **CA-TRAVA-09** · *completa* — **Dado** o GitHub falso inalcançável só para a Cidadela e uma Torre promovida, **quando** a nova Cidadela passa a ler o bot depois da espera, **então** a antiga recebe `409` com o aluguel vencido e para o Telegram, e o GitHub falso só recebe ações da nova.

## Depende de

- [Cidadela e Torres](cidadela-e-torres.md) · [Identidades e Apps](../github/identidades-e-apps.md)

## Verificar na implementação

- **Verificar na implementação:** duas criações simultâneas da mesma ref no repositório da trava: uma recebe `201` e a outra `422` (a documentação oficial não garante a atomicidade; a comunidade a usa como mutex). **Padrão:** a regra do passo 4, que não depende disso; o teste só informa. **Se falhar:** nada muda na regra; o resultado fica registrado nas gravações do GitHub falso. **Onde:** à mão no sandbox `lucasndas/mestre-sandbox` ([projetos de treino](../qualidade/projetos-de-treino.md#sandbox-real-no-github)).
- **Verificar na implementação:** `GET …/git/matching-refs/mestre/epoca-` devolve `ETag` e o `304` não gasta cota, e o prefixo fora de `heads/` e `tags/` funciona. **Padrão:** sim. **Se falhar:** a conferência segue a cada 30 s sem `ETag` (120 por hora, 2,4% da cota da instalação), e o painel mostra o custo em cota. **Onde:** no sandbox, como acima.

## Fontes

- Refs (criar, listar por prefixo, códigos 201/409/422): https://docs.github.com/en/rest/git/refs?apiVersion=2022-11-28
- Requisições condicionais e em série: https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api
- Ref como trava, na comunidade: https://github.com/dnd-it/action-lock
- `409` do `getUpdates` no servidor da Bot API (`Client.cpp`, `do_get_updates` e `abort_long_poll`): https://github.com/tdlib/telegram-bot-api
