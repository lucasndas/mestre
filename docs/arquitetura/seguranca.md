# Segurança

> **Assunto:** modelo de ameaças, TLS e CA da Guilda, certificados com `mestre.local`, credenciais de máquina, uma Cidadela por vez, risco de máquina roubada, isolamento dos agentes, mascaramento em logs e quem vê o quê · **Componente:** Todos · **Atualizado:** 2026-10-13

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
| Máquina estranha tenta entrar na Guilda | código de comparação mostrado na máquina e conferido pelo Dono, aprovação com PIN, pedido que expira; depois, mTLS com credencial própria | [Guilda](../guilda/guilda.md#entrada-na-guilda) |
| Máquina roubada ou comprometida | credencial revogável no painel (derruba as conexões na hora). **Toda máquina não excluída guarda o cofre cifrado com a chave da máquina e pode abri-lo**: que uma Torre só o abra ao virar Cidadela é regra do programa, não da criptografia, então uma máquina roubada tem o **mesmo risco da Cidadela** e pede a troca dos segredos; a [máquina excluída](../guilda/guilda.md#máquina-excluída) não guarda segredos | [Guilda](../guilda/guilda.md#saída-e-remoção), [replicação](../guilda/replicacao.md#cofre-em-toda-máquina), [cofre](../plataforma/cofre.md) |
| Duas Cidadelas agindo ao mesmo tempo (a antiga volta, rede partida) | época em toda mensagem e certidão assinada; ação externa só depois de confirmar a época na trava do GitHub (só uma máquina cria a ref da época nova); Cidadela sem confirmação fica suspensa; o `409 Conflict` do Telegram é só sinal para conferir a trava; promoção só com aprovação do Dono | [Cidadela e Torres](../guilda/cidadela-e-torres.md#época-e-fencing), [trava no GitHub](../guilda/trava-no-github.md) |
| Agente manipulado por conteúdo (issue, anexo, código) | nenhum agente tem credencial; permissões por classe no `mestre hook` + reforço nativo; anexos nunca são executados; ações destrutivas só a pedido explícito de usuário | [permissões](../classes/permissoes.md), [anexos e transcrição](../telegram/anexos-e-transcricao.md), [convenções da CLI](../cli/convencoes.md) |
| Agente se passa por outro (`MESTRE_AGENT_ID` falso) | o motor confere o processo chamador: no Linux, pelo cgroup do terminal; no Windows, pela árvore de processos (abaixo) | [canal local](canal-local.md#identificação-e-versão), [convenções da CLI](../cli/convencoes.md) |
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

Todo tráfego é TLS 1.3, inclusive na rede local ([ADR 0002](../decisoes/0002-wss-iniciado-pela-torre.md)). A CA é **da Guilda**, não de uma máquina: nasce com a Guilda, e a chave privada é um segredo **dentro do cofre**, que vai a toda máquina não excluída, para que uma nova Cidadela continue emitindo ([replicação](../guilda/replicacao.md#ca-da-guilda)). Valores **padrão**:

| Certificado | Emitido por | Chave | Validade | Nomes (SAN) |
|---|---|---|---|---|
| CA da Guilda | a si mesma, na criação da Guilda | ECDSA P-256, dentro do cofre | 10 anos | — |
| Motor de cada máquina (cliente mTLS e servidor da 7443) | CA, na [entrada](../guilda/guilda.md#credenciais) | ECDSA P-256, só na máquina | 1 ano | `mestre://machine/<machine_id>`, nome da máquina, nome `.local`, IPs da rede local, nomes extras configurados e `mestre.local` (menos na [máquina excluída](../guilda/guilda.md#máquina-excluída), que nunca é Cidadela) |
| Guardião de cada máquina (cliente mTLS, servidor da 7444 e da página reserva 7446) | CA, na entrada | ECDSA P-256, na pasta do Guardião | 1 ano | `mestre://guardian/<machine_id>`, nome da máquina, nome `.local`, IPs da rede local |

- **`mestre.local` em todo certificado de motor** (menos o da excluída): qualquer máquina pode virar Cidadela e servir o painel nesse nome sem certificado novo. Por isso os retornos dos GitHub Apps (`redirect_url`, `setup_url`) usam `https://mestre.local:<porta>/…`, nunca um IP ([criação de Apps](../github/criacao-de-apps.md)), e o redirecionamento `307` das Torres preserva caminho e query ([painel redirecionado](../guilda/cidadela-e-torres.md#painel-redirecionado)).
- Renovação automática **30 dias antes de vencer**, pela conexão já autenticada com a Cidadela atual ([credenciais](../guilda/guilda.md#credenciais)); o [checklist](../plataforma/checklist.md) alerta se um certificado chegar a menos de 30 dias sem ter sido renovado. Certificados com IPs são reemitidos quando os IPs da máquina mudam.
- A Cidadela exige certificado de cliente nas rotas das Torres e dos Guardiões; o painel usa login. O Guardião da Cidadela aceita os certificados de motor e de Guardião das máquinas da Guilda.
- **Quem é a Cidadela:** uma Torre só aceita como Cidadela a máquina nomeada na certidão de época mais alta que viu, assinada pela CA ([certidão de época](../guilda/replicacao.md#certidão-de-época)); um certificado válido de outra máquina da Guilda não basta.
- Revogação: a Cidadela confere a credencial em `machine_credentials` a cada conexão; revogar fecha as conexões abertas daquela máquina. A lista de revogados vai às Torres na réplica, para valer também depois de uma troca de Cidadela.
- As máquinas confiam na CA ao entrar na Guilda; o navegador recebe instruções para confiar nela uma vez ([primeiro acesso](../operacao/primeiro-acesso.md)) e continua confiando depois de uma troca de Cidadela, porque a CA é a mesma.
- **Download da CA:** o motor da Cidadela serve o certificado da CA em `https://<cidadela>:7443/ca.crt`, **sem login** (é público), no formato **DER**, para que o SHA-256 do arquivo seja igual à impressão digital SHA-256 que o instalador mostra. Quem baixa confere essa impressão antes de confiar.

## Identidade de quem chama a CLI

O `MESTRE_AGENT_ID` está no ambiente de cada agente, mas qualquer processo pode definir essa variável. Por isso o motor (`mestre`) de cada máquina, que roda os agentes e atende o [canal local](canal-local.md), confere o chamador. O terminal achado tem de ser do personagem do `MESTRE_AGENT_ID` informado; senão, a chamada é recusada (`not_identified`) e registrada na auditoria. Chamadas encaminhadas à Cidadela carregam a identidade já verificada pelo motor de origem, autenticado por mTLS.

- **Linux: um cgroup v2 por terminal.** Cada terminal nasce no cgroup `term-<id>`, sob o cgroup delegado do `mestre`, já dentro dele (`CLONE_INTO_CGROUP`, sem janela de corrida; [terminal](terminal.md#criação)). Todo descendente herda o cgroup, mesmo o que troca de pai ou de sessão. O motor lê o PID e um pidfd do chamador (`SO_PEERCRED` e `SO_PEERPIDFD`) e aceita se `/proc/<pid>/cgroup` é o cgroup de um terminal registrado. Depois de ler o `/proc`, o pidfd ainda precisa apontar para o mesmo PID (o chamador não foi colhido nem o PID reaproveitado). Sem pidfd (o chamador já foi colhido antes de o motor aceitar a conexão): recusa. Num kernel sem `SO_PEERPIDFD` (anterior ao 6.5) vale só a conferência por horário de início (abaixo).
- **Linux sem cgroup delegado** (suíte completa, que roda como processos comuns, ou LIN-11 do [checklist](../plataforma/checklist.md) falhou): o motor sobe a árvore de processos, como no Windows, com a mesma conferência do pidfd. Falha segura: só recusa os órfãos.
- **Windows:** PID do pipe por `GetNamedPipeClientProcessId`; o motor sobe a árvore de processos do chamador até o processo raiz de um terminal registrado (spike de Windows ainda aberto, em [Verificar](#verificar-na-implementação)).
- **Subida pela árvore:** a cada passo, o pai lido não pode ter começado depois do filho (horário de início no `/proc/<pid>/stat`); se começou, o pai real morreu durante a subida e o PID foi reaproveitado: a subida recomeça do chamador, até 3 vezes, e então desiste com `not_identified`.

**Resultado do spike no Linux** (`tools/spikes/linux/01-caller-identity`, [spikes do M1](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1)): subir só pela árvore falha para órfãos, que o init adota: `nohup … &` disparado pela ferramenta Bash do Claude Code real, `sh -c 'nohup cmd &'`, double fork `( cmd & )`, `setsid -f` e tmux. Conferir a sessão (SID) não ajuda, porque o Claude Code roda cada comando numa sessão nova; um subreaper no próprio CLI não serve, porque o Node 24 não colhe os órfãos adotados. Custos medidos: subida pela árvore, ~58 µs (profundidade 6); leitura de `/proc/<pid>/cgroup`, ~6 µs.

**Codex só com `--no-daemon`:** no modo padrão, o Codex sobe um processo de fundo compartilhado por todos os terminais do perfil, que fica no cgroup de quem o iniciou e é pai dos comandos da ferramenta de shell; o cgroup por terminal só identifica o chamador certo com `--no-daemon` na base ([Codex](../provedores/codex.md#processo-de-fundo-e---no-daemon)).

**Requisitos no Linux** (conferidos pelo [checklist](../plataforma/checklist.md#itens-linux)): cgroup v2, kernel 5.7 ou maior (`CLONE_INTO_CGROUP`; 5.14 para o `cgroup.kill` do encerramento), `Delegate=yes` na unidade `mestre` ([componentes](componentes.md#serviços-e-contas-padrão)), `kernel.yama.ptrace_scope` 1 ou maior (todos os personagens são `mestre-agente`: sem isso, um agente poderia injetar código num processo de outro terminal e chamar como ele) e `/proc` legível pela conta `mestre` (sem `hidepid` que a esconda). O `mestre-agente` não tem gerenciador de usuário (`systemd --user`, linger): com ele, tmux e D-Bus movem processos para outros scopes, que saem do cgroup do terminal e passam a ser recusados (falha segura, mas evitada).

## Contas do sistema e isolamento dos agentes

**Princípio, nos dois sistemas: agente nunca roda com a conta dona do cofre nem com a conta do usuário.** Exceção única: o [Operador do PC](#operador-do-pc) ([ADR 0021](../decisoes/0021-operador-do-pc.md)). Quem guarda cofre, banco e configuração é a conta do motor, que nenhum terminal usa; os agentes rodam numa conta própria, `mestre-agente`, sem acesso aos arquivos pessoais de quem usa a máquina.

- **Linux (Cidadela e Torre):** o motor (`mestre`) roda como `mestre`; os agentes, como `mestre-agente` (o motor tem `CAP_SETUID` e `CAP_SETGID` só para iniciar os terminais com essa conta). Banco, cofre, configuração e logs ficam com dono `mestre`, modo 0700/0600. Workspaces, perfis de conta, o retrato `policy.json` e a fila do `mestre hook` ficam no grupo `mestre-agente`, com os modos de [persistência](persistencia.md#arquivos). O Guardião roda como root ([componentes](componentes.md#serviços-e-contas-padrão)).
- **Windows (Cidadela e Torre):** o motor (`mestre`) roda na conta virtual `NT SERVICE\Mestre`, dona do cofre (DPAPI de máquina + ACL só para ela, [cofre](../plataforma/cofre.md#chave-do-cofre)), do banco, do espelho e do painel, e inicia os agentes com a conta local `mestre-agente`: usuário comum, fora de Administradores, sem logon interativo, com senha aleatória guardada pelo Mestre ([Windows](windows.md#conta-dos-agentes-mestre-agente)). O Guardião roda como LocalSystem. Nenhum processo do Mestre usa a conta do usuário, exceto o Operador do PC, quando habilitado.
- **Arquivos pessoais (Windows):** o perfil do usuário (`C:\Users\<usuário>`) não é acessível ao `mestre-agente` pelas ACLs padrão do Windows. Pastas fora do perfil que dão leitura a `Users` continuam legíveis por ele; o Mestre não altera ACLs fora das pastas dele, e quem guarda algo sensível fora do perfil deve restringir a pasta.
- **CLIs como `mestre-agente` no Linux (spike do M1, `tools/spikes/linux/02-clis-as-agent`, feito):** `claude`, `codex` e `agy` funcionam com o perfil de conta em `/var/lib/mestre/contas/<perfil>` e sem pasta pessoal real (`/nonexistent`); a pasta pessoal própria do `mestre-agente` (plano B) não é necessária. Os três gravam a credencial em arquivo `0600` dentro do perfil e nenhum usa chaveiro nem D-Bus. O `agy` exige `HOME` gravável, que o perfil cumpre. Detalhes por provedor: [Claude Code](../provedores/claude-code.md#contas-múltiplas), [Codex](../provedores/codex.md#contas-múltiplas), [Antigravity](../provedores/antigravity.md#contas-múltiplas).
- **Entre personagens, a separação não é do sistema operacional.** Todos os personagens de uma máquina rodam com a **mesma** conta, `mestre-agente`, nos dois sistemas. Pelo sistema operacional, um personagem consegue ler e escrever o worktree de outra mesa, a `MESTRE_TMP` de outro personagem e ler o `policy.json` alheio. Quem impede isso é o **despachante** (`mestre hook`), pelas regras fixas ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)), com o reforço nativo de cada provedor ([reforço nativo](../classes/reforco-nativo.md)) onde ele existe. **Risco residual aceito:** nenhum desses arquivos dá acesso ao cofre, e o motor valida tudo o que lê das pastas compartilhadas.

## Navegação web e login em sites

- Cada personagem que navega usa o **próprio** MCP de navegador (ex.: Playwright MCP headless), iniciado pelo CLI dele e, portanto, como `mestre-agente`, com navegador limpo: sem acesso às sessões, aos cookies e aos perfis de navegador do usuário ([permissões](../classes/permissoes.md#formato-do-permissionsyaml)).
- **Login em sites só pelo cofre:** credenciais de site ficam no cofre, por domínio, e a classe libera quais o personagem pode usar. O motor da máquina do personagem faz o login num navegador que ele controla, com o mesmo navegador e versão do MCP do personagem, e entrega a ele só o estado de sessão resultante, apagado no fim; a senha nunca chega ao personagem ([cofre](../plataforma/cofre.md#login-em-sites)). Segundo fator e captcha de imagem o motor resolve com o dono da credencial pelo Telegram, sem passar por agente; captcha interativo vira `login_requires_human` e proposta de Operador do PC ([segundo fator](../plataforma/cofre.md#segundo-fator-e-captcha)).
- **Navegador e computador do usuário:** MCPs que controlam o computador ou o navegador do usuário só funcionam na base Operador do PC; em qualquer outra, uma regra fixa os nega, qualquer que seja o transporte ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)).

## Operador do PC

A única exceção ao princípio de contas, só numa máquina Windows com o Operador habilitado ([ADR 0021](../decisoes/0021-operador-do-pc.md); comportamento em [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc), peças em [Windows](windows.md#operador-do-pc)).

- **Critério:** nenhum processo do Mestre roda com a conta do usuário, exceto o processo da sessão `mestre operator-session` (com o Operador habilitado) e, durante uma ativação aprovada, os terminais do Operador e seus filhos (CA-INST-11, [instalação](../plataforma/instalacao-e-atualizacao.md)).
- **Cofre e pastas:** com UAC ligado o processo roda com token filtrado (Administradores só para negar); `%ProgramData%\Mestre` não tem entrada para a conta do usuário ([persistência](persistencia.md#arquivos)); a chave do cofre só abre para `NT SERVICE\Mestre` ([cofre](../plataforma/cofre.md#chave-do-cofre)); regras fixas negam acesso a essas pastas e os comandos de elevação ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)).
- **Clicar "Sim" num UAC:** o pedido de elevação aparece na área de trabalho segura, que cliques e teclas sintéticos não alcançam (UIPI). O [checklist](../plataforma/checklist.md) exige UAC ligado, `PromptOnSecureDesktop = 1` e `ConsentPromptBehaviorAdmin` com pedido (nunca "elevar sem perguntar"); se falhar, a ficha da máquina não anuncia `operador`.
- **Risco residual aceito:** ações de mouse e teclado não são filtráveis pelo hook (ele vê "clicar em (x, y)", não o efeito). A proteção é prazo curto, aprovação explícita do Dono, aviso visível no PC, no Telegram e no painel, `/travar` e auditoria completa com capturas de tela.

## Mascaramento em logs

Aplicado a tudo o que vai para log (motor, Guardião, histórico de terminais, auditoria), antes de gravar:
1. **Valores conhecidos:** todo segredo carregado do cofre é registrado no mascarador na hora em que é lido; ocorrências exatas viram `[segredo]`.
2. **Formatos conhecidos:** tokens do GitHub (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`), tokens de bot do Telegram (`<números>:<35 caracteres>`), tokens do Slack (`xoxb-`, `xoxp-`), JWT (três partes base64url começando por `eyJ`), blocos `-----BEGIN … PRIVATE KEY-----`, cabeçalhos `Authorization:`, chaves `sk-…`, pares `password=`/`token=`/`secret=` e palavras de alta entropia (20 caracteres ou mais, padrão, com maiúsculas, minúsculas, ao menos dois dígitos e entropia de 3,5 bits por caractere ou mais, para UUIDs e hashes continuarem legíveis). O mesmo mascarador filtra as mensagens de usuários antes de chegarem ao Grão-Mestre ([credenciais de site](../plataforma/cofre.md#credenciais-de-site)).
3. Histórico de terminal: o mascaramento é por linha, depois de remover as sequências de escape. O fluxo ao vivo no painel não é mascarado (é a tela do agente).

## Quem vê o quê

| Dado | Agentes | Motor | Guardião | Usuários |
|---|---|---|---|---|
| Cofre (chaves dos Apps, tokens de usuários, credenciais, chave da CA) | nunca | só o motor da Cidadela, para a ação específica; as Torres não excluídas guardam a cópia cifrada, só a abrem depois de uma promoção e pedem à Cidadela, no uso, a credencial de que precisam; exceção: a Torre condutora em modo reduzido lê só o token do bot e a lista de usuários ([replicação](../guilda/replicacao.md#cofre-em-toda-máquina)) | não | ninguém vê um valor salvo, nem o Dono; ele cadastra, substitui e apaga com PIN ([cofre](../plataforma/cofre.md)) |
| Token do bot e lista de usuários | nunca | Cidadela; as Torres têm a lista de usuários na réplica, que não serve login | cópia criptografada nos Guardiões das Torres e, no modo máquina única, no da Cidadela ([cofre](../plataforma/cofre.md)) | Dono gerencia |
| Credenciais de site | nunca a senha; só o estado de sessão dos domínios liberados pela classe, durante a tarefa | o motor da Cidadela, que a entrega ao motor da máquina do personagem só em memória, para o login | não | não; Dono gerencia com PIN ([cofre](../plataforma/cofre.md#login-em-sites)) |
| Arquivos pessoais do usuário (Windows: `C:\Users\<usuário>`) | não (ACL do Windows); o Operador do PC, sim, durante a sessão | não | não | o próprio usuário |
| Código e worktrees | os da própria mesa (o Grão-Mestre, o clone principal) | sim | não | pelo GitHub |
| Terminais (tela e entrada) | `check` só em agentes conectados | sim | não | ver: todos com acesso ao workspace; digitar: Administrador ou acima ([canvas](../painel/canvas.md)) |
| Capturas de tela do Operador do PC | não | só o motor da Cidadela, cifradas, com cópia nas Torres não excluídas ([persistência](persistencia.md#operador-do-pc)) | não | só o Dono, no painel |
| Decretos, aprovações, auditoria | decretos que os afetam, na camada do Mestre | sim | não | conforme o nível ([usuários e níveis](../telegram/usuarios-e-niveis.md)) |
| Logs | não | sim | lê os do motor local (lista fechada) | pelo painel, conforme o nível |

## Verificar na implementação

O item é o quarto spike de Windows do M1, ainda aberto ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1)); o dos CLIs como `mestre-agente` no Linux foi fechado pelo spike ([acima](#contas-do-sistema-e-isolamento-dos-agentes)).

- **Verificar na implementação:** no Windows, a subida pela árvore de processos acha o terminal mesmo quando o provedor roda comandos por shell intermediário (`cmd /c` no Antigravity), por processo que troca de pai ou por órfão cujo pai já saiu (o equivalente ao `nohup … &` que derrubou a árvore no Linux, acima). **Padrão:** a subida pela árvore, como acima. **Se falhar:** a verificação passa a ser "o chamador pertence ao Job Object do terminal" (`IsProcessInJob` com o Job Object que o terminal já tem, [terminal](terminal.md#criação)). **Onde:** spike no Windows Sandbox ([spikes do M1](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1)) e conformidade de cada provedor no Windows (o agente chama a CLI pela ferramenta de shell). No Linux o item foi fechado pelo spike, com o cgroup por terminal ([acima](#identidade-de-quem-chama-a-cli)).
