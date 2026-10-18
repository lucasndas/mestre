# MCP por classe

> **Assunto:** o catálogo de servidores MCP do workspace, o campo `mcp` da classe, o gateway do Mestre que entrega a cada personagem só os servidores dele, igual nos três provedores, as chaves e a rede · **Componente:** Motor · **Atualizado:** 2026-10-18

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
    url: "https://mcp.context7.com/mcp"
    domains: ["mcp.context7.com"]
    auth: {header: CONTEXT7_API_KEY, secret: context7}   # nome no cofre, nunca o valor
```

- Cada servidor tem `command` (com a versão exata) ou `url` e os `domains` que ele acessa. `command[0]` é um lançador conhecido (`npx`, `uvx`, `node`, `python`), nunca um shell (`sh`, `bash`, `cmd`, `powershell`). O motor instala o pacote, com as dependências travadas e conferidas, numa pasta dele que o `mestre-agente` não escreve, e o servidor roda dali; nada é baixado no início do personagem. **Chave só em servidor remoto:** `auth` (o cabeçalho e o nome de uma `mcp_key` do [cofre](../plataforma/cofre.md)) só existe com `url`, e quem faz a chamada com a chave é o motor (abaixo, "O gateway"); servidor local com chave fica fora (abaixo, "A confirmar"). Chave escrita em `command` ou `url` é recusada.
- **Entrar no catálogo é uma decisão de pessoa:** quem acrescenta é Administrador ou acima. Fica na auditoria. O servidor local roda como `mestre-agente` e fala com a rede por conta própria: o tráfego dele não passa pelas regras de rede da classe, e o `domains` diz o que está sendo liberado (abaixo, "Rede").
- Servidor de controle do computador ou do navegador do usuário é recusado no catálogo, qualquer que seja o nome dado a ele: o binário traz a lista desses pacotes e endereços, e o motor confere o pacote do `command` e a `url`, não o nome. Só a base Operador do PC os usa ([regras fixas](permissoes.md#regras-fixas-de-segurança)).
- Arquivo mudado por fora do painel: vale a última versão aprovada e os Administradores são avisados, como no [teto](teto-do-workspace.md).

### A classe declara

```yaml
mcp:                       # frontmatter do CLASS.md
  - {name: playwright, enabled: true}
