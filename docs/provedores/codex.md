# Codex

> **Assunto:** como o Mestre inicia, instrui, vigia e mede agentes Codex (OpenAI) · **Componente:** Todos · **Atualizado:** 2026-10-14

## Objetivo

Registrar o que o [adaptador](adaptador.md) do Codex precisa saber. Pesquisado na documentação oficial de 2026-10-12 (agora em learn.chatgpt.com/docs; os endereços antigos de developers.openai.com redirecionam) e no código-fonte de `openai/codex`, commit `b3c3bad`. **Versão mínima:** a primeira que passar no [teste de conformidade](conformidade.md), registrada na instalação.

## Comportamento

### Instruções do projeto

- Do diretório raiz do projeto (primeiro ancestral com `.git`) até a pasta atual, pega em cada pasta o primeiro que existir: `AGENTS.override.md`, `AGENTS.md`, nomes de `project_doc_fallback_filenames`. Junta da raiz para baixo; o mais próximo vem por último e vence.
- Limite somado: `project_doc_max_bytes`, **32 KiB** por padrão; passado o limite, para de juntar arquivos.
- `CLAUDE.md` não é lido. A leitura acontece uma vez por sessão.
- **Projeto marcado como não confiável (`untrusted`) pula o `AGENTS.md` do projeto.** Por isso a pré-confiança é obrigatória.

### Camada do Mestre

- Canal preferido: `-c developer_instructions="<conteúdo de MESTRE.md>"` no início, como string TOML (aspas e quebras de linha escapadas). Vira uma mensagem de papel `developer` que **soma** às instruções de fábrica. A documentação e o código confirmam que o modo interativo aplica a opção; a etapa 2 do [teste de conformidade](conformidade.md) continua conferindo a cada versão.
- Arquivos de agente do Codex (`.codex/agents/<nome>.toml`) só valem para subagentes; o Mestre não os gera ([ADR 0010](../decisoes/0010-instrucoes-em-duas-camadas.md)).
- Reserva: hook SessionStart devolvendo o conteúdo em `additionalContext`. Reserva final: primeira mensagem ([personagem](../sessao-zero/personagem.md)).
- Não usar `model_instructions_file` (substitui as instruções embutidas), `instructions` (reservado) nem `experimental_instructions_file` (não existe mais).
- Atualização a cada turno: `additionalContext` do UserPromptSubmit. Acima de cerca de 2.500 tokens o Codex grava o texto em arquivo e manda só um trecho; o contexto do turno fica bem abaixo disso.
- No Windows, a linha de comando tem limite de 32.767 caracteres; o tamanho de `MESTRE.md` é controlado em [personagem](../sessao-zero/personagem.md).

### Skills

