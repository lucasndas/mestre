# ADR 0026: Servidores MCP declarados pela classe, de um catálogo do workspace

> **Assunto:** por que a classe passa a dizer de quais servidores MCP o personagem precisa, escolhidos de um catálogo aprovado do workspace, e o motor os configura por personagem em cada provedor · **Componente:** Motor · **Atualizado:** 2026-10-18

## Status

Aceita, 2026-10-18, por decisão do Dono. Complementa o [ADR 0011](0011-agents-skills-pasta-oficial.md) (revisão das skills de apoio): os dois tratam do que uma classe traz além das próprias regras.

## Contexto

- Hoje a classe só **filtra** ferramentas MCP pelo nome (`tools` no [`permissions.yaml`](../classes/formato-das-permissoes.md)); de onde vem o servidor não está dito em lugar nenhum: cada personagem que navega usa "o próprio MCP de navegador, iniciado pelo CLI dele" ([segurança](../arquitetura/seguranca.md)), e o `.mcp.json` do repositório pede aprovação na tela do Claude Code, que o adaptador escala ([Claude Code](../provedores/claude-code.md)).
- Papéis comuns precisam de servidores diferentes: um QA e um Designer precisam de navegador (Playwright) para capturar a tela; um Pesquisador e um Dev ganham com a documentação oficial das bibliotecas (context7). Sem a classe dizer isso, o usuário configura à mão em cada perfil de conta, e todo personagem do perfil herda tudo.
- Um servidor MCP é um processo que roda como `mestre-agente` e fala com a rede por conta própria: o tráfego dele **não** passa pelo despachante nem pelas regras de rede da classe; só as chamadas às ferramentas dele passam.

## Opções consideradas

1. **Deixar como está** (servidor configurado à mão no perfil, classe só filtra).
   - Prós: nada a construir.
   - Contras: todo personagem do perfil herda todos os servidores; nada documenta o que a classe precisa; não dá para ativar uma classe sugerida pronta para uso.
2. **A classe traz a definição do servidor** (comando, argumentos, segredos).
   - Prós: a classe é autossuficiente.
   - Contras: qualquer autor de classe da mesa faria o motor iniciar um programa novo; segredo dentro de pacote que viaja pela replicação.
3. **Catálogo do workspace aprovado por Administrador, e a classe só cita nomes dele.**
   - Prós: quem decide que programa roda é uma pessoa com nível para isso; a classe continua só texto e regras; o mesmo servidor serve a várias classes.
   - Contras: um passo a mais para o Administrador; servidores com segredo exigem cuidado (abaixo).

## Decisão

- **Opção 3.** O workspace tem um **catálogo de servidores MCP** e a classe declara `mcp: [<nome>…]` no frontmatter do `CLASS.md`. Detalhes em [MCP por classe](../classes/mcp-por-classe.md).
- O motor configura **só** os servidores da classe no CLI de cada personagem, por provedor, e gera nas regras efetivas o `tools.deny` dos servidores do catálogo que a classe não declara.
- As regras fixas continuam valendo antes de tudo: MCP de controle do computador ou do navegador do usuário só na base Operador do PC ([ADR 0021](0021-operador-do-pc.md)); um servidor desses é recusado no catálogo de qualquer outra base.

## Consequências

- **Positivas:**
  - Cada personagem tem só os servidores do papel dele; uma classe sugerida ativada já sai pronta (ex.: QA com navegador).
  - O que roda numa máquina como servidor MCP é decisão de uma pessoa, registrada na auditoria.
- **Negativas:**
  - A rede do servidor não é filtrada pela classe: entrar no catálogo é aprovar o que o servidor acessa. O catálogo registra os domínios que ele usa, para quem aprova saber.
  - Servidor com segredo recebe o valor do [cofre](../plataforma/cofre.md) no ambiente do processo; ele roda na mesma conta do agente, que em tese poderia lê-lo. Por isso servidor com segredo entra no catálogo só pelo Dono, com PIN.
  - A configuração por personagem depende de cada provedor aceitar servidores fora da configuração do perfil; onde não aceitar, vale o plano B do documento de [MCP por classe](../classes/mcp-por-classe.md#verificar-na-implementação).
