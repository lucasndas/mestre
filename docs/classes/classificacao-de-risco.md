# Classificação de risco

> **Assunto:** o risco de cada pedido de aprovação (`baixo`, `medio`, `alto`, `fixo`), dado pelo motor a partir do tipo da ação e do alvo: tabela padrão, sinais de risco, risco dos tipos de pedido, ajustes do Dono no arquivo de política, padrões aprováveis e o que eles cobrem · **Componente:** Motor · **Atualizado:** 2026-10-14

## Objetivo

Dar a cada pedido de aprovação um **risco**, que diz até onde ele pode ser decidido na cadeia de aprovação ([ADR 0024](../decisoes/0024-cadeia-de-aprovacao-por-risco.md)). Quem classifica é sempre o motor, de forma determinística, pela ação e pelo alvo; nunca um agente, e nunca pelo texto com que o agente justifica o pedido. Este documento é a **fonte única** da tabela de risco.

## Comportamento

### Onde a classificação entra

- As regras decidem primeiro: regras fixas, exceções e regras efetivas ([pipeline](permissoes.md#pipeline-de-decisão)). Só o que termina em `ask` é classificado; o que as regras fixas negam nunca vira pedido. O `ask` que vem só de nenhuma regra cobrir a ação e sai `baixo` roda direto, sem pedido ([ADR 0025](../decisoes/0025-padroes-seguem-os-provedores.md)); o resto vira pedido.
- O risco é calculado uma vez, quando o pedido nasce, e fica gravado nele; subir na cadeia não o muda. Alvo que muda é pedido novo, classificado de novo.
- **Entradas:** o tipo do pedido; para `permissao_de_classe`, a ação normalizada do [adaptador](../provedores/adaptador.md#formato-interno-normalizado-de-evento) (tipo de ferramenta, comando, caminhos, URL, nome nativo), a raiz do worktree e os domínios das regras efetivas; os ajustes do Dono. A justificativa do agente não entra.
- **Saída:** o risco, a versão da tabela padrão (`risk_table_version`, embutida no binário) e a linha ou o ajuste que decidiu; os três vão para o pedido e para a auditoria.
- Quem decide cada risco, e como o pedido sobe, está na [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md) (M4).
- O código é `internal/classes` (`ClassifyRisk`, `ParseRiskAdjustments`, `CheckApprovablePattern`, `PatternCovers`). No M3 os ajustes entram como dado; o arquivo de política que os carrega chega no M5 e usa `LoadRiskAdjustments` (inválido: fica a versão anterior). O `ask` do pipeline sai já classificado (`Decision.Risk`), e o despachante abre o pedido com ele (`ApprovalRequest.Risk`); em modo degradado não há pedido nem classificação.

### Os quatro riscos

| Risco | O que é | Exemplos |
|---|---|---|
| `baixo` | ação local e reversível, dentro do worktree, sem sinal de risco | instalar um pacote do projeto, rodar um script do projeto, copiar um arquivo, `echo`, `jq` |
| `medio` | efeito fora do worktree, rede para domínio não listado, apagar, instalação global, contêiner ou VM, código que o motor não lê | baixar de um domínio fora da lista, apagar uma pasta, instalar algo global, subir contêineres, `python -c` |
| `alto` | efeito externo difícil de desfazer, segredos, dados, as regras do próprio Mestre, novos agentes | ações no GitHub, publicar pacote, ler `.env`, `DROP TABLE`, mexer em `.mestre/`, iniciar outro CLI de agente |
| `fixo` | o que uma regra fixa ou um tipo de pedido de regra fixa já cobre | `git push`, cofre, pedidos da Guilda, ativação do Operador do PC |

### Comandos de shell

- A linha é dividida em comandos simples como no [formato das permissões](formato-das-permissoes.md#comandos) (mesmos separadores, invólucros e prefixos); cada parte é classificada e o pedido fica com o **maior** risco entre as partes.
- Cada parte é comparada com a tabela abaixo na ordem `alto` → `medio` → `baixo`, com o casamento de `deny` (nome do programa sem pasta e extensão, com e sem invólucros, sem diferença de maiúsculas no SQL); vale a primeira linha que casa. **Parte que não casa nenhuma linha é `baixo` se não mostra nenhum [sinal de risco](#sinais-de-risco); com sinal, vale o do sinal.**
- Trecho que não dá para analisar (`$(…)`, crases, variáveis, laços, here-documents, `sh -c` e afins, a expansão de chaves, `busybox` sem programa) é no mínimo `medio`; o texto interno, quando legível, é classificado também e vale o maior. `eval` e shell aninhado de texto ilegível são `alto`: `eval` e `Invoke-Expression` sempre; o shell aninhado quando lê o script da entrada (`curl … | sh`) ou quando o texto começa por uma expansão (`sh -c "$CMD"`).
- `cd` e os comandos só de leitura do [formato](formato-das-permissoes.md#comandos) chamados pelo nome, sem linha na tabela, são `baixo`: valem pelos caminhos que leem (`cat config/.env.local` é `alto`). Assim também `echo`, `printf`, `test`, `which`, `jq` e os scripts do projeto (`./scripts/build.sh`) sem sinal; script do projeto é o que veio do repositório ([sinais](#sinais-de-risco)).
- `rm` com operando que o motor não resolve (variável, pasta desconhecida) conta como fora do worktree; as linhas `alto` e `medio` de `rm` valem também para os equivalentes do PowerShell (`Remove-Item`, `del`, `rd`…). "Toda URL literal" pede ao menos uma URL; as do GitHub incluem os subdomínios de `github.com` e a forma `git@github.com:`.
- **Caminhos literais**, já normalizados (inclusive `..` e links resolvidos pelo adaptador): fora do worktree, a parte sobe a no mínimo `medio`; um caminho de segredo (`areas_sensiveis.permissoes_e_segredos`) ou do Mestre (`areas_sensiveis.mestre`), com os globs do [arquivo de política](../aprovacoes/arquivo-de-politica.md) (até o M5, os do conteúdo padrão), sobe a `alto`.

| Risco | Tabela padrão (padrões de comando) |
|---|---|
| `alto` | GitHub e publicação: `gh *`, `hub *`, `npm publish*`, `pnpm publish*`, `yarn publish*`, `yarn npm publish*`, `cargo publish*`, `twine upload*`, `gem push*`, `docker push*`, `podman push*`, e qualquer comando com URL de `github.com`, `api.github.com` ou `*.githubusercontent.com` |
| `alto` | segredos e acesso remoto: `printenv*`, `env` sem comando, `set` sem argumentos, `ssh*`, `scp*`, `sftp*`, `rsync *:*`, `gpg*`, `aws *`, `gcloud *`, `az *`, `kubectl *`, `helm *`, `terraform *`, `pulumi *`, `git credential*`, `npm login*`, `npm adduser*`, `npm token*`, `secret-tool *`, `cmdkey*`, `vault *` |
| `alto` | apagar dados: `*DROP *`, `*TRUNCATE*`, `*DELETE FROM*`, `dropdb*`, `redis-cli flushall*`, `docker volume rm*`, `docker system prune*`, `rm` com caminho fora do worktree |
| `alto` | novos agentes: `aider*`, `gemini*`, `cursor-agent*`, `opencode*`, `goose*`, `amp *`, `ollama run*` (o `claude`, o `codex` e o `agy` já são regra fixa) |
| `medio` | rede: `curl*`, `wget*`, `http *`, `Invoke-WebRequest*`, `iwr *`, `nc *`, `ncat *`, `telnet *`; `baixo` quando toda URL literal é de um domínio das regras efetivas (e não do GitHub) |
| `medio` | apagar em massa no worktree: `rm` com `-r`, `-R`, `--recursive`, curinga ou mais de 20 caminhos (padrão); `git clean*`, `git reset --hard*`, `git checkout -- *`, `git restore *`, `find * -delete*` |
| `medio` | fora do worktree e configuração: `npm install -g*`, `npm i -g*`, `* --global*`, `pip install*`, `pip3 install*` (instalam fora do worktree), `go install*`, `cargo install*`, `npx *`, `pnpm dlx *`, `bunx *`, `brew *`, `winget *`, `choco *`, `scoop *`, `npm config *`, `npm pkg set*`, `npm exec*`, `npm x *`, `npm link*`, `yarn dlx *`, `yarn global *`, `bun x *`, `deno install*` |
| `medio` | envio da branch da mesa: `mestre pr open*`, `mestre pr update*` (o pedido da classe sem `git.request_push`, [formato](formato-das-permissoes.md#cli-mestre)) |
| `medio` | contêineres e serviços: `docker *`, `podman *`, `docker-compose *`, `systemctl *`, `service *`, `launchctl *`, `sc *`, `Stop-Service*`, `Start-Service*` |
| `baixo` | pacotes do projeto: `npm install*`, `npm i *`, `npm ci*`, `npm uninstall*`, `npm update*`, `pnpm install*`, `pnpm add*`, `pnpm remove*`, `yarn`, `yarn install*`, `yarn add*`, `yarn remove*`, `bun install*`, `bun add*`, `bun remove*`, `uv pip install*`, `poetry install*`, `poetry add*`, `uv sync*`, `uv add*`, `go mod *`, `go get *`, `cargo add*`, `cargo fetch*`, `bundle install*`, `composer install*`, `composer require*` |
| `baixo` | scripts e ferramentas do projeto: `npm run *`, `npm test*`, `pnpm run *`, `pnpm test*`, `yarn run *`, `yarn test*`, `bun run *`, `make *`, `just *`, `go build*`, `go test*`, `go vet*`, `go run *`, `go generate*`, `cargo build*`, `cargo test*`, `cargo check*`, `cargo run*`, `cargo clippy*`, `cargo fmt*`, `pytest*`, `tsc*`, `eslint*`, `prettier*`, `./gradlew *`, `gradle *`, `mvn *`, `dotnet build*`, `dotnet test*` (`make *` e `npm run *` ficam `baixo` por decisão do Dono, embora o script possa fazer qualquer coisa); um interpretador (`python`, `python3`, `node`, `bash`, `sh`, `pwsh`, `ruby`) cujo primeiro argumento é um arquivo do worktree vindo do repositório (o [sinal](#sinais-de-risco) de script alterado na mesa vale por cima) |
| `baixo` | arquivos dentro do worktree: `mkdir *`, `touch *`, `cp *`, `mv *`, `ln -s *`, `chmod *`, e `rm` sem `-r`, sem curinga e com até 20 caminhos |

### Sinais de risco

O que leva a `medio` (ou mais) uma parte sem linha na tabela, que sem sinal seria `baixo` (`RiskTableVersion` 3):
- **rede:** uma URL ou um host (`usuário@host:`, IP, `localhost`) de domínio fora do `network.domains` das regras efetivas (qualquer domínio, o padrão, não lista nenhum), um programa de rede (`ping`, `dig`, `ftp`, `socat`, `nmap`, `Invoke-RestMethod`…) ou `git clone`, `git ls-remote`, `git submodule`, `git send-email`;
- **apagar:** os equivalentes de `rm` (`unlink`, `shred`, `rmdir`, `Remove-Item`, `del`…), `git rm` e as opções `--delete`;
- **instalação ou configuração do sistema:** gerenciadores do sistema e de versões (`apt`, `dnf`, `pacman`, `pip`, `gem`, `rustup`, `nvm`…), `-g`, `--global`, `--location=global` e `--system` de `npm`, `pnpm`, `yarn`, `bun` e `uv`, e o que mexe na máquina (`kill`, `crontab`, `reg`, `setx`, `mount`, `chown`, `mkfs*`…);
- **contêiner ou VM:** `nerdctl`, `buildah`, `vagrant`, `virsh`, `qemu*`, `multipass`, `lxc`, `minikube`… (`docker` e `podman` já são linha);
- **interpretador com código:** `python`, `node`, `ruby`, `perl`, `php`, `deno`, `bun`, `source` e afins sem um arquivo do worktree como primeiro argumento (`python -c`, `node -e`, `python -m`), `deno eval`; os shells com `-c` são shell aninhado (acima);
- **caminho:** um caminho literal fora do worktree, também depois de `=` (`DESTDIR=../fora`), sobe a `medio`; de segredo ou do Mestre, a `alto`; valem por cima de toda parte, como o mínimo `medio` do que não se analisa (`eval` é `alto`);
- **leitura do shell:** `alias`, `function`, `trap`, `shopt` e afins, e `export` ou `declare` de uma variável que muda o programa;
- **escrita sem caminho visível:** o que escreve arquivos que a linha não nomeia, e que as regras fixas e o bloco `files` não veem: extrair um arquivo compactado (`tar -x`, `unzip` que não só lista ou testa, `7z x`, `unrar x`, `cpio -i`, `pax -r`, `ar x`, `jar x`, `Expand-Archive`) e aplicar um patch (`patch`, `git apply`, `git am`);
- **script alterado na mesa:** o script do worktree que a parte roda (o programa dado por caminho, `./scripts/x.sh`, ou o primeiro argumento de um interpretador ou shell, `python x.py`, `bash x.sh`, `source x.sh`) só é "do projeto" se veio do repositório: conteúdo igual, a menos dos fins de linha, ao do mesmo caminho na **base da branch da mesa**, o merge-base dela com a branch principal (em cada base, se a branch trouxe a principal por merge). Criado na mesa (rastreado, não rastreado ou ignorado), alterado no worktree ou em commit da branch, link para fora do worktree, arquivo que não é regular ou acima de 1 MiB: `medio`; sem o estado do git, todo script do worktree é `medio`. Vale por cima das linhas e dos ajustes, como os caminhos; `make *` e `npm run *` seguem a linha.

A rede para domínio não listado e a instalação global valem também por cima das linhas `baixo` (`npm install x -g` e `npm install https://evil.example/x.tgz` são `medio`), não por cima de um ajuste do Dono. A lista exata fica no código (`internal/classes`, `risksignal.go`).

### Outras ferramentas

Só `shell`, `web`, `mcp`, `subagent` e `other` chegam a `ask` (arquivos não têm `ask`, [formato](formato-das-permissoes.md#o-arquivo)):
- `web`: URL de domínio das regras efetivas → `baixo`; do GitHub → `alto`; outra → `medio`;
- `mcp` e `other`, pelo nome nativo: `mcp__github__*` → `alto`; nome com `delete`, `drop`, `destroy`, `purge` ou `truncate` → `alto`; o resto → `medio`;
- `subagent` → `baixo` (o subagente é o próprio personagem e herda as regras dele, [permissões](permissoes.md#subagentes-nativos); sem regra, roda direto).

### Risco dos tipos de pedido

| Tipo ([tipos de ação](../aprovacoes/politica.md#tipos-de-ação)) | Risco |
|---|---|
| `permissao_de_classe` | o da ação, pelas tabelas acima |
| `ponto_de_missao`, `pr`, `merge` | `medio` (com as condições do motor da política) |
| `custo_extra` | `medio` dentro do teto de custo; acima dele, `alto` sem delegação possível: só um Administrador ou acima ([teto](teto-do-workspace.md#onde-o-teto-age)) |
| `envio_workflows`, `configuracao_repositorio`, `classe_workspace`, `acao_destrutiva`, `proposta_agents_md` | `alto` |
| `ativacao_operador`, `atualizacao_mestre`, `entrada_na_guilda`, `promocao_cidadela` | `fixo`: fora da cadeia, com a regra própria do tipo na política |
| tipo desconhecido | `alto` |

### Ajustes do Dono

Na seção `risco` do [arquivo de política](../aprovacoes/arquivo-de-politica.md), editado só pelo Dono:

```yaml
risco:
  ajustes:                       # valem antes da tabela padrão, na ordem do arquivo
    - { comando: "make deploy-teste", risco: alto }
    - { comando: "docker compose up*", risco: baixo }
    - { ferramenta: "mcp__db__query", risco: alto }
  nivel_minimo: { alto: administrador }   # nível mínimo das pessoas por risco
```

- `comando` casa como os padrões de `commands`; `ferramenta`, como os de `tools`. O primeiro ajuste que casa decide a parte; sem ajuste, vale a tabela padrão. O ajuste toma o lugar da linha: a regra de caminhos e o mínimo `medio` do que não se analisa continuam valendo por cima.
- Cada item tem `risco` e só um de `comando` e `ferramenta`; qualquer outra chave (inclusive `tipo`: o risco dos tipos não se ajusta) é inválida.
- **Válidos:** subir qualquer padrão; descer só de `medio` para `baixo` (e voltar).
- **Inválidos** (o conjunto inteiro é recusado com o item e o motivo; com o arquivo, vale a versão anterior, como na [política](../aprovacoes/politica.md)):
  1. `risco: fixo`, ou um padrão que casa ação de regra fixa (`git push*`) ou um tipo `fixo`: o `fixo` nunca muda;
  2. descer algo que a tabela padrão dá como `alto`, ou um padrão que pode casar um comando `alto` (conferência conservadora, como a do teto: `docker *` para `baixo` alcança `docker push`): o `alto` só se abre por delegação do Dono ([delegação](../aprovacoes/delegacao.md));
  3. `*` sozinho ou um padrão de interpretador com curinga (abaixo);
  4. qualquer chave que mude até onde cada nível decide: o formato não a tem, e o teto de risco dos agentes é fixo.
- `nivel_minimo`: o nível mínimo das pessoas para cada risco (padrão: `alto: administrador`); vale o maior entre o do tipo e o do risco, e só sobe: abaixo do padrão é inválido.
- As conferências 1 e 2 usam o **alcance** do padrão (abaixo). Casa ação de regra fixa o padrão cujo texto literal a regra fixa nega ou que alcança um subcomando git que a regra fixa nega, inteiro ou com alguma opção (`git push`, `git remote`, `git branch`, `git checkout -b`, `git reflog expire`…: a lista é a das [regras fixas](permissoes.md#regras-fixas-de-segurança), `gitFixed` no código), `claude`, `codex`, `agy` ou um comando de elevação; para `ferramenta`, os MCPs de controle do computador. Um padrão de `ferramenta` com curinga alcança `alto` (pode nomear `mcp__github__*` ou uma ferramenta com `delete`): só sobe.

### Padrões aprováveis

As respostas que aprovam um padrão (ex.: `npm install *`) e as sugestões de regra usam um **padrão** como alvo ([respostas](../orquestracao/cadeia-de-aprovacao.md#respostas)):
- é um alvo próprio, com impressão própria: SHA-256 do JSON canônico com padrão, tipo de ferramenta, classe e mesa;
- precisa casar a ação do pedido de origem e começar por um programa literal;
- o risco do padrão é o do **alcance** (abaixo): o maior que o programa e os subcomandos que ele pode casar dão (`npm install *` é `baixo`; `npm *` alcança `npm publish`, logo `alto`);
- **o que ele cobre:** um padrão aprovado (ou a regra temporária feita dele) só libera um comando que ele casa **e** cuja classificação própria cabe no risco dele (`PatternCovers`): `npm install *` aprovado como `baixo` não libera `npm install -g x` nem `npm install x && curl https://evil.example`, que são `medio` e seguem como pedido;
- nunca um interpretador com curinga: `sh *`, `bash *`, `zsh *`, `pwsh *`, `powershell *`, `cmd *`, `python *`, `python3 *`, `node *`, `deno *`, `bun *`, `ruby *`, `perl *`, `php *`, `env *`, `xargs *`, `npx *`, nem `*`: recusados com `pattern_too_broad`. Recusado é o curinga que pode ocupar o lugar do código ou do script (`python *`, `python -c *`, `node*`); depois de um script ou subcomando literal (`node scripts/build.js *`, `bun install *`), aceito; em `env`, `xargs`, `cmd`, `pwsh` e `powershell`, qualquer curinga. Padrão que não casa o pedido: `pattern_no_match`.
- **Alcance**, para o risco do padrão: o maior entre a classificação do texto antes do primeiro curinga (com os sinais do programa); a primeira linha da tabela que cobre o padrão inteiro (`npm install *` está em `npm install*`); as linhas e os subcomandos de sinal que o curinga alcança pelo programa e pelos subcomandos, não pelas opções (`npm *` alcança `npm publish*`, `git *` alcança `git rm`; `npm install *` não alcança `npm install -g*`); e as condições do programa (`rm *` alcança `rm` fora do worktree). O conteúdo dos argumentos (`*DROP *`, `* --global*`, URL, caminhos) não entra no alcance: cada comando continua classificado inteiro, e o padrão só o cobre se couber.
- A impressão da ação exata do pedido é `ClassPermissionFingerprint` (personagem, ação, pasta).

## Regras e erros

- A classificação nunca falha para baixo: o que não se analisa sobe (`medio` ou `alto`); o que não tem linha só é `baixo` sem nenhum sinal de risco.
- Mesma ação, mesma versão da tabela e mesmos ajustes dão sempre o mesmo risco.
- Ajuste inválido nunca é aplicado em parte.

## Critérios de aceite

- **CA-RISCO-01** · *rápida* — **Dado** o worktree `/w` e regras efetivas sem domínios, **quando** o motor classifica `npm install lodash`, `npm run build` e `make test`, **então** os três são `baixo`; `npm install -g x`, `rm -r build dist` e `docker compose up` são `medio`; `npm publish`, `gh pr create` e `psql -c "DROP TABLE users"` são `alto`.
- **CA-RISCO-02** · *rápida* — **Dado** os comandos `npm test && npm publish`, `npm install && echo $(curl https://x.io)`, `eval "$CMD"` e `echo ok && foo https://evil.example`, **quando** o motor classifica, **então** saem `alto` (a parte mais alta), `medio`, `alto` e `medio` (a parte sem linha na tabela, com o sinal de rede).
- **CA-RISCO-03** · *rápida* — **Dado** o worktree `/w`, **quando** o motor classifica `cp a.txt b.txt`, `cp a.txt ../fora/`, `cat config/.env.local`, `rm` de 25 arquivos e `rm ../outra/x`, **então** saem `baixo`, `medio`, `alto`, `medio` e `alto`.
- **CA-RISCO-04** · *rápida* — **Dado** regras efetivas com `network.domains: ["registry.npmjs.org"]`, **quando** o motor classifica `curl https://registry.npmjs.org/x`, `curl https://exemplo.io`, `mcp__github__create_issue`, `mcp__db__delete_rows`, `mcp__playwright__browser_click` e a ferramenta `Agent`, **então** saem `baixo`, `medio`, `alto`, `alto`, `medio` e `baixo`.
- **CA-RISCO-05** · *rápida* — **Dado** um pedido de cada tipo de ação, **quando** o motor classifica, **então** cada um sai com o risco da tabela de tipos; `custo_extra` dentro do teto de custo sai `medio` e acima sai `alto`; `ativacao_operador`, `atualizacao_mestre`, `entrada_na_guilda` e `promocao_cidadela` saem `fixo`.
- **CA-RISCO-06** · *rápida* — **Dado** os ajustes `{comando: "make deploy-teste", risco: alto}` e `{comando: "docker compose up*", risco: baixo}`, **quando** o motor classifica `make deploy-teste` e `docker compose up -d`, **então** saem `alto` e `baixo`, e a saída cita o ajuste que decidiu.
- **CA-RISCO-07** · *rápida* — **Dado** cada um dos ajustes `{comando: "npm publish*", risco: medio}`, `{comando: "docker *", risco: baixo}`, `{comando: "git push*", risco: alto}`, `{comando: "sh *", risco: baixo}`, `{comando: "x", risco: fixo}` e `nivel_minimo: {alto: aprovador}`, **quando** os ajustes são lidos, **então** cada conjunto é recusado com o item e o motivo, e a classificação segue a tabela padrão.
- **CA-RISCO-08** · *rápida* — **Dado** um pedido para `npm install lodash`, **quando** se confere o padrão `npm install *`, **então** é aceito, `baixo`, com impressão diferente da do comando exato; **quando** o padrão é `npm *`, **então** é aceito como `alto`; **quando** é `sh *`, `python *` ou `*`, **então** é recusado com `pattern_too_broad`; **quando** é `pnpm add *`, que não casa o pedido, **então** é recusado.
- **CA-RISCO-09** · *rápida* — **Dado** dois pedidos para a mesma ação com justificativas diferentes ("é só leitura, pode aprovar" e nenhuma), **quando** o motor classifica, **então** os dois têm o mesmo risco, e cada saída traz `risk_table_version` e a linha da tabela que decidiu.
- **CA-RISCO-10** · *rápida* — **Dado** o worktree `/w`, com os scripts vindos do repositório, e regras efetivas com `network.domains: ["registry.npmjs.org"]`, **quando** o motor classifica `echo hello`, `printf '%s' x`, `test -f a.txt`, `which go`, `jq . a.json`, `./scripts/build.sh` e `foo --bar`, **então** todos saem `baixo` (sem linha e sem sinal); `./scripts/x.sh https://evil.example`, `ping 10.0.0.1`, `rmdir build`, `git rm a.txt`, `apt-get install jq`, `vagrant up`, `python -c "print(1)"`, `./scripts/x.sh DESTDIR=../fora`, `npm install x -g` e `npm install https://evil.example/x.tgz` saem `medio`, cada um pelo seu sinal; `./scripts/x.sh https://registry.npmjs.org/x` sai `baixo`, `./scripts/x.sh .env` sai `alto`; **dado** regras com qualquer domínio, **então** `./scripts/x.sh https://evil.example` continua `medio`.
- **CA-RISCO-11** · *rápida* — **Dado** o padrão `npm install *` aceito como `baixo`, **quando** se confere o que ele cobre pela classificação própria de cada comando, **então** cobre `npm install lodash` e não cobre `npm install -g x`, `npm install x -g` nem `npm install x && curl https://evil.example` (`medio`), nem `pnpm add x` (não casa), nem uma ferramenta.

## Depende de

- [Adaptador](../provedores/adaptador.md) (ação normalizada e mapa de ferramentas)
- [Formato das permissões](formato-das-permissoes.md) (divisão e casamento dos comandos) · [Arquivo de política](../aprovacoes/arquivo-de-politica.md) (formato da seção `risco`) · [Precedência](../arquitetura/precedencia.md)
