# ADR 0004: Mesa é um git worktree com branch própria

> **Assunto:** por que cada mesa é um worktree saído do espelho do Core · **Componente:** Core · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- Cada pedido grande (épico) vira uma mesa. Várias mesas trabalham em paralelo no mesmo repositório sem interferir uma na outra (MVP-4).
- Cada mesa precisa de:
  - uma pasta própria, onde os agentes editam e fazem commits;
  - uma branch própria, que vira o PR do épico;
  - um ciclo de vida claro: abrir, trabalhar, revisar, encerrar ou cancelar.
- Uma mesa pode ter agentes em mais de um nó.

## Opções consideradas

1. **Worktree por mesa, saído de um espelho local no Core.**
   - Prós:
     - Pastas isoladas compartilhando um único banco de objetos: rápido de criar e pouco disco.
     - Uma branch por mesa, nativo do git.
     - O espelho centraliza a sincronização com o GitHub.
   - Contras:
     - Arquivos gerados que não vão para o git não aparecem num worktree novo.
     - Operações git simultâneas no mesmo repositório disputam travas.
2. **Clone completo por mesa.**
   - Prós: isolamento total.
   - Contras:
     - Duplica objetos e tempo de clone a cada mesa.
     - Cada clone falaria com o GitHub por conta própria, sem um ponto central para o motor controlar o envio.
3. **Uma pasta só, trocando de branch (checkout) por mesa.**
   - Prós: simples.
   - Contras: impossível ter duas mesas trabalhando ao mesmo tempo.
4. **Container por mesa.**
   - Prós: isolamento de processos e dependências.
   - Contras:
     - Exige runtime de containers no Core e no Windows.
     - Complica PTY, credenciais dos provedores e agentes com interface gráfica.
     - Fora do escopo do MVP.

## Decisão

- **Mesa = um git worktree com a sua branch**, ligado a uma issue do GitHub (o épico).
- O Core mantém um **espelho local** de cada workspace, sem área de trabalho. O clone principal do Grão-Mestre e os worktrees das mesas no Core saem dele.
- **Mesa em outro nó:** cada nó que roda agentes de uma mesa tem o **próprio worktree** da mesma branch. A sincronização é feita com o espelho do Core, nunca direto com o GitHub ([ADR 0013](0013-sincronizacao-de-worktrees-por-git-bundle.md)).
- **Onde estão os detalhes:**
  - Ciclo de vida, nome da branch, remoção do worktree e cancelamento: [mesas](../orquestracao/mesas.md).
  - Arquivos gerados em cada worktree: [sessão zero da mesa](../sessao-zero/mesa.md).

## Consequências

- **Positivas:**
  - Mesas paralelas não se veem no disco.
  - Abrir uma mesa é barato.
  - Só o motor envia a branch do espelho para o GitHub, o que reforça o [ADR 0005](0005-motor-executa-github-grao-mestre-porteiro.md).
- **Negativas:**
  - A cada mesa aberta (e em cada nó), o motor precisa materializar skills copiadas, hooks locais e arquivos de execução, e confiar a pasta nos provedores ([sessão zero da mesa](../sessao-zero/mesa.md)).
    - O Claude Code procura skills só até a raiz do worktree. Por isso a cópia em `.claude/skills/` precisa existir dentro de cada worktree ([ADR 0011](0011-agents-skills-pasta-oficial.md)).
    - A confiança de pasta do Claude Code e do Codex é resolvida pela raiz do checkout principal, não pelo worktree. O efeito exato está nos documentos de [Claude Code](../provedores/claude-code.md) e [Codex](../provedores/codex.md).
  - Worktrees compartilham o banco de objetos e as referências. O motor serializa as operações git que mexem em referências do mesmo repositório (por exemplo, criar ou remover worktree enquanto outro faz commit).
  - Uma mesa espalhada em vários nós pode ter conflitos se dois nós editarem os mesmos arquivos. O Mestre da mesa coordena isso ([Mestre da mesa](../orquestracao/mestre-da-mesa.md)).
  - Remover um worktree com alterações não enviadas apagaria trabalho. A remoção automática só acontece nas condições definidas em [mesas](../orquestracao/mesas.md).
