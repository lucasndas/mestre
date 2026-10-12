# Cofre de segredos

> **Assunto:** onde e como o Core (Linux ou Windows) guarda segredos, quem pode usá-los e a réplica restrita para os Guardiões · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Guardar todos os segredos do Mestre criptografados no Core, com a chave protegida pelo sistema operacional, para que só o motor os use, só na ação que precisa deles, e que nenhum agente, arquivo de projeto, banco em texto puro ou log os contenha.

## Comportamento

### O que fica no cofre

| Segredo | Tipo | Quem usa |
|---|---|---|
| Token do bot do Telegram | `telegram_bot_token` | motor do Core ([bot](../telegram/bot.md)); réplica nos Guardiões |
| Chave privada de cada GitHub App (fixos e por classe) | `github_app_key` | motor do Core, para gerar tokens de instalação ([identidades e Apps](../github/identidades-e-apps.md)) |
| Token de acesso e de renovação de usuário do GitHub (device flow do App "Grão-Mestre") | `github_user_token` | motor do Core, só com delegação ou aprovação daquele usuário ([aprovação no GitHub](../github/aprovacao-no-github.md)) |
| Chave de API de provedor, quando a conta usa chave em vez de login | `provider_api_key` | motor do nó que inicia o personagem daquela conta ([bases e fichas](../orquestracao/bases-e-fichas.md)) |
| Chave privada da CA interna | `ca_key` | motor do Core, ao emitir e renovar certificados ([segurança](../arquitetura/seguranca.md)) |

