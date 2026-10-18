# MCP por classe

> **Assunto:** o catálogo de servidores MCP do workspace, o campo `mcp` da classe, a configuração de cada personagem por provedor, a negação gerada para os servidores que a classe não declara e a rede · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Deixar a classe dizer de quais servidores MCP o personagem precisa (navegador para o QA e o Designer, documentação oficial para o Pesquisador e o Dev) sem que um autor de classe possa fazer o motor iniciar um programa qualquer. A decisão está no [ADR 0026](../decisoes/0026-mcp-por-classe.md).

## Comportamento

### O catálogo do workspace

Fica em `.mestre/regras/mcp.yaml` no clone principal ([estrutura de `.mestre/`](../sessao-zero/workspace.md#estrutura-de-mestre)) e muda só pelo painel, por Administrador ou acima ([administração](../painel/administracao.md)):

```yaml
version: 1
servers:
  playwright:
    command: ["npx", "-y", "@playwright/mcp@0.0.41", "--headless", "--isolated"]   # versão fixada
    domains: ["*"]                         # o que o servidor acessa (informativo)
  context7:
    url: "https://mcp.context7.com/mcp"   # sem chave: o plano gratuito
    domains: ["mcp.context7.com"]
```

- Cada servidor tem `command` (com a versão fixada) ou `url` e os `domains` que ele acessa. **Não há campo de segredo:** servidor que precisa de chave fica fora do catálogo (abaixo, "A confirmar"); chave escrita em `command` ou `url` é recusada pela regra do [cofre](../plataforma/cofre.md).
- **Entrar no catálogo é uma decisão de pessoa:** quem acrescenta é Administrador ou acima. Fica na auditoria. O servidor roda como `mestre-agente` e fala com a rede por conta própria: o tráfego dele não passa pelas regras de rede da classe, e o `domains` registra para quem aprova o que está sendo liberado.
- Servidor de controle do computador ou do navegador do usuário é recusado no catálogo, qualquer que seja o nome dado a ele: o binário traz a lista desses pacotes e endereços, e o motor confere o `command` e a `url`, não o nome. Só a base Operador do PC os usa ([regras fixas](permissoes.md#regras-fixas-de-segurança)).
- Arquivo mudado por fora do painel: vale a última versão aprovada e os Administradores são avisados, como no [teto](teto-do-workspace.md).

### A classe declara

```yaml
mcp:                       # frontmatter do CLASS.md
  - {name: playwright, enabled: true}
```

- O `class check` confere que cada nome existe no catálogo do workspace; nome que não existe é erro, e a classe não é salva.
- Declarar não libera as ferramentas: quem decide cada chamada continua sendo `tools` do [`permissions.yaml`](formato-das-permissoes.md) (ex.: `tools.allow: ["mcp__playwright__*"]`).
- O motor gera, nas regras efetivas de cada personagem, `tools.deny: ["mcp__<servidor>__*"]` para todo servidor do catálogo que a classe dele **não** declara. Assim um servidor que estiver no perfil de conta por outro motivo não serve ao personagem errado.
- `enabled` é o mesmo liga/desliga das [skills de apoio](skills-de-apoio.md), com as mesmas aprovações: desligado é como não declarado. As [classes sugeridas](classes-sugeridas.md) vêm com tudo desligado.

### Configuração por provedor

O motor configura, no início do personagem, **só** os servidores ligados na classe:

| Provedor | Como |
|---|---|
| Claude Code | arquivo de servidores na pasta do personagem (`.mestre/agents/<id>/`), passado no início junto com a opção que faz o Claude ignorar os outros arquivos de servidores, inclusive o `.mcp.json` do repositório ([Claude Code](../provedores/claude-code.md)) |
| Codex | os servidores pela configuração de início do personagem (`-c`, que só o motor passa; a base não pode, [Codex](../provedores/codex.md)) |
| Antigravity | **nenhum servidor no M8:** o reforço nativo tira `mcpServers` do perfil ([reforço nativo](reforco-nativo.md)), e não há configuração por personagem. O personagem inicia sem servidores, a camada do Mestre diz isso e o `class check` avisa (abaixo, "A confirmar") |

Em todos, a camada do Mestre do personagem tem uma linha com os servidores dele.

## Regras e erros

- Servidor que não inicia (programa ausente, `url` fora): o personagem inicia sem ele, a camada do Mestre diz isso e o Mestre da mesa é avisado; o `class check` mostra o servidor como indisponível na máquina.
- Remover um servidor do catálogo com classe que o declara: recusado, listando as classes.
- Servidor no catálogo com chave escrita no `command` ou na `url` (padrões de segredo do [cofre](../plataforma/cofre.md)): recusado.

## Critérios de aceite

- **CA-MCP-01** · *rápida* — **Dado** um catálogo com `playwright` e `context7` e uma classe `qa` com `playwright` ligado e `context7` não declarado, **quando** a Cidadela monta as regras efetivas de um QA, **então** elas têm `tools.deny` para `mcp__context7__*` e não para `mcp__playwright__*`.
- **CA-MCP-02** · *rápida* — **Dado** uma classe que declara o servidor `inexistente`, **quando** o `class check` roda, **então** aponta o erro e a classe não é salva.
- **CA-MCP-03** · *rápida* — **Dado** um Administrador que acrescenta ao catálogo um servidor chamado `browser` cujo `command` é um pacote de controle do navegador do usuário, **quando** salva, **então** é recusado com o motivo; **dado** um servidor com uma chave na `url`, **então** é recusado pela regra do cofre.
- **CA-MCP-04** · *rápida* — **Dado** um QA Claude Code com `playwright` ligado e um `.mcp.json` no repositório com outro servidor, **quando** o motor monta o início, **então** o arquivo de servidores do personagem tem só `playwright` e a opção de ignorar os outros arquivos vai junto.
- **CA-MCP-05** · *completa* — **Dado** um QA Codex com `playwright` ligado e o agente falso, **quando** ele chama `mcp__context7__resolve` (servidor do perfil, fora da classe), **então** o despachante nega e registra.

## Depende de

- [Classes](classes.md)
- [Formato das permissões](formato-das-permissoes.md)
- [Permissões](permissoes.md)
- [Reforço nativo](reforco-nativo.md)
- [Cofre](../plataforma/cofre.md)
- [Sessão zero do workspace](../sessao-zero/workspace.md)

## Verificar na implementação

- **Verificar na implementação:** que o Claude Code da versão em uso aceita um arquivo de servidores por início e a opção de ignorar os demais, sem mostrar a tela de aprovação de servidores. **Padrão:** aceita, e a tela não aparece. **Se falhar:** o Claude recebe os servidores pelo perfil de conta, como o Antigravity, e só a negação gerada separa um personagem do outro. **Onde:** [teste de classe](../provedores/teste-de-classe.md) com um caso que usa e um que proíbe um servidor, no Claude Code.
- **Verificar na implementação:** que o Codex aceita servidores pela configuração de início (`-c`), somados ou no lugar dos do `config.toml` do perfil. **Padrão:** aceita, e o motor deixa o `config.toml` do perfil sem servidores. **Se falhar:** o mesmo plano do Claude. **Onde:** o mesmo caso, no Codex.

## A confirmar

- **Servidor com chave:** só o processo do servidor deveria ver o valor, mas ele roda na mesma conta do agente, que poderia lê-lo. Fica de fora até haver um jeito de entregar a chave que o agente não alcance.
- **Antigravity:** servidores por personagem, sem devolver `mcpServers` ao perfil compartilhado. Até lá, QA e Designer que precisam de navegador usam Claude Code ou Codex.
