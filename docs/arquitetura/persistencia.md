# Persistência

> **Assunto:** SQLite por nó, o que vai no banco e o que vai em arquivo, tabelas principais, terminais, logs e backup · **Componente:** Todos · **Atualizado:** 2026-10-12

## Princípios

- Cada motor tem **um banco SQLite local**, em modo WAL, com migrações versionadas: o motor do Core tem o banco do Core, e todo motor de nó tem um banco de nó. Na máquina do Core, o nó local é sempre o serviço `mestre-node`, à parte, com o próprio banco de nó (pasta `no/`, abaixo; [componentes](componentes.md)).
- O **Core é a fonte da verdade**. O nó guarda o que ele roda e um cache do essencial para continuar funcionando quando o Core está fora; ao reconectar, sincroniza com o Core.
- O que precisa ser lido ou versionado por pessoas fica em **arquivo** (classes, pergaminhos, anexos, logs); o banco guarda só o índice e os vínculos.
- Segredos nunca vão para o banco em texto puro: ficam no [cofre](../plataforma/cofre.md).
- O Guardião não usa o banco do motor; o que ele guarda está em [Guardião](../autocura/guardiao.md).

## Banco: configuração

- Biblioteca `modernc.org/sqlite` ([ADR 0001](../decisoes/0001-stack-go.md)).
- `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000` (padrão).
- **Uma conexão de escrita** e um grupo de conexões só de leitura.
- **Migrações:** arquivos SQL numerados, embutidos no binário, um conjunto para o Core e outro para o nó; aplicados na subida, em transação, registrados em `schema_migrations`.
- **Migrações só aditivas** dentro de uma versão maior (acrescentar tabela ou coluna; remover só numa versão seguinte), para a versão anterior do motor ainda abrir o banco depois de uma volta de versão. Antes de migrar, o motor grava uma cópia `pre-migracao-<versão>.db` ([instalação e atualização](../plataforma/instalacao-e-atualizacao.md)). É essa cópia que a ação `rollback` com restauração do banco usa: rara, nunca automática, só com Dono ou Administrador e PIN ([Guardião](../autocura/guardiao.md)).

## Tabelas principais do Core (proposta)

Nomes de tabela em inglês (são identificadores de código). Colunas detalhadas ficam nas migrações; o significado de cada dado, no documento da funcionalidade.

