# Usuários e níveis

> **Assunto:** usuários autorizados, níveis e o que cada um pode, cadastro, pareamento, PIN, suspensão e transferência de Dono · **Componente:** Motor · **Atualizado:** 2026-10-26

## Objetivo

Definir quem pode falar com o Mestre e o que cada pessoa pode fazer. Este documento é a **fonte única dos níveis**: painel, Telegram, página reserva e aprovações consultam a tabela abaixo e não repetem regras de nível.

## Comportamento

### Cadastro

- Só o **Dono** cadastra usuários, no painel ([administração](../painel/administracao.md)).
- Campos de cada usuário:
  - nome, nível e workspaces em que pode atuar;
  - ID numérico do Telegram (opcional no cadastro; preenchido no pareamento);
  - login do GitHub (`github_login`, opcional): como o review dele feito direto no GitHub é reconhecido, sem precisar da credencial ([aprovação no GitHub](../github/aprovacao-no-github.md#review-feito-direto-no-github));
  - credencial pessoal do GitHub (opcional, para as aprovações dele; o login que ela confere é o `github_login`).
- Os usuários do painel e do Telegram são os mesmos. O primeiro Dono nasce no assistente de primeiro acesso ([login](../painel/login.md)).
- O Dono atua em todos os workspaces. Os demais só nos workspaces da lista dele.
- Existe **um único Dono** por Guilda.

### Níveis e capacidades

"Nos seus workspaces" vale para todos os níveis, menos para o Dono.

| Capacidade | Dono | Administrador | Aprovador | Observador |
|---|---|---|---|---|
| Receber alertas e notificações ([roteamento](roteamento.md)) | sim | sim | sim | sim |
| Consultar status: `/status`, `/missoes`, `/aprovacoes`, `/decretos`, `/guilda` (os pedidos e botões da Guilda só ao Dono), painel em leitura | sim | sim | sim | sim |
| Conversar com o Grão-Mestre (pedidos e perguntas) | sim | sim | sim | não |
| Aprovar ou negar pedidos de aprovação (as [respostas](../orquestracao/cadeia-de-aprovacao.md#respostas) que criam regra seguem as linhas de decreto) | sim | sim | sim | não |
| Criar exceção pontual por iniciativa própria ([decretos](decretos.md)) | sim | sim | sim | não |
| Criar regra permanente (decreto) | sim | sim | não | não |
| Revogar decretos | qualquer um | de Administrador e de Aprovador | só as próprias exceções ainda não usadas | não |
| `/pausar` e `/retomar` (também retomar um personagem pausado pelo disjuntor da [cadeia](../orquestracao/cadeia-de-aprovacao.md#salvaguardas)) | sim | sim | não | não |
| Botões **Pausar mesa** e **Pausar autonomia** (só restringem, sem PIN) e retomar a pausa de autonomia feita por uma pessoa ([supervisão](../orquestracao/supervisao-da-cadeia.md)) | sim | sim | sim | não |
| Revogar [regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md) e subdelegações, inclusive pelo botão **Foi um erro** do resumo ([resumo](../aprovacoes/resumo-e-sugestoes.md)) | sim | sim | não | não |
| `/travar` ([segurança do canal](seguranca-do-canal.md)) | sim | sim | não | não |
| `/destravar` (com PIN) | sim | não | não | não |
| Encerrar uma sessão do Operador do PC: `/operador encerrar` e botão **Encerrar agora** ([Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc)) | sim | sim | não | não |
| Digitar no terminal de um agente pelo canvas | sim | sim | não | não |
| Editar o canvas: conexões, missões, fixar personagens, pergaminhos, grimórios e rituais ([canvas](../painel/canvas.md#ações-no-canvas)); ler e mover os elementos é de quem vê a mesa | sim | sim | não | não |
| Criar workspace | sim | sim | não | não |
| Alterar a configuração de um workspace pelo painel (fichas do Grão-Mestre e dos Mestres de mesa, instruções extras, limites, retenção, fuso), menos o que outra linha reserva | sim | sim | não | não |
| Aprovar classe do workspace ([classes](../classes/classes.md)) | sim | sim | não | não |
| Ampliar o teto ou passar do teto de custo ([teto](../classes/teto-do-workspace.md)) | sim | sim | não | não |
| Aprovar ações de cura que pedem pessoa e correções de rede, firewall ou serviços do checklist (desde o M7) | sim | sim | não | não |
| Ativar classes e receitas sugeridas, rodar **Testar** numa classe, acrescentar ao catálogo MCP, recuperar a Cidadela e pedir diagnóstico pela [página reserva](../autocura/pagina-reserva.md), ver a Auditoria no painel | sim | sim | não | não |
| Ativar ou desativar a transcrição numa máquina ([Whisper](../plataforma/instalacao-e-atualizacao.md#whisper)) | sim | sim | não | não |
| Cadastrar, editar, suspender e remover usuários; gerar código de pareamento | sim | não | não | não |
| Aceitar máquinas na Guilda, remover e excluir máquinas, mudar a prioridade, aprovar a promoção a Cidadela e passar a Cidadela a outra máquina ([Guilda](../guilda/guilda.md), [Cidadela e Torres](../guilda/cidadela-e-torres.md)) | sim | não | não | não |
| Editar bases, fichas, política de aprovação e acessar o cofre | sim | não | não | não |
| Criar e instalar GitHub Apps pelo link ([criação de Apps](../github/criacao-de-apps.md)) | sim | não | não | não |
| Configurar perfis de conta: login e, no Codex, confiança dos hooks ([perfis de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta)) | sim | não | não | não |
| Aprovar a ativação do Operador do PC (`ativacao_operador`), ligar `operator` no teto, delegar essa aprovação ao Grão-Mestre e ver as capturas de tela das sessões ([política](../aprovacoes/politica.md#tipos-de-ação)) | sim | não | não | não |
| Delegar ao Grão-Mestre a decisão de um tipo `alto` e revogar de uma vez todas as delegações e regras temporárias (`/revogar_tudo`) ([delegação](../aprovacoes/delegacao.md)) | sim | não | não | não |
| Fuso da Guilda, limite diário total de uso e zerar o registro de termos (`glossary_seen`) de um usuário | sim | não | não | não |
| Conectar o próprio GitHub (credencial pessoal, [aprovação no GitHub](../github/aprovacao-no-github.md)) | sim | sim | sim | não |
| Transferir o posto de Dono | sim | não | não | não |

- **Observador:** se escrever texto livre, a mensagem não vai ao Grão-Mestre. O bot responde, uma vez por dia no máximo, com os comandos que ele pode usar.
- **Grão-Mestre não é usuário.** Ele é o aprovador delegado, com as regras de [política](../aprovacoes/politica.md).
- **Aprovar pedidos:** a linha "aprovar ou negar" é o máximo; cada tipo de pedido tem o seu nível mínimo na [política](../aprovacoes/politica.md#tipos-de-ação), e o risco `alto` exige pelo menos Administrador (ex.: `acao_destrutiva` e `proposta_agents_md` exigem Administrador ou acima).
- **Ações sensíveis com PIN:** a lista única está em [PIN pessoal](#pin-pessoal).

### Pareamento por código

1. O Dono gera um código para um usuário já cadastrado.
   - O código tem 8 caracteres do alfabeto `A-Z2-9`, sem `0`, `O`, `1` e `I`.
   - É de uso único e vale 30 minutos (padrão).
2. O painel mostra o código e o link `https://t.me/<bot>?start=<código>`.
3. O usuário abre o link ou manda ao bot `/start <código>` ou só o código, em chat privado.
4. O motor confere quatro coisas:
   - o código existe, não expirou e não foi usado;
   - o chat é privado;
   - se o cadastro já tem ID do Telegram, o `from.id` é esse ID;
   - o ID não pertence a outro usuário.
5. Se tudo confere, o motor grava o `from.id`, marca o código como usado e responde com nível, workspaces e uma frase de boas-vindas. O Dono recebe um aviso.
- **Tentativas erradas:** o bot responde "código inválido ou expirado" só a mensagens com formato de pareamento; o limite é 5 por ID do Telegram por hora, e depois o bot fica em silêncio. Cada falha conta como tentativa não autorizada ([segurança do canal](seguranca-do-canal.md)).
- **Trocar o ID:** para trocar o Telegram de alguém, o Dono limpa o ID no cadastro e gera um código novo.
- O passo a passo para quem usa está em [parear Telegram](../operacao/parear-telegram.md).

### PIN pessoal

- **Definição:** cada usuário define o próprio PIN no painel. São de 6 a 12 dígitos. A Cidadela guarda só o hash (argon2id), nunca o PIN.
- **Sem PIN:** quem não tem PIN não conclui nenhuma ação que exige PIN.
- **Ações que pedem PIN** (lista única; a cada vez, no Telegram ou no painel):
  - decretos que afrouxam a segurança ([decretos](decretos.md));
  - gerenciar usuários: cadastrar, editar nível, suspender, remover, gerar códigos, transferir o posto de Dono;
  - cofre ([cofre](../plataforma/cofre.md));
  - `/destravar` ([segurança do canal](seguranca-do-canal.md));
  - aprovar script de cura ou volta de versão com restauração do banco ([diagnóstico com IA](../autocura/diagnostico-com-ia.md), desde o M7);
  - aceitar, remover e excluir máquinas da Guilda, aprovar a promoção a Cidadela e passar a Cidadela a outra máquina ([Guilda](../guilda/guilda.md));
  - tipos de aprovação marcados com PIN na [política](../aprovacoes/politica.md).
- **Troca e reset:** trocar o PIN pede o PIN atual. O Dono pode resetar o PIN de outro usuário, que define um novo no próximo login.
- **Pedido de PIN no Telegram:**
  - Quem pede é o **motor**, não o Grão-Mestre. O motor manda uma mensagem com o resumo da ação e um campo de resposta (`ForceReply`).
  - A resposta do usuário é interceptada pelo motor. Ela nunca entra na caixa de entrada, nunca chega a agentes e nunca vai para logs.
  - O motor apaga a mensagem com o PIN na hora (`deleteMessage`, permitido em chat privado).
  - O pedido vale 5 minutos (padrão).
- **Erros de PIN:**
  - 3 erros no mesmo pedido cancelam o pedido.
  - 5 erros em 15 minutos bloqueiam o PIN do usuário por 30 minutos (padrão) e geram alerta ao Dono.
- O Telegram não tem criptografia ponta a ponta, então o PIN passa pelos servidores do Telegram. O PIN confirma a intenção, mas não é segredo forte. Por isso o bloqueio por tentativas é obrigatório.

### Suspensão e remoção

- **Suspender** (só o Dono) tem efeito imediato:
  - encerra as sessões do usuário no painel;
  - o bot passa a ignorar as mensagens dele, sem alerta;
  - **invalida todas as aprovações pendentes dele**: votos e exceções pontuais dadas por ele e ainda não executadas. A ação volta a esperar outra aprovação ([fluxo](../aprovacoes/fluxo.md));
  - os decretos permanentes dele seguem a regra em [decretos](decretos.md#autor-suspenso-ou-removido).
- **Reativar:** só o Dono pode.
- **Remover:** tem os mesmos efeitos e libera o ID do Telegram. O registro fica marcado como removido, para o histórico e a auditoria continuarem mostrando o nome. Decretos já executados continuam no histórico.

### Transferência do posto de Dono

1. No painel, o Dono escolhe um usuário ativo que já tenha PIN e confirma com o próprio PIN.
2. O escolhido recebe o pedido no painel e no Telegram e aceita com o PIN dele, em até 24 horas (padrão).
3. A troca é atômica: o novo Dono assume e o antigo vira Administrador de todos os workspaces. O novo Dono pode ajustar isso depois.
- Nunca existem zero ou dois Donos. O Dono pode cancelar o pedido antes do aceite. Tudo fica na auditoria, e os dois recebem aviso.

## Regras e erros

- Toda checagem de nível e de workspace é feita pelo motor, nunca pelo agente.
- Ação negada por nível: o bot ou o painel diz qual nível é exigido. Nada é executado.
- Mudança de nível ou de workspaces vale na próxima mensagem. Aprovações pendentes que o novo nível não permite são invalidadas, como na suspensão.
- O Dono não pode suspender nem remover a si mesmo. Antes, precisa transferir o posto.
- **Implementação:** `internal/users` guarda os usuários, o login e a senha do painel (`login.go`; [login](../painel/login.md#implementação)) (tabelas `users`, `user_workspaces`, `telegram_pairing_codes`, `telegram_pairing_failures`, `pin_requests`, `pin_failures` e `owner_transfers`, no banco da Guilda): a tabela de níveis é `Can`/`Require` (um nível ou capacidade desconhecido é recusado), o pareamento é `NewCode`/`Pair`, o PIN (argon2id) é `StartPIN`/`CheckPIN`/`Consume` e a transferência é `StartTransfer`/`AcceptTransfer`. Quem pede o PIN no Telegram e quem recebe o texto livre do Observador é o [bot](bot.md) (`internal/telegram`, etapa 2): o texto livre de quem não fala com o Grão-Mestre recebe só os comandos do nível (CA-USR-01) e o PIN é uma resposta capturada pelo motor (`PINCapture`, CA-USR-06).

## Critérios de aceite

- **CA-USR-01** · *rápida* — **Dado** um Observador pareado, **quando** ele manda texto livre, **então** nada entra na caixa de entrada do Grão-Mestre e o bot responde com os comandos permitidos.
- **CA-USR-02** · *rápida* — **Dado** um Aprovador, **quando** ele tenta criar uma regra permanente, **então** o motor recusa e informa que exige Administrador ou acima.
- **CA-USR-03** · *rápida* — **Dado** um código válido, **quando** o usuário o envia em chat privado, **então** o `from.id` é gravado e um segundo uso do mesmo código é recusado.
- **CA-USR-04** · *rápida* — **Dado** um código com mais de 30 minutos (relógio falso), **quando** ele é enviado, **então** o pareamento falha e conta como tentativa não autorizada.
- **CA-USR-05** · *rápida* — **Dado** um Aprovador que aprovou uma ação ainda não executada, **quando** o Dono o suspende, **então** a aprovação é invalidada e a ação não é executada.
- **CA-USR-06** · *rápida* — **Dado** um pedido de PIN no Telegram, **quando** o usuário responde, **então** a mensagem é apagada, não entra na caixa de entrada e não aparece em log.
- **CA-USR-07** · *rápida* — **Dado** 5 PINs errados em 15 minutos, **quando** chega o sexto, **então** ele é recusado sem verificação e o Dono recebe alerta.
- **CA-USR-08** · *completa* — **Dado** o Dono e um Administrador com PIN, **quando** os dois confirmam a transferência, **então** os níveis trocam numa única transação e em nenhum momento há zero ou dois Donos.
- **CA-USR-09** · *rápida* — **Dado** o mesmo ID do Telegram tentando 6 códigos errados em 1 h (relógio falso), **quando** chega o sexto, **então** o bot não responde e cada falha foi registrada como tentativa não autorizada; **dado** o código do usuário A enviado por um `from.id` já cadastrado em B, **então** o pareamento é recusado e o código não é consumido.
- **CA-USR-10** · *rápida* — **Dado** um Aprovador, **quando** toca **Pausar mesa**, **então** a mesa pausa; **quando** retoma a pausa de autonomia feita por uma pessoa, **então** o motor aceita; **quando** tenta `/pausar` ou **Foi um erro**, **então** o motor recusa e diz que o nível exigido é Administrador; **dado** um Observador, **então** nenhum dos três vale.

## Depende de

- [Cofre](../plataforma/cofre.md)
- [Persistência](../arquitetura/persistencia.md)
- [Segurança](../arquitetura/seguranca.md)
