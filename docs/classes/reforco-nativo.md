# Reforço nativo das permissões

> **Assunto:** tradução das regras efetivas de cada personagem para os controles nativos de Claude Code, Codex e Antigravity (defesa em profundidade) · **Componente:** Node · **Atualizado:** 2026-10-12

## Objetivo

Fazer cada provedor segurar o essencial das [permissões](permissoes.md) mesmo se o `mestre hook` falhar. O adaptador traduz as [regras efetivas](permissoes.md#regras-efetivas) do personagem para o que cada provedor aceita, na hora de iniciá-lo. A decisão completa continua no [despachante](../provedores/despachante-de-hooks.md); o reforço nativo só é uma segunda barreira.

## Comportamento

### Tradução por provedor

| Política | Claude Code (`--settings` do personagem) | Codex (flags de início) | Antigravity |
|---|---|---|---|
| `files.write` | `allow`: `Edit(<glob>)`, `Write(<glob>)`; sem escrita: `--disallowedTools Edit Write NotebookEdit` | sem escrita: `-s read-only`; senão `-s workspace-write` (sem regra por caminho) | sem regra por caminho |
| `files.deny` | `deny`: `Read(<glob>)`, `Edit(<glob>)` | — | — |
| `commands.allow` / `deny` | `allow` / `deny`: `Bash(<padrão>)` | — (contido pelo sandbox) | — |
| regras fixas | `deny`: `Bash(git push *)`, `Bash(git remote *)` | `.git` fora do alcance de escrita do sandbox | `permissions.deny` no `settings.json` do perfil (só regras fixas, pois o arquivo é compartilhado) |
| `network.internet: false` | `--disallowedTools WebFetch WebSearch` | `sandbox_workspace_write.network_access=false`, sem `--search` | `--sandbox` |
| `network.domains` | `allow`: `WebFetch(domain:<d>)` | só liga/desliga | — |
| falha do despachante | `onFailure: block` | sandbox | falha fechada nativa |

- `ask` nunca vira regra nativa: o despachante cuida.
- `tools` (ferramentas `mcp` e `other`) não tem regra nativa; fica só no despachante.
- No Claude, os globs de caminho saem sempre absolutos (`Edit(//<worktree>/tests/**)`), para não depender de a regra relativa ser lida a partir da pasta do arquivo `--settings`.
- Flags de cada provedor: [Claude Code](../provedores/claude-code.md), [Codex](../provedores/codex.md), [Antigravity](../provedores/antigravity.md).

### Quando as regras mudam

Quando um decreto ou uma mudança de classe libera algo bloqueado no nível nativo, o motor regenera o arquivo do personagem. Como o Mestre trata o `--settings` do Claude e as flags do Codex como lidos só no início, o personagem é reiniciado com retomada quando estiver livre ([retomada](../provedores/adaptador.md#retomada-de-sessão)). Restrições novas também esperam o reinício no nível nativo; até lá, o despachante já as aplica.

## Regras e erros

- Regra que o provedor não consegue expressar fica só no despachante, sem aviso ao usuário (é o caso normal, não um erro).
- Arquivo nativo que não pode ser gravado (ex.: `settings.json` do perfil Antigravity inválido): o personagem não inicia e o checklist explica.

## Critérios de aceite

- **CA-NATIVO-01** · *rápida* — **Dado** uma classe QA que só escreve em `tests/**`, sem internet, **quando** o adaptador traduz, **então** o Claude recebe `Edit(//<worktree>/tests/**)` em `allow` e `Bash(git push *)` em `deny`, o Codex `-s workspace-write` sem rede, e o Antigravity `--sandbox`.
- **CA-NATIVO-02** · *rápida* — **Dado** regras efetivas com `commands.ask` e `tools.allow`, **quando** o adaptador traduz para os três provedores, **então** nenhuma regra nativa sai desses dois blocos.
- **CA-NATIVO-03** · *rápida* — **Dado** um personagem Claude ocupado e um decreto que libera `Bash(make deploy *)`, **quando** as regras efetivas chegam, **então** o arquivo `--settings` é regenerado e o reinício com retomada fica agendado para quando ele estiver livre.

## Depende de

- [Permissões](permissoes.md) · [Adaptador](../provedores/adaptador.md)

## Verificar na implementação

- **Verificar na implementação:** a regra `Edit(//<caminho absoluto>/<glob>)` do Claude casa os arquivos do worktree. **Padrão:** casa. **Se falhar:** o motor gera a forma relativa que a conformidade aceitar; enquanto isso, a regra de caminho fica só no despachante. **Onde:** etapa 4 do [teste de conformidade](../provedores/conformidade.md), com o despachante em modo degradado.
- **Verificar na implementação:** a sintaxe `action(target)` com curinga no `permissions.deny` do Antigravity (ex.: `command(git push*)`). **Padrão:** essa forma. **Se falhar:** as regras fixas desse provedor ficam só no hook, que as aplica até em modo degradado ([despachante](../provedores/despachante-de-hooks.md#modo-degradado)). **Onde:** etapa 5 do [teste de conformidade](../provedores/conformidade.md), pedindo `git push` com o hook respondendo `allow`.