| Grupo | Tabelas | Dono do comportamento |
|---|---|---|
| Nós e conexão | `nodes` (capacidades, prioridade, versão, estado, se é o nó local), `node_heartbeats`, `pairing_invites`, `node_credentials` (identificador e revogação, não a chave), `account_profiles`, `outbox` (mensagens com ack ainda sem confirmação, por nó) | [protocolo](../conectividade/protocolo.md), [pareamento](../conectividade/pareamento.md), [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Usuários | `users`, `user_workspaces`, `panel_sessions`, `telegram_pairing_codes` | [usuários e níveis](../telegram/usuarios-e-niveis.md), [login](../painel/login.md) |
| Workspaces e mesas | `workspaces`, `tables` (mesas: épico, branch, estado), `table_worktrees` (nó, caminho, último commit sincronizado), `bundle_transfers` | [workspaces](../orquestracao/workspaces.md), [mesas](../orquestracao/mesas.md) |
| Presets e classes | `bases`, `sheets`, `classes` (índice: nome, escopo, caminho, App), `templates` (versões dos modelos) | [bases e fichas](../orquestracao/bases-e-fichas.md), [classes](../classes/classes.md), [sessão zero do personagem](../sessao-zero/personagem.md) |
| Personagens e canvas | `characters`, `terminals`, `connections`, `notes` (índice de pergaminhos), `tomes`, `canvas_layout`, `grimoires` | [personagens](../orquestracao/personagens.md), [conexões](../orquestracao/conexoes.md), [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md), [grimórios](../orquestracao/grimorios.md) |
| Missões | `quests`, `quest_steps` (com issue/sub-issue), `step_runs` | [missões](../orquestracao/missoes.md) |
| Esperas e mensagens | `waits`, `inbox_messages` (com a origem da mensagem de usuário), `outbound_messages` (respostas guardadas para canal indisponível) | [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) |
| Rituais | `rituals`, `ritual_runs` | [rituais](../orquestracao/rituais.md) |
| Regras e aprovações | `decrees`, `approval_requests`, `approval_responses` | [decretos](../telegram/decretos.md), [fluxo de aprovação](../aprovacoes/fluxo.md) |
| GitHub | `github_apps`, `app_installations`, `github_objects` (objetos que o motor criou: mesa/etapa ↔ issue, sub-issue, PR, comentário), `github_poll_state` (ETag e cursor por recurso), `github_seen_events` (chaves de eventos já vistos) | [identidades e Apps](../github/identidades-e-apps.md), [eventos](../github/eventos.md) |
| Telegram | `telegram_state` (offset do long polling, workspace ativo por usuário), `telegram_updates` (updates recebidos, gravados antes de o offset avançar), `bot_messages` (mensagem do bot → workspace e mesa), `glossary_seen` (termos já explicados a cada usuário) | [bot](../telegram/bot.md), [roteamento](../telegram/roteamento.md) |
| Anexos | `attachments` (autor, data, origem, caminho, tamanho) | [anexos e transcrição](../telegram/anexos-e-transcricao.md) |
| Uso e custo | `usage_records`, `usage_limits` | [limites de uso](../plataforma/limites-de-uso.md) |
| Cofre | `secrets` (segredos cifrados) | [cofre](../plataforma/cofre.md) |
| Atualizações | `releases` (versões do Mestre vistas, estado, aprovação), `update_steps` (passos por máquina e trava de atualização), `conformance_results` (por nó, provedor, versão e perfil) | [autoatualização](../plataforma/autoatualizacao.md), [atualização dos CLIs](../provedores/atualizacao-dos-clis.md), [conformidade](../provedores/conformidade.md) |
| Operação | `checklist_alerts`, `healing_log`, `audit_log`, `processed_commands` (idempotência) | [checklist](../plataforma/checklist.md), [camadas](../autocura/camadas.md), [protocolo](../conectividade/protocolo.md) |

## Tabelas principais do nó (proposta)

| Tabela | Conteúdo |
|---|---|
| `schema_migrations` | migrações aplicadas |
| `node_identity` | id do nó, último endereço conhecido do Core, versão de protocolo negociada |
| `terminals` | terminais que este nó roda (registro abaixo) |
| `local_worktrees` | worktrees de mesa neste nó: caminho, branch, último commit sincronizado |
| `account_profiles` | perfis de conta locais (pastas de configuração por provedor) |
| `outbox` | eventos ainda sem ack do Core (enviados de novo ao reconectar) |
| `processed_commands` | comandos do Core já executados (idempotência) |
| `cache_users`, `cache_sheets`, `cache_bases`, `cache_classes` | cópia do essencial para o motor do nó continuar operando sem o Core (mensagem `cache.users` e afins); não serve para login. A cópia de usuários para a página reserva e o Telegram reduzido é outra, a `guardian.replica` do Guardião ([cofre](../plataforma/cofre.md)) |
| `agent_rules` | regras efetivas de cada personagem deste nó ([permissões](../classes/permissoes.md#regras-efetivas)) |
| `checklist_results` | último resultado local do [checklist](../plataforma/checklist.md) |

Ao reconectar: o nó envia o `outbox` e o estado dos `terminals`; o Core responde com o que mudou no cache. Em conflito, vale o Core, exceto o estado real dos processos, que só o nó conhece ([protocolo](../conectividade/protocolo.md)).

## Registro de terminais

Campos: id, personagem (agente), nó, PID, pasta, classe, status, id da sessão do provedor (para a [retomada](../provedores/adaptador.md#retomada-de-sessão)), início e último sinal de vida. O terminal em si (PTY, buffer, log) está em [terminal](terminal.md). Fica em `terminals` no nó que roda o terminal e é espelhado no Core. É com ele que o motor sabe o que reabrir depois de um reinício. Os valores de status e as transições estão em [personagens](../orquestracao/personagens.md).

## Arquivos

| O quê | Onde | Banco guarda |
|---|---|---|
| Classes do workspace e da mesa | `.mestre/classes/`, `.mestre/mesas/<mesa>/classes/` | índice e App vinculado |
| Classes globais | pasta do Core (abaixo) | índice |
| Pergaminhos | Markdown no disco ([pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md)) | índice e conexões |
| Anexos | `.mestre/anexos/` do workspace (fora do git) | registro |
| Histórico dos terminais | arquivos de log com rotação | nada |
| Logs do motor | JSON por linha, com rotação | nada |

Pastas de dados (padrão). Os mesmos caminhos valem para o Core e para os nós de cada sistema; a última coluna diz onde cada uma existe.

| Linux | Windows | Conteúdo | Onde |
|---|---|---|---|
| `/etc/mestre/` | `%ProgramData%\Mestre\config\` | configuração | todas |
| `/etc/mestre/cofre.key` | `%ProgramData%\Mestre\config\cofre.key` (DPAPI de máquina, ACL só de `NT SERVICE\MestreCore`) | chave do [cofre](../plataforma/cofre.md#chave-do-cofre) | Core |
| `/var/lib/mestre/banco/` | `%ProgramData%\Mestre\banco\` | `mestre.db`, o banco do Core | Core |
| `/var/lib/mestre/no/` | `%ProgramData%\Mestre\no\` | `no.db`, o banco do nó, e os dados do motor do nó | todas |
| `/var/lib/mestre/agentes/<id>/` | `%ProgramData%\Mestre\no\agentes\<id>\` | `policy.json`, retrato das regras efetivas para o modo degradado do `mestre hook` ([permissões](../classes/permissoes.md#modo-degradado)); `tmp/`, a `MESTRE_TMP` do personagem ([personagem](../sessao-zero/personagem.md#variáveis-de-ambiente)), apagada no encerramento definitivo | todas |
| `/var/spool/mestre/hook/` | `%ProgramData%\Mestre\no\fila-hook\` | fila local de eventos do `mestre hook` com o motor fora | todas |
| `/var/lib/mestre/backups/` | `%ProgramData%\Mestre\backups\` | backups diários dos bancos da máquina | todas |
| `/var/lib/mestre/workspaces/` | `%ProgramData%\Mestre\workspaces\` | espelhos, clones e worktrees (estrutura em [workspaces](../orquestracao/workspaces.md#espelho-e-clone-principal-no-core) e [mesas](../orquestracao/mesas.md#worktree-por-nó-e-sincronização)) | todas |
| `/var/lib/mestre/contas/` | `%ProgramData%\Mestre\contas\` | perfis de conta | todas |
| `/var/lib/mestre/classes-globais/`, `/var/lib/mestre/modelos/` | `%ProgramData%\Mestre\classes-globais\`, `%ProgramData%\Mestre\modelos\` | classes globais e modelos versionados | Core |
| `/var/lib/mestre/whisper/` | `%ProgramData%\Mestre\whisper\` | `whisper-server` e modelo de transcrição | nó de transcrição |
| `/var/log/mestre/` | `%ProgramData%\Mestre\logs\` | logs do motor e dos terminais | todas |

Donos e acessos (em todos os casos, **outras contas da máquina não têm acesso**):
- **Linux:** dono `mestre`; `workspaces/` e `contas/` no grupo `mestre-agente` ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)). `agentes/` (pasta 0750, `policy.json` 0640, `tmp/` 2770 com escrita do grupo; como todos os personagens usam a conta `mestre-agente`, a separação entre a `MESTRE_TMP` de um e de outro é do despachante, não do sistema operacional, [segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)) e a fila do hook (1730: o grupo `mestre-agente` só cria arquivos) também ficam no grupo `mestre-agente`; o motor valida tudo o que lê da fila. As regras fixas negam às ferramentas dos agentes qualquer acesso a essas pastas ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)).
- **Core Windows:** dono `NT SERVICE\MestreCore`; a conta do usuário do `mestre-node` tem modificação só em `workspaces\` e `contas\`, e é dona de `no\` ([Windows](windows.md#windows-como-core)).
- **Windows (Core e nó):** o instalador cria `%ProgramData%\Mestre\` com a **herança desligada** e remove as entradas herdadas de `%ProgramData%` (`Users`, `Authenticated Users`, `CREATOR OWNER`); ficam só as contas listadas abaixo, SYSTEM e Administradores. Nenhum outro usuário da máquina lê nada ali.
- **Nó Windows:** ACL para a conta do `mestre-node`, SYSTEM e Administradores. `no\agentes\` e `no\fila-hook\` ficam legíveis pela conta do usuário, a mesma dos agentes (risco aceito em [segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)); as regras fixas negam às ferramentas dos agentes o acesso.

## Logs

- Logs do motor: `core.log` (motor do Core) e `no.log` (motor do nó), rotação a cada 20 MB, 10 arquivos (padrão).
- Histórico de cada terminal: `terminais/<terminal-id>.log`, rotação a cada 10 MB, 5 arquivos por terminal; apagado 30 dias depois do encerramento (padrão). A ação "limpar logs antigos" do Guardião segue os mesmos limites.
- Tudo o que vai para log passa pelo mascaramento de segredos ([segurança](seguranca.md)).

## Backup

- Diário, às 03:00 no horário local (padrão), com `VACUUM INTO` (cópia consistente com o banco em uso), seguido de `PRAGMA quick_check` na cópia.
- Mantém os **últimos 7**; o mais antigo é apagado só depois de um backup novo íntegro.
- Vale para o banco do Core e para o banco de cada nó. Falha de backup vira alerta do [checklist](../plataforma/checklist.md).
