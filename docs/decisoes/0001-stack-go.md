# ADR 0001: Stack em Go

> **Assunto:** linguagem e bibliotecas do motor, da CLI e do Guardião · **Componente:** Todos · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. Decisão completada em 2026-10-12: git pelo binário do sistema, por um wrapper interno, e emulador de terminal na máquina do terminal (abaixo). **Termos atualizados em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): Core e nó viram Cidadela e Torre (ou máquina); a decisão não muda. **Revista em 2026-10-13** pelo [ADR 0023](0023-terminais-fora-do-motor.md): PTY e ConPTY pela `aymanbagabas/go-pty`, dentro do `mestre-pty` (o wrapper próprio vira plano B). **Revista em 2026-10-13** (auditoria de segurança): a versão mínima do git deixa de ser a das funções usadas (2.32, `GIT_CONFIG_GLOBAL`) e passa a ser um piso de segurança, abaixo.

## Contexto

O Mestre tem serviços que ficam ligados o tempo todo em duas plataformas, e a linguagem precisa atender a todos estes requisitos ao mesmo tempo:

- **Terminais:** cada agente roda num PTY no Linux e num ConPTY no Windows.
- **Windows:** toda máquina, Cidadela ou Torre, roda serviços do Windows, sem interface gráfica; o serviço do motor (`mestre`, numa conta virtual) cria os ConPTY e inicia cada agente com a conta `mestre-agente` (`CreateProcessAsUser`). O desenho está em [Windows](../arquitetura/windows.md).
- **Linux:** toda máquina roda serviços do systemd com watchdog (`Type=notify`, `WatchdogSec=`). A camada 1 da autocura depende disso ([camadas](../autocura/camadas.md)).
- **Conexões:** WSS com TLS e CA interna, com mTLS entre Torre e Cidadela ([protocolo](../conectividade/protocolo.md), [segurança](../arquitetura/seguranca.md)).
- **Banco:** SQLite em modo WAL em cada máquina, com réplica do banco da Cidadela nas Torres ([persistência](../arquitetura/persistencia.md)).
- **Entrega:** um binário por componente e plataforma, fácil de instalar, atualizar e voltar de versão. O Guardião é separado e quase nunca muda ([ADR 0003](0003-autocura-com-guardiao-sem-ssh.md)).
- **Testes:** a suíte rápida tem orçamento de 30 segundos (hoje 36 s, [testes](../qualidade/testes.md)), sem `sleep` e com relógio injetável ([testes](../qualidade/testes.md), [dublês](../qualidade/dubles.md)). O sistema tem muitos temporizadores.

## Opções consideradas

Comparação feita com as versões disponíveis em outubro de 2026.

| Requisito | Go | Rust | TypeScript / Bun |
|---|---|---|---|
| PTY no Linux | `creack/pty`, padrão de fato | `portable-pty` 0.9.0 (do WezTerm) | `Bun.Terminal` (openpty) ou `node-pty` |
| ConPTY no Windows | `golang.org/x/sys/windows` (v0.49) tem `CreatePseudoConsole`, `ResizePseudoConsole` e `NewProcThreadAttributeList`. `creack/pty` não suporta Windows (`ErrUnsupported`), então o wrapper é próprio, tendo como modelos `aymanbagabas/go-pty` e `UserExistsError/conpty` | backend ConPTY do `portable-pty`, o mais testado dos três (WezTerm) | `Bun.Terminal` no Windows com ressalvas documentadas: sem termios, sequências de escape recodificadas, `\r` não traduzido, sem SIGWINCH fora do modo raw, `close()` pode não matar o filho antes do Windows 11 24H2 |
| ConPTY dentro de serviço com conta de usuário, ACLs, DPAPI e mDNS nativo (ver [Windows](../arquitetura/windows.md)) | APIs nativas em `x/sys/windows`: `CreatePseudoConsole`, lista de atributos, `CreateEnvironmentBlock`, segurança de arquivos, `CryptProtectData`; `DnsServiceRegister` pela DLL do sistema | crate `windows` tem tudo; o spawn com ConPTY é próprio (mesmo trabalho que em Go) | só por FFI (`bun:ffi`/koffi) para kernel32: possível, mas frágil |
| Serviço do Windows | `kardianos/service` v1.3.0 (jul/2026) ou `x/sys/windows/svc` | `windows-service` 0.8.1 (Mullvad, mai/2026) | nada nativo; exige um wrapper (WinSW, NSSM, shawl), o que quebra o binário único |
| systemd + watchdog | `coreos/go-systemd/v22/daemon` v22.7.0 (`SdNotify`, `SdWatchdogEnabled`) | `sd-notify` 0.5.0 | sem socket datagrama AF_UNIX; teria de chamar `systemd-notify` com `NotifyAccess=all` |
| WebSocket, TLS e CA interna | `coder/websocket` + `crypto/tls` e `crypto/x509` da biblioteca padrão (emite certificados, mTLS) | `tokio-tungstenite` ou `axum` + `rustls` + `rcgen` | `Bun.serve` com TLS + ws; `node:tls` |
| SQLite (WAL) sem compilador C | `modernc.org/sqlite` v1.60.x (SQLite 3.53.4, Go puro); alternativa `ncruces/go-sqlite3` | `rusqlite` 0.40 `bundled`: compila C | `bun:sqlite` embutido |
| Binário único e compilação cruzada | trivial: `GOOS=windows GOARCH=amd64`, `CGO_ENABLED=0` | toolchains por alvo, `cargo-xwin` ou `cross`; dependências em C complicam | `bun build --compile --target=bun-windows-x64`; ícone e metadados do Windows não podem ser definidos na compilação cruzada |
| UI embutida | `//go:embed` + `http.FileServerFS` | `rust-embed` / `include_dir` | `bun build --compile --asset` |
| mDNS e broadcast UDP | `grandcat/zeroconf`, `brutella/dnssd`, `net.ListenUDP` | `mdns-sd` 0.21.5 | `bonjour-service`, `dgram` |
| Velocidade de testes e relógio falso | `go test` rápido com cache; `testing/synctest` (GA no Go 1.25) torna temporizadores determinísticos e instantâneos; interface de relógio para o que cruza E/S | `tokio::time::pause()`/`advance()`; compilação e link lentos (dezenas de segundos incrementais num projeto async + TLS + SQLite, estimativa) | `bun test` muito rápido, `useFakeTimers`/`setSystemTime` (o construtor `Date` não é falsificado) |
| Riscos principais | wrapper de ConPTY escrito à mão (estimativa: 300 a 500 linhas); SQLite em Go puro 1,3 a 2x mais lento que o C | iteração mais lenta; `portable-pty` sem release desde fev/2025; compilação cruzada com C; mais atrito com async para um time pequeno | Bun 1.4 (2026-08-20) é a primeira versão reescrita de Zig para Rust, ainda "não 100% compatível com Node.js"; sem serviço do Windows nem sd_notify; cada lacuna empurra para um binário auxiliar |

