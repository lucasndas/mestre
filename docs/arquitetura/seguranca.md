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
| Nó roubado ou comprometido | credencial revogável no painel (derruba as conexões na hora); segredos do GitHub e do cofre nunca vão para o nó, exceto as cópias do Guardião | [pareamento](../conectividade/pareamento.md), [cofre](../plataforma/cofre.md) |
| Agente manipulado por conteúdo (issue, anexo, código) | nenhum agente tem credencial; permissões por classe no `mestre hook` + reforço nativo; anexos nunca são executados; ações destrutivas só a pedido explícito de usuário | [permissões](../classes/permissoes.md), [anexos e transcrição](../telegram/anexos-e-transcricao.md), [convenções da CLI](../cli/convencoes.md) |
| Agente se passa por outro (`MESTRE_AGENT_ID` falso) | o motor confere o processo chamador (abaixo) | [convenções da CLI](../cli/convencoes.md) |
| Agente lê segredos ou o banco no disco | Linux: contas separadas para motor e agentes; Windows: regras fixas do Mestre no hook (risco residual, abaixo) | [precedência](precedencia.md) |
| Agente gasta sem controle | limites por missão, mesa, workspace e dia | [limites de uso](../plataforma/limites-de-uso.md) |
| Usuário age acima do seu nível | níveis por workspace; ninguém revoga decreto de nível acima | [usuários e níveis](../telegram/usuarios-e-niveis.md), [decretos](../telegram/decretos.md) |
| Cura vira acesso remoto | sem SSH, lista fechada de ações; script de diagnóstico mostrado inteiro e aprovado por Dono ou Administrador, executado uma vez | [Guardião](../autocura/guardiao.md), [diagnóstico com IA](../autocura/diagnostico-com-ia.md) |
| Segredo vaza por log | mascaramento (abaixo) | este documento |
| Segredo vaza pelo Telegram (sem criptografia ponta a ponta) | o Mestre nunca envia segredos pelo Telegram | [segurança do canal](../telegram/seguranca-do-canal.md) |
| Arquivos gerados atrapalham quem clona o repositório | arquivos locais e `.git/info/exclude`; `mestre hook` só age com `MESTRE_AGENT_ID` | [sessão zero da mesa](../sessao-zero/mesa.md), [adaptador](../provedores/adaptador.md) |
| Emergência (algo saiu do controle) | `/travar` congela agentes e suspende decretos | [segurança do canal](../telegram/seguranca-do-canal.md) |

## TLS e CA interna

Todo tráfego é TLS 1.3, inclusive na rede local ([ADR 0002](../decisoes/0002-wss-iniciado-pelo-no.md)). Valores **padrão**:

| Certificado | Emitido por | Chave | Validade | Nomes (SAN) |
|---|---|---|---|---|
| CA interna | a si mesma, na instalação do Core | ECDSA P-256, no cofre | 10 anos | — |
| Servidor do Core (portas 7443 e 7444) | CA interna | ECDSA P-256 | 1 ano | nome da máquina, nome `.local`, IPs da rede local (e nome Tailscale, se configurado) |
| Página reserva de cada nó | CA interna, no pareamento | ECDSA P-256 | 1 ano | nome do nó, nome `.local`, IPs da rede local |
| Cliente do motor de cada nó (mTLS) | CA interna, no pareamento | ECDSA P-256, só no nó | 1 ano | identificador do nó + papel `motor` |
| Cliente do Guardião de cada nó (mTLS) | CA interna, no pareamento | ECDSA P-256, só no nó | 1 ano | identificador do nó + papel `guardiao` |

- Renovação automática 30 dias antes de vencer, pela conexão já autenticada; o [checklist](../plataforma/checklist.md) alerta se faltar menos que isso. Se os IPs do Core mudarem, o certificado do servidor é reemitido.
- O motor do Core exige certificado de cliente nas rotas dos nós e dos Guardiões; o painel usa login. O Guardião do Core aceita os certificados de motor e de Guardião dos nós.
- Revogação: o Core confere a credencial no `node_credentials` a cada conexão; revogar fecha as conexões abertas daquele nó.
- Os nós confiam na CA ao serem pareados ([pareamento](../conectividade/pareamento.md)); o navegador recebe instruções para confiar nela uma vez ([primeiro acesso](../operacao/primeiro-acesso.md)).
- **Download da CA:** o motor do Core serve o certificado da CA em `https://<core>:7443/ca.crt`, **sem login** (é público), no formato **DER**, para que o SHA-256 do arquivo seja igual à impressão digital SHA-256 que o instalador mostra. Quem baixa confere essa impressão antes de confiar.

