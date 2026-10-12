# Ordem de implementação

> **Assunto:** marcos de desenvolvimento (M0 a M7), o marco de cada documento de funcionalidade, as partes que chegam em marco posterior e a regra de "Depende de" · **Componente:** Projeto · **Atualizado:** 2026-10-12

Este documento é a **fonte única** da ordem de desenvolvimento. Cada documento de funcionalidade (todo documento com a seção "Objetivo") pertence a exatamente um marco, listado no [mapa](#mapa-de-documentos-por-marco). A [verificação de docs](../qualidade/verificacao-de-docs.md) lê o mapa e confere a regra abaixo.

## Regra de "Depende de"

- A seção "Depende de" de um documento de funcionalidade lista **só o que precisa existir antes** para implementá-lo: documentos do **mesmo marco ou de um marco anterior**. Nunca um documento de marco posterior.
- O grafo formado por essas seções **não tem ciclos**. Dentro de um marco, a ordem de implementação é a ordem do grafo.
- Ligações de "ver também" (quem usa o documento, onde o assunto continua, o que chega em marco posterior) ficam no corpo do texto, nunca em "Depende de".
- Documentos de referência (`docs/arquitetura/` sem a seção "Objetivo", `docs/produto/`, `docs/decisoes/`, `docs/desenvolvimento/`, `docs/operacao/`) não pertencem a marco: valem desde o M0 e podem aparecer em "Depende de" de qualquer documento, sem contar para o grafo.
- Um documento de funcionalidade novo entra no mapa na mesma alteração que o cria; mudar um documento de marco exige conferir os "Depende de" de quem aponta para ele.

## Marcos

| Marco | Nome | O que fica pronto ao final |
|---|---|---|
| M0 | Fundação | executor das suítes (`tools/testrun`), verificação de docs (`tools/docscheck`), dublês e `testkit` (relógio injetável, rede, PTY, git, Telegram e GitHub falsos, agente falso), `logging` com [mascaramento](../arquitetura/seguranca.md#mascaramento-em-logs) |
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

### Spikes do M1

Seis itens de "Verificar na implementação" mudam camadas de base se falharem; são testados no começo do M1, antes de fechar o terminal e o canal local:

| Spike | Se falhar, muda | Onde está o item |
|---|---|---|
| ConPTY com o processo iniciado como `mestre-agente` (`CreateProcessAsUser`, perfil carregado) a partir do serviço, na sessão 0 | o serviço auxiliar `mestre-launcher` (LocalSystem) inicia os processos; ou a distribuição passa a levar o `OpenConsole.exe` | [Windows](../arquitetura/windows.md#verificar-na-implementação) |
| conta virtual `NT SERVICE\MestreNode` com `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege` | o serviço auxiliar `mestre-launcher` | [Windows](../arquitetura/windows.md#verificar-na-implementação) |
| `claude`, `codex` e `agy` gravando e achando o login no perfil do `mestre-agente` no Windows | login em arquivo no perfil de conta, ou o provedor não é oferecido naquela máquina | [Windows](../arquitetura/windows.md#verificar-na-implementação) |
| identificação do chamador subindo a árvore de processos | um grupo de processos por terminal (cgroup ou Job Object) | [segurança](../arquitetura/seguranca.md#verificar-na-implementação) |
| `claude`, `codex` e `agy` rodando como `mestre-agente` no Linux | pasta pessoal própria para a conta dos agentes | [segurança](../arquitetura/seguranca.md#verificar-na-implementação) |

## Mapa de documentos por marco

Tabela lida pela verificação de docs: cada link da coluna "Documentos" é um documento de funcionalidade daquele marco.

| Marco | Documentos |
|---|---|
| M0 | [testes](../qualidade/testes.md) · [dublês](../qualidade/dubles.md) · [verificação de docs](../qualidade/verificacao-de-docs.md) |
| M1 | [terminal](../arquitetura/terminal.md) · [canal local](../arquitetura/canal-local.md) · [cofre](../plataforma/cofre.md) |
| M2 | [protocolo](../conectividade/protocolo.md) · [descoberta](../conectividade/descoberta.md) · [pareamento](../conectividade/pareamento.md) |
| M3 | [convenções da CLI](../cli/convencoes.md) · [adaptador](../provedores/adaptador.md) · [despachante de hooks](../provedores/despachante-de-hooks.md) · [permissões](../classes/permissoes.md) · [reforço nativo](../classes/reforco-nativo.md) · [teto do workspace](../classes/teto-do-workspace.md) · [classes](../classes/classes.md) · [Claude Code](../provedores/claude-code.md) · [Codex](../provedores/codex.md) · [Antigravity](../provedores/antigravity.md) · [conformidade](../provedores/conformidade.md) |
| M4 | [workspaces](../orquestracao/workspaces.md) · [bases e fichas](../orquestracao/bases-e-fichas.md) · [personagens](../orquestracao/personagens.md) · [conexões](../orquestracao/conexoes.md) · [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) · [mesas](../orquestracao/mesas.md) · [missões](../orquestracao/missoes.md) · [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) · [rituais](../orquestracao/rituais.md) · [grimórios](../orquestracao/grimorios.md) · [sessão zero do workspace](../sessao-zero/workspace.md) · [sessão zero da mesa](../sessao-zero/mesa.md) · [sessão zero do personagem](../sessao-zero/personagem.md) · [CLI de comunicação](../cli/comunicacao.md) · [CLI de coordenação](../cli/coordenacao.md) · [CLI de classes e rituais](../cli/coordenacao-classes-e-rituais.md) · [skills geradas](../cli/skills-geradas.md) |
| M5 | [usuários e níveis](../telegram/usuarios-e-niveis.md) · [bot](../telegram/bot.md) · [roteamento](../telegram/roteamento.md) · [segurança do canal](../telegram/seguranca-do-canal.md) · [decretos](../telegram/decretos.md) · [anexos e transcrição](../telegram/anexos-e-transcricao.md) · [política de aprovação](../aprovacoes/politica.md) · [fluxo de aprovação](../aprovacoes/fluxo.md) · [identidades e Apps](../github/identidades-e-apps.md) · [criação de Apps](../github/criacao-de-apps.md) · [eventos do GitHub](../github/eventos.md) · [ações por nível](../github/acoes-por-nivel.md) · [aprovação no GitHub](../github/aprovacao-no-github.md) · [Grão-Mestre](../orquestracao/grao-mestre.md) · [Mestre da mesa](../orquestracao/mestre-da-mesa.md) · [limites de uso](../plataforma/limites-de-uso.md) · [CLI de issues e PRs](../cli/coordenacao-github.md) · [CLI do Grão-Mestre](../cli/grao-mestre.md) |
| M6 | [login](../painel/login.md) · [canvas](../painel/canvas.md) · [visão do workspace](../painel/visao-do-workspace.md) · [conversa e aprovações](../painel/conversa-e-aprovacoes.md) · [administração](../painel/administracao.md) |
| M7 | [Guardião](../autocura/guardiao.md) · [camadas](../autocura/camadas.md) · [diagnóstico com IA](../autocura/diagnostico-com-ia.md) · [página reserva](../autocura/pagina-reserva.md) · [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) · [instalação e atualização](../plataforma/instalacao-e-atualizacao.md) · [checklist](../plataforma/checklist.md) · [autoatualização](../plataforma/autoatualizacao.md) · [atualização dos CLIs](../provedores/atualizacao-dos-clis.md) |

## Como usar no dia a dia

- Comece um marco só com o anterior pronto (critérios de aceite das suítes rápida e completa passando), salvo o Guardião, acima.
- Dentro do marco, implemente na ordem do grafo de "Depende de"; documentos sem dependência entre si podem andar em paralelo.
- Os critérios *pesada* (MVP-1 a MVP-14) fecham no M7, quando instalação, autocura e laboratório existem ([testes](../qualidade/testes.md#mapa-do-mvp-para-a-suíte-pesada)).
