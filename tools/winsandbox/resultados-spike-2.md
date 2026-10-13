# Spikes de Windows do M1, rodada 2

- **Data:** 2026-10-12 (relógio do Sandbox, -03:00), branch `spike/m1-windows-2`.
- **Windows:** Windows 11 Enterprise 24H2, build 10.0.26100.9457 (Windows Sandbox, usuário WDAGUtilityAccount, administrador).
- **Ferramentas:** Go 1.27.2, `golang.org/x/sys` v0.47.0 (sem mudar o `go.mod`), `agy` 1.3.3.
- **Código:** `tools/winsandbox/spikes/01-agent-service/` (só o `login.ps1` mudou: `agy` do perfil do `mestre-agente` e o modo `agy-install`) e o novo `tools/winsandbox/spikes/02-caller-identity/` (`callerid`).
- **Logs:** `C:\winsandbox\resultados\rodada-2\` (arquivos citados abaixo).

| Tarefa | Resultado | Em uma linha |
|---|---|---|
| A. `agy` com o token entregue | **Passou** (com um achado; renovação não exercitada) | a credencial fica e o `agy` responde pelas duas variantes, 3 vezes pela (ii); a renovação do token não foi testada (§A.3); o `CreateProcessAsUser` do serviço não abre um `.exe` dentro do perfil do `mestre-agente` |
| B. Identificação do chamador | **Árvore falhou; Job Object passou** | a árvore recusa órfãos (como no Linux); o Job acerta todos os casos dentro dele, com uma ACE do serviço na DACL padrão do token |

## Tarefa A — `agy` pela entrega do token: PASSOU

### A.1 Preparação

- `spikesvc install -service mestre` e `-service launcher`: `MestreSpike` (`NT SERVICE\MestreSpike`, com `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege` declarados) e `MestreSpikeLauncher` (LocalSystem); conta `mestre-agente` nova.
- `agy` instalado pelo instalador oficial (`irm https://antigravity.google/cli/install.ps1 | iex`, conferido: baixa o binário, confere o SHA-512 do manifesto, roda `agy install`) **como `mestre-agente`**, pelo iniciador: foi para `C:\Users\mestre-agente\AppData\Local\agy\bin\agy.exe` (ACL: SYSTEM, Administradores e `mestre-agente`). `a0-instalar-agy.txt`.
- Antes do login, `cmdkey /list` vazio (`a0-cmdkey-antes-do-login.txt`). O Dono fez o login com `login.ps1 agy` (iniciador, `HOME` e `USERPROFILE` = `work\contas\p1\home`, como na rodada 1), às ~23:05.

### A.2 Sequência por variante (cada passo numa sessão de logon nova)

Todos os passos usam `HOME` e `USERPROFILE` do perfil de conta. Passos: `cmdkey /list` → `agy -p "/usage" --output-format json` → `cmdkey /list`.

| Rodada | Variante | Credencial antes | `agy -p` | Credencial depois | Log |
|---|---|---|---|---|---|
| 1 (logo após o login) | (i) iniciador LocalSystem cria o processo | `gemini:antigravity` | `"status":"SUCCESS"` (cotas) | `gemini:antigravity` | `a-1-variante-i.txt` |
| 2, 3 e 4 | (ii) serviço cria o processo com o token entregue, **`agy.exe` como raiz** | `gemini:antigravity` | **não iniciou:** `CreateProcessAsUser … Acesso negado.` | `gemini:antigravity` | `a-2-variante-ii-{1,2,3}.txt` |
| 5, 6 e 7 | (ii) idem, **raiz `cmd.exe /c agy.exe …`** | `gemini:antigravity` | `"status":"SUCCESS"` | `gemini:antigravity` | `a-3-variante-ii-cmd-{1,2,3}.txt` |
| 8 | (ii) `agy.exe` raiz, com RX de `NT SERVICE\MestreSpike` em `agy\bin` | — | `"status":"SUCCESS"` | — | `a-3b-variante-ii-acl-rx-no-bin.txt` |

**Achado (não previsto nos documentos):** o `CreateProcessAsUser` abre o executável **com o contexto de quem chama** (a conta virtual), não com o do token. Um `.exe` dentro do perfil do `mestre-agente` (onde os instaladores oficiais do `agy` e do `codex` instalam e se atualizam; o `claude` também, em `~\.local\bin`) dá `ERROR_ACCESS_DENIED` ao `mestre`, mesmo com o token certo. A ACL do perfil é só SYSTEM, Administradores e `mestre-agente`. Isso não apareceu na rodada 1 porque lá o `agy` estava copiado em `%ProgramData%`. Funciona de duas formas: (a) a raiz do terminal é um executável que a conta virtual lê (`cmd.exe /c …`, ou um binário do próprio Mestre) e ele inicia o CLI já como `mestre-agente`; (b) a conta virtual ganha RX (herdável) na pasta `bin` do CLI. A travessia das pastas acima vem do `SeChangeNotifyPrivilege`, que o serviço já tem.