## Identidade de quem chama a CLI

O `MESTRE_AGENT_ID` está no ambiente de cada agente, mas qualquer processo pode definir essa variável. Por isso o motor confere o chamador:
- Linux: PID do socket por `SO_PEERCRED`; Windows: `GetNamedPipeClientProcessId`.
- O motor sobe a árvore de processos do chamador até achar o processo raiz de um terminal registrado. Esse terminal tem de ser do personagem do `MESTRE_AGENT_ID` informado; senão, a chamada é recusada e registrada na auditoria.
- Chamadas encaminhadas entre nós carregam a identidade já verificada pelo motor de origem, autenticado por mTLS.

## Contas do sistema e isolamento dos agentes

- **Core (Linux):** o motor roda como `mestre`; os agentes, como `mestre-agente` (o motor tem só `CAP_SETUID` e `CAP_SETGID` para iniciar os terminais com essa conta). Banco, cofre, configuração e logs ficam com dono `mestre`, modo 0700/0600. Worktrees e perfis de conta ficam no grupo `mestre-agente`. O Guardião roda como root ([componentes](componentes.md)).
- **Nó Windows:** motor e agentes usam a mesma conta do usuário ([nó Windows](no-windows.md)). Um agente, em tese, consegue ler os arquivos do nó, inclusive a chave de cliente. **Risco residual aceito:** a regra fixa do Mestre proíbe aos agentes ler ou escrever as pastas de dados do Mestre, e o `mestre hook` e o reforço nativo a aplicam. Se a credencial vazar, ela não dá acesso ao cofre e pode ser revogada.

## Mascaramento em logs

Aplicado a tudo o que vai para log (motor, Guardião, histórico de terminais, auditoria), antes de gravar:
1. **Valores conhecidos:** todo segredo carregado do cofre é registrado no mascarador na hora em que é lido; ocorrências exatas viram `[segredo]`.
2. **Formatos conhecidos:** tokens do GitHub (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`), tokens de bot do Telegram (`<números>:<35 caracteres>`), blocos `-----BEGIN … PRIVATE KEY-----`, cabeçalhos `Authorization:`, chaves `sk-…`, e pares `password=`/`token=`/`secret=`.
3. Histórico de terminal: o mascaramento é por linha, depois de remover as sequências de escape. O fluxo ao vivo no painel não é mascarado (é a tela do agente).

## Quem vê o quê

| Dado | Agentes | Motor | Guardião | Usuários |
|---|---|---|---|---|
| Cofre (chaves dos Apps, tokens de usuários, credenciais) | nunca | só o motor do Core, para a ação específica | não | não; Dono gerencia com PIN ([cofre](../plataforma/cofre.md)) |
| Token do bot e lista de usuários | nunca | Core | cópia criptografada nos Guardiões dos nós | Dono gerencia |
| Código e worktrees | os da própria mesa (o Grão-Mestre, o clone principal) | sim | não | pelo GitHub |
| Terminais (tela e entrada) | `check` só em agentes conectados | sim | não | ver: todos com acesso ao workspace; digitar: Administrador ou acima ([canvas](../painel/canvas.md)) |
| Decretos, aprovações, auditoria | decretos que os afetam, na camada do Mestre | sim | não | conforme o nível ([usuários e níveis](../telegram/usuarios-e-niveis.md)) |
| Logs | não | sim | lê os do motor local (lista fechada) | pelo painel, conforme o nível |

## A confirmar

- **A confirmar:** `claude`, `codex` e `agy` funcionam como `mestre-agente` (conta de sistema sem login) com os perfis de conta em `/var/lib/mestre/contas/`.
- **A confirmar:** a verificação da árvore de processos funciona com os provedores que iniciam subprocessos por shell intermediário (ex.: `sh -c`, `cmd /c` no Antigravity).
