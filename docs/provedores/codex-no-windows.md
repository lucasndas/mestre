# Codex no Windows

> **Assunto:** o sandbox do Codex no Windows: o modo `unelevated`, o que se mediu dos três modos, a rede sem barreira nativa e o `mxc` como evolução · **Componente:** Motor · **Atualizado:** 2026-10-20

Documento de referência do [Codex](codex.md): ali ficam as flags; aqui, por que o Windows leva uma flag a mais e o que ela custa.

## Modo do sandbox

O motor passa `-c windows.sandbox="unelevated"` em todo início de personagem Codex no Windows (`NativeControls`, [reforço nativo](../classes/reforco-nativo.md)), junto de `-s workspace-write`; nunca `danger-full-access`. A chave `windows.sandbox` é **reservada ao motor**: o `BuildLaunch` recusa a que vier da base (`-c`, `--config`, tabela embutida `windows={…}`), aceita do motor só o valor `"unelevated"`, e recusa o início no Windows sem ela (falha fechada: o modo padrão, `elevated`, não separa os personagens). Um `.codex/config.toml` do repositório não muda a chave de topo, porque o `-c` fica acima de todo arquivo. **Verificado no Windows real (2026-10-20):** nem um perfil do próprio arquivo (`profile` com `[profiles.<p>.windows]`), nem `[windows] sandbox="elevated"`, nem o recurso antigo `[features] elevated_windows_sandbox=true` voltam o modo a `elevated` com o projeto confiado e o `-c` do motor; o comando rodou como o próprio usuário (no caso do perfil, o Codex avisa `Ignored unsupported project-local config keys`). Não é preciso recusar repositórios com essas chaves.

