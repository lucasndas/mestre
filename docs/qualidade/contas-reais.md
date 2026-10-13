# Contas reais

> **Assunto:** testes que só têm sentido com contas de verdade do sistema (troca de usuário do launcher, logon em lote da conta dos agentes, conta de serviço virtual): o subconjunto da suíte pesada que roda numa máquina descartável, no CI e no laboratório; o que cobre em cada sistema, como roda, orçamento e o que ainda é do laboratório · **Componente:** Projeto · **Atualizado:** 2026-10-13

## Objetivo

Partes de CA-TERM-05, CA-TERM-07, CA-TERM-11, CA-TERM-12 e CA-COFRE-08 exigem o que a [completa](testes.md#suítes) não pode ter: o `mestre-launcher` com `CAP_SETUID` trocando de conta de verdade, o `LogonUser` em lote da conta dos agentes, duas sessões de logon da mesma conta, uma conta de serviço virtual com DPAPI. Os runners hospedados do GitHub são descartáveis e dão `sudo` sem senha (Linux) e administrador (Windows), então essas partes rodam lá, sem tocar na máquina de ninguém, e não esperam o laboratório.

## Comportamento

### O que é

- **Subconjunto nomeado da [pesada](testes.md#suítes)**, não uma suíte nova do executor: *build tag* `realaccount`, pacote `internal/realaccount`, arquivos `*_realaccount_test.go`. Nenhuma das suítes `fast`, `full` e `heavy` (que compila com `full,heavy`) o compila.
- **Ferramenta** `MESTRE_REAL_ACCOUNT=1 go run ./tools/realaccount run [-budget 10m] [-timeout 25m] [-keep]`: compila o binário de teste e o `mestre-pty` com `CGO_ENABLED=0` (o launcher esvazia as capacidades com chamada de sistema em todas as threads, o que cgo impede), cria as contas e os serviços, roda os testes **como o motor** e remove tudo. Imprime `PASS`/`FAIL` com o tempo e falha se passar do orçamento.
- **Trava:** a ferramenta recusa (código 2, nada é criado) se `MESTRE_REAL_ACCOUNT` não for exatamente `1`, e o binário de teste também recusa sem ela e sem o papel `MESTRE_RA_ROLE=engine` que só a ferramenta define. Só o workflow define a variável; ninguém a põe no perfil de uma máquina de uso. A ferramenta também recusa uma máquina onde as contas ou a pasta já existem.
- As contas são da instância `ci`, para nada se confundir com uma instalação ([instâncias](../arquitetura/componentes.md#instâncias)).

### Linux

- **Contas:** `mestre-ci` (o motor) e `mestre-agente-ci` (os agentes), de sistema, sem shell.
- **Launcher:** a unit empacotada `packaging/systemd/mestre-launcher@.service`, copiada como está, com um *drop-in* que só troca `User=` e `ExecStart=` (o binário de teste no modo `helper-launcher`, que chama `launcher.Run`). Vale o que a unit declara: `Delegate=yes`, `DelegateSubgroup=launcher`, `KillMode=process`, as duas capacidades e o `RuntimeDirectory`. Três instâncias: `ci` (a da instalação), `nocg` (sem `Delegate`: o LIN-11 do [checklist](../plataforma/checklist.md) falhou) e `rst` (a que o teste reinicia, por um `sudoers` de uma linha).
- **Motor:** os testes rodam na unit transitória `mestre-ci-engine.service` (`systemd-run --uid`), o cgroup que o launcher aceita, com o `terminal.Manager` e o `launcher.Client` de produção. Os agentes são terminais de verdade; o que um agente faz é um modo do próprio binário de teste.
- **`kernel.yama.ptrace_scope`** vai a 0 durante a rodada (e volta ao fim), para que só o `PR_SET_DUMPABLE` do `mestre-pty` separe um agente dele; um processo dumpable da mesma conta é o controle.
- **Confere:** CA-TERM-07 (o `mestre-pty` roda como o agente, sem grupo suplementar, em `term-<id>` sob o cgroup delegado do launcher; o launcher tem só `CAP_SETUID` e `CAP_SETGID`, sem ambiente; o `mestre-pty` e o agente não têm capacidade em conjunto nenhum; os terminais sobrevivem a `systemctl restart` da unit e o launcher novo os encerra; a mesma conta do motor, numa unit que não é a dele, é recusada e nada é criado); CA-TERM-05 (o motor não sinaliza nada da conta dos agentes; o `Stop` forçado acaba com a árvore, inclusive o neto de `setsid -f`, e remove o cgroup; sem cgroup, o `kill` por identidade do launcher assume o UID dos agentes numa thread e encerra o `mestre-pty` e as sessões, não um terminal vizinho da mesma conta nem um PID com outro início); CA-TERM-11 (outro processo dos agentes, em outro terminal, não lê `environ` nem `mem`, não faz `ptrace` nem `pidfd_getfd` no `mestre-pty` e não alcança o socket do launcher, o do terminal nem a pasta `pty`).

### Windows

- **Conta:** `mestre-agente-ci`, local, no grupo `Users`, senha de 32 caracteres aleatórios que não expira, direitos `SeBatchLogonRight`, `SeDenyInteractiveLogonRight` e `SeDenyRemoteInteractiveLogonRight` (pela política LSA), oculta da tela de login; a ferramenta confere que o logon em lote passa e o interativo é negado. A senha fica no arquivo da instalação: DPAPI de máquina, dono e ACL só SYSTEM.
- **Serviços:** `MestreLauncher$ci` (LocalSystem) e `Mestre$ci` (a conta virtual `NT SERVICE\Mestre$ci`, com `sc privs` só `SeChangeNotifyPrivilege`, como a instalação), ambos o binário de teste, iniciados pelo gerenciador de serviços. O launcher é o `winsvc.NewLauncher` com o `AgentAccount` real e o `EnginePolicy` (SID da conta virtual e PID do SCM); o serviço `Mestre$ci` roda os testes e grava o resultado num log que a ferramenta mostra.
- **Confere:** CA-TERM-07 (o `mestre-pty` e o agente rodam como a conta, em sessão de logon em lote própria que não é a do motor nem de outro terminal, fora do Job do motor, com o perfil carregado e o ambiente do `CreateEnvironmentBlock`; o motor consulta, espera e encerra o `mestre-pty` e só consulta e espera o agente; o perfil é descarregado e a sessão de logon fechada quando o `mestre-pty` termina); CA-TERM-12 (dois terminais com logons distintos: o processo de um não abre os do outro com `PROCESS_VM_READ`, `PROCESS_DUP_HANDLE`, `PROCESS_TERMINATE` nem `WRITE_DAC`, e do `mestre-pty` vizinho só consulta; tem o controle sobre os do próprio terminal); CA-TERM-05 (um processo criado pelo WMI `Win32_Process.Create` dentro do terminal nasce fora do Job e morre com o fim do terminal: pela parada forçada do `mestre-pty`, pelo `Stop` do motor e pela varredura do launcher quando o `mestre-pty` é morto); ACLs do `cli.sock`, da pasta `pty` e do pipe do launcher ([Windows](../arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty)): a conta dos agentes, num terminal, não conecta ao socket de outro, não lista as pastas, não apaga socket e não abre o pipe, mas conecta ao `cli.sock`; CA-COFRE-08 (a parte sem instalador: sob a conta virtual de serviço a chave do cofre é gravada com DPAPI e ACL só dela, a conta dos agentes não a lê, o cofre recusa quando a ACL a inclui — e então a conta dos agentes a lê — e volta a abrir depois da correção).

### Onde e quando roda

- **CI:** `.github/workflows/real-accounts.yml`, `ubuntu-latest` e `windows-latest` em paralelo, `contents: read`, ações fixadas por SHA como no `ci.yml`. Dispara no pull request que mexe em `internal/launcher`, `winsvc`, `terminal`, `vault`, `localchan`, `realaccount`, `cmd`, `tools/realaccount`, `packaging/systemd`, `go.mod`, `go.sum` ou no próprio arquivo, toda noite (03:47 UTC) e sob demanda. **Não é exigido pelo ruleset** ([CI](../desenvolvimento/ambiente.md#ci-e-proteção-da-main)): falhar nele não trava a `main`, abre trabalho. É um workflow à parte, e não um job do `nightly.yml`, porque roda também no PR e porque a falha dele não é a de um verificador da noite.
- **Orçamento:** 5 minutos para a rodada inteira (compilação, contas, testes e limpeza), medidos pela ferramenta; o job tem 30 minutos de folga. Medido no PR: a rodada leva 19 s no Linux e 42 s no Windows (os jobs, com o Go e a cópia do repositório, 38 s e 1 min 41 s); o tempo de cada rodada fica no log do job.
- **Laboratório:** o mesmo comando nas duas VMs (descartáveis por retrato) antes do `testrun heavy`.

### O que continua no laboratório e no M7

- Instalador: criar as contas e os direitos, as unidades e serviços de verdade, aplicar as ACLs de `run\` e `pty\`, `RuntimeDirectory`, `NoNewPrivileges` e o resto do endurecimento da unit.
- O binário `mestre launcher` e o serviço `mestre` reais (aqui o binário de teste faz o papel deles), o Guardião, o reinício da máquina e os CLIs dos provedores logados na conta (Gerenciador de Credenciais do `agy`).
- A conta `mestre` do Linux e a instância principal; duas Cidadelas instaladas na mesma máquina.

## Regras e erros

- Estes testes só rodam por `tools/realaccount`, nunca na rápida, na completa nem em máquina de uso. Quem precisa de conta real num critério novo acrescenta o teste aqui, com o marcador `// CA-…` do critério, e tira a parte da lista do laboratório.
- Sem `sleep`: espera por eventos (saída do terminal, conexão que cai, processo que sai). A única consulta repetida é a do perfil e da sessão de logon, que o Windows libera depois que o `mestre-pty` termina, sem evento que o motor possa esperar.
- No Windows, a ferramenta confere no fim que o launcher soltou o perfil e todas as sessões de logon da conta dos agentes (só um administrador lista as sessões de outra conta). Antes, reinicia o serviço do WMI: ele guarda por um minuto ou mais o token de quem chamou `Win32_Process.Create` (os testes chamam, de dentro de um terminal), e a sessão de logon vive enquanto houver um token dela; isso não é do launcher soltar (visto no CI: a sessão sumia na hora do reinício).
- Falha de limpeza é aviso, não falha de teste; a máquina é descartável.

## Critérios de aceite

- **CA-CONTAS-01** · *rápida* — **Dado** `MESTRE_REAL_ACCOUNT` ausente ou diferente de `1`, **quando** o `tools/realaccount run` roda, **então** sai com 2, a mensagem cita a variável e nada é preparado; **dado** um comando desconhecido, **então** sai com 2.
- **CA-CONTAS-02** · *rápida* — **Dado** uma rodada de 8 min e o orçamento de 10 min, **quando** a ferramenta termina, **então** imprime `PASS` com o tempo e sai com 0; **dado** 11 min, ou `-budget 2m` com 3 min, **então** falha dizendo que passou do orçamento; **dado** um teste que falha, **então** sai com 1 e imprime `FAIL`.

## Depende de

- [testes.md](testes.md) · [terminal](../arquitetura/terminal.md) · [cofre](../plataforma/cofre.md)
