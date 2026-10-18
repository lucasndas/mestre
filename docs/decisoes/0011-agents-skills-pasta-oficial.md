# ADR 0011: `.agents/skills/` como pasta oficial de skills

> **Assunto:** por que as skills ficam em `.agents/skills/` com cópia gerenciada para o Claude Code · **Componente:** Todos · **Atualizado:** 2026-10-18

## Status

Aceita, 2026-10-12. **Decisão revista em 2026-10-12:** as pastas compartilhadas guardam só a skill de comunicação; as de nível e de classe passam a ser entregues por personagem. **Termos atualizados em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): Core e nó viram Cidadela e Torre (ou máquina); a decisão não muda. **Decisão revista em 2026-10-18 (skills de apoio):** além da de comunicação, a pasta compartilhada `.agents/skills/` de uma mesa passa a guardar as **skills de apoio** ligadas em alguma classe recrutada nela; o acesso a cada uma continua por personagem, por uma regra que o motor gera ([skills de apoio](../classes/skills-de-apoio.md)).

## Contexto

- O Mestre instala skills (`SKILL.md`) que ensinam a CLI `mestre` a cada nível de agente, além das skills extras das classes ([skills geradas](../cli/skills-geradas.md)).
- **Onde cada provedor procura skills de projeto:**
  - **Codex:** `.agents/skills/`, nativo. Exige `name` e `description` no frontmatter.
  - **Antigravity:** `.agents/skills/`, nativo. `name` é opcional e `description` é obrigatória.
  - **Claude Code:** só `.claude/skills/`. A documentação não menciona `.agents/`. Ele aceita pastas de skill que sejam links simbólicos, e num worktree procura só até a raiz do worktree.
- **Links simbólicos no Windows** exigem permissão especial ou o modo de desenvolvedor.
- **Pastas de usuário** (`~/.agents/skills`, `~/.claude/skills`) afetariam o uso dos agentes fora do Mestre. Além disso, o Codex lê `$HOME/.agents/skills` mesmo com `CODEX_HOME` separado.

## Opções consideradas

1. **`.agents/skills/` como fonte, com cópia gerenciada em `.claude/skills/`.**
   - Prós:
     - Uma fonte só, na pasta que o mercado está adotando.
     - Funciona igual no Linux e no Windows.
     - A cópia é desligada quando o Claude Code passar a ler `.agents/skills/`.
   - Contras:
     - Arquivos duplicados no disco.
     - A cópia precisa ser mantida em sincronia.
2. **Link simbólico `.claude/skills/<nome>` → `.agents/skills/<nome>`.**
   - Prós: sem duplicação, e é suportado pela documentação do Claude Code.
   - Contras: no Windows exige permissão especial, e máquinas Windows rodam agentes.
3. **`.claude/skills/` como fonte, copiando para `.agents/skills/`.**
   - Prós: o Claude Code lê a fonte direto.
   - Contras: a fonte fica na pasta de um único provedor, ao contrário do padrão que dois dos três já usam.
4. **Skills nas pastas globais do usuário.**
   - Prós: instala uma vez.
   - Contras:
     - Vaza para o uso fora do Mestre.
     - Não diferencia o nível de cada agente.

## Decisão

- **Fonte única:** `.agents/skills/` em cada worktree.
- **Cópia gerenciada:** em `.claude/skills/`, regenerada pelo motor a partir da fonte e nunca editada à mão. É uma cópia, não um link.
- **Frontmatter:** todo `SKILL.md` gerado tem sempre `name` e `description`, o que atende ao provedor mais exigente (Codex).
- **Só a skill de comunicação nas pastas compartilhadas:** as skills de nível e de classe são entregues só ao personagem que as recebe: no Claude Code por `--add-dir` de uma pasta por personagem; no Codex e no Antigravity como seção compacta da camada do Mestre ([sessão zero do personagem](../sessao-zero/personagem.md#skills-por-personagem)).
- **Desligar a cópia:** o teste de conformidade detecta quando o Claude Code passar a ler `.agents/skills/`; o checklist (ALL-13) então **oferece** desligar a cópia, e ela só deixa de ser gerada depois da aprovação de um Dono ou Administrador ([checklist](../plataforma/checklist.md), [sessão zero da mesa](../sessao-zero/mesa.md#regras-e-erros)).
- **Skills de apoio (revisão de 2026-10-18):** um terceiro tipo, ao lado da de comunicação (compartilhada) e das de nível e de classe (por personagem). São skills de engenharia que a classe traz com liga/desliga e que não dão permissão nenhuma. No Codex e no Antigravity vão para a pasta compartilhada da mesa, onde os dois as carregam sob demanda, em vez de caberem no orçamento da camada do Mestre; no Claude Code continuam na pasta do personagem. O personagem cuja classe não tem uma delas ligada recebe `files.deny` para ela nas regras efetivas, aplicado pelo despachante, com reforço nativo onde o provedor permite. A cópia gerenciada em `.claude/skills/` continua só com a de comunicação.
- **Onde estão os detalhes:** materialização por worktree e exclusão do git em [sessão zero da mesa](../sessao-zero/mesa.md).

## Consequências

- **Positivas:**
  - Uma única fonte de skills para os três provedores.
  - Nada depende de links simbólicos no Windows.
  - Nada toca nas pastas globais do usuário.
- **Negativas:**
  - **Sincronia:** a geração precisa ser idempotente e detectar cópias divergentes, por exemplo quando alguém edita `.claude/skills/` à mão.
  - **Ocultas no git:** as duas pastas precisam ficar fora do git quando não forem versionadas (via `.git/info/exclude`), em todos os worktrees de todas as máquinas.
  - **Worktrees novos:** como o Claude Code procura skills só até a raiz do worktree, cada mesa nova precisa da cópia materializada ([ADR 0004](0004-mesa-igual-worktree.md)).
  - **Recarregar:** uma pasta de skills que não existia quando o agente iniciou pode exigir recarga (`/reload-skills` no Claude Code, `/skills reload` no Antigravity). O motor deve gerar as skills **antes** de iniciar o personagem.
- **Skills de apoio (revisão de 2026-10-18):**
  - **Positiva:** skill de engenharia completa (centenas de linhas) chega ao Codex e ao Antigravity sem ocupar o orçamento de 6.000 a 8.000 caracteres da camada do Mestre ([sessão zero do personagem](../sessao-zero/personagem.md)).
  - **Negativa:** o catálogo do Codex e do Antigravity mostra as skills de apoio da mesa também a quem não as tem ligadas; ler o corpo é negado pela regra gerada, mas o nome e a descrição aparecem. Ruído, não permissão: ler uma skill não libera ação nenhuma.
  - **Negativa:** a pasta compartilhada muda quando entra na mesa um personagem de outra classe, e o provedor pode precisar recarregar.
- **Antigravity e `.claude/skills/`:** não está documentado que ele leia essa pasta, e o Mestre não depende disso nem verifica: o Antigravity recebe as skills por `.agents/skills/`. Se ele ler as duas, vê a mesma skill duas vezes com o mesmo conteúdo, sem efeito nas permissões.

## Fontes

- Claude Code, skills: https://code.claude.com/docs/en/skills
- Codex, skills: https://learn.chatgpt.com/docs/build-skills
- Antigravity, skills: https://antigravity.google/docs/skills
