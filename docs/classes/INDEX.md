# Classes

> **Assunto:** o que cada personagem faz e pode fazer: pacote da classe, permissões, teto do workspace e risco dos pedidos · **Componente:** Motor · **Atualizado:** 2026-10-18

| Documento | O que cobre | Quando ler |
|---|---|---|
| [classes.md](classes.md) | estrutura do pacote (`CLASS.md`, `permissions.yaml`, hooks, skills), escopos global/workspace/mesa, vínculo obrigatório com App, hooks extras por evento do Mestre, quem cria e `class check` | ao criar, validar ou entregar classes aos personagens |
| [classes-sugeridas.md](classes-sugeridas.md) | o pacote que vem desligado: ativar e atualizar, as duas fichas de cada classe, a etapa opcional das receitas, a calibração, o ritual Vigia | ao ativar o time sugerido ou mudar o pacote |
| [skills-de-apoio.md](skills-de-apoio.md) | skills de engenharia que a classe traz com liga/desliga: onde ficam, como chegam a cada provedor, as camadas que fazem o desligado valer, licença | ao pôr numa classe uma skill de terceiros |
| [mcp-por-classe.md](mcp-por-classe.md) | catálogo de servidores MCP do workspace, o campo `mcp` da classe e o gateway do Mestre que entrega só os servidores da classe, igual nos três provedores | ao dar a uma classe um servidor MCP |
| [sugeridas/](sugeridas/INDEX.md) | o conteúdo do pacote: as sete classes e as receitas de missão | ao ler ou adaptar uma classe sugerida |
| [classe-orquestrador.md](classe-orquestrador.md) | a classe fixa embutida de onde o Grão-Mestre e os Mestres de mesa tiram as regras efetivas, as camadas que só restringem e o papel dela na atenuação | ao montar as regras dos orquestradores ou conferir uma decisão de agente da cadeia |
| [permissoes.md](permissoes.md) | regras fixas de segurança, regras efetivas (documento por personagem), passo a passo do despachante (com o `baixo` por padrão que roda direto), pedido de aprovação, modo degradado, ficha só restringe | ao implementar decisões de permissão ou a tradução para cada provedor |
| [formato-das-permissoes.md](formato-das-permissoes.md) | o arquivo `permissions.yaml` (também do teto e da ficha), padrões de arquivos, comandos e ferramentas e como o despachante os casa | ao escrever ou validar um `permissions.yaml` ou mexer no casamento de comandos |
| [classificacao-de-risco.md](classificacao-de-risco.md) | risco de cada pedido de aprovação (baixo, médio, alto, fixo) dado pelo motor: tabela padrão de comandos e ferramentas, sinais de risco, risco dos tipos de pedido, ajustes do Dono na política, padrões aprováveis e o que cobrem | ao classificar um pedido, ajustar a tabela de risco ou validar um padrão aprovado |
| [lista-de-seguros.md](lista-de-seguros.md) | a lista de programas e formas de argumento que o risco dá como `baixo` (o resto é `medio`), o que fica fora, os comandos dentro de argumentos, a leitura no Windows e como o Dono estende a lista | ao conferir por que um comando é `baixo` ou `medio`, ou ao pôr um programa na lista |
| [reforco-nativo.md](reforco-nativo.md) | tradução das regras efetivas para os controles nativos de cada provedor, regeneração quando as regras mudam | ao implementar ou conferir a defesa em profundidade de um provedor |
| [teto-do-workspace.md](teto-do-workspace.md) | arquivo do teto de permissões e custo, onde ele age, quem pode restringir ou ampliar, mudanças feitas por fora | ao validar classes ou mexer nos limites de um workspace |
