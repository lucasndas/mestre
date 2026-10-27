# Provedores

> **Assunto:** como o Mestre conversa com Claude Code, Codex e Antigravity CLI, e como testa uma classe em cada um · **Componente:** Todos · **Atualizado:** 2026-10-20

| Documento | O que cobre | Quando ler |
|---|---|---|
| [adaptador.md](adaptador.md) | contrato do adaptador, formato interno de evento, mapa de eventos e de ferramentas, telas interativas, digitação de mensagens (`TypeMessage`), retomada de sessão, monitoramento reduzido | antes de mexer em qualquer provedor ou na entrega de instruções e mensagens |
| [teste-de-classe.md](teste-de-classe.md) | `mestre class test`: casos da classe com o provedor real, efeitos esperados e proibidos, limiar de execuções, resultado por versão, custo | ao testar se uma classe faz o que diz em cada provedor |
| [despachante-de-hooks.md](despachante-de-hooks.md) | o comando `mestre hook`: fluxo, prazos, modo degradado, erro interno, respostas por provedor | ao mexer nos hooks ou nas decisões de permissão na máquina |
| [claude-code.md](claude-code.md) | Claude Code: instruções, camada do Mestre, skills, hooks, confiança, flags, contas, uso e rate limit, telas | ao implementar ou atualizar o adaptador do Claude Code |
| [codex.md](codex.md) | Codex: instruções, camada do Mestre, skills, hooks e confiança dos hooks, sandbox, flags, contas, uso e rate limit, telas | ao implementar ou atualizar o adaptador do Codex |
| [codex-no-windows.md](codex-no-windows.md) | o sandbox do Codex no Windows: o modo `unelevated` e por que, o que se mediu nos três modos, rede sem barreira nativa, `mxc` como evolução | ao mexer no sandbox ou na isolação dos personagens Codex no Windows |
| [antigravity.md](antigravity.md) | Antigravity CLI (`agy`): instruções, agente próprio, skills, os cinco hooks e a falha fechada, confiança, flags, contas, uso, telas | ao implementar ou atualizar o adaptador do Antigravity |
| [atualizacao-dos-clis.md](atualizacao-dos-clis.md) | os CLIs se atualizam sozinhos; o Mestre detecta a versão nova por máquina e perfil, roda a conformidade logo depois, marca o provedor como não conforme se ela falhar e avisa o Senhor da Guilda | quando sair versão nova de `claude`, `codex` ou `agy` |
| [conformidade.md](conformidade.md) | teste automático com o provedor real: quando roda, etapas, resultado e efeito de uma falha | quando a versão de um provedor muda ou ao adicionar um provedor |
