# Arquitetura

> **Assunto:** índice da área de arquitetura · **Componente:** Todos · **Atualizado:** 2026-10-19

| Documento | O que cobre | Quando ler |
|---|---|---|
| [visao-geral.md](visao-geral.md) | motor e interface, a Guilda (Cidadela, Torres e Guardiões) nas quatro composições, diagrama geral, canais e quem inicia cada um, princípios | primeiro contato com a arquitetura |
| [componentes.md](componentes.md) | componentes, binários `mestre`, `mestre-guardiao` e `mestre-pty`, subcomandos (`run`, `install`, `join`, `attach`…), serviços `mestre`, `mestre-guardiao` e `mestre-update` (e `KillMode=process`), contas por sistema, portas e canais locais | criar ou mudar um processo, serviço, subcomando ou porta |
| [windows.md](windows.md) | o Windows como Cidadela ou Torre, só com serviços e sem interface gráfica: o serviço `mestre` na conta virtual `NT SERVICE\Mestre`, a conta dos agentes `mestre-agente`, o `mestre-launcher` que inicia o `mestre-pty`, diagnóstico com IA, Operador do PC, ConPTY, spike 5 | qualquer trabalho em máquina Windows |
| [terminal.md](terminal.md) | o `mestre-pty` de cada terminal (PTY/ConPTY, emulador, buffer de 1 MiB), socket e aperto de mão, volta do motor sem parar os agentes, tamanho fixo, log, digitação e encerramento da árvore de processos | implementar ou mudar qualquer coisa que leia ou escreva num terminal |
| [canal-local.md](canal-local.md) | canal entre a CLI (e o `mestre hook`) e o motor local: endpoint (socket Unix nos dois sistemas), JSON Lines, pedido, resposta, parciais, tempo limite, motor reiniciando, versão, encaminhamento à Cidadela | implementar a CLI, o despachante ou o lado do motor que os atende |
| [persistencia.md](persistencia.md) | banco da Guilda (principal na Cidadela, réplica nas Torres) e banco local de máquina, migrações, tabelas principais, registro de terminais, arquivos, pastas, logs, backup | mexer no banco, em arquivos de dados ou em logs |
| [seguranca.md](seguranca.md) | modelo de ameaças, TLS e CA da Guilda, credenciais de máquina, uma Cidadela por vez, identidade de quem chama a CLI, isolamento dos agentes, mascaramento, quem vê o quê | antes de qualquer mudança que toque em segredos, rede ou permissões |
| [risco-do-codigo-nao-analisado.md](risco-do-codigo-nao-analisado.md) | o risco aceito do código que o hook não lê e roda como `mestre-agente`: o que alcança (credenciais, binários, configuração do Mestre, chaves de API), o que não alcança, o que reduz, o que removeria e o que fazer depois de um incidente | avaliar o risco das contas; mudar o isolamento dos agentes |
| [precedencia.md](precedencia.md) | ordem decretos > regras do Mestre > decisões de agentes > classe > ficha > AGENTS.md, delegação, canais de cada camada, combinação na decisão de permissão | implementar o `mestre hook`, decretos, classes ou a camada do Mestre |
