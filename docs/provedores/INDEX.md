# Provedores

> **Assunto:** como o Mestre conversa com Claude Code, Codex e Antigravity CLI · **Componente:** Todos · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [adaptador.md](adaptador.md) | contrato do adaptador, formato interno de evento, mapa de eventos e de ferramentas, comportamento do `mestre hook`, monitoramento reduzido, telas interativas | antes de mexer em qualquer provedor, nos hooks ou na entrega de instruções |
| [claude-code.md](claude-code.md) | Claude Code: instruções, camada do Mestre, skills, hooks, confiança, flags, contas, uso e rate limit, telas | ao implementar ou atualizar o adaptador do Claude Code |
| [codex.md](codex.md) | Codex: instruções, camada do Mestre, skills, hooks e confiança dos hooks, sandbox, flags, contas, uso e rate limit, telas | ao implementar ou atualizar o adaptador do Codex |
| [antigravity.md](antigravity.md) | Antigravity CLI (`agy`): instruções, agente próprio, skills, os cinco hooks e a falha fechada, confiança, flags, contas, uso, telas | ao implementar ou atualizar o adaptador do Antigravity |
| [atualizacao-dos-clis.md](atualizacao-dos-clis.md) | detecção, instalação, conformidade e reinício em ponto seguro quando um CLI de provedor atualiza | quando sair versão nova de `claude`, `codex` ou `agy` |
| [conformidade.md](conformidade.md) | teste automático com o provedor real: quando roda, etapas, resultado e efeito de uma falha | quando a versão de um provedor muda ou ao adicionar um provedor |
