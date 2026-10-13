# ADR 0012: Despachante único de hooks (`mestre hook`)

> **Assunto:** por que toda a lógica de hooks e permissões passa por um único comando · **Componente:** CLI · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. **Decisão revista em 2026-10-12:** os hooks do Codex saem do `.codex/hooks.json` do worktree e vão para a camada de usuário do perfil de conta (`$CODEX_HOME/hooks.json`), confiados uma vez por perfil. **Termos atualizados em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): Core e nó viram Cidadela e Torre (ou máquina); a decisão não muda.

## Contexto

- O motor precisa saber o que cada agente está fazendo sem depender de ler a tela. Também precisa aplicar as permissões da classe da mesma forma nos três provedores (MVP-6).
- **Os arquivos de hooks do projeto são compartilhados** por todos os agentes do mesmo provedor naquele worktree:
  - `.claude/settings.local.json` no Claude Code;
  - `hooks.json` do `CODEX_HOME` do perfil de conta no Codex (camada de usuário, exclusiva do Mestre; confiado uma vez por perfil);
  - `.agents/hooks.json` no Antigravity.

  Por isso eles não podem conter regras de uma classe específica.
- **Os provedores diferem em pontos críticos:**

| | Claude Code | Codex | Antigravity |
|---|---|---|---|
| Formato | eventos → grupos com `matcher` | igual ao Claude | parecido, só 5 eventos, entrada em camelCase |
| Hook que falha | libera (exit 1 **não** bloqueia; falha ou timeout liberam); `"onFailure":"block"` desde v2.1.295 | libera | **bloqueia** (exit ≠ 0, saída não-JSON, timeout ou `{}` sem decisão) |
| Confiança | hooks não rodam em pasta não confiada | cada hook precisa ser confiado; editar o hook invalida a confiança | hooks do projeto só em pasta confiada |

## Opções consideradas

1. **Hooks escritos por classe nos arquivos de cada provedor.**
   - Prós: cada provedor executa as próprias regras.
   - Contras:
     - Os arquivos são compartilhados, então Dev e QA no mesmo worktree teriam as mesmas regras.
     - A mesma regra seria escrita três vezes.
     - No Codex, toda mudança exigiria confiar o hook de novo.
2. **Só os controles nativos de cada provedor** (permissões do Claude Code, sandbox do Codex, autonomia do Antigravity).
   - Prós: nenhum código de hook.
   - Contras:
     - Capacidades diferentes por provedor, então a mesma classe se comportaria diferente.
     - O motor não fica sabendo dos eventos.
3. **Ler a tela do terminal.**
   - Prós: funciona com qualquer programa.
   - Contras: é frágil, quebra a cada versão e não consegue bloquear uma ação antes que ela aconteça.
4. **Um despachante único.** Todos os arquivos de hooks chamam o mesmo `mestre hook`, que descobre o personagem por `MESTRE_AGENT_ID` e aplica as regras.
   - Prós:
     - Regras escritas uma vez, valendo nos três provedores.
     - Arquivos de hooks iguais para todos os agentes.
     - O motor recebe os eventos.
   - Contras:
     - Um processo por evento, o que exige resposta rápida.
     - É um ponto único de falha, com semânticas de falha opostas entre os provedores.

Onde ficam os hooks do Codex (revisão de 2026-10-12):

- **(a) `.codex/hooks.json` em cada worktree** (primeira versão desta decisão), excluído do git e confiado por worktree. Contras: cada mesa e cada máquina exigiria aprovar os hooks de novo em `/hooks`, o que não é automático.
- **(b) `hooks.json` do `CODEX_HOME` do perfil de conta**, só com a chamada fixa ao `mestre hook`. Prós: confiado uma vez por perfil e por máquina, vale para todos os worktrees. Contras: depende de a confiança de camada de usuário valer para pastas novas (verificado na implementação).

## Decisão

