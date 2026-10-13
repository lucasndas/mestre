# Persistência

> **Assunto:** banco principal na Cidadela, réplica nas Torres e banco local de máquina, o que vai no banco e o que vai em arquivo, tabelas principais, terminais, logs e backup · **Componente:** Todos · **Atualizado:** 2026-10-13

## Princípios

- Toda máquina da Guilda tem **dois bancos SQLite**, em modo WAL, com migrações versionadas:
  - o **banco da Guilda** (`mestre.db`): na Cidadela é o **banco principal**, a fonte da verdade; em cada Torre é uma **réplica só de leitura**, mantida pela [replicação](../guilda/replicacao.md). O mesmo arquivo vira principal numa promoção, sem cópia;
  - o **banco local de máquina** (`maquina.db`): o que só aquela máquina sabe (terminais que roda, worktrees, perfis de conta, [fila de saída](../guilda/cidadela-e-torres.md#cidadela-fora-e-fila-de-saída)). Nunca é replicado.
- Uma Torre continua operando com a Cidadela fora lendo a réplica (usuários, fichas, bases, classes, regras efetivas); ao reconectar, a Cidadela decide e a replicação traz o resultado.
- O que precisa ser lido ou versionado por pessoas fica em **arquivo** (classes, pergaminhos, anexos, logs); o banco guarda só o índice e os vínculos.
- Os segredos do [cofre](../plataforma/cofre.md) nunca vão para o banco em texto puro: ficam cifrados; as tabelas do cofre vão a toda Torre menos a [máquina excluída](../guilda/guilda.md#máquina-excluída) ([replicação](../guilda/replicacao.md#cofre-em-toda-máquina)). Exceção: o segredo do aperto de mão de cada terminal (`terminals.handshake_secret`) fica em claro no banco local de máquina, que só a conta do motor lê; ele vale só para aquele `mestre-pty` e nunca é replicado ([registro de terminais](#registro-de-terminais)).
- O Guardião não usa o banco do motor; o que ele guarda está em [Guardião](../autocura/guardiao.md).
- Cada [instância](componentes.md#instâncias) tem os próprios bancos e pastas.

## Banco: configuração

- Biblioteca `modernc.org/sqlite` ([ADR 0001](../decisoes/0001-stack-go.md)).
- `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000` (padrão). Com `NORMAL`, um commit sobrevive à queda do processo, mas numa queda de energia pode se perder até o próximo checkpoint; quando algo fora do banco vai depender do commit (a troca da chave do [cofre](../plataforma/cofre.md#chave-do-cofre)), a transação roda com `synchronous=FULL`, que sincroniza o WAL no commit, e a conexão volta ao `NORMAL` depois.
- **Uma conexão de escrita** e um grupo de conexões só de leitura. Numa Torre, a conexão de escrita do banco da Guilda é só da replicação.
- **Migrações:** arquivos SQL numerados (`NNNN_nome.sql`, de 1 em diante, sem buracos), embutidos no binário, um conjunto para o banco da Guilda (`internal/store/guild/migrations/`) e outro para o banco local de máquina (`internal/store/machine/migrations/`); aplicados na subida, cada um na própria transação junto com a linha em `schema_migrations` (`version`, `name`, `applied_at`), de modo que uma migração que falha deixa o banco na última versão inteira. Só para frente: não há migração de volta. Numa Torre, as migrações do banco da Guilda chegam pela replicação, nunca aplicadas localmente (o banco é aberto como réplica).
- **Banco mais novo que o binário:** o motor recusa abrir, sem alterar o arquivo, quando `schema_migrations` tem versão maior que a última migração embutida (vale também para a réplica).
- **Datas:** colunas `*_at` guardam inteiros em milissegundos Unix, em UTC, vindos do relógio injetável.
- **Migrações só aditivas** dentro de uma versão maior (acrescentar tabela ou coluna; remover só numa versão seguinte), para a versão anterior do motor ainda abrir o banco depois de uma volta de versão. Antes de migrar, o motor grava, se houver migração pendente num banco que já tinha esquema, uma cópia `pre-migracao-<banco>-<versão>.db` na pasta de backups (`VACUUM INTO`; `<banco>` é `mestre` ou `maquina`, porque os dois bancos dividem a pasta; `<versão>` é a versão do esquema antes de migrar, a que a restauração devolve; banco novo não tem o que copiar) e confere a cópia com `PRAGMA quick_check`: cópia que não passa impede a migração ([instalação e atualização](../plataforma/instalacao-e-atualizacao.md)). É essa cópia que a ação `rollback` com restauração do banco usa: rara, nunca automática, só com Dono ou Administrador e PIN ([Guardião](../autocura/guardiao.md)).

## Tabelas principais do banco da Guilda (proposta)

Cada marco cria só as tabelas de que precisa, em migração nova ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)). Existem hoje: `secrets` no banco da Guilda e `terminals` no banco local de máquina (M1); as demais abaixo são proposta.

Nomes de tabela em inglês (são identificadores de código). Colunas detalhadas ficam nas migrações; o significado de cada dado, no documento da funcionalidade.

| Grupo | Tabelas | Dono do comportamento |
|---|---|---|
| Guilda e máquinas | `guild` (`guild_id`, época atual e certidão, fuso da Guilda, chave pública de recuperação do cofre), `machines` (ficha da máquina, prioridade, se é excluída, versão, estado), `machine_heartbeats`, `join_requests`, `machine_credentials` (identificador e revogação, não a chave), `account_profiles`, `outbox` (mensagens com ack ainda sem confirmação, por máquina), `replication_state` (último `lsn` confirmado por Torre) | [Guilda](../guilda/guilda.md), [ficha da máquina](../guilda/ficha-da-maquina.md), [replicação](../guilda/replicacao.md), [protocolo](../conectividade/protocolo.md), [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Usuários | `users`, `user_workspaces`, `panel_sessions`, `telegram_pairing_codes` | [usuários e níveis](../telegram/usuarios-e-niveis.md), [login](../painel/login.md) |
| Workspaces e mesas | `workspaces`, `tables` (mesas: épico, branch, estado), `table_worktrees` (máquina, caminho, último commit sincronizado), `bundle_transfers` | [workspaces](../orquestracao/workspaces.md), [mesas](../orquestracao/mesas.md) |
| Presets e classes | `bases`, `sheets`, `classes` (índice: nome, escopo, caminho, App), `templates` (versões dos modelos) | [bases e fichas](../orquestracao/bases-e-fichas.md), [classes](../classes/classes.md), [sessão zero do personagem](../sessao-zero/personagem.md) |
| Personagens e canvas | `characters`, `terminals`, `character_state_log` (transições de estado, com motivo e horário), `connections`, `notes` (índice de pergaminhos), `tomes`, `canvas_layout`, `grimoires` | [personagens](../orquestracao/personagens.md), [conexões](../orquestracao/conexoes.md), [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md), [grimórios](../orquestracao/grimorios.md) |
| Missões | `quests`, `quest_steps` (com issue/sub-issue), `step_runs` | [missões](../orquestracao/missoes.md) |
| Esperas e mensagens | `waits`, `inbox_messages` (com a origem da mensagem de usuário), `outbound_messages` (respostas guardadas para canal indisponível) | [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) |
| Rituais | `rituals`, `ritual_runs` | [rituais](../orquestracao/rituais.md) |
| Regras e aprovações | `decrees`, `approval_requests`, `approval_responses` | [decretos](../telegram/decretos.md), [fluxo de aprovação](../aprovacoes/fluxo.md) |
| GitHub | `github_apps`, `app_installations`, `github_objects` (objetos que o motor criou: mesa/etapa ↔ issue, sub-issue, PR, comentário, com o marcador de operação; [GitHub na troca](../guilda/cidadela-e-torres.md#github-depois-da-troca)), `github_poll_state` (ETag e cursor por recurso), `github_seen_events` (chaves de eventos já vistos) | [identidades e Apps](../github/identidades-e-apps.md), [eventos](../github/eventos.md) |
| Telegram | `telegram_state` (offset do long polling, workspace ativo por usuário), `telegram_updates` (updates recebidos, gravados antes de o offset avançar), `bot_messages` (mensagem do bot → workspace e mesa), `glossary_seen` (termos já explicados a cada usuário) | [bot](../telegram/bot.md), [roteamento](../telegram/roteamento.md) |
| Anexos | `attachments` (autor, data, origem, caminho, tamanho) | [anexos e transcrição](../telegram/anexos-e-transcricao.md) |
| Uso e custo | `usage_records`, `usage_limits` | [limites de uso](../plataforma/limites-de-uso.md) |
| Cofre | `secrets` (segredos cifrados, inclusive a chave privada da CA da Guilda; em toda máquina menos a excluída; nas credenciais de site, os metadados `owner_user`, `classes`, `domains`, `kind` e `expires_at` fora da parte cifrada), `site_login_attempts` (estado de cada login, sem código nem senha) | [cofre](../plataforma/cofre.md) |
| Atualizações | `releases` (versões do Mestre vistas, estado, aprovação), `update_steps` (passos por máquina e trava de atualização), `conformance_results` (por máquina, provedor, versão e perfil) | [autoatualização](../plataforma/autoatualizacao.md), [atualização dos CLIs](../provedores/atualizacao-dos-clis.md), [conformidade](../provedores/conformidade.md) |
| Operador do PC | `operator_sessions`, `operator_captures` (abaixo) | [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) |
| Operação | `checklist_alerts`, `healing_log`, `audit_log` (inclusive as transações descartadas numa troca de Cidadela), `processed_commands` (idempotência) | [checklist](../plataforma/checklist.md), [camadas](../autocura/camadas.md), [protocolo](../conectividade/protocolo.md), [Cidadela e Torres](../guilda/cidadela-e-torres.md) |

## Tabelas principais do banco local de máquina (proposta)

| Tabela | Conteúdo |
|---|---|
| `schema_migrations` | migrações aplicadas |
| `machine_identity` | `machine_id`, `guild_id`, papel, maior certidão de época vista, endereços conhecidos da Cidadela, versão de protocolo negociada |
| `terminals` | terminais que esta máquina roda (registro abaixo) |
| `local_worktrees` | worktrees de mesa nesta máquina: caminho, branch, último commit sincronizado |
| `account_profiles` | perfis de conta locais (pastas de configuração por provedor) |
| `outbox` | a **fila de saída**: eventos, `done`, resultados e pedidos da CLI que esperam a Cidadela, cada um com id idempotente; reenviados ao reconectar ([Cidadela fora](../guilda/cidadela-e-torres.md#cidadela-fora-e-fila-de-saída)) |
| `processed_commands` | comandos da Cidadela já executados (idempotência) |
| `agent_rules` | regras efetivas de cada personagem desta máquina ([permissões](../classes/permissoes.md#regras-efetivas)) |
| `checklist_results` | último resultado local do [checklist](../plataforma/checklist.md) |

Ao reconectar: a máquina envia o `outbox` e o estado dos `terminals`; a Cidadela decide e a replicação traz o que mudou. Em conflito, vale a Cidadela, exceto o estado real dos processos, que só a máquina conhece ([protocolo](../conectividade/protocolo.md)). Na Cidadela, o banco local de máquina guarda o mesmo que numa Torre, para os personagens que rodam nela.

## Registro de terminais

Campos: id, personagem (agente), máquina, pasta, classe, status, id da sessão do provedor (para a [retomada](../provedores/adaptador.md#retomada-de-sessão)), caminho do socket, segredo do aperto de mão e versão do `mestre-pty`, PID do `mestre-pty` com a identidade do início do processo (contra PID reaproveitado), início e último sinal de vida. A linha é gravada **antes** de o `mestre-pty` iniciar, e o PID logo depois; o agente só começa depois disso. O `mestre-pty`, o aperto de mão e a reconciliação na volta do motor estão em [terminal](terminal.md#mestre-pty). Fica em `terminals` no banco local da máquina que roda o terminal e é espelhado no banco da Guilda sem o segredo. Os valores de status e as transições estão em [personagens](../orquestracao/personagens.md).

## Arquivos

| O quê | Onde | Banco guarda |
|---|---|---|
| Classes do workspace e da mesa | `.mestre/classes/`, `.mestre/mesas/<mesa>/classes/` | índice e App vinculado |
| Classes globais | pasta da Cidadela (abaixo), copiada às Torres | índice |
| Pergaminhos | Markdown no disco ([pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md)) | índice e conexões |
| Anexos | `.mestre/anexos/` do workspace (fora do git) | registro |
| Histórico dos terminais | arquivos de log com rotação | nada |
| Logs do motor | JSON por linha, com rotação | nada |

Pastas de dados (padrão). `<inst>` é o nome da [instância](componentes.md#instâncias) (padrão `principal`). Os mesmos caminhos valem para toda máquina de cada sistema; a última coluna diz onde cada pasta tem conteúdo. "Cidadela e Torres" quer dizer: original na Cidadela, cópia replicada nas Torres; "menos a excluída", que a [máquina excluída](../guilda/guilda.md#máquina-excluída) não recebe ([replicação](../guilda/replicacao.md)).

| Linux | Windows | Conteúdo | Onde |
|---|---|---|---|
| `/etc/mestre/<inst>/` | `%ProgramData%\Mestre\<inst>\config\` | configuração (inclusive o bloco de portas da instância) | todas |
| `/etc/mestre/<inst>/cofre.key` | `%ProgramData%\Mestre\<inst>\config\cofre.key` (DPAPI de máquina, ACL só de `NT SERVICE\Mestre`) | a chave de dados do [cofre](../plataforma/cofre.md#chave-do-cofre), protegida pela chave da máquina | Cidadela e Torres, menos a excluída |
| `/var/lib/mestre/<inst>/banco/` | `%ProgramData%\Mestre\<inst>\banco\` | `mestre.db`, o banco da Guilda: principal na Cidadela, réplica nas Torres; `cofre-envelope.bin`, a chave de dados cifrada para a chave pública de recuperação (menos na excluída); `descartado-<época>.db` depois de uma [troca de Cidadela](../guilda/replicacao.md#perda-na-troca) | todas |
| `/var/lib/mestre/<inst>/maquina/` | `%ProgramData%\Mestre\<inst>\maquina\` | `maquina.db`, o banco local de máquina, e os dados locais do motor | todas |
| `/var/lib/mestre/<inst>/agentes/<id>/` | `%ProgramData%\Mestre\<inst>\maquina\agentes\<id>\` | `policy.json`, retrato das regras efetivas para o modo degradado do `mestre hook` ([permissões](../classes/permissoes.md#modo-degradado)); `tmp/`, a `MESTRE_TMP` do personagem ([personagem](../sessao-zero/personagem.md#variáveis-de-ambiente)), apagada no encerramento definitivo | todas |
| — | `%ProgramData%\Mestre\<inst>\maquina\mestre-agente.senha` (DPAPI de máquina, ACL só de SYSTEM) | senha da conta `mestre-agente`, lida pelo `mestre-launcher` e pelo Guardião ([Windows](windows.md#conta-dos-agentes-mestre-agente)) | Windows |
| `/var/spool/mestre/<inst>/hook/` | `%ProgramData%\Mestre\<inst>\maquina\fila-hook\` | fila local de eventos do `mestre hook` com o motor fora | todas |
| `/var/lib/mestre/<inst>/backups/` | `%ProgramData%\Mestre\<inst>\backups\` | backups diários dos bancos da máquina | todas |
| `/var/lib/mestre/<inst>/workspaces/` | `%ProgramData%\Mestre\<inst>\workspaces\` | por workspace, `espelho.git` (na Cidadela, o principal; nas Torres, a réplica), os worktrees `mesas/<mesa>/` saídos do espelho local e, na Cidadela, o clone principal `principal/`, replicado como arquivos ([worktrees na troca](../guilda/cidadela-e-torres.md#worktrees-e-espelho-na-troca), [workspaces](../orquestracao/workspaces.md), [mesas](../orquestracao/mesas.md#worktree-por-máquina-e-sincronização)) | todas |
| `/var/lib/mestre/<inst>/contas/` | `%ProgramData%\Mestre\<inst>\contas\` | perfis de conta | todas |
| `/var/lib/mestre/<inst>/classes-globais/`, `/var/lib/mestre/<inst>/modelos/` | `%ProgramData%\Mestre\<inst>\classes-globais\`, `%ProgramData%\Mestre\<inst>\modelos\` | classes globais e modelos versionados | Cidadela e Torres |
| `/var/lib/mestre/<inst>/operador/` | `%ProgramData%\Mestre\<inst>\operador\` | capturas de tela cifradas do Operador do PC (abaixo) | Cidadela e Torres, menos a excluída |
| — | `%ProgramData%\Mestre\<inst>\maquina\operador\` | capturas ainda não confirmadas pela Cidadela | máquina com o Operador |
| `/var/lib/mestre/<inst>/whisper/` | `%ProgramData%\Mestre\<inst>\whisper\` | `whisper-server` e modelo de transcrição | máquina com `transcricao` |
| `/run/mestre/<inst>/` (`cli.sock`; `pty/`, 0700) | `%ProgramData%\Mestre\<inst>\run\` (`cli.sock`), `%ProgramData%\Mestre\<inst>\pty\` | sockets do [canal local](canal-local.md) e dos `mestre-pty` ([terminal](terminal.md#socket-e-aperto-de-mão)) | todas |
| `/var/log/mestre/<inst>/` | `%ProgramData%\Mestre\<inst>\logs\` | logs do motor e dos terminais | todas |

Donos e acessos (em todos os casos, **outras contas da máquina não têm acesso**):
- **Linux:** dono `mestre`; `workspaces/` e `contas/` no grupo `mestre-agente` ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)), menos `config`, `info/` e `hooks/` de cada `espelho.git` e do `.git` do clone principal, que o motor usa e o `mestre-agente` não pode alterar nem trocar ([git do motor](seguranca.md#git-do-motor)). `agentes/` (pasta 0750, `policy.json` 0640, `tmp/` 2770 com escrita do grupo; como todos os personagens usam a conta `mestre-agente`, a separação entre a `MESTRE_TMP` de um e de outro é do despachante, não do sistema operacional, [segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)) e a fila do hook (1730: o grupo `mestre-agente` só cria arquivos) também ficam no grupo `mestre-agente`; o motor valida tudo o que lê da fila. As regras fixas negam às ferramentas dos agentes qualquer acesso a essas pastas ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)).
- **Windows, conta de serviço:** `NT SERVICE\Mestre` é dona de `%ProgramData%\Mestre\<inst>\` e de todas as pastas de dados da instância ([Windows](windows.md#serviços-no-windows)).
- **Windows, `mestre-agente`** ([Windows](windows.md#conta-dos-agentes-mestre-agente)): modificação só em `workspaces\` (menos `config`, `info\` e `hooks\` dos repositórios, só leitura), `contas\` e `maquina\agentes\<id>\tmp\`; leitura só em `maquina\agentes\<id>\policy.json`; em `maquina\fila-hook\`, só criar arquivos (sem ler nem listar); em `pty\`, só criar arquivos, e os sockets criados lá herdam a ACL que tira do dono (`OWNER RIGHTS`) tudo menos ler permissões; em `run\cli.sock`, ler e escrever (conectar). Nada em `config\`, `banco\`, `backups\`, `logs\` nem no resto de `maquina\`. Como todos os personagens usam essa conta, a separação entre a `MESTRE_TMP` de um e de outro é do despachante, como no Linux; o motor valida tudo o que lê da fila.
- **Windows:** o instalador cria `%ProgramData%\Mestre\` com a **herança desligada** e remove as entradas herdadas de `%ProgramData%` (`Users`, `Authenticated Users`, `CREATOR OWNER`); ficam só as contas listadas acima, SYSTEM e Administradores, sem `Users`, `Authenticated Users` nem `Everyone`. Nenhum outro usuário da máquina, nem o que instalou, lê nada ali sem elevar. Exceção: `config\cofre.key` nasce com ACL protegida (sem herança) só de `NT SERVICE\Mestre`, que é a dona, sem entrada para SYSTEM nem Administradores ([cofre](../plataforma/cofre.md#chave-do-cofre)).

## Operador do PC

- **`operator_sessions`:** uma linha por ativação: pedido de aprovação, quem aprovou (Dono ou Grão-Mestre e o decreto de delegação), mesa, missão, personagem, máquina, usuário do Windows, tarefa, duração pedida, início, fim previsto, fim real e motivo (`prazo`, `done`, `encerrado_por`, `parar_no_pc`, `travar`, `tela_bloqueada`, `queda_da_sessao`). As ações vão completas para `audit_log`, com o id da sessão ([bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc)).
- **`operator_captures`:** sessão, horário, gatilho (`intervalo` ou `acao`, com o id do evento do hook), mesa, missão, caminho e SHA-256 do arquivo cifrado.
- **Arquivos:** `operador/<sessão>/<horário>.png.enc` na pasta da Cidadela (copiada às Torres não excluídas), cifrado com AES-256-GCM por uma chave própria guardada no [cofre](../plataforma/cofre.md) (segredo `operator_capture_key`); só a pasta do motor, sem acesso de `mestre-agente`. O painel decifra só para o Dono ([administração](../painel/administracao.md#operador-do-pc)).
- **Retenção:** 30 dias depois do fim da sessão (padrão); a limpeza apaga arquivos e linhas de `operator_captures`, e `operator_sessions` fica. Na máquina do Operador, `maquina\operador\` só guarda a captura até a Cidadela confirmar o recebimento, e os backups do banco não levam as imagens.

## Logs

- Logs do motor: `mestre.log`, rotação a cada 20 MB, 10 arquivos (padrão).
- Histórico de cada terminal: `terminais/<terminal-id>.log`, rotação a cada 10 MB, 5 arquivos por terminal; apagado 30 dias depois do encerramento (padrão). A ação "limpar logs antigos" do Guardião segue os mesmos limites.
- Tudo o que vai para log passa pelo mascaramento de segredos ([segurança](seguranca.md)).

## Backup

- Diário, às 03:00 no [fuso da Guilda](../painel/administracao.md#fuso-da-guilda) (padrão), com `VACUUM INTO` (cópia consistente com o banco em uso), seguido de `PRAGMA quick_check` na cópia.
- Mantém os **últimos 7**; o mais antigo é apagado só depois de um backup novo íntegro.
- Vale para o banco principal na Cidadela e para o banco local de cada máquina; a réplica de uma Torre não tem backup próprio (a Cidadela é a fonte). Falha de backup vira alerta do [checklist](../plataforma/checklist.md).
- **Cofre no backup da Cidadela:** os segredos vão no banco, já cifrados, e junto vai `cofre-envelope.bin`, a chave de dados cifrada para a chave pública de recuperação; o arquivo da chave protegido pelo sistema não vai. Sem a chave de recuperação, de que o Mestre guarda só a parte pública, o cofre do backup não abre ([cofre na troca](../guilda/replicacao.md#cofre-na-troca), [backup e chave de recuperação](../plataforma/cofre.md#backup-e-chave-de-recuperação)). As capturas do Operador do PC não vão no backup.

## Critérios de aceite

- **CA-PERSIST-01** · *rápida* — **Dado** um banco novo, **quando** é aberto, **então** a conexão de escrita e as de leitura estão em WAL, com `foreign_keys=ON` e `busy_timeout=5000`, a escrita com `synchronous=NORMAL` e uma só conexão, e as de leitura recusam gravar.
- **CA-PERSIST-02** · *rápida* — **Dado** um conjunto de migrações, **quando** o banco é aberto, **então** elas são aplicadas em ordem e registradas em `schema_migrations` com a data do relógio injetável; reabrir não aplica de novo e um binário com migração a mais aplica só a nova.
- **CA-PERSIST-03** · *rápida* — **Dado** uma migração que falha no meio, **quando** o banco é aberto, **então** a abertura falha e nada dela fica: nem a versão registrada, nem as tabelas que ela criou.
- **CA-PERSIST-04** · *rápida* — **Dado** um banco com versão maior que a do binário, **quando** é aberto (como principal ou réplica), **então** a abertura é recusada com erro de esquema mais novo e o arquivo não muda.
- **CA-PERSIST-05** · *rápida* — **Dado** uma migração pendente num banco que já tinha esquema, **quando** é aberto, **então** `pre-migracao-<banco>-<versão>.db` existe, passa no `quick_check` e tem os dados anteriores, e as cópias de `mestre.db` e `maquina.db` na mesma pasta não se sobrescrevem; num banco novo, ou sem migração pendente, nada é gravado.
- **CA-PERSIST-06** · *rápida* — **Dado** um banco aberto como réplica, **quando** há migrações no binário, **então** nenhuma é aplicada, nem num banco novo nem num mais antigo.
- **CA-PERSIST-07** · *rápida* — **Dado** uma transação de escrita aberta que já gravou páginas no disco, **quando** uma conexão de leitura consulta com `busy_timeout` de 1 ms, **então** ela não espera e só vê o que foi confirmado.
- **CA-PERSIST-08** · *rápida* — **Dado** os conjuntos embutidos da Guilda e da máquina, **quando** são aplicados a um banco vazio, **então** chegam à última versão, com as tabelas do marco e sem violação de chave estrangeira.
