# Mensagens do protocolo

> **Assunto:** catálogo dos tipos de mensagem entre a Cidadela, as Torres e os Guardiões, com os campos principais, e os códigos de erro · **Componente:** Todos · **Atualizado:** 2026-10-13

Documento de referência do [protocolo](protocolo.md), que define envelope, versão, ack e transporte. A **fonte dos campos** é a struct Go de cada tipo em `internal/protocol` (o protocolo do Guardião, em `guardian/protocol`); o comentário de cada struct leva o link para a linha desta tabela, e os tipos do painel são gerados delas ([convenções](../desenvolvimento/convencoes.md#código-gerado)). Campos opcionais novos não mudam a versão ([regras](protocolo.md#abertura-versão-e-capacidades)). Todo envelope entre máquinas leva `machine_id` e `epoch` ([época](protocolo.md#época)). Sentido: T = Torre (motor), C = Cidadela (motor).

## Motor da Torre ↔ motor da Cidadela

| `type` | Sentido | Ack | Campos principais do `payload` | Resposta |
|---|---|---|---|---|
| `hello` | T→C | — | `protocol {min, max}`, `version`, `sheet` (a [ficha da máquina](../guilda/ficha-da-maquina.md) inteira), `certificate` (certidão de época que segue), `replica_lsn`, `terminals[]` (estado atual) | `welcome {protocol, citadel_version, citadel_time, certificate}`, ou fechamento `4003`, `4006` ou `4007` |
| `machine.heartbeat` | T→C | não | `cpu_pct`, `mem_pct`, `disk_free_bytes`, `terminals`, `replica_lag_s` | — |
| `machine.sheet` | T→C | sim | os campos da ficha da máquina que mudaram | — |
| `guild.epoch` | ambos | sim | pedido sem campos; resposta com a certidão de época atual (`guild_id`, `epoch`, `citadel_machine_id`, `approved_by`, `approval_id`, `issued_at`, `signature`) | ([certidão](../guilda/replicacao.md#certidão-de-época)) |
| `guild.config` | C→T | sim | máquinas da Guilda (`machine_id`, nome, endereços, prioridade, `can_be_citadel`), lista de revogados | — |
| `guild.leave` | ambos | sim | C→T: a máquina foi removida (apagar réplica, cofre e configuração); T→C: pedido de saída, `reason` (`uninstall`) | ([saída e remoção](../guilda/guilda.md#saída-e-remoção)) |
| `replica.ack` | T→C | não | `lsn` aplicado | — (os registros vão em frames `0x03`; [replicação](../guilda/replicacao.md)) |
| `replica.resync` | T→C | sim | `from_lsn`; pede de novo a partir dali, ou cópia completa sem `from_lsn` | `xfer.begin` com `kind: db-snapshot` ou frames `0x03` |
| `vault.replica` | C→T | sim | só para candidatas: chave do cofre e chave da CA, cifradas para a máquina | — ([cofre só nas candidatas](../guilda/replicacao.md#cofre-só-nas-candidatas)) |
| `vault.drop` | C→T | sim | apagar a cópia do cofre e as tabelas do cofre da réplica | recibo `vault.dropped` |
| `citadel.handover` | C→T | sim | troca planejada: `target_machine_id`, `last_lsn`, `approval_id` | a candidata promove-se e responde com a certidão nova ([troca planejada](../guilda/cidadela-e-torres.md#troca-planejada)) |
| `ack` | ambos | — | `status`: `ok` \| `duplicate` \| `error` | — |
| `error` | ambos | — | `code`, `message`, `retryable` | — |
| `terminal.open` | C→T | sim | `terminal_id`, `agent_id`, `command`, `args[]`, `env {}`, `cwd`, `adapter`; no Operador do PC, `operator {session_id, ends_at}` (a máquina cria o terminal pelo processo da sessão) | `terminal.opened {terminal_id, pid}` ou `error` |
| `terminal.input` | C→T | sim | `terminal_id`, `mode` (`message` pela `TypeMessage` do adaptador \| `raw`), `data` | — (ack basta) |
| `terminal.close` | C→T | sim | `terminal_id`, `mode` (`graceful` \| `kill`) | `terminal.status` com `stopped` |
| `terminal.status` | T→C | sim | `terminal_id`, `state` (`starting`, `idle`, `busy`, `blocked`, `stuck`, `stopped`, `interrupted`, os identificadores de [personagens](../orquestracao/personagens.md)), `reason` (em `blocked`: `permission` \| `quota` \| `input`), `exit_code`, `session_id` | — |
| `terminal.replay` | C→T | não | `terminal_id`, `from` (deslocamento) | frames `0x01` e `terminal.replayed {from, to, lost}` |
| `terminal.screen` | C→T | não | `terminal_id`, `lines` (histórico) | `terminal.screen_result {text, cursor, captured_at}` ([terminal](../arquitetura/terminal.md)) |
| `inbox.deliver` | C→T | sim | `terminal_id`, `item_id`, `text`, `header` | `inbox.result {item_id, status: delivered \| busy}`; `busy` devolve o item à fila na Cidadela ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)) |
| `agent.rules` | C→T | sim | o documento de [regras efetivas](../classes/permissoes.md#regras-efetivas) de um personagem | — |
| `agent.site_login` | C→T | sim | `attempt_id`, `agent_id`, `domains[]`, `login_url`, `username`, `secret`, seletores e verificação de sucesso; a máquina guarda só em memória, registrado no mascarador | `agent.site_login_status` ([login em sites](../plataforma/cofre.md#login-em-sites)) |
| `agent.site_login_status` | T→C | sim | `attempt_id`, `state` (`needs_code` \| `needs_push` \| `needs_captcha` com `xfer_id` da imagem \| `ok` \| `requires_human` \| `failed` com `reason`) | a Cidadela conduz o [segundo fator](../plataforma/cofre.md#segundo-fator-e-captcha); com `ok`, a máquina já gravou o estado na `MESTRE_TMP` |
| `agent.site_login_input` | C→T | sim | `attempt_id`, `value` (código ou resposta do captcha; nunca vai para log) | — |
| `operator.session` | T→C | sim | `session_id`, `state` (`started` \| `locked` \| `stopped_on_pc` \| `session_lost`), `at` | — (a Cidadela encerra a sessão com o motivo; [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc)) |
| `operator.capture` | T→C | sim | `session_id`, `captured_at`, `trigger` (`intervalo` \| `acao`), `event_id`, `xfer_id` da imagem já transferida | — (a Cidadela cifra e guarda; [persistência](../arquitetura/persistencia.md#operador-do-pc)) |
| `agent.event` | T→C | sim | o evento normalizado do [adaptador](../provedores/adaptador.md#formato-interno-normalizado-de-evento) | — |
| `cli.call` | T→C | sim | o pedido do [canal local](../arquitetura/canal-local.md#pedido), mais `caller {agent_id, terminal_id, machine_id}` já verificado | `cli.result` |
| `cli.result` | C→T | sim | a resposta do canal local, inclusive as parciais (`partial: true`) | — |
| `sync.request` / `sync.applied` | ambos | sim | `table_id`, `branch`, `base_commit`, `tip_commit` | ([mesas](../orquestracao/mesas.md)) |
| `xfer.*` | ambos | sim | ver [transferência em partes](protocolo.md#transferência-em-partes) | — |
| `transcribe.request` | C→T | sim | `audio_xfer_id` (WAV já transferido), `language` | `transcribe.result {text, duration_s}` ou `error` ([transcrição](../telegram/anexos-e-transcricao.md)) |
| `cert.renew` | T→C | sim | `role` (`motor`), `csr` | `cert.issued {cert}` ([credenciais](../guilda/guilda.md#credenciais)) |
| `update.apply` | C→T | sim | `release_tag`, `component` (`motor` \| `guardiao`), `xfer_id` | resultado no passo da [autoatualização](../plataforma/autoatualizacao.md) |

## Guardião da Torre ↔ Cidadela

Conexão própria (`/ws/guardian`), com versão própria ([conexão do Guardião](protocolo.md#conexão-do-guardião)).

| `type` | Sentido | Para quê |
|---|---|---|
| `guardian.hello` / `guardian.welcome` | G→C / C→G | abertura e versão do protocolo do Guardião, com a certidão de época |
| `guardian.heartbeat` | G→C | sinal de vida e estado do motor local visto pelo Guardião |
| `guardian.action` / `guardian.result` | C→G / G→C | só ações da [lista fechada](../autocura/guardiao.md) |
| `guardian.replica` | C→G | cópia criptografada do token do bot, da lista de usuários (com o necessário para o login da página reserva) e do estado "travado"; a **única** cópia de usuários que serve login ([cofre](../plataforma/cofre.md)) |
| `cert.renew` / `cert.issued` | G→C / C→G | renovar o certificado do Guardião da máquina (`role`: `guardiao`), inclusive quando os IPs da máquina mudam ([credenciais](../guilda/guilda.md#credenciais)) |
| `citadel.promote` | Guardião da Torre condutora → Guardião da candidata | ordem de promoção depois da aprovação do Dono, com `approval_id` e a certidão anterior; se a candidata é outra máquina, vai pela porta do Guardião dela (7446), com mTLS ([Cidadela e Torres](../guilda/cidadela-e-torres.md#promoção)) |
| `guardian.telegram` | ambos | mensagens do [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) |

## Códigos de erro

Valem para `error.code` em qualquer conexão. `retryable` diz se reenviar a mesma mensagem (mesmo `id`) pode dar certo.

| Código | `retryable` | Quando |
|---|---|---|
| `unknown_type` | não | `type` desconhecido nesta versão |
| `invalid_payload` | não | campo obrigatório ausente ou com tipo errado |
| `too_large` | não | mensagem JSON acima de 1 MiB |
| `not_found` | não | terminal, transferência, mesa ou personagem inexistente |
| `stale_epoch` | não | mensagem com época menor que a do destinatário ([época](protocolo.md#época)) |
| `missing_prerequisite` | não | bundle cujo commit pré-requisito o destino não tem (o remetente manda a branch inteira) |
| `hash_mismatch` | sim | SHA-256 ou `git bundle verify` falhou no fim de uma transferência |
| `busy_terminal` | sim | `terminal.input` cru num terminal que não está livre |
| `unavailable` | sim | recurso local temporariamente fora (ex.: whisper-server reiniciando) |
| `affects_guardian` | não | ação ou script que tocaria o Guardião ([Guardião](../autocura/guardiao.md)) |
| `forbidden` | não | a conexão não pode pedir aquilo (ex.: Guardião pedindo `terminal.open`) |
| `internal` | sim | erro inesperado do destinatário (vai para o log) |
