# Ordem de implementação

> **Assunto:** marcos de desenvolvimento (M0 a M7), o marco de cada documento de funcionalidade, as partes que chegam em marco posterior e a regra de "Depende de" · **Componente:** Projeto · **Atualizado:** 2026-10-13

Este documento é a **fonte única** da ordem de desenvolvimento. Cada documento de funcionalidade (todo documento com a seção "Objetivo") pertence a exatamente um marco, listado no [mapa](#mapa-de-documentos-por-marco-e-etapa). A [verificação de docs](../qualidade/verificacao-de-docs.md) lê o mapa e confere a regra abaixo.

## Regra de "Depende de"

- A seção "Depende de" de um documento de funcionalidade lista **só o que precisa existir antes** para implementá-lo: documentos do **mesmo marco ou de um marco anterior**. Nunca um documento de marco posterior.
- O grafo formado por essas seções **não tem ciclos**. Dentro de um marco, a ordem de implementação é a ordem do grafo.
- Ligações de "ver também" (quem usa o documento, onde o assunto continua, o que chega em marco posterior) ficam no corpo do texto, nunca em "Depende de".
- Documentos de referência (`docs/arquitetura/` sem a seção "Objetivo", `docs/produto/`, `docs/decisoes/`, `docs/desenvolvimento/`, `docs/operacao/`) não pertencem a marco: valem desde o M0 e podem aparecer em "Depende de" de qualquer documento, sem contar para o grafo.
- Um documento de funcionalidade novo entra no mapa na mesma alteração que o cria; mudar um documento de marco exige conferir os "Depende de" de quem aponta para ele.

## Marcos

| Marco | Nome | O que fica pronto ao final |
|---|---|---|
| M0 | Fundação | executor das suítes (`tools/testrun`), verificação de docs (`tools/docscheck`), dublês e `testkit` (relógio injetável, rede, PTY, git, Telegram e GitHub falsos, agente falso), projetos de treino (`testkit.NewProject`), `logging` com [mascaramento](../arquitetura/seguranca.md#mascaramento-em-logs) |
| M1 | Base local | banco e migrações ([persistência](../arquitetura/persistencia.md)), cofre, terminal (PTY/ConPTY e emulador), `internal/gitx` (wrapper do binário `git`), canal local CLI ↔ motor |
| M2 | Transporte | TLS e CA interna ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)), protocolo WSS, pareamento e descoberta |
| M3 | CLI e provedores | CLI (convenções), adaptador e `mestre hook`, [precedência](../arquitetura/precedencia.md), permissões, reforço nativo, classes, teto, os três provedores e o teste de conformidade |
| M4 | Orquestração | workspaces, sessão zero, mesas, personagens, conexões, esperas e caixa de entrada, missões, pergaminhos, rituais, grimórios, bases e fichas, comandos de comunicação e coordenação, skills geradas |
| M5 | Integrações | GitHub, Telegram, usuários e níveis, decretos, aprovações, Grão-Mestre, Mestre da mesa, anexos e transcrição, limites de uso |
| M6 | Painel | login, canvas, visão do workspace, conversa e aprovações, administração |
| M7 | Plataforma, autocura e Operador do PC | instalação, checklist, Guardião, camadas, diagnóstico com IA, página reserva, Telegram reduzido, autoatualização, atualização dos CLIs; por último, o [Operador do PC](#operador-do-pc-no-m7); suíte pesada (MVP-1 a MVP-14) |

- **Guardião em paralelo:** [guardiao.md](../autocura/guardiao.md) depende só de documentos até o M2 e pode começar logo depois do M2, em paralelo com M3 a M6.
- **Escolhas antes de cada marco:** biblioteca de mDNS antes do M2 e gerador de tipos TypeScript antes do M6 ([convenções](convencoes.md#código-gerado)).

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

### Critérios do cofre em marcos posteriores

O [cofre](../plataforma/cofre.md) é do M1, mas o segundo fator, o filtro de segredos nas mensagens e os avisos de validade usam o Telegram, o Grão-Mestre e a caixa de entrada: CA-COFRE-10 a CA-COFRE-17 são implementados no M5, junto do [bot](../telegram/bot.md) (com CA-BOT-09), CA-COFRE-18 no M4, junto das [bases e fichas](../orquestracao/bases-e-fichas.md) (MCP de navegador do personagem), CA-COFRE-20 no M6, com a [administração](../painel/administracao.md), e CA-COFRE-19 no M7, com a [instalação](../plataforma/instalacao-e-atualizacao.md).

### Critérios dos dublês em marcos posteriores

Os [dublês](../qualidade/dubles.md) são do M0, mas dois critérios dependem de peças de marcos seguintes: CA-DUBLES-08 fecha no M2, quando o nó real do [protocolo](../conectividade/protocolo.md) substitui o cliente de teste que reconecta com o mesmo backoff, e CA-DUBLES-11 fecha no M4, com os [personagens](../orquestracao/personagens.md) (precisa do terminal real do M1 e do `mestre hook` do M3).

### Spikes do M1

Os spikes de Windows rodam no **Windows Sandbox** do PC do Dono, descartável a cada abertura ([como preparar e rodar](ambiente.md#windows-sandbox-para-os-spikes-do-m1)); a VM Windows dedicada continua só para o [laboratório](../qualidade/testes.md#laboratório) da suíte pesada. Os de Linux rodam nesta máquina de desenvolvimento. Seis itens de "Verificar na implementação" mudam camadas de base se falharem; são testados no começo do M1, antes de fechar o terminal e o canal local:

| Spike | Se falhar, muda | Onde está o item |
|---|---|---|
| ConPTY com o processo iniciado como `mestre-agente` (`CreateProcessAsUser`, perfil carregado) a partir do serviço, na sessão 0 | o serviço auxiliar `mestre-launcher` (LocalSystem) inicia os processos; ou a distribuição passa a levar o `OpenConsole.exe` | [Windows](../arquitetura/windows.md#verificar-na-implementação) |
| conta virtual `NT SERVICE\MestreNode` com `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege` | o serviço auxiliar `mestre-launcher` | [Windows](../arquitetura/windows.md#verificar-na-implementação) |
| `claude`, `codex` e `agy` gravando e achando o login no perfil do `mestre-agente` no Windows | login em arquivo no perfil de conta, ou o provedor não é oferecido naquela máquina | [Windows](../arquitetura/windows.md#verificar-na-implementação) |
| identificação do chamador subindo a árvore de processos, no Windows | um Job Object por terminal na verificação | [segurança](../arquitetura/seguranca.md#verificar-na-implementação) |
| identificação do chamador, no Linux: **feito** (2026-10-12); a árvore falhou para órfãos (`nohup … &`, `setsid -f`, tmux) | adotado um cgroup v2 por terminal | [segurança](../arquitetura/seguranca.md#identidade-de-quem-chama-a-cli) |
| `claude`, `codex` e `agy` rodando como `mestre-agente` no Linux: **feito** (2026-10-13); os três funcionam só com o perfil de conta, sem pasta pessoal, com credencial em arquivo `0600`; o Codex exige `--no-daemon` | dispensada a pasta pessoal própria para a conta dos agentes | [segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes) |

## Mapa de documentos por marco e etapa

Tabela lida pela verificação de docs; é a **única** lista de documentos por marco. Cada link é um documento de funcionalidade, em exatamente uma linha. Dentro de um marco, as etapas seguem os "Depende de": um documento fica numa etapa **maior** que a de cada dependência do mesmo marco, e documentos da mesma etapa podem ser feitos **em paralelo**. A ordem não é escrita à mão em outro lugar: quem muda um "Depende de" ajusta a etapa aqui na mesma alteração, e a [verificação de docs](../qualidade/verificacao-de-docs.md#checagem-da-ordem-de-implementação) falha se a tabela ficar incoerente.

| Marco | Etapa | Documentos |
|---|---|---|
| M0 | 1 | [testes](../qualidade/testes.md) |
| M0 | 2 | [dublês](../qualidade/dubles.md) |
| M0 | 3 | [verificação de docs](../qualidade/verificacao-de-docs.md) · [projetos de treino](../qualidade/projetos-de-treino.md) |
| M1 | 1 | [terminal](../arquitetura/terminal.md) · [cofre](../plataforma/cofre.md) |
| M1 | 2 | [canal local](../arquitetura/canal-local.md) |
| M2 | 1 | [protocolo](../conectividade/protocolo.md) |
| M2 | 2 | [descoberta](../conectividade/descoberta.md) |
| M2 | 3 | [pareamento](../conectividade/pareamento.md) |
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
| M5 | 2 | [política de aprovação](../aprovacoes/politica.md) · [ações por nível](../github/acoes-por-nivel.md) · [bot](../telegram/bot.md) |
| M5 | 3 | [fluxo de aprovação](../aprovacoes/fluxo.md) · [CLI de issues e PRs](../cli/coordenacao-github.md) · [criação de Apps](../github/criacao-de-apps.md) · [eventos do GitHub](../github/eventos.md) · [segurança do canal](../telegram/seguranca-do-canal.md) |
| M5 | 4 | [limites de uso](../plataforma/limites-de-uso.md) · [decretos](../telegram/decretos.md) · [roteamento](../telegram/roteamento.md) |
| M5 | 5 | [aprovação no GitHub](../github/aprovacao-no-github.md) · [Grão-Mestre](../orquestracao/grao-mestre.md) · [anexos e transcrição](../telegram/anexos-e-transcricao.md) |
| M5 | 6 | [CLI do Grão-Mestre](../cli/grao-mestre.md) · [Mestre da mesa](../orquestracao/mestre-da-mesa.md) |
| M6 | 1 | [login](../painel/login.md) |
| M6 | 2 | [administração](../painel/administracao.md) · [canvas](../painel/canvas.md) · [conversa e aprovações](../painel/conversa-e-aprovacoes.md) |
| M6 | 3 | [visão do workspace](../painel/visao-do-workspace.md) |
| M7 | 1 | [Guardião](../autocura/guardiao.md) · [instalação e atualização](../plataforma/instalacao-e-atualizacao.md) |
| M7 | 2 | [camadas](../autocura/camadas.md) · [checklist](../plataforma/checklist.md) |
| M7 | 3 | [página reserva](../autocura/pagina-reserva.md) · [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) · [autoatualização](../plataforma/autoatualizacao.md) |
| M7 | 4 | [diagnóstico com IA](../autocura/diagnostico-com-ia.md) · [atualização dos CLIs](../provedores/atualizacao-dos-clis.md) |

### Regras de execução da ordem

- **M0, além dos documentos:** layout do repositório em Go ([convenções](convencoes.md)) e o `logging` com mascaramento, antes da etapa 1.
- **M1:** os [spikes](#spikes-do-m1) de Linux vêm antes da etapa 1, e as partes de Linux do M1 andam primeiro, com o início de processos atrás de uma interface (`internal/terminal`, `Launcher`) que o Windows implementa depois. **Os spikes de Windows precisam rodar antes de fechar o M1, e nada do M2 nem do `winsvc` começa antes deles.** banco e migrações ([persistência](../arquitetura/persistencia.md)) e `internal/gitx` entram junto da etapa 1.
- **M2:** TLS e CA interna entram junto da etapa 1. Depois do M2, o [Guardião](../autocura/guardiao.md) pode começar em paralelo com M3 a M6 (a etapa 1 do M7 só confere que ele está pronto).
- **Conformidade real no fim do M3:** depois da etapa 5, uma primeira rodada do [teste de conformidade](../provedores/conformidade.md) com os CLIs reais nesta máquina Linux, para achar cedo mudanças dos provedores; a rodada completa, nos dois sistemas, é a da suíte pesada no laboratório.
- **"Casca" dos comandos da CLI:** um comando que outro documento usa antes da sua etapa (ex.: `mestre done` nas [missões](../orquestracao/missoes.md), etapa 9, antes da [CLI de comunicação](../cli/comunicacao.md), etapa 10) é implementado como casca junto do documento que o usa: sintaxe, chamada pelo [canal local](../arquitetura/canal-local.md) e o caminho feliz. Os critérios de aceite do documento da CLI (erros, códigos de saída, `--json`) fecham na etapa dele.
- **M7:** depois da etapa 4, o [Operador do PC](#operador-do-pc-no-m7) e, por último, a suíte pesada (MVP-1 a MVP-14).

## Como usar no dia a dia

- Comece um marco só com o anterior pronto (critérios de aceite das suítes rápida e completa passando), salvo o Guardião, acima.
- Dentro do marco, implemente na ordem do grafo de "Depende de"; documentos sem dependência entre si podem andar em paralelo.
- Os critérios *pesada* (MVP-1 a MVP-14) fecham no M7, quando instalação, autocura e laboratório existem ([testes](../qualidade/testes.md#mapa-do-mvp-para-a-suíte-pesada)).
