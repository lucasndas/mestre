# ADR 0010: Instruções em duas camadas

> **Assunto:** por que as regras do Mestre ficam fora do AGENTS.md e entram pelo canal mais forte de cada provedor · **Componente:** Todos · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- Cada personagem precisa de dois tipos de instrução:
  - **as regras do Mestre:** CLI, `done`, segurança, a sua classe e a sua missão;
  - **as regras do projeto:** stack, comandos, convenções.
- O `AGENTS.md` **pertence ao projeto e à pessoa**. Ele é lido nativamente pelos três provedores:
  - o Claude Code lê desde a v2.1.277, mas só quando não há `CLAUDE.md` nem `CLAUDE.local.md`;
  - o Codex e o Antigravity sempre leem.
- O que é específico de cada personagem não pode ir para arquivos do projeto, porque esses arquivos são compartilhados por todos os agentes e por quem clona o repositório.
- O Mestre precisa prevalecer em caso de conflito, sem apagar as regras do projeto.

## Opções consideradas

1. **Escrever as regras do Mestre no `AGENTS.md`.**
   - Prós: um arquivo só, lido pelos três provedores.
   - Contras:
     - Altera um arquivo da pessoa.
     - Afeta quem clona o repositório sem usar o Mestre.
     - Não diferencia classe nem missão por personagem.
     - Viola o MVP-3.
