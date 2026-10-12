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
| Credencial de site: endereço da página de login, usuário, senha (ou senha de app, token, chave de API) e, se preciso, os seletores dos campos e a verificação de sucesso; metadados em [credenciais de site](#credenciais-de-site) | `site_credential` | motor do Core, para fazer o login e entregar ao personagem só o estado de sessão ([login em sites](#login-em-sites)) |
| Chave das capturas de tela do Operador do PC (AES-256, gerada quando o primeiro nó habilita o Operador) | `operator_capture_key` | motor do Core, ao gravar e ao mostrar ao Dono as capturas ([persistência](../arquitetura/persistencia.md#operador-do-pc)) |
| Chave privada da CA interna | `ca_key` | motor do Core, ao emitir e renovar certificados ([segurança](../arquitetura/seguranca.md)) |

- As chaves privadas dos nós e dos Guardiões **não** ficam no cofre: nascem e ficam nos nós ([pareamento](../conectividade/pareamento.md)). O Core guarda só os certificados.
- Senhas e PINs não ficam no cofre: o banco guarda só hashes ([login](../painel/login.md), [usuários e níveis](../telegram/usuarios-e-niveis.md#pin-pessoal)).

### Formato (proposta)

- Tabela `secrets` no banco do Core ([persistência](../arquitetura/persistencia.md)): `name`, `type`, `ciphertext`, `nonce`, `key_version`, `fingerprint`, os metadados de `site_credential` (`owner_user`, `classes`, `domains`, `kind`, `expires_at`, fora da parte cifrada, para o motor decidir sem decifrar), `created_at`, `updated_at`, `last_used_at`.
- **Criptografia:** AES-256-GCM, com nonce aleatório de 96 bits a cada gravação e dados associados (AAD) = `name` + `type` + `key_version`. Assim um valor copiado para outra linha não decifra.
- **Impressão (`fingerprint`):** HMAC-SHA256 do valor com uma chave derivada da chave do cofre (HKDF), mostrando 8 caracteres. Serve para comparar sem revelar o valor.

### Chave do cofre

- 32 bytes aleatórios gerados na instalação do Core ([instalação e atualização](instalacao-e-atualizacao.md)).
- **Padrão no Linux:** arquivo `/etc/mestre/cofre.key`, dono `mestre` (conta do motor, [componentes](../arquitetura/componentes.md#serviços-e-contas-padrão)), modo 0600, pasta 0700. O motor se recusa a abrir o cofre se o arquivo tiver permissão mais aberta ou outro dono, e o [checklist](checklist.md) acusa como grave (LIN-05).
- **Padrão no Windows:** arquivo `%ProgramData%\Mestre\config\cofre.key` com a chave cifrada por **DPAPI com escopo de máquina** (`CryptProtectData` com `CRYPTPROTECT_LOCAL_MACHINE`) e **ACL só para `NT SERVICE\MestreCore`**, sem herança. O escopo de máquina deixa a chave presa a este Windows (uma cópia do arquivo não abre em outra máquina); quem impede outras contas locais de ler é a ACL, porque o DPAPI de máquina decifra para qualquer processo local que consiga ler o arquivo. O motor confere dono e ACL antes de abrir, como no Linux, e o checklist acusa como grave (WIN-14) ([Windows](../arquitetura/windows.md#windows-como-core)).
- **Opção "chaveiro do sistema" (Linux):** guardar a chave com `systemd-creds` (cifrada pelo TPM2 ou pela chave do host) e entregá-la ao serviço com `LoadCredentialEncrypted=`. O motor lê de `$CREDENTIALS_DIRECTORY`. É opcional: a instalação sempre começa com o arquivo 0600; o [checklist](checklist.md) (LIN-07) detecta suporte (`systemd-creds has-tpm2`) e oferece a troca, feita só com aprovação.
- **Troca da chave:** o Dono pede no painel (com PIN); o motor gera a chave nova, decifra e cifra de novo todos os segredos numa transação, incrementa `key_version`, refaz o envelope de recuperação (abaixo) e só então substitui o arquivo.

### Backup e chave de recuperação

- **No backup diário:** a tabela `secrets` vai no backup do banco ([persistência](../arquitetura/persistencia.md#backup)) já cifrada, e junto vai `cofre-envelope.bin`: a chave do cofre cifrada (AES-256-GCM) com uma chave derivada da **chave de recuperação** por Argon2id (sal aleatório e parâmetros no próprio arquivo). O arquivo da chave protegido pelo sistema (0600, `systemd-creds` ou DPAPI) nunca vai no backup.
- **Chave de recuperação:** 32 bytes aleatórios, mostrados **uma única vez** na instalação do Core, em base32 (52 caracteres em grupos de 4), para o usuário guardar fora do Mestre. A instalação só termina depois de ele redigitar dois grupos sorteados ([instalação](instalacao-e-atualizacao.md)). O Mestre não a guarda em lugar nenhum (disco, banco, log, cofre): guarda só um verificador (SHA-256 da chave derivada, dentro do envelope), para conferir a digitação na restauração. **Sem ela, o backup do cofre é inútil**: a instalação e a tela de backup dizem isso.
- **Restaurar** (mesma máquina ou outra, Linux ou Windows): `mestre install core --restore <pasta do backup>` pede a chave de recuperação, confere o verificador, abre o envelope e grava a chave do cofre protegida pelo sistema da máquina nova. É o único caminho de restauração do cofre nos dois sistemas.
- **Nova chave de recuperação:** o Dono gera pelo painel, com PIN; ela aparece uma vez, e o envelope dos backups seguintes passa a usá-la. Backups antigos continuam exigindo a chave anterior, e o painel avisa isso antes de confirmar.

### Uso

- Só o motor do Core lê o cofre, por uma função interna que recebe o nome do segredo e a finalidade (ex.: "gerar token de instalação do App QA para o PR #42").
- Cada leitura entra na auditoria (nome, finalidade, ação) e atualiza `last_used_at`. O valor lido é registrado no mascarador na hora ([segurança](../arquitetura/seguranca.md#mascaramento-em-logs)).
- Nenhuma rota da API, comando da CLI ou mensagem para agente devolve um valor. O painel mostra só nome, tipo, impressão, datas e metadados, e permite substituir ou remover com PIN; um valor salvo nunca é mostrado de novo, nem ao Dono ([administração](../painel/administracao.md)).
- **Chave de API de provedor:** sai do Core, como a credencial de site no login (abaixo). Vai cifrada pela conexão do nó só no momento de iniciar o personagem e entra só no ambiente do processo do provedor; nunca vai para arquivo. **Risco aceito:** o agente consegue ler o próprio ambiente. Por isso o Mestre recomenda login por assinatura (perfil de conta) em vez de chave.

### Credenciais de site

- **Cadastro só pelo painel** (HTTPS, login e PIN; Dono, [administração](../painel/administracao.md)); nunca pelo Telegram nem pela conversa com o Grão-Mestre. Segredo mandado por esses canais é tratado antes da IA, sempre que possível, com o mesmo mascarador dos logs ([segurança](../arquitetura/seguranca.md#mascaramento-em-logs)):
  1. **Formato conhecido** (`ghp_`, `github_pat_`, `sk-`, `xoxb-`, blocos PEM, JWT, texto de alta entropia): o motor troca o trecho por `[segredo removido]` antes de entregar ao Grão-Mestre;
  2. **Frase de senha** ("a senha é", "minha senha", "password:"): o motor retém a mensagem inteira; o Grão-Mestre recebe só o aviso, sem o conteúdo;
  3. **Senha em texto livre que só o Grão-Mestre percebe:** ele responde com `mestre reply --redact-source` ([CLI](../cli/comunicacao.md#mestre-reply)), e o motor faz o mesmo que nos outros casos.
  Nos três casos, o motor apaga a mensagem original (`deleteMessage` no Telegram; no painel, ela não é gravada), ela não entra em histórico, caixa de entrada nem log, a auditoria guarda só "tentativa de envio de segredo bloqueada" (usuário, canal, horário), e o Grão-Mestre recusa, pede o cadastro pelo painel e **recomenda trocar a senha**, que já passou por um canal sem criptografia ponta a ponta e, no caso 3, pela IA ([segurança do canal](../telegram/seguranca-do-canal.md#segredos)).
- **Só de escrita:** depois de salva, a credencial nunca é mostrada de novo, nem ao Dono; só substituir ou apagar, com PIN. Nenhuma API do painel nem comando da CLI a devolve: só os metadados.
- **Metadados obrigatórios**, fora da parte cifrada: `owner_user` (quem responde ao segundo fator e recebe avisos; padrão: o Dono), `classes` que podem usá-la, `domains`, `kind` (`senha_principal` \| `senha_de_app` \| `token` \| `chave_api`) e `expires_at` (opcional). No cadastro, o painel sugere usar senha de app, token ou chave de API em vez da senha principal.
- **Quem usa:** só personagem cuja classe libera a credencial em `network.credentials` ([permissões](../classes/permissoes.md#formato-do-permissionsyaml)) **e** está em `classes` da credencial. Cada uso vai para a auditoria (usuário dono, classe, personagem, site, horário), nunca o valor.
- **Validade:** o motor avisa o `owner_user` e o Dono (Telegram e painel) 7 dias antes de `expires_at` (padrão) e, nas credenciais sem validade, lembra de trocá-las a cada 90 dias (padrão) desde a última substituição.

### Login em sites

Um personagem nunca usa as sessões de navegador do usuário nem vê a senha de um site ([segurança](../arquitetura/seguranca.md#navegação-web-e-login-em-sites)). Para cada credencial que ele pode usar (acima):
1. Ao iniciar ou retomar o personagem e a cada tarefa nova entregue a ele, o motor do Core lê a credencial (com auditoria e finalidade) e a envia ao **motor do nó do personagem**, só para aquele login, pela WSS (mensagem `agent.site_login`, [mensagens](../conectividade/mensagens.md)). O nó a guarda só em memória, registrada no mascarador, e nunca em disco.
2. O motor do nó abre um navegador headless que ele controla, com a conta do `mestre-node` (nunca `mestre-agente`) e um perfil temporário na pasta de dados do nó: **o mesmo motor de navegador e a mesma versão** do MCP de navegador do personagem (ex.: o Chromium do Playwright MCP daquele perfil), pelo protocolo CDP.
3. Faz o login na página da credencial e confere o sucesso. Exporta os cookies e o armazenamento local **só dos domínios da credencial**, no formato de estado de sessão do Playwright (`storageState`), fecha o navegador, apaga o perfil temporário e descarta a credencial.
4. Grava o estado em `sessao-sites.json` na `MESTRE_TMP` do personagem ([personagem](../sessao-zero/personagem.md#variáveis-de-ambiente)); o adaptador passa o caminho ao MCP de navegador dele (`--storage-state`). O arquivo é apagado quando o personagem termina a tarefa (`done`) ou é encerrado.

- A senha nunca chega ao personagem: nem no ambiente, nem em argumentos, nem em arquivo, nem no terminal. No nó, existe só na memória do motor durante o login.
- O estado de sessão dá acesso ao site enquanto a sessão valer; por isso vale só para os domínios da credencial e some no fim da tarefa.
- Login que falha (senha errada, verificação de sucesso não confere, segundo fator ou captcha não resolvidos abaixo) → o personagem não recebe estado daquele domínio e recebe na caixa de entrada "login em <domínio> falhou: <motivo>"; o Dono recebe alerta.
- Site que recusa a sessão transferida (amarrada a navegador, IP ou máquina): `login_requires_human`, como o caso (d) abaixo.

### Segundo fator e captcha

Quando a página de login pede um segundo passo, o motor trata nesta ordem, sempre no navegador de login do nó; o motor do Core conduz a conversa e repassa ao nó o que o usuário responder. Quem responde é o **dono da credencial**: o `owner_user` dela ou o Dono; respostas de qualquer outro usuário são ignoradas. A conversa é do **motor** pelo Telegram, numa mensagem direta a essa pessoa ([bot](../telegram/bot.md#mensagens-de-saída)); nada passa pelo Grão-Mestre nem por agente.

| Caso | O que o motor faz | Espera (padrão, configurável) |
|---|---|---|
| (a) **código** (SMS, e-mail, app autenticador) | pede "código de <site> para <usuário>"; a resposta é capturada pelo motor, digitada no navegador e apagada | 2 min |
| (b) **aprovação no app do celular** (push) | avisa "aprove o login em <site> no seu celular" e confere a página de login até ela passar | 2 min |
| (c) **captcha de imagem com texto** | manda a imagem como foto; a resposta segue o caminho do código | 2 min |
| (d) **captcha interativo** (clicar em imagens, caixa do reCAPTCHA) ou falha que não se encaixa acima | marca `login_requires_human` e põe um evento na caixa de entrada do Grão-Mestre com o site e a tarefa; o Grão-Mestre propõe o [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) pela ativação normal (`ativacao_operador`) | — |

- **Retenção zero do código e da resposta do captcha:** são registrados no mascarador assim que chegam; não vão para log, para a caixa de entrada, para o histórico de conversa nem para agente; a auditoria guarda só "código recebido de <usuário>" e o horário. Depois de usar, o bot apaga a mensagem do usuário e a do pedido (`deleteMessage`). Isto não abre exceção à regra de não enviar segredos pelo Telegram: o motor só **recebe** ([segurança do canal](../telegram/seguranca-do-canal.md#segredos)).
- **Espera estourada:** o motor desiste (`login_timeout`), avisa o usuário e põe o motivo na caixa de entrada do Grão-Mestre; o personagem segue sem o estado daquele domínio.
- **Estados da tentativa de login:** `em_andamento` → `aguardando_codigo` | `aguardando_celular` | `aguardando_captcha` → `concluido` | `login_timeout` | `login_requires_human` | `falhou`. Ficam em `site_login_attempts` ([persistência](../arquitetura/persistencia.md)), sem o código.
- **Nunca contorna:** com `login_requires_human`, sem nó com o Operador habilitado ou sem a aprovação, a tarefa que precisava do site fica bloqueada e o Grão-Mestre explica o motivo ao usuário ([Grão-Mestre](../orquestracao/grao-mestre.md#instruções-do-sistema)).

### Réplica para os Guardiões

- **Conteúdo, e só isto:** o token do bot e a lista de usuários (id, nome, login, nível, workspaces, ID do Telegram, hash da senha, hash do PIN, ativo ou suspenso). Junto, sem ser segredo, vão o estado "travado" e o último `update_id` do bot ([Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md)).
- **Para quem:** os Guardiões dos **nós** e, no [modo máquina única](../autocura/camadas.md#modo-máquina-única), o Guardião do Core (são eles que servem a [página reserva](../autocura/pagina-reserva.md) e o modo reduzido). Nunca vão chaves do GitHub, tokens de usuários, chaves de provedor ou a chave da CA.
- **Transporte:** mensagem `guardian.replica` pela conexão própria do Guardião, com mTLS ([protocolo](../conectividade/protocolo.md#conexão-do-guardião)); para o Guardião do Core, pelo canal local motor ↔ Guardião ([componentes](../arquitetura/componentes.md#canais-locais-padrão)). Cada réplica tem um número de versão crescente; o Guardião confirma e guarda só a mais nova.
- **Quando:** ao conectar e a cada mudança (usuário criado, alterado, suspenso, removido; token trocado; travar e destravar), em até 10 s (padrão).
- **Em repouso no Guardião:** AES-256-GCM com uma chave local do Guardião, protegida pelo sistema: DPAPI com escopo de máquina e ACL só de SYSTEM no Windows (o Guardião roda como LocalSystem); arquivo 0600 de root no Linux.
- **Não confundir com `cache.users`:** é a cópia de usuários que o **motor** de cada nó guarda no próprio banco para continuar operando com o Core fora; é só cache de operação e não serve login ([persistência](../arquitetura/persistencia.md), [protocolo](../conectividade/protocolo.md)). Login com o Core fora, na página reserva e no modo reduzido, usa só esta réplica do Guardião.
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
- **CA-COFRE-07** · *rápida* — **Dado** uma credencial de site salva, **quando** listo e abro os segredos por qualquer rota da API do painel ou comando da CLI, inclusive como Dono, **então** a resposta tem só nome, tipo, impressão, datas e metadados, nunca o valor; trocar ou apagar pede PIN.
- **CA-COFRE-08** · *pesada* — **Dado** o Core Windows instalado com `cofre.key` legível também por `NT SERVICE\MestreNode`, **quando** o `mestre-core` abre o cofre, **então** recusa, as ações com segredo falham com erro claro e sai alerta grave; **dado** a ACL só de `NT SERVICE\MestreCore`, **então** abre, e um processo do `mestre-node` não consegue ler o arquivo (MVP-13).
- **CA-COFRE-09** · *completa* — **Dado** uma credencial de site de teste, um site falso local com formulário de login e um personagem do agente falso cuja classe libera a credencial, **quando** o personagem nasce e navega com o estado recebido, **então** o site falso o vê logado, e a senha não aparece no ambiente do processo, nos argumentos, em nenhum arquivo da `MESTRE_TMP`, do worktree ou do perfil de conta, nem no histórico do terminal; depois do `done`, `sessao-sites.json` não existe.
- **CA-COFRE-10** · *rápida* — **Dado** um site falso que pede código, a credencial com `owner_user` Ana e o Telegram falso, **quando** Bia responde ao pedido com um código, **então** a resposta é ignorada e o motor continua esperando a de Ana ou do Dono.
- **CA-COFRE-11** · *rápida* — **Dado** o mesmo site e Ana respondendo `482913`, **quando** o login conclui, **então** o motor digitou o código no navegador de login, `482913` não aparece em log, auditoria (só "código recebido de Ana" e o horário), caixa de entrada, histórico de conversa nem em mensagem a agente, e as duas mensagens foram apagadas pelo `deleteMessage`.
- **CA-COFRE-12** · *rápida* — **Dado** um pedido de código sem resposta e o relógio falso, **quando** passam 2 min, **então** a tentativa vira `login_timeout`, Ana é avisada e o Grão-Mestre recebe o motivo na caixa de entrada.
- **CA-COFRE-13** · *rápida* — **Dado** um site falso com captcha de imagem, **quando** o login chega a ele, **então** o Telegram falso recebe a imagem como foto para o dono da credencial, e a resposta dele é digitada pelo motor, com a mesma retenção zero do código.
- **CA-COFRE-14** · *rápida* — **Dado** um site falso com captcha interativo, **quando** o login chega a ele, **então** a tentativa vira `login_requires_human`, o Grão-Mestre recebe o evento com o site e a tarefa, nenhum pedido de código sai pelo Telegram e o motor não tenta outro caminho.
- **CA-COFRE-15** · *rápida* — **Dado** o Telegram falso, **quando** o Dono escreve "cadastre o site x, a senha é Abc123!", **então** o motor apaga a mensagem, nada dela entra em log, histórico ou caixa de entrada, a auditoria tem só "tentativa de envio de segredo bloqueada", e o Grão-Mestre recebe só o aviso; **quando** chega `token ghp_abc…`, **então** o Grão-Mestre recebe `token [segredo removido]` e o autor, a recomendação de troca; **quando** o agente falso do Grão-Mestre responde a outra mensagem com `reply --redact-source`, **então** ela é apagada e sai do histórico, e o autor recebe a recusa com a recomendação de troca.
- **CA-COFRE-16** · *rápida* — **Dado** uma credencial com `classes: [qa]` e as classes QA e Dev liberando-a em `network.credentials`, **quando** personagens das duas iniciam, **então** só o QA recebe o estado de sessão, e a auditoria registra o uso (dono, classe, personagem, site, horário) sem o valor.
- **CA-COFRE-17** · *rápida* — **Dado** uma credencial com `expires_at` e outra sem validade, e o relógio falso, **quando** faltam 7 dias para a primeira e passam 90 dias da última troca da segunda, **então** o `owner_user` e o Dono recebem um aviso de cada, no Telegram e no painel, uma vez só.
- **CA-COFRE-18** · *completa* — **Dado** um personagem num nó com o Playwright MCP numa versão e um site falso local, **quando** o login acontece, **então** ele roda no motor desse nó, com o mesmo navegador e versão do MCP, e a credencial não aparece em nenhum arquivo do nó.
- **CA-COFRE-19** · *completa* — **Dado** uma instalação do Core numa pasta temporária, **quando** ela termina, **então** a chave de recuperação foi mostrada uma vez e não aparece em disco, banco nem log; **dado** o backup dela numa máquina nova, **quando** restauro sem a chave ou com uma errada, **então** falha com a explicação; com a chave certa, os segredos abrem.
- **CA-COFRE-20** · *rápida* — **Dado** o Dono no painel, **quando** pede uma nova chave de recuperação, **então** é exigido o PIN, o aviso sobre backups antigos aparece antes de confirmar, e o envelope do backup seguinte só abre com a chave nova.

## Depende de

- [Segurança](../arquitetura/seguranca.md)
- [Persistência](../arquitetura/persistencia.md)
- [Componentes](../arquitetura/componentes.md) e [Windows](../arquitetura/windows.md)

## Verificar na implementação

- **Verificar na implementação:** o site aceita a sessão transferida por `storageState` do navegador de login do nó para o MCP do personagem (alguns sites amarram a sessão a navegador, IP ou máquina). **Padrão:** login no mesmo nó e com o mesmo navegador e versão do MCP, e o estado passado ao Playwright MCP por `--storage-state`. **Se falhar** (o site recusa a sessão): `login_requires_human`, e o Grão-Mestre propõe o Operador do PC pela ativação normal. **Onde:** suíte pesada, contra um site de teste próprio (dublê) que amarra a sessão ao user agent, no `TestMVP14_PCOperator`; e à mão, no primeiro uso real de cada site, com o resultado na auditoria.
- **Verificar na implementação:** o bot apaga, em chat privado, a mensagem do usuário com o código (ou com uma senha bloqueada) e a do pedido. **Padrão:** `deleteMessage` logo depois de usar o código (a Bot API permite apagar mensagens recebidas em chat privado até 48 h depois). **Se falhar:** a mensagem fica; o bot pede ao usuário que a apague e registra a falha na auditoria, sem o código. **Onde:** suíte rápida com o Telegram falso (CA-COFRE-11) e, à mão, na sessão zero, com o bot real; a resposta real vira gravação do dublê.
