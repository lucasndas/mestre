# Arquitetura

> **Assunto:** índice da área de arquitetura · **Componente:** Todos · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [visao-geral.md](visao-geral.md) | motor e interface, topologia Core + nós + Guardiões, diagrama geral, canais e quem inicia cada um, princípios | primeiro contato com a arquitetura |
| [componentes.md](componentes.md) | componentes, binários `mestre` e `mestre-guardiao`, subcomandos, serviços e contas, portas e canais locais | criar ou mudar um processo, serviço, subcomando ou porta |
| [no-windows.md](no-windows.md) | serviço na sessão 0, processo na sessão do usuário, named pipe, ConPTY, capacidade `gui`, conta do serviço | qualquer trabalho no nó Windows |
| [persistencia.md](persistencia.md) | SQLite por nó, migrações, tabelas principais do Core e do nó, registro de terminais, arquivos, pastas, logs, backup | mexer no banco, em arquivos de dados ou em logs |
| [seguranca.md](seguranca.md) | modelo de ameaças, TLS e CA interna, credenciais de nó, identidade de quem chama a CLI, isolamento dos agentes, mascaramento, quem vê o quê | antes de qualquer mudança que toque em segredos, rede ou permissões |
| [precedencia.md](precedencia.md) | ordem decretos > regras do Mestre > classe > ficha > AGENTS.md, canais de cada camada, combinação na decisão de permissão | implementar o `mestre hook`, decretos, classes ou a camada do Mestre |