- As chaves privadas dos nós e dos Guardiões **não** ficam no cofre: nascem e ficam nos nós ([pareamento](../conectividade/pareamento.md)). O Core guarda só os certificados.
- Senhas e PINs não ficam no cofre: o banco guarda só hashes ([login](../painel/login.md), [usuários e níveis](../telegram/usuarios-e-niveis.md#pin-pessoal)).

### Formato (proposta)

- Tabela `secrets` no banco do Core ([persistência](../arquitetura/persistencia.md)): `name`, `type`, `ciphertext`, `nonce`, `key_version`, `fingerprint`, `created_at`, `updated_at`, `last_used_at`.
- **Criptografia:** AES-256-GCM, com nonce aleatório de 96 bits a cada gravação e dados associados (AAD) = `name` + `type` + `key_version`. Assim um valor copiado para outra linha não decifra.
- **Impressão (`fingerprint`):** HMAC-SHA256 do valor com uma chave derivada da chave do cofre (HKDF), mostrando 8 caracteres. Serve para comparar sem revelar o valor.

### Chave do cofre

- 32 bytes aleatórios gerados na instalação do Core ([instalação e atualização](instalacao-e-atualizacao.md)).
- **Padrão no Linux:** arquivo `/etc/mestre/cofre.key`, dono `mestre` (conta do motor, [componentes](../arquitetura/componentes.md#serviços-e-contas-padrão)), modo 0600, pasta 0700. O motor se recusa a abrir o cofre se o arquivo tiver permissão mais aberta ou outro dono, e o [checklist](checklist.md) acusa como grave (LIN-05).
- **Padrão no Windows:** arquivo `%ProgramData%\Mestre\config\cofre.key` com a chave cifrada por **DPAPI com escopo de máquina** (`CryptProtectData` com `CRYPTPROTECT_LOCAL_MACHINE`) e **ACL só para `NT SERVICE\MestreCore`**, sem herança. O escopo de máquina deixa a chave presa a este Windows (uma cópia do arquivo não abre em outra máquina); quem impede outras contas locais de ler é a ACL, porque o DPAPI de máquina decifra para qualquer processo local que consiga ler o arquivo. O motor confere dono e ACL antes de abrir, como no Linux, e o checklist acusa como grave (WIN-14) ([Windows](../arquitetura/windows.md#windows-como-core)).
- **Opção "chaveiro do sistema" (Linux):** guardar a chave com `systemd-creds` (cifrada pelo TPM2 ou pela chave do host) e entregá-la ao serviço com `LoadCredentialEncrypted=`. O motor lê de `$CREDENTIALS_DIRECTORY`. É opcional: a instalação sempre começa com o arquivo 0600; o [checklist](checklist.md) (LIN-07) detecta suporte (`systemd-creds has-tpm2`) e oferece a troca, feita só com aprovação.
- **Backup da chave:** os backups do banco ([persistência](../arquitetura/persistencia.md#backup)) **não** levam a chave. Sem ela, um backup restaurado perde os segredos (é preciso cadastrá-los de novo). A instalação orienta guardar uma cópia da chave fora do servidor ([instalar](../operacao/instalar.md)); no Windows, a cópia sai por `mestre core export-key` ([componentes](../arquitetura/componentes.md#mestre)), porque o arquivo protegido por DPAPI não abre em outra máquina.
- **Troca da chave:** o Dono pede no painel (com PIN); o motor gera a chave nova, decifra e cifra de novo todos os segredos numa transação, incrementa `key_version` e só então substitui o arquivo.

### Uso

- Só o motor do Core lê o cofre, por uma função interna que recebe o nome do segredo e a finalidade (ex.: "gerar token de instalação do App QA para o PR #42").
- Cada leitura entra na auditoria (nome, finalidade, ação) e atualiza `last_used_at`. O valor lido é registrado no mascarador na hora ([segurança](../arquitetura/seguranca.md#mascaramento-em-logs)).
- Nenhuma rota da API, comando da CLI ou mensagem para agente devolve um valor. O painel mostra só nome, tipo, impressão e datas, e permite trocar ou remover com PIN ([administração](../painel/administracao.md)).
- **Chave de API de provedor:** é a única que sai do Core. Vai cifrada pela conexão do nó só no momento de iniciar o personagem e entra só no ambiente do processo do provedor; nunca vai para arquivo. **Risco aceito:** o agente consegue ler o próprio ambiente. Por isso o Mestre recomenda login por assinatura (perfil de conta) em vez de chave.

### Réplica para os Guardiões

- **Conteúdo, e só isto:** o token do bot e a lista de usuários (id, nome, login, nível, workspaces, ID do Telegram, hash da senha, hash do PIN, ativo ou suspenso). Junto, sem ser segredo, vão o estado "travado" e o último `update_id` do bot ([Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md)).
- **Para quem:** os Guardiões dos **nós** e, no [modo máquina única](../autocura/camadas.md#modo-máquina-única), o Guardião do Core (são eles que servem a [página reserva](../autocura/pagina-reserva.md) e o modo reduzido). Nunca vão chaves do GitHub, tokens de usuários, chaves de provedor ou a chave da CA.
- **Transporte:** mensagem `guardian.replica` pela conexão própria do Guardião, com mTLS ([protocolo](../conectividade/protocolo.md#conexão-do-guardião)); para o Guardião do Core, pelo canal local motor ↔ Guardião ([componentes](../arquitetura/componentes.md#canais-locais-padrão)). Cada réplica tem um número de versão crescente; o Guardião confirma e guarda só a mais nova.
- **Quando:** ao conectar e a cada mudança (usuário criado, alterado, suspenso, removido; token trocado; travar e destravar), em até 10 s (padrão).
- **Em repouso no Guardião:** AES-256-GCM com uma chave local do Guardião, protegida pelo sistema: DPAPI com escopo de máquina e ACL só de SYSTEM no Windows (o Guardião roda como LocalSystem); arquivo 0600 de root no Linux.
- **Nó revogado:** ao receber o fechamento "credencial revogada" ([protocolo](../conectividade/protocolo.md#heartbeat-e-reconexão)), o Guardião apaga a réplica. Como um nó roubado pode não obedecer, o painel recomenda trocar o token do bot ao revogar um nó.

## Regras e erros

- Chave ausente ou errada (falha na verificação do GCM) → o cofre fica fechado, as funções que dependem dele param com erro claro, o motor continua o resto e manda alerta grave. Nunca há tentativa de "seguir sem cofre" usando valores em texto.
- Segredo inexistente → a ação que pediu falha com o nome do segredo que falta (nunca com o valor).
- Gravar segredo pelo painel exige Dono e PIN ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)); fluxos automáticos (manifesto de App, device flow) gravam pelo motor, com auditoria.

## Critérios de aceite

- **CA-COFRE-01** · *rápida* — **Dado** um segredo gravado, **quando** leio a tabela `secrets` direto, **então** o valor não aparece em nenhuma coluna.
- **CA-COFRE-02** · *rápida* — **Dado** o arquivo de chave com modo 0644, **quando** o motor abre o cofre, **então** recusa, as ações com segredo falham com erro claro e sai alerta grave.
- **CA-COFRE-03** · *rápida* — **Dado** um `ciphertext` copiado para outra linha, **quando** o motor tenta decifrar, **então** a verificação falha.
- **CA-COFRE-04** · *rápida* — **Dado** a montagem de uma réplica, **quando** ela é serializada, **então** contém só os campos listados acima e nenhum segredo do GitHub, de provedor ou da CA.
- **CA-COFRE-05** · *completa* — **Dado** um usuário suspenso no painel, **quando** passam 10 s, **então** o Guardião do nó tem a réplica nova e a página reserva recusa o login dele.
- **CA-COFRE-06** · *rápida* — **Dado** uma troca de chave interrompida no meio (falha simulada), **quando** o motor reinicia, **então** todos os segredos continuam decifráveis com a chave antiga.
- **CA-COFRE-07** · *rápida* — **Dado** a API do painel, **quando** listo os segredos, **então** a resposta tem só nome, tipo, impressão e datas.
- **CA-COFRE-08** · *completa* — **Dado** o Core Windows com `cofre.key` legível também pela conta do usuário, **quando** o `mestre-core` abre o cofre, **então** recusa, as ações com segredo falham com erro claro e sai alerta grave; **dado** a ACL só de `NT SERVICE\MestreCore`, **então** abre, e um processo do `mestre-node` não consegue ler o arquivo.

## Depende de

- [Segurança](../arquitetura/seguranca.md)
- [Persistência](../arquitetura/persistencia.md)
- [Componentes](../arquitetura/componentes.md) e [Windows](../arquitetura/windows.md)
- [Protocolo](../conectividade/protocolo.md)
- [Pareamento](../conectividade/pareamento.md)
- [Login](../painel/login.md)
