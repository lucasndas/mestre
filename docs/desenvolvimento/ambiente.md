# Preparar a máquina de desenvolvimento

> **Assunto:** o que instalar para desenvolver o Mestre, em que versão e como conferir, por marco; como rodar as suítes e a verificação; gancho de pré-commit; CI e proteção da `main` · **Componente:** Projeto · **Atualizado:** 2026-10-13

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

## Rodar as suítes e a verificação

Na raiz do repositório (o que cada suíte contém e quando roda está em [testes](../qualidade/testes.md)):

| Comando | Para quê |
|---|---|
| `go run ./tools/testrun affected` | durante o trabalho |
| `go run ./tools/testrun fast` | antes de cada commit (inclui o golangci-lint e a verificação de docs) |
| `go run ./tools/testrun full` | antes do merge |
| `go run ./tools/docscheck` | só a [verificação de docs](../qualidade/verificacao-de-docs.md) |
| `go run ./tools/codecheck` | só as regras de código fora do linter ([estilo](convencoes.md#estilo)) |
| `golangci-lint run ./...` | só o linter |

**Gancho de pré-commit:** versionado em `tools/githooks/pre-commit` (roda a rápida). Ligue uma vez em cada clone, na raiz: `git config core.hooksPath tools/githooks`. Ele testa **o que vai no commit**, não a pasta de trabalho: grava o índice (`git checkout-index`, que vale também para `commit -a` e `commit <caminhos>`) em `<pasta do git>/precommit-snapshot` e roda a rápida lá. Nada da pasta de trabalho é guardado, trocado ou restaurado (sem `stash`), então edições não adicionadas e arquivos novos não entram no teste e nenhum trabalho se perde; a pasta tem sempre o mesmo caminho, para o cache de compilação do Go e do lint continuar quente. No Windows, o Git for Windows roda o gancho com o `sh` dele.

## CI e proteção da `main`

O CI é `.github/workflows/ci.yml`: em cada PR e push na `main`, em `ubuntu-latest` e `windows-latest` em paralelo, instala o Go da `toolchain` do `go.mod` e o golangci-lint 2.14.0 (binário oficial, SHA-256 conferido) e roda `go vet ./...`, `tools/docscheck`, `tools/codecheck` e as suítes `fast` e `full` do `testrun` (o lint roda dentro delas). O cache de análise do golangci-lint fica guardado entre execuções (`actions/cache`, chave por sistema, versão do lint, `go.sum` e `.golangci.yml`), porque a primeira análise de uma dependência grande, como o `modernc.org/sqlite`, leva segundos. Ações de terceiros fixadas por SHA de commit; permissão só `contents: read`. As verificações se chamam `test (ubuntu-latest)` e `test (windows-latest)`.

**Jobs da noite:** `.github/workflows/nightly.yml` (agendado às 03:23 UTC e sob demanda), com as mesmas ações fixadas por SHA e permissão `contents: read`. `quarantine` roda `testrun quarantine` nos dois sistemas; `flaky-hunt` roda `testrun fast -count 5 -shuffle` nos dois e guarda as linhas `mixed:` num artefato; `report`, o único job com `issues: write` (e sem rodar código do repositório), abre uma issue com o rótulo `flaky` por teste misto, ou comenta na já aberta, com o `gh` do runner e o `GITHUB_TOKEN`. O resumo do job traz os testes lentos e a quarentena que pode sair ([testes](../qualidade/testes.md#quarentena)).

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

Criada para os spikes do M1 e mantida: é a mesma conta que o Mestre usa ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)). O instalador do Mestre faz igual em toda máquina Linux ([instalação](../plataforma/instalacao-e-atualizacao.md)):

```bash
sudo useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin mestre-agente
sudo install -d -m 0755 /var/lib/mestre
sudo install -d -o mestre-agente -g mestre-agente -m 2770 /var/lib/mestre/contas
```

- **CLIs dos provedores:** ficam no `HOME` de cada perfil de conta (`/var/lib/mestre/contas/<perfil>`), gravável pelo `mestre-agente`, instalados pelo instalador oficial de cada provedor na configuração do perfil ([bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta)). Assim a autoatualização deles funciona, e o Mestre não a desliga ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)). Os CLIs da pasta pessoal de quem desenvolve não são usados pelo `mestre-agente`.
- **Histórico do spike dos CLIs:** o spike usou cópias dos binários em `/opt/mestre-clis/` (e, no Codex, o pacote completo com um link), só porque os CLIs da pasta pessoal (`0750`) não eram executáveis pelo `mestre-agente`; essa pasta pode ser apagada (`sudo rm -rf /opt/mestre-clis`), assim como os perfis `spike-*` em `/var/lib/mestre/contas/`, que guardam logins reais.
- Regra do sudo `/etc/sudoers.d/mestre-dev-agente` (`lucasndas ALL=(mestre-agente) NOPASSWD: ALL`, modo `0440`, conferida com `sudo visudo -cf`), só da máquina de desenvolvimento, para testes e spikes iniciarem processos como `mestre-agente`. O Mestre não usa sudo: o serviço `mestre` usa `CAP_SETUID`/`CAP_SETGID` ([componentes](../arquitetura/componentes.md)).