Com **Node SEA** no lugar do Bun, as lacunas de serviço do Windows e de ConPTY continuam as mesmas. Além disso, o `node-pty` precisa ser extraído para um arquivo temporário e carregado com `process.dlopen`, e sua compilação exige MSVC com as bibliotecas Spectre.

**Rust**
- Prós: ConPTY mais testado, menor uso de memória e Whisper dentro do processo (`whisper-rs` 0.16).
- Contras: ciclo de compilar e testar mais lento, que afeta direto o orçamento de 30 s (hoje 36 s, [testes](../qualidade/testes.md)). Compilação cruzada para Windows com dependências em C. O código de ConPTY, ACL e ambiente do usuário é o mesmo trabalho que em Go.

**TypeScript/Bun**
- Prós: uma só linguagem com o painel e testes muito rápidos.
- Contras: runtime recém-reescrito por baixo de um serviço 24/7. Sem integração com o gerenciador de serviços do Windows nem com o sd_notify. ConPTY e ACLs só por FFI.

**Go**
- Prós: é a única opção em que todos os requisitos têm biblioteca madura sem CGO, com compilação cruzada trivial a partir do Linux e tempo falso na biblioteca padrão.
- Contras: o código de ConPTY, ACL e ambiente do usuário do Windows é nosso.

## Decisão

- **Go 1.25 ou superior**, um único módulo, compilado com `CGO_ENABLED=0`.
- O `go.mod` mantém `go 1.25` e fixa `toolchain go1.27.2`, a versão de desenvolvimento ([ambiente](../desenvolvimento/ambiente.md)).
- **Dois binários por plataforma:**
  - `mestre`: CLI, motor e subcomandos.
  - `mestre-guardiao`: separado, com código e versão independentes.
  - A lista de subcomandos e de quem roda o quê está em [componentes](../arquitetura/componentes.md).
- **Bibliotecas:**

  | Área | Biblioteca |
  |---|---|
  | PTY no Linux | `creack/pty` |
  | ConPTY no Windows | wrapper próprio sobre `golang.org/x/sys/windows`, inspirado em `aymanbagabas/go-pty` e `UserExistsError/conpty` |
  | Serviços | `kardianos/service` (instalar, iniciar, parar) + `coreos/go-systemd/v22/daemon` (`READY=1`, `WATCHDOG=1`, `STATUS=`) |
  | WebSocket | `coder/websocket` |
  | TLS, CA interna e mTLS | biblioteca padrão |
  | SQLite | `modernc.org/sqlite` em WAL, com uma conexão de escrita |
  | UI | `embed` para servir o build do painel ([ADR 0017](0017-canvas-react-flow.md)) |
  | Testes | `testing/synctest` + interface de relógio injetável |
  | Emulador de terminal na máquina (tela para `check` e reconhecimento de telas) | `github.com/charmbracelet/x/vt` ou equivalente; escolha e plano B em [terminal](../arquitetura/terminal.md) |
  | Git | **binário `git` do sistema**, na versão mínima de segurança (abaixo), chamado por um wrapper interno único, `internal/gitx`; nunca uma biblioteca Go de git |

