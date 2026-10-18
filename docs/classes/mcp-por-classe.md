# MCP por classe

> **Assunto:** o catálogo de servidores MCP do workspace, o campo `mcp` da classe, a configuração de cada personagem por provedor, a negação gerada para os servidores que a classe não declara, segredos e rede · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Deixar a classe dizer de quais servidores MCP o personagem precisa (navegador para o QA e o Designer, documentação oficial para o Pesquisador e o Dev) sem que um autor de classe possa fazer o motor iniciar um programa qualquer. A decisão está no [ADR 0026](../decisoes/0026-mcp-por-classe.md).

## Comportamento

### O catálogo do workspace

Fica em `.mestre/regras/mcp.yaml` no clone principal ([estrutura de `.mestre/`](../sessao-zero/workspace.md#estrutura-de-mestre)) e muda só pelo painel, por Administrador ou acima ([administração](../painel/administracao.md)):

```yaml
version: 1
servers:
  playwright:
    command: ["npx", "-y", "@playwright/mcp@0.0.41", "--headless"]   # versão fixada
    domains: ["*"]                         # o que o servidor acessa (informativo)
  context7:
    url: "https://mcp.context7.com/mcp"
    domains: ["mcp.context7.com"]
    secrets: {CONTEXT7_API_KEY: context7}  # variável ← nome no cofre
```

- Cada servidor tem `command` (com a versão fixada) ou `url`, os `domains` que ele acessa e, opcional, `secrets`: variáveis de ambiente lidas do [cofre](../plataforma/cofre.md) pelo nome.
- **Entrar no catálogo é uma decisão de pessoa:** quem acrescenta é Administrador ou acima; servidor com `secrets` só o Dono, com o PIN ([PIN em ações sensíveis](../painel/login.md#pin-em-ações-sensíveis)). Fica na auditoria. O servidor roda como `mestre-agente` e fala com a rede por conta própria: o tráfego dele não passa pelas regras de rede da classe, e o `domains` registra para quem aprova o que está sendo liberado.
- Servidor de controle do computador ou do navegador do usuário é recusado no catálogo, qualquer que seja o transporte: só a base Operador do PC os usa ([regras fixas](permissoes.md#regras-fixas-de-segurança)).
- Arquivo mudado por fora do painel: vale a última versão aprovada e os Administradores são avisados, como no [teto](teto-do-workspace.md).

### A classe declara

```yaml
mcp: [playwright]          # frontmatter do CLASS.md
```

- O `class check` confere que cada nome existe no catálogo do workspace; nome que não existe é erro, e a classe não é salva.
- Declarar não libera as ferramentas: quem decide cada chamada continua sendo `tools` do [`permissions.yaml`](formato-das-permissoes.md) (ex.: `tools.allow: ["mcp__playwright__*"]`).
- O motor gera, nas regras efetivas de cada personagem, `tools.deny: ["mcp__<servidor>__*"]` para todo servidor do catálogo que a classe dele **não** declara. Assim um servidor que estiver no perfil de conta por outro motivo não serve ao personagem errado.
- Numa classe sugerida, cada servidor tem o mesmo liga/desliga das [skills de apoio](skills-de-apoio.md): desligado é como não declarado.

### Configuração por provedor

O motor configura, no início do personagem, **só** os servidores que a classe declara, com os segredos do cofre no ambiente do processo do servidor:

| Provedor | Como |
|---|---|
| Claude Code | arquivo de servidores na pasta do personagem (`.mestre/agents/<id>/`), passado no início junto com a opção que faz o Claude ignorar os outros arquivos de servidores, inclusive o `.mcp.json` do repositório ([Claude Code](../provedores/claude-code.md)) |
| Codex | os servidores pela configuração de início do personagem (`-c`, que só o motor passa; a base não pode, [Codex](../provedores/codex.md)) |
| Antigravity | a configuração de servidores é do perfil de conta, compartilhada; o perfil recebe os servidores das classes que o usam, e o campo `tools` do arquivo de agente do personagem lista só as ferramentas dos servidores da classe dele ([Antigravity](../provedores/antigravity.md), [reforço nativo](reforco-nativo.md)) |

Em todos, a camada do Mestre do personagem tem uma linha com os servidores dele.

## Regras e erros

- Servidor que não inicia (programa ausente, `url` fora): o personagem inicia sem ele, a camada do Mestre diz isso e o Mestre da mesa é avisado; o `class check` mostra o servidor como indisponível na máquina.
- Remover um servidor do catálogo com classe que o declara: recusado, listando as classes.
- Segredo citado que não existe no cofre: o servidor não entra no catálogo.

## Critérios de aceite

- **CA-MCP-01** · *rápida* — **Dado** um catálogo com `playwright` e `context7` e uma classe `qa` com `mcp: [playwright]`, **quando** a Cidadela monta as regras efetivas de um QA, **então** elas têm `tools.deny` para `mcp__context7__*` e não para `mcp__playwright__*`.
- **CA-MCP-02** · *rápida* — **Dado** uma classe com `mcp: [inexistente]`, **quando** o `class check` roda, **então** aponta o erro e a classe não é salva.
- **CA-MCP-03** · *rápida* — **Dado** um Administrador que acrescenta ao catálogo um servidor com `secrets`, **quando** salva, **então** é recusado com "só o Dono, com PIN", e o Dono com PIN consegue; **dado** um servidor de controle do computador do usuário, **então** é recusado com o motivo.
- **CA-MCP-04** · *rápida* — **Dado** um QA Claude Code com `mcp: [playwright]` e um `.mcp.json` no repositório com outro servidor, **quando** o motor monta o início, **então** o arquivo de servidores do personagem tem só `playwright` e a opção de ignorar os outros arquivos vai junto.
- **CA-MCP-05** · *completa* — **Dado** um QA Codex com `mcp: [playwright]` e o agente falso, **quando** ele chama `mcp__context7__resolve` (servidor do perfil, fora da classe), **então** o despachante nega e registra.

## Depende de

- [Classes](classes.md)
- [Formato das permissões](formato-das-permissoes.md)
- [Permissões](permissoes.md)
- [Reforço nativo](reforco-nativo.md)
- [Cofre](../plataforma/cofre.md)
- [Sessão zero do workspace](../sessao-zero/workspace.md)
- [Login](../painel/login.md)

## Verificar na implementação

- **Verificar na implementação:** que o Claude Code da versão em uso aceita um arquivo de servidores por início e a opção de ignorar os demais, sem mostrar a tela de aprovação de servidores. **Padrão:** aceita, e a tela não aparece. **Se falhar:** o Claude recebe os servidores pelo perfil de conta, como o Antigravity, e só a negação gerada separa um personagem do outro. **Onde:** [teste de classe](../provedores/teste-de-classe.md) com um caso que usa e um que proíbe um servidor, no Claude Code.
- **Verificar na implementação:** que o Codex aceita servidores pela configuração de início (`-c`), somados ou no lugar dos do `config.toml` do perfil. **Padrão:** aceita, e o motor deixa o `config.toml` do perfil sem servidores. **Se falhar:** o mesmo plano do Claude. **Onde:** o mesmo caso, no Codex.
- **Verificar na implementação:** o nome com que o Antigravity entrega as ferramentas MCP ao hook ([adaptador](../provedores/adaptador.md#verificar-na-implementação)) e se o campo `tools` do arquivo de agente as aceita. **Padrão:** `mcp__<servidor>__<ferramenta>`, aceito no campo. **Se falhar:** só a negação do despachante, que no `agy` falha fechado. **Onde:** o mesmo caso, no Antigravity.
