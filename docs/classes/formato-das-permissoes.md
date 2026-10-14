# Formato das permissões

> **Assunto:** formato do `permissions.yaml` (o mesmo do teto do workspace e das restrições da ficha), padrões de arquivos, comandos e ferramentas e como o despachante os casa · **Componente:** Todos · **Atualizado:** 2026-10-14

Documento de referência das [permissões](permissoes.md), que são donas do comportamento, das regras fixas, das regras efetivas e dos critérios de aceite. O código é `internal/classes` (`ParsePermissions`).

## O arquivo

```yaml
version: 1
files:                      # globs relativos à raiz do worktree
  read: ["**"]              # padrão: todo o worktree
  write: ["tests/**", "reports/**"]
  deny: ["**/.env", "secrets/**"]     # nem ler nem escrever
commands:                   # padrões sobre cada comando simples; * casa qualquer texto
  allow: ["npm test", "npm run test:*", "git add *", "git commit *"]
  ask: ["npm install *"]    # exige aprovação
  deny: ["curl *", "rm -rf *"]
  default: ask              # comando que não casa com nada: allow | ask | deny
git:
  commit: true
  branches: ["{mesa.branch}"]         # onde pode commitar
  request_push: false       # pode pedir `mestre pr open|update` (o envio é do motor)
  workflows: never          # enviar mudanças em .github/workflows/: never | ask | allow
network:
  internet: false
  domains: []               # internet: true e lista vazia = qualquer domínio
  credentials: []           # credenciais de site do cofre (nome); o domínio precisa estar em domains, e a classe, na lista da credencial
cli:
  allow: ["issue comment"]  # comandos de GitHub e coordenação que o nível permite
tools:                      # ferramentas de tipo mcp, subagent e other, pelo nome nativo; * casa qualquer texto
  allow: ["mcp__playwright__*"]       # ex.: o MCP de navegador do personagem
  ask: []
  deny: ["mcp__*__delete_*"]
  default: ask              # ferramenta que não casa com nada: allow | ask | deny
operator: false             # pode ser usada na base Operador do PC
```

