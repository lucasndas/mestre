# Checklist de ambiente

> **Assunto:** verificação de pré-requisitos de cada máquina da Guilda, por sistema e por papel (Cidadela, Torre, máquina não excluída): itens, quando roda, gravidade, alertas e correções · **Componente:** Todos · **Atualizado:** 2026-10-13

## Objetivo

Descobrir cedo o que impede o Mestre de funcionar em cada máquina, explicar o problema em linguagem simples e oferecer a correção, sem nunca mexer em rede, firewall ou serviços do sistema sem aprovação.

## Comportamento

### Quando roda

- Ao iniciar o motor, a cada 30 min (padrão) e sob demanda (painel, ação `checklist` do [Guardião](../autocura/guardiao.md#lista-fechada-de-ações)).
- Itens baratos (conexões, heartbeat, serviços) são reavaliados a cada evento que os afeta, sem esperar o ciclo.
- **Verificação de início** no Windows (seção abaixo).
- Mudança de versão de um provedor detectada num perfil (os CLIs se atualizam sozinhos) → roda o [teste de conformidade](../provedores/conformidade.md) daquele provedor naquele perfil e máquina ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)).
- Cada máquina roda os itens do seu **sistema** (Linux ou Windows) e do seu **papel**: **todas**; **Cidadela** (só enquanto é a Cidadela); **Torre**; **não excluída** (toda máquina que o Dono não excluiu, inclusive a Cidadela: guarda o cofre e pode assumir, então já confere os itens de Cidadela que dependem só dela, para a promoção dar certo); **com Operador do PC**. Ao mudar de papel (promoção, volta da antiga Cidadela, exclusão pelo Dono ou fim dela), roda de novo na hora. O resultado local fica em `checklist_results`, os alertas abertos em `checklist_alerts` ([persistência](../arquitetura/persistencia.md)), e vai à Cidadela.

### Verificação de início (Windows)

Roda quando o serviço `mestre` sobe. Confere, nesta ordem, os itens WIN-19 (`mestre` na conta virtual), WIN-17 (conta `mestre-agente`), WIN-14 (não excluída: chave do cofre), ALL-01 (Torre: motor e Guardião conectados à Cidadela) e WIN-05 (serviços e Guardião ativos). O resultado vai para o painel (Guilda e saúde das máquinas) e, se grave, para o Telegram, como qualquer item. Não há aviso na área de trabalho: o Mestre não tem interface gráfica ([Windows](../arquitetura/windows.md)).

### Gravidade e alertas

| Gravidade | Onde aparece |
|---|---|
| **aviso** | painel, em "Guilda e saúde das máquinas" ([administração](../painel/administracao.md#guilda-e-saúde-das-máquinas)) |
| **grave** | painel e Telegram, como alerta "Sistema" ([roteamento](../telegram/roteamento.md)) |

- Cada alerta diz o que está errado, por que importa, e a correção (botão "corrigir" ou o passo manual).
- Um alerta é enviado uma vez ao abrir e outra ao fechar; enquanto aberto, não se repete.

### Correções

| Tipo de correção | Quem aplica |
|---|---|
| O que o motor já faz no trabalho normal (pré-confiar pastas, regenerar arquivos da mesa, refazer a descoberta) | o motor, sozinho, com registro |
| Rede, firewall, serviços e contas do sistema | só com aprovação de Dono ou Administrador ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)); executada pelo Guardião da máquina |
| Login em provedor | manual: o alerta explica o passo |

### Itens: Windows

