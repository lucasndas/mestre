# Projetos de treino

> **Assunto:** projetos pequenos e conhecidos que os testes copiam para fazer o papel do repositório do usuário: modelos-base, variações, tarefas com teste oculto, `testkit.NewProject` e o repositório sandbox do GitHub · **Componente:** Projeto · **Atualizado:** 2026-10-12

## Objetivo

Dar a todo teste que precisa de "um repositório de verdade" (sessão zero, mesas, missões, conformidade, ponta a ponta) o mesmo ponto de partida: um projeto pequeno, conhecido e reproduzível, copiado para uma pasta temporária, com tarefas de resultado conferível por máquina. Nenhum teste usa projeto real do usuário. Este documento é o **dono** do assunto; [testes](testes.md) e [dublês](dubles.md) só apontam para cá.

## Comportamento

### Modelo-base `go-basico`

- Fica em `testdata/projetos/go-basico/`, na raiz do repositório (o `go` ignora pastas `testdata`). É um módulo Go próprio (`example.com/gobasico`, com o seu `go.mod`), sem dependências.
- Conteúdo: o pacote `stats` com três funções (`Sum`, `Max`, `CountAbove`) e os testes delas, que rodam em menos de 1 s, e um `README.md`.
- **Bug conhecido:** `Max` devolve 0 quando todos os números são negativos (começa do zero, não do primeiro número). Os testes visíveis não cobrem o caso; a tarefa T1 cobre.
- Sem repositório git aninhado: o modelo não tem `.git`; o repositório da cópia é criado pelo `NewProject` (abaixo).
- No M6 entra um segundo modelo-base mínimo, `node-basico` (TypeScript/Node), para testar a análise de projeto da [sessão zero](../sessao-zero/workspace.md) fora do Go.

### Variações

Cada variação é uma **sobreposição**: uma pasta `testdata/projetos/variacoes/<nome>/` cujos arquivos são copiados por cima do modelo, na ordem pedida (o último vence).

| Variação | O que acrescenta | Marco |
|---|---|---|
| `com-agents-md` | `AGENTS.md` do projeto; **padrão**, aplicada a toda cópia | M0 |
| `com-sensiveis` | `.env` e `secrets/` | M3 |
| `sem-agents-md` | cópia sem o `AGENTS.md` padrão | M4 |
| `com-claude-md` | `CLAUDE.md` junto do `AGENTS.md` | M4 |
| `com-hooks-claude` | `.claude/settings.json` com hooks do próprio projeto | M4 |
| `com-workflows` | `.github/workflows/ci.yml` | M5 |

Cada variação nasce com um critério de aceite no documento do marco que a usa.

### Tarefas (`tarefas.yaml`)

- Ficam **fora da cópia**, em `testdata/projetos/conferencia/<modelo>/tarefas.yaml`; o agente nunca vê o arquivo nem os testes ocultos.
- Campos de cada item de `tasks`: `id` (`T<n>`), `prompt` (o texto do pedido), `may_change` (arquivos que podem mudar), `verify` com `hidden` (pasta de testes ocultos, relativa a `conferencia/<modelo>/`) e `command` (comando de verificação, em lista), e `heavy` com `max_minutes` e `max_turns` (teto na suíte pesada; só nas tarefas que rodam com provedores reais). Campo desconhecido é erro.
- **Teste oculto:** guardado em `conferencia/<modelo>/ocultos/<id>/`, com os mesmos caminhos do projeto; é copiado para a cópia **só na conferência**, depois que o agente terminou, e então roda o `command`.