### A.3 Renovação do token (hipótese da rodada 1)

As rodadas 1 a 8 (23:06–23:17) caíram dentro da validade do token de acesso (~1 h após o login), então nenhuma renovou. **A hipótese "o sumiço foi uma renovação que falhou" não foi testada nesta rodada.** O teste estava pronto: às 00:15, primeiro a variante (ii), para que a renovação passasse por ela, depois a (i). Ele foi cancelado a pedido do Dono, para não esperar ~1 h. A renovação não dá para forçar sem mexer na credencial guardada.

O que esta rodada mostra: sem renovação, a variante (ii) lê a credencial e a mantém. A diferença com a rodada 1, onde a primeira execução pela (ii) falhou, não vem da variante em si, porque a sessão de logon, o perfil carregado e o ambiente são os do iniciador nas duas. Sobram a renovação ou algo da rodada 1 que não se repetiu (lá o `agy` rodava de uma cópia em `%ProgramData%`, que não se atualizava).

### A.4 Conclusão

**A variante "entrega do token" serve para o Antigravity**, desde que a raiz do processo seja um executável que a conta virtual do `mestre` consiga abrir (A.2). Três execuções seguidas pela (ii), cada uma numa sessão de logon nova, mantiveram a credencial `gemini:antigravity` e responderam `/usage`. O plano B completo (o launcher criando o pseudo-console e o processo) não é necessário para o `agy` com base nesta rodada. Fica aberta só a renovação do token pela variante (ii), que cabe no item que já existe em `antigravity.md` ("o `agy` renova sozinho o token de login, que vale 1 h"; suíte pesada, personagem ativo por mais de 1 h), rodando no Windows pela entrega do token.

## Tarefa B — identificação do chamador: árvore FALHOU, Job Object PASSOU

### B.1 Montagem

- `callerid install` aponta o serviço `MestreSpike` para o `callerid.exe`. É o mesmo serviço da tarefa A (conta virtual, privilégios declarados, aceito pelo iniciador), sem tocar no `01-agent-service`; `callerid restore` desfaz.
- O serviço pede o token ao `MestreSpikeLauncher` (a entrega adotada) e abre dois terminais como `mestre-agente` (ConPTY, raiz `cmd.exe /q /k`): **T1** num Job com `KILL_ON_JOB_CLOSE`; **T2** num Job com `KILL_ON_JOB_CLOSE | BREAKAWAY_OK`. Ele guarda o PID, o horário de criação e o handle do Job de cada um.
- Servidor no pipe `\\.\pipe\mestre-spike-callerid` (ACL: SYSTEM, Administradores, o serviço e `mestre-agente`). Para cada cliente, pega o PID por `GetNamedPipeClientProcessId` e roda quatro métodos:
  1. **árvore (Toolhelp):** PIDs dos pais pelo snapshot, horário de criação por `OpenProcess` + `GetProcessTimes`;
  2. **árvore (NtQuerySystemInformation):** pais e horários de criação numa única chamada, sem abrir processo;
  3. **Job (`IsProcessInJob`):** com o handle do chamador e o de cada Job;
  4. **Job (lista de PIDs):** o PID em `JobObjectBasicProcessIdList` de cada Job.
- A árvore segue `seguranca.md`: um pai que começou depois do filho indica PID reaproveitado e faz recomeçar, até 3 vezes.
- O chamador é o `callerid probe`, digitado em cada terminal pelo `callerid run`. Cada caso espera o resultado com prazo de 40 s, sem sleep; as esperas reais ficam no log.
- Duas rodadas: **DACL padrão** do token (`b-dacl-padrao.md`/`.json`) e **com o serviço na DACL padrão** (`b-dacl-com-servico.md`/`.json`).

### B.2 Resultado por caso

DACL padrão do token entregue pelo iniciador: `D:(A;;GA;;;<mestre-agente>)(A;;GA;;;SY)(A;;GXGR;;;<SID de logon>)`. A conta virtual não entra nela, então **`OpenProcess` dá "Acesso negado"** em todo processo do agente. Os métodos 1 e 3 recusam tudo. Na segunda rodada, o serviço acrescentou `(A;;0x101000;;;NT SERVICE\MestreSpike)` (`PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE`) com `SetTokenInformation(TokenDefaultDacl)` antes do `CreateProcessAsUser`. Os descendentes herdam a ACE, porque são criados com o mesmo token.

