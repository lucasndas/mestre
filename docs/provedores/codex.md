# Codex

> **Assunto:** como o Mestre inicia, instrui, vigia e mede agentes Codex (OpenAI) · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Registrar o que o [adaptador](adaptador.md) do Codex precisa saber. Pesquisado na documentação oficial de 2026-10-12 (agora em learn.chatgpt.com/docs; os endereços antigos de developers.openai.com redirecionam) e no código-fonte de `openai/codex`, commit `b3c3bad`. **Versão mínima:** a primeira que passar no [teste de conformidade](conformidade.md), registrada na instalação.

## Comportamento

### Instruções do projeto

- Do diretório raiz do projeto (primeiro ancestral com `.git`) até a pasta atual, pega em cada pasta o primeiro que existir: `AGENTS.override.md`, `AGENTS.md`, nomes de `project_doc_fallback_filenames`. Junta da raiz para baixo; o mais próximo vem por último e vence.
- Limite somado: `project_doc_max_bytes`, **32 KiB** por padrão; passado o limite, para de juntar arquivos.
- `CLAUDE.md` não é lido. A leitura acontece uma vez por sessão.
- **Projeto marcado como não confiável (`untrusted`) pula o `AGENTS.md` do projeto.** Por isso a pré-confiança é obrigatória.

### Camada do Mestre

- Canal preferido: `-c developer_instructions="<conteúdo de MESTRE.md>"` no início, como string TOML (aspas e quebras de linha escapadas). Vira uma mensagem de papel `developer`. O código mostra que o modo interativo aplica a opção; falta testar ao vivo.
- Reserva: hook SessionStart devolvendo o conteúdo em `additionalContext`. Reserva final: primeira mensagem ([personagem](../sessao-zero/personagem.md)).
- Não usar `model_instructions_file` (substitui as instruções embutidas), `instructions` (reservado) nem `experimental_instructions_file` (não existe mais).
- Atualização a cada turno: `additionalContext` do UserPromptSubmit. Acima de cerca de 2.500 tokens o Codex grava o texto em arquivo e manda só um trecho; o contexto do turno fica bem abaixo disso.
- No Windows, a linha de comando tem limite de 32.767 caracteres; o tamanho de `MESTRE.md` é controlado em [personagem](../sessao-zero/personagem.md).

### Skills

- Lê `.agents/skills/` nativamente, de cada pasta entre a atual e a raiz do projeto. Também lê `$HOME/.agents/skills` (compartilhada entre todos os `CODEX_HOME`; o Mestre não usa).
- `name` e `description` são **obrigatórios** no `SKILL.md`. O catálogo ocupa no máximo ~2% da janela de contexto (ou 8.000 caracteres); o corpo só é lido quando a skill é escolhida. Mudanças são detectadas sozinhas.

### Hooks

