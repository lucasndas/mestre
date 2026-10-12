# Preparar a máquina de desenvolvimento

> **Assunto:** o que instalar para desenvolver o Mestre, em que versão e como conferir, por marco; como rodar as suítes e a verificação · **Componente:** Projeto · **Atualizado:** 2026-10-12

Referência para quem vai desenvolver (pessoa ou agente) numa máquina Linux. Instale só o que o marco em andamento pede ([ordem de implementação](ordem-de-implementacao.md)).

## O que instalar

| Desde | Ferramenta | Versão | Conferir com |
|---|---|---|---|
| M0 | Go, em `~/.local/go` | 1.27.2 (a `toolchain` do `go.mod`) | `go version` |
| M0 | git | 2.32 ou maior ([ADR 0001](../decisoes/0001-stack-go.md)) | `git --version` |
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

O `go.mod` pede `go 1.25` (versão mínima da linguagem, [convenções](convencoes.md#layout-do-repositório)) e `toolchain go1.27.2`; com outra versão instalada, o `go` baixa a 1.27.2 sozinho, se puder.

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

## Conta `mestre-agente` nesta máquina (Linux)

Criada para os spikes do M1 e mantida: é a mesma conta que o Mestre usa ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)). O instalador do Core e do nó Linux faz igual ([instalação](../plataforma/instalacao-e-atualizacao.md)):

```bash
sudo useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin mestre-agente
sudo install -d -m 0755 /var/lib/mestre
sudo install -d -o mestre-agente -g mestre-agente -m 2770 /var/lib/mestre/contas
```

Só da máquina de desenvolvimento (o instalador não faz):
- `/opt/mestre-clis/bin/` com cópias de `claude`, `codex` e `agy` (`sudo install -m 0755 "$(readlink -f ~/.local/bin/<cli>)" /opt/mestre-clis/bin/<cli>`), porque os da pasta pessoal (`0750`) não são executáveis pelo `mestre-agente`. Atualize as cópias quando o CLI pessoal mudar de versão.
- Regra do sudo `/etc/sudoers.d/mestre-dev-agente` (`lucasndas ALL=(mestre-agente) NOPASSWD: ALL`, modo `0440`, conferida com `sudo visudo -cf`), para testes e spikes iniciarem processos como `mestre-agente`. O Mestre não usa sudo: o `mestre-node` usa `CAP_SETUID`/`CAP_SETGID` ([componentes](../arquitetura/componentes.md)).

## Windows Sandbox para os spikes do M1

Os [spikes de Windows](ordem-de-implementacao.md#spikes-do-m1) rodam no Windows Sandbox (Windows Pro com o recurso ligado), que começa limpo e não guarda nada ao fechar. Arquivos em `tools/winsandbox/`: `mestre-spikes.wsb` (rede ligada; compartilha só `C:\mestre\tools\winsandbox` como `C:\winsandbox`; roda o preparo ao abrir), `setup.ps1` (instala Git for Windows e Go 1.27.2 com SHA-256 conferido e o Claude Code pelo instalador oficial; clona o repositório em `C:\mestre` dentro do Sandbox, na branch `spike/m1-windows`) e `spikes-prompt.md` (o pedido para a sessão do Claude que roda os spikes).

1. No Windows do Dono, clone o repositório em `C:\mestre` (ou atualize com `git pull`).
2. Abra `C:\mestre\tools\winsandbox\mestre-spikes.wsb` (duplo clique) e espere a janela do preparo terminar.
3. Nessa janela, rode `claude` uma vez e faça o login no navegador do Sandbox; saia com `/exit`.
4. Comece os spikes: `cd C:\mestre; claude --model opus --effort high "Leia e execute o arquivo tools/winsandbox/spikes-prompt.md"`.
5. No fim, a sessão faz commit e `git push` da branch `spike/m1-windows` (o Git pede login do GitHub no navegador do Sandbox) e copia o relatório e os logs para `C:\winsandbox\resultados\`, que no Windows do Dono é `C:\mestre\tools\winsandbox\resultados\` (fora do git). Feche o Sandbox só depois disso.

## O que não precisa

- Nenhum serviço instalado (systemd, serviço do Windows, Avahi): a completa roda Core e nó como processos comuns.
- Nenhuma conta real de Telegram ou GitHub: as suítes usam os [dublês](../qualidade/dubles.md); o repositório sandbox do GitHub é só para conferências manuais ([projetos de treino](../qualidade/projetos-de-treino.md#sandbox-real-no-github)).
- Os CLIs dos provedores antes do M3: até lá, o agente falso faz o papel deles.
