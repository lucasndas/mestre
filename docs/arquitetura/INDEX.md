# Arquitetura

> **Assunto:** índice da área de arquitetura · **Componente:** Todos · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [visao-geral.md](visao-geral.md) | motor e interface, topologia Core + nós + Guardiões nos quatro usos, diagrama geral, canais e quem inicia cada um, princípios | primeiro contato com a arquitetura |
| [componentes.md](componentes.md) | componentes, binários `mestre` e `mestre-guardiao`, subcomandos, serviços e contas por sistema e papel, portas e canais locais | criar ou mudar um processo, serviço, subcomando ou porta |
| [windows.md](windows.md) | o Windows como Core ou nó, só com serviços e sem interface gráfica: dois serviços do motor no Core, contas virtuais, a conta dos agentes `mestre-agente`, diagnóstico com IA, ConPTY | qualquer trabalho em máquina Windows |
| [terminal.md](terminal.md) | PTY/ConPTY no nó, emulador de terminal, tamanho fixo, buffer de 1 MiB, log, digitação e encerramento da árvore de processos | implementar ou mudar qualquer coisa que leia ou escreva num terminal |
| [canal-local.md](canal-local.md) | canal entre a CLI (e o `mestre hook`) e o motor local: endpoint, JSON Lines, pedido, resposta, parciais, tempo limite, versão, encaminhamento ao Core | implementar a CLI, o despachante ou o lado do motor que os atende |
| [persistencia.md](persistencia.md) | SQLite por nó, migrações, tabelas principais do Core e do nó, registro de terminais, arquivos, pastas, logs, backup | mexer no banco, em arquivos de dados ou em logs |
| [seguranca.md](seguranca.md) | modelo de ameaças, TLS e CA interna, credenciais de nó, identidade de quem chama a CLI, isolamento dos agentes, mascaramento, quem vê o quê | antes de qualquer mudança que toque em segredos, rede ou permissões |
| [precedencia.md](precedencia.md) | ordem decretos > regras do Mestre > classe > ficha > AGENTS.md, canais de cada camada, combinação na decisão de permissão | implementar o `mestre hook`, decretos, classes ou a camada do Mestre |
