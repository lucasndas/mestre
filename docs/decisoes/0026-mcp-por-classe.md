# ADR 0026: Servidores MCP declarados pela classe, de um catálogo do workspace

> **Assunto:** por que a classe passa a dizer de quais servidores MCP o personagem precisa, escolhidos de um catálogo aprovado do workspace, e o motor os configura por personagem em cada provedor · **Componente:** Motor · **Atualizado:** 2026-10-18

## Status

Aceita, 2026-10-18, por decisão do Dono. **Revista no mesmo dia:** a entrega passa a ser pelo gateway do Mestre (opção 4), para valer igual nos três provedores e para servidores com chave. Complementa o [ADR 0011](0011-agents-skills-pasta-oficial.md) (revisão das skills de apoio): os dois tratam do que uma classe traz além das próprias regras.

## Contexto

- Hoje a classe só **filtra** ferramentas MCP pelo nome (`tools` no [`permissions.yaml`](../classes/formato-das-permissoes.md)); de onde vem o servidor não está dito em lugar nenhum: cada personagem que navega usa "o próprio MCP de navegador, iniciado pelo CLI dele" ([segurança](../arquitetura/seguranca.md)), e o `.mcp.json` do repositório pede aprovação na tela do Claude Code, que o adaptador escala ([Claude Code](../provedores/claude-code.md)).
- Papéis comuns precisam de servidores diferentes: um QA e um Designer precisam de navegador (Playwright) para capturar a tela; um Pesquisador e um Dev ganham com a documentação oficial das bibliotecas (context7). Sem a classe dizer isso, o usuário configura à mão em cada perfil de conta, e todo personagem do perfil herda tudo.
- Os três provedores configuram MCP de jeitos diferentes (documentação oficial, 2026-10-18): o Claude aceita servidores por início (`--mcp-config`, `--strict-mcp-config`); o Codex, por `-c` ou `CODEX_HOME`; o Antigravity só por arquivo do perfil ou do projeto, compartilhado, sem troca de variável nem configuração por início. Nenhum dos três guarda uma chave longe de um agente que roda na mesma conta.
- Um servidor MCP local é um processo que roda como `mestre-agente` e fala com a rede por conta própria: o tráfego dele **não** passa pelo despachante nem pelas regras de rede da classe; só as chamadas às ferramentas dele passam.

## Opções consideradas

1. **Deixar como está** (servidor configurado à mão no perfil, classe só filtra).
   - Prós: nada a construir.
   - Contras: todo personagem do perfil herda todos os servidores; nada documenta o que a classe precisa; não dá para ativar uma classe sugerida pronta para uso.
2. **A classe traz a definição do servidor** (comando, argumentos, segredos).
   - Prós: a classe é autossuficiente.
   - Contras: qualquer autor de classe da mesa faria o motor iniciar um programa novo; segredo dentro de pacote que viaja pela replicação.
3. **Catálogo do workspace aprovado por Administrador, e a classe só cita nomes dele; o motor configura cada provedor.**
   - Prós: quem decide que programa roda é uma pessoa com nível para isso; a classe continua só texto e regras.
   - Contras: no Antigravity a configuração é do perfil, comum a todos os personagens dele, e não dá para separar; chave no ambiente do servidor fica ao alcance do agente; cada provedor tem um caminho e um conjunto de falhas.
4. **O catálogo da opção 3, entregue por um gateway do Mestre:** cada CLI recebe só o servidor `mestre` (o próprio binário, por stdio), que o motor identifica pelo canal local e que mostra e atende só os servidores da classe; os remotos são chamados pelo motor, com a chave.
   - Prós: a mesma configuração nos três provedores, inclusive num perfil compartilhado; a decisão de cada chamada é do motor, com ou sem hook do provedor para MCP; a chave de servidor remoto nunca chega à conta do agente.
   - Contras: o Mestre passa a falar o protocolo MCP e fica no caminho de toda chamada; servidor local com chave ainda pede outra conta.

## Decisão

- **Opção 4.** O workspace tem um **catálogo de servidores MCP**, a classe declara `mcp:` no frontmatter do `CLASS.md`, e o gateway do Mestre é o único servidor que cada CLI conhece. Detalhes em [MCP por classe](../classes/mcp-por-classe.md).
- O nome `mestre` é reservado; repositório com servidores MCP próprios é ignorado no Claude (`--strict-mcp-config`) e impede o início do personagem no Codex e no Antigravity. Toda ferramenta MCP que não vem do gateway é negada pelo despachante; ligar um servidor só passa se os `domains` dele cabem na rede da classe e do teto.
- Chave só em servidor remoto, como `mcp_key` do [cofre](../plataforma/cofre.md), usada pelo motor e só nos domínios dela. Servidor local com chave fica em "A confirmar" (conta própria `mestre-mcp`).
- As regras fixas continuam valendo antes de tudo: MCP de controle do computador ou do navegador do usuário só na base Operador do PC ([ADR 0021](0021-operador-do-pc.md)); só o Dono cadastra um servidor desses, e ele só serve à base Operador, julgado pelo pacote ou endereço e pela base, não pelo nome. O Operador não tem caminho próprio: os MCPs dele vêm do catálogo pelo gateway, e o `--chrome` sai.

## Consequências

- **Positivas:**
  - Cada personagem tem só os servidores do papel dele, em qualquer provedor; uma classe sugerida ativada já sai pronta (ex.: QA com navegador).
  - O que roda numa máquina como servidor MCP é decisão de uma pessoa, registrada na auditoria.
- **Negativas:**
  - A rede de um servidor local não é filtrada pela classe: ligar é aprovar o que o servidor acessa, e por isso os `domains` dele precisam caber na rede da classe e do teto.
  - O motor fora do ar deixa o personagem sem MCP (falha fechada), e uma chamada que pede aprovação pode passar do limite de tempo do CLI.
  - As regras de `files` não olham os argumentos de uma ferramenta MCP; um servidor local alcança o que o `mestre-agente` alcança.

## Fontes

- Claude Code, MCP (`--mcp-config`, `--strict-mcp-config`): https://code.claude.com/docs/en/mcp
- Codex, MCP (`mcp_servers`, `-c`): https://developers.openai.com/codex/mcp
- Antigravity, MCP (`mcp_config.json`): https://antigravity.google/docs/mcp
- Playwright MCP (argumentos, modo `--extension`): https://github.com/microsoft/playwright-mcp