2. **Usar os arquivos globais do usuário** (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.gemini/AGENTS.md`).
   - Prós: não toca no projeto.
   - Contras:
     - Afeta todo uso desses agentes fora do Mestre.
     - Continua sem nada por personagem.
3. **Substituir o prompt de sistema inteiro** (ex.: `--system-prompt` no Claude Code, `model_instructions_file` no Codex).
   - Prós: controle total.
   - Contras:
     - Joga fora as instruções de fábrica do provedor, o que piora o comportamento.
     - O Antigravity não oferece isso.
4. **Arquivo de agente nativo de cada provedor** (`.claude/agents/<nome>.md` + `claude --agent`, `.codex/agents/<nome>.toml`, `.agents/agents/<nome>.md` + `agy --agent`), gerado a partir da classe.
   - Prós: usa o mecanismo de "papel" que cada provedor já tem, com campos de ferramentas e modelo.
   - Contras:
     - **Claude Code:** o corpo do agente **substitui** o prompt de fábrica inteiro, como `--system-prompt` (cai na opção 3).
     - **Codex:** o arquivo de agente só vale para subagentes; não há como a sessão principal assumir um, e o código só aplica dele as instruções e o modelo.
     - Não existe padrão aberto de definição de agente: os três formatos são incompatíveis, e o Agent Plugins 1.0 cobre só skills e MCP. Seriam três geradores sem ganho sobre os canais da opção 5.
     - No **Antigravity** o arquivo de agente é o único canal forte que existe, então ele é usado lá, só como recipiente da camada do Mestre (decisão abaixo).
5. **Duas camadas.**
   - A **camada do Mestre** é um arquivo por personagem (`MESTRE.md`), entregue pelo canal mais forte que cada provedor oferece.
   - A **camada do projeto** é o `AGENTS.md`, intocado.
   - Prós:
     - O projeto vale integralmente.
     - O Mestre prevalece só onde há choque.
     - Tudo é por personagem.
   - Contras:
     - Cada provedor tem um canal diferente.
     - Alguns canais não estão confirmados.

## Decisão

- **Duas camadas.** A camada do Mestre é montada por personagem e começa pela regra de prioridade; a ordem completa está em [precedência](../arquitetura/precedencia.md). A camada do projeto é o `AGENTS.md`, que o Mestre nunca edita.
- **Canal mais forte por provedor.** Os canais escolhidos e a reserva universal (primeira mensagem) estão em [sessão zero do personagem](../sessao-zero/personagem.md). O porquê de cada escolha:
  - **Claude Code:** `--append-system-prompt-file`. A documentação garante que funciona no modo interativo e que soma ao prompt de fábrica.
  - **Codex:** não há flag de prompt de sistema.
    - O canal preferido é `-c developer_instructions=...`, porque entra na partida, sem depender de hook confiado. A documentação e o código confirmam que vale no modo interativo e que **soma** uma mensagem de papel developer às instruções de fábrica.
    - A reserva é o hook `SessionStart` com `additionalContext`.
  - **Antigravity:** não há flag de prompt de sistema nem `additionalContext`.
    - O canal é um **arquivo de agente próprio**, cujo corpo vira o prompt de sistema, selecionado com `--agent`. Ele é gerado por personagem e fica fora do git.
    - A reserva é o `PreInvocation` com mensagem efêmera.
- **Camada do Mestre curta.** Os detalhes da CLI ficam nas skills, carregadas sob demanda ([ADR 0011](0011-agents-skills-pasta-oficial.md)).
- **Sem `AGENTS.md` no projeto:** o Grão-Mestre propõe um, e o usuário aprova ([sessão zero do workspace](../sessao-zero/workspace.md)).

## Consequências

- **Positivas:**
  - O projeto não é alterado (MVP-3).
  - Cada personagem recebe a sua classe e a sua missão.
  - Trocar de provedor não muda a fonte das regras.
- **Negativas:**
  - **Canais diferentes:** três canais distintos para manter, cada um com um adaptador ([adaptador](../provedores/adaptador.md)). O teste de conformidade confere, em cada provedor, que as duas camadas chegaram e que o Mestre vence um conflito ([conformidade](../provedores/conformidade.md)).
  - **Camadas de projeto diferentes:** com `CLAUDE.md` no projeto, os agentes Claude leem um arquivo e os outros leem outro. A camada do Mestre não é afetada, mas o usuário é avisado.
  - **Limite do Codex:** as instruções somadas têm limite de **32 KiB** por padrão, o que obriga a camada do Mestre a ser enxuta.
  - **Força desigual:** no Codex, `developer_instructions` entra como mensagem de papel "developer", não como prompt de sistema. A prioridade depende da regra escrita no início da camada.
  - **Atualização por turno:** decretos que mudam comportamento precisam atualizar a camada a cada turno, pelo hook de início de turno ou pela reserva ([decretos](../telegram/decretos.md)).
- **Pontos conferidos na implementação** (no Antigravity, o arquivo de agente com `--agent` no modo interativo e se o prompt de fábrica continua; nos três, que as camadas chegaram e o prompt de fábrica continua, na etapa 2 do [teste de conformidade](../provedores/conformidade.md)): os itens completos estão em [sessão zero do personagem](../sessao-zero/personagem.md#verificar-na-implementação), [Codex](../provedores/codex.md#verificar-na-implementação) e [Antigravity](../provedores/antigravity.md#verificar-na-implementação).

## Fontes

- Claude Code, flags de prompt de sistema: https://code.claude.com/docs/en/cli-reference
- Claude Code, AGENTS.md e CLAUDE.md: https://code.claude.com/docs/en/memory#agents-md
- Codex, AGENTS.md e limite: https://learn.chatgpt.com/docs/agent-configuration/agents-md
- Codex, `developer_instructions`: https://learn.chatgpt.com/docs/config-file/config-reference
- Antigravity, regras e arquivos de agente: https://antigravity.google/docs/rules , https://antigravity.google/docs/subagents
- Claude Code, subagentes e `--agent` (substitui o prompt padrão): https://code.claude.com/docs/en/sub-agents
- Codex, agentes como subagentes: https://learn.chatgpt.com/docs/agent-configuration/subagents
- Agent Plugins 1.0 (só skills e MCP): https://agent-plugins.org , https://aaif.io/blog/from-skills-and-tools-to-portable-agent-plugins
