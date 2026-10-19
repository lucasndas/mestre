# Preparar a máquina de desenvolvimento

> **Assunto:** o que instalar para desenvolver o Mestre, em que versão e como conferir, por marco; como rodar as suítes e a verificação; gancho de pré-commit; CI e proteção da `main` · **Componente:** Projeto · **Atualizado:** 2026-10-19

Referência para quem vai desenvolver (pessoa ou agente) numa máquina Linux. Instale só o que o marco em andamento pede ([ordem de implementação](ordem-de-implementacao.md)).

## O que instalar

| Desde | Ferramenta | Versão | Conferir com |
|---|---|---|---|
| M0 | Go, em `~/.local/go` | 1.27.2 (a `toolchain` do `go.mod`) | `go version` |
| M0 | git | a versão mínima de segurança do [ADR 0001](../decisoes/0001-stack-go.md) (por sistema) | `git --version` |
| M0 | golangci-lint, binário em `~/.local/bin` | **2.14.0** (a que valida o `.golangci.yml`) | `golangci-lint version` |
| M3 | `claude`, `codex` e `agy` | a mínima de cada documento: [Claude Code](../provedores/claude-code.md), [Codex](../provedores/codex.md), [Antigravity](../provedores/antigravity.md) | `claude --version`, `codex --version`, `agy --version` |
| M5 | ffmpeg | a do sistema ([anexos e transcrição](../telegram/anexos-e-transcricao.md)) | `ffmpeg -version` |
| M6 | Node e npm | Node 24 | `node --version`, `npm --version` |

### Go

1. Baixe o arquivo `go1.27.2.linux-amd64.tar.gz` de https://go.dev/dl/ e confira o SHA-256 publicado na mesma página.
2. Extraia em `~/.local/` (fica `~/.local/go/`).
3. Ponha no `~/.profile` a linha abaixo e abra um terminal novo:

```
export PATH="$HOME/.local/go/bin:$PATH"
```

