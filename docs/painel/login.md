# Login do painel

> **Assunto:** login, assistente de criação do Dono, primeiro acesso de cada usuário, sessões com expiração e PIN em ações sensíveis · **Componente:** Painel · **Atualizado:** 2026-10-19

## Objetivo

Garantir que só usuários autorizados usem o painel, cada um com o seu nível e os seus workspaces. Os usuários e níveis são os mesmos do Telegram; a tabela de níveis é única e mora em [usuários e níveis](../telegram/usuarios-e-niveis.md).

## Comportamento

### Acesso por HTTPS

- O painel é servido pelo motor da Cidadela por HTTPS, na porta padrão de [componentes](../arquitetura/componentes.md#portas), com o certificado da CA interna da Guilda ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)).
- **Qualquer máquina leva ao painel:** toda máquina da Guilda responde no endereço do painel e redireciona o navegador para a Cidadela atual com `307`, **preservando o caminho e a query** (um link de criação de App ou de aprovação aberto numa Torre chega inteiro). O nome mDNS do painel (`mestre.local`) é publicado só pela Cidadela e muda de máquina junto com ela numa troca de Cidadela ([Cidadela e Torres](../guilda/cidadela-e-torres.md)). Uma Torre nunca serve o painel nem a API.
- **Cidadela suspensa:** responde `307` para a Cidadela atual quando a conhece; senão mostra só uma página "Cidadela suspensa", sem login e sem ações, e não anuncia `mestre.local` ([Cidadela suspensa](../guilda/cidadela-e-torres.md#cidadela-suspensa)).
- Confiar na CA no navegador é feito uma vez por aparelho; o passo a passo está em [primeiro acesso](../operacao/primeiro-acesso.md).
- Qualquer rota do painel ou da API sem sessão válida responde 401 e o navegador vai para a tela de login.

### Assistente do Dono (primeira vez)

1. A instalação que cria a Guilda (a primeira, que vira Cidadela) gera um **código de configuração** de uso único (12 caracteres) e o mostra no terminal da instalação ([instalação e atualização](../plataforma/instalacao-e-atualizacao.md)). Ele vale até ser usado.
2. Enquanto não existe nenhum usuário, o painel abre o assistente em vez do login.
3. O assistente pede: o código de configuração, nome, nome de login, senha (duas vezes) e PIN (duas vezes, regras do PIN em [usuários e níveis](../telegram/usuarios-e-niveis.md#pin-pessoal)).
4. Cria o usuário com nível **Dono**, marca o código como usado e abre a sessão.
5. Em seguida, o painel leva aos próximos passos do primeiro acesso (Apps fixos, Telegram, outras máquinas da Guilda), descritos em [primeiro acesso](../operacao/primeiro-acesso.md).

- Depois que existe um usuário, o assistente não abre mais; a rota dele responde 404.
- Código errado: 5 tentativas por endereço IP a cada 15 min (padrão).
- Perdeu o código antes de usar: o subcomando local `mestre setup-code`, só para root ou administrador na máquina da Cidadela, gera outro e invalida o anterior ([componentes](../arquitetura/componentes.md#mestre)).

### Primeiro acesso de cada usuário

- O Dono cadastra o usuário ([administração](administracao.md)) e o painel gera um **código de primeiro acesso** de uso único, válido por 24 h (padrão). O Dono entrega o código pessoalmente ou por um canal que ele escolher; o Mestre nunca o envia pelo Telegram.
- Na tela de login, "primeiro acesso" pede o código, o nome de login, a senha e o PIN.
- O mesmo mecanismo serve para **redefinir a senha** de alguém: o Dono gera um código novo; a senha antiga deixa de valer e as sessões do usuário acabam.

### Senha

- Mínimo de 12 caracteres, sem outras regras de composição.
- Guardada só como hash argon2id (com sal por usuário). O mesmo hash vai (desde o M7), criptografado, para a réplica dos Guardiões ([cofre](../plataforma/cofre.md#réplica-para-os-guardiões)), para o login da [página reserva](../autocura/pagina-reserva.md).
- Trocar a senha pede a senha atual e encerra as outras sessões do usuário.
- 5 senhas erradas em 15 min para o mesmo login → login bloqueado por 15 min (padrão), com aviso ao Dono.

### Sessões

- Ao entrar, a Cidadela cria uma sessão (tabela `panel_sessions`, [persistência](../arquitetura/persistencia.md)): guarda só o hash do token, usuário, aparelho (agente do navegador), IP, criação e último uso.
- O token vai num cookie `HttpOnly`, `Secure`, `SameSite=Strict`, com caminho `/`.
- **Expiração (padrão):** 12 h sem uso, e no máximo 7 dias desde o login. Depois disso, novo login.
- Toda requisição que muda algo, e a abertura do WebSocket, conferem o cabeçalho `Origin` contra o endereço do painel.
- Quando a sessão acaba, o WebSocket do painel é fechado pela Cidadela.
- **Encerram na hora** todas as sessões do usuário: suspensão, remoção, mudança de nível, redefinição ou troca de senha.
- Cada usuário vê e encerra as próprias sessões; o Dono vê e encerra as de todos.

### PIN em ações sensíveis

- Ações sensíveis pedem o PIN **a cada vez**, num campo que aparece depois do botão; não há "lembrar PIN".
- Quais ações pedem PIN: lista única em [PIN pessoal](../telegram/usuarios-e-niveis.md#pin-pessoal).
- Conferência e bloqueio por erros seguem as regras únicas de [PIN pessoal](../telegram/usuarios-e-niveis.md#pin-pessoal). Bloqueio do PIN também encerra a sessão em que os erros aconteceram.
- O servidor exige o PIN na própria requisição da ação; um PIN conferido não vale para outra ação.

### Nível em cada tela

O painel esconde o que o nível não permite, mas quem decide é o servidor, em toda requisição, pela [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).

## Regras e erros

- Mensagem de login errado é sempre "usuário ou senha inválidos", sem dizer qual.
- Usuário suspenso tentando entrar → mesma mensagem; a tentativa fica na auditoria.
- Relógio: expirações usam o relógio injetável do motor ([dublês](../qualidade/dubles.md)).
- Toda entrada, saída, falha e ação com PIN fica na auditoria (sem senha nem PIN).

## Critérios de aceite

- **CA-LOGIN-01** · *completa* — **Dado** uma Guilda recém-criada, sem usuários, **quando** abro o painel e informo o código de configuração, **então** o assistente cria o Dono e, depois disso, a rota do assistente responde 404 (MVP-1).
- **CA-LOGIN-02** · *rápida* — **Dado** uma sessão sem uso por 12 h (relógio falso), **quando** chega uma requisição, **então** a resposta é 401 e o WebSocket da sessão é fechado.
- **CA-LOGIN-03** · *rápida* — **Dado** uma ação de cofre sem PIN na requisição, **quando** chega ao servidor, **então** é recusada com "PIN obrigatório" e nada muda.
- **CA-LOGIN-04** · *rápida* — **Dado** um usuário com duas sessões abertas, **quando** o Dono o suspende, **então** as duas sessões deixam de valer na hora.
- **CA-LOGIN-05** · *rápida* — **Dado** 5 senhas erradas em 15 min, **quando** chega a sexta, **então** o login é recusado sem conferir a senha e o Dono recebe aviso.
- **CA-LOGIN-06** · *rápida* — **Dado** uma requisição de mudança com `Origin` diferente do painel, **quando** chega, **então** é recusada.
- **CA-LOGIN-07** · *rápida* — **Dado** um código de primeiro acesso com mais de 24 h, **quando** é usado, **então** é recusado.
- **CA-LOGIN-08** · *completa* — **Dado** uma Guilda com Cidadela A e Torre B, **quando** o navegador abre o endereço do painel em B, **então** é redirecionado para A, e a API em B não responde com dados; **depois** de B ser promovida, o mesmo endereço em A redireciona para B.
- **CA-LOGIN-09** · *rápida* — **Dado** uma Torre e a Cidadela no ar, **quando** o navegador abre na Torre `/github/apps/novo/abc?origem=telegram`, **então** recebe `307` para o mesmo caminho e a mesma query na Cidadela; **dado** uma Cidadela suspensa que não conhece a Cidadela atual, **então** ela mostra só a página "Cidadela suspensa", sem login nem ações.

## Depende de

- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Segurança](../arquitetura/seguranca.md)
- [Persistência](../arquitetura/persistencia.md)
- [Cofre](../plataforma/cofre.md)

