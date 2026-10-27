# Administração no painel

> **Assunto:** telas de administração: decretos, delegações e regras temporárias, usuários, bases, fichas, perfis de conta, classes e Apps, teto, política, consumo, Guilda e saúde das máquinas (excluir máquina, passar a Cidadela, fuso da Guilda), segredos e diagnóstico · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

Reunir num só lugar as telas para configurar e acompanhar o Mestre. Cada tela é só a interface: as regras ficam no documento dono, linkado na tabela, e quem pode fazer o quê fica na [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).

## Comportamento

### Telas

Todas moram sob **Configurações** (Workspaces tem item próprio no menu): um índice de quatro grupos, cada entrada com a frase do que a tela faz, e migalha `Configurações › grupo › tela` em cada uma ([estrutura](design.md#estrutura-da-tela)). Conta: sessões, senha, GitHub pessoal. Pessoas e acesso: usuários, segredos, auditoria. Infraestrutura: Guilda e máquinas, perfis de conta (com a frase "as contas de IA de cada máquina"), Apps do GitHub, classes, bases e fichas. Regras e consumo: decretos, delegações, supervisão da cadeia, teto, política, instruções extras e consumo de uso. Os endereços atuais (`/usuarios`, `/decretos`…) continuam valendo, para links salvos e avisos. A tabela abaixo diz o que cada tela mostra.

| Tela | O que mostra e permite | Dono das regras |
|---|---|---|
| Decretos | decretos ativos e suspensos por workspace, com autor, texto original, validade e o que liberaram; criar regra permanente ou exceção pontual; revogar | [decretos](../telegram/decretos.md) |
| Usuários | lista, cadastro, nível, workspaces, credencial do GitHub do usuário, códigos de pareamento do Telegram e de primeiro acesso, suspender, remover, transferir o posto de Dono | [usuários e níveis](../telegram/usuarios-e-niveis.md), [login](login.md) |
| Bases e fichas | criar, editar, apagar, exportar e importar em YAML ou JSON | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Perfis de conta | perfis por máquina e provedor; configuração inicial guiada (login e, no Codex, aprovar uma vez os hooks do Mestre em `/hooks`); estado do login e da confiança (checklist ALL-11) | [bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta) |
| Classes | classes globais, do workspace e das mesas, com o App vinculado e o estado dele (rótulos de [estados de um App](../github/criacao-de-apps.md#estados-de-um-app)); **Testar de novo** (desde o M7, o [teste de classe](../provedores/teste-de-classe.md)); propostas de classe do workspace esperando aprovação, com a marca "não testada" quando for o caso (inclusive as de calibração e de versão nova das sugeridas); desde o M8, o pacote [sugerido](../classes/classes-sugeridas.md): ativar classes e receitas e ligar as skills de apoio e os servidores MCP de cada uma | [classes](../classes/classes.md), [identidades e Apps](../github/identidades-e-apps.md); o resultado do [teste de classe](../provedores/teste-de-classe.md) por provedor, com o botão **Testar** (Administrador ou acima); as [sugeridas](../classes/classes-sugeridas.md#ativar), para ativar (com a contagem de Apps que vão nascer) e as propostas de atualização delas |
| Apps do GitHub | Apps fixos e por classe, workspaces em que estão instalados, contagem "N de 100"; para o Dono, os pedidos de criação abertos com o link de criação e o botão "novo link" para os vencidos | [identidades e Apps](../github/identidades-e-apps.md), [criação de Apps](../github/criacao-de-apps.md) |
| Delegações e regras temporárias | delegações de decisão (tipo, escopo, validade, autor, decisões tomadas com base nelas), subdelegações e [regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md) ativas, com revogar (Administrador ou acima); para o Dono, renovar e o botão **Revogar todas as delegações** | [delegação](../aprovacoes/delegacao.md) |
| Supervisão da cadeia | por mesa: pausa de autonomia (quem pausou, quando, motivo) e métricas da fila de aprovações (abertos, o mais antigo, por nível), com **Pausar autonomia** e **Retomar** (Aprovador ou acima; retomar a pausa do Grão-Mestre, só ele) | [supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md#pausa-de-autonomia) |
| Catálogo MCP | servidores do workspace: acrescentar e remover (Administrador ou acima; os de controle, só o Dono) e a reaprovação quando a lista de ferramentas de um servidor muda; o catálogo muda só por aqui (desde o M7) | [MCP por classe](../classes/mcp-por-classe.md) |
| Teto do workspace | o teto em vigor; "ampliar" abre a criação de um decreto, nunca edita direto | [teto do workspace](../classes/teto-do-workspace.md) |
| Política de aprovação | editor do arquivo de política com validação antes de salvar | [arquivo de política](../aprovacoes/arquivo-de-politica.md) |
| Instruções extras | texto do usuário para o Grão-Mestre e para os Mestres de mesa de cada workspace | [Grão-Mestre](../orquestracao/grao-mestre.md), [Mestre da mesa](../orquestracao/mestre-da-mesa.md) |
| Workspaces | lista, criar (abre a sessão zero), acompanhar as etapas da sessão zero e tentar de novo a que falhou, pausar e retomar; os comandos de verificação (`verificacao.yaml`): ver a proposta do `analysis`, editar e confirmar (Administrador) | [workspaces](../orquestracao/workspaces.md), [sessão zero do workspace](../sessao-zero/workspace.md); a tela em [workspaces no painel](workspaces.md) |
| Consumo de uso | gráficos e tabelas por personagem, missão, mesa, workspace, conta e dia; limites configurados e quanto falta | [limites de uso](../plataforma/limites-de-uso.md) |
| Guilda e saúde das máquinas | ver seção abaixo; as colunas Autocura, Checklist e Página reserva, o Guardião e o botão da transcrição são do M7 | [camadas](../autocura/camadas.md), [checklist](../plataforma/checklist.md) |
| Segredos | nomes dos segredos do cofre, tipo, impressão curta, datas e metadados; nenhum valor salvo é mostrado de novo, nem ao Dono: só substituir e apagar, com PIN; é o único lugar de cadastro de credenciais de site (página de login, usuário, segredo, dono, classes, domínios, tipo, com a sugestão de preferir senha de app, token ou chave de API, e validade opcional); avisos de validade das credenciais ([cofre](../plataforma/cofre.md#credenciais-de-site)); gerar nova chave de recuperação, com PIN, avisando que backups antigos exigem a anterior e que sem ela o backup do cofre é inútil | [credenciais de site](../plataforma/cofre.md#credenciais-de-site), [backup e chave de recuperação](../plataforma/cofre.md#backup-e-chave-de-recuperação) |
| Diagnóstico com IA (desde o M7) | ficha de diagnóstico de cada máquina | [diagnóstico com IA](../autocura/diagnostico-com-ia.md#ficha-de-diagnóstico) |
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

Ações da tela: aceitar ou recusar uma máquina que pediu para entrar na Guilda, remover uma máquina, **excluir** uma máquina ou tirar a exclusão, **passar a Cidadela** a uma máquina online (troca planejada), tudo só o Dono, com PIN ([Guilda](../guilda/guilda.md), [Cidadela e Torres](../guilda/cidadela-e-torres.md)); mudar a prioridade (Dono); aplicar uma correção oferecida pelo checklist; ativar ou desativar a transcrição numa máquina ([Whisper](../plataforma/instalacao-e-atualizacao.md#whisper)); pedir ao Guardião as ações da [lista fechada](../autocura/guardiao.md#lista-fechada-de-ações) (status, logs, reiniciar, checklist).

- **Do M6 ao M7:** a tela traz a faixa do modo máquina única, as colunas Papel, Estado, Motor, Ficha, Excluída, Carga, Agentes e Prioridade e o aviso de remoção; o Guardião, a autocura, o checklist, a página reserva e **ativar ou desativar a transcrição** (arquivos e benchmark do Whisper) chegam no M7, e a tela só diz se a máquina transcreve.
- **Promoção recusada:** passar a Cidadela ou promover uma máquina excluída ou **fora da rede local** é recusado com o motivo, no painel e no Telegram: ninguém da Guilda alcança essa máquina pelo `mestre.local`.

### Fuso da Guilda

O fuso da Guilda (padrão: o do sistema da máquina que criou a Guilda) fica no banco da Guilda e só o Dono o muda, nesta tela. Vale para o backup diário às 03:00 ([persistência](../arquitetura/persistencia.md#backup)), a virada do dia dos [limites de uso](../plataforma/limites-de-uso.md), as validades dos [decretos](../telegram/decretos.md) e, quando o workspace não tem um próprio, os [rituais](../orquestracao/rituais.md). Não muda numa troca de Cidadela.

### Implementação: Guilda

`internal/panel/routes_guild.go` (tipos em `types_guild.go`) e `internal/app/guildhost.go`, sobre o `guild.Citadel` que age; a tela é `web/src/screens/admin/Guild.tsx`. Tudo é do Dono (`manage_machines`, e `guild_settings` no fuso), com o PIN na própria requisição. O tópico `guild:main` do `/ws/ui` manda o snapshot e, a cada 3 s com alguém seguindo, a visão de novo se mudou. Máquina fora da rede local é a que não tem IP numa rede das interfaces da Cidadela (`guild.LocalNetworks`).

| Rota | O que faz |
|---|---|
| `GET /api/guild`, `GET /api/guild/machines/{id}/removal` | a visão (época, fuso, `single`, máquinas, pedidos) e o aviso da remoção (quantos e quais agentes rodam lá) |
| `POST /api/guild/joins/{id}/decide`, `…/machines/{id}/remove` (`confirm`), `…/exclusion`, `…/pass-citadel`, `PUT …/priority`, `PUT /api/guild/timezone` | aceitar ou recusar, remover, excluir, troca planejada (`Engine.Handover`, em segundo plano), prioridade, fuso; recusas são `refused` com `reason` (`excluded_machine`, `outside_local_network`, `citadel_protected`, `not_online`, `no_lock`, `not_revealed`, `handover_in_progress`…; uma troca planejada por vez) |

Auditoria `panel.guild.*` (`entry_decided`, `machine_removed`, `exclusion`, `citadel_pass_started`, `citadel_pass_refused`, `priority`, `timezone`) com `user_id` e antes/depois, sem PIN. "GitHub fora" e "Telegram parado" vêm da cerca (`Fence`: `lock-down` e `TelegramStopped`).

### Operador do PC

Tela só do **Dono** ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)); comportamento em [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc).
- **Por máquina habilitada:** usuário do Windows configurado, estado (sem usuário logado, tela bloqueada, pronto, em sessão), duração máxima (padrão 2 h), a configuração inicial guiada (conferência do Chrome e da extensão do Playwright MCP e o terminal de login do Claude Code no perfil do Operador, aberto com a conta do usuário, sem agente).
- **Servidores de controle** do computador e do navegador do usuário no [catálogo MCP](../classes/mcp-por-classe.md): só o Dono os cadastra, e marca `control: true` o que não está na lista de pacotes do binário; a regra fixa usa os dois ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)).
- **Sessões:** cada sessão de `operator_sessions` com tarefa, quem aprovou, início, fim e motivo, as ações completas da auditoria e as **capturas de tela** numa linha do tempo, decifradas só para o Dono, a cada abertura, com auditoria de quem viu ([persistência](../arquitetura/persistencia.md#operador-do-pc)).

### Contagem de Apps

- O topo da tela de Apps mostra "N de 100 Apps na conta": os Apps do Mestre, contados pelo banco, mais o número de outros Apps da conta, que o Dono informa nessa tela (padrão 0) e atualiza quando quiser. O Mestre não lê a página de Apps do GitHub.
- Com 90 ou mais (padrão), uma faixa de aviso aparece também na tela de classes ([identidades e Apps](../github/identidades-e-apps.md)).

### Exportar e importar

- Bases e fichas: a tela exporta e importa; as regras de formato e de conflito de nomes são as de [exportar e importar](../orquestracao/bases-e-fichas.md#exportar-e-importar).

As telas de decretos, delegações, supervisão da cadeia, teto, política e instruções extras estão em [regras e cadeia](administracao-regras.md).

### Implementação: usuários, segredos e auditoria

Código: `internal/panel/routes_users.go`, `routes_secrets.go` e `routes_audit.go` (tipos em `types_admin.go`, gerados em `web/src/gen/panel.ts`) e `web/src/screens/admin/`. O nível vem da [tabela](../telegram/usuarios-e-niveis.md#níveis-e-capacidades) (`forbidden_level` com `need` e `have`; o painel diz "exige o nível Dono; o seu é Administrador"); toda ação que muda algo pede o PIN na própria requisição.

| Rota | Quem | O que faz |
|---|---|---|
| `GET /api/users` | Dono | usuários, workspaces e a passagem de posto que espera |
| `POST /api/users`, `PATCH /api/users/{id}` | Dono, PIN | cadastra (devolve o código de primeiro acesso, uma vez) e edita nome, nível, workspaces e login do GitHub; workspace desconhecido é `invalid_workspace`, um segundo Dono é `owner_exists` |
| `POST /api/users/{id}/suspend`, `/reactivate`, `/remove`, `/pairing-code`, `/access-code` | Dono, PIN | suspender (sessões acabam), reativar, remover, código do Telegram e códigos de primeiro acesso e de redefinição; o Dono não age sobre si (`self_action`) |
| `GET`, `POST`, `DELETE /api/transfer` e `POST /api/transfer/accept` | Dono, PIN; quem aceita, com o PIN dele | passa o posto de Dono, cancela e aceita (as sessões dos dois acabam); é a tela `/transferencia` e a faixa no alto do painel |
| `GET /api/me/github`, `POST /api/me/github/begin` e `/poll` | Aprovador ou acima | "Conectar GitHub": o device flow de `github.Personal`; conta diferente da cadastrada é `github_login_mismatch` (o motor já descartou o token) e as demais recusas têm código próprio (`github_denied`, `github_expired`, `github_no_login`, `github_flow_off`, `github_not_ready`) |
| `GET /api/secrets` | Dono | nome, tipo, impressão, datas, metadados e o aviso de validade (`expiring`, `expired`, `stale`, com os dias), nunca valor |
| `POST /api/secrets/site`, `PUT` e `DELETE /api/secrets/{name}` | Dono, PIN | cadastra credencial de site, substitui e apaga; o painel só mexe nos tipos `site_credential` e `provider_api_key` (os que o próprio Mestre guarda são `secret_managed`) |
| `POST /api/recovery-key` | Dono, PIN | `{acknowledged: true}` (o aviso dos backups antigos, lido antes do PIN; sem ele `ack_required`) gera a chave, que só vem nessa resposta; `Vault.RewrapRecovery` reenvelopa a chave do cofre e troca a pública da Guilda de uma vez |
| `GET /api/audit` | Administrador ou acima | `user`, `action` (início do nome), `from` e `to` (dia no fuso da Guilda), `workspace`, `cursor`, `limit`; o Dono vê tudo, os outros só as linhas dos workspaces deles, e o que o Operador do PC fez só como fato |

Auditoria desta área (`panel.user.*`, `panel.access_code.created`, `panel.pairing_code.created`, `panel.transfer.*`, `panel.secret.*`, `panel.recovery_key.renewed`, `panel.github.*`): `user_id` de quem fez, `target` ou `secret` e `before`/`after` com o que a tela mostra, sem valor, código nem PIN. O filtro "o quê" da tela escolhe entre estas famílias.

### Implementação: classes, Apps, bases e fichas, perfis e consumo

Código: `internal/panel/routes_{classes,apps,presets,profiles,usage}.go` (tipos em `types_manage.go`; o motor entra por `Panel.SetManage`, `internal/app/panelmanage.go`) e `web/src/screens/admin/`. Tudo com o nível conferido no servidor; mudança pede o PIN.

| Rota | Quem | O que faz |
|---|---|---|
| `GET /api/classes` · `POST /api/classes/proposals/{id}/approve` | Administrador; PIN | classes global, do workspace e da mesa com o App e o estado dele; propostas do Grão-Mestre (`request`) e mudanças feitas por fora (`disk`), todas "não testada" até o M7; a faixa de 90 Apps vem em `apps`; "Aprovar" leva o hash visto e a `Library.Approve` confere nível, hash, problemas e teto (`proposal_stale`, `proposal_invalid`) |
| `GET /api/github/apps` · `PUT /api/github/apps/others` · `POST /api/github/apps/{name}/link` | Administrador lê; Dono muda; PIN no link | "N de 100 Apps"; quem não é Dono vê só os Apps fixos e os instalados nos repositórios dos seus workspaces; os pedidos de criação e o "novo link" são do Dono; o link sai nesta resposta, uma vez, porque só o hash do token fica guardado |
| `GET /github/apps/novo/{token}` (e `/github/manifest/callback`, `/github/apps/instalado`) | Dono | páginas de `github.Web` montadas no painel; outro nível vê "exige Dono" e nenhum formulário (CA-GH-APPS-04) |
| `GET /api/presets` · `PUT`/`DELETE /api/presets/{bases,sheets}/{name}` · `GET /api/presets/export` · `POST /api/presets/import[/plan]` | Dono; PIN ao mudar | cada base e ficha é editada como YAML (o formato da exportação); importar valida o arquivo inteiro, mostra os conflitos e aplica tudo ou nada (CA-ADM-05) |
| `GET`/`POST /api/profiles` · `DELETE /api/profiles/{name}` · `POST …/default` · `POST …/advance` | Dono; PIN, menos `advance` | perfis desta máquina e o passo da configuração guiada; sem os passos da máquina (instalador, terminal de login, conformidade: M7) `advance` responde `unavailable` |
| `GET /api/usage?days=` | qualquer nível; o servidor limita aos workspaces do usuário | consumo por dia, workspace, mesa, missão, personagem e conta (só o Dono), com os limites, o que falta e o vazio quando o provedor não deu o dado |

## Regras e erros

- O servidor confere o nível em toda requisição; telas sem permissão nem aparecem no menu.
- Salvar um arquivo inválido (política, importação) → nada é gravado; os erros aparecem com a linha.
- Valores de segredos nunca são devolvidos pela API, nem mascarados: só nome, tipo, impressão curta e datas.
- Login do Administrador pelo device flow do GitHub com um login diferente do cadastro → o motor descarta o token e o painel mostra o erro ([aprovação no GitHub](../github/aprovacao-no-github.md); CA-GH-APROV-06, M6).
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
- **CA-ADM-13** · *rápida* — **Dado** uma mesa com 3 pedidos `medio` abertos e o mais antigo há 40 min (relógio falso), **quando** um Aprovador toca **Pausar autonomia**, **então** a tela mostra a pausa com quem e quando, as métricas (3 abertos, 40 min) e os pedidos sobem ao Grão-Mestre; um Observador não vê o botão e a API recusa.
- **CA-ADM-14** · *rápida* — **Dado** o catálogo MCP, **quando** um Administrador acrescenta um servidor comum, **então** é aceito e a impressão da lista de ferramentas é guardada; um servidor `control: true`, só o Dono; **dado** a lista de ferramentas mudada, **então** o servidor fica suspenso até a reaprovação (desde o M7).
- **CA-ADM-16** · *rápida* — **Dado** o Dono, **quando** abre Configurações, **então** vê quatro grupos, cada entrada com uma frase de uso, toda tela da tabela acima alcançável em até dois cliques e marcada com a migalha `Configurações › grupo › tela`; **dado** um Aprovador, **então** os grupos e entradas que o nível dele não alcança não aparecem, e o endereço antigo de uma tela (`/decretos`) abre a mesma tela.
- **CA-ADM-15** · *rápida* — **Dado** o `verificacao.yaml` proposto, **quando** um Administrador edita e confirma, **então** vale a versão confirmada e a edição fica na auditoria; um Aprovador não vê a ação.

## Depende de

- [Login](login.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Decretos](../telegram/decretos.md)
- [Workspaces](../orquestracao/workspaces.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)
- [Classes](../classes/classes.md)
- [Identidades e Apps](../github/identidades-e-apps.md)
- [Criação de Apps](../github/criacao-de-apps.md)
- [Política de aprovação](../aprovacoes/politica.md) · [Delegação](../aprovacoes/delegacao.md) · [Teto do workspace](../classes/teto-do-workspace.md)
- [Cofre](../plataforma/cofre.md)
- [Limites de uso](../plataforma/limites-de-uso.md)
- [Guilda](../guilda/guilda.md)