O `go.mod` pede `go 1.25.0` (versão mínima da linguagem, [convenções](convencoes.md#layout-do-repositório)) e `toolchain go1.27.2`; com outra versão instalada, o `go` baixa a 1.27.2 sozinho, se puder.

### golangci-lint

Binário do release oficial (`golangci/golangci-lint` no GitHub), conferido pelo checksum publicado:

```
v=2.14.0; f=golangci-lint-$v-linux-amd64
curl -fsSLO https://github.com/golangci/golangci-lint/releases/download/v$v/$f.tar.gz
curl -fsSLO https://github.com/golangci/golangci-lint/releases/download/v$v/golangci-lint-$v-checksums.txt
sha256sum -c --ignore-missing golangci-lint-$v-checksums.txt
tar -xzf $f.tar.gz && install -m 0755 $f/golangci-lint ~/.local/bin/
```

`~/.local/bin` precisa estar no `PATH`. Sem o binário, as suítes rápida e completa falham apontando para este documento. Trocar a versão é mudar esta tabela e conferir o `.golangci.yml` na mesma alteração.

## Pinos do `go.mod`

Valores que o `go.mod` mantém, escritos só aqui: a [verificação de docs](../qualidade/verificacao-de-docs.md#checagens-de-código-e-de-marcos) lê esta tabela e falha, apontando para cá, se o arquivo divergir. Mudar um pino é uma decisão: edite a tabela e o `go.mod` na mesma alteração e rode a completa nos dois sistemas. Um `go get` ou `go mod tidy` pode subir a diretiva `go` por conta própria, e o `x/sys` sustenta o que é específico do Windows (ConPTY, ACLs, DPAPI).

| Pino | Valor |
|---|---|
| `go` | `1.25.0` |
| `toolchain` | `go1.27.2` |
| `golang.org/x/sys` | `v0.47.0` |

## Rodar as suítes e a verificação

Na raiz do repositório (o que cada suíte contém e quando roda está em [testes](../qualidade/testes.md)):

| Comando | Para quê |
|---|---|
| `go run ./tools/testrun affected` | durante o trabalho |
| `go run ./tools/testrun fast` | antes de cada commit (inclui o golangci-lint e a verificação de docs) |
| `go run ./tools/testrun full` | antes do merge |
| `go run ./tools/testrun fast -race` (ou `full -race`) | o que o CI Linux roda a mais fora do PR (no push para a `main` e sob demanda): o detector de corridas, com orçamento 3 vezes maior (precisa de cgo e de `gcc`) |
| `go run ./tools/docscheck` | só a [verificação de docs](../qualidade/verificacao-de-docs.md) |
| `go run ./tools/codecheck` | só as regras de código fora do linter ([estilo](convencoes.md#estilo)) |
| `golangci-lint run ./...` | só o linter |

### Ganchos de git

Versionados em `tools/githooks/`. Ligue uma vez em cada clone, na raiz: `git config core.hooksPath tools/githooks` (no Windows, o Git for Windows roda os ganchos com o `sh` dele). Os dois testam **o que vai ser gravado ou enviado**, não a pasta de trabalho: escrevem esse conteúdo numa pasta dentro da pasta do git, sempre com o mesmo caminho, para o cache de compilação do Go e do lint continuar quente. Nada da pasta de trabalho é guardado, trocado ou restaurado (sem `stash`): edições não adicionadas e arquivos novos não entram no teste.

- **`pre-commit`** roda a rápida sobre o índice (`git checkout-index`, que vale também para `commit -a` e `commit <caminhos>`), em `precommit-snapshot`. Se o commit muda **só documentos** (`docs/`, os `.md` da raiz e `LICENSE`, a mesma regra do CI), roda no lugar dela o `docscheck`, o `codecheck` e os testes que abrem um documento, como o CI faz.
- **`pre-push`** roda o [`testrun prepush`](../qualidade/testes.md#antes-do-push) sobre o commit enviado (`git read-tree` num índice próprio, em `prepush-snapshot`): a completa, sem `-race`, só nos pacotes mudados em relação à branch remota (numa branch nova, em relação a `origin/main`) e nos que os importam. Se só documentos mudaram, sai na hora, sem montar nada. Imprime os segundos gastos. Mexer em `pkg/clock` leva a completa inteira. Em emergência, `git push --no-verify`: o CI roda a rápida e a completa de qualquer jeito. O `-race` nunca roda antes do merge (só no push para a `main`), então uma corrida só aparece depois que o PR foi juntado.

Os dois ganchos (`tools/githooks/suite.sh`, usado pelos dois) fazem duas coisas a mais:
- **Uma suíte por vez na máquina:** seguram um `flock` (`/tmp/mestre-suite.lock`, ou `MESTRE_SUITE_LOCK`) enquanto o `testrun` roda. Clones, worktrees e sessões esperam a vez em vez de brigar pela CPU; a espera não conta no orçamento, porque o executor só começa a medir depois dela. Sem `flock` (Git for Windows) o gancho roda direto.
- **Árvore que já passou hoje não roda de novo:** o gancho grava a árvore (pré-commit) ou a árvore e a base (pré-push) que passou, em `suite-passed/` dentro da pasta do git. `git commit --amend` e um `push` repetido do mesmo commit saem na hora. A data faz parte da chave, porque a quarentena vence por dia. `MESTRE_FORCE_SUITE=1` ignora a memória.

## CI e proteção da `main`

O CI é `.github/workflows/ci.yml`: em cada PR, em cada push na `main` e sob demanda (`workflow_dispatch`), em `ubuntu-latest` e `windows-latest` em paralelo, instala o Go da `toolchain` do `go.mod` e o golangci-lint 2.14.0 (binário oficial, SHA-256 conferido) e roda `tools/docscheck`, `tools/codecheck`, `go build` e `go vet` com `CGO_ENABLED=0` (como os binários de release) e as suítes `fast` e `full` do `testrun` (o lint roda dentro delas, com o cache de análise já aquecido por um passo antes, como o cache de compilação). **Fora do PR (no push para a `main` e no `workflow_dispatch`), o job Linux roda ainda `fast -race` e `full -race`**, com orçamento 3 vezes maior ([testes](../qualidade/testes.md#executor-testrun)), depois de compilar os binários de teste com `-race` fora do orçamento; no PR não (a corrida só aparece depois do merge, e o pré-push também não a roda), e o push para a `main` nunca é cancelado por outro. **PR só de documentos** (todos os arquivos em `docs/`, `.md` da raiz ou `LICENSE`, comparando o commit de merge do PR com o pai dele): o job roda só `tools/docscheck`, `tools/codecheck` e, na rápida e na completa (`go test` direto, com `MESTRE_TEST_SUITE`, sem o `testrun`), os pacotes inteiros que têm um teste que abre um documento (achados pelo caminho `../docs/` que abrem) e o `tools/docscheck`; continua reportando `test (…)`, que o ruleset exige. Os caches de compilação, de módulos e de análise do golangci-lint ficam no disco de trabalho do runner e são guardados por `actions/cache` (não pelo do `setup-go`, que tem uma chave por `go.sum`, gravada uma vez e nunca renovada): cada push na `main` grava uma entrada nova (chave por sistema, versão do lint, `go.sum`, `.golangci.yml` e commit), e o PR restaura a mais recente, então só o que ele mudou é compilado e analisado de novo. No Windows, a varredura em tempo real do Defender fica desligada no runner, que é descartado depois do job. No Windows, `TEMP` e `TMP` apontam para a pasta temporária do runner (disco de trabalho `D:`, e não o disco do sistema `C:`, onde a padrão fica), porque os testes criam arquivos e bancos o tempo todo e ali isso custa o triplo. Ações de terceiros fixadas por SHA de commit; permissão só `contents: read`. As verificações se chamam `test (ubuntu-latest)` e `test (windows-latest)`.

**Contas reais:** `.github/workflows/real-accounts.yml` roda o subconjunto de [contas reais](../qualidade/contas-reais.md) nos dois sistemas (cria contas e serviços descartáveis no runner, com `MESTRE_REAL_ACCOUNT=1`, que só o workflow define): no PR que mexe no código que ele testa, toda noite (03:47 UTC) e sob demanda, com as mesmas ações fixadas por SHA e `contents: read`. As verificações se chamam `real accounts (ubuntu-latest)` e `real accounts (windows-latest)` e **não** entram no ruleset: o ruleset exige só as duas do `ci.yml`.

**Jobs da noite:** `.github/workflows/nightly.yml` (agendado às 03:23 UTC e sob demanda), com as mesmas ações fixadas por SHA e permissão `contents: read`. `quarantine` roda `testrun quarantine` nos dois sistemas; `flaky-hunt` roda `testrun fast -count 5 -shuffle` nos dois e guarda as linhas `mixed:` num artefato; `report`, um dos dois jobs com `issues: write` (nenhum roda código do repositório), abre uma issue com o rótulo `flaky` por teste misto, ou comenta na já aberta, com o `gh` do runner e o `GITHUB_TOKEN`. O resumo do job traz os testes lentos e a quarentena que pode sair ([testes](../qualidade/testes.md#quarentena)). `ruleset` compara o ruleset `main` ao vivo (`gh api repos/<repo>/rulesets`, depois o de nome `main`) com o JSON do bloco abaixo, a fonte única, com `tools/rulesetcheck`: ignora o que o servidor acrescenta (identificadores, links, datas, `source`, `source_type`, `current_user_can_bypass`, `do_not_enforce_on_create`, `required_reviewers`, `require_extra_approval_for_unattributed_changes`) e campo novo vazio; regra ou verificação a mais ou a menos, outro valor ou ajuste ligado é desvio. Com desvio, o job falha e `report ruleset drift` (o outro) abre ou reescreve uma issue com o rótulo `ruleset` e a fecha quando voltam a coincidir.

Passo do Dono, uma vez, com a conta dele (`gh auth login`): criar o ruleset da `main` que exige as duas verificações, PR com squash e proíbe apagar e forçar push. Rulesets em repositório privado pedem plano pago do GitHub.

```bash
gh api -X POST repos/lucasndas/mestre/rulesets --input - <<'JSON'
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "pull_request", "parameters": {
      "required_approving_review_count": 0, "dismiss_stale_reviews_on_push": false,
      "require_code_owner_review": false, "require_last_push_approval": false,
      "required_review_thread_resolution": false, "allowed_merge_methods": ["squash"]}},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": true,
      "required_status_checks": [
        {"context": "test (ubuntu-latest)", "integration_id": 15368},
        {"context": "test (windows-latest)", "integration_id": 15368}]}}
  ]
}
JSON
```

Conferir com `gh api repos/lucasndas/mestre/rulesets`. O `integration_id` 15368 é o do GitHub Actions; o nome das verificações muda se mudar o `name` do job no `ci.yml`, e o ruleset muda junto.

## Conta `mestre-agente` nesta máquina (Linux)

Criada para os spikes do M1 e mantida: é a mesma conta que o Mestre usa ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)), e o instalador a cria igual ([instalação](../plataforma/instalacao-e-atualizacao.md)). A pasta dos perfis é que difere: o instalador usa `/var/lib/mestre/<inst>/contas/` (instância `principal`), de dono `mestre` e grupo `mestre-agente` ([persistência](../arquitetura/persistencia.md#arquivos)); esta máquina mantém a dos spikes, sem instância e de dono `mestre-agente`:

```bash
sudo useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin mestre-agente
sudo install -d -m 0755 /var/lib/mestre
sudo install -d -o mestre-agente -g mestre-agente -m 2770 /var/lib/mestre/contas
```

- **CLIs dos provedores:** ficam no `HOME` de cada perfil de conta (aqui, `/var/lib/mestre/contas/<perfil>`), gravável pelo `mestre-agente`, instalados pelo instalador oficial de cada provedor na configuração do perfil ([bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta)). Assim a autoatualização deles funciona, e o Mestre não a desliga ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)). Os CLIs da pasta pessoal de quem desenvolve não são usados pelo `mestre-agente`.
- **Histórico do spike dos CLIs:** o spike usou cópias dos binários em `/opt/mestre-clis/` (e, no Codex, o pacote completo com um link), só porque os CLIs da pasta pessoal (`0750`) não eram executáveis pelo `mestre-agente`; essa pasta pode ser apagada (`sudo rm -rf /opt/mestre-clis`), assim como os perfis `spike-*` em `/var/lib/mestre/contas/`, que guardam logins reais.
- **Conferência manual com os CLIs reais:** `tools/spikes/linux/03-real-clis` (tag `realcli`, fora de todas as suítes; recusa rodar sem `MESTRE_REAL_CLI=1`) faz o papel do motor como `mestre-agente`: sobe o `mestre-pty` de produção e, dentro dele, cada CLI com o perfil `spike-*`, digita um pedido trivial, encerra com Ctrl-C e reinicia o motor com um CLI vivo (CA-TERM-08). Compile com `go test -c -tags realcli` e rode o binário com `sudo -u mestre-agente env MESTRE_REAL_CLI=1`; as opções estão no comentário do arquivo. Serve para repetir a conferência a cada versão nova de um CLI ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)); não responde à tela de confiança, então use uma pasta já confiada pelo perfil.
- Regra do sudo `/etc/sudoers.d/mestre-dev-agente` (`lucasndas ALL=(mestre-agente) NOPASSWD: ALL`, modo `0440`, conferida com `sudo visudo -cf`), só da máquina de desenvolvimento, para testes e spikes iniciarem processos como `mestre-agente`. O Mestre não usa sudo: quem inicia os terminais como `mestre-agente` é o `mestre-launcher`, o único com `CAP_SETUID` e `CAP_SETGID`; o serviço `mestre` roda sem capacidade extra ([iniciador](../arquitetura/componentes.md#iniciador-mestre-launcher)).

## Windows Sandbox para os spikes do M1

Os [spikes de Windows](ordem-de-implementacao.md#spikes-do-m1) continuam rodando no Windows Sandbox (Windows Pro com o recurso ligado), que começa limpo e não guarda nada ao fechar. Arquivos em `tools/winsandbox/`: `mestre-spikes.wsb` (rede ligada; compartilha só `C:\mestre\tools\winsandbox` como `C:\winsandbox`; roda o preparo ao abrir), `setup.ps1` (instala Git for Windows, Go 1.27.2 e o golangci-lint 2.14.0 com SHA-256 conferido e o Claude Code pelo instalador oficial; clona o repositório em `C:\mestre` dentro do Sandbox, na branch da rodada, hoje `spike/m1-windows-3`), `spikes-prompt.md` (o pedido da 1ª rodada, para a sessão do Claude que roda os quatro spikes; as outras rodadas têm `rodada-2-prompt.md` e `rodada-3-prompt.md`) e `spikes/` (o código dos spikes, guardado como referência; [convenções](convencoes.md#estilo)).

1. No Windows do Dono, clone o repositório em `C:\mestre` (ou atualize com `git pull`).
2. Abra `C:\mestre\tools\winsandbox\mestre-spikes.wsb` (duplo clique) e espere a janela do preparo terminar.
3. Nessa janela, rode `claude` uma vez e faça o login no navegador do Sandbox; saia com `/exit`.
4. Comece a rodada: `cd C:\mestre; claude --model opus --effort high "Leia e execute o arquivo <prompt da rodada>"` — a janela do preparo mostra o comando exato. Cada rodada tem o seu prompt e a sua branch, definidos no início do `setup.ps1` (`$Prompt` e `$Branch`): 1ª rodada `spikes-prompt.md` em `spike/m1-windows`; 2ª rodada (Antigravity e identificação do chamador) `rodada-2-prompt.md` em `spike/m1-windows-2`; 3ª rodada (`mestre-pty`, spike 5) `rodada-3-prompt.md` em `spike/m1-windows-3`.
5. No fim, a sessão faz commit e `git push` da branch da rodada (o Git pede login do GitHub no navegador do Sandbox) e copia o relatório e os logs para `C:\winsandbox\resultados\`, que no Windows do Dono é `C:\mestre\tools\winsandbox\resultados\` (fora do git). Feche o Sandbox só depois disso.

## O que não precisa

- Nenhum serviço instalado (systemd, serviço do Windows, Avahi): a completa roda a Cidadela e as Torres como processos comuns ou instâncias no processo do teste.
- Nenhuma conta real de Telegram ou GitHub: as suítes usam os [dublês](../qualidade/dubles.md); o repositório sandbox do GitHub é só para conferências manuais ([projetos de treino](../qualidade/projetos-de-treino.md#sandbox-real-no-github)).
- Os CLIs dos provedores antes do M3: até lá, o agente falso faz o papel deles.
