# Codex no Windows

> **Assunto:** o sandbox do Codex no Windows: o modo `unelevated`, o que se mediu dos três modos, a rede sem barreira nativa e o `mxc` como evolução · **Componente:** Motor · **Atualizado:** 2026-10-20

Documento de referência do [Codex](codex.md): ali ficam as flags; aqui, por que o Windows leva uma flag a mais e o que ela custa.

## Modo do sandbox

O motor passa `-c windows.sandbox="unelevated"` em todo início de personagem Codex no Windows (`NativeControls`, [reforço nativo](../classes/reforco-nativo.md)), junto de `-s workspace-write`; nunca `danger-full-access`. A chave `windows.sandbox` é **reservada ao motor**: o `BuildLaunch` recusa a que vier da base (`-c`, `--config`, tabela embutida `windows={…}`), aceita do motor só o valor `"unelevated"`, e recusa o início no Windows sem ela (falha fechada: o modo padrão, `elevated`, não separa os personagens). Um `.codex/config.toml` do repositório não muda a chave de topo, porque o `-c` fica acima de todo arquivo. **A verificar no Windows real:** se um perfil do próprio arquivo (`profile` e `[profiles.<p>.windows]`) ou os recursos antigos (`features.elevated_windows_sandbox`) voltam o modo a `elevated`; o repositório de teste da conformidade traz esses casos, e, se algum vencer, o personagem não inicia num repositório com essas chaves.

No modo `unelevated` não há `codex-command-runner`: o comando roda como o usuário do terminal, com uma DACL que dá controle só ao **SID de logon do processo que chamou o `codex.exe`** (mais leitura da DACL ao dono). Como o [launcher](../arquitetura/windows.md#iniciador-mestre-launcher) faz um logon em lote por terminal, cada terminal tem o seu SID de logon, e o isolamento entre personagens Codex é o mesmo dos outros provedores.

## Medições (Codex 0.163.0, Windows 10.0.26100, rodada de 2026-10-20)

| Modo | Resultado |
|---|---|
| `elevated` (o padrão do Codex) | **Não isola.** O `codex-command-runner` roda como o usuário de sandbox da **máquina** (`CodexSandboxOffline`), com DACL que concede a esse usuário; um segundo Codex abre o runner e, por ele, chega ao comando. O `connect()` ao socket do canal local só passa com o SID sintético `workspace_by_cwd` do `cap_sid`, interno e frágil. Vários Codex na mesma máquina não ficariam separados. |
| `unelevated` (**adotado**) | Sem runner. Um terminal não abre os processos de outro (logons distintos). Contenção de arquivos vale: escrita no worktree de outro personagem e fora de qualquer workspace **negada**, no próprio **permitida**. **A rede fica aberta** (sem barreira nativa). O `connect()` ao socket do canal local só passa com a ACE do SID de logon do terminal no socket e na pasta `run\` ([ACL do socket](../arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty)). |
| `mxc` | Isola até no mesmo logon e corta a rede, loopback inclusive; mas o socket só conecta com `ALL APPLICATION PACKAGES`, porque o SID de pacote muda a cada execução. Fica para depois (abaixo). |

## Canal local

A CLI `mestre` que a ferramenta de shell do Codex chama roda no token restrito do sandbox. O `connect()` ao `cli.sock` só passa se o SID de logon do terminal (o do processo que chamou o `codex.exe`) tiver leitura e escrita no arquivo do socket e na pasta `run\`: o motor acrescenta essa ACE enquanto o terminal vive e a retira ao fim, com o SID tirado do token do launcher, validado (`S-1-5-5-<x>-<y>`) e com falha fechada ([ACL do socket](../arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty)). No `unelevated` o motor só identifica o chamador se a própria CLI o deixar abrir o processo dela ([segurança](../arquitetura/seguranca.md#identidade-de-quem-chama-a-cli)). A ACE da pasta dá escrita nela (criar arquivo) a esse terminal, mas não apagar nem trocar o `cli.sock`; reduzi-la ao mínimo que ainda conecta fica para a rodada do Windows.

## Rede

No `unelevated` o sandbox não tem barreira de rede, como no Linux ([canal local](codex.md#canal-local-dentro-do-sandbox)): `sandbox_workspace_write.network_access` vai sempre `true`, a internet de uma classe sem internet fica barrada só pelas regras do despachante, que no Codex falha aberto, e a ficha e o `mestre class check` mostram `CodexNoNativeNetwork` nos dois sistemas ([reforço nativo](../classes/reforco-nativo.md)). O plano B antigo de ligar a rede só como reserva (`CodexOfflineNetwork`) deixou de existir: não resolvia, e a rede é aberta de qualquer modo.

## Evolução: `mxc` (M7)

O modo `mxc` dá a barreira de rede e o isolamento que o `unelevated` não dá. Entra quando o Codex expuser o SID do pacote por execução, para o motor pôr a ACE do socket na hora (hoje só `ALL APPLICATION PACKAGES` conecta, o que abriria o socket a qualquer pacote). Destino: M7 ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#marcos)).

## Critérios de aceite

- **CA-CODEX-WIN-01** · *rápida* — **Dado** uma máquina Windows, **quando** o reforço nativo monta os argumentos do Codex e o `BuildLaunch` os lê, **então** levam `-c windows.sandbox="unelevated"` e `network_access=true`, a classe sem internet fica marcada sem barreira nativa de rede, e o início sem a chave, com outro valor ou com ela vinda da base é recusado.
- **CA-CODEX-WIN-02** · *rápida* — **Dado** um launcher que informa o SID de logon do terminal e um acesso falso, **quando** o motor cria o terminal, o encerra, volta depois de um reinício ou recebe um SID que não é de logon (`S-1-5-18`, grupo, texto com SDDL) ou um acesso que falha, **então** a ACE é pedida com o SID do launcher e gravada na linha, é retirada ao fim, é reposta só para os terminais vivos (o resto é varrido), e o terminal com SID inválido ou acesso que falha não inicia.
- **CA-CODEX-WIN-03** · *completa* — **Dado** um arquivo de socket e a pasta dele no Windows, **quando** o motor concede, revoga e varre a ACE de SIDs de logon, **então** cada SID tem uma só ACE de leitura e escrita, sem herança, no arquivo e na pasta, a retirada e a varredura tiram só ACEs de SID de logon, a DACL base e a proteção contra herança ficam como estavam, e o que não é SID de logon é recusado sem tocar na ACL.
