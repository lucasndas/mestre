# Administração no painel

> **Assunto:** telas de administração: decretos, delegações e regras temporárias, usuários, bases, fichas, perfis de conta, classes e Apps, teto, política, consumo, Guilda e saúde das máquinas (excluir máquina, passar a Cidadela, fuso da Guilda), segredos e diagnóstico · **Componente:** Painel · **Atualizado:** 2026-10-18

## Objetivo

Reunir num só lugar as telas para configurar e acompanhar o Mestre. Cada tela é só a interface: as regras ficam no documento dono, linkado na tabela, e quem pode fazer o quê fica na [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).

## Comportamento

### Telas

| Tela | O que mostra e permite | Dono das regras |
|---|---|---|
| Decretos | decretos ativos e suspensos por workspace, com autor, texto original, validade e o que liberaram; criar regra permanente ou exceção pontual; revogar | [decretos](../telegram/decretos.md) |
| Usuários | lista, cadastro, nível, workspaces, credencial do GitHub do usuário, códigos de pareamento do Telegram e de primeiro acesso, suspender, remover, transferir o posto de Dono | [usuários e níveis](../telegram/usuarios-e-niveis.md), [login](login.md) |
| Bases e fichas | criar, editar, apagar, exportar e importar em YAML ou JSON | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Perfis de conta | perfis por máquina e provedor; configuração inicial guiada (login e, no Codex, aprovar uma vez os hooks do Mestre em `/hooks`); estado do login e da confiança (checklist ALL-11) | [bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta) |
| Classes | classes globais, do workspace e das mesas, com o App vinculado e o estado dele (rótulos de [estados de um App](../github/criacao-de-apps.md#estados-de-um-app)); propostas de classe do workspace esperando aprovação | [classes](../classes/classes.md), [identidades e Apps](../github/identidades-e-apps.md); o resultado do [teste de classe](../provedores/teste-de-classe.md) por provedor, com o botão **Testar** (Administrador ou acima); as [sugeridas](../classes/classes-sugeridas.md#ativar), para ativar (com a contagem de Apps que vão nascer) e as propostas de atualização delas |
| Apps do GitHub | Apps fixos e por classe, workspaces em que estão instalados, contagem "N de 100"; para o Dono, os pedidos de criação abertos com o link de criação e o botão "novo link" para os vencidos | [identidades e Apps](../github/identidades-e-apps.md), [criação de Apps](../github/criacao-de-apps.md) |
| Delegações e regras temporárias | delegações de decisão (tipo, escopo, validade, autor, decisões tomadas com base nelas), subdelegações e [regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md) ativas, com revogar (Administrador ou acima); para o Dono, renovar e o botão **Revogar todas as delegações** | [delegação](../aprovacoes/delegacao.md) |
| Teto do workspace | o teto em vigor; "ampliar" abre a criação de um decreto, nunca edita direto | [teto do workspace](../classes/teto-do-workspace.md) |
| Política de aprovação | editor do arquivo de política com validação antes de salvar | [arquivo de política](../aprovacoes/arquivo-de-politica.md) |
| Instruções extras | texto do usuário para o Grão-Mestre e para os Mestres de mesa de cada workspace | [Grão-Mestre](../orquestracao/grao-mestre.md), [Mestre da mesa](../orquestracao/mestre-da-mesa.md) |
| Workspaces | lista, criar (abre a sessão zero), acompanhar as etapas da sessão zero e tentar de novo a que falhou, pausar e retomar | [workspaces](../orquestracao/workspaces.md), [sessão zero do workspace](../sessao-zero/workspace.md) |
| Consumo de uso | gráficos e tabelas por personagem, missão, mesa, workspace, conta e dia; limites configurados e quanto falta | [limites de uso](../plataforma/limites-de-uso.md) |
| Guilda e saúde das máquinas | ver seção abaixo | [camadas](../autocura/camadas.md), [checklist](../plataforma/checklist.md) |
| Segredos | nomes dos segredos do cofre, tipo, impressão curta, datas e metadados; nenhum valor salvo é mostrado de novo, nem ao Dono: só substituir e apagar, com PIN; é o único lugar de cadastro de credenciais de site (página de login, usuário, segredo, dono, classes, domínios, tipo, com a sugestão de preferir senha de app, token ou chave de API, e validade opcional); avisos de validade; gerar nova chave de recuperação, com PIN, avisando que backups antigos exigem a anterior e que sem ela o backup do cofre é inútil | [credenciais de site](../plataforma/cofre.md#credenciais-de-site), [backup e chave de recuperação](../plataforma/cofre.md#backup-e-chave-de-recuperação) |
| Diagnóstico com IA | ficha de diagnóstico de cada máquina | [diagnóstico com IA](../autocura/diagnostico-com-ia.md#ficha-de-diagnóstico) |
| Auditoria | trilha de auditoria com filtros (quem, o quê, quando, workspace) | [persistência](../arquitetura/persistencia.md) |

- Cada usuário só vê os workspaces dele; o Dono vê todos.
- Ações sensíveis pedem PIN ([login](login.md#pin-em-ações-sensíveis)).
- Toda alteração feita aqui entra na auditoria com o usuário e o antes/depois (sem valores de segredos).

### Guilda e saúde das máquinas

Uma linha por máquina da Guilda, atualizada em tempo real, com a época atual no topo. Regras da Guilda em [Guilda](../guilda/guilda.md) e [Cidadela e Torres](../guilda/cidadela-e-torres.md). No [modo máquina única](../autocura/camadas.md#modo-máquina-única), uma faixa fixa avisa que, se a máquina inteira cair, ninguém de fora percebe, e sugere instalar o Mestre em uma segunda máquina para ela entrar na Guilda.

| Coluna | Conteúdo |
|---|---|
| Papel | Cidadela ou Torre; "Cidadela suspensa", "GitHub fora" ou "Telegram parado" quando for o caso ([trava no GitHub](../guilda/trava-no-github.md#github-fora)) |
| Estado | conectada, sem sinal (desde quando), removida; "fora da rede local" quando entrou por endereço; selo do incidente aberto, se houver |
| Motor e Guardião | versão de cada um, protocolo negociado, último heartbeat de cada conexão |
| Ficha da máquina | sistema, interface gráfica, provedores logados por perfil, navegador e Operador do PC (e, se indisponível, o motivo), CPU, memória, GPU, transcrição ([ficha da máquina](../guilda/ficha-da-maquina.md)) |
| Excluída | marca do Dono, com PIN: a máquina excluída não recebe o cofre nem o envelope e fica fora da troca de Cidadela ([replicação](../guilda/replicacao.md)); também o estado da réplica (banco, arquivos e, se não excluída, cofre) |
| Carga | CPU, memória, disco livre, terminais rodando |
| Agentes | personagens rodando na máquina, com mesa e estado (link para o canvas) |
| Autocura | ações de cura recentes (últimos 7 dias), com resultado ([camadas](../autocura/camadas.md#anti-loop-e-auditoria)) |
| Checklist | alertas abertos, com a correção oferecida ([checklist](../plataforma/checklist.md)) |
| Prioridade | prioridade da máquina para ser a Torre condutora e para propor a promoção (editável pelo Dono) ([camadas](../autocura/camadas.md#eleição-da-torre-condutora)) |
| Página reserva | endereço da [página reserva](../autocura/pagina-reserva.md) da máquina (no modo máquina única, a da Cidadela) |

Ações da tela: aceitar ou recusar uma máquina que pediu para entrar na Guilda, remover uma máquina, **excluir** uma máquina ou tirar a exclusão, **passar a Cidadela** a uma máquina online (troca planejada) e responder ao pedido de promoção, tudo só o Dono, com PIN ([Guilda](../guilda/guilda.md), [Cidadela e Torres](../guilda/cidadela-e-torres.md)); mudar a prioridade (Dono); aplicar uma correção oferecida pelo checklist; ativar ou desativar a transcrição numa máquina ([Whisper](../plataforma/instalacao-e-atualizacao.md#whisper)); pedir ao Guardião as ações da [lista fechada](../autocura/guardiao.md#lista-fechada-de-ações) (status, logs, reiniciar, checklist).

- **Promoção recusada:** passar a Cidadela ou promover uma máquina excluída ou **fora da rede local** é recusado com o motivo, no painel e no Telegram: ninguém da Guilda alcança essa máquina pelo `mestre.local`.

### Fuso da Guilda

O fuso da Guilda (padrão: o do sistema da máquina que criou a Guilda) fica no banco da Guilda e só o Dono o muda, nesta tela. Vale para o backup diário às 03:00 ([persistência](../arquitetura/persistencia.md#backup)), a virada do dia dos [limites de uso](../plataforma/limites-de-uso.md), as validades dos [decretos](../telegram/decretos.md) e, quando o workspace não tem um próprio, os [rituais](../orquestracao/rituais.md). Não muda numa troca de Cidadela.

### Operador do PC

Tela só do **Dono** ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)); comportamento em [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc).
- **Por máquina habilitada:** usuário do Windows configurado, estado (sem usuário logado, tela bloqueada, pronto, em sessão), duração máxima (padrão 2 h), a configuração inicial guiada (conferência do Chrome e da extensão e o terminal de login do Claude Code no perfil do Operador, aberto com a conta do usuário, sem agente).
- **Lista de MCPs de controle** do computador e do navegador do usuário, usada pela regra fixa ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)): o padrão e o nome do MCP de controle do computador que o Dono instalou.
- **Sessões:** cada sessão de `operator_sessions` com tarefa, quem aprovou, início, fim e motivo, as ações completas da auditoria e as **capturas de tela** numa linha do tempo, decifradas só para o Dono, a cada abertura, com auditoria de quem viu ([persistência](../arquitetura/persistencia.md#operador-do-pc)).

### Contagem de Apps

- O topo da tela de Apps mostra "N de 100 Apps na conta": os Apps do Mestre, contados pelo banco, mais o número de outros Apps da conta, que o Dono informa nessa tela (padrão 0) e atualiza quando quiser. O Mestre não lê a página de Apps do GitHub.
- Com 90 ou mais (padrão), uma faixa de aviso aparece também na tela de classes ([identidades e Apps](../github/identidades-e-apps.md)).

### Exportar e importar

- Bases e fichas: a tela exporta e importa; as regras de formato e de conflito de nomes são as de [exportar e importar](../orquestracao/bases-e-fichas.md#exportar-e-importar).

## Regras e erros

- O servidor confere o nível em toda requisição; telas sem permissão nem aparecem no menu.
- Salvar um arquivo inválido (política, importação) → nada é gravado; os erros aparecem com a linha.
- Valores de segredos nunca são devolvidos pela API, nem mascarados: só nome, tipo, impressão curta e datas.
- Remover uma máquina da Guilda com personagens rodando → o painel avisa quantos e quais antes de confirmar.

## Critérios de aceite

- **CA-ADM-01** · *rápida* — **Dado** um Administrador, **quando** chama a API de cadastrar usuário, **então** recebe a recusa com o nível exigido.
- **CA-ADM-02** · *rápida* — **Dado** 90 Apps na conta, **quando** abro a tela de classes, **então** a faixa de aviso aparece.
- **CA-ADM-03** · *rápida* — **Dado** um segredo no cofre de teste, **quando** a tela de segredos carrega, **então** a resposta da API não contém o valor nem parte dele.
- **CA-ADM-04** · *rápida* — **Dado** "ampliar teto" com um novo valor, **quando** confirmo, **então** é criado um pedido de decreto (com PIN) e o teto em vigor não muda antes dele.
- **CA-ADM-05** · *rápida* — **Dado** uma ficha exportada em YAML, **quando** importo de volta, **então** a ficha resultante é igual à original.
- **CA-ADM-06** · *pesada* — **Dado** uma Cidadela e uma Torre instaladas, **quando** paro o serviço do motor da Torre, **então** a linha da Torre mostra "sem sinal" com o horário, a coluna Autocura mostra a ação do Guardião e, de volta, a coluna Checklist fica em dia (MVP-11, MVP-13).
- **CA-ADM-07** · *rápida* — **Dado** uma política com erro de formato, **quando** salvo, **então** nada muda e o erro aponta a linha.
- **CA-ADM-09** · *rápida* — **Dado** uma sessão do Operador com capturas, **quando** um Administrador chama a API de capturas, **então** recebe a recusa e nenhum arquivo é decifrado; **quando** o Dono abre a sessão, **então** vê as capturas em ordem e a abertura entra na auditoria.
- **CA-ADM-08** · *rápida* — **Dado** uma Guilda só com a Cidadela, **quando** a tela carrega, **então** aparece a faixa do modo máquina única; **dado** uma Torre aceita na Guilda, **então** ela some.
- **CA-ADM-10** · *rápida* — **Dado** um Administrador, **quando** chama a API de excluir uma máquina, de passar a Cidadela ou de mudar a prioridade, **então** recebe a recusa; **dado** o Dono sem PIN, **então** nada muda; com PIN, a exclusão é gravada e a linha mostra o cofre e o envelope apagados naquela máquina.
- **CA-ADM-11** · *rápida* — **Dado** uma Torre que entrou por endereço (fora da rede local) e outra excluída, **quando** o Dono pede, com PIN, para passar a Cidadela a cada uma, **então** as duas são recusadas com o motivo e nada muda; **dado** uma Torre na rede local e não excluída, **então** a troca planejada começa.
- **CA-ADM-12** · *rápida* — **Dado** uma delegação, uma subdelegação e duas regras temporárias ativas, **quando** um Administrador abre a tela de delegações, **então** vê as quatro e revoga uma regra temporária, mas não vê o botão **Revogar todas as delegações** e a API dele recusa com o nível exigido; **quando** o Dono usa o botão e confirma, **então** as quatro ficam revogadas e a auditoria registra.

## Depende de

- [Login](login.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Decretos](../telegram/decretos.md)
- [Workspaces](../orquestracao/workspaces.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)
- [Classes](../classes/classes.md)
- [Identidades e Apps](../github/identidades-e-apps.md)
- [Criação de Apps](../github/criacao-de-apps.md)
- [Política de aprovação](../aprovacoes/politica.md)
- [Cofre](../plataforma/cofre.md)
- [Limites de uso](../plataforma/limites-de-uso.md)
- [Guilda](../guilda/guilda.md)
