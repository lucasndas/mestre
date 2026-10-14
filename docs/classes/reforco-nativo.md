# Reforço nativo das permissões

> **Assunto:** tradução das regras efetivas de cada personagem para os controles nativos de Claude Code, Codex e Antigravity (defesa em profundidade) · **Componente:** Motor · **Atualizado:** 2026-10-14

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
| `network.internet: false` | `--disallowedTools WebFetch WebSearch` | `sandbox_workspace_write.network_access=false`, sem `--search`, com o socket do canal local liberado (abaixo) | `--sandbox` |
| `network.domains` | `allow`: `WebFetch(domain:<d>)` | só liga/desliga | — |
| ferramentas disponíveis | — | — | campo `tools` do arquivo de agente `mestre-<id>.md`: só as ferramentas nativas que as regras efetivas não negam por inteiro (sem escrita: sem as de escrita; sem internet: sem as de web) |
| falha do despachante | `onFailure: block` | sandbox | falha fechada nativa |

- `ask` nunca vira regra nativa: o despachante cuida.
- `tools` (ferramentas `mcp` e `other`) não tem regra nativa; fica só no despachante.
- No Claude, os globs de caminho saem sempre absolutos (`Edit(//<worktree>/tests/**)`), para não depender de a regra relativa ser lida a partir da pasta do arquivo `--settings`; no Windows, na forma POSIX que o Claude compara (`C:\x` vira `//c/x`).
- Também no Claude: o arquivo `--settings` leva `disableBypassPermissionsMode: "disable"`; um padrão de `deny` terminado em ` *` sai também sem o curinga (`Bash(curl *)` e `Bash(curl)`), como o despachante casa; os caminhos que as regras fixas fecham à escrita viram `Edit(//<worktree>/**/.git/**)` e afins; com lista de domínios, `--disallowedTools WebSearch` (ferramenta web sem URL é negada).
- No Antigravity, o `settings.json` do perfil fica também com `toolPermission: request-review`. O campo `tools` lista as ferramentas nativas do mapa do adaptador, tirando as de escrita sem escrita, as de web sem internet, `search_web` com lista de domínios e as de tipo `subagent` e `other` que as regras negam sempre (casam `tools.deny`, ou não casam nada e o `default` é `deny`).
- **Decretos e exceções** não viram regra nativa (mudam a qualquer momento), mas um bloqueio nativo que um decreto ou uma exceção pontual da lista libera sai da tradução: caminhos e escrita, comandos, rede. Senão a barreira nativa pararia o que o despachante libera. As regras fixas nunca saem.
- **Codex e o canal local:** no Codex 0.162.1, `network_access=false` também bloqueia o `connect()` ao socket do [canal local](../arquitetura/canal-local.md) (`EPERM`), e a CLI `mestre` deixa de alcançar o motor. A base do Codex precisa liberar esse socket sem liberar a rede; como, e o que acontece se não houver jeito, está no item de "Verificar" do [Codex](../provedores/codex.md#verificar-na-implementação).
- Flags de cada provedor: [Claude Code](../provedores/claude-code.md), [Codex](../provedores/codex.md), [Antigravity](../provedores/antigravity.md).

### Quando as regras mudam

Quando um decreto ou uma mudança de classe libera algo bloqueado no nível nativo, o motor regenera o arquivo do personagem. Como o Mestre trata o `--settings` do Claude e as flags do Codex como lidos só no início, o personagem é reiniciado com retomada quando estiver livre ([retomada](../provedores/adaptador.md#retomada-de-sessão)). Restrições novas também esperam o reinício no nível nativo; até lá, o despachante já as aplica.

- Qualquer mudança no que é nativo, liberando ou restringindo (um arquivo regravado, ou flags diferentes das do início), agenda o reinício com retomada; personagem ocupado reinicia no fim do turno, nunca no meio. Regras efetivas novas que não mudam nada nativo não reiniciam.
- Os arquivos são regravados inteiros (arquivo temporário renomeado), mantendo o que não é do Mestre: as outras chaves do `--settings` (statusLine) e do `settings.json` do perfil, e o resto do arquivo de agente. O código é `internal/classes` (`NativePolicy`, `NativeSync`) e a tradução de cada provedor em `internal/providers` (`Native`).
- Os caminhos "Se falhar" de [Verificar](#verificar-na-implementação) existem já como chaves que a conformidade liga (`NativeFallbacks`): no Claude, regras de caminho relativas ou nenhuma (só o despachante); no Antigravity, sem `permissions.deny` do Mestre.

## Regras e erros

- Regra que o provedor não consegue expressar fica só no despachante, sem aviso ao usuário (é o caso normal, não um erro).
- Arquivo nativo que não pode ser gravado (ex.: `settings.json` do perfil Antigravity inválido): o personagem não inicia e o checklist explica.

## Critérios de aceite

- **CA-NATIVO-01** · *rápida* — **Dado** uma classe QA que só escreve em `tests/**`, sem internet, **quando** o adaptador traduz, **então** o Claude recebe `Edit(//<worktree>/tests/**)` em `allow` e `Bash(git push *)` em `deny`, o Codex `-s workspace-write` sem rede, e o Antigravity `--sandbox`.
- **CA-NATIVO-02** · *rápida* — **Dado** regras efetivas com `commands.ask` e `tools.allow`, **quando** o adaptador traduz para os três provedores, **então** nenhuma regra nativa sai desses dois blocos.
- **CA-NATIVO-03** · *rápida* — **Dado** um personagem Claude ocupado e um decreto que libera `Bash(make deploy *)`, **quando** as regras efetivas chegam, **então** o arquivo `--settings` é regenerado e o reinício com retomada fica agendado para quando ele estiver livre.
- **CA-NATIVO-04** · *rápida* — **Dado** uma classe sem escrita e sem internet, **quando** o adaptador do Antigravity gera o arquivo de agente, **então** o campo `tools` não lista ferramentas de escrita nem de web.

## Depende de

- [Permissões](permissoes.md) · [Adaptador](../provedores/adaptador.md)

## Verificar na implementação

- **Verificar na implementação:** a regra `Edit(//<caminho absoluto>/<glob>)` do Claude casa os arquivos do worktree. **Padrão:** casa. **Se falhar:** o motor gera a forma relativa que a conformidade aceitar; enquanto isso, a regra de caminho fica só no despachante. **Onde:** etapa 4 do [teste de conformidade](../provedores/conformidade.md), com o despachante em modo degradado.
- **Verificar na implementação:** a sintaxe `action(target)` com curinga no `permissions.deny` do Antigravity (ex.: `command(git push*)`). **Padrão:** essa forma. **Se falhar:** as regras fixas desse provedor ficam só no hook, que as aplica até em modo degradado ([despachante](../provedores/despachante-de-hooks.md#modo-degradado)). **Onde:** etapa 5 do [teste de conformidade](../provedores/conformidade.md), pedindo `git push` com o hook respondendo `allow`.
- **Verificar na implementação:** o campo `tools` do arquivo de agente do Antigravity restringe as ferramentas da **sessão principal** (`mainAgent: true`), e não só de subagentes. **Padrão:** restringe. **Se falhar:** o campo continua gerado, sem efeito, e o bloqueio fica só no despachante (falha fechada) e no `--sandbox`. **Onde:** etapa 4 do [teste de conformidade](../provedores/conformidade.md), conferindo que a ferramenta de escrita nem chega a ser oferecida.