- **Arquivo:** `.codex/hooks.json` do worktree, no mesmo formato do Claude Code, listado no `.git/info/exclude` ([mesa](../sessao-zero/mesa.md)). Hooks do projeto só carregam com o projeto confiável. Camadas somam: todos os hooks que casam rodam.
- **Eventos do provedor:** SessionStart, SessionEnd, SubagentStart, SubagentStop, PreToolUse, PermissionRequest, PostToolUse, UserPromptSubmit, PreCompact, PostCompact, Stop, Interrupt.
- **Eventos ligados:** SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, PostToolUse, Stop, PreCompact, SessionEnd, Interrupt. Matcher `*`. O matcher `Edit`/`Write` também casa `apply_patch`, mas `tool_name` continua `apply_patch`.
- **Entrada:** JSON no stdin, `snake_case`: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`, `permission_mode`; PreToolUse traz `turn_id`, `tool_name`, `tool_use_id`, `tool_input`; Stop traz `last_assistant_message`; Interrupt traz `turn_id`.
- **Saída:** exit 0 com `hookSpecificOutput` (`hookEventName`, `additionalContext`, `permissionDecision: allow|deny`, `permissionDecisionReason`; PermissionRequest: `decision.behavior`). **`ask` ainda não é aceito.** Texto puro no stdout é inválido em Stop e Interrupt: o despachante não imprime nada neles. Campo não suportado marca o hook como falho.
- **Falha:** exit 2 bloqueia (motivo no stderr). Erro, tempo esgotado e saída inválida **liberam** (falha aberta), sem opção para mudar. A proteção que sobra é o sandbox nativo ([permissões](../classes/permissoes.md)).
- **Tempos limite gerados:** 30 s em PreToolUse, PermissionRequest, UserPromptSubmit e SessionStart; 60 s em PostToolUse, Stop e PreCompact; 3 s (máximo do provedor) em SessionEnd e Interrupt.
- **Confiança dos hooks:** todo hook não gerenciado precisa ser revisado e confiado (`/hooks`). A confiança fica presa ao hash do hook: editar invalida. Por isso o arquivo gerado tem **conteúdo fixo**, igual em todo worktree do mesmo nó (só chama `mestre hook` pelo caminho estável do binário). Pré-gravar a confiança (`[hooks.state."<chave>"] trusted_hash`) é frágil (hash interno), então o MVP não faz isso: a sessão zero guia o usuário na primeira confiança e o [checklist](../plataforma/checklist.md) detecta quando falta.

### Confiança do projeto

- **Pré-confiar:** gravar no `config.toml` do `CODEX_HOME` do perfil `[projects."<caminho>"] trust_level = "trusted"`. A busca usa a pasta atual, a raiz do projeto e a raiz git (num worktree, a do checkout principal); o motor grava a do worktree e a raiz resolvida.
- Projeto sem confiança pula as camadas `.codex/` (configuração, hooks e regras). Sem `trust_level`, o modo interativo abre a tela de confiança.
- O Codex reescreve o `config.toml`: editar preservando o resto do TOML e sem gravações simultâneas de dois personagens do mesmo perfil.

### Flags

| Usadas pela base | Para quê |
|---|---|
| `-c developer_instructions="…"` | camada do Mestre |
| `-s workspace-write` (ou `-s read-only` se a classe não escreve) | sandbox nativo |
| `-a on-request` | pedidos de escalonamento passam pelo PermissionRequest |
| `-c sandbox_workspace_write.network_access=true\|false` | rede da classe |
| `-m <modelo>` | modelo preferido da classe ou ficha, se houver |
| `--search` | só se a classe libera internet sem lista de domínios (a busca hospedada não passa por hook) |

**Proibidas:** `--dangerously-bypass-approvals-and-sandbox` (`--yolo`); `-s danger-full-access`; `--full-auto` (obsoleta); `--disable hooks` ou `features.hooks = false`; `model_instructions_file`; `project_doc_max_bytes = 0`; `--add-dir`; `--dangerously-bypass-hook-trust` (só com decisão registrada em ADR).

### Contas múltiplas

- `CODEX_HOME=<pasta do perfil>` (a pasta precisa existir): configuração, `auth.json`, sessões e logs próprios. Perfil de conta: [bases e fichas](../orquestracao/bases-e-fichas.md).
- No `config.toml` do perfil: `cli_auth_credentials_store = "file"`, para os perfis não disputarem o chaveiro do sistema.
- Login: `codex login --device-auth` (sem navegador no servidor) ou `codex login` (retorno em `localhost:1455`). Verificar: `codex login status` sai com 0 quando logado.

### Uso, custo e rate limit

- Fonte principal: o JSONL da sessão (`transcript_path`, em `$CODEX_HOME/sessions/AAAA/MM/DD/rollout-*.jsonl`). O adaptador lê, a cada Stop, os eventos `token_count` novos: `info.total_token_usage` (`input_tokens`, `cached_input_tokens`, `cache_write_input_tokens`, `output_tokens`, `reasoning_output_tokens`, `total_tokens`), `info.model_context_window` e `rate_limits` (`primary` e `secondary` com `used_percent`, `window_minutes`, `resets_at`; `rate_limit_reached_type`).
- `rate_limit_reached_type` preenchido: o personagem fica "aguardando cota" até `resets_at` ([limites](../plataforma/limites-de-uso.md)). O Codex não informa custo em dinheiro.
- Reserva: `/status` e `/usage` na tela. O formato do JSONL é interno e pode mudar: o teste de conformidade confere.

### Telas que o PTY precisa tratar

| Tela | Ação |
|---|---|
| login | escalar como alerta do checklist |
| confiar na pasta | evitar (pré-confiança); se aparecer num worktree do Mestre, responder "confiar" |
| revisão de hooks na inicialização ("Review hooks" / "Trust all and continue" / "Continue without trusting") | escalar: confiar em tudo poderia aprovar hooks de terceiros vindos do repositório |
| apresentação do sandbox do Windows | escalar na primeira vez por perfil |
| pedido de aprovação nativo (se o PermissionRequest falhar) | responder com a decisão registrada; sem decisão, negar |

## Regras e erros

- Hooks não confiados ou modificados: não rodam; o personagem entra em monitoramento reduzido ([adaptador](adaptador.md#monitoramento-reduzido)) e o checklist pede a confiança.
- Como o Codex falha aberto, a base nunca roda com sandbox desligado.

## Critérios de aceite

- **CA-CODEX-01** · *rápida* — **Dado** dois worktrees no mesmo nó, **quando** `HookFiles` gera `.codex/hooks.json` para os dois, **então** os arquivos são idênticos byte a byte.
- **CA-CODEX-02** · *rápida* — **Dado** um `config.toml` com comentários e outras tabelas, **quando** `PreTrust` roda duas vezes, **então** o projeto fica `trusted` e o resto do arquivo não muda.
- **CA-CODEX-03** · *rápida* — **Dado** um `MESTRE.md` com aspas, quebras de linha e acentos, **quando** `BuildLaunch` monta `-c developer_instructions`, **então** o valor lido de volta como TOML é igual ao arquivo.
- **CA-CODEX-04** · *rápida* — **Dado** um JSONL gravado com `token_count`, **quando** `ReadUsage` lê, **então** o registro tem os tokens e as duas janelas de limite, e marca limite atingido quando `rate_limit_reached_type` vem preenchido.
- **CA-CODEX-05** · *rápida* — **Dado** os eventos Stop e Interrupt, **quando** `FormatResponse` responde, **então** o stdout fica vazio e o exit é 0.
- **CA-CODEX-06** · *completa* — **Dado** o agente falso do Codex mostrando a revisão de hooks, **quando** o adaptador reconhece a tela, **então** o personagem fica "aguardando entrada" e o checklist registra o alerta.

## Depende de

- [Adaptador](adaptador.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md) e [do personagem](../sessao-zero/personagem.md)
- [Permissões](../classes/permissoes.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)

## A confirmar

- **A confirmar:** `-c developer_instructions` no modo interativo, num teste ao vivo (o código diz que sim).
- **A confirmar:** se a chave de confiança de hook inclui o caminho do arquivo. Se incluir, cada worktree novo pede confiança; alternativa a decidir em ADR: hooks na camada de usuário do `CODEX_HOME` do perfil (confiança uma vez por perfil) ou `--dangerously-bypass-hook-trust`.
- **A confirmar:** qual raiz o Codex usa para worktrees saídos do espelho sem área de trabalho, e se `git commit` (o `.git` fica fora do worktree e só leitura no sandbox) gera pedido de escalonamento respondido pelo PermissionRequest.
- **A confirmar:** `cli_auth_credentials_store = "file"` separa as contas de fato (não é recurso oficial); e se o processo de fundo do modo interativo (`/daemon`) respeita `CODEX_HOME`.
- **A confirmar:** exit diferente de 0 e 2 tratado como não bloqueante; nome da ferramenta de shell no `tool_name`.

## Fontes

- https://learn.chatgpt.com/docs/hooks
- https://learn.chatgpt.com/docs/agent-configuration/agents-md
- https://learn.chatgpt.com/docs/config-file/config-reference
- https://learn.chatgpt.com/docs/config-file/config-advanced
- https://learn.chatgpt.com/docs/config-file/environment-variables
- https://learn.chatgpt.com/docs/developer-commands?surface=cli
- https://learn.chatgpt.com/docs/build-skills
- https://learn.chatgpt.com/docs/sandboxing
- https://learn.chatgpt.com/docs/permissions
- https://learn.chatgpt.com/docs/auth
- https://learn.chatgpt.com/docs/non-interactive-mode
- https://learn.chatgpt.com/docs/windows/windows-sandbox
- https://github.com/openai/codex (código-fonte, commit `b3c3bad`)