| Caso | Origem real | Árvore Toolhelp (padrão / com ACE) | Árvore NtQSI | `IsProcessInJob` (padrão / com ACE) | Lista de PIDs do Job |
|---|---|---|---|---|---|
| direto (`callerid probe`) | T1 | recusa / **T1** | **T1** | recusa / **T1** | **T1** |
| `cmd /c` | T1 | recusa / **T1** | **T1** | recusa / **T1** | **T1** |
| `powershell -c` | T1 | recusa / **T1** | **T1** | recusa / **T1** | **T1** |
| processo que cria um Job próprio (aninhado) | T1 | recusa / **T1** | **T1** | recusa / **T1** | **T1** |
| órfão: `cmd /c start "" /b …` (o `cmd` sai) | T1 | recusa / recusa | **recusa** | recusa / **T1** | **T1** |
| órfão: `powershell Start-Process -NoNewWindow` (o `powershell` sai) | T1 | recusa / recusa | **recusa** | recusa / **T1** | **T1** |
| `CREATE_BREAKAWAY_FROM_JOB`, Job **sem** `BREAKAWAY_OK` | T1 | o `CreateProcess` falha com "Acesso negado"; o pai é identificado como T1 pelos métodos possíveis | | | |
| direto em T2 | T2 | recusa / **T2** | **T2** | recusa / **T2** | **T2** |
| filho com breakaway, Job **com** `BREAKAWAY_OK` (pai vivo) | T2 | recusa / **T2** | **T2** | recusa / recusa | recusa |
| filho com breakaway, pai já saiu | T2 | recusa / recusa | recusa | recusa / recusa | recusa |
| processo criado pelo WMI (`Win32_Process.Create`) a partir de T1 | T1 | recusa / recusa | recusa | recusa / recusa | recusa |
| fora: console de Administrador | — | recusa (certo) | recusa (certo) | recusa (certo) | recusa (certo) |
| fora: `mestre-agente` noutro Job (`spikesvc open -svc launcher`) | — | recusa (certo) | recusa (certo) | recusa (certo) | recusa (certo) |

- **Nenhum método atribuiu um chamador ao terminal errado** em nenhum caso; quando erram, recusam (falha segura).
- **A árvore falha para órfãos no Windows**, como no Linux. O Windows não reparenta: o PID do pai fica apontando para um processo morto (`parent … is not running`).
- **O Job pega todos os casos de dentro do terminal**, inclusive os órfãos e o Job aninhado (jobs aninhados, Windows 8+: o processo continua no Job do terminal).
- **Breakaway:** sem `JOB_OBJECT_LIMIT_BREAKAWAY_OK`, o `CREATE_BREAKAWAY_FROM_JOB` é negado, e o Job fica fechado. Com ele, o filho sai do Job e só a árvore o acha enquanto o pai vive. Os Jobs dos terminais não devem ter `BREAKAWAY_OK` nem `SILENT_BREAKAWAY_OK`.
- **WMI escapa do Job e da árvore:** o `mestre-agente` (usuário comum, logon em lote, sessão 0) consegue `Win32_Process.Create`. O processo nasce filho do `WmiPrvSE`, como `mestre-agente`, fora do Job. Para a identificação é uma recusa (segura). Mas ele **sobrevive ao `TerminateJobObject`**: um `cmd … & ping -n 20` criado assim continuava vivo, com dono `mestre-agente`, depois que o Job do terminal foi encerrado (`b-wmi-sobrevive-ao-job.txt`). É um furo do encerramento (abaixo), não da identificação.

### B.3 Custo por identificação (µs, mín. / mediana / máx. sobre os casos; ~150 processos no Sandbox)

O relógio do Go no Windows tem passo de ~0,5 ms, então cada método foi repetido até somar ≥ 20 ms e o custo é a média.

| Método | DACL padrão | Com a ACE do serviço |
|---|---|---|
| Lista de PIDs do Job | 0,4 / 1,9 / 3,9 | 0,7 / 1,6 / 4,4 |
| `IsProcessInJob` (inclui `OpenProcess`) | 18,6 / 34,2 / 172 (falha no `OpenProcess`) | **1,2 / 3,9 / 61** |
| Árvore NtQuerySystemInformation | 552 / 1259 / 3111 | 1146 / 1397 / 2887 |
| Árvore Toolhelp + `GetProcessTimes` | 1555 / 4331 / 8480 (falha no `OpenProcess`) | 4370 / 5745 / 8015 |

### B.4 Conclusão

**O caminho que funcionou em todos os casos é o Job Object (plano B), com o handle do Job que o motor já guarda.** A forma recomendada:
1. antes do `CreateProcessAsUser`, acrescentar à DACL padrão do token entregue uma ACE de `NT SERVICE\Mestre` com `PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE`;
2. no pipe, ler o PID por `GetNamedPipeClientProcessId`, abrir o processo (`OpenProcess`) e recusar se ele começou depois de a conexão ser aceita (PID reaproveitado);
3. aceitar se `IsProcessInJob` dá verdadeiro para o Job de exatamente um terminal registrado.

