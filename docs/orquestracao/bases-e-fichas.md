# Bases e fichas

> **Assunto:** campos de bases e fichas, exemplos, exportação/importação YAML/JSON e perfis de conta · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Todo terminal nasce de uma **base** (o "como iniciar") e todo agente de uma **ficha** (o "quem é"). São presets definidos pelos usuários; agentes só os usam, nunca os criam nem editam. Assim nenhum agente consegue abrir um terminal com comando livre.

## Comportamento

### Onde ficam e quem edita

- No banco da Cidadela, valendo para todas as campanhas. Criar, editar e apagar: pelo painel ([administração](../painel/administracao.md)), só no nível que a [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades) permite.
- Agentes só leem (`mestre sheet list|show`, `mestre base list|show`; sintaxe em [coordenação](../cli/coordenacao.md)).
- Alterações valem para personagens **novos**. Os vivos guardam a cópia usada ao nascer ([registro de terminais](personagens.md#registro-de-terminais)).

### Base

| Campo | Tipo | Obrigatório | Significado |
|---|---|---|---|
| `name` | texto `[a-z0-9-]`, único | sim | ex.: `claude-code` |
| `command`, `args` | texto, lista | sim | programa e argumentos |
| `shell` | `bash`\|`pwsh`\|`powershell`\|`cmd` | não | se presente, o comando roda dentro desse shell; senão, execução direta |
| `cwd` | caminho com variáveis | não (padrão `{mesa}`) | pasta inicial |
| `env` | mapa | não | variáveis de ambiente extras |
| `platforms` | lista de `{os, command?, args?}` | sim | sistemas onde pode rodar, com comando próprio por sistema quando preciso |
| `machines` | lista de ids de máquina | não | restringe a essas máquinas da Guilda |
| `requires` | mapa de requisitos | não | o que a máquina precisa ter, conferido contra a [ficha da máquina](../guilda/ficha-da-maquina.md) (ex.: `gpu: true`) |
| `ready` | `hook` \| `pattern: <regex>` \| `delay: <segundos>` | sim | como saber que está pronto para receber entrada |

- **Variáveis de `cwd`:** `{mesa}` = worktree da mesa na máquina (para o Grão-Mestre, o clone principal); `{workspace}` = clone principal na Cidadela, ou `<dados da máquina>/workspaces/<slug>` numa Torre; `{home}` = pasta pessoal da conta dos agentes (`mestre-agente`). Para personagens com agente, `cwd` precisa ficar dentro de `{mesa}`.
- **`ready`:** `hook` espera o evento de início de sessão do `mestre hook` (padrão para agentes); `pattern` procura a regex na última linha da tela (padrão para shells, também usada para detectar o fim de cada comando); a tela vem sem os espaços do fim da linha, e o padrão `\$ $` casa com ou sem o espaço depois do prompt; `delay` só como último recurso.
- O motor acrescenta sozinho as opções da camada do Mestre, as variáveis `MESTRE_*` e as do perfil de conta ([sessão zero do personagem](../sessao-zero/personagem.md)); a base não as define. Opções proibidas por provedor (ex.: em [Antigravity](../provedores/antigravity.md)) são recusadas ao salvar.

### Ficha

| Campo | Tipo | Obrigatório | Significado |
|---|---|---|---|
| `name` | texto, único | sim | ex.: "Dev Claude" |
| `base` | nome de base | sim | como iniciar |
| `agent` | `claude-code`\|`codex`\|`antigravity`\|`shell` | sim | tipo de agente (escolhe o [adaptador](../provedores/adaptador.md)); `shell` não recebe skills nem hooks |
| `class` | nome de classe | não | classe padrão; `recruit --class` substitui |
| `account` | nome de perfil de conta | não | conta/assinatura usada (abaixo); sem ela, o perfil padrão do provedor na máquina |
| `mestre_skills` | booleano | não (padrão `true`) | recebe as skills do Mestre |
| `extra_skills` | lista | não | skills extras além das da classe |
| `preferred_machine` | id de máquina | não | preferência na [escolha da máquina](personagens.md#escolha-da-máquina) |
| `requires` | mapa de requisitos | não | soma aos da base |
| `lifecycle` | `ephemeral`\|`persistent` | não (padrão `ephemeral`) | [ciclo de vida](personagens.md#ciclo-de-vida-definido-na-ficha) |
| `max_idle` | duração | não (padrão `120m`) | tempo máximo ocioso (persistentes) |
| `max_copies` | inteiro ≥ 1 | não (padrão 3) | cópias simultâneas |
| `restrictions` | mesmo formato de `permissions.yaml` | não | só restringe a classe, nunca libera ([permissões](../classes/permissoes.md)) |

Validação ao salvar: a base existe; `agent` é compatível com o `ready` da base (`hook` exige agente diferente de `shell`); `restrictions` não libera nada. Nome de base: `a-z`, dígitos e `-`, de 1 a 64 caracteres; nome de ficha: de 1 a 80, sem espaços nas pontas nem caracteres de controle; `ready.delay`: de 1 a 3600 s; `platforms` e `machines` não repetem item.

### Exemplos

```yaml
kind: mestre/presets
version: 1
bases:
  - name: claude-code
    command: claude
    platforms: [{os: linux}, {os: windows, command: claude.exe}]
    ready: hook
  - name: codex
    command: codex
    platforms: [{os: linux}, {os: windows, command: codex.exe}]
    ready: hook
  - name: antigravity
    command: agy
    platforms: [{os: linux}, {os: windows, command: agy.exe}]
    ready: hook
  - name: bash
    command: bash
    platforms: [{os: linux}]
    ready: {pattern: '\$ $'}
  - name: powershell
    command: pwsh
    platforms: [{os: windows}]
    ready: {pattern: 'PS .*> $'}
sheets:
  - {name: Dev Claude, base: claude-code, agent: claude-code, class: Dev, lifecycle: persistent, max_idle: 240m}
  - {name: Revisor Codex, base: codex, agent: codex, class: Revisor}
  - {name: QA Antigravity, base: antigravity, agent: antigravity, class: QA, account: agy-pessoal}
```

### Exportar e importar

- Formato único em YAML ou JSON (mesma estrutura acima: `kind`, `version`, `bases`, `sheets`). Exporta-se tudo ou uma seleção; nunca contém segredos.
- Importação: valida o arquivo inteiro primeiro (versão, campos, referências). Para cada nome que já existe, o usuário escolhe **substituir**, **renomear** ou **pular**. Depois das escolhas, aplica tudo de uma vez ou nada. Lê arquivos de até 1 MiB (o que começa com `{` é JSON, o resto é YAML; campo desconhecido é recusado), e renomear uma base leva junto as fichas do arquivo que a citam.
- `version` maior que a suportada: recusa dizendo a versão esperada.

### Perfis de conta

Para usar várias contas do mesmo provedor, cada máquina declara **perfis de conta**: `name` (ex.: `claude-trabalho`), `provider` e a pasta de configuração isolada da conta. O motor aplica a pasta pela variável que cada provedor usa (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `HOME` separado), detalhada em [Claude Code](../provedores/claude-code.md), [Codex](../provedores/codex.md) e [Antigravity](../provedores/antigravity.md).
- **Onde ficam e quem usa:** as pastas dos perfis ficam em `contas` dos dados da máquina (`/var/lib/mestre/<inst>/contas/` ou `%ProgramData%\Mestre\<inst>\contas\`, [persistência](../arquitetura/persistencia.md#arquivos)) e são usadas pela conta dos agentes, `mestre-agente`, nos dois sistemas. Os logins pessoais do usuário nos CLIs (ex.: `~/.claude`, `%USERPROFILE%\.codex`) não são usados.
- O mesmo nome de perfil pode existir em várias máquinas (cada máquina faz login na mesma conta). A ficha aponta para o nome; uma máquina sem esse perfil não atende.
- **Configuração inicial do perfil**, guiada pelo painel, uma vez por máquina (é aqui, e não na sessão zero nem por mesa, que se confia nos hooks do Codex): (0) instalação do CLI no `HOME` do perfil, com o instalador oficial do provedor, se ainda não estiver lá ([CLIs dos provedores](../plataforma/instalacao-e-atualizacao.md#clis-dos-provedores)); (1) login na conta do provedor, feito **como `mestre-agente`** num terminal de login aberto no painel (no Windows, com o perfil dessa conta carregado, [Windows](../arquitetura/windows.md#conta-dos-agentes-mestre-agente)); logins por link ou código (ex.: `codex login --device-auth`) o Senhor da Guilda conclui no próprio navegador; (2) no Codex, aprovar uma vez os hooks do Mestre em `/hooks` ([codex](../provedores/codex.md)); (3) o motor roda a [conformidade](../provedores/conformidade.md) do provedor nesse perfil. Quem pode configurar: [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades). Passo a passo para quem instala: [primeiro acesso](../operacao/primeiro-acesso.md) e [entrar na Guilda](../operacao/entrar-na-guilda.md). O [checklist](../plataforma/checklist.md) confere se continua logado e, no Codex, se a confiança dos hooks existe.
- O Codex sempre usa um perfil do Mestre: ficha de Codex sem `account` usa o perfil padrão criado pelo Mestre na máquina, nunca o `~/.codex` pessoal.
- Perfis são cadastrados no painel, por máquina ([administração](../painel/administracao.md)).

### Base especial: Operador do PC

Agente que age na área de trabalho do usuário, com a conta dele, por tempo curto e com aprovação do Senhor da Guilda: a única exceção a "nenhum processo do Mestre usa a conta do usuário" ([ADR 0021](../decisoes/0021-operador-do-pc.md)). Só em máquina Windows com o Operador habilitado na instalação, e é a **única** base que atende ao requisito "precisa de interface" ([ficha da máquina](../guilda/ficha-da-maquina.md)); tarefa, processo da sessão, pipe e aviso na área de trabalho estão em [Windows](../arquitetura/windows.md#operador-do-pc).

- **Base `operador`** (desde o M7): criada pelo Mestre e não apagável: `command: claude.exe`, `args: []`, `platforms: [{os: windows}]`, `requires: {operador: true}`, `ready: hook`. O terminal é criado pelo processo da sessão do usuário, com a conta dele, e não como `mestre-agente`. Só o Senhor da Guilda edita os argumentos; nenhuma outra base vira Operador, e `--chrome` é recusado em toda base ao salvar: o navegador do usuário vem de um servidor de controle do [catálogo MCP](../classes/mcp-por-classe.md).
- **Ficha padrão "Operador":** base `operador`, `agent: claude-code`, `lifecycle: ephemeral`, `max_copies: 1`, **sem classe pronta**: o Mestre não vem com classes ([classes](../classes/classes.md)); as [sugeridas](../classes/classes-sugeridas.md) vêm desligadas e nenhuma tem `operator: true`. O Senhor da Guilda cria pelo painel uma classe com `operator: true` (há um [exemplo](../classes/classes.md#exemplo-classe-para-o-operador-do-pc)) e a escolhe como classe da ficha; até lá, a ficha aparece como incompleta no painel e pedidos de Operador falham com `operator_class_missing`. A base só aceita fichas cuja classe tenha `operator: true`, e só personagens dela usam os servidores de controle do catálogo ([regras fixas](../classes/permissoes.md#regras-fixas-de-segurança)).
- **Perfil e pasta:** a conta do usuário não alcança `%ProgramData%\Mestre`, então o Operador não usa os perfis de `contas\`: usa `CLAUDE_CONFIG_DIR` em `%LOCALAPPDATA%\Mestre\Operador\claude\`, logado uma vez pelo Senhor da Guilda num terminal de login aberto pelo painel com a conta do usuário ([administração](../painel/administracao.md#operador-do-pc)), e trabalha em `%LOCALAPPDATA%\Mestre\Operador\sessoes\<id>\`, fora de `{mesa}`, onde o motor grava a camada do Mestre e os hooks como numa mesa. `MESTRE_ENDPOINT` aponta para o pipe do Operador.
- **Pedido:** um Mestre da mesa, ou o Grão-Mestre numa mesa, chama `mestre recruit` com a ficha, a tarefa e a duração ([CLI](../cli/coordenacao.md#mestre-recruit)). O motor confere classe, teto e se alguma máquina tem o Operador **habilitado** (campo da [ficha da máquina](../guilda/ficha-da-maquina.md#campos) separado de **pronto**: habilitado diz que a máquina tem o Operador instalado e ligado pelo Senhor da Guilda; pronto, que o processo da sessão está ativo e a tela desbloqueada agora) e abre `ativacao_operador` ([política](../aprovacoes/politica.md#tipos-de-ação), [fluxo](../aprovacoes/fluxo.md)). Duração: padrão 30 min; máximo 2 h (padrão), configurável pelo Senhor da Guilda.
- **Ativação:** aprovado o pedido, o motor espera até 10 min (padrão) uma máquina com o Operador habilitado anunciá-lo **pronto**; senão, falha com "tela bloqueada" ou "usuário não logado". Abre o terminal, entrega a tarefa como primeira mensagem e começa a contar o prazo. Enquanto ativo: mensagem fixada no Telegram ([bot](../telegram/bot.md#mensagens-de-saída)), aviso no painel ([canvas](../painel/canvas.md#operador-do-pc)) e faixa "Mestre operando — até HH:MM" com **Parar** na área de trabalho, todos com a hora do fim.
- **Fim:** no fim do prazo, o motor interrompe o agente, espera 60 s e encerra o processo e a árvore dele (Job Object). Encerra **na hora**: **Encerrar agora** (Telegram ou painel), `/operador encerrar`, **Parar** na área de trabalho, `/travar`, tela bloqueada (motivo "tela bloqueada") e queda do processo da sessão. Entregue o `done`, encerra como efêmero. Os avisos passam a "encerrado: <motivo>". O prazo e o encerramento por tela bloqueada são aplicados pelo motor da própria máquina do Operador, sem depender da Cidadela: valem mesmo com ela fora.
- **Auditoria reforçada:** todo `pre_tool` e `post_tool` do Operador, e toda chamada MCP que o [gateway](../classes/mcp-por-classe.md#o-gateway-do-mestre) (M7) julga, fica na auditoria completo (entrada e resultado, depois do mascaramento), com a sessão; o processo da sessão captura a tela a cada 30 s (padrão) e a cada ação do MCP de controle do computador, e as capturas vão cifradas para a Cidadela, vinculadas à missão e à mesa, visíveis só ao Senhor da Guilda ([persistência](../arquitetura/persistencia.md#operador-do-pc)).
- **Login em sites:** o Operador é o recurso para os logins que o motor não conclui (`login_requires_human`, [segundo fator](../plataforma/cofre.md#segundo-fator-e-captcha)): entra pelo Chrome do usuário, já logado.

### No código

`internal/orchestration/presets` (bases, fichas, exportar e importar, escolha da máquina) e `internal/orchestration/accounts` (perfis e configuração inicial); `bases` e `sheets` ficam no banco da Cidadela e `account_profiles` no da máquina. Ao salvar, a base passa pela mesma conferência que o adaptador roda a cada início (`providers.CheckBase`), com o provedor deduzido do comando (`claude`, `codex`, `agy`), e a ficha por ela com o `agent` dela.
- **No painel:** cada base e ficha é editada como YAML, no formato da exportação (`internal/panel/routes_presets.go`); o arquivo importado passa por `PlanImport` e `ApplyImport`, e o Senhor da Guilda escolhe substituir, renomear ou pular por conflito.
- **Ficha × classe:** `restrictions` que dá o que a classe proíbe (um `allow` coberto por um `deny` dela, um interruptor que ela desligou) é recusada; um `allow` que a classe só não dá é guardado com aviso e ignorado. A classe é achada pelo nome; não achada, a ficha é guardada com aviso (cada campanha resolve a sua), menos na base `operador`, que exige uma com `operator: true`.
- **Perfis:** o primeiro perfil de um provedor é o padrão; `<provedor>-padrao` é o que o Mestre cria quando a máquina não tem nenhum (sempre no Codex). Remover um perfil guarda a pasta, com o login. Só exportar "tudo" deixa de fora a base `operador` e as fichas dela (numa seleção explícita a base pode ir; a ficha dela sozinha não a puxa).

## Regras e erros

- `env` não pode definir variáveis `MESTRE_*` nem conter segredos; valores que parecem segredo são recusados ao salvar ([segurança](../arquitetura/seguranca.md)); o mesmo vale para argumento de linha de comando da base, inclusive os de cada plataforma.
- **A cada início**, não só ao salvar, o adaptador recusa o `env` da base que define (sem diferença de maiúsculas) `MESTRE_*` e, numa base de agente, o que roda código antes dos hooks ou muda o git que o motor fixa (`LD_PRELOAD`, `LD_AUDIT`, `LD_LIBRARY_PATH`, `DYLD_*`, `NODE_OPTIONS`, `BASH_ENV`, `ENV`, `GIT_CONFIG*`, `GIT_EXEC_PATH`) e o que desliga ou desvia hooks, permissões, configuração ou sandbox do provedor: no Claude Code, `CLAUDE_CONFIG_DIR`, `CLAUDE_CODE_SIMPLE`, `CLAUDE_CODE_SAFE_MODE`, `CLAUDE_CODE_RESTRICTED`, o shell e o que embrulha os comandos (`SHELL`, `CLAUDE_CODE_SHELL`, `CLAUDE_CODE_SHELL_PREFIX`, `CLAUDE_ENV_FILE`, `CLAUDE_CODE_PROCESS_WRAPPER`, `CLAUDE_CODE_GIT_BASH_PATH`, `CLAUDE_CODE_USE_POWERSHELL_TOOL`), plugins, pastas e mensagens de fora (`CLAUDE_CODE_PLUGIN_*`, `CLAUDE_CODE_PROJECT_DIR_NAME`, `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD`, `CLAUDE_CODE_MESSAGING_*`, `CLAUDE_CODE_TMPDIR`), o modo automático e os limites de hooks (`CLAUDE_CODE_AUTO_MODE_SERVER`, `CLAUDE_CODE_ENABLE_AUTO_MODE`, `CLAUDE_AFK_TIMEOUT_MS`, `CLAUDE_CODE_ENABLE_CFC`, `CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS`, `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP`) e todo `CLAUDE_CODE_DISABLE_*` fora dos de tela e de modelo (`TERMINAL_TITLE`, `MOUSE`, `MOUSE_CLICKS`, `ALTERNATE_SCREEN`, `VIRTUAL_SCROLL`, `FEEDBACK_SURVEY`, `NONESSENTIAL_TRAFFIC`, `EXPERIMENTAL_BETAS`, `1M_CONTEXT`, `ADAPTIVE_THINKING`, `THINKING`, `FAST_MODE`, `LEGACY_MODEL_REMAP`, `STRUCTURED_OUTPUTS`, `NONSTREAMING_FALLBACK`); no Codex, todo `CODEX_*`; no Antigravity, `HOME` e `USERPROFILE`. O perfil de conta e as `MESTRE_*` vêm do motor. O resto passa: a lista não tenta prever as variáveis de rede, idioma ou modelo que uma base usa.
- Apagar base usada por ficha, ou ficha usada por personagem vivo, é recusado com a lista de quem usa. Missões e grimórios que citam uma ficha apagada falham ao rodar/restaurar, dizendo qual ficha falta.
- Base sem nenhuma plataforma, ou `cwd` de agente fora de `{mesa}` (exceto a base `operador`): recusada ao salvar.
- Base ou ficha, fora a base `operador` e as fichas dela, que exige interface gráfica: recusada ao salvar ([requisitos](../guilda/ficha-da-maquina.md#requisitos)).
- Requisito que nenhuma máquina da Guilda atende: o personagem não roda em outra máquina; a criação falha e o Senhor da Guilda é avisado ([escolha da máquina](personagens.md#escolha-da-máquina)).

## Critérios de aceite

- **CA-BF-01** · *rápida* — **Dado** a base `claude-code` do exemplo, **quando** resolvida para uma máquina Windows, **então** o comando é `claude.exe`; para Linux, `claude`.
- **CA-BF-02** · *rápida* — **Dado** uma ficha com `restrictions` que libera um comando proibido pela classe, **quando** é salva, **então** é recusada.
- **CA-BF-03** · *rápida* — **Dado** o arquivo do exemplo exportado em YAML, **quando** é importado como JSON equivalente num banco vazio, **então** bases e fichas ficam idênticas; **quando** importado de novo com "pular", **então** nada muda.
- **CA-BF-04** · *rápida* — **Dado** uma base com `env: {MESTRE_AGENT_ID: x}`, **quando** é salva, **então** é recusada.
- **CA-BF-05** · *rápida* — **Dado** uma ficha com `account: agy-pessoal` e só uma máquina com esse perfil, **quando** se escolhe a máquina, **então** só essa máquina atende.
- **CA-BF-06** · *rápida* — **Dado** uma base `claude-dev` ou `operador` com `args: [--chrome]` e uma ficha da base `operador` com a classe Dev (sem `operator: true`), **quando** são salvas, **então** as duas são recusadas, com o motivo.
- **CA-BF-07** · *rápida* — **Dado** uma sessão do Operador aprovada por 30 min, a máquina falsa anunciando `operador` e o relógio falso, **quando** passam 30 min, **então** o motor manda a interrupção, espera 60 s, fecha o terminal com `kill` e edita os avisos para "encerrado: prazo"; **quando** a máquina encerra a sessão por tela bloqueada no meio dela, sem a Cidadela, **então** informa `operator.session` `locked` e os avisos passam a "encerrado: tela bloqueada".
- **CA-BF-08** · *pesada* — **Dado** o uso Linux + Windows com o Operador habilitado, o usuário logado na VM Windows com o Chrome e a extensão, **quando** o Senhor da Guilda pede pelo Telegram (dublê) uma tarefa no PC, aprova a ativação com PIN e acompanha, **então** o Operador abre uma página no Chrome do usuário dentro do prazo, a faixa aparece na área de trabalho, as capturas aparecem no painel só para o Senhor da Guilda, a sessão termina sozinha no prazo, e uma segunda ativação termina na hora com `/travar` (MVP-14).
- **CA-BF-09** · *rápida* — **Dado** uma instalação nova com o Operador habilitado, **quando** listo as classes, **então** não existe nenhuma classe pronta, a ficha "Operador" aparece como incompleta e um pedido de Operador sai com `operator_class_missing`.
- **CA-BF-10** · *rápida* — **Dado** uma base `pintor` (não `operador`) com o requisito de interface gráfica, **quando** é salva, **então** é recusada com o motivo; **dado** o mesmo requisito na base `operador`, **então** é aceita.

## Depende de

- [Persistência](../arquitetura/persistencia.md)
- [Adaptador de provedores](../provedores/adaptador.md)
- [Permissões](../classes/permissoes.md)
- [Classes](../classes/classes.md)
- [Ficha da máquina](../guilda/ficha-da-maquina.md)