No modo `unelevated` não há `codex-command-runner`: o comando roda como o usuário do terminal, com uma DACL que dá controle só ao **SID de logon do processo que chamou o `codex.exe`** (mais leitura da DACL ao dono). Como o [launcher](../arquitetura/windows.md#iniciador-mestre-launcher) faz um logon em lote por terminal, cada terminal tem o seu SID de logon, e o isolamento entre personagens Codex é o mesmo dos outros provedores.

## Medições (Codex 0.163.0, Windows 10.0.26100, medições de 2026-10-19 e 20)

| Modo | Resultado |
|---|---|
| `elevated` (o padrão do Codex) | **Não isola.** O `codex-command-runner` roda como o usuário de sandbox da **máquina** (`CodexSandboxOffline`), com DACL que concede a esse usuário; um segundo Codex abre o runner e, por ele, chega ao comando. O `connect()` ao socket do canal local só passa com o SID sintético `workspace_by_cwd` do `cap_sid`, interno e frágil. Vários Codex na mesma máquina não ficariam separados. |
| `unelevated` (**adotado**) | Sem runner. Um terminal não abre os processos de outro (logons distintos). Contenção de arquivos vale: escrita no worktree de outro personagem e fora de qualquer workspace **negada**, no próprio **permitida**. **A rede fica aberta** (sem barreira nativa). O `connect()` ao socket do canal local só passa com a ACE do SID de logon do terminal no socket e na pasta `run\` ([ACL do socket](../arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty)). |
| `mxc` | Isola até no mesmo logon e corta a rede, loopback inclusive; mas o socket só conecta com `ALL APPLICATION PACKAGES`, porque o SID de pacote muda a cada execução. Fica para depois (abaixo). |

## Canal local

A CLI `mestre` que a ferramenta de shell do Codex chama roda no token restrito do sandbox. O `connect()` ao `cli.sock` só passa se o SID de logon do terminal (o do processo que chamou o `codex.exe`) tiver leitura e escrita no arquivo do socket e na pasta `run\`: o motor acrescenta essa ACE enquanto o terminal vive (na pasta só o lado de leitura, sem criar arquivo) e a retira ao fim, com o SID tirado do token do launcher, validado (`S-1-5-5-<x>-<y>`) e com falha fechada ([ACL do socket](../arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty)). No `unelevated` o motor só identifica o chamador porque a própria CLI acrescenta, à DACL do processo dela, a ACE `PROCESS_QUERY_LIMITED_INFORMATION` do serviço do motor ([segurança](../arquitetura/seguranca.md#identidade-de-quem-chama-a-cli)); sem ela, a recusa é a de sempre. **Em aberto (M7):** o `connect()` com a pasta só em leitura e execução (a medição usou leitura e escrita; escrita na pasta deixaria um terminal criar o nome `cli.sock` com o motor parado, e foi cortada) não foi medido: no Windows Sandbox o token do `WDAGUtilityAccount` não tem SID de logon, então a sonda deu SKIP. Se não passar, o plano é outro, não devolver a escrita. Só o Codex no `unelevated` precisa da ACE, mas ela vale para todo terminal do Windows (é a SID do próprio terminal).

## Rede

No `unelevated` o sandbox não tem barreira de rede, como no Linux ([canal local](codex.md#canal-local-dentro-do-sandbox)): `sandbox_workspace_write.network_access` vai sempre `true`, a internet de uma classe sem internet fica barrada só pelas regras do despachante, que no Codex falha aberto, e a ficha e o `mestre class check` mostram `CodexNoNativeNetwork` nos dois sistemas ([reforço nativo](../classes/reforco-nativo.md)). O plano B antigo de ligar a rede só como reserva (`CodexOfflineNetwork`) deixou de existir: não resolvia, e a rede é aberta de qualquer modo.

## A verificar no Windows real

Tudo o que o CI não alcança (o isolamento entre terminais da tabela de medições vem das medições do Dono de 2026-10-19 e 20, não desta rodada). A rodada do Windows Sandbox de **2026-10-20** (main `27ac501`, Codex no `unelevated`, `tools/winsandbox/round/run-all.ps1`, item 2) fechou (c) e (d) e parte de (b); o resto fica para a conformidade nas [contas reais](../qualidade/contas-reais.md) (M7, CA-CODEX-WIN-05). O ambiente limita: no Windows Sandbox o token do `WDAGUtilityAccount` **não tem SID de logon** (nenhum `S-1-5-5-*` em `whoami /groups`), então, sem o launcher, a etapa 5 da conformidade falha (`mestre debug` identificado: `false`; o `curl` foi negado) e as sondas que dependem desse SID dão SKIP.

| Item | Resultado |
|---|---|
| (a) `connect()` com a pasta `run\` só em leitura e execução | SKIP: sem SID de logon não há como medir. **M7.** |
| (b) a ACE sai com o terminal; é varrida e reposta quando o motor parte | **Passou** no Windows real: os testes `-tags full` de `winsvc` (`TestTheACEOfTheTerminalIsPutOnTheSocketAndItsFolderAndTakenAway`, `…RefusesWhatIsNotALogonSID`), de `terminal` (`TestTheGrantLeavesWithTheTerminalAndTheEngineAtStartKeepsTheLiveOnes`, `TestTheEngineSweepsBeforeTheSocketAndPutsTheGrantsBackAfter`, `TestTheGrantOfATerminalFailsClosed`) e de `logonsweep`. **Não medido:** a ACE no `cli.sock` com terminais do launcher. **M7.** |
| (c) `WRITE_DAC` da CLI sobre si, e o `IsProcessInJob` | **Passou.** Dentro do `unelevated` o `SetSecurityInfo` de `allowQueryOfSelf` acrescentou a ACE `0x1000`; de fora, na mesma sessão, `OpenProcess(QUERY_LIMITED)` abriu o processo e `IsProcessInJob` deu verdadeiro. |
| (d) `.codex/config.toml` não volta o modo a `elevated` | **Passou** (detalhe na seção de cima). |
| (e) limite de conexões por usuário | SKIP. **Limite conhecido:** no `unelevated` o comando roda com o SID do próprio usuário e divide com a conta a cota de 256 conexões do [canal local](../arquitetura/canal-local.md). **M7.** |
| (f) isolamento entre dois Codex (`Test-CodexCrossIsolation`) | **Não medido:** o Codex B saiu com `0xC0000142` (`STATUS_DLL_INIT_FAILED`), típico de processo em logon S4U sem acesso ao desktop do Sandbox. É limite do ambiente, não resultado de isolamento. **M7** (CA-CODEX-WIN-05). |

## Evolução: `mxc` (M7)

O modo `mxc` dá a barreira de rede e o isolamento que o `unelevated` não dá. Entra quando o Codex expuser o SID do pacote por execução, para o motor pôr a ACE do socket na hora (hoje só `ALL APPLICATION PACKAGES` conecta, o que abriria o socket a qualquer pacote). Destino: M7 ([critérios em marcos posteriores](../desenvolvimento/ordem-de-implementacao.md#critérios-em-marcos-posteriores)).

## Critérios de aceite

- **CA-CODEX-WIN-01** · *rápida* — **Dado** uma máquina Windows, **quando** o reforço nativo monta os argumentos do Codex e o `BuildLaunch` os lê, **então** levam `-c windows.sandbox="unelevated"` e `network_access=true`, a classe sem internet fica marcada sem barreira nativa de rede, e o início sem a chave, com outro valor ou com ela vinda da base é recusado.
- **CA-CODEX-WIN-02** · *rápida* — **Dado** um launcher que informa o SID de logon do terminal e um acesso falso, **quando** o motor cria o terminal, o encerra, volta depois de um reinício ou recebe um SID que não é de logon (`S-1-5-18`, grupo, texto com SDDL) ou um acesso que falha, **então** a ACE é pedida com o SID do launcher e gravada na linha, é retirada ao fim, é varrida antes de o socket ser refeito e reposta depois, só para os terminais vivos, e o terminal com SID inválido ou acesso que falha não inicia.
- **CA-CODEX-WIN-03** · *completa* — **Dado** um arquivo de socket e a pasta dele no Windows, **quando** o motor concede, revoga e varre a ACE de SIDs de logon, **então** cada SID tem uma só ACE, sem herança, no arquivo (leitura e escrita) e na pasta (só leitura e execução), com as edições serializadas, a retirada e a varredura tiram só ACEs de SID de logon, a DACL base e a proteção contra herança ficam como estavam, e o que não é SID de logon é recusado sem tocar na ACL.
- **CA-CODEX-WIN-04** · *rápida* — **Dado** a `mestre` CLI iniciando no Windows com o `MESTRE_ENDPOINT` de uma instância, **quando** ela acrescenta a ACE ao próprio processo, **então** a DACL ganha uma só ACE de permissão com exatamente `PROCESS_QUERY_LIMITED_INFORMATION` para a conta do serviço daquela instância (lida do caminho do socket, nos dois tipos de barra; a principal se o caminho não tiver a forma), repetir não acrescenta outra e as ACEs que havia ficam.
- **CA-CODEX-WIN-05** · *pesada* — **Dado** dois personagens Codex na mesma máquina Windows real, cada um num terminal com o seu logon em lote, **quando** cada um roda a ferramenta de shell do Codex (`unelevated`) para abrir um processo do terminal do outro (`OpenProcess`), escrever no worktree do outro e fora de qualquer workspace, escrever no próprio worktree e chamar `mestre list`, **então** o `OpenProcess` e as escritas alheias são negados, a escrita própria passa, o `connect()` da CLI ao `cli.sock` passa, o motor identifica cada chamador com o terminal certo e a ACE de cada terminal some do socket e da pasta quando ele termina (MVP-6).
