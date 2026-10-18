# Classes

> **Assunto:** pacote da classe (instruções, permissões, hooks, skills, App), escopos e quem cria · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

A classe diz a um personagem **o que fazer e o que pode fazer** (ex.: Dev, QA, Revisor; os nomes são só exemplos). As regras são escritas uma vez e valem igual em Claude Code, Codex e Antigravity. O Mestre não vem com classes prontas para personagens (a única embutida é a dos orquestradores, [`orquestrador`](classe-orquestrador.md)): elas são criadas pelos usuários, pelo Grão-Mestre ou pelo Mestre de uma mesa, conforme a necessidade. A partir do M8 existem também as [classes sugeridas](classes-sugeridas.md), que vêm desligadas e um Administrador ativa quando quiser (sugerida não é padrão, e a regra acima não muda).

## Comportamento

### Escopos

| Escopo | Pasta (fonte na Cidadela) | Quem cria | Aprovação |
|---|---|---|---|
| global | `<dados da Cidadela>/classes-globais/<classe>/` ([persistência](../arquitetura/persistencia.md#arquivos)) | Dono ou Administrador, pelo painel | — |
| workspace | `.mestre/classes/<classe>/` no clone principal | Dono ou Administrador; o Grão-Mestre pode propor | de um Administrador ou acima; o Grão-Mestre nunca aprova a própria proposta |
| mesa | `.mestre/mesas/<mesa>/classes/<classe>/` | Mestre da mesa (ou usuários) | nenhuma, desde que dentro do [teto do workspace](teto-do-workspace.md) |

- As classes são arquivos, para poderem ser versionadas; o banco da Cidadela guarda o índice ([persistência](../arquitetura/persistencia.md)). As Torres recebem cópias materializadas pelo motor ([mesa](../sessao-zero/mesa.md)).
- O nome (slug `[a-z0-9-]`) é único entre os escopos visíveis numa mesa (global + workspace + mesa). Variações têm nome próprio (ex.: `qa-api`).
- Criar, editar e excluir têm a sintaxe em [CLI de classes e rituais](../cli/coordenacao-classes-e-rituais.md) e também podem ser feitos no [painel](../painel/administracao.md). Quem não tem o direito no escopo recebe `forbidden`: o Grão-Mestre só cria classe do workspace (como proposta) e o Mestre da mesa só a da própria mesa.

### Onde a classe é alterada

- A **única** forma de criar, alterar ou excluir uma classe é `mestre class create|edit|delete` ([CLI de classes e rituais](../cli/coordenacao-classes-e-rituais.md)) ou o [painel](../painel/administracao.md). Quem grava os arquivos é o motor, depois de validar o pacote, conferir o teto e obter as aprovações do escopo; a regra fixa que proíbe o agente de escrever em `.mestre/**` não atinge esse caminho ([permissões](permissoes.md#regras-fixas-de-segurança)).
- A cada versão aprovada, o motor registra no banco o hash e o pacote inteiro (`classes`). **O motor só usa uma classe cujos arquivos batem com o hash registrado**, e usa a versão guardada, diga a pasta o que disser.
- Uma versão nova do pacote [sugerido](classes-sugeridas.md#atualizar) também vira proposta, com autor "Mestre". Qualquer mudança feita por fora (edição manual, `git pull`, PR mesclado que altere `.mestre/classes/` ou `.mestre/mesas/<mesa>/classes/`), válida ou não, vira uma **proposta** (`class_proposals`): passa pelas mesmas validações e aprovações do escopo. Até ser aprovada (ou se for inválida), vale a última versão aprovada, e o painel e o Grão-Mestre avisam os usuários, uma vez por mudança. O motor percebe quando um personagem começa um turno e no `class check`. Aprova-se o hash que o aprovador viu; a proposta inválida não se aprova. É a mesma regra do [teto](teto-do-workspace.md).

### Estrutura do pacote

```
<classe>/
  CLASS.md            frontmatter + instruções da função
  permissions.yaml    o que a classe pode fazer (ver permissoes.md)
  hooks/hooks.yaml    hooks extras, por evento do Mestre
  hooks/…             scripts usados pelos hooks (só classes globais e do workspace)
  skills/<nome>/      skills próprias da classe (opcional); support/<nome>/: skills de apoio (M8)
```
O pacote tem até 64 arquivos (inclusive os casos de `cases/`, desde o M7, [teste de classe](../provedores/teste-de-classe.md)), 512 KiB e 256 KiB por arquivo, porque viaja inteiro na replicação; links e arquivos que não são comuns são recusados.

Frontmatter do `CLASS.md`:

```yaml
name: qa
description: Testa a mesa e relata falhas; não altera código de produção
app: herdar:qa-base      # novo | herdar:<classe> | <slug-do-app>
skills: [rodar-suite, mestre-relatorio]
provider: codex          # opcional: preferência para escolher a ficha
model: ""                # opcional
requires: {gpu: true}    # opcional: requisitos sobre a máquina
done_requires: {proof: all, verify: true}   # opcional, desde o M5: o que o done exige (prova-e-devolucao.md)
```
`name` é o da pasta; `description` e `app` são obrigatórios. `requires` soma aos requisitos da base e da ficha; os aceitos e como a máquina é escolhida estão em [ficha da máquina](../guilda/ficha-da-maquina.md#requisitos), e um requisito desconhecido ou `gui: true` recusa a classe ao salvar.

O corpo do `CLASS.md` traz as instruções: o que fazer, como entregar, critérios de pronto e quando sinalizar `mestre done`.

### Exemplo: classe para o Operador do PC

Só um exemplo, para o Dono criar pelo painel se quiser usar o [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc); o Mestre não a cria sozinho.
```yaml
# permissions.yaml (escopo global, criada pelo Dono)
operator: true                     # só valida com o teto que tenha operator: true
tools:
  allow: ["mcp__navegador-do-usuario__*", "mcp__<mcp-de-controle>__*"]   # ligados no mcp: do CLASS.md
  default: ask
files: { write: [] }               # o Operador age pela tela, não pelos arquivos da mesa
git: { commit: false, request_push: false, workflows: never }
```
O `CLASS.md` descreve a tarefa típica, o que nunca fazer (ex.: compras, mudar senhas) e como entregar o `done`.

### Vínculo com um App do GitHub

Toda classe nasce vinculada a um App: `novo` (o Grão-Mestre prepara o App e o Dono o cria e instala pelo link; até ficar pronto, a classe usa o App "Mestre" com o nome dela na mensagem e aparece "em criação"), `herdar:<classe>` (usa o App da classe de origem; só se `git.workflows` não for maior que o dela, ver [workflows](../github/identidades-e-apps.md#workflows)) ou o slug de um App existente. O vínculo fica no frontmatter e no banco; trocar de App só vale para ações novas. Detalhes em [identidades e Apps](../github/identidades-e-apps.md) e [criação de Apps](../github/criacao-de-apps.md).

### Hooks extras da classe

Organizados pelos eventos do Mestre do [mapa de eventos](../provedores/adaptador.md#mapa-de-eventos) (`session_start`, `turn_start`, `pre_tool`, `post_tool`, `turn_end`, `turn_failure`, `pre_compact`, `session_end`), nunca pelos eventos de cada provedor.

```yaml
post_tool:
  - name: formatar
    when: {kind: [edit, write], paths: ["src/**"]}
    run: "npm run format"
    mode: sync            # sync | async
    timeout: 20           # segundos (obrigatório)
    output: log           # log | scroll | agent
turn_end:
  - name: testes
    run: "npm test"
    run_windows: "npm.cmd test"
    mode: async
    timeout: 900
    output: scroll        # anexa ao pergaminho da missão
pre_tool:
  - name: checar-comando
    when: {kind: [shell]}
    run: "./hooks/checar.sh"
    mode: sync
    timeout: 5
    blocking: true        # existe para bloquear
```

- **Entrada:** o evento no formato interno ([adaptador](../provedores/adaptador.md#formato-interno-normalizado-de-evento)) no stdin; ambiente do personagem mais `MESTRE_HOOK_EVENT`; pasta atual = worktree; mesmo usuário e permissões do personagem.
- **Quem roda:** `sync` (o padrão) roda dentro da chamada do `mestre hook`, com tempo limite cortado para 2 s abaixo do tempo limite do provedor, e os `sync` de uma chamada dividem esse mesmo tempo ([despachante](../provedores/despachante-de-hooks.md#uma-chamada)); `async` roda no motor da máquina depois da resposta, sem segurar o agente, no máximo um por personagem de cada vez (os outros esperam em fila).
- **`pre_tool`:** é sempre `sync` e só roda se o [pipeline de permissões](permissoes.md) já liberou. Exit 0 = sem objeção; exit 2 = negar (motivo no stderr). Um hook só pode restringir, nunca liberar o que o pipeline negou. `blocking` só existe aqui; `when` só em `pre_tool` e `post_tool`.
- **Falha ou tempo esgotado:** registra o erro (no stderr da chamada do `mestre hook`) e segue, a não ser com `blocking: true`, que nega.
- **Saída** (padrão `log`): `log` vai para o log do personagem; `scroll` é anexada ao pergaminho da missão ([pergaminhos](../orquestracao/pergaminhos-e-tomos.md)); `agent` é entregue pela [caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md). `scroll` e `agent` só valem em `async`.
- Em provedor sem o evento (ex.: Antigravity não tem `pre_compact`), o hook não roda; `session_end` vem do fim do processo no PTY. O `class check` mostra isso por provedor.
- O `run` de cada hook passa pelo mesmo pipeline de permissões da classe, como um comando do personagem. O que o pipeline não libera não roda: o hook `blocking` então nega e o `class check` avisa. Classes da mesa não podem ter scripts no pacote: só linhas de comando checadas; scripts ficam para classes globais e do workspace, que passam por aprovação de um usuário. Um script do pacote só roda da cópia aprovada, que o motor materializa do banco no M4 ([mesa](../sessao-zero/mesa.md#arquivos-gerados-no-worktree)) e que o agente não pode alterar; a do worktree ele pode trocar. Até lá o motor, sem interpretar o shell, recusa o `run` que **possa** alcançar `hooks/`: o que cita `hooks` de qualquer forma, sem maiúsculas e depois de tirar aspas, barras invertidas, crases e `^` (`cd hooks && ./x`, `true;hooks/x`, `(hooks/x)`, `$PWD/hooks/x`, `.//hooks/x`, o caminho absoluto do worktree, `h""ooks/x`), e o que tem palavra montada na execução (`$x`, `%x%`, `~`, glob, chaves, `$(…)`) que seja um caminho ou esteja numa linha que muda de pasta (`cd`, `pushd`, `-C`). Esse hook conta como recusado, e o `blocking` nega; o custo é recusar também linha legítima que cite `hooks` (como `core.hooksPath`).

### Skills da classe

As [skills de apoio](skills-de-apoio.md) (`support_skills`, desde o M8) e os servidores MCP ([MCP por classe](mcp-por-classe.md), campo `mcp`, desde o M7) têm documentos próprios. `skills` lista nomes de skills do pacote (`skills/<nome>/`) ou já existentes no `.agents/skills/` do projeto. As do pacote chegam **só aos personagens da classe**, nunca à pasta compartilhada do worktree: no Claude Code, em `.mestre/agents/<id>/`, lida por `--add-dir`; no Codex e no Antigravity, como seção compacta na camada do Mestre do personagem (`MESTRE.md` ou arquivo de agente). A regra única de entrega por provedor está em [sessão zero do personagem](../sessao-zero/personagem.md); as skills da CLI `mestre`, em [skills geradas](../cli/skills-geradas.md).

### Como a classe chega ao personagem

- `CLASS.md` → camada do Mestre ([personagem](../sessao-zero/personagem.md)); `permissions.yaml` → pipeline do despachante e controles nativos ([permissões](permissoes.md)); `hooks/` → despachante único ([adaptador](../provedores/adaptador.md)).
- Mudanças em permissões e hooks valem na próxima chamada de hook (a Cidadela recombina e reenvia as regras efetivas); as do `CLASS.md`, no contexto do próximo turno e por inteiro nos personagens novos. O banco guarda o hash de cada versão e qual versão cada personagem usa (M4).

### Validação (`mestre class check`)

Confere: frontmatter e nome único (e, desde o M8, avisa de `mestre <comando>` inexistente citado no pacote, [classes sugeridas](classes-sugeridas.md#comando-inexistente-numa-skill)); desde o M7, os casos de `cases/` ([teste de classe](../provedores/teste-de-classe.md#os-casos)); esquema do `permissions.yaml`; que tudo cabe no teto; eventos, tempos limite e scripts dos hooks; skills existentes; App vinculado, se é herdado e o estado dele ([criação de Apps](../github/criacao-de-apps.md)). Mostra, por provedor, como as permissões viram controles nativos e quais hooks não rodam ali. Pacote inválido sai com `invalid_class` e a lista de problemas; "acima do teto" é só um estado (nada é apagado), e o `run` que o pipeline recusa é um aviso.

O código é `internal/classes` (`Library`, `Examine`) e `internal/hook` (`ClassHookSource`, `AsyncRunner`). A entrega aos personagens (M4), o estado dos Apps e a aprovação pelo fluxo (M5) entram por interfaces e chegam nos marcos seguintes ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)).

## Regras e erros

- Criar sem `--app`: recusado, com a explicação das três opções.
- Arquivos da classe diferentes do hash registrado: a classe continua na última versão aprovada e nasce a proposta descrita acima.
- Nome já usado num escopo visível: recusado.
- Mestre da mesa criando ou editando algo fora do teto: recusado, mostrando o que passa do teto.
- Excluir só a pedido explícito de um usuário autorizado ([convenções da CLI](../cli/convencoes.md)); classe em uso por personagem ativo não é excluída até ele trocar de classe ou ser encerrado.
- Restaurar um grimório com classes que não existem no destino: [grimórios](../orquestracao/grimorios.md).

## Critérios de aceite

- **CA-CLASSES-01** · *rápida* — **Dado** um pedido de criação sem `--app`, **quando** o motor valida, **então** recusa e lista `novo`, `herdar:<classe>` e `<nome-do-app>`.
- **CA-CLASSES-02** · *rápida* — **Dado** uma classe de workspace `qa`, **quando** o Mestre de uma mesa cria uma classe da mesa `qa`, **então** a criação é recusada por nome repetido.
- **CA-CLASSES-03** · *rápida* — **Dado** um hook `pre_tool` com `blocking: true`, **quando** ele sai com 2, falha ou estoura o tempo, **então** a decisão é negar (com o stderr como motivo no exit 2); com `blocking: false`, a falha só é registrada.
- **CA-CLASSES-04** · *completa* — **Dado** uma classe com hook `turn_end` assíncrono e `output: scroll`, **quando** o agente falso termina o turno, **então** o próximo turno começa sem esperar o hook e a saída aparece no pergaminho da missão.
- **CA-CLASSES-05** · *rápida* — **Dado** uma classe da mesa com um script em `hooks/`, **quando** `class check` roda, **então** aponta o erro e a classe não é salva.
- **CA-CLASSES-06** · *completa* — **Dado** uma classe de workspace proposta pelo Grão-Mestre, **quando** o próprio Grão-Mestre tenta aprovar, **então** a aprovação é recusada e a classe só passa a valer com o "Aprovar" de um Administrador.
- **CA-CLASSES-07** · *rápida* — **Dado** uma classe com `app: novo` e hook `pre_compact`, **quando** `class check` roda, **então** mostra o App "em criação" e que o hook não roda no Antigravity.
- **CA-CLASSES-08** · *rápida* — **Dado** uma classe aprovada e uma edição manual **válida** no `permissions.yaml` dela (hash diferente do registrado), **quando** um personagem dessa classe inicia um turno, **então** valem as regras da última versão aprovada e existe uma proposta pendente com a mudança.

## Depende de

- [Permissões](permissoes.md) · [Teto do workspace](teto-do-workspace.md)
- [Adaptador](../provedores/adaptador.md) (eventos do Mestre para os hooks extras)
- [Persistência](../arquitetura/persistencia.md)