Ids que saíram não são reaproveitados. Toda máquina abre o bloco de portas da sua instância ([portas](../arquitetura/componentes.md#portas)), porque toda máquina responde no endereço do painel e qualquer uma pode virar Cidadela.

| Id | Papel | Verifica | Gravidade | Correção |
|---|---|---|---|---|
| WIN-01 | todas | Windows 11 ou superior (build 22000 ou maior) | grave | manual |
| WIN-02 | todas | perfil da rede em uso como **Privada** (`Get-NetConnectionProfile`) | grave | `Set-NetConnectionProfile -NetworkCategory Private` (aprovação) |
| WIN-03 | todas | firewall libera a descoberta (UDP broadcast e mDNS, com a entrada em 7445/udp e 5353/udp) | grave | criar regras (aprovação) |
| WIN-04 | todas | firewall libera a porta da [página reserva](../autocura/pagina-reserva.md) só no perfil Privado ([portas](../arquitetura/componentes.md#portas)) | aviso | criar regra (aprovação) |
| WIN-05 | todas | serviços `mestre` e `mestre-guardiao` com início automático e recuperação configurada ([camadas](../autocura/camadas.md#valores-padrão)) | grave | reconfigurar (aprovação) |
| WIN-13 | todas | firewall libera a entrada em 7443/tcp e 7444/tcp só no perfil Privado | grave | criar regras (aprovação) |
| WIN-14 | não excluída | `cofre.key` protegido por DPAPI de máquina com ACL só de `NT SERVICE\Mestre` ([cofre](cofre.md#chave-do-cofre)) | grave | corrigir ACL (aprovação) |
| WIN-15 | todas | ACLs de `%ProgramData%\Mestre\` como em [persistência](../arquitetura/persistencia.md#arquivos): `mestre-agente` só com modificação em `workspaces\`, `contas\` e `maquina\agentes\<id>\tmp\`, leitura em `policy.json` e só criar na fila do hook | grave | corrigir ACL (aprovação) |
| WIN-16 | todas | anúncio mDNS do Guardião respondendo (`_mestre._tcp` da máquina); na Cidadela, também `mestre.local`, publicado só com a [concessão do motor](../autocura/guardiao.md#concessão-do-motor); numa Torre, sem `mestre.local` ([descoberta](../conectividade/descoberta.md#mdns)) | grave | manual |
| WIN-17 | todas | conta local `mestre-agente` existe, ativa, só no grupo `Users` (fora de `Administradores`), oculta da tela de login, e o logon em lote com a senha guardada funciona ([Windows](../arquitetura/windows.md#conta-dos-agentes-mestre-agente)) | grave | recriar a conta ou gerar e guardar senha nova (aprovação) |
| WIN-18 | todas | `mestre-agente` tem "Negar logon local" e "Negar logon pelos Serviços de Área de Trabalho Remota" e não está em `Remote Desktop Users` | grave | reaplicar os direitos (aprovação) |
| WIN-19 | todas | `mestre` roda como `NT SERVICE\Mestre` (nunca com a conta de um usuário), com a lista de privilégios exigidos declarada ([Windows](../arquitetura/windows.md#serviços-no-windows)) | grave | reconfigurar o serviço (aprovação) |
| WIN-20 | todas | cada provedor usado pelas bases da máquina (`claude`, `codex`, `agy`) roda como `mestre-agente`, com o `HOME` do perfil de conta, onde o próprio CLI se instala e se atualiza ([CLIs dos provedores](instalacao-e-atualizacao.md#clis-dos-provedores)): `--version` e a verificação de login do ALL-11 feitas com essa conta. O `agy` guarda o login no Gerenciador de Credenciais do `mestre-agente`, comum a todos os perfis: **um só perfil Antigravity por máquina Windows**; um segundo é recusado com essa explicação ([Antigravity](../provedores/antigravity.md#contas-múltiplas)) | grave | manual (o alerta explica o passo) |
| WIN-21 | com Operador do PC | UAC para o [Operador do PC](../arquitetura/windows.md#operador-do-pc): `EnableLUA = 1`, `PromptOnSecureDesktop = 1` e `ConsentPromptBehaviorAdmin` com pedido (1 a 5; nunca 0, "elevar sem perguntar"), em `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`; falhou → a ficha da máquina não anuncia o Operador | grave | manual (o alerta explica o passo; o Mestre não muda o UAC) |
| WIN-22 | com Operador do PC | tarefa `\Mestre\Operador` existe para o usuário configurado (logon dele, privilégios normais, sem limite de tempo, reiniciar em falha) e, com ele logado, o processo da sessão está ligado ao `mestre`; falhou → a ficha da máquina não anuncia o Operador | aviso | recriar a tarefa (aprovação de Dono) |
| WIN-23 | todas | serviço `mestre-launcher` roda como LocalSystem, o pipe dele só aceita a conta `NT SERVICE\Mestre`, o arquivo da senha do `mestre-agente` tem ACL só de SYSTEM e a pasta `pty` tem a ACL do [terminal](../arquitetura/terminal.md#socket-e-aperto-de-mão) ([Windows](../arquitetura/windows.md#iniciador-mestre-launcher)) | grave | reaplicar as ACLs (sozinho) |
| WIN-24 | todas | horário ativo do Windows Update configurado pelo Mestre no maior intervalo que o Windows aceita (18 h; padrão 06:00 às 24:00) e, onde a edição aceita a política (Pro ou maior), atualizações só baixadas e instaladas quando alguém manda ("baixar e avisar"); assim o Windows não reinicia sozinho no meio do trabalho dos agentes | aviso | reaplicar (sozinho) |
| WIN-25 | todas | sem reinício pendente do Windows Update (`RebootRequired`); pendente → avisa o Dono com os terminais vivos da máquina, para ele escolher a hora; o reinício da máquina leva à [retomada](../orquestracao/personagens.md#retomada-após-reinício) | aviso | manual (o Dono reinicia ou agenda no Windows) |

### Itens: Linux

| Id | Papel | Verifica | Gravidade | Correção |
|---|---|---|---|---|
| LIN-01 | todas | Ubuntu LTS; versão registrada na instalação e mudança detectada | aviso | — |
| LIN-02 | todas | Avahi rodando e nome `.local` resolvendo | grave | reiniciar o Avahi (aprovação) |
| LIN-03 | todas | firewall (ufw/iptables) libera as portas do motor, do Guardião e da descoberta ([portas](../arquitetura/componentes.md#portas)) | grave | criar regras (aprovação) |
| LIN-04 | todas | serviços `mestre` e `mestre-guardiao` ativos, com `Restart=always` (e watchdog no motor) | grave | reconfigurar (aprovação) |
| LIN-05 | não excluída | chave do cofre com dono e permissão corretos ([cofre](cofre.md#chave-do-cofre)) | grave | corrigir permissão (aprovação) |
| LIN-07 | não excluída | chave do cofre ainda em arquivo numa máquina com `systemd-creds` e TPM2 disponíveis ([cofre](cofre.md#chave-do-cofre)) | aviso | mover a chave para `systemd-creds` (aprovação) |
| LIN-08 | todas | conta `mestre-agente` existe e os terminais rodam com ela, nunca com `mestre` ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)) | grave | recriar a conta (aprovação) |
| LIN-09 | todas | firewall libera a porta da página reserva só para a rede local | aviso | criar regra (aprovação) |
| LIN-10 | todas | cada provedor usado pelas bases da máquina (`claude`, `codex`, `agy`) roda como `mestre-agente`, com o `HOME` do perfil de conta, onde o próprio CLI se instala e se atualiza: `--version` e a verificação de login do ALL-11 feitas com essa conta (`claude auth status`, `codex login status`, `agy -p "/usage" --output-format json`); o Codex como pacote completo ([CLIs dos provedores](instalacao-e-atualizacao.md#clis-dos-provedores)) | grave | manual (o alerta explica o passo) |
| LIN-11 | todas | cgroup v2 unificado, kernel 5.14 ou maior e cgroup delegado ao `mestre-launcher` (`Delegate=yes`, [componentes](../arquitetura/componentes.md#iniciador-mestre-launcher)): o launcher cria e remove um cgroup de teste sob o seu; falhou → os terminais novos nascem sem cgroup, o encerramento passa a ser pelo `kill` por identidade do launcher ([terminal](../arquitetura/terminal.md#encerramento)) e a identificação volta à subida pela árvore, que recusa órfãos ([segurança](../arquitetura/seguranca.md#identidade-de-quem-chama-a-cli)) | grave | reconfigurar a unidade (aprovação); kernel ou cgroup v1: manual |
| LIN-12 | todas | `kernel.yama.ptrace_scope` 1 ou maior (um personagem não depura processos de outro terminal) | grave | gravar o `sysctl` (aprovação) |
| LIN-13 | todas | `/proc` legível pela conta `mestre`: sem `hidepid`, ou com `mestre` no grupo `gid=` da montagem | grave | manual |
| LIN-14 | todas | `mestre-agente` sem gerenciador de usuário (`user@<uid>.service` parado, `Linger=no`): com ele, tmux e D-Bus tiram processos do cgroup do terminal | aviso | `loginctl disable-linger` e parar o gerenciador (aprovação) |
| LIN-15 | todas | unidade `mestre-launcher` com `KillMode=process` e `mestre` sem `CAP_SETUID`, `CAP_SETGID` nem cgroup delegado ([componentes](../arquitetura/componentes.md#iniciador-mestre-launcher)): sem o primeiro, reiniciar o launcher mata os terminais; sem o segundo, o motor pode virar root | grave | reconfigurar a unidade (aprovação) |

### Itens: os dois sistemas

| Id | Papel | Verifica | Gravidade | Correção |
|---|---|---|---|---|
| ALL-01 | Torre | motor e Guardião conectados à Cidadela atual, heartbeat em dia, seguindo a época atual; credencial não revogada | grave | refazer descoberta (sozinho); revogada: manual |
| ALL-02 | Torre | Guardião da Cidadela responde (sondagem da porta dele) | grave | manual |
| ALL-03 | todas | certificados TLS válidos e com mais de 30 dias até vencer (o da Cidadela em exercício, de 7 dias, com mais de 2); a renovação automática acontece nesse prazo, então chegar abaixo dele indica que ela falhou ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)) | aviso; grave com menos de 7 dias (o da Cidadela, com menos de 1) | renovar (sozinho) |
| ALL-04 | Torre | relógio da máquina a menos de 5 s do relógio da Cidadela ([protocolo](../conectividade/protocolo.md)) | aviso | manual |
| ALL-05 | todas | CLI `mestre` no PATH, `MESTRE_CLI` definido e `mestre debug` passa | grave | manual |
| ALL-06 | todas | git instalado, na versão mínima de segurança do [ADR 0001](../decisoes/0001-stack-go.md) (piso por sistema; [identidades e Apps](../github/identidades-e-apps.md)) | grave | manual |
| ALL-07 | Cidadela | último backup diário íntegro ([persistência](../arquitetura/persistencia.md#backup)) | grave | rodar backup (sozinho) |
| ALL-08 | todas | disco livre acima dos limites da autocura ([camadas](../autocura/camadas.md#valores-padrão)) | aviso / grave | `cleanup` (sozinho) |
| ALL-09 | não excluída e máquina de transcrição | modelo Whisper baixado com o hash certo e transcrição de teste (5 s de áudio) em até 10 s (padrão); nas não excluídas, também o `ffmpeg` convertendo o áudio de teste ([anexos e transcrição](../telegram/anexos-e-transcricao.md)) | grave | baixar de novo (sozinho) |
| ALL-10 | todas | provedores usados pelas bases da máquina (`claude`, `codex`, `agy`) instalados, com versão registrada e não abaixo da mínima ([adaptador](../provedores/adaptador.md)) | grave | manual |
| ALL-11 | todas | cada perfil de conta das fichas está **logado** no seu provedor e, no Codex, com os **hooks do Mestre confiados** (configuração inicial do perfil, [bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta)); perfil incompleto não recebe personagens | grave | manual (o painel guia) |
| ALL-12 | todas | **pré-confiança de pastas por provedor** para o clone principal e os worktrees das mesas da máquina: Claude Code (`hasTrustDialogAccepted` no `.claude.json` da conta), Codex (`trust_level = "trusted"`; a confiança dos hooks é do perfil, ALL-11), Antigravity (`trustedWorkspaces`) ([sessão zero da mesa](../sessao-zero/mesa.md)) | grave | pré-confiar de novo (sozinho) |
| ALL-13 | todas | **cópia `.claude/skills/` ainda necessária:** o teste de conformidade do Claude Code confere se ele já lê `.agents/skills/`; se ler, **oferece** desligar a cópia ([ADR 0011](../decisoes/0011-agents-skills-pasta-oficial.md)); sem a aprovação, a cópia continua sendo gerada ([sessão zero da mesa](../sessao-zero/mesa.md#regras-e-erros)) | aviso | desligar a cópia (só com aprovação de Dono ou Administrador) |
| ALL-14 | Cidadela | projeto com `CLAUDE.md`: avisa que os agentes Claude leem um arquivo diferente dos outros e sugere unificar no `AGENTS.md` ([precedência](../arquitetura/precedencia.md)) | aviso | manual |
| ALL-15 | todas | personagens em monitoramento reduzido por falta de hooks ([adaptador](../provedores/adaptador.md)) | aviso | conforme o motivo |
| ALL-16 | Cidadela | Apps sem `bot_user_id` lido e pastas de skills `mestre-*` sem marcador ([skills geradas](../cli/skills-geradas.md)) | aviso | manual |
| ALL-17 | Cidadela | Guilda sem nenhuma Torre ([modo máquina única](../autocura/camadas.md#modo-máquina-única)): se a máquina inteira cair, ninguém de fora percebe | aviso, aberto enquanto o modo durar | manual: instalar o Mestre numa segunda máquina e aceitá-la ([entrar na Guilda](../operacao/entrar-na-guilda.md)) |
| ALL-18 | Cidadela | token do bot presente no cofre e bot conectado; um `409 Conflict` recente aparece como detalhe, e o Telegram parado pelo `409` com o aluguel da trava vencido é falha ([o `409`](../guilda/trava-no-github.md#o-409-do-telegram)) | grave | manual: cadastrar o token ([parear Telegram](../operacao/parear-telegram.md)) |
| ALL-19 | Cidadela | Guilda com Torres, todas excluídas: se a Cidadela se perder, só a chave de recuperação traz o cofre de volta ([cofre](cofre.md#backup-e-chave-de-recuperação)) | aviso | manual: tirar a exclusão de uma Torre ([administração](../painel/administracao.md)) |
| ALL-20 | Torre | réplica do banco, dos arquivos e (se não excluída) do cofre em dia, dentro do alvo de atraso de [replicação](../guilda/replicacao.md) | aviso | refazer a cópia completa (sozinho) |
| ALL-21 | não excluída | a ficha do Grão-Mestre de cada workspace tem base executável nesta máquina ([Grão-Mestre](../orquestracao/grao-mestre.md#onde-roda)) | aviso | manual: instalar e logar o provedor, ou excluir a máquina |
| ALL-22 | Cidadela | [repositório da trava](../github/identidades-e-apps.md#repositório-da-trava) acessível e aluguel válido (última conferência confirmada há menos de 60 s); vencido, é o [GitHub fora](../guilda/trava-no-github.md#github-fora): ações no GitHub na fila e nenhuma promoção possível | grave | sozinho quando o GitHub volta; repositório apagado ou sem acesso: manual (o alerta traz o link) |

## Regras e erros

- Uma verificação que não consegue rodar (comando ausente, sem permissão) vira alerta **aviso** "não foi possível verificar", nunca "ok".
- Correção aprovada que falha → o alerta continua aberto com o erro; nada é tentado de novo sozinho.
- O checklist nunca altera o Guardião (serviço, binário, regras de firewall dele) ([Guardião](../autocura/guardiao.md#proteções)).

## Critérios de aceite

- **CA-CHK-01** · *rápida* — **Dado** um sistema falso com rede "Pública", **quando** WIN-02 roda, **então** abre alerta grave com a correção, que só é aplicada depois da aprovação de Dono ou Administrador.
- **CA-CHK-02** · *rápida* — **Dado** build 19045, **quando** WIN-01 roda, **então** falha com "exige Windows 11 ou superior".
- **CA-CHK-03** · *rápida* — **Dado** a versão do `codex` mudou, **quando** o checklist roda, **então** dispara o teste de conformidade do Codex naquela máquina.
- **CA-CHK-04** · *rápida* — **Dado** o caminho de um worktree ausente de `trustedWorkspaces`, **quando** ALL-12 roda, **então** o motor grava a confiança sozinho e fecha o alerta.
- **CA-CHK-05** · *rápida* — **Dado** um alerta grave aberto, **quando** o checklist roda de novo sem mudança, **então** nenhum novo envio ao Telegram acontece.
- **CA-CHK-07** · *rápida* — **Dado** um comando de verificação ausente, **quando** o item roda, **então** o resultado é "não foi possível verificar", nunca "ok".
- **CA-CHK-11** · *rápida* — **Dado** uma Cidadela Windows falsa em que `cofre.key` dá leitura a `mestre-agente`, **quando** WIN-14 roda, **então** abre alerta grave com a correção da ACL, aplicada só com aprovação.
- **CA-CHK-12** · *rápida* — **Dado** uma Guilda só com a Cidadela, **quando** o checklist roda, **então** ALL-17 fica aberto como aviso; **quando** uma Torre entra na Guilda, **então** o aviso fecha; **quando** o Dono exclui a única Torre, **então** ALL-19 abre até a exclusão sair.
- **CA-CHK-13** · *rápida* — **Dado** uma Torre Linux excluída pelo Dono, **quando** o checklist monta a lista, **então** roda os LIN-* de "todas" e os ALL-* de "todas" e de Torre, e não roda LIN-05, LIN-07, ALL-07 nem ALL-18; **quando** a exclusão sai, **então** passa a rodar também LIN-05, LIN-07, ALL-09 e ALL-21; **quando** é promovida, **então** troca os itens de Torre pelos de Cidadela na hora.
- **CA-CHK-15** · *rápida* — **Dado** uma máquina Windows falsa com o Operador habilitado e `ConsentPromptBehaviorAdmin = 0` (ou `PromptOnSecureDesktop = 0`), **quando** o checklist roda, **então** WIN-21 abre alerta grave e a ficha da máquina passa a mostrar o Operador indisponível; corrigido o valor, o alerta fecha e o Operador volta; numa máquina sem o Operador, WIN-21 e WIN-22 não rodam.
- **CA-CHK-16** · *rápida* — **Dado** um Linux falso com cgroup v1 (ou um `mestre` sem cgroup delegado), **quando** LIN-11 roda, **então** abre alerta grave e o motor passa a criar os terminais novos sem cgroup e a identificar pela subida pela árvore; corrigido, o alerta fecha e os terminais novos voltam a nascer no cgroup próprio.
- **CA-CHK-17** · *rápida* — **Dado** um Linux falso com `kernel.yama.ptrace_scope = 0`, `/proc` montado com `hidepid=2` sem o grupo de `mestre` e linger ligado para `mestre-agente`, **quando** o checklist roda, **então** LIN-12 e LIN-13 abrem alertas graves e LIN-14 um aviso, e nenhuma correção é aplicada sem aprovação.
- **CA-CHK-14** · *rápida* — **Dado** um Windows falso em que `mestre-agente` está em `Administradores` e o `mestre` roda com a conta de um usuário, **quando** o checklist roda, **então** WIN-17 e WIN-19 abrem alertas graves, e nenhuma correção é aplicada sem aprovação.
- **CA-CHK-18** · *rápida* — **Dado** um Windows falso com o horário ativo de 08:00 às 17:00 e um reinício pendente, e um Linux falso com a unidade `mestre-launcher` em `KillMode=control-group`, **quando** o checklist roda, **então** WIN-24 reaplica o horário de 18 h sozinho, WIN-25 avisa o Dono com os terminais vivos da máquina sem reiniciar nada, e LIN-15 abre alerta grave cuja correção só é aplicada com aprovação.

## Depende de

- [Componentes](../arquitetura/componentes.md)
- [Windows](../arquitetura/windows.md)
- [Guardião](../autocura/guardiao.md)
- [Conformidade](../provedores/conformidade.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md)
- [Persistência](../arquitetura/persistencia.md)
