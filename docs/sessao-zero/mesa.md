# Sessão zero da mesa

> **Assunto:** os arquivos que o motor gera em cada worktree de mesa (e no clone principal), a exclusão local do git, a mescla gerenciada, a confiança de pasta por provedor e a regra de não atrapalhar quem clona · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Garantir que todo worktree onde roda um personagem tenha a fiação que os provedores exigem (skills, hooks, confiança), sem nada disso entrar no git do projeto, sem estragar arquivos da pessoa e sem atrapalhar quem clonar o repositório sem o Mestre. Vale para o motor de cada máquina da Guilda, inclusive a Cidadela.

## Comportamento

### Quando roda
- ao abrir uma mesa (worktree na Cidadela) e quando uma Torre recebe o primeiro personagem da mesa (worktree na Torre; [mesas](../orquestracao/mesas.md));
- no clone principal, na criação do workspace ([workspace](workspace.md));
- antes de iniciar cada personagem (verifica e corrige, sem reescrever o que já está certo);
- ao regenerar depois de uma versão nova dos modelos ([personagem](personagem.md)).

A geração é idempotente: rodar de novo sem mudança nos modelos não altera nenhum byte (o Antigravity e o Claude Code recarregam a configuração a cada mudança de arquivo).

### Arquivos gerados no worktree
| Arquivo | Para | Conteúdo | Dono do formato |
|---|---|---|---|
| `.agents/skills/mestre-comunicacao/SKILL.md` | Codex, Antigravity | a única skill compartilhada (as de nível e de classe são por personagem: [personagem](personagem.md#skills-por-personagem)), ao lado das [skills de apoio](../classes/skills-de-apoio.md) ligadas na mesa (desde o M8) | [skills-geradas](../cli/skills-geradas.md) |
| `.claude/skills/mestre-comunicacao/SKILL.md` | Claude Code | cópia gerenciada da anterior (cópia, não link: links no Windows exigem permissão especial) | idem |
| `.claude/settings.local.json` | Claude Code | só as chaves `hooks` e `statusLine`, mescladas | [claude-code](../provedores/claude-code.md) |
| `.agents/hooks.json` | Antigravity | um hook nomeado `mestre` | [antigravity](../provedores/antigravity.md) |
| `.mestre/run/classes/<classe>/` | todos | cópia das classes usadas ali (os hooks extras rodam na máquina) | [classes](../classes/classes.md) |
| `.mestre/anexos/…` | todos | cópias dos anexos repassados à mesa | [anexos-e-transcricao](../telegram/anexos-e-transcricao.md) |

Os hooks do Codex não ficam no worktree, e sim no perfil de conta ([codex](../provedores/codex.md)). Arquivos por personagem (`.mestre/agents/<id>/` e `.agents/agents/mestre-<id>.md`) são gerados ao criar o personagem: [personagem](personagem.md). Classes da mesa e pergaminhos têm a Cidadela como fonte; o motor de cada Torre atualiza as cópias quando a Cidadela avisa que mudaram. O conteúdo dos hooks (eventos, comandos e respostas) é do adaptador ([adaptador](../provedores/adaptador.md), [0012](../decisoes/0012-despachante-unico-de-hooks.md)).

### `.git/info/exclude`
O motor mantém um bloco próprio, de nome `managed`, no `info/exclude` do repositório de onde o worktree sai (o arquivo vale para todos os worktrees daquele repositório). Os marcadores são os do código que mexe no arquivo (`# >>> mestre:<nome> >>>` e `# <<< mestre:<nome> <<<`), e um teste confere que o bloco abaixo é o que o motor grava:
```
# >>> mestre:managed >>>
/.mestre/agents/
/.mestre/anexos/
/.mestre/run/
/.agents/skills/mestre-*/
/.claude/skills/mestre-*/
/.agents/agents/mestre-*.md
/.claude/settings.local.json
/.agents/hooks.json
/.mestre/
# <<< mestre:managed <<<
```
- `/.mestre/` só aparece quando o workspace escolheu "só local" ([workspace](workspace.md)); desde o M8, o bloco ganha uma linha `/.agents/skills/<nome>/` por [skill de apoio](../classes/skills-de-apoio.md) materializada.
- `.agents/hooks.json` só entra se foi o Mestre que criou o arquivo (um arquivo da pessoa, ainda não versionado, continua aparecendo para ela no `git status`).
- Linhas fora do bloco nunca são tocadas; o `.gitignore` do projeto nunca é alterado.

### Mescla gerenciada
Para arquivos que podem já existir com conteúdo da pessoa:
1. Ler o arquivo. Se não for válido (JSON quebrado, por exemplo), **não tocar**: registrar o erro, alertar no checklist e não iniciar personagens daquele provedor ali até resolver.
2. Retirar só a parte do Mestre, reconhecida pelo marcador do tipo de arquivo:
   - texto (`info/exclude`): o bloco entre `# >>> mestre:managed >>>` e `# <<< mestre:managed <<<`;
   - hooks do Claude Code: os handlers cujo comando é a chamada do Mestre (abaixo) e a `statusLine` do Mestre; handlers da pessoa ficam onde estão, e os do Mestre entram **no fim** de cada lista;
   - hooks do Antigravity: a chave de topo `mestre`;
   - skills: pastas `mestre-*` e (desde o M8) as de [apoio](../classes/skills-de-apoio.md), com o marcador de gerado ([skills-geradas](../cli/skills-geradas.md)); uma pasta com esse nome e sem o marcador não é tocada nem substituída, a skill não é materializada ali e o Mestre da mesa e o checklist avisam.
3. Acrescentar a parte atual do Mestre e gravar de forma atômica (arquivo temporário + renomear), só se o conteúdo mudou.

**Arquivo já versionado pelo projeto** (ex.: um `.agents/hooks.json` do repositório): a mescla é feita e o motor marca o arquivo com `git update-index --skip-worktree`, para que a parte do Mestre não apareça no `git status` nem entre em commits. Antes de sincronizar a branch ou atualizar da principal (inclusive por rebase), o motor desliga a marca e tira a parte do Mestre do arquivo (devolvendo-o byte a byte ao que o projeto tem no HEAD, se só a parte do Mestre o diferia; mudanças da pessoa ficam), sincroniza e reaplica, mesmo se a sincronização falhar; assim o `skip-worktree` nunca está ligado durante um rebase. Quem tira a parte do Mestre é o adaptador de cada provedor (`HookStripper`). Se o projeto mudou o mesmo arquivo, vale a versão do projeto e a parte do Mestre é mesclada de novo por cima.

### Confiança de pasta por provedor
Sem confiança, hooks e instruções do projeto não carregam. O motor grava a confiança antes de iniciar o personagem, em cada perfil de conta da máquina que vai rodar ali ([bases-e-fichas](../orquestracao/bases-e-fichas.md)):

| Provedor | Onde | O quê |
|---|---|---|
| Claude Code | `.claude.json` do `CLAUDE_CONFIG_DIR` do perfil | `projects["<worktree>"].hasTrustDialogAccepted = true` (nunca a chave do espelho) |
| Codex | `config.toml` do `CODEX_HOME` do perfil | `[projects."<worktree>"] trust_level = "trusted"` (nunca a raiz do espelho); hooks: confiar uma vez (ver [codex](../provedores/codex.md)) |
| Antigravity | `settings.json` em `.gemini/antigravity-cli/` do `HOME` do perfil | caminho do worktree em `trustedWorkspaces` |

- A chave exata (worktree ou raiz do checkout principal) e os detalhes de cada provedor estão nos documentos de provedor.
- Os provedores reescrevem esses arquivos: o motor grava de forma atômica, relê logo antes de iniciar o personagem e grava de novo se a entrada sumiu.
- A confiança dos hooks do Codex não é da mesa nem da sessão zero: é dada uma vez, na [configuração inicial do perfil de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta); o checklist (ALL-11, [checklist](../plataforma/checklist.md)) detecta quando falta.
- Ao remover o worktree, o motor retira as entradas de confiança que criou para ele, em todo perfil em que as gravou (também o de uma conta removida depois) e mesmo que o worktree não abra mais.

### Não atrapalhar quem clona
- Os arquivos gerados ficam fora do git (acima). Se mesmo assim algum arquivo de hook do Mestre acabar no repositório, ele precisa funcionar numa máquina sem o Mestre, porque no Antigravity um hook que falha bloqueia o agente.
- Por isso o comando gravado em todo arquivo de hook **do worktree** (Claude Code e Antigravity) não chama o `mestre` direto: é uma linha que só chama o `mestre hook` se `MESTRE_AGENT_ID` existe e o binário é encontrado (no Claude Code, pelo `command -v`; no Antigravity, no caminho estável, que não tem `MESTRE_CLI` de reserva); senão imprime a resposta neutra do evento e sai com 0. Exemplo (Antigravity, PreToolUse, Linux):
  ```sh
  if [ -n "$MESTRE_AGENT_ID" ] && [ -e '/caminho/estavel/mestre' ]; then exec '/caminho/estavel/mestre' hook --provider antigravity --event PreToolUse; else printf '%s\n' '{"decision":"allow"}'; fi
  ```
- Resposta neutra: `{"decision":"allow"}` no PreToolUse do Antigravity; `{}` nos outros eventos dele; saída vazia no Claude Code e no Codex.
- A mesma linha é a forma definitiva. O `hooks.json` do Codex fica no perfil de conta, não no repositório, e não leva a guarda: o conteúdo é fixo, só o caminho do binário e os argumentos, para o Dono confiar uma vez. Dentro do `mestre hook` a regra se repete: sem `MESTRE_AGENT_ID`, libera e sai ([adaptador](../provedores/adaptador.md)).

### Como o motor faz
`sessionzero.Table` (`internal/sessionzero/table.go`) junta as peças. `Prepare` gera os arquivos do worktree (`Worktree.Ensure`), copia as classes e os anexos (pelo `safefile`; remove da cópia da classe o que saiu do pacote, nunca um anexo) e grava a confiança em cada perfil. `CheckStart` roda antes de cada personagem: é o `Prepare` mais a recusa (`BlockedError`) quando um arquivo do provedor do personagem não foi gerado; os outros provedores seguem, e o aviso aos usuários sai uma vez por conjunto de problemas. `Release` tira a confiança quando o worktree é removido; `Sync` envolve a atualização da branch. As fontes das classes, dos anexos, da conformidade e da aprovação da cópia são interfaces pequenas (o Telegram, o fluxo de aprovação e o checklist são dos marcos 5 e 7).

## Regras e erros

- Nada é apagado nem sobrescrito fora das partes marcadas como do Mestre.
- Falha ao gerar um arquivo de um provedor bloqueia só personagens daquele provedor naquele worktree, com o motivo no painel; os outros seguem.
- O início do Grão-Mestre, que roda no clone principal, **não** passa por `CheckStart`: a sessão zero do workspace verifica o clone na criação e avisa os usuários dos arquivos de provedor que ficaram sem gerar, mas não recusa o provedor bloqueado. Fechar essa lacuna é trabalho do M5, junto da hospedagem do Grão-Mestre no `mestre run`.
- A cópia em `.claude/skills/` só deixa de ser gerada por decisão de usuário: quando o teste de conformidade detecta que o Claude Code lê `.agents/skills/`, o checklist (ALL-13) **oferece** desligar a cópia; só depois que um Dono ou Administrador aprova, ela deixa de ser gerada e a existente é removida. Sem a aprovação, nada muda.

## Critérios de aceite

- **CA-SZ-MESA-01** · *rápida* — **Dado** um worktree já gerado, **quando** a geração roda de novo, **então** nenhum arquivo muda (mesmo hash e mesma data de modificação).
- **CA-SZ-MESA-02** · *rápida* — **Dado** um `.claude/settings.local.json` com `permissions` e um hook da pessoa, **quando** a mescla roda duas vezes, **então** o conteúdo da pessoa fica igual e os handlers do Mestre aparecem uma vez, depois dos dela.
- **CA-SZ-MESA-03** · *rápida* — **Dado** um `.agents/hooks.json` com JSON inválido, **quando** a geração roda, **então** o arquivo não muda, um alerta é registrado e nenhum personagem Antigravity inicia ali.
- **CA-SZ-MESA-04** · *rápida* — **Dado** um `info/exclude` com linhas da pessoa, **quando** o bloco é atualizado, **então** as linhas dela ficam iguais e existe um só bloco do Mestre.
- **CA-SZ-MESA-05** · *completa* — **Dado** uma mesa com worktree na Cidadela e numa Torre, **quando** os dois são gerados, **então** todos os arquivos da tabela existem nos dois e `git status --porcelain` fica vazio nos dois (MVP-3).
- **CA-SZ-MESA-06** · *completa* — **Dado** um `.agents/hooks.json` versionado e mesclado, **quando** um agente faz commit de tudo, **então** o commit não contém o hook `mestre`; e, num clone novo sem o `mestre` no PATH, o agente Antigravity falso tem o PreToolUse liberado (MVP-3).
- **CA-SZ-MESA-07** · *completa* — **Dado** um worktree com confiança gravada nos três provedores, **quando** a mesa é encerrada e o worktree removido, **então** as entradas de confiança daquele caminho deixam de existir.
- **CA-SZ-MESA-08** · *completa* — **Dado** o comando de hook gerado para o Claude Code ou o Antigravity, **quando** roda sem `MESTRE_AGENT_ID`, **então** imprime a resposta neutra do evento, sai com 0 e não executa o `mestre`. A linha roda num shell de verdade, e a suíte rápida não inicia processos ([testes](../qualidade/testes.md#regras-e-erros)); a rápida confere só a forma da linha (o teste de `MESTRE_AGENT_ID` vem antes da chamada).
- **CA-SZ-MESA-09** · *rápida* — **Dado** a conformidade marcando que o Claude Code lê `.agents/skills/`, **quando** a geração roda sem a aprovação de um Dono ou Administrador, **então** a cópia em `.claude/skills/` continua sendo gerada; **quando** a aprovação existe, **então** a cópia é removida e não volta.

## Depende de

- [Sessão zero do workspace](workspace.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)
- [Adaptador](../provedores/adaptador.md) · [Claude Code](../provedores/claude-code.md) · [Codex](../provedores/codex.md) · [Antigravity](../provedores/antigravity.md)
- [Classes](../classes/classes.md)

## Verificar na implementação

- **Verificar na implementação:** a forma Windows da linha de hook em cada provedor (`cmd /c` no Antigravity, `command_windows` no Codex, shell usado pelo Claude Code). **Padrão:** a linha do Codex é só o executável e os argumentos (`mestre hook --provider … --event …`), e as do Claude Code e do Antigravity levam a guarda, sem outra sintaxe própria de shell além do teste de existência do binário, que muda por sistema. **Se falhar:** a linha passa a usar o caminho absoluto estável do `mestre.exe` e, no Codex, o campo `command_windows`. **Onde:** etapas 4 a 6 do [teste de conformidade](../provedores/conformidade.md) na máquina Windows. **Decisão pelo código dos CLIs (2026-10-14), antes da rodada:** o Codex roda o hook pelo shell que detecta, o PowerShell no Windows, e a linha é `& "<caminho>" …; exit $LASTEXITCODE`; o Claude Code roda com bash (o do Git for Windows), e a linha é a mesma do Linux, com o caminho em barras normais; o Antigravity segue com `cmd /c`. Os itens de cada um estão no [Codex](../provedores/codex.md#verificar-na-implementação) e no [Claude Code](../provedores/claude-code.md#verificar-na-implementação).
