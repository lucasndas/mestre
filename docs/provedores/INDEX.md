# Provedores

> **Assunto:** como o Mestre conversa com Claude Code, Codex e Antigravity CLI · **Componente:** Todos · **Atualizado:** 2026-10-13

| Documento | O que cobre | Quando ler |
|---|---|---|
| [adaptador.md](adaptador.md) | contrato do adaptador, formato interno de evento, mapa de eventos e de ferramentas, telas interativas, digitação de mensagens (`TypeMessage`), retomada de sessão, monitoramento reduzido | antes de mexer em qualquer provedor ou na entrega de instruções e mensagens |
| [despachante-de-hooks.md](despachante-de-hooks.md) | o comando `mestre hook`: fluxo, prazos, modo degradado, erro interno, respostas por provedor | ao mexer nos hooks ou nas decisões de permissão na máquina |
| [claude-code.md](claude-code.md) | Claude Code: instruções, camada do Mestre, skills, hooks, confiança, flags, contas, uso e rate limit, telas | ao implementar ou atualizar o adaptador do Claude Code |
| [codex.md](codex.md) | Codex: instruções, camada do Mestre, skills, hooks e confiança dos hooks, sandbox, flags, contas, uso e rate limit, telas | ao implementar ou atualizar o adaptador do Codex |
| [antigravity.md](antigravity.md) | Antigravity CLI (`agy`): instruções, agente próprio, skills, os cinco hooks e a falha fechada, confiança, flags, contas, uso, telas | ao implementar ou atualizar o adaptador do Antigravity |
| [atualizacao-dos-clis.md](atualizacao-dos-clis.md) | os CLIs se atualizam sozinhos; o Mestre detecta a versão nova por máquina e perfil, roda a conformidade logo depois, marca o provedor como não conforme se ela falhar e avisa o Dono | quando sair versão nova de `claude`, `codex` ou `agy` |
| [conformidade.md](conformidade.md) | teste automático com o provedor real: quando roda, etapas, resultado e efeito de uma falha | quando a versão de um provedor muda ou ao adicionar um provedor |
