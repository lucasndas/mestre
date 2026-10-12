# Segurança

> **Assunto:** modelo de ameaças, TLS e CA interna, credenciais de nó, isolamento dos agentes, mascaramento em logs e quem vê o quê · **Componente:** Todos · **Atualizado:** 2026-10-12

## O que proteger

1. **Credenciais do GitHub:** chaves privadas dos Apps e tokens de usuários (no [cofre](../plataforma/cofre.md)).
2. **Token do bot do Telegram** e a lista de usuários autorizados.
3. **Contas dos provedores** (assinaturas do Claude, Codex, Antigravity) nos perfis de conta.
4. **O código** dos repositórios e as branches protegidas.
5. **As máquinas:** nenhuma ação deve dar a alguém (pessoa ou agente) acesso livre a elas.

## Ameaças e proteções

| Ameaça | Proteção | Dono do detalhe |
|---|---|---|
| Pessoa não autorizada escreve ao bot | só IDs autorizados, só chat privado; tentativa gera alerta | [segurança do canal](../telegram/seguranca-do-canal.md) |
| Aparelho na rede local abre o painel, a API ou a página reserva | TLS sempre, login, sessões com expiração, PIN em ações sensíveis | [login](../painel/login.md), [página reserva](../autocura/pagina-reserva.md) |
| Máquina estranha tenta entrar como nó | convite de uso único com validade; depois, mTLS com credencial própria | [pareamento](../conectividade/pareamento.md) |
| Nó roubado ou comprometido | credencial revogável no painel (derruba as conexões na hora); segredos do GitHub e do cofre nunca vão para o nó, exceto as cópias do Guardião e, só em memória durante um login, a credencial de site do personagem daquele nó | [pareamento](../conectividade/pareamento.md), [cofre](../plataforma/cofre.md) |
| Agente manipulado por conteúdo (issue, anexo, código) | nenhum agente tem credencial; permissões por classe no `mestre hook` + reforço nativo; anexos nunca são executados; ações destrutivas só a pedido explícito de usuário | [permissões](../classes/permissoes.md), [anexos e transcrição](../telegram/anexos-e-transcricao.md), [convenções da CLI](../cli/convencoes.md) |
| Agente se passa por outro (`MESTRE_AGENT_ID` falso) | o motor confere o processo chamador (abaixo) | [convenções da CLI](../cli/convencoes.md) |
| Agente lê segredos, o banco ou arquivos pessoais no disco | o agente nunca roda com a conta dona do cofre nem com a conta do usuário (exceto o Operador do PC, abaixo): roda como `mestre-agente`, que só alcança as pastas liberadas pela ACL | abaixo, [persistência](persistencia.md#arquivos) |
| Agente usa as sessões do usuário em sites | navegador próprio do personagem, limpo, como `mestre-agente`; login em sites só com credencial do cofre, entregue ao personagem como estado de sessão, nunca a senha | abaixo, [cofre](../plataforma/cofre.md#login-em-sites) |
| Operador do PC age com a conta do usuário | só na base especial, com aprovação do Dono, prazo curto, aviso visível, auditoria com capturas; UAC na área de trabalho segura; elevação negada | abaixo, [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) |
| Agente gasta sem controle | limites por missão, mesa, workspace e dia | [limites de uso](../plataforma/limites-de-uso.md) |
| Usuário age acima do seu nível | níveis por workspace; ninguém revoga decreto de nível acima | [usuários e níveis](../telegram/usuarios-e-niveis.md), [decretos](../telegram/decretos.md) |
| Cura vira acesso remoto | sem SSH, lista fechada de ações; script de diagnóstico mostrado inteiro e aprovado por Dono ou Administrador, executado uma vez | [Guardião](../autocura/guardiao.md), [diagnóstico com IA](../autocura/diagnostico-com-ia.md) |
| Segredo vaza por log | mascaramento (abaixo) | este documento |
| Segredo vaza pelo Telegram (sem criptografia ponta a ponta) | o Mestre nunca envia segredos pelo Telegram; credenciais só se cadastram pelo painel, e as mandadas por mensagem são tiradas, apagadas e geram recomendação de troca | [segurança do canal](../telegram/seguranca-do-canal.md) |
| Arquivos gerados atrapalham quem clona o repositório | arquivos locais e `.git/info/exclude`; `mestre hook` só age com `MESTRE_AGENT_ID` | [sessão zero da mesa](../sessao-zero/mesa.md), [adaptador](../provedores/adaptador.md) |
| Emergência (algo saiu do controle) | `/travar` congela agentes e suspende decretos | [segurança do canal](../telegram/seguranca-do-canal.md) |

## TLS e CA interna

Todo tráfego é TLS 1.3, inclusive na rede local ([ADR 0002](../decisoes/0002-wss-iniciado-pelo-no.md)). Valores **padrão**:

| Certificado | Emitido por | Chave | Validade | Nomes (SAN) |
|---|---|---|---|---|
| CA interna | a si mesma, na instalação do Core | ECDSA P-256, no cofre | 10 anos | — |
| Servidor do motor do Core (porta 7443) | CA interna, na instalação | ECDSA P-256 | 1 ano | `mestre://core/<core_id>`, nome da máquina, nome `.local`, IPs da rede local e os nomes extras configurados no Core |
| Servidor do Guardião do Core (7444 e, no modo máquina única, 7446) | CA interna, na instalação | ECDSA P-256, na pasta do Guardião | 1 ano | `mestre://core-guardian/<core_id>`, nome da máquina, nome `.local`, IPs da rede local |
| Servidor da página reserva do Guardião de cada nó (7446) | CA interna, no pareamento | ECDSA P-256, só no nó | 1 ano | nome do nó, nome `.local`, IPs da rede local |
| Cliente do motor de cada nó (mTLS) | CA interna, no pareamento | ECDSA P-256, só no nó | 1 ano | `mestre://node/<node_id>` |
| Cliente do Guardião de cada nó (mTLS) | CA interna, no pareamento | ECDSA P-256, só no nó | 1 ano | `mestre://guardian/<node_id>` |

- Renovação automática **30 dias antes de vencer**, pela conexão já autenticada ([pareamento](../conectividade/pareamento.md#credenciais)); o [checklist](../plataforma/checklist.md) alerta se um certificado chegar a menos de 30 dias sem ter sido renovado. Certificados de servidor com IPs (Core, Guardião do Core, página reserva dos nós) são reemitidos quando os IPs da máquina mudam.
- O nó local do Core é pareado pelo instalador e recebe os mesmos certificados de um nó remoto.
- O motor do Core exige certificado de cliente nas rotas dos nós e dos Guardiões; o painel usa login. O Guardião do Core aceita os certificados de motor e de Guardião dos nós.
- Revogação: o Core confere a credencial no `node_credentials` a cada conexão; revogar fecha as conexões abertas daquele nó.
- Os nós confiam na CA ao serem pareados ([pareamento](../conectividade/pareamento.md)); o navegador recebe instruções para confiar nela uma vez ([primeiro acesso](../operacao/primeiro-acesso.md)).
- **Download da CA:** o motor do Core serve o certificado da CA em `https://<core>:7443/ca.crt`, **sem login** (é público), no formato **DER**, para que o SHA-256 do arquivo seja igual à impressão digital SHA-256 que o instalador mostra. Quem baixa confere essa impressão antes de confiar.

## Identidade de quem chama a CLI

O `MESTRE_AGENT_ID` está no ambiente de cada agente, mas qualquer processo pode definir essa variável. Por isso o motor confere o chamador:
- Quem confere é o `mestre-node` de cada máquina (também no Core), que roda os agentes e atende o [canal local](canal-local.md). Linux: PID do socket por `SO_PEERCRED`; Windows: `GetNamedPipeClientProcessId`.
- O motor sobe a árvore de processos do chamador até achar o processo raiz de um terminal registrado. Esse terminal tem de ser do personagem do `MESTRE_AGENT_ID` informado; senão, a chamada é recusada e registrada na auditoria.
- Chamadas encaminhadas entre nós carregam a identidade já verificada pelo motor de origem, autenticado por mTLS.

## Contas do sistema e isolamento dos agentes

**Princípio, nos dois sistemas: agente nunca roda com a conta dona do cofre nem com a conta do usuário.** Exceção única: o [Operador do PC](#operador-do-pc) ([ADR 0021](../decisoes/0021-operador-do-pc.md)). Quem guarda cofre, banco e configuração do Core é uma conta que nenhum terminal usa; os agentes rodam numa conta própria, `mestre-agente`, sem acesso aos arquivos pessoais de quem usa a máquina.

- **Linux (Core e nó):** os motores (`mestre-core` e `mestre-node`) rodam como `mestre`; os agentes, como `mestre-agente` (só o `mestre-node` tem `CAP_SETUID` e `CAP_SETGID`, para iniciar os terminais com essa conta). Banco, cofre, configuração e logs ficam com dono `mestre`, modo 0700/0600. Workspaces, perfis de conta, o retrato `policy.json` e a fila do `mestre hook` ficam no grupo `mestre-agente`, com os modos de [persistência](persistencia.md#arquivos). O Guardião roda como root ([componentes](componentes.md#serviços-e-contas-padrão)).
- **Windows (Core e nó):** no Core, o `mestre-core` roda na conta virtual `NT SERVICE\MestreCore`, dona do cofre (DPAPI de máquina + ACL só para ela, [cofre](../plataforma/cofre.md#chave-do-cofre)), do banco, do espelho e do painel. Em toda máquina, o `mestre-node` roda na conta virtual `NT SERVICE\MestreNode` e inicia os agentes com a conta local `mestre-agente`: usuário comum, fora de Administradores, sem logon interativo, com senha aleatória guardada pelo Mestre ([Windows](windows.md#conta-dos-agentes-mestre-agente)). O Guardião roda como LocalSystem. Nenhum processo do Mestre usa a conta do usuário, exceto o Operador do PC, quando habilitado.
- **Arquivos pessoais (Windows):** o perfil do usuário (`C:\Users\<usuário>`) não é acessível ao `mestre-agente` pelas ACLs padrão do Windows. Pastas fora do perfil que dão leitura a `Users` continuam legíveis por ele; o Mestre não altera ACLs fora das pastas dele, e quem guarda algo sensível fora do perfil deve restringir a pasta.
- **Entre personagens, a separação não é do sistema operacional.** Todos os personagens de uma máquina rodam com a **mesma** conta, `mestre-agente`, nos dois sistemas. Pelo sistema operacional, um personagem consegue ler e escrever o worktree de outra mesa, a `MESTRE_TMP` de outro personagem e ler o `policy.json` alheio. Quem impede isso é o **despachante** (`mestre hook`), pelas regras fixas ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)), com o reforço nativo de cada provedor ([reforço nativo](../classes/reforco-nativo.md)) onde ele existe. **Risco residual aceito:** nenhum desses arquivos dá acesso ao cofre, e o motor valida tudo o que lê das pastas compartilhadas.

## Navegação web e login em sites

- Cada personagem que navega usa o **próprio** MCP de navegador (ex.: Playwright MCP headless), iniciado pelo CLI dele e, portanto, como `mestre-agente`, com navegador limpo: sem acesso às sessões, aos cookies e aos perfis de navegador do usuário ([permissões](../classes/permissoes.md#formato-do-permissionsyaml)).
- **Login em sites só pelo cofre:** credenciais de site ficam no cofre, por domínio, e a classe libera quais o personagem pode usar. O motor do nó do personagem faz o login num navegador que ele controla, com o mesmo navegador e versão do MCP do personagem, e entrega a ele só o estado de sessão resultante, apagado no fim; a senha nunca chega ao personagem ([cofre](../plataforma/cofre.md#login-em-sites)). Segundo fator e captcha de imagem o motor resolve com o dono da credencial pelo Telegram, sem passar por agente; captcha interativo vira `login_requires_human` e proposta de Operador do PC ([segundo fator](../plataforma/cofre.md#segundo-fator-e-captcha)).
- **Navegador e computador do usuário:** MCPs que controlam o computador ou o navegador do usuário só funcionam na base Operador do PC; em qualquer outra, uma regra fixa os nega, qualquer que seja o transporte ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)).

## Operador do PC

A única exceção ao princípio de contas, só num nó Windows com o Operador habilitado ([ADR 0021](../decisoes/0021-operador-do-pc.md); comportamento em [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc), peças em [Windows](windows.md#operador-do-pc)).

- **Critério:** nenhum processo do Mestre roda com a conta do usuário, exceto o processo da sessão `mestre node operator-session` (com o Operador habilitado) e, durante uma ativação aprovada, os terminais do Operador e seus filhos (CA-INST-11, [instalação](../plataforma/instalacao-e-atualizacao.md)).
- **Cofre e pastas:** com UAC ligado o processo roda com token filtrado (Administradores só para negar); `%ProgramData%\Mestre` não tem entrada para a conta do usuário ([persistência](persistencia.md#arquivos)); a chave do cofre só abre para `NT SERVICE\MestreCore` ([cofre](../plataforma/cofre.md#chave-do-cofre)); regras fixas negam acesso a essas pastas e os comandos de elevação ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)).
- **Clicar "Sim" num UAC:** o pedido de elevação aparece na área de trabalho segura, que cliques e teclas sintéticos não alcançam (UIPI). O [checklist](../plataforma/checklist.md) exige UAC ligado, `PromptOnSecureDesktop = 1` e `ConsentPromptBehaviorAdmin` com pedido (nunca "elevar sem perguntar"); se falhar, o nó não anuncia `operador`.
- **Risco residual aceito:** ações de mouse e teclado não são filtráveis pelo hook (ele vê "clicar em (x, y)", não o efeito). A proteção é prazo curto, aprovação explícita do Dono, aviso visível no PC, no Telegram e no painel, `/travar` e auditoria completa com capturas de tela.

## Mascaramento em logs

Aplicado a tudo o que vai para log (motor, Guardião, histórico de terminais, auditoria), antes de gravar:
1. **Valores conhecidos:** todo segredo carregado do cofre é registrado no mascarador na hora em que é lido; ocorrências exatas viram `[segredo]`.
2. **Formatos conhecidos:** tokens do GitHub (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`), tokens de bot do Telegram (`<números>:<35 caracteres>`), tokens do Slack (`xoxb-`, `xoxp-`), JWT (três partes base64url começando por `eyJ`), blocos `-----BEGIN … PRIVATE KEY-----`, cabeçalhos `Authorization:`, chaves `sk-…`, pares `password=`/`token=`/`secret=` e palavras de alta entropia (20 caracteres ou mais, padrão, com maiúsculas, minúsculas, ao menos dois dígitos e entropia de 3,5 bits por caractere ou mais, para UUIDs e hashes continuarem legíveis). O mesmo mascarador filtra as mensagens de usuários antes de chegarem ao Grão-Mestre ([credenciais de site](../plataforma/cofre.md#credenciais-de-site)).
3. Histórico de terminal: o mascaramento é por linha, depois de remover as sequências de escape. O fluxo ao vivo no painel não é mascarado (é a tela do agente).

## Quem vê o quê

| Dado | Agentes | Motor | Guardião | Usuários |
|---|---|---|---|---|
| Cofre (chaves dos Apps, tokens de usuários, credenciais) | nunca | só o motor do Core, para a ação específica | não | ninguém vê um valor salvo, nem o Dono; ele cadastra, substitui e apaga com PIN ([cofre](../plataforma/cofre.md)) |
| Token do bot e lista de usuários | nunca | Core (o motor do nó guarda só o cache `cache_users`, que não serve login) | cópia criptografada nos Guardiões dos nós e, no modo máquina única, no do Core ([cofre](../plataforma/cofre.md)) | Dono gerencia |
| Credenciais de site | nunca a senha; só o estado de sessão dos domínios liberados pela classe, durante a tarefa | o motor do Core, que a entrega ao motor do nó do personagem só em memória, para o login | não | não; Dono gerencia com PIN ([cofre](../plataforma/cofre.md#login-em-sites)) |
| Arquivos pessoais do usuário (Windows: `C:\Users\<usuário>`) | não (ACL do Windows); o Operador do PC, sim, durante a sessão | não | não | o próprio usuário |
| Código e worktrees | os da própria mesa (o Grão-Mestre, o clone principal) | sim | não | pelo GitHub |
| Terminais (tela e entrada) | `check` só em agentes conectados | sim | não | ver: todos com acesso ao workspace; digitar: Administrador ou acima ([canvas](../painel/canvas.md)) |
| Capturas de tela do Operador do PC | não | só o motor do Core, cifradas ([persistência](persistencia.md#operador-do-pc)) | não | só o Dono, no painel |
| Decretos, aprovações, auditoria | decretos que os afetam, na camada do Mestre | sim | não | conforme o nível ([usuários e níveis](../telegram/usuarios-e-niveis.md)) |
| Logs | não | sim | lê os do motor local (lista fechada) | pelo painel, conforme o nível |

## Verificar na implementação

Os dois itens são spikes do M1 ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1)).

- **Verificar na implementação:** `claude`, `codex` e `agy` funcionam como `mestre-agente` (conta de sistema sem login) com os perfis de conta em `/var/lib/mestre/contas/`. **Padrão:** funcionam, com `HOME` e as variáveis de conta de cada provedor apontando para o perfil. **Se falhar:** a instalação dá a `mestre-agente` uma pasta pessoal própria em `/var/lib/mestre/agente/` e o perfil de conta continua pelas variáveis do provedor; se o CLI ainda falhar, aquele provedor não é oferecido naquela máquina Linux e o checklist explica (LIN-10). **Onde:** [conformidade](../provedores/conformidade.md) em cada máquina Linux (Core ou nó), na instalação e na suíte pesada.
- **Verificar na implementação:** a subida pela árvore de processos acha o terminal mesmo quando o provedor roda comandos por shell intermediário (`sh -c`, `cmd /c` no Antigravity) ou por processo que troca de pai. **Padrão:** a subida pela árvore, como acima. **Se falhar:** cada terminal passa a rodar num grupo que não depende do pai (um cgroup por terminal no Linux, um Job Object no Windows), e a verificação passa a ser "o chamador pertence ao grupo do terminal". **Onde:** conformidade de cada provedor (o agente chama a CLI pela ferramenta de shell) no Linux e no Windows.