```

- O `class check` confere que cada nome existe no catálogo do workspace; nome que não existe é erro, e a classe não é salva.
- **Rede:** ligar um servidor é dar a rede dele ao personagem. O `class check` e a montagem das regras efetivas recusam ligar um servidor cujos `domains` não cabem nas regras de rede da classe e do [teto](teto-do-workspace.md) (`["*"]` exige internet sem lista de domínios); vale também para classe da mesa, que não passa por aprovação.
- Declarar não libera as ferramentas: quem decide cada chamada continua sendo `tools` do [`permissions.yaml`](formato-das-permissoes.md) (ex.: `tools.allow: ["mcp__playwright__*"]`).
- Toda ferramenta MCP que não vem do gateway (de um servidor do perfil de conta ou do repositório) é negada pelo despachante, e o gateway só mostra e só atende as dos servidores ligados na classe do personagem.
- `enabled` é o mesmo liga/desliga das [skills de apoio](skills-de-apoio.md), com as mesmas aprovações: desligado é como não declarado. As [classes sugeridas](classes-sugeridas.md) vêm com tudo desligado.

### O gateway do Mestre

Cada CLI recebe **um único servidor MCP**, o `mestre`: o próprio binário (`mestre mcp`, pelo caminho estável), por stdio, sem variável de ambiente nem argumento que mude de um personagem para outro. Por ser filho do CLI, ele está na árvore do terminal do personagem, e o motor o identifica pelo [canal local](../arquitetura/canal-local.md) (`cmd: "mcp"`) como identifica o despachante: pelo cgroup no Linux e pelo Job Object no Windows, nunca por algo que o agente possa forjar. O mesmo arquivo serve a todos os personagens de um perfil.

- **Lista:** no `tools/list`, o gateway pergunta ao motor e mostra só as ferramentas dos servidores ligados na classe, com o nome `<servidor>__<ferramenta>`; o CLI as chama `mcp__mestre__<servidor>__<ferramenta>`, e o motor as julga como `mcp__<servidor>__<ferramenta>`, o nome que a classe escreve em `tools`. Quando a classe muda, o gateway avisa o CLI (`notifications/tools/list_changed`).
- **Chamada:** cada `tools/call` vai antes ao motor, que aplica o [pipeline de permissões](permissoes.md#pipeline-de-decisão) do personagem, com a [cadeia](../orquestracao/cadeia-de-aprovacao.md) quando pede aprovação, e registra. O despachante deixa passar `mcp__mestre__*`, que o gateway já julga; assim a decisão é a mesma nos três provedores, vejam os hooks deles a ferramenta MCP ou não.
- **Servidor local** (`command`): o gateway o inicia como filho, a partir da pasta de instalação do motor, só quando o personagem o usa pela primeira vez, com o ambiente limpo (sem as variáveis do agente além de `PATH`, `HOME` e `MESTRE_TMP`), e o encerra junto com ele.
- **Servidor remoto** (`url`): quem chama é o motor da máquina, nunca o gateway. Com `auth`, ele pede a `mcp_key` à Cidadela no uso, guarda só na memória e só a manda a um endereço dentro dos domínios da chave. O valor nunca entra no ambiente, num arquivo ou na resposta que chega ao agente.

| Provedor | Como o `mestre` chega |
|---|---|
| Claude Code | sempre `--mcp-config` com um arquivo da pasta do personagem (`.mestre/agents/<id>/`) que só tem o `mestre`, e `--strict-mcp-config`, que ignora o `.mcp.json` do repositório e os do perfil ([Claude Code](../provedores/claude-code.md)) |
| Codex | `-c mcp_servers.mestre.command=…` e `args`, que só o motor passa ([Codex](../provedores/codex.md)); o `config.toml` do perfil fica sem servidores, e para cada servidor de um `.codex/config.toml` do repositório o motor passa `-c mcp_servers.<nome>.enabled=false` |
| Antigravity | `~/.gemini/config/mcp_config.json` do `HOME` do perfil, regravado pelo motor antes de cada início só com o `mestre`, como o `settings.json` ([reforço nativo](reforco-nativo.md), [Antigravity](../provedores/antigravity.md)) |

Os arquivos são gerados do zero e o caminho é conferido na forma canônica. Em todos, a camada do Mestre do personagem tem uma linha com os servidores dele.

## Regras e erros

- Servidor que não inicia (programa ausente, `url` fora): a chamada volta com erro que diz isso, e o Mestre da mesa é avisado; o `class check` mostra o servidor como indisponível na máquina. Motor fora do ar: o gateway responde erro a toda chamada (falha fechada).
- Remover um servidor do catálogo com classe que o declara: recusado, listando as classes.
- Servidor no catálogo com chave escrita no `command` ou na `url` (padrões de segredo do [cofre](../plataforma/cofre.md)), com `auth` sem `url`, ou cujo `url` sai dos domínios da `mcp_key`: recusado.

## Critérios de aceite

- **CA-MCP-01** · *rápida* — **Dado** um catálogo com `playwright` e `context7` e uma classe `qa` com só `playwright` ligado, **quando** o gateway de um QA atende `tools/list`, **então** mostra só `playwright__*`; **e** um `tools/call` de `context7__resolve` é negado e registrado.
- **CA-MCP-02** · *rápida* — **Dado** uma classe que declara o servidor `inexistente`, **quando** o `class check` roda, **então** aponta o erro e a classe não é salva.
- **CA-MCP-03** · *rápida* — **Dado** um Administrador que acrescenta ao catálogo um servidor chamado `browser` cujo `command` é um pacote de controle do navegador do usuário, **quando** salva, **então** é recusado com o motivo; **dado** um servidor com uma chave na `url`, ou com `auth` e `command`, **então** é recusado.
- **CA-MCP-04** · *rápida* — **Dado** um QA e um Revisor em cada provedor e um repositório com `.mcp.json`, `.codex/config.toml` com `[mcp_servers.x]`, **quando** o motor monta os inícios, **então** a configuração de cada um tem só o `mestre`, com o mesmo conteúdo; no Claude vai `--strict-mcp-config`, e no Codex `-c mcp_servers.x.enabled=false`.
- **CA-MCP-05** · *completa* — **Dado** um QA de cada provedor com `playwright` ligado e o agente falso, **quando** ele chama `mcp__mestre__playwright__browser_navigate` e depois `mcp__x__run` (servidor fora do gateway), **então** a primeira passa pelo pipeline e chega ao servidor, e a segunda é negada pelo despachante e registrada.
- **CA-MCP-06** · *rápida* — **Dado** um teto com `network.domains: ["github.com"]` e uma classe da mesa que liga `playwright` (`domains: ["*"]`), **quando** o `class check` roda, **então** recusa e cita os domínios; **dado** um servidor com `command: ["sh", "-c", …]`, **então** o catálogo recusa.
- **CA-MCP-07** · *rápida* — **Dado** `context7` com `auth` e um servidor remoto falso, **quando** um Dev chama uma ferramenta dele, **então** o pedido chega ao servidor falso com o cabeçalho e o valor, e o valor não aparece no ambiente do gateway, em arquivo da pasta do personagem nem na resposta.
- **CA-MCP-08** · *rápida* — **Dado** um processo `mestre mcp` iniciado fora do terminal de um personagem, **quando** ele pede `tools/list`, **então** o motor responde `not_identified` e o gateway não mostra ferramenta nenhuma.

## Depende de

- [Classes](classes.md)
- [Formato das permissões](formato-das-permissoes.md)
- [Permissões](permissoes.md)
- [Reforço nativo](reforco-nativo.md)
- [Cofre](../plataforma/cofre.md)
- [Canal local](../arquitetura/canal-local.md)
- [Despachante de hooks](../provedores/despachante-de-hooks.md)
- [Sessão zero do workspace](../sessao-zero/workspace.md)

## Verificar na implementação

- **Verificar na implementação:** que cada CLI inicia o `mestre` sem tela de aprovação e espera uma chamada que aguarda aprovação (o limite de tempo de ferramenta MCP de cada um). **Padrão:** sem tela; o motor responde antes do limite e, se a aprovação passar dele, devolve "aguardando aprovação, tente de novo" e a aprovação fica valendo para a próxima chamada igual. **Se falhar:** o adaptador responde a tela pelo Mestre, que é o autor do arquivo. **Onde:** [teste de classe](../provedores/teste-de-classe.md), com um caso que usa e um que proíbe um servidor, nos três provedores.
- **Verificar na implementação:** que o Antigravity não carrega um `.agents/mcp_config.json` do repositório, ou que o hook dele vê as ferramentas desses servidores. **Padrão:** o hook vê, e o despachante nega. **Se falhar:** a mesa cujo repositório traz esse arquivo não inicia personagem Antigravity, e o Mestre da mesa é avisado. **Onde:** o mesmo caso, no Antigravity.
- **Verificar na implementação:** o limite de tamanho do nome de ferramenta em cada provedor (`mcp__mestre__<servidor>__<ferramenta>`). **Padrão:** cabe em 64 caracteres. **Se falhar:** o gateway encurta o nome com um sufixo de hash e guarda a tabela, e o motor julga pelo nome original. **Onde:** teste rápido do gateway com o nome mais longo do catálogo.

## A confirmar

- **Servidor local com chave:** ele roda como `mestre-agente`, e o agente poderia ler o valor. O caminho é rodá-lo numa conta própria (`mestre-mcp`), sem acesso ao worktree, falando só com o gateway; até lá, só a versão remota do servidor (com `auth`) entra no catálogo.
