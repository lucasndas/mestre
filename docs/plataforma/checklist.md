# Checklist de ambiente

> **Assunto:** verificação de pré-requisitos de cada máquina: itens, quando roda, gravidade, alertas e correções · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Descobrir cedo o que impede o Mestre de funcionar em cada máquina, explicar o problema em linguagem simples e oferecer a correção, sem nunca mexer em rede, firewall ou serviços do sistema sem aprovação.

## Comportamento

### Quando roda

- Ao iniciar o motor, a cada 30 min (padrão) e sob demanda (painel, ação `checklist` do [Guardião](../autocura/guardiao.md#lista-fechada-de-ações)).
- Itens baratos (conexões, heartbeat, serviços) são reavaliados a cada evento que os afeta, sem esperar o ciclo.
- **Verificação de início** no Windows (seção abaixo).
- Mudança de versão de um provedor detectada → roda o [teste de conformidade](../provedores/conformidade.md) daquele provedor naquele nó.
- Cada máquina roda os itens do seu **sistema** (Linux ou Windows) e do seu **papel** (Core ou nó; o Core roda também os de nó, pelo nó local) e manda o resultado ao Core; o resultado local fica em `checklist_results` e os alertas abertos em `checklist_alerts` ([persistência](../arquitetura/persistencia.md)).

### Verificação de início (Windows)

Roda quando um serviço do motor sobe (`mestre-node` e, no Core, `mestre-core`). Confere, nesta ordem, os itens WIN-06 (serviço com a conta do usuário), WIN-14 (no Core: conta virtual e chave do cofre), ALL-01 (motor e Guardião conectados ao Core) e WIN-05 (serviços e Guardião ativos). O resultado vai para o painel (saúde do nó) e, se grave, para o Telegram, como qualquer item. Não há aviso na área de trabalho: o Mestre não tem interface gráfica ([Windows](../arquitetura/windows.md)).

### Gravidade e alertas

| Gravidade | Onde aparece |
|---|---|
| **aviso** | painel, em "Saúde dos nós" ([administração](../painel/administracao.md#saúde-dos-nós)) |
| **grave** | painel e Telegram, como alerta "Sistema" ([roteamento](../telegram/roteamento.md)) |

- Cada alerta diz o que está errado, por que importa, e a correção (botão "corrigir" ou o passo manual).
- Um alerta é enviado uma vez ao abrir e outra ao fechar; enquanto aberto, não se repete.

### Correções

| Tipo de correção | Quem aplica |
|---|---|
| O que o motor já faz no trabalho normal (pré-confiar pastas, regenerar arquivos da mesa, refazer a descoberta) | o motor, sozinho, com registro |
| Rede, firewall, serviços e tarefas agendadas do sistema | só com aprovação de Dono ou Administrador ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)); executada pelo Guardião da máquina |
| Login em provedor, senha do serviço | manual: o alerta explica o passo |

### Itens: Windows

Papel: **os dois** (Core e nó), **Core** ou **nó**. Ids que saíram não são reaproveitados.

| Id | Papel | Verifica | Gravidade | Correção |
|---|---|---|---|---|
| WIN-01 | os dois | Windows 11 ou superior (build 22000 ou maior) | grave | manual |
| WIN-02 | os dois | perfil da rede em uso como **Privada** (`Get-NetConnectionProfile`) | grave | `Set-NetConnectionProfile -NetworkCategory Private` (aprovação) |
| WIN-03 | os dois | firewall libera a descoberta (UDP broadcast e mDNS; no Core, a entrada em 7445/udp e 5353/udp) | grave | criar regras (aprovação) |
| WIN-04 | nó; Core no modo máquina única | firewall libera a porta da [página reserva](../autocura/pagina-reserva.md) só no perfil Privado ([portas](../arquitetura/componentes.md#portas)) | aviso | criar regra (aprovação) |
| WIN-05 | os dois | serviços do motor (`mestre-node`; no Core, também `mestre-core`) e `mestre-guardiao` com início automático e recuperação configurada ([camadas](../autocura/camadas.md#valores-padrão)) | grave | reconfigurar (aprovação) |
| WIN-06 | os dois | `mestre-node` roda com a conta do usuário ("Fazer logon como") e consegue iniciar (sem erro de logon por senha alterada) | grave | manual: refazer a configuração da conta ([Windows](../arquitetura/windows.md#conta-do-usuário-fazer-logon-como)) |
| WIN-09 | os dois | tarefa agendada `\Mestre\Diagnostico` existe ([diagnóstico com IA](../autocura/diagnostico-com-ia.md)) | aviso | recriar (aprovação) |
| WIN-13 | Core | firewall libera a entrada em 7443/tcp e 7444/tcp só no perfil Privado | grave | criar regras (aprovação) |
| WIN-14 | Core | `mestre-core` roda como `NT SERVICE\MestreCore`; `cofre.key` protegido por DPAPI de máquina com ACL só dessa conta ([cofre](cofre.md#chave-do-cofre)) | grave | corrigir ACL (aprovação) |
| WIN-15 | Core | ACLs de `workspaces\` e `contas\` dão modificação à conta do `mestre-node`, e nada mais dela fora dessas pastas e de `no\` ([Windows](../arquitetura/windows.md#windows-como-core)) | grave | corrigir ACL (aprovação) |
| WIN-16 | Core | anúncio mDNS do Guardião respondendo (`_mestre._tcp`) ([descoberta](../conectividade/descoberta.md#mdns)) | grave | manual |

### Itens: Linux

| Id | Papel | Verifica | Gravidade | Correção |
|---|---|---|---|---|
| LIN-01 | os dois | Ubuntu LTS; versão registrada na instalação e mudança detectada | aviso | — |
| LIN-02 | Core | Avahi rodando e nome `.local` resolvendo | grave | reiniciar o Avahi (aprovação) |
| LIN-03 | Core | firewall (ufw/iptables) libera as portas do motor, do Guardião do Core e da descoberta ([portas](../arquitetura/componentes.md#portas)) | grave | criar regras (aprovação) |
| LIN-04 | os dois | serviços do motor (`mestre-core` ou `mestre-node`) e `mestre-guardiao` ativos, com `Restart=always` (e watchdog no motor) | grave | reconfigurar (aprovação) |
| LIN-05 | Core | chave do cofre com dono e permissão corretos ([cofre](cofre.md#chave-do-cofre)) | grave | corrigir permissão (aprovação) |
| LIN-07 | Core | chave do cofre ainda em arquivo num servidor com `systemd-creds` e TPM2 disponíveis ([cofre](cofre.md#chave-do-cofre)) | aviso | mover a chave para `systemd-creds` (aprovação) |
| LIN-08 | os dois | conta `mestre-agente` existe e os terminais rodam com ela, nunca com `mestre` ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)) | grave | recriar a conta (aprovação) |
| LIN-09 | nó; Core no modo máquina única | firewall libera a porta da página reserva só para a rede local | aviso | criar regra (aprovação) |

### Itens: todas as máquinas

| Id | Verifica | Gravidade | Correção |
|---|---|---|---|
| ALL-01 | motor e Guardião conectados ao Core, heartbeat em dia; credencial não revogada | grave | refazer descoberta (sozinho); revogada: manual |
| ALL-02 | Guardião do Core responde aos nós (sondagem da porta dele) | grave | manual |
| ALL-03 | certificados TLS válidos e com mais de 30 dias até vencer ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)) | aviso; grave com menos de 7 dias | renovar (sozinho) |
| ALL-04 | relógio do nó a menos de 5 s do relógio do Core ([protocolo](../conectividade/protocolo.md)) | aviso | manual |
| ALL-05 | CLI `mestre` no PATH, `MESTRE_CLI` definido e `mestre debug` passa | grave | manual |
| ALL-06 | git instalado, versão 2.32 ou maior ([identidades e Apps](../github/identidades-e-apps.md)) | grave | manual |
| ALL-07 | último backup diário íntegro ([persistência](../arquitetura/persistencia.md#backup)) | grave | rodar backup (sozinho) |
| ALL-08 | disco livre acima dos limites da autocura ([camadas](../autocura/camadas.md#valores-padrão)) | aviso / grave | `cleanup` (sozinho) |
| ALL-09 | nó de transcrição: modelo Whisper baixado com o hash certo e transcrição de teste (5 s de áudio) em até 10 s (padrão) ([anexos e transcrição](../telegram/anexos-e-transcricao.md)) | grave | baixar de novo (sozinho) |
| ALL-10 | provedores usados pelas bases do nó (`claude`, `codex`, `agy`) instalados, com versão registrada e não abaixo da mínima ([adaptador](../provedores/adaptador.md)) | grave | manual |
| ALL-11 | cada perfil de conta das fichas está **logado** no seu provedor e, no Codex, com os **hooks do Mestre confiados** (configuração inicial do perfil, [bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta)); perfil incompleto não recebe personagens | grave | manual (o painel guia) |
| ALL-12 | **pré-confiança de pastas por provedor** para o clone principal e os worktrees das mesas do nó: Claude Code (`hasTrustDialogAccepted` no `.claude.json` da conta), Codex (`trust_level = "trusted"`; a confiança dos hooks é do perfil, ALL-11), Antigravity (`trustedWorkspaces`) ([sessão zero da mesa](../sessao-zero/mesa.md)) | grave | pré-confiar de novo (sozinho) |
| ALL-13 | **cópia `.claude/skills/` ainda necessária:** o teste de conformidade do Claude Code confere se ele já lê `.agents/skills/`; se ler, oferece desligar a cópia ([ADR 0011](../decisoes/0011-agents-skills-pasta-oficial.md)) | aviso | desligar a cópia (Dono ou Administrador) |
| ALL-14 | projeto com `CLAUDE.md`: avisa que os agentes Claude leem um arquivo diferente dos outros e sugere unificar no `AGENTS.md` ([precedência](../arquitetura/precedencia.md)) | aviso | manual |
| ALL-15 | personagens em monitoramento reduzido por falta de hooks ([adaptador](../provedores/adaptador.md)) | aviso | conforme o motivo |
| ALL-16 | Apps sem `bot_user_id` lido e pastas de skills `mestre-*` sem marcador ([skills geradas](../cli/skills-geradas.md)) | aviso | manual |
| ALL-17 | Core sem nenhum nó remoto ([modo máquina única](../autocura/camadas.md#modo-máquina-única)): se a máquina inteira cair, ninguém de fora percebe | aviso, aberto enquanto o modo durar | manual: parear um segundo nó ([parear nó](../operacao/parear-no.md)) |
| ALL-18 | Core: token do bot presente no cofre e bot conectado ([bot](../telegram/bot.md)) | grave | manual: cadastrar o token ([parear Telegram](../operacao/parear-telegram.md)) |

## Regras e erros

- Uma verificação que não consegue rodar (comando ausente, sem permissão) vira alerta **aviso** "não foi possível verificar", nunca "ok".
- Correção aprovada que falha → o alerta continua aberto com o erro; nada é tentado de novo sozinho.
- O checklist nunca altera o Guardião (serviço, binário, regras de firewall dele) ([Guardião](../autocura/guardiao.md#proteções)).

## Critérios de aceite

- **CA-CHK-01** · *rápida* — **Dado** um sistema falso com rede "Pública", **quando** WIN-02 roda, **então** abre alerta grave com a correção, que só é aplicada depois da aprovação de Dono ou Administrador.
- **CA-CHK-02** · *rápida* — **Dado** build 19045, **quando** WIN-01 roda, **então** falha com "exige Windows 11 ou superior".
- **CA-CHK-03** · *rápida* — **Dado** a versão do `codex` mudou, **quando** o checklist roda, **então** dispara o teste de conformidade do Codex naquele nó.
- **CA-CHK-04** · *rápida* — **Dado** o caminho de um worktree ausente de `trustedWorkspaces`, **quando** ALL-12 roda, **então** o motor grava a confiança sozinho e fecha o alerta.
- **CA-CHK-05** · *rápida* — **Dado** um alerta grave aberto, **quando** o checklist roda de novo sem mudança, **então** nenhum novo envio ao Telegram acontece.
- **CA-CHK-07** · *rápida* — **Dado** um comando de verificação ausente, **quando** o item roda, **então** o resultado é "não foi possível verificar", nunca "ok".
- **CA-CHK-11** · *rápida* — **Dado** um Core Windows falso em que `cofre.key` dá leitura à conta do usuário, **quando** WIN-14 roda, **então** abre alerta grave com a correção da ACL, aplicada só com aprovação.
- **CA-CHK-12** · *rápida* — **Dado** um Core sem nó remoto pareado, **quando** o checklist roda, **então** ALL-17 fica aberto como aviso; **quando** um nó é pareado, **então** o aviso fecha.
- **CA-CHK-13** · *rápida* — **Dado** uma máquina Linux no papel de nó, **quando** o checklist monta a lista, **então** roda LIN-01, LIN-04, LIN-08, LIN-09 e os ALL-*, e não roda LIN-02, LIN-03, LIN-05 nem LIN-07.

## Depende de

- [Componentes](../arquitetura/componentes.md)
- [Windows](../arquitetura/windows.md)
- [Guardião](../autocura/guardiao.md)
- [Conformidade](../provedores/conformidade.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md)
- [Persistência](../arquitetura/persistencia.md)
