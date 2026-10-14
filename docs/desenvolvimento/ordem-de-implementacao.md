# Ordem de implementação

> **Assunto:** marcos de desenvolvimento (M0 a M7), o marco de cada documento de funcionalidade, a auditoria que fecha cada marco, as partes que chegam em marco posterior e a regra de "Depende de" · **Componente:** Projeto · **Atualizado:** 2026-10-14

Este documento é a **fonte única** da ordem de desenvolvimento. Cada documento de funcionalidade (todo documento com a seção "Objetivo") pertence a exatamente um marco, listado no [mapa](#mapa-de-documentos-por-marco-e-etapa). A [verificação de docs](../qualidade/verificacao-de-docs.md) lê o mapa e confere a regra abaixo.

## Regra de "Depende de"

- A seção "Depende de" de um documento de funcionalidade lista **só o que precisa existir antes** para implementá-lo: documentos do **mesmo marco ou de um marco anterior**. Nunca um documento de marco posterior.
- O grafo formado por essas seções **não tem ciclos**. Dentro de um marco, a ordem de implementação é a ordem do grafo.
- Ligações de "ver também" (quem usa o documento, onde o assunto continua, o que chega em marco posterior) ficam no corpo do texto, nunca em "Depende de".
- Documentos de referência (`docs/arquitetura/` sem a seção "Objetivo", `docs/produto/`, `docs/decisoes/`, `docs/desenvolvimento/`, `docs/operacao/`) não pertencem a marco: valem desde o M0 e podem aparecer em "Depende de" de qualquer documento, sem contar para o grafo.
- Um documento de funcionalidade novo entra no mapa na mesma alteração que o cria; mudar um documento de marco exige conferir os "Depende de" de quem aponta para ele.

## Marcos

| Marco | Nome | Auditoria | O que fica pronto ao final |
|---|---|---|---|
| M0 | Fundação | 2026-10-13 | executor das suítes (`tools/testrun`), verificação de docs (`tools/docscheck`), dublês e `testkit` (relógio injetável, rede, PTY, git, Telegram e GitHub falsos, agente falso), projetos de treino (`testkit.NewProject`), `logging` com [mascaramento](../arquitetura/seguranca.md#mascaramento-em-logs) |
| M1 | Base local | 2026-10-13 | CI com matriz Linux e Windows e a `main` protegida ([testes](../qualidade/testes.md#como-as-suítes-são-selecionadas)), banco e migrações ([persistência](../arquitetura/persistencia.md)), cofre (DPAPI no Windows e arquivo `0600` no Linux), terminal (`mestre-pty` com PTY/ConPTY, emulador e buffer, volta do motor sem parar os agentes, e o `mestre-launcher`), `internal/gitx` (wrapper do binário `git`), canal local CLI ↔ motor |
| M2 | Transporte e Guilda | 2026-10-13 | TLS e CA da Guilda ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)), protocolo WSS com época, descoberta da Guilda, entrada e exclusão de máquinas, instâncias múltiplas, ficha da máquina, replicação do banco, dos arquivos e do cofre, e época, fencing e troca de Cidadela ([Cidadela e Torres](../guilda/cidadela-e-torres.md)) atrás da interface da trava |
| M3 | CLI e provedores |  | CLI (convenções), adaptador e `mestre hook`, [precedência](../arquitetura/precedencia.md), permissões, reforço nativo, classes, teto, os três provedores e o teste de conformidade |
| M4 | Orquestração |  | workspaces, sessão zero, mesas, personagens, conexões, esperas e caixa de entrada, missões, pergaminhos, rituais, grimórios, bases e fichas, comandos de comunicação e coordenação, skills geradas |
| M5 | Integrações |  | GitHub e a [trava no GitHub](../guilda/trava-no-github.md) (primeiro marco com ação externa; antes dele não há promoção), Telegram, usuários e níveis, decretos, aprovações, Grão-Mestre, Mestre da mesa, anexos e transcrição, limites de uso |
| M6 | Painel |  | login, canvas, visão do workspace, conversa e aprovações, administração |
| M7 | Plataforma, autocura e Operador do PC |  | instalação, checklist, Guardião, camadas, diagnóstico com IA, página reserva, Telegram reduzido, autoatualização, detecção de versão nova dos CLIs; por último, o [Operador do PC](#operador-do-pc-no-m7); suíte pesada (MVP-1 a MVP-15) |

- **Guardião em paralelo:** [guardiao.md](../autocura/guardiao.md) depende só de documentos até o M2 e pode começar logo depois do M2, em paralelo com M3 a M6.
- **Escolhas já feitas:** a biblioteca de mDNS e o gerador de tipos TypeScript ([convenções](convencoes.md#código-gerado)). O modo de captura das mudanças do banco saiu do spike do M2: registro lógico (`replication_log`) ([regras de execução](#regras-de-execução-da-ordem)).

### Tudo roda nos dois sistemas

Qualquer máquina da Guilda pode virar Cidadela ([ADR 0022](../decisoes/0022-guilda-cidadela-e-torres.md)), então **nada do Mestre é "só Linux"**: painel, bot do Telegram, `whisper-server` e `ffmpeg`, banco, GitHub, cofre e replicação precisam funcionar também no Windows, desde o marco em que nascem.

- **Pronto, em cada marco,** inclui a suíte completa passando no CI Windows, além do Linux, desde o M1 ([testes](../qualidade/testes.md)): o cofre com DPAPI, o banco e o `mestre-launcher` no M1; protocolo, descoberta, entrada na Guilda, replicação e troca de Cidadela (com instâncias múltiplas) no M2; GitHub, Telegram (falso) e transcrição no M5; o painel servido pelo motor no M6.
- O que depende de serviço instalado ou de modelo real (Whisper com o modelo, mDNS do sistema) fecha na pesada, com a Cidadela em cada sistema ([laboratório](../qualidade/testes.md#laboratório)).
- Um teste da completa que só roda num sistema precisa dizer por quê (ConPTY, systemd) e ter o par no outro sistema.

### Operador do PC no M7

O [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) ([ADR 0021](../decisoes/0021-operador-do-pc.md)) é implementado no fim do M7, depois da instalação e do checklist, porque precisa de tudo antes: permissões e teto (M3), bases e personagens (M4), aprovações, decretos e Telegram (M5), painel (M6), instalação e checklist (M7). As partes dele ficam nos documentos donos, que continuam no marco de origem no mapa; os critérios abaixo é que são implementados no M7. Começa por um spike na VM Windows com os três itens de "Verificar na implementação" do Operador em [Windows](../arquitetura/windows.md#verificar-na-implementação).

| Documento (marco no mapa) | Critérios implementados no M7 |
|---|---|
| [permissões](../classes/permissoes.md) (M3) | CA-PERM-14, CA-PERM-15 |
| [teto do workspace](../classes/teto-do-workspace.md) (M3) | CA-TETO-06 |
| [bases e fichas](../orquestracao/bases-e-fichas.md) (M4) | CA-BF-06, CA-BF-07, CA-BF-08 |
| [política](../aprovacoes/politica.md), [fluxo](../aprovacoes/fluxo.md), [decretos](../telegram/decretos.md) (M5) | CA-APROV-POL-10, CA-APROV-FLUXO-11, CA-DEC-10 |
| [bot](../telegram/bot.md), [segurança do canal](../telegram/seguranca-do-canal.md) (M5) | CA-BOT-10, CA-CANAL-08 |
| [canvas](../painel/canvas.md), [administração](../painel/administracao.md) (M6) | CA-CANVAS-08, CA-ADM-09 |
| [instalação](../plataforma/instalacao-e-atualizacao.md), [checklist](../plataforma/checklist.md) (M7) | CA-INST-14, CA-CHK-15 (e CA-INST-11 ganha a exceção do Operador) |

### Critérios em marcos posteriores

Critérios de documentos de um marco que usam peças de marcos seguintes; cada um é implementado no marco indicado, e o documento continua no marco de origem no mapa. "Guardião" quer dizer quando o [Guardião](../autocura/guardiao.md) estiver pronto (depois do M2, até a etapa 1 do M7).

| Documento (marco no mapa) | Critério → marco em que fecha |
|---|---|
| [dublês](../qualidade/dubles.md) (M0) | CA-DUBLES-08 → M2 (Torre real do protocolo); CA-DUBLES-11 → M4 (personagens) |
| [cofre](../plataforma/cofre.md) (M1) | CA-COFRE-09, CA-COFRE-18 e CA-COFRE-21 → M4 (personagens e MCP de navegador; o pedido da Torre pelo protocolo precisa da identidade do agente e da classe do personagem para decidir a liberação); CA-COFRE-10 a CA-COFRE-17 → M5 (bot, com CA-BOT-09); CA-COFRE-07 e CA-COFRE-20 → M6 (rotas do painel e administração); CA-COFRE-05 → Guardião (réplica e página reserva); CA-COFRE-19 → M7 (instalação) |
| [terminal](../arquitetura/terminal.md) (M1) | CA-TERM-02 com as gravações reais de tela dos provedores e o item "Verificar" do `x/vt` → M3 (conformidade; no M1 a tela é falsa); a parte de CA-TERM-05, CA-TERM-07, CA-TERM-11 e CA-TERM-12 que exige logon real da conta de agentes ou troca de conta (varredura da sessão de logon, DACL entre dois terminais, `kill` do launcher como `mestre-agente`) já roda nas [contas reais](../qualidade/contas-reais.md) (CI), sem esperar o M7; o instalador aplicar as ACLs de `run\` e `pty\` no Windows e a unidade do launcher (`RuntimeDirectory=` e o resto) → M7 (instalação) |
| [canal local](../arquitetura/canal-local.md) (M1) | CA-LOCAL-04 com a entrega na caixa de entrada real → M4 (esperas e caixa de entrada; no M1 o destino é um dublê) |
| [protocolo](../conectividade/protocolo.md) (M2) | CA-PROTO-06 → M4 (precisa de uma etapa de missão e dos terminais ligados ao protocolo, que o M2 não tem); CA-PROTO-08 → M4 (esperas); CA-PROTO-09 → M6 (painel); CA-PROTO-07 → Guardião |
| [Guilda](../guilda/guilda.md) (M2) | CA-GUILDA-02, CA-GUILDA-09 e CA-GUILDA-12 → M5 (aprovação e PIN do Dono pelo bot); CA-GUILDA-06 → Guardião; CA-GUILDA-08 → M7 (instalação) |
| [ficha da máquina](../guilda/ficha-da-maquina.md) (M2) | CA-FMAQ-03 → M4 (fichas e perfis de conta) |
| [replicação](../guilda/replicacao.md) (M2) | CA-REPL-08 → M4 (espelho do workspace); CA-REPL-06 → M7 (checklist) |
| [Cidadela e Torres](../guilda/cidadela-e-torres.md) (M2) | CA-CIDADELA-10, CA-CIDADELA-11 e CA-CIDADELA-13 → M4 (personagens, missões, esperas); CA-CIDADELA-03 e CA-CIDADELA-12 → M5 (ações no GitHub e trava); CA-CIDADELA-08 → M6 (painel); CA-CIDADELA-05, CA-CIDADELA-06 e CA-CIDADELA-07 → M7 (Torre condutora, Telegram em modo reduzido, página reserva) |
| [convenções da CLI](../cli/convencoes.md) (M3) | CA-CLI-CONV-04, CA-CLI-CONV-05 e CA-CLI-CONV-07 → M4 (`ask`, `done` e `ask --raw` com personagens, terminais e duas máquinas; o M3 testa a fila de saída, a recusa do `--raw` e o `PENDING:` com `// Part of …`); CA-CLI-CONV-06, CA-CLI-CONV-08, CA-CLI-CONV-10 e CA-CLI-CONV-11 → M5 (aprovação `acao_destrutiva`, `table`/`gh`/`app`/`notify`, `reply` do Grão-Mestre e `pr open` com a trava; o M3 testa o filtro do `help`, a recusa de segredo e o `queued` com `// Part of …`); [classes](../classes/classes.md) (M3): CA-CLASSES-04 → M4 (pergaminho da missão e o agente falso numa missão; o M3 testa o hook assíncrono, um por personagem de cada vez, e a saída entregue, com `// Part of …`); CA-CLASSES-06 → M5 (o fluxo de aprovação e o Grão-Mestre; o M3 testa a regra de quem propõe e quem aprova) |

### Critérios antecipados

O inverso da tabela acima: critérios de documento de marco posterior que um marco anterior já testa, por causa da "casca" dos comandos ([regras de execução](#regras-de-execução-da-ordem)). O teste leva o marcador `// CA-…` desde já, e a [regra de auditoria](#como-usar-no-dia-a-dia) não conta esse marcador como código do marco do documento. Hoje: das [convenções da CLI](../cli/convencoes.md) (M3), CA-CLI-CONV-01 a CA-CLI-CONV-03 e CA-CLI-CONV-09 (o canal local do M1).

### Spikes do M1

Os resultados dos spikes valem para o serviço único `mestre` ([ADR 0022](../decisoes/0022-guilda-cidadela-e-torres.md)). Os spikes de Windows rodam no **Windows Sandbox** do PC do Dono, descartável a cada abertura ([como preparar e rodar](ambiente.md#windows-sandbox-para-os-spikes-do-m1)); a VM Windows dedicada continua só para o [laboratório](../qualidade/testes.md#laboratório) da suíte pesada. Os de Linux rodam nesta máquina de desenvolvimento. São itens de "Verificar na implementação" que mudam camadas de base se falharem, testados antes de fechar o terminal e o canal local:

| Spike | Resultado ou plano se falhar | Onde está o item |
|---|---|---|
| ConPTY como `mestre-agente` a partir do serviço, na sessão 0: **feito** (2026-10-13) | falhou no padrão (`LoadUserProfile` recusado à conta virtual); adotado o `mestre-launcher` mínimo, que entrega o token | [Windows](../arquitetura/windows.md#resultado-dos-spikes-do-m1) |
| conta virtual `NT SERVICE\Mestre` com `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege`: **feito** (2026-10-13) | passou; com o launcher criando o `mestre-pty`, os dois deixaram de ser necessários, e ficou a declaração dos privilégios exigidos | [Windows](../arquitetura/windows.md#serviços-no-windows) |
| `claude`, `codex` e `agy` com o login no perfil do `mestre-agente` no Windows: **feito** (2026-10-13) | passou; `agy` no Gerenciador de Credenciais (um perfil Antigravity por máquina Windows); na rodada 2, o `agy` manteve o login; a confirmação no `mestre-pty` vai no spike 5 | [Windows](../arquitetura/windows.md#verificar-na-implementação) |
| identificação do chamador subindo a árvore de processos, no Windows: **feito** (2026-10-12) | a árvore falhou para órfãos; adotado o Job Object do terminal, perguntado ao `mestre-pty` | [segurança](../arquitetura/seguranca.md#identidade-de-quem-chama-a-cli) |
| `mestre-pty` no Windows (spike 5): iniciado pelo launcher, `go-pty` com ConPTY, socket Unix com ACL, motor e launcher reiniciados com o agente vivo, Job Object, processo do WMI: **feito** (2026-10-13) | tudo passou no padrão, sem os planos B | [Windows](../arquitetura/windows.md#resultado-dos-spikes-do-m1) |
| identificação do chamador, no Linux: **feito** (2026-10-12) | a árvore falhou para órfãos; adotado um cgroup v2 por terminal | [segurança](../arquitetura/seguranca.md#identidade-de-quem-chama-a-cli) |
| `claude`, `codex` e `agy` como `mestre-agente` no Linux: **feito** (2026-10-13) | funcionam só com o perfil de conta, credencial em arquivo `0600`; o Codex exige `--no-daemon` | [segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes) |

## Mapa de documentos por marco e etapa

Tabela lida pela verificação de docs; é a **única** lista de documentos por marco. Cada link é um documento de funcionalidade, em exatamente uma linha. Dentro de um marco, as etapas seguem os "Depende de": um documento fica numa etapa **maior** que a de cada dependência do mesmo marco, e documentos da mesma etapa podem ser feitos **em paralelo**. A ordem não é escrita à mão em outro lugar: quem muda um "Depende de" ajusta a etapa aqui na mesma alteração, e a [verificação de docs](../qualidade/verificacao-de-docs.md#checagem-da-ordem-de-implementação) falha se a tabela ficar incoerente.

| Marco | Etapa | Documentos |
|---|---|---|
| M0 | 1 | [testes](../qualidade/testes.md) |
| M0 | 2 | [dublês](../qualidade/dubles.md) |
| M0 | 3 | [verificação de docs](../qualidade/verificacao-de-docs.md) · [projetos de treino](../qualidade/projetos-de-treino.md) · [regras de código](../qualidade/regras-de-codigo.md) |
| M1 | 1 | [terminal](../arquitetura/terminal.md) · [cofre](../plataforma/cofre.md) |
| M1 | 2 | [canal local](../arquitetura/canal-local.md) · [contas reais](../qualidade/contas-reais.md) |
| M2 | 1 | [protocolo](../conectividade/protocolo.md) |
| M2 | 2 | [descoberta](../conectividade/descoberta.md) |
| M2 | 3 | [Guilda](../guilda/guilda.md) |
| M2 | 4 | [ficha da máquina](../guilda/ficha-da-maquina.md) · [replicação](../guilda/replicacao.md) |
| M2 | 5 | [Cidadela e Torres](../guilda/cidadela-e-torres.md) |
| M3 | 1 | [adaptador](../provedores/adaptador.md) |
| M3 | 2 | [permissões](../classes/permissoes.md) |
| M3 | 3 | [reforço nativo](../classes/reforco-nativo.md) · [teto do workspace](../classes/teto-do-workspace.md) · [convenções da CLI](../cli/convencoes.md) · [despachante de hooks](../provedores/despachante-de-hooks.md) |
| M3 | 4 | [classes](../classes/classes.md) · [Claude Code](../provedores/claude-code.md) · [Codex](../provedores/codex.md) · [Antigravity](../provedores/antigravity.md) |
| M3 | 5 | [conformidade](../provedores/conformidade.md) |
| M4 | 1 | [workspaces](../orquestracao/workspaces.md) · [bases e fichas](../orquestracao/bases-e-fichas.md) |
| M4 | 2 | [sessão zero do workspace](../sessao-zero/workspace.md) |
| M4 | 3 | [sessão zero da mesa](../sessao-zero/mesa.md) |
| M4 | 4 | [sessão zero do personagem](../sessao-zero/personagem.md) |
| M4 | 5 | [personagens](../orquestracao/personagens.md) |
| M4 | 6 | [conexões](../orquestracao/conexoes.md) |
| M4 | 7 | [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) · [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| M4 | 8 | [mesas](../orquestracao/mesas.md) |
| M4 | 9 | [missões](../orquestracao/missoes.md) |
| M4 | 10 | [CLI de comunicação](../cli/comunicacao.md) · [CLI de coordenação](../cli/coordenacao.md) · [grimórios](../orquestracao/grimorios.md) · [rituais](../orquestracao/rituais.md) |
| M4 | 11 | [CLI de classes e rituais](../cli/coordenacao-classes-e-rituais.md) |
| M4 | 12 | [skills geradas](../cli/skills-geradas.md) |
| M5 | 1 | [identidades e Apps](../github/identidades-e-apps.md) · [usuários e níveis](../telegram/usuarios-e-niveis.md) |
| M5 | 2 | [política de aprovação](../aprovacoes/politica.md) · [ações por nível](../github/acoes-por-nivel.md) · [bot](../telegram/bot.md) · [trava no GitHub](../guilda/trava-no-github.md) |
| M5 | 3 | [fluxo de aprovação](../aprovacoes/fluxo.md) · [CLI de issues e PRs](../cli/coordenacao-github.md) · [criação de Apps](../github/criacao-de-apps.md) · [eventos do GitHub](../github/eventos.md) · [segurança do canal](../telegram/seguranca-do-canal.md) |
| M5 | 4 | [limites de uso](../plataforma/limites-de-uso.md) · [decretos](../telegram/decretos.md) · [roteamento](../telegram/roteamento.md) |
| M5 | 5 | [aprovação no GitHub](../github/aprovacao-no-github.md) · [Grão-Mestre](../orquestracao/grao-mestre.md) · [anexos e transcrição](../telegram/anexos-e-transcricao.md) |
| M5 | 6 | [CLI do Grão-Mestre](../cli/grao-mestre.md) · [Mestre da mesa](../orquestracao/mestre-da-mesa.md) |
| M6 | 1 | [login](../painel/login.md) |
| M6 | 2 | [administração](../painel/administracao.md) · [canvas](../painel/canvas.md) · [conversa e aprovações](../painel/conversa-e-aprovacoes.md) |
| M6 | 3 | [visão do workspace](../painel/visao-do-workspace.md) · [`mestre attach`](../cli/attach.md) |
| M7 | 1 | [Guardião](../autocura/guardiao.md) · [instalação e atualização](../plataforma/instalacao-e-atualizacao.md) |
| M7 | 2 | [camadas](../autocura/camadas.md) · [checklist](../plataforma/checklist.md) |
| M7 | 3 | [página reserva](../autocura/pagina-reserva.md) · [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) · [autoatualização](../plataforma/autoatualizacao.md) |
| M7 | 4 | [diagnóstico com IA](../autocura/diagnostico-com-ia.md) · [atualização dos CLIs](../provedores/atualizacao-dos-clis.md) |

### Regras de execução da ordem

- **M0, além dos documentos:** layout do repositório em Go ([convenções](convencoes.md)) e o `logging` com mascaramento, antes da etapa 1.
- **M1:** o CI (`.github/workflows/ci.yml`, matriz `ubuntu-latest` e `windows-latest`) e o ruleset da `main` que o exige ([passo do Dono](ambiente.md#ci-e-proteção-da-main)) entram antes da etapa 1; os [spikes](#spikes-do-m1) de Linux vêm antes da etapa 1, e as partes de Linux do M1 andam primeiro, com o início de processos atrás de uma interface (`internal/terminal`, `Launcher`) que o Windows implementa depois. Os três primeiros spikes de Windows já rodaram; o da identificação do chamador (4) rodou na rodada 2 e o do `mestre-pty` (5) na rodada 3: os spikes de Windows do M1 estão fechados, e o `winsvc` (launcher, ACLs, DPAPI) pode começar. O `mestre-pty` é código comum aos dois sistemas e anda já no M1, com o CI Windows rodando-o como processo comum; o que depende do launcher espera o spike 5. banco e migrações ([persistência](../arquitetura/persistencia.md)) e `internal/gitx` entram junto da etapa 1.
- **M2:** começou pelo spike de captura das mudanças do banco, nos dois sistemas, **antes da replicação**: **feito** (2026-10-13): ler os frames do WAL funcionou no Linux e no Windows (CI `windows-latest`), mas não filtra o cofre para a excluída, não aceita `VACUUM INTO` como base e não lista as transações descartadas; adotado o plano B, o `replication_log` na mesma transação, pelo *preupdate hook* ([replicação](../guilda/replicacao.md#banco-cópia-completa-e-envio-contínuo)). TLS e CA da Guilda entram junto da etapa 1; a época vai no envelope desde a etapa 1, e a certidão nasce com a [Guilda](../guilda/guilda.md) (etapa 3). Época, fencing e troca de Cidadela (etapa 5) chegam com a replicação, atrás de uma interface da trava; nos testes do M2 a trava é a falsa do `testkit`, e a [trava no GitHub](../guilda/trava-no-github.md) chega no M5: até lá não há promoção numa Guilda real. A etapa 3 (`internal/guild`) fez a lógica da Guilda atrás de interfaces: `Approver` (a pergunta ao Dono), `Wiper` (apagar o que a máquina que sai guarda), `Peers` (o protocolo) e os ganchos da exclusão e da remoção; o botão com PIN (CA-GUILDA-02, 09 e 12) e o instalador (CA-GUILDA-08) fecham nos marcos da tabela acima. A etapa 5 fez a troca atrás da interface `guild.Lock` e o motor de cada instância em `internal/app` (`mestre run`): o teste de troca da completa roda três [instâncias](../arquitetura/componentes.md#instâncias) como processos comuns, com o relógio, a trava e o Dono falsos; os Guardiões entram nele quando o [Guardião](../autocura/guardiao.md) existir, e a ordem de promoção (`citadel.promote`) vem da condutora no M7 (até lá, um gancho do teste), junto com a recusa de promover a máquina fora da rede local (é a condutora que sabe quem ela alcança; parte de CA-CIDADELA-05). Os testes que cobrem hoje só uma parte de um critério de marco posterior levam `// Part of CA-…` ([rastreabilidade](../qualidade/testes.md#regras-e-erros)). Depois do M2, o [Guardião](../autocura/guardiao.md) pode começar em paralelo com M3 a M6 (a etapa 1 do M7 só confere que ele está pronto).
- **M3, etapa 2:** as [permissões](../classes/permissoes.md) ficam em `internal/classes` (formato, regras fixas, regras efetivas e `agent.rules` guardado na máquina, decisão e modo degradado); teto, ficha, decretos e exceções entram como dados de entrada, e as camadas chegam no marco delas. CA-PERM-05 e CA-PERM-07 fecharam na etapa 3, com o [despachante](../provedores/despachante-de-hooks.md) e o agente falso. **Etapa 3, teto e reforço nativo:** o [teto](../classes/teto-do-workspace.md) e o [reforço nativo](../classes/reforco-nativo.md) ficam em `internal/classes`, com a tradução de cada provedor em `internal/providers`. As peças de marcos seguintes entram por interfaces pequenas, com dublês nos testes: o aviso aos Administradores (`AdminAlerts`), o pedido aos usuários do fluxo de aprovação (`UserAsker`, CA-TETO-04) e o reinício com retomada dos personagens (`Restarter`, `Activity`, CA-NATIVO-03). CA-TETO-06 tem hoje um teste `// Part of …` (o corte de `operator` pelo teto padrão e a recusa do Administrador) e fecha no M7. **Etapa 3, despachante:** `mestre hook` e o lado do motor ficam em `internal/hook`; o registro de terminais (M4), as aprovações (M5), o estado "aguardando permissão" e o contexto do turno (M4) entram como interfaces (`Characters`, `Approvals`, `States`, `Contexts`), com dublês nos testes, e os hooks da classe (`ClassHooks`) com as classes (etapa 4). Como o `mestre run` só cria terminais no M4, CA-HOOK-07 e CA-PERM-05 rodam com o lado do motor real (canal local num socket, identificação real do chamador, pipeline sobre o banco da máquina) e o agente falso num PTY real. O instalador (M7) aplica o plano de pastas `LinuxPlan` (CA-HOOK-06); dar o grupo `mestre-agente` às entradas de cada personagem é do motor, com a criação do personagem (M4). **Etapa 4, classes:** o pacote, os escopos, as propostas e o `class check` ficam em `internal/classes` (`Library`; o banco da Guilda guarda a versão aprovada de cada classe e as propostas) e os hooks extras em `internal/hook` (`ClassHookSource`, `AsyncRunner`). `mestre class check` é casca (o `Spec` e o `CheckCommand`, que o motor liga quando existir o registro de personagens, M4); a CLI de classes é a etapa 11 do M4. O estado dos Apps (`AppDirectory`), os avisos aos usuários (`ClassAlerts`), o processo dos hooks assíncronos (`HookExec`) e a saída deles (`HookOutputs`) entram por interfaces, e a aprovação é só a regra de quem pode (`Author`), sem o fluxo. CA-FMAQ-04 fechou aqui.
- **Conformidade real no fim do M3:** depois da etapa 5, uma primeira rodada do [teste de conformidade](../provedores/conformidade.md) com os CLIs reais nesta máquina Linux, para achar cedo mudanças dos provedores; a rodada completa, nos dois sistemas, é a da suíte pesada no laboratório.
- **"Casca" dos comandos da CLI:** um comando que outro documento usa antes da sua etapa (ex.: `mestre done` nas [missões](../orquestracao/missoes.md), etapa 9, antes da [CLI de comunicação](../cli/comunicacao.md), etapa 10) é implementado como casca junto do documento que o usa: sintaxe, chamada pelo [canal local](../arquitetura/canal-local.md) e o caminho feliz. Os critérios de aceite do documento da CLI (erros, códigos de saída, `--json`) fecham na etapa dele.
- **M7:** com a autocura pronta (camadas, Telegram em modo reduzido, página reserva) fecham os critérios da troca de Cidadela que dependem dela ([acima](#critérios-em-marcos-posteriores)); depois da etapa 4, o [Operador do PC](#operador-do-pc-no-m7) e, por último, a suíte pesada (MVP-1 a MVP-15).

## Como usar no dia a dia

- Comece um marco só com o anterior pronto, salvo o Guardião, acima. **Pronto** quer dizer: critérios de aceite das suítes rápida e completa passando **e auditado**.
- **Auditoria do marco:** antes de o marco seguinte começar, revisores conferem que o teste de cada critério afirma o que o critério diz (não só que existe um teste marcado), olhando primeiro a segurança e os dois sistemas; os achados são corrigidos antes de o marco seguinte começar. A **data** da auditoria (`AAAA-MM-DD`) entra na coluna "Auditoria" da tabela de [marcos](#marcos); é só a data, o relatório não vai para o repositório (histórico fica no controle de versão). A [verificação de docs](../qualidade/verificacao-de-docs.md#checagens-de-código-e-de-marcos) exige a cobertura dos critérios de um marco auditado e recusa código de um marco cujo anterior ficou sem data.
- Dentro do marco, implemente na ordem do grafo de "Depende de"; documentos sem dependência entre si podem andar em paralelo.
- Os critérios *pesada* (MVP-1 a MVP-15) fecham no M7, quando instalação, autocura e laboratório existem ([testes](../qualidade/testes.md#mapa-do-mvp-para-a-suíte-pesada)).