- **Versão mínima do git, um piso de segurança:** **2.43.0 no Linux**, a do Ubuntu 24.04 LTS, a distribuição de referência (as distribuições corrigem falhas por backport sem mudar o número, então o piso é o pacote dela, que recebe essas correções; o Ubuntu 22.04 traz 2.34.1 e precisa do PPA `git-core`); **2.50.1 no Windows**, porque o Git for Windows não faz backport e essa versão fecha as falhas de clone e de ajudante de credencial (CVE-2024-32002, CVE-2025-48384, CVE-2025-48386). As duas passam da correção do `safe.directory` (CVE-2022-24765, 2.35.2) e têm tudo o que o `gitx` usa (`GIT_CONFIG_GLOBAL`, 2.32; `GIT_CONFIG_COUNT`, 2.31). O `gitx` recusa versão menor (`ErrGitTooOld`) e o [checklist](../plataforma/checklist.md) confere (ALL-06). Rever o piso quando sair uma LTS ou uma correção crítica.
- **Por que o binário `git`:** worktrees, `git bundle`, `info/exclude`, `skip-worktree` e `merge --ff-only` precisam se comportar exatamente como o git que a pessoa usa; bibliotecas Go de git não cobrem tudo isso com fidelidade. O `testkit` tem um **git falso** atrás da mesma interface do `gitx`, para a suíte rápida; critérios que precisam de git real ficam na completa ([dublês](../qualidade/dubles.md)).
- **Fora do processo Go:**
  - Whisper roda como processo auxiliar ([ADR 0016](0016-whisper-cpp-local.md)).
  - O painel é TypeScript ([ADR 0017](0017-canvas-react-flow.md)).

## Consequências

- **Positivas:**
  - Um build no Linux gera todos os binários.
  - Instalar e voltar de versão é trocar um arquivo ([instalação e atualização](../plataforma/instalacao-e-atualizacao.md)).
  - Os temporizadores (heartbeat, backoff, tempos limite, rituais) são testáveis sem esperar.
- **Negativas:**
  - Mantemos de 300 a 500 linhas de código Windows de baixo nível (ConPTY, ACL, ambiente do usuário).
  - O git precisa estar instalado em toda máquina, na versão mínima, conferido pelo [checklist](../plataforma/checklist.md) (ALL-06); cada chamada é um processo, o que é irrelevante no volume do Mestre.
  - Os testes reais de ConPTY só rodam num runner Windows, na suíte completa ou na pesada.
  - O `synctest` não cobre E/S real de rede e disco. Nesses pontos usamos transporte e PTY falsos ([dublês](../qualidade/dubles.md)).
  - SQLite em Go puro é mais lento. Isso é irrelevante no volume de um orquestrador. A versão de `modernc.org/libc` fica fixada.
  - O repositório tem duas linguagens (Go e TypeScript). Os tipos compartilhados do protocolo são gerados a partir do Go ou de JSON Schema ([convenções](../desenvolvimento/convencoes.md)).
- **Spike do M1:** o ConPTY com o processo iniciado como `mestre-agente` a partir de um serviço com conta virtual, na sessão 0, é testado num Windows 11 real antes de fechar o wrapper ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1)); padrão e plano B no documento dono, [Windows](../arquitetura/windows.md#verificar-na-implementação).

## Fontes

- creack/pty (sem Windows): https://github.com/creack/pty , https://raw.githubusercontent.com/creack/pty/master/start_windows.go
- ConPTY em Go: https://github.com/aymanbagabas/go-pty , https://pkg.go.dev/github.com/UserExistsError/conpty , https://pkg.go.dev/github.com/charmbracelet/x/conpty
- x/sys/windows: https://pkg.go.dev/golang.org/x/sys/windows
- Sessão ConPTY (Microsoft): https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session
- kardianos/service: https://pkg.go.dev/github.com/kardianos/service ; go-systemd: https://pkg.go.dev/github.com/coreos/go-systemd/v22/daemon
- coder/websocket: https://github.com/coder/websocket
- SQLite: https://pkg.go.dev/modernc.org/sqlite , https://github.com/ncruces/go-sqlite3 , https://github.com/cvilsmeier/go-sqlite-bench
- synctest: https://go.dev/blog/testing-time
- Rust: https://crates.io/crates/portable-pty , https://crates.io/crates/windows-service , https://crates.io/crates/rusqlite , https://crates.io/crates/sd-notify , https://crates.io/crates/mdns-sd , https://crates.io/crates/whisper-rs
- Bun: https://bun.com/blog/bun-v1.4 , https://bun.com/docs/runtime/child-process , https://bun.com/docs/bundler/executables , https://bun.com/docs/test/dates-times
- Node SEA e node-pty: https://nodejs.org/api/single-executable-applications.html , https://github.com/microsoft/node-pty
