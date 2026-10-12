# Mensagens do protocolo

> **Assunto:** catálogo dos tipos de mensagem entre Core, nós e Guardiões, com os campos principais, e os códigos de erro · **Componente:** Todos · **Atualizado:** 2026-10-12

Documento de referência do [protocolo](protocolo.md), que define envelope, versão, ack e transporte. A **fonte dos campos** é a struct Go de cada tipo em `internal/protocol` (o protocolo do Guardião, em `guardian/protocol`); o comentário de cada struct leva o link para a linha desta tabela, e os tipos do painel são gerados delas ([convenções](../desenvolvimento/convencoes.md#código-gerado)). Campos opcionais novos não mudam a versão ([regras](protocolo.md#abertura-versão-e-capacidades)).

## Motor do nó ↔ motor do Core

| `type` | Sentido | Ack | Campos principais do `payload` | Resposta |
|---|---|---|---|---|
| `hello` | N→C | — | `protocol {min, max}`, `version`, `node_id`, `name`, `os`, `arch`, `local`, `gpu`, `transcricao`, `operador` (com `operador_reason` quando falso e o Operador está habilitado: `sem_usuario`, `tela_bloqueada`, `uac`, `tarefa`), `tools {nome: versão}`, `account_profiles[]`, `terminals[]` (estado atual) | `welcome {protocol, core_version, core_time}` ou fechamento `4003` |
| `node.heartbeat` | N→C | não | `cpu_pct`, `mem_pct`, `disk_free_bytes`, `terminals` | — |
| `node.capabilities` | N→C | sim | os campos de capacidade do `hello` que mudaram | — |
| `ack` | ambos | — | `status`: `ok` \| `duplicate` \| `error` | — |
| `error` | ambos | — | `code`, `message`, `retryable` | — |
| `terminal.open` | C→N | sim | `terminal_id`, `agent_id`, `command`, `args[]`, `env {}`, `cwd`, `adapter`; no Operador do PC, `operator {session_id, ends_at}` (o nó cria o terminal pelo processo da sessão) | `terminal.opened {terminal_id, pid}` ou `error` |
| `terminal.input` | C→N | sim | `terminal_id`, `mode` (`message` pela `TypeMessage` do adaptador \| `raw`), `data` | — (ack basta) |
| `terminal.close` | C→N | sim | `terminal_id`, `mode` (`graceful` \| `kill`) | `terminal.status` com `stopped` |
| `terminal.status` | N→C | sim | `terminal_id`, `state` (`starting`, `idle`, `busy`, `blocked`, `stuck`, `stopped`, `interrupted`, os identificadores de [personagens](../orquestracao/personagens.md)), `reason` (em `blocked`: `permission` \| `quota` \| `input`), `exit_code`, `session_id` | — |
| `terminal.replay` | C→N | não | `terminal_id`, `from` (deslocamento) | frames `0x01` e `terminal.replayed {from, to, lost}` |
| `terminal.screen` | C→N | não | `terminal_id`, `lines` (histórico) | `terminal.screen_result {text, cursor, captured_at}` ([terminal](../arquitetura/terminal.md)) |
| `inbox.deliver` | C→N | sim | `terminal_id`, `item_id`, `text`, `header` | `inbox.result {item_id, status: delivered \| busy}`; `busy` devolve o item à fila no Core ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)) |
| `agent.rules` | C→N | sim | o documento de [regras efetivas](../classes/permissoes.md#regras-efetivas) de um personagem | — |
| `agent.site_login` | C→N | sim | `attempt_id`, `agent_id`, `domains[]`, `login_url`, `username`, `secret`, seletores e verificação de sucesso; o nó guarda só em memória, registrado no mascarador | `agent.site_login_status` ([login em sites](../plataforma/cofre.md#login-em-sites)) |
| `agent.site_login_status` | N→C | sim | `attempt_id`, `state` (`needs_code` \| `needs_push` \| `needs_captcha` com `xfer_id` da imagem \| `ok` \| `requires_human` \| `failed` com `reason`) | o Core conduz o [segundo fator](../plataforma/cofre.md#segundo-fator-e-captcha); com `ok`, o nó já gravou o estado na `MESTRE_TMP` |
| `agent.site_login_input` | C→N | sim | `attempt_id`, `value` (código ou resposta do captcha; nunca vai para log) | — |
| `operator.session` | N→C | sim | `session_id`, `state` (`started` \| `locked` \| `stopped_on_pc` \| `session_lost`), `at` | — (o Core encerra a sessão com o motivo; [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc)) |
| `operator.capture` | N→C | sim | `session_id`, `captured_at`, `trigger` (`intervalo` \| `acao`), `event_id`, `xfer_id` da imagem já transferida | — (o Core cifra e guarda; [persistência](../arquitetura/persistencia.md#operador-do-pc)) |
| `agent.event` | N→C | sim | o evento normalizado do [adaptador](../provedores/adaptador.md#formato-interno-normalizado-de-evento) | — |
| `cli.call` | N→C | sim | o pedido do [canal local](../arquitetura/canal-local.md#pedido), mais `caller {agent_id, terminal_id, node_id}` já verificado | `cli.result` |
| `cli.result` | C→N | sim | a resposta do canal local, inclusive as parciais (`partial: true`) | — |
| `sync.request` / `sync.applied` | ambos | sim | `table_id`, `branch`, `base_commit`, `tip_commit` | ([mesas](../orquestracao/mesas.md)) |
| `xfer.*` | ambos | sim | ver [transferência em partes](protocolo.md#transferência-em-partes) | — |
| `transcribe.request` | C→N | sim | `audio_xfer_id` (WAV já transferido), `language` | `transcribe.result {text, duration_s}` ou `error` ([transcrição](../telegram/anexos-e-transcricao.md)) |
| `cache.users` | C→N | sim | usuários (id, nome, nível por workspace), sem PIN nem senha: cache para o motor do nó operar sem o Core; **não serve login** | — |
| `cert.renew` | N→C | sim | `role` (`motor`), `csr` | `cert.issued {cert}` ([pareamento](pareamento.md#credenciais)) |
| `update.apply` | C→N | sim | `release_tag`, `component` (`motor` \| `guardiao`), `xfer_id` | resultado no passo da [autoatualização](../plataforma/autoatualizacao.md) |

## Guardião do nó ↔ Core

Conexão própria (`/ws/guardian`), com versão própria ([conexão do Guardião](protocolo.md#conexão-do-guardião)).

| `type` | Sentido | Para quê |
|---|---|---|
| `guardian.hello` / `guardian.welcome` | G→C / C→G | abertura e versão do protocolo do Guardião |
| `guardian.heartbeat` | G→C | sinal de vida e estado do motor local visto pelo Guardião |
| `guardian.action` / `guardian.result` | C→G / G→C | só ações da [lista fechada](../autocura/guardiao.md) |
| `guardian.replica` | C→G | cópia criptografada do token do bot, da lista de usuários (com o necessário para o login da página reserva) e do estado "travado"; a **única** cópia de usuários que serve login ([cofre](../plataforma/cofre.md)) |
| `cert.renew` / `cert.issued` | G→C / C→G | renovar os certificados do Guardião do nó (`role`: `guardiao` ou `pagina_reserva`), inclusive quando os IPs da máquina mudam ([pareamento](pareamento.md#credenciais)) |
| `guardian.telegram` | ambos | mensagens do [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) |

## Códigos de erro

Valem para `error.code` em qualquer conexão. `retryable` diz se reenviar a mesma mensagem (mesmo `id`) pode dar certo.

| Código | `retryable` | Quando |
|---|---|---|
| `unknown_type` | não | `type` desconhecido nesta versão |
| `invalid_payload` | não | campo obrigatório ausente ou com tipo errado |
| `too_large` | não | mensagem JSON acima de 1 MiB |
| `not_found` | não | terminal, transferência, mesa ou personagem inexistente |
| `missing_prerequisite` | não | bundle cujo commit pré-requisito o destino não tem (o remetente manda a branch inteira) |
| `hash_mismatch` | sim | SHA-256 ou `git bundle verify` falhou no fim de uma transferência |
| `busy_terminal` | sim | `terminal.input` cru num terminal que não está livre |
| `unavailable` | sim | recurso local temporariamente fora (ex.: whisper-server reiniciando) |
| `affects_guardian` | não | ação ou script que tocaria o Guardião ([Guardião](../autocura/guardiao.md)) |
| `forbidden` | não | a conexão não pode pedir aquilo (ex.: Guardião pedindo `terminal.open`) |
| `internal` | sim | erro inesperado do destinatário (vai para o log) |