| Tarefa | Pedido | Verificação | Em uso a partir de |
|---|---|---|---|
| T1 | corrigir o bug de `Max` | teste oculto que hoje falha | M0 (dados); M3 na primeira conformidade real |
| T2 | adicionar `Media` com teste | teste oculto de `Media` | M4 |
| T3 | atualizar o README (só documentação) | teste oculto que lê o README; a classe QA não edita código | M3 |
| T4 | mudar o workflow de CI | regra `git.workflows` ([workflows](../github/identidades-e-apps.md#workflows)) | M5, com `com-workflows` |

O `tarefas.yaml` do M0 traz T1 a T3 (dados e testes ocultos); T4 entra no M5, junto da variação de que precisa.

### `testkit.NewProject`

`testkit.NewProject(t, "go-basico", variações...)` devolve a cópia (`Dir`, `Head` e, na rápida, `Git`):

- copia o modelo, a variação padrão e as pedidas para uma pasta nova em `t.TempDir()`; cada teste tem a sua, e o modelo nunca muda;
- **rápida:** sem git real nem processo; a cópia entra no [git falso](dubles.md#outros-dublês) como um commit na branch `main`;
- **completa e pesada** (build tag `full`): `git init` e um commit com autor `Mestre Treino <treino@example.invalid>` e data `2026-01-01T00:00:00Z` fixos, isolado da configuração do usuário, então o hash do commit é sempre o mesmo;
- `ApplyHiddenTests(t, id)` copia os testes ocultos da tarefa para a cópia; `LoadTasks(t, modelo)` lê e valida o `tarefas.yaml`;
- a partir do M5, nas suítes com o GitHub falso, a cópia é registrada no dublê, que serve o git por HTTP (`git http-backend`) como remoto.

### Quem faz as tarefas

- Nas suítes rápida e completa, o [agente falso](dubles.md#agente-falso-fakeagent) cumpre o roteiro de cada tarefa.
- Provedores reais só na suíte pesada, e só com T1 e T3, dentro do teto do `heavy` ([testes](testes.md#suítes)).

### Sandbox real no GitHub

- Repositório `lucasndas/mestre-sandbox`, criado pelo usuário no M5. Serve **só** às conferências manuais dos itens "Verificar na implementação" do GitHub (aprovação de App, e-mail do bot, rulesets). Os itens que antes diziam "à mão, na sessão zero" passam a ser conferidos nele.
- Nenhuma suíte o usa (o GitHub das suítes é sempre o dublê) e ele nunca é um projeto real do usuário.

### Marcos

| Marco | O que entra |
|---|---|
| M0 | `NewProject`, modelo `go-basico`, variação padrão, T1 e o formato do `tarefas.yaml` |
| M3 | `com-sensiveis`, T3 e a primeira [conformidade](../provedores/conformidade.md) real com T1 |
| M4 | `sem-agents-md`, `com-claude-md`, `com-hooks-claude` e T2 |
| M5 | `com-workflows`, T4, git por HTTP no GitHub falso e o sandbox |
| M6 | segundo modelo-base mínimo, `node-basico` (TypeScript/Node) |
| M7 | suíte pesada com T1 e T3 nos provedores reais |

O marco deste documento é o M0 ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)); as partes seguintes fecham no marco da tabela.

## Regras e erros

- Modelo ou variação desconhecidos fazem o teste falhar, listando as variações conhecidas; uma pasta `.git` dentro de um modelo ou variação também.
- `tarefas.yaml` inválido (campo desconhecido, `id` repetido ou fora de `T<n>`, sem `prompt`, `may_change` ou `verify.command`, teto `heavy` zerado, pasta de teste oculto inexistente) faz o teste falhar.
- A rápida nunca roda `git` nem `go` de verdade numa cópia; isso é da completa ([testes](testes.md#regras-e-erros)).
- Um teste oculto só passa com a tarefa feita: a completa confere que ele falha na cópia sem mudança e passa com uma mudança de referência escrita no próprio teste.
- O teste do git falso só compila sem a tag `full` (`//go:build !full`), porque na completa a cópia é um repositório real.

## Critérios de aceite

- **CA-TREINO-01** · *rápida* — **Dado** o modelo `go-basico`, **quando** dois testes chamam `NewProject` sem variação e com `com-agents-md`, **então** cada um recebe uma pasta temporária própria com o modelo e o `AGENTS.md`, sem `tarefas.yaml` nem testes ocultos, e mudar uma cópia não muda a outra nem o modelo.
- **CA-TREINO-02** · *rápida* — **Dado** um modelo e uma variação que sobrescreve o `README.md`, **quando** a cópia é feita, **então** vale o arquivo da variação; **dado** uma variação desconhecida, um nome de modelo inválido ou um `.git` no modelo, **então** a cópia falha com a mensagem do caso.
- **CA-TREINO-03** · *rápida* — **Dado** o `tarefas.yaml` do `go-basico`, **quando** é lido, **então** T1 tem todos os campos e `ApplyHiddenTests` põe o teste oculto na cópia; **dado** um arquivo com campo desconhecido, `id` repetido ou sem comando, **então** a leitura falha.
- **CA-TREINO-04** · *rápida* — **Dado** a suíte rápida, **quando** `NewProject` copia o modelo, **então** não há `.git` na pasta, o git falso tem um commit em `main` com os mesmos arquivos do disco, e duas cópias têm o mesmo `Head`.
- **CA-TREINO-05** · *completa* — **Dado** a suíte completa, **quando** `NewProject` copia o modelo, **então** a pasta é um repositório git limpo com um commit de autor e data fixos, e duas cópias têm o mesmo hash.
- **CA-TREINO-06** · *completa* — **Dado** uma cópia sem mudança, **quando** os testes do projeto rodam, **então** passam em menos de 1 s; **dado** os testes ocultos de T1 a T3, **então** cada um falha na cópia sem mudança e passa depois da mudança de referência.

## Depende de

- [testes.md](testes.md) (suítes e build tags)
- [dubles.md](dubles.md) (git falso)