- Lê `.agents/skills/` nativamente, de cada pasta entre a atual e a raiz do projeto. Também lê `$HOME/.agents/skills` (compartilhada entre todos os `CODEX_HOME`; o Mestre não usa).
- **Skills por personagem** (nível e classe): o Codex não tem pasta por personagem, então o conteúdo delas vai como seção compacta no `MESTRE.md` do personagem; `.agents/skills/` do worktree só tem a de comunicação ([personagem](../sessao-zero/personagem.md#skills-por-personagem)).
- `name` e `description` são **obrigatórios** no `SKILL.md`. O catálogo ocupa no máximo ~2% da janela de contexto (ou 8.000 caracteres); o corpo só é lido quando a skill é escolhida. Mudanças são detectadas sozinhas.

### Hooks

- **Arquivo:** `hooks.json` na **camada de usuário do perfil de conta** (`$CODEX_HOME/hooks.json`), no mesmo formato do Claude Code. Como o `CODEX_HOME` de cada perfil é exclusivo do Mestre ([perfis de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta)), os hooks não afetam o uso pessoal do Codex e não dependem do caminho de cada worktree. Nada de hooks do Codex é gravado no worktree. O arquivo inteiro é a parte gerenciada: a confiança é presa à posição de cada hook nele. Camadas somam: hooks de projeto do próprio repositório continuam rodando junto, se a pessoa tiver.
- **Eventos do provedor:** SessionStart, SessionEnd, SubagentStart, SubagentStop, PreToolUse, PermissionRequest, PostToolUse, UserPromptSubmit, PreCompact, PostCompact, Stop, Interrupt.
- **Eventos ligados:** SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, PostToolUse, Stop, PreCompact, SessionEnd, Interrupt. Matcher `*`, menos em UserPromptSubmit, Stop e Interrupt, onde o Codex o ignora. O matcher `Edit`/`Write` também casa `apply_patch`, mas `tool_name` continua `apply_patch`.
- **Entrada:** JSON no stdin, `snake_case`: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`, `permission_mode`; PreToolUse traz `turn_id`, `tool_name`, `tool_use_id`, `tool_input`; Stop traz `last_assistant_message`; Interrupt traz `turn_id`.
- **Saída:** exit 0 com `hookSpecificOutput` (`hookEventName`, `additionalContext`, `permissionDecision: allow|deny`, `permissionDecisionReason`; PermissionRequest: `decision.behavior`). **`ask` ainda não é aceito.** Texto puro no stdout é inválido em Stop e Interrupt: o despachante não imprime nada neles. Campo não suportado marca o hook como falho.
- **Falha:** exit 2 bloqueia (motivo no stderr). Erro, tempo esgotado e saída inválida **liberam** (falha aberta), sem opção para mudar. O despachante só sai com 0 ou 2; qualquer outro código é tratado como falha, que libera. A proteção que sobra é o sandbox nativo ([reforço nativo](../classes/reforco-nativo.md)). Os hooks rodam **fora** do sandbox, pelo shell do usuário (`sh -c`, `cmd /c` ou PowerShell), como o processo do Codex: o `mestre hook` alcança o canal local mesmo sem rede.
- **Tempos limite gerados:** 30 s em PreToolUse, PermissionRequest, UserPromptSubmit e SessionStart; 60 s em PostToolUse, Stop e PreCompact; 3 s (máximo do provedor) em SessionEnd e Interrupt.
- **Confiança dos hooks:** todo hook não gerenciado precisa ser revisado e confiado (`/hooks`), e a confiança fica presa ao hash do hook (editar invalida). Por isso o arquivo tem **conteúdo fixo** (só chama `mestre hook` pelo caminho estável do binário) e é confiado **uma vez por perfil**, na **configuração inicial do perfil Codex**: depois do login, o painel abre uma sessão do Codex com aquele perfil e guia o usuário a aprovar os hooks do Mestre em `/hooks` ([perfis de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta)). O [checklist](../plataforma/checklist.md) detecta perfil sem a confiança (ALL-11), e o perfil não recebe personagens até ela existir. Plano B, se a confiança não valer para worktrees novos: [Verificar](#verificar-na-implementação).

### Confiança do projeto

- **Pré-confiar:** gravar no `config.toml` do `CODEX_HOME` do perfil `[projects."<caminho>"] trust_level = "trusted"`. A busca usa a pasta atual, a raiz do projeto e a raiz git (num worktree, a do checkout principal), nessa ordem; o motor grava a do worktree, que é a pasta atual do personagem e vem primeiro, e **só** ela: nunca a raiz git resolvida, que num worktree saído do espelho é o espelho e confiaria todos os worktrees dele (o mesmo problema do [Claude Code](claude-code.md)).
- Projeto sem confiança pula as camadas `.codex/` (configuração, hooks e regras). Sem `trust_level`, o modo interativo abre a tela de confiança.
- O Codex reescreve o `config.toml`: editar preservando o resto do TOML e sem gravações simultâneas de dois personagens do mesmo perfil.
- **Confirmado no spike do Linux** ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)): com o `trust_level` gravado, a tela de confiança não aparece; ao responder a tela, o Codex grava a chave do próprio worktree.

### Flags

| Usadas pela base | Para quê |
|---|---|
| `--no-daemon` | **obrigatória**: sem processo de fundo compartilhado (abaixo) |
| `-c developer_instructions="…"` | camada do Mestre |
| `-s workspace-write` (ou `-s read-only` se a classe não escreve) | sandbox nativo |
| `-a on-request` | pedidos de escalonamento passam pelo PermissionRequest |
| `-c sandbox_workspace_write.network_access=true\|false` | rede da classe; com `false`, bloqueia também o canal local (abaixo) |
| `-m <modelo>` | modelo preferido da classe, se houver |
| `--search` | só se a classe libera internet sem lista de domínios (a busca hospedada não passa por hook) |

### Processo de fundo e `--no-daemon`

Sem `--no-daemon`, o TUI sobe `codex app-server --managed-daemon`, **um por perfil**, compartilhado por todos os terminais daquele `CODEX_HOME` (spike do Linux, [segurança](../arquitetura/seguranca.md#identidade-de-quem-chama-a-cli)). Ele vira filho do init, ignora SIGTERM, fica no cgroup do terminal que o iniciou, copia ~430 MB para `$CODEX_HOME/packages/app-server-daemon/` e tem atualizador próprio; os comandos da ferramenta de shell viram filhos dele. Resultado: a identificação do chamador quebra, as variáveis `MESTRE_*` do personagem não chegam aos comandos e o cgroup por terminal aponta o terminal errado. Com `--no-daemon`, tudo descende do TUI, a variável chega e não há daemon nem cópia. Por isso a flag é obrigatória na base.

### Canal local dentro do sandbox

No Codex 0.162.1, com `-s workspace-write` e `network_access=false`, o `connect()` ao socket Unix do [canal local](../arquitetura/canal-local.md) devolve `EPERM`: a CLI `mestre` chamada pela ferramenta de shell não alcança o motor. Com `sandbox_workspace_write.network_access=true`, alcança. O código-fonte (commit `2f05c0a`) explica: no Linux, sem rede, o seccomp nega **todo** `connect()`, socket Unix inclusive, seja qual for a pasta, e não há regra que libere um socket só (a lista `unix_sockets` de `[permissions]` só vale no macOS, com o proxy de rede). Nenhuma pasta do socket entra em `writable_roots`, que deixaria um agente apagar ou trocar o socket de todos. O que vale até a conformidade decidir está em [Verificar](#verificar-na-implementação).

**Proibidas:** iniciar sem `--no-daemon`; `--dangerously-bypass-approvals-and-sandbox` (`--yolo`); `-s danger-full-access`; `--full-auto` (obsoleta); `--disable hooks` ou `features.hooks = false`; `model_instructions_file`; `project_doc_max_bytes = 0`; `--add-dir`; `--dangerously-bypass-hook-trust`. O `BuildLaunch` recusa essas flags (também como `-c`) e o início sem `-s`, e acrescenta `--no-daemon`, `-a on-request` e `-m` quando faltam.

### Contas múltiplas

- `CODEX_HOME=<pasta do perfil>` (a pasta precisa existir): configuração, `auth.json`, sessões e logs próprios. Perfil de conta: [bases e fichas](../orquestracao/bases-e-fichas.md).
- No `config.toml` do perfil: `cli_auth_credentials_store = "file"`, sempre explícito, para os perfis não disputarem o chaveiro do sistema. Confirmado no Linux e no Windows (spike): a credencial fica em `CODEX_HOME/auth.json` (no Linux, modo `0600`); no Windows o arquivo já é o padrão sem `config.toml`, mas a linha fica explícita para não depender do padrão.
- **Instalação:** o pacote completo da versão (`bin/`, `codex-path/`, `codex-resources/`, `codex-package.json`); o binário `codex` solto não funciona, também no Windows, onde o spike confirmou que o `codex.exe` precisa da pasta inteira da versão (cerca de 450 MB, com `codex-path\rg.exe` e `codex-resources\`) ([instalação](../plataforma/instalacao-e-atualizacao.md#clis-dos-provedores)).
- **Escritas fora do perfil** (Linux): `/tmp/codex-daemon-<uid>`, `/tmp/codex-bwrap-synthetic-mount-targets-<uid>` e `/var/tmp/etilqs_*` (temporários do SQLite); limpeza em [personagem](../sessao-zero/personagem.md#variáveis-de-ambiente).
- Login, feito como `mestre-agente` na configuração inicial do perfil: `codex login --device-auth` (o Dono abre o link e digita o código no próprio navegador); `codex login` (retorno em `localhost:1455`) só serve com o navegador na mesma máquina. No Windows, `USERPROFILE` é o do perfil carregado do `mestre-agente` ([Windows](../arquitetura/windows.md#conta-dos-agentes-mestre-agente)); o instalador oficial põe o pacote em `%USERPROFILE%\.codex\packages\standalone\current\` (junção em `%LOCALAPPDATA%\Programs\OpenAI\Codex\bin`, no `Path` do usuário) e faz o login no `CODEX_HOME` padrão, então a configuração do perfil de conta define o `CODEX_HOME` antes do login. Verificar: `codex login status` sai com 0 quando logado.

### Uso, custo e rate limit

- Fonte principal: o JSONL da sessão (`transcript_path`, em `$CODEX_HOME/sessions/AAAA/MM/DD/rollout-*.jsonl`; linhas `event_msg`). O adaptador lê, no fim do turno e periodicamente, as linhas novas desde a leitura anterior (o motor guarda o cursor): dos eventos `token_count`, `info.total_token_usage` (`input_tokens`, `cached_input_tokens`, `cache_write_input_tokens`, `output_tokens`, `reasoning_output_tokens`, `total_tokens`; o registro é a diferença dos totais), `info.last_token_usage` e `info.model_context_window` (o % de contexto) e `rate_limits` (`primary` e `secondary` com `used_percent`, `window_minutes`, `resets_at` em segundos Unix; `rate_limit_reached_type`). Só lê dentro de `sessions/` do perfil, sem seguir link: o caminho vem do payload.
- `rate_limit_reached_type` preenchido (só no `token_count` do limite; os seguintes voltam a `null`): o personagem fica "aguardando cota" até `resets_at` da janela cheia ([limites](../plataforma/limites-de-uso.md)). O Codex não informa custo em dinheiro.
- **`turn_failure`:** o Codex não roda o Stop num turno que termina em erro; o erro fica no `task_complete` do turno (`error.message`, `error.codex_error_info`: `usage_limit_exceeded` e `rate_limit_exceeded` viram `rate_limit`, `unauthorized` vira `auth`, o resto `error`). A mesma leitura devolve esses turnos como `turn_failure`; o `mestre hook` também confere, no Stop, o fim do transcript do turno.
- Reserva: `/status` e `/usage` na tela. O formato do JSONL é interno e pode mudar: o teste de conformidade confere.

### Digitação

Mensagens vão com colagem entre colchetes e `\r` ([digitação de mensagens](adaptador.md#digitação-de-mensagens)). Na caixa de entrada do Codex, `\r` envia e uma quebra de linha dentro da colagem só quebra a linha.

### Telas que o PTY precisa tratar

| Tela | Ação |
|---|---|
| login | escalar como alerta do checklist |
| confiar na pasta | evitar (pré-confiança); se aparecer num worktree do Mestre, responder "confiar" |
| revisão de hooks na inicialização ("Hooks need review": "Review hooks" / "Trust all and continue" / "Continue without trusting") | escalar, com o alerta ALL-11 do checklist: confiar em tudo poderia aprovar hooks de terceiros vindos do repositório |
| atualização ao iniciar ("Update now" / "Skip" / "Skip until next version"; `check_for_update_on_startup` fica ligado) | responder "Update now": o Mestre deixa o CLI se atualizar ([atualização dos CLIs](atualizacao-dos-clis.md)) |
| apresentação do sandbox do Windows | escalar na primeira vez por perfil |
| pedido de aprovação nativo (se o PermissionRequest falhar: "Would you like to run the following command?", "Do you want to approve network access…") | responder com a decisão registrada (`y`); sem decisão, negar (`Esc`) |

Quem chama estas operações na criação de mesas e personagens é a sessão zero ([mesa](../sessao-zero/mesa.md), [personagem](../sessao-zero/personagem.md)); os perfis de conta estão em [bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta).

## Regras e erros

- Hooks não confiados ou modificados: não rodam; o personagem entra em monitoramento reduzido ([adaptador](adaptador.md#monitoramento-reduzido)) e o checklist pede a confiança.
- Como o Codex falha aberto, a base nunca roda com sandbox desligado.

## Critérios de aceite

- **CA-CODEX-01** · *rápida* — **Dado** dois perfis de conta Codex na mesma máquina, **quando** `HookFiles` gera o `hooks.json` de cada `CODEX_HOME`, **então** os arquivos são idênticos byte a byte e nenhum arquivo de hooks do Codex é criado no worktree.
- **CA-CODEX-02** · *rápida* — **Dado** um `config.toml` com comentários e outras tabelas, **quando** `PreTrust` roda duas vezes, **então** o projeto fica `trusted` e o resto do arquivo não muda.
- **CA-CODEX-03** · *rápida* — **Dado** um `MESTRE.md` com aspas, quebras de linha e acentos, **quando** `BuildLaunch` monta `-c developer_instructions`, **então** o valor lido de volta como TOML é igual ao arquivo.
- **CA-CODEX-04** · *rápida* — **Dado** um JSONL gravado com `token_count`, **quando** `ReadUsage` lê, **então** o registro tem os tokens e as duas janelas de limite, e marca limite atingido quando `rate_limit_reached_type` vem preenchido.
- **CA-CODEX-05** · *rápida* — **Dado** os eventos Stop e Interrupt, **quando** `FormatResponse` responde, **então** o stdout fica vazio e o exit é 0.
- **CA-CODEX-06** · *completa* — **Dado** o agente falso do Codex mostrando a revisão de hooks, **quando** o adaptador reconhece a tela, **então** o personagem fica `blocked` com motivo `input` e o checklist registra o alerta.

## Depende de

- [Adaptador](adaptador.md) · [Despachante de hooks](despachante-de-hooks.md)
- [Reforço nativo](../classes/reforco-nativo.md)

## Verificar na implementação

Conferidos pelo [teste de conformidade](conformidade.md), salvo onde indicado.

- **Verificar na implementação:** a confiança dada uma vez em `/hooks`, na configuração inicial do perfil, vale para todos os worktrees daquele perfil. **Padrão:** vale, porque os hooks estão na camada de usuário e a confiança fica presa ao hash do hook. **Se falhar:** o motor grava no perfil a entrada de confiança (`trusted_hash`) dos hooks do Mestre, calculada pelo mesmo algoritmo do Codex e validada pela conformidade (o hook tem conteúdo fixo, então o hash é conhecido): `[hooks.state."<caminho do hooks.json>:<evento em snake_case>:0:0"]`, com `sha256:` e o SHA-256 do JSON compacto, chaves ordenadas, de `event_name`, `matcher` (onde o Codex o usa) e o hook com `async` e `timeout` (chave `NativeFallbacks.CodexHookTrust`); se isso também falhar, o painel guia a confiança em `/hooks` por worktree, e o personagem fica `blocked` (`input`) até ela existir. **Onde:** etapas 4 a 6, que rodam num worktree novo a cada execução.
- **Verificar na implementação:** texto de várias linhas colado entre colchetes e seguido de `\r` é enviado inteiro como uma mensagem. **Padrão:** como em Digitação. **Se falhar:** mensagens de várias linhas vão pelo arquivo de mensagem longa ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)). **Onde:** etapa 2 do [teste de conformidade](conformidade.md), com a pergunta em três linhas.
- **Verificar na implementação:** `git commit` num worktree saído do espelho (o `.git` do worktree fica no espelho, fora da pasta e só leitura no sandbox) gera pedido de escalonamento respondido pelo PermissionRequest. **Padrão:** sim; o motor responde pela regra da classe. **Se falhar:** a base acrescenta a pasta git daquele worktree no espelho em `sandbox_workspace_write.writable_roots`; a proteção das outras branches continua no hook ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)). **Onde:** etapa 5.
- **Verificar na implementação:** a base do Codex deixa a CLI `mestre` alcançar o socket do canal local numa classe sem internet, sem liberar a rede inteira. **Padrão:** uma regra do sandbox que libere só esse socket, se a versão tiver; o código atual não tem (acima), e as outras formas pensadas não servem: a pasta do socket em `writable_roots` deixaria o socket apagável e não passa do seccomp, e um socket dentro de uma pasta do personagem (`MESTRE_TMP`) também não. Até a conformidade decidir, a classe sem internet fica com `network_access=false` (a barreira nativa de rede vale e a CLI não alcança o motor de dentro da ferramenta de shell; os hooks alcançam). Classe com internet: `network_access=true`, que já libera o socket. **Se falhar:** a base do Codex roda sempre com `network_access=true` (`NativeFallbacks.CodexOfflineNetwork`); numa classe sem internet, a rede fica contida só pelo despachante, que no Codex falha aberto, e o painel mostra na ficha que essa combinação não tem reforço nativo de rede ([reforço nativo](../classes/reforco-nativo.md)). **Onde:** teste de conformidade do Codex com uma classe sem internet: o comando da etapa 5 chama também `mestre list` (tem de responder com o terminal certo; no Windows, o sandbox elevado roda os comandos como `CodexSandboxOffline`/`CodexSandboxOnline`, fora da ACL do socket e talvez do Job do terminal) e um `curl` para fora (tem de falhar).
- **Verificar na implementação:** `cli_auth_credentials_store = "file"` separa as contas de fato (não é recurso oficial) com dois perfis Codex de contas diferentes. O processo de fundo e os hooks rodando nele deixaram de importar: com `--no-daemon` não há daemon. **Padrão:** separa. **Se falhar:** só um perfil de conta Codex por máquina; o checklist recusa o segundo. **Onde:** suíte pesada, com dois perfis Codex de contas diferentes na mesma VM (`codex login status` em cada um).

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
