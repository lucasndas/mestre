# Classes

> **Assunto:** o que cada personagem faz e pode fazer: pacote da classe, permissões, teto do workspace e risco dos pedidos · **Componente:** Motor · **Atualizado:** 2026-10-14

| Documento | O que cobre | Quando ler |
|---|---|---|
| [classes.md](classes.md) | estrutura do pacote (`CLASS.md`, `permissions.yaml`, hooks, skills), escopos global/workspace/mesa, vínculo obrigatório com App, hooks extras por evento do Mestre, quem cria e `class check` | ao criar, validar ou entregar classes aos personagens |
| [permissoes.md](permissoes.md) | regras fixas de segurança, regras efetivas (documento por personagem), passo a passo do despachante, pedido de aprovação, modo degradado, ficha só restringe | ao implementar decisões de permissão ou a tradução para cada provedor |
| [formato-das-permissoes.md](formato-das-permissoes.md) | o arquivo `permissions.yaml` (também do teto e da ficha), padrões de arquivos, comandos e ferramentas e como o despachante os casa | ao escrever ou validar um `permissions.yaml` ou mexer no casamento de comandos |
| [classificacao-de-risco.md](classificacao-de-risco.md) | risco de cada pedido de aprovação (baixo, médio, alto, fixo) dado pelo motor: tabela padrão de comandos e ferramentas, risco dos tipos de pedido, ajustes do Dono na política, padrões aprováveis | ao classificar um pedido, ajustar a tabela de risco ou validar um padrão aprovado |
| [reforco-nativo.md](reforco-nativo.md) | tradução das regras efetivas para os controles nativos de cada provedor, regeneração quando as regras mudam | ao implementar ou conferir a defesa em profundidade de um provedor |
| [teto-do-workspace.md](teto-do-workspace.md) | arquivo do teto de permissões e custo, onde ele age, quem pode restringir ou ampliar, mudanças feitas por fora | ao validar classes ou mexer nos limites de um workspace |