- **Todos os hooks gerados chamam `mestre hook`**, passando o provedor e o evento.
- **O que o `mestre hook` faz:**
  - normaliza a entrada para um formato interno único;
  - aplica decretos, regras do Mestre, permissões da classe e hooks extras da classe;
  - responde no formato de cada provedor.
- **Permissões unificadas** no evento "antes de usar ferramenta", que existe nos três provedores.
- **Fiação de conteúdo fixo:** o arquivo de hooks gerado só chama `mestre hook` e não muda quando as regras mudam. Assim ele é confiado uma vez no Codex.
- **Respostas explícitas:**
  - Para bloquear no Claude Code, a resposta usa decisão JSON ou exit 2, com `"onFailure":"block"` nos hooks que existem para bloquear.
  - No Antigravity, a resposta é sempre um `allow` ou `deny` explícito.
- **Reforço nativo:** as permissões da classe também são traduzidas para os controles nativos de cada provedor, como defesa em profundidade.
- **Onde estão os detalhes:**
  - Contrato, formato normalizado, mapa de eventos e comportamento com o motor fora do ar: [adaptador](../provedores/adaptador.md).
  - Pipeline de decisão: [permissões](../classes/permissoes.md).
  - Particularidades de cada provedor: [Claude Code](../provedores/claude-code.md), [Codex](../provedores/codex.md) e [Antigravity](../provedores/antigravity.md).

## Consequências

- **Positivas:**
  - Dev e QA no mesmo worktree e no mesmo provedor se comportam diferente.
  - Uma regra nova vale na hora nos três provedores, sem regenerar arquivos.
  - O motor fica sabendo dos eventos sem depender da tela.
- **Negativas:**
  - **Latência:** cada chamada de ferramenta inicia um processo `mestre hook`, que precisa responder em poucos milissegundos.
  - **Motor fora do ar:** o `mestre hook` libera a ação, exceto para as regras locais que consegue checar sozinho. No Antigravity, falhar é bloquear o agente, e travar todos os agentes seria pior.
  - **Fora do Mestre:** o `mestre hook` só age quando `MESTRE_AGENT_ID` está presente. Em qualquer outro caso, libera e sai. Isso protege quem clona o repositório.
    - Se um arquivo de hook do Mestre acabar versionado, ele precisa funcionar numa máquina sem o Mestre, porque no Antigravity um comando inexistente bloqueia ([sessão zero da mesa](../sessao-zero/mesa.md)).
  - **Antigravity sem eventos:** não há eventos de início e fim de sessão nem de compactação. Esses personagens ficam com **monitoramento reduzido**, mostrado no canvas.
  - **`--dangerously-skip-permissions`:** no Antigravity, essa opção **não desliga os hooks** (testado na 1.3.3; a spec dizia o contrário). Mesmo assim, a base `antigravity` não pode usá-la, porque ela desliga a proteção nativa que serve de defesa em profundidade.
  - **Confiança antes de iniciar:** o motor precisa pré-confiar a pasta em cada provedor. Sem isso, os hooks não rodam no Claude Code, e no Codex o `AGENTS.md` do projeto não carrega; os hooks do Mestre no Codex dependem da confiança dada uma vez no perfil.
- **Confiança dos hooks do Codex:** decidida com o usuário em 2026-10-12 (opção b acima): os hooks ficam na camada de usuário do perfil de conta e são confiados **uma vez**, na configuração inicial do perfil Codex, guiada pelo painel ([perfis de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta)). Se essa confiança não valer, o plano B é o motor gravar o `trusted_hash` calculado pelo mesmo algoritmo do Codex, validado no teste de conformidade, e, se isso também falhar, o painel guiar a confiança por worktree; o item completo está em [Codex](../provedores/codex.md). `--dangerously-bypass-hook-trust` não é usado.

## Fontes

- Claude Code, hooks: https://code.claude.com/docs/en/hooks
- Codex, hooks: https://learn.chatgpt.com/docs/hooks
- Antigravity, hooks: https://antigravity.google/docs/hooks
