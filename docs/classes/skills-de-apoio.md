# Skills de apoio

> **Assunto:** skills de engenharia que uma classe traz com liga/desliga (como TDD, diagnóstico de bug, revisão de código): onde ficam no pacote, como chegam a cada provedor, as quatro camadas que fazem o desligado valer e a licença · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Deixar uma classe trazer skills completas de terceiros, que ensinam **como** fazer (a disciplina do TDD, o roteiro de diagnóstico, os dois eixos de uma revisão), e não só o resumo que cabe no `CLASS.md`. No Codex e no Antigravity, as skills de uma classe hoje viram seção compacta da camada do Mestre, com 6.000 a 8.000 caracteres para tudo ([sessão zero do personagem](../sessao-zero/personagem.md#skills-por-personagem)); uma skill de engenharia sozinha passa disso. A decisão está no [ADR 0011](../decisoes/0011-agents-skills-pasta-oficial.md), revisto em 2026-10-18.

## Comportamento

### Os três tipos de skill

| Tipo | Exemplo | Onde chega |
|---|---|---|
| comunicação | `mestre-comunicacao` | pasta compartilhada do worktree, para todos ([sessão zero da mesa](../sessao-zero/mesa.md)) |
| nível e classe | `mestre-grao-mestre`, as `skills` da classe | só ao personagem ([sessão zero do personagem](../sessao-zero/personagem.md#skills-por-personagem)) |
| **apoio** | `tdd`, `diagnosing-bugs`, `code-review` | este documento |

Skill de apoio não dá permissão nenhuma: o motor confere o nível e as regras em toda chamada, leia o agente o que ler.

### No pacote da classe

```yaml
support_skills:                   # frontmatter do CLASS.md
  - {name: tdd, enabled: true}
  - {name: ponytail, enabled: false}
```

- Cada uma fica em `support/<nome>/` no pacote, com o `SKILL.md`, a pasta inteira da skill e o arquivo de **licença** dela (`LICENSE`; e `NOTICE` quando a licença o exige, como a Apache-2.0). Conta nos limites do pacote ([classes](classes.md#estrutura-do-pacote)).
- O pacote é copiado e versionado com a classe; nada vai para `~/.claude/skills` nem `~/.agents/skills` ([ADR 0011](../decisoes/0011-agents-skills-pasta-oficial.md), opção 4). Atualizar a skill é editar a classe, com as aprovações do escopo.
- **Ligar e desligar** é editar `enabled`, pelo painel ou pela CLI, com as mesmas aprovações. Desligada, a skill continua no pacote e a classe funciona pelo próprio `CLASS.md`: desligar escolhe entre profundidade e contexto, nunca entre funcionar e não funcionar.

### Como chega a cada provedor (camada 1: entrega)

- **Claude Code:** só as ligadas vão para a pasta do personagem, `.mestre/agents/<id>/.claude/skills/<nome>/`, lida por `--add-dir`, como as skills da classe. A cópia gerenciada em `.claude/skills/` do worktree continua só com a de comunicação.
- **Codex e Antigravity:** o motor materializa em `.agents/skills/<nome>/` do worktree da mesa, em cada máquina onde roda um personagem dela, as skills de apoio **ligadas em alguma classe com personagem vivo na mesa**, antes de iniciar o personagem (fora do git, como a de comunicação). Os dois carregam sob demanda: só o nome e a descrição entram no catálogo, e o corpo é lido quando a skill é usada. Quando nenhum personagem vivo da mesa a tem ligada, a pasta sai na materialização seguinte.

### O desligado vale (camadas 2 a 4)

2. **Regra gerada pelo motor:** nas regras efetivas de cada personagem, `files.deny: [".agents/skills/<nome>/**"]` para toda skill de apoio da mesa que a classe dele não tem ligada. Quem aplica é o [despachante](../provedores/despachante-de-hooks.md), nas ferramentas de leitura e nos caminhos literais de comando ([formato](formato-das-permissoes.md)), igual nos três provedores. Ninguém escreve essa regra à mão.
3. **Reforço nativo**, onde o provedor permite ([reforço nativo](reforco-nativo.md)): no Claude Code, `Read(<glob>)` em `deny` no `--settings` do personagem; no Codex, o motor desliga a skill na configuração de início do personagem (abaixo, "Verificar"); no Antigravity, sem reforço: vale o despachante, que lá falha fechado.
4. **Aviso:** a camada do Mestre do personagem tem uma linha com as skills de apoio ligadas para ele. É informação, nunca a barreira.

## Regras e erros

- Skill de apoio sem arquivo de licença, ou com nome que começa com `mestre-`: o `class check` recusa a classe.
- Nome igual ao de uma skill versionada no `.agents/skills/` do próprio repositório: a de apoio não é materializada naquela mesa, e o Mestre da mesa e o `class check` avisam.
- Duas classes da mesa com a mesma skill de apoio em versões diferentes: vale a da classe do primeiro personagem vivo, e o motor avisa o Mestre da mesa.

## Critérios de aceite

- **CA-APOIO-01** · *rápida* — **Dado** uma mesa com um Dev (com `tdd` ligada) e um Revisor (com `tdd` desligada), ambos Codex, **quando** os arquivos são montados, **então** o `.agents/skills/` da mesa tem `mestre-comunicacao` e `tdd`, as regras efetivas do Revisor têm `files.deny` para `.agents/skills/tdd/**`, e um `cat .agents/skills/tdd/SKILL.md` dele é negado pelo despachante e registrado.
- **CA-APOIO-02** · *rápida* — **Dado** um Dev e um Revisor Claude Code, com `tdd` ligada só para o Dev, **quando** os arquivos são montados, **então** `tdd` existe só em `.mestre/agents/<id do Dev>/.claude/skills/`, o `.claude/skills/` do worktree tem só `mestre-comunicacao`, e o `--settings` do Revisor tem `Read` em `deny` para `.agents/skills/tdd/**`.
- **CA-APOIO-03** · *rápida* — **Dado** uma classe com `support/tdd/` sem `LICENSE`, ou uma skill de apoio chamada `mestre-x`, **quando** o `class check` roda, **então** aponta o erro e a classe não é salva.
- **CA-APOIO-04** · *rápida* — **Dado** o último personagem vivo com `tdd` ligada dispensado da mesa, **quando** o motor materializa de novo, **então** `.agents/skills/tdd/` sai do worktree; **dado** um repositório que versiona `.agents/skills/tdd/`, **então** a de apoio não é materializada e o Mestre da mesa recebe o aviso.
- **CA-APOIO-05** · *rápida* — **Dado** o Dev com `tdd` ligada e o Revisor com ela desligada, **quando** as camadas do Mestre são montadas, **então** a do Dev tem a linha das skills de apoio com `tdd` e a do Revisor não a cita.

## Depende de

- [Classes](classes.md)
- [Formato das permissões](formato-das-permissoes.md)
- [Reforço nativo](reforco-nativo.md)
- [Despachante de hooks](../provedores/despachante-de-hooks.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md)
- [Sessão zero do personagem](../sessao-zero/personagem.md)

## Verificar na implementação

- **Verificar na implementação:** como o Codex lê o corpo de uma skill de `.agents/skills/` (por um comando de shell que o despachante vê, ou por dentro do CLI) e se o motor consegue desligar uma skill só para um personagem pela configuração de início (`-c`, com a chave de skills da versão em uso). **Padrão:** a leitura passa pelo despachante e a configuração de início desliga a skill (some do catálogo). **Se falhar:** numa mesa com personagem Codex que tem a skill desligada, ela não vai à pasta compartilhada; os personagens Codex que a têm ligada recebem a versão compacta na camada do Mestre, como antes. **Onde:** [teste de classe](../provedores/teste-de-classe.md) com um caso que proíbe a leitura da skill desligada, no Codex.
- **Verificar na implementação:** se o Antigravity lê o corpo de uma skill pela ferramenta de leitura que o despachante vê. **Padrão:** sim, e a negação vale. **Se falhar:** o mesmo plano do Codex. **Onde:** o mesmo caso do teste de classe, no Antigravity.
- **Verificar na implementação:** quanto o catálogo do Codex cresce com as skills de apoio da mesa (ele guarda até cerca de 2% do contexto ou 8.000 caracteres para o catálogo, [Codex](../provedores/codex.md)). **Padrão:** cabe com até 10 skills de apoio por mesa. **Se falhar:** o motor limita a quantidade por mesa e avisa o Mestre da mesa. **Onde:** teste de classe com uma mesa de dez skills.
