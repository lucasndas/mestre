# Administração: rotas e implementação

> **Assunto:** implementação das telas de administração que não são de regras (Guilda, usuários, segredos, auditoria, classes, Apps, bases e fichas, perfis e consumo): arquivos, rotas, níveis, PIN, recusas e auditoria · **Componente:** Painel · **Atualizado:** 2026-10-27

Documento de referência. O que cada tela mostra e permite está em [administração](administracao.md#telas) e o desenho de cada uma, em [telas de Configurações](administracao-telas.md); as telas de regras têm o próprio [documento de implementação](administracao-regras.md). As rotas e os arquivos abaixo não mudam com o redesenho das telas.

### Implementação: Guilda

`internal/panel/routes_guild.go` (tipos em `types_guild.go`) e `internal/app/guildhost.go`, sobre o `guild.Citadel` que age; a tela é `web/src/screens/admin/Guild.tsx`. Tudo é do Senhor da Guilda (`manage_machines`, e `guild_settings` no fuso), com o PIN na própria requisição. O tópico `guild:main` do `/ws/ui` manda o snapshot e, a cada 3 s com alguém seguindo, a visão de novo se mudou. Máquina fora da rede local é a que não tem IP numa rede das interfaces da Cidadela (`guild.LocalNetworks`).

| Rota | O que faz |
|---|---|
| `GET /api/guild`, `GET /api/guild/machines/{id}/removal` | a visão (época, fuso, `single`, máquinas, pedidos) e o aviso da remoção (quantos e quais agentes rodam lá) |
| `POST /api/guild/joins/{id}/decide`, `…/machines/{id}/remove` (`confirm`), `…/exclusion`, `…/pass-citadel`, `PUT …/priority`, `PUT /api/guild/timezone` | aceitar ou recusar, remover, excluir, troca planejada (`Engine.Handover`, em segundo plano), prioridade, fuso; recusas são `refused` com `reason` (`excluded_machine`, `outside_local_network`, `citadel_protected`, `not_online`, `no_lock`, `not_revealed`, `handover_in_progress`…; uma troca planejada por vez) |

Auditoria `panel.guild.*` (`entry_decided`, `machine_removed`, `exclusion`, `citadel_pass_started`, `citadel_pass_refused`, `priority`, `timezone`) com `user_id` e antes/depois, sem PIN. "GitHub fora" e "Telegram parado" vêm da cerca (`Fence`: `lock-down` e `TelegramStopped`).

### Implementação: usuários, segredos e auditoria

Código: `internal/panel/routes_users.go`, `routes_secrets.go` e `routes_audit.go` (tipos em `types_admin.go`, gerados em `web/src/gen/panel.ts`) e `web/src/screens/admin/`. O nível vem da [tabela](../telegram/usuarios-e-niveis.md#níveis-e-capacidades) (`forbidden_level` com `need` e `have`; o painel diz "exige o nível Senhor da Guilda; o seu é Administrador"); toda ação que muda algo pede o PIN na própria requisição.

| Rota | Quem | O que faz |
|---|---|---|
| `GET /api/users` | Senhor da Guilda | usuários, campanhas e a passagem de posto que espera |
| `POST /api/users`, `PATCH /api/users/{id}` | Senhor da Guilda, PIN | cadastra (devolve o código de primeiro acesso, uma vez) e edita nome, nível, campanhas e login do GitHub; campanha desconhecida é `invalid_workspace`, um segundo Senhor da Guilda é `owner_exists` |
| `POST /api/users/{id}/suspend`, `/reactivate`, `/remove`, `/pairing-code`, `/access-code` | Senhor da Guilda, PIN | suspender (sessões acabam), reativar, remover, código do Telegram e códigos de primeiro acesso e de redefinição; o Senhor da Guilda não age sobre si (`self_action`) |
| `GET`, `POST`, `DELETE /api/transfer` e `POST /api/transfer/accept` | Senhor da Guilda, PIN; quem aceita, com o PIN dele | passa o posto de Senhor da Guilda, cancela e aceita (as sessões dos dois acabam); é a tela `/transferencia` e a faixa no alto do painel |
| `GET /api/me/github`, `POST /api/me/github/begin` e `/poll` | Aprovador ou acima | "Conectar GitHub": o device flow de `github.Personal`; conta diferente da cadastrada é `github_login_mismatch` (o motor já descartou o token) e as demais recusas têm código próprio (`github_denied`, `github_expired`, `github_no_login`, `github_flow_off`, `github_not_ready`) |
| `GET /api/secrets` | Senhor da Guilda | nome, tipo, impressão, datas, metadados e o aviso de validade (`expiring`, `expired`, `stale`, com os dias), nunca valor |
| `POST /api/secrets/site`, `PUT` e `DELETE /api/secrets/{name}` | Senhor da Guilda, PIN | cadastra credencial de site, substitui e apaga; o painel só mexe nos tipos `site_credential` e `provider_api_key` (os que o próprio Mestre guarda são `secret_managed`) |
| `POST /api/recovery-key` | Senhor da Guilda, PIN | `{acknowledged: true}` (o aviso dos backups antigos, lido antes do PIN; sem ele `ack_required`) gera a chave, que só vem nessa resposta; `Vault.RewrapRecovery` reenvelopa a chave do cofre e troca a pública da Guilda de uma vez |
| `GET /api/audit` | Administrador ou acima | `user`, `action` (início do nome), `from` e `to` (dia no fuso da Guilda), `workspace`, `cursor`, `limit`; o Senhor da Guilda vê tudo, os outros só as linhas das campanhas deles, e o que o Operador do PC fez só como fato |

Auditoria desta área (`panel.user.*`, `panel.access_code.created`, `panel.pairing_code.created`, `panel.transfer.*`, `panel.secret.*`, `panel.recovery_key.renewed`, `panel.github.*`): `user_id` de quem fez, `target` ou `secret` e `before`/`after` com o que a tela mostra, sem valor, código nem PIN. O filtro "o quê" da tela escolhe entre estas famílias.

### Implementação: classes, Apps, bases e fichas, perfis e consumo

Código: `internal/panel/routes_{classes,apps,presets,profiles,usage}.go` (tipos em `types_manage.go`; o motor entra por `Panel.SetManage`, `internal/app/panelmanage.go`) e `web/src/screens/admin/`. Tudo com o nível conferido no servidor; mudança pede o PIN.

| Rota | Quem | O que faz |
|---|---|---|
| `GET /api/classes` · `POST /api/classes/proposals/{id}/approve` | Administrador; PIN | classes global, da campanha e da mesa com o App e o estado dele; propostas do Grão-Mestre (`request`) e mudanças feitas por fora (`disk`), todas "não testada" até o M7; a faixa de 90 Apps vem em `apps`; "Aprovar" leva o hash visto e a `Library.Approve` confere nível, hash, problemas e teto (`proposal_stale`, `proposal_invalid`) |
| `GET /api/github/apps` · `PUT /api/github/apps/others` · `POST /api/github/apps/{name}/link` | Administrador lê; Senhor da Guilda muda; PIN no link | "N de 100 Apps"; `next_fixed` diz ao Senhor da Guilda qual App fixo (Grão-Mestre, depois Mestre) ainda não tem linha no registro, e o primeiro `POST …/link` desse nome a cria; quem não é Senhor da Guilda vê só os Apps fixos e os instalados nos repositórios das suas campanhas; os pedidos de criação e o "novo link" são do Senhor da Guilda; o link sai nesta resposta, uma vez, porque só o hash do token fica guardado |
| `GET /github/apps/novo/{token}` (e `/github/manifest/callback`, `/github/apps/instalado`) | Senhor da Guilda | páginas de `github.Web` montadas no painel; outro nível vê "exige Senhor da Guilda" e nenhum formulário (CA-GH-APPS-04) |
| `GET /api/presets` · `PUT`/`DELETE /api/presets/{bases,sheets}/{name}` · `GET /api/presets/export` · `POST /api/presets/import[/plan]` | Senhor da Guilda; PIN ao mudar | cada base e ficha é editada como YAML (o formato da exportação); importar valida o arquivo inteiro, mostra os conflitos e aplica tudo ou nada (CA-ADM-05) |
| `GET`/`POST /api/profiles` · `DELETE /api/profiles/{name}` · `POST …/default` · `POST …/advance` | Senhor da Guilda; PIN, menos `advance` | perfis desta máquina e o passo da configuração guiada; sem os passos da máquina (instalador, terminal de login, conformidade: M7) `advance` responde `unavailable` |
| `GET /api/usage?days=` | qualquer nível; o servidor limita às campanhas do usuário | consumo por dia, campanha, mesa, missão, personagem e conta (só o Senhor da Guilda), com os limites, o que falta e o vazio quando o provedor não deu o dado |

## Depende de

- [Administração](administracao.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Guilda](../guilda/guilda.md)
- [Cofre](../plataforma/cofre.md)
