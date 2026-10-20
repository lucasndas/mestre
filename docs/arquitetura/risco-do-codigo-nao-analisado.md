# Risco aceito: código que o hook não lê

> **Assunto:** o que um código que roda como `mestre-agente` e que o despachante não analisa (teste, script do projeto, `python -c`, hook do repositório) alcança, o que não alcança, o que reduz o risco, o que o removeria e o que fazer depois de um incidente · **Componente:** Todos · **Atualizado:** 2026-10-19

Decisão de fundo no [ADR 0025](../decisoes/0025-padroes-seguem-os-provedores.md); contas e isolamento em [segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes). Registrado na auditoria do M0 ao M4 (2026-10-18), com o alcance inteiro.

## Por que existe

O despachante (`mestre hook`) julga a **linha de comando** que o agente pede; o que um programa faz depois de rodar não é visto. Todos os personagens de uma máquina rodam com a mesma conta, `mestre-agente`, e `contas/` e `workspaces/` ficam no grupo dela, porque os CLIs rodam com essa conta ([persistência](persistencia.md#arquivos)). A negação dessas pastas nas regras fixas ([permissões](../classes/permissoes.md#regras-fixas-de-segurança)) é, portanto, do hook, e não do sistema.

## O que o código alcança

- **Credenciais de todos os perfis de conta.** No Linux, as de `contas/` (`.credentials.json`, `auth.json`, o token do Antigravity e as chaves SSH); no Windows, o perfil Windows de `mestre-agente` e o Gerenciador de Credenciais dessa conta, comuns a todos os perfis ([Windows](windows.md#conta-dos-agentes-mestre-agente)). É o roubo das assinaturas dos três provedores.
- **Os binários dos CLIs**, que se instalam e se atualizam em `contas/` (Linux) ou no perfil Windows de `mestre-agente`: a troca do binário de qualquer provedor persiste entre mesas e classes. Nada confere o binário: a versão que a conformidade lê é a que ele informa ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)).
- **A configuração do Mestre em pastas do agente:** o `claude-settings.json` e o `gitconfig` do personagem, o `.claude/settings.local.json` e o `.agents/hooks.json` do worktree, o `hooks.json` e o `config.toml` do perfil Codex, o `settings.json` e o arquivo de agente do Antigravity, o `.claude.json` do perfil. Os hooks do Claude Code recarregam sem reiniciar ([Claude Code](../provedores/claude-code.md)), então tirar os handlers do Mestre **desliga o despachante, as regras fixas e a auditoria na sessão em curso**, também nos worktrees de outras mesas e classes que estão rodando. Um `gitconfig` com `core.fsmonitor` ou um alias faz um `git status`, que o hook dá como `baixo`, rodar código (o git do motor usa `GIT_CONFIG_GLOBAL=/dev/null` e não é afetado). O `MESTRE.md` de outro personagem, inclusive o do Mestre da mesa, que decide pedidos, pode ganhar instruções: o risco sobe da classe mais fechada até a cadeia de aprovação. O motor regrava esses arquivos ao iniciar o personagem, ao trocar a classe e quando as regras mudam, o que limita a persistência, mas não o efeito na sessão em curso.
- **Segredos que saem do cofre.** A chave de API do provedor entra no ambiente do processo do CLI; no Linux, só o `mestre-pty` é protegido contra leitura do `/proc/<pid>/environ` (os filhos não herdam), e o Yama só restringe o *attach*, então o código lê a chave de API dos outros personagens (no Windows, a DACL por terminal impede). O `sessao-sites.json` fica na `MESTRE_TMP`, legível por todos os personagens: a lista de classes de uma credencial de site não vale contra código ([cofre](../plataforma/cofre.md#login-em-sites)).
- **Os outros worktrees e as refs e objetos do espelho**, a `MESTRE_TMP` alheia e o `/tmp`.

## O que não alcança

O banco, o arquivo do cofre e a configuração com dono `mestre` (0700/0600, ACL da conta do serviço); os tokens dos Apps do GitHub, porque o git do motor roda como `mestre` com a autenticação em memória; o `cli.sock` além da identidade do próprio personagem (cgroup ou Job Object).

Por provedor: o Codex roda com `-s workspace-write`, e o código do shell dele só escreve no worktree, na pasta git e em `objects`, `refs` e `logs` do espelho; ele lê tudo, e no Linux tem rede. O alcance de escrita inteiro vale para o Claude Code (nos dois sistemas) e para o Antigravity no Linux.

## O que reduz, e seus limites

- **Classe sem internet** reduz a saída, mas **não a corta**: no Claude só entram `--disallowedTools WebFetch WebSearch`; no Codex (nos dois sistemas) e no Antigravity do Linux a rede do sandbox fica ligada e a barra é só do despachante, que vê a linha (`curl`) e não o código; só o Antigravity no Windows tem corte nativo (`--sandbox`, ainda não verificado) ([reforço nativo](../classes/reforco-nativo.md)). Uma classe só fica contida se todas as da máquina estiverem sem rede, porque o que foi lido pode sair por outra classe, via `/tmp` ou a `MESTRE_TMP` alheia.
- **A regravação da configuração** limita a persistência de uma mudança nela, não as credenciais, os binários nem o efeito na sessão em curso.
- **O `agy` rodado sem `MESTRE_AGENT_ID`** (à mão, num clone sem o Mestre) recebe o `allow` do PreToolUse, no Windows como já no Linux: a linha do hook imprime a resposta neutra sem chamar o `mestre` ([Antigravity](../provedores/antigravity.md#hooks)). Um personagem do Mestre sempre tem o id; quem apaga a variável do próprio ambiente já roda fora do despachante.
- **A auditoria** registra só a linha que entrou (`npm test`), não o que o código fez; com os hooks desligados, as ações seguintes nem entram.

## O que removeria o risco (não feito)

Reduziria, e não removeria, uma conta do sistema por perfil de conta e a configuração do Mestre fora de pastas que o agente grava: o perfil continua legível e gravável pelo CLI, que precisa renovar a credencial e se autoatualizar. Para remover de fato falta ainda: uma conta ou grupo **por personagem**, o shell do agente num sandbox que negue a leitura de `contas/`, firewall por conta (hoje adiado para o M7, `packaging/systemd`) e binários fora do alcance de escrita, o que conflita com a autoatualização.

## Depois de um incidente

Revogar as credenciais e refazer o login de **todos** os perfis (uma credencial OAuth vazada vale até ser revogada), trocar as chaves de API, reinstalar os CLIs e regerar os arquivos do Mestre ([regenerar](../sessao-zero/personagem.md#modelos-versionados-e-regeneração)).