O custo mediano é ~4 µs. A lista de PIDs do Job (~2 µs) funciona sem a ACE, mas não tem handle que fixe a identidade do processo, então fica só como alternativa. A árvore sai do Windows: falha nos órfãos e custa de 1 a 6 ms.

## O que precisa mudar nos documentos

1. `docs/desenvolvimento/ordem-de-implementacao.md`, tabela "Spikes do M1", linha "identificação do chamador subindo a árvore de processos, no Windows: **aberto**" → **feito (2026-10-12)**: a árvore falhou para órfãos; adotado o Job Object por terminal (`IsProcessInJob`, ACE do motor na DACL padrão do token).
2. `docs/arquitetura/seguranca.md`, "Identidade de quem chama a CLI", item **Windows**: trocar a subida pela árvore pelo Job Object (B.4: ACE na DACL padrão, conferência do horário de criação contra a conexão, `IsProcessInJob` com o Job do terminal) e acrescentar o "Resultado do spike no Windows" com os casos e custos de B.2–B.3. O item "Verificar na implementação" do Windows (linha 118) fica fechado pelo spike. A linha da tabela de ameaças (linha 23, "no Windows, pela árvore de processos") passa a "pelo Job Object do terminal".
3. `docs/arquitetura/canal-local.md`, "Identificação e versão" (linhas 66–67): no Windows, `GetNamedPipeClientProcessId` + `IsProcessInJob`; a regra "pai mais novo que o filho em 3 tentativas" vale só para o Linux sem cgroup; acrescentar o critério de recusa "processo criado depois da conexão". `docs/cli/convencoes.md`, linha 49: "árvore de processos no Windows" → "Job Object do terminal no Windows".
4. `docs/arquitetura/terminal.md`, linha 16 (Criação, Windows): o Job do terminal **sem** `JOB_OBJECT_LIMIT_BREAKAWAY_OK` e sem `SILENT_BREAKAWAY_OK` (com eles, o filho sai do Job e escapa da identificação e do `TerminateJobObject`).
5. `docs/arquitetura/windows.md`, "ConPTY" (sequência): acrescentar o passo "ACE do `NT SERVICE\Mestre` na DACL padrão do token antes do `CreateProcessAsUser`" e a regra de que a **raiz do terminal é um executável que a conta virtual lê** (achado de A.2: `cmd.exe /c <cli>` ou um binário do Mestre; ou RX da conta virtual na pasta `bin` de cada CLI no perfil do `mestre-agente`, o que mexe numa pasta fora das do Mestre). O item "CLIs" da conta `mestre-agente` (os instaladores instalam no perfil) precisa citar essa regra.
6. `docs/arquitetura/windows.md`, "Operador do PC": a CLI do Operador usa "a identificação do chamador pela árvore de processos" → pelo Job Object. O Job é criado pelo processo da sessão (conta do usuário), então ele precisa entregar ao `mestre` um handle do Job (`DuplicateHandle`) ao abrir cada terminal.
7. `docs/arquitetura/windows.md`, "Verificar na implementação", 1.º item (`agy` com o token entregue) e `docs/provedores/antigravity.md`, linha 132: o "Padrão: mantém" se confirmou sem renovação (3/3 pela entrega do token; A.2). O item pode fechar, desde que a renovação pela entrega do token passe para o item da linha 133 (renovação em 1 h, suíte pesada), com a nota "no Windows, pelo token entregue pelo `mestre-launcher`". O texto do "Se falhar" do 1.º item de `windows.md` (o launcher criar o pseudo-console para o `agy`) fica como plano daquele item. Na tabela de `ordem-de-implementacao.md`, a linha dos CLIs no Windows perde "fica um item do `agy`" ou passa a apontar para a renovação.
8. **Novo item de "Verificar na implementação"** (`docs/arquitetura/terminal.md#encerramento` ou `seguranca.md`): no Windows, um agente sai do Job do terminal por `Win32_Process.Create` (WMI), e o processo sobrevive ao `TerminateJobObject`. **Padrão sugerido:** no `release`, o motor (ou o launcher) encerra todo processo restante da sessão de logon entregue (token com aquele `AuthenticationId`), e o checklist confere. **Se falhar:** negar ao `mestre-agente` a execução de métodos no namespace WMI `root\cimv2`. **Onde:** spike curto no Sandbox: ver se o processo criado pelo WMI tem a mesma sessão de logon do terminal. Equivalente no Linux: o `systemd-run --user` já está coberto pela falta do gerenciador de usuário.

## `go vet` e `go test` (Windows)

- `go vet ./...`: sem achados (exit 0). `go-vet.txt`.
- `go test ./...`: todos os pacotes `ok`; os dois spikes não têm testes (exit 0). `go-test.txt`.