- **Leitura estrita:** `version` é obrigatório e vale 1; campo desconhecido, valor fora das listas (`allow | ask | deny`, `never | ask | allow`), glob malformado, segmento `..`, caminho absoluto em `write`, domínio que não é um nome em minúsculas (com `*.` opcional) ou mais de um documento YAML tornam o arquivo inválido ([regras e erros](permissoes.md#regras-e-erros)).
- **Onde fica cada `default`:** `commands.default` e `tools.default`, dentro do bloco de cada um, como nas [regras efetivas](permissoes.md#regras-efetivas); `default` no topo do arquivo é campo desconhecido e torna o arquivo inválido.
- **Campo ausente:** numa classe vale o padrão: `read: ["**"]`, nada em `write`, `default: ask` em `commands` e em `tools` (o que nenhuma regra cobre vira pedido de aprovação, que a [cadeia](../orquestracao/cadeia-de-aprovacao.md) decide perto de quem pediu; [ADR 0024](../decisoes/0024-cadeia-de-aprovacao-por-risco.md)), `git.commit`, `request_push`, `internet` e `operator` falsos, `workflows: never`. Classe que quer negar o resto escreve `default: deny`. No teto e na ficha, campo ausente não limita nada ([teto](teto-do-workspace.md)).
- **`default` ausente e as outras camadas:** é o padrão do sistema, não uma concessão da classe: não conta na conferência da classe contra o teto ao salvar, e o corte ao montar as regras efetivas fica com o mais restritivo (um teto com `commands.default: deny` deixa `deny`, com `origin` `ceiling`). No reforço nativo, `ask` não vira regra e não tira ferramentas da lista do Antigravity ([reforço nativo](reforco-nativo.md)); em modo degradado, `ask` nega com "motor indisponível", como sempre ([modo degradado](permissoes.md#modo-degradado)).
- **Marcadores:** só em `git.branches`, e o único é `{mesa.branch}`; a Cidadela resolve ao montar as [regras efetivas](permissoes.md#regras-efetivas), e marcador sem valor impede o documento.

## Arquivos

- Valem para as ferramentas de ler, escrever e editar, para caminhos literais em argumentos de comandos e para redirecionamentos (`>`, `>>`). Leitura fora do worktree é negada, a não ser por caminho absoluto listado em `read`.
- Globs: `**` casa zero ou mais pastas, `*`, `?` e `[…]` casam dentro de um nome; arquivos com ponto contam como os outros. Os caminhos são comparados já normalizados ([adaptador](../provedores/adaptador.md#formato-interno-normalizado-de-evento)); no Windows, sem diferença de maiúsculas, sem os prefixos `\\?\` e sem os pontos e espaços finais e os fluxos alternativos (`::$DATA`) que o sistema ignora.
- A pasta `MESTRE_TMP` do próprio personagem é dele: ler e escrever lá não depende de `files`.
- Um caminho com curinga num comando é julgado pela pasta antes do curinga; se o curinga pode alcançar um arquivo protegido (`.git`, `.mestre`…), a regra fixa nega.

## Comandos

- A linha é dividida em comandos simples (`&&`, `||`, `;`, `|`, `&`, quebra de linha, parênteses e chaves); prefixos `VAR=valor` são ignorados, menos os que mudam o programa que roda (`PATH`, `LD_PRELOAD` e afins), que deixam a parte no mínimo em `ask`, e os que apontam o git para outro repositório (`GIT_DIR` e afins), que a regra fixa nega; cada parte é avaliada e vale a mais restritiva. Trechos que não dá para analisar (`$(…)`, crases, variáveis como `$X`, here-documents, laços, `eval`, `sh -c`, `bash -c`, `powershell -Command`, `cmd /c`) ficam no mínimo em `ask`; o texto de uma substituição ou de um shell aninhado é analisado também, e uma regra fixa nele nega. No Windows a linha é lida como POSIX e como PowerShell, e vale a mais restritiva.
- Comandos só de leitura (`cd`, `pwd`, `ls`, `cat`, `head`, `tail`, `grep`, `rg`, `find`, `wc`, `git status|diff|log|show` e equivalentes do PowerShell) são liberados por padrão, ainda sujeitos a `files.deny` e a `commands.deny`, quando chamados pelo nome (`tests/ls` pode ser qualquer coisa); `find` com `-exec` ou `-delete`, `git -c` e `git diff --output` não são só leitura, e `GIT_PAGER`, `EDITOR` e afins contam entre as variáveis que mudam o programa.
- **Casamento:** `allow` vê o comando como escrito, sem os invólucros que não mudam o programa (`env`, `nice`, `nohup`, `timeout`, `time`, `command`, `exec`); `deny` e `ask` veem também o nome do programa sem a pasta e a extensão (`/usr/bin/curl` casa `curl *`) e a linha com os invólucros. Um padrão terminado em ` *` casa também o comando sem argumentos (`curl *` casa `curl`). `xargs` e `find -exec` têm o comando analisado, no mínimo em `ask`.
- Depois de `cd x &&`, numa linha sem subshell nem pipe, o resto roda em `x`; nos outros casos os caminhos relativos são conferidos nas duas pastas.
- A CLI `mestre` passa pelo despachante sem precisar de `allow` (o motor confere cada chamada; [CLI](#cli-mestre)), ainda sujeita a `commands.deny`.

## Git, rede e ferramentas

- **Git:** `git commit` só com `commit: true`, na branch que casa com `branches` e com todos os arquivos que o commit leva dentro de `files.write`: os preparados (`git diff --cached --name-only`) e, com `-a`, `-i`, `-o` ou caminhos, também os alterados. Trocar de branch só para uma que casa com `branches`. `merge`, `rebase`, `cherry-pick`, `revert` e `am` criam commits e exigem `commit: true`.
- **Rede:** vale para as ferramentas web (domínio da URL; `*.exemplo.com` casa os subdomínios, não o próprio domínio) e é reforçada pelo sandbox nativo; comandos de shell só são contidos pelo controle nativo. Com `domains` não vazio, ferramenta web sem URL (busca) é negada.
- **Ferramentas (`tools`):** padrões sobre o nome nativo das ferramentas de tipo `mcp`, `subagent` e `other` ([mapa de ferramentas](../provedores/adaptador.md#ferramentas-nome-nativo--tipo-toolkind)), com a mesma regra de casamento de `commands`; vale a mais restritiva entre `deny`, `ask` e `allow`, e o que não casa cai em `tools.default`. As de tipo `internal` (planos e tarefas do próprio provedor) não passam por regra. **Navegação web:** cada personagem que navega tem o **próprio** MCP de navegador (ex.: Playwright MCP com `--headless --isolated`), configurado no perfil de conta ([bases e fichas](../orquestracao/bases-e-fichas.md)) e iniciado pelo CLI do personagem, portanto como `mestre-agente`, com navegador limpo: perfil de navegador só em memória, sem acesso às sessões, cookies e perfis de navegador do usuário ([segurança](../arquitetura/seguranca.md#navegação-web-e-login-em-sites)). A classe libera ou nega as ferramentas dele aqui (ex.: `mcp__playwright__*`, ou `mcp__*` para todas). Ferramentas MCP não passam por `network`: classe sem internet não deve liberar um MCP de navegador. **Login em sites:** só com credencial do cofre listada em `network.credentials` e que liste a classe nos metadados dela; o personagem recebe o estado de sessão, nunca a senha ([cofre](../plataforma/cofre.md#login-em-sites)).
- **Operador do PC (`operator`):** `true` deixa a classe ser usada na base especial Operador do PC, que roda com a conta do usuário ([Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc)); só vale se o [teto](teto-do-workspace.md) também tiver `operator: true`. Padrão `false`. Num terminal do Operador, classe sem `operator` tem toda ferramenta negada.

## CLI `mestre`

Comandos de comunicação são de todos; os demais seguem o nível de quem chama ([convenções da CLI](../cli/convencoes.md)) e `cli.allow`; `git.request_push` cobre `mestre pr open|update`. `git.workflows` decide o envio de branches que alteram `.github/workflows/` (o CI do repositório): `never` (padrão) o motor recusa; `ask` cada envio vira pedido de aprovação `envio_workflows` a um usuário ([política](../aprovacoes/politica.md)); `allow` envia sem perguntar. Classes `ask` e `allow` precisam de um App com a permissão "Workflows" ([workflows](../github/identidades-e-apps.md#workflows)). A checagem acontece no motor, na hora da chamada. O que cada nível pode fazer no GitHub está em [ações por nível](../github/acoes-por-nivel.md).