## Windows Sandbox para os spikes do M1

Os [spikes de Windows](ordem-de-implementacao.md#spikes-do-m1) continuam rodando no Windows Sandbox (Windows Pro com o recurso ligado), que começa limpo e não guarda nada ao fechar. Arquivos em `tools/winsandbox/`: `mestre-spikes.wsb` (rede ligada; compartilha só `C:\mestre\tools\winsandbox` como `C:\winsandbox`; roda o preparo ao abrir), `setup.ps1` (instala Git for Windows, Go 1.27.2 e o golangci-lint 2.14.0 com SHA-256 conferido e o Claude Code pelo instalador oficial; clona o repositório em `C:\mestre` dentro do Sandbox, na branch `spike/m1-windows`) `spikes-prompt.md` (o pedido para a sessão do Claude que roda os quatro spikes) e `spikes/` (o código dos spikes, guardado como referência; [convenções](convencoes.md#estilo)).

1. No Windows do Dono, clone o repositório em `C:\mestre` (ou atualize com `git pull`).
2. Abra `C:\mestre\tools\winsandbox\mestre-spikes.wsb` (duplo clique) e espere a janela do preparo terminar.
3. Nessa janela, rode `claude` uma vez e faça o login no navegador do Sandbox; saia com `/exit`.
4. Comece a rodada: `cd C:\mestre; claude --model opus --effort high "Leia e execute o arquivo <prompt da rodada>"` — a janela do preparo mostra o comando exato. Cada rodada tem o seu prompt e a sua branch, definidos no início do `setup.ps1` (`$Prompt` e `$Branch`): 1ª rodada `spikes-prompt.md` em `spike/m1-windows`; 2ª rodada (Antigravity e identificação do chamador) `rodada-2-prompt.md` em `spike/m1-windows-2`; 3ª rodada (`mestre-pty`, spike 5) `rodada-3-prompt.md` em `spike/m1-windows-3`.
5. No fim, a sessão faz commit e `git push` da branch da rodada (o Git pede login do GitHub no navegador do Sandbox) e copia o relatório e os logs para `C:\winsandbox\resultados\`, que no Windows do Dono é `C:\mestre\tools\winsandbox\resultados\` (fora do git). Feche o Sandbox só depois disso.

## O que não precisa

- Nenhum serviço instalado (systemd, serviço do Windows, Avahi): a completa roda a Cidadela e as Torres como processos comuns ou instâncias no processo do teste.
- Nenhuma conta real de Telegram ou GitHub: as suítes usam os [dublês](../qualidade/dubles.md); o repositório sandbox do GitHub é só para conferências manuais ([projetos de treino](../qualidade/projetos-de-treino.md#sandbox-real-no-github)).
- Os CLIs dos provedores antes do M3: até lá, o agente falso faz o papel deles.
