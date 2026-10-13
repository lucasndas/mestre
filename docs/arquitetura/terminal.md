# Terminal

> **Assunto:** terminal de cada agente na máquina onde roda: o processo `mestre-pty` (PTY/ConPTY, emulador, buffer), socket e aperto de mão com o motor, volta do motor sem parar os agentes, tamanho fixo, log, digitação e encerramento da árvore de processos · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Ser o dono de tudo o que acontece entre o processo de um agente (ou de um terminal simples) e o resto do Mestre: como o terminal nasce, como a tela é lida, como a saída chega ao painel, quem pode digitar e como ele termina. Cada terminal roda num processo próprio e pequeno, o **`mestre-pty`**, separado do motor (`mestre`) da máquina onde roda, Cidadela ou Torre ([componentes](componentes.md)): **reiniciar, atualizar ou perder o motor não para os agentes**. Só o reinício da máquina ou a morte do `mestre-pty` levam à [retomada](../orquestracao/personagens.md#retomada-após-reinício).

## Comportamento

### `mestre-pty`

- **Um processo por terminal**, binário próprio ([componentes](componentes.md#binários)), com a conta `mestre-agente` ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)) e dentro do agrupamento do terminal: o cgroup `term-<id>` no Linux; no Windows, ele cria e segura o Job Object do terminal. Segura o PTY ou ConPTY, inicia o agente como filho, roda o emulador e mantém o buffer.
- **O agente morre com ele:** no Linux, o filho nasce com `PR_SET_PDEATHSIG` (`SIGKILL`); no Windows, o Job tem `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, e só o `mestre-pty` tem handle dele.
- **Só executa; o motor decide:** o que iniciar, quando e o que digitar, o adaptador, o log e o encerramento continuam no motor. O `mestre-pty` não tem segredo do Mestre (fora o do próprio aperto de mão), não abre conexão de rede e só fala com o motor da própria máquina, pelo socket dele. Sem o motor, ele segue rodando e guardando a saída.
- **Um código só nos dois sistemas:** emulador, buffer, protocolo, aperto de mão, reconciliação e o lado do `attach` são comuns; só iniciar o processo como `mestre-agente`, matar a árvore e identificar um processo pelo horário de início ficam em arquivos por sistema ([convenções](../desenvolvimento/convencoes.md#layout-do-repositório)).
- **PTY:** biblioteca `github.com/aymanbagabas/go-pty` (MIT), com a mesma interface para PTY no Linux e ConPTY no Windows ([Verificar](#verificar-na-implementação)).

### Socket e aperto de mão

- Cada `mestre-pty` escuta num **socket Unix** (`AF_UNIX`, também no Windows 10 1803 ou maior, o mesmo código Go) na pasta `pty` da [instância](componentes.md#instâncias): `/run/mestre/<inst>/pty/<id>.sock` ou `%ProgramData%\Mestre\<inst>\pty\<id>.sock`. Só a conta do motor conecta:
  - **Linux:** o motor cria o socket (dono `mestre`, modo 0600, pasta 0700) e passa ao `mestre-pty` o descritor já escutando; o `mestre-agente` nem atravessa a pasta.
  - **Windows:** o `mestre-pty` cria o socket na pasta, cuja ACL herdável dá controle total a `NT SERVICE\Mestre` e SYSTEM e, a `OWNER RIGHTS`, só ler permissões: o dono do arquivo (`mestre-agente`) não muda a ACL, e nenhum processo `mestre-agente` conecta ([spike 5](windows.md#verificar-na-implementação)).
- **Segredo do terminal:** 32 bytes aleatórios, gerados pelo motor, gravados em `terminals` ([persistência](persistencia.md#registro-de-terminais)) e entregues ao `mestre-pty` na criação por um pipe herdado, nunca em argumento nem em ambiente.
- **Aperto de mão nos dois sentidos:** desafio e resposta HMAC-SHA256 com nonce dos dois lados. O motor só aceita o `mestre-pty` que prova o segredo, e cujo PID do outro lado do socket (`SO_PEERCRED` no Linux, `SIO_AF_UNIX_GETPEERPID` no Windows) é o registrado, com o mesmo horário de início; o `mestre-pty` só aceita o motor que prova o segredo. Falhou: conexão fechada e registro na auditoria.
- **Processo protegido dos agentes:** no Linux, o `mestre-pty` se marca como não depurável (`PR_SET_DUMPABLE` 0): outro processo `mestre-agente` não o depura, não lê a memória nem o ambiente dele e não pega os descritores (`pidfd_getfd`). No Windows, o launcher o cria com um descritor de segurança que dá a `NT SERVICE\Mestre` encerrar e consultar e, a `OWNER RIGHTS`, só consultar. O que sobra a um agente é encerrar o `mestre-pty` do próprio terminal no Linux (mesma conta), o que só derruba o próprio terminal.

### Protocolo motor ↔ `mestre-pty`

- Mensagens de controle em JSON Lines, como no [canal local](canal-local.md), e os bytes do terminal em quadros binários com o deslocamento. Mensagens: `hello` (versão e aperto de mão), `start` (processo do agente: executável, argumentos, ambiente, pasta), `status`, `screen`, `replay` a partir de um deslocamento, `follow` (fluxo ao vivo), `input`, `ack` (até onde o log do motor gravou) e `stop`; eventos `stable` e `exit`.
- **Só aditivo:** campos e mensagens novos podem entrar; nada sai nem muda de sentido. Assim, qualquer motor novo fala com qualquer `mestre-pty` antigo, e um recurso novo só vale em terminais novos.
- **Versão do `mestre-pty`:** própria, gravada em `terminals`, e muda raramente. Cada versão fica na sua pasta ([instalação](../plataforma/instalacao-e-atualizacao.md)): terminal novo usa a mais nova; terminal vivo segue na versão em que nasceu até ser encerrado; versão sem terminal vivo é apagada na atualização seguinte ([autoatualização](../plataforma/autoatualizacao.md)).

### Criação

- Só o motor da máquina cria terminais, a pedido da Cidadela (`terminal.open`, [protocolo](../conectividade/protocolo.md)), com a base já resolvida: executável, argumentos, ambiente e pasta ([adaptador](../provedores/adaptador.md)).
- **Ordem:** (1) grava a linha em `terminals` com socket, segredo e versão; (2) inicia o `mestre-pty`; (3) grava o PID e o horário de início; (4) aperto de mão; (5) `start` do agente. O agente nunca roda sem a linha no banco.
- **Linux:** o motor inicia o `mestre-pty` como `mestre-agente` já dentro do cgroup v2 do terminal, `term-<id>`, criado sob o cgroup delegado do `mestre` (`Delegate=yes`, [componentes](componentes.md#serviços-e-contas-padrão)) e passado ao `clone` (`CLONE_INTO_CGROUP`), sem janela de corrida. O `mestre-pty` inicia o agente numa sessão nova (`setsid`); todo descendente fica no cgroup, mesmo o que troca de pai ou de sessão (`nohup`, double fork, `setsid -f`). O cgroup agrupa a árvore para o encerramento e identifica o terminal no [canal local](canal-local.md#identificação-e-versão). **Sem cgroup delegado** (suíte completa, que roda como processos comuns, ou LIN-11 do [checklist](../plataforma/checklist.md#itens-linux) falhou): só a sessão nova agrupa a árvore.
- **Windows:** o motor pede ao [`mestre-launcher`](windows.md#iniciador-mestre-launcher) que inicie o `mestre-pty.exe` com o token do `mestre-agente`, o perfil dele carregado e fora de qualquer Job Object do motor; o `mestre-pty` cria o ConPTY e o Job do terminal e inicia o agente suspenso, põe no Job e retoma ([ConPTY](windows.md#conpty)).
- `TERM=xterm-256color` no ambiente de todo terminal.
- **Tamanho:** 200 colunas × 50 linhas (padrão, configurável por máquina), definido na criação e **fixo**. Nem o navegador nem o `mestre attach` redimensionam: vários olhando veem a mesma tela, e as telas reconhecidas pelo adaptador não mudam com quem olha.

### Volta do motor

Ao subir (reinício, atualização, queda), o motor não encerra terminais: lê `terminals` e a pasta `pty` e reconcilia nos dois sentidos.

1. **No banco e vivo** (PID com o mesmo horário de início e aperto de mão aceito, inclusive a linha ainda sem PID, quando o motor caiu entre iniciar o `mestre-pty` e gravar): reconecta, pede `screen` e o `replay` desde o último deslocamento gravado no log, e o personagem segue no estado em que estava, sem passar por `interrupted`.
2. **No banco e morto** (processo ausente, PID reaproveitado ou socket sem aperto de mão em 5 s, padrão): o terminal vira `interrupted` e segue a [retomada](../orquestracao/personagens.md#retomada-após-reinício); no Linux, o motor ainda faz `cgroup.kill` e remove o cgroup.
3. **Vivo e fora do banco** (socket na pasta sem linha, ex.: banco restaurado de backup): sem linha não há segredo, o aperto de mão não vale, e o motor encerra o `mestre-pty` e a árvore dele. Como o agente só começa depois da linha gravada, nenhum trabalho se perde.

Enquanto o motor está fora, o agente continua, a saída vai para o buffer, nada é digitado e as chamadas da CLI esperam o motor voltar ([canal local](canal-local.md#motor-reiniciando)).

### Emulador de terminal

- Toda saída do processo passa por um emulador VT no `mestre-pty` (biblioteca Go `github.com/charmbracelet/x/vt`, ver [Verificar](#verificar-na-implementação)), que mantém a tela atual, a posição do cursor e um histórico de 5.000 linhas (padrão).
- O emulador é a única fonte de "tela" do Mestre:
  - `mestre check` e a mensagem `terminal.screen` devolvem o texto da tela (e, com `--lines`, do histórico), sem sequências de controle;
  - a operação `Screens` do adaptador recebe a tela renderizada para reconhecer prontidão e telas interativas ([adaptador](../provedores/adaptador.md#prontidão-e-telas-interativas));
  - "tela estável" é a tela sem mudança por 500 ms (padrão), medidos pelo relógio injetável, e chega ao motor no evento `stable`.
- No Windows, um `ResizePseudoConsole` faz o ConPTY redesenhar a tela inteira, e o texto antigo volta no fluxo: o emulador trata o redesenho como tela refeita, não como saída nova. Por isso o tamanho é fixo e só se define na criação.

### Buffer, painel e log

- **Buffer circular de 1 MiB** (padrão) por terminal, no `mestre-pty`, com os bytes crus e o deslocamento de cada um. A Cidadela não guarda buffer: para mostrar um terminal no painel ou no `attach`, ou depois de uma reconexão, ela pede à máquina `terminal.replay` a partir de um deslocamento ([protocolo](../conectividade/protocolo.md#multiplexação-e-prioridade)). Trecho que já saiu do buffer aparece como "trecho perdido".
- **Log completo** de cada terminal, gravado **pelo motor** no disco da máquina, com rotação e mascaramento por linha ([persistência](persistencia.md#logs)). Fica no motor porque o mascaramento usa os valores do cofre, que o `mestre-agente` nunca vê, e porque um arquivo escrito pela conta dos agentes poderia ser alterado por qualquer agente. O `mestre-pty` guarda, além do buffer, os bytes que o motor ainda não confirmou (`ack`), até 64 MiB (padrão); passou disso, o log marca o trecho perdido. O fluxo ao vivo não é mascarado: é a tela do agente.

### Digitação

- Só o motor digita num terminal, pela mensagem `input`. As origens são: entregas da caixa de entrada, só com o alvo livre ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)); entrada crua de `mestre ask --raw` ([CLI](../cli/comunicacao.md#mestre-ask)); respostas a telas reconhecidas pelo adaptador; e a digitação de um usuário pelo painel ([canvas](../painel/canvas.md#digitar-no-terminal)) ou pelo [`mestre attach`](../cli/attach.md), sempre com a mesma regra de nível e auditoria do canvas.
- Mensagens para agentes são digitadas pela operação `TypeMessage` do adaptador (colagem entre colchetes e tecla de envio do provedor; [adaptador](../provedores/adaptador.md#digitação-de-mensagens)). Entrada crua vai byte a byte, como recebida.

### Encerramento

1. **Gracioso:** o adaptador pede a saída normal do agente (comando do provedor) e o motor espera até 10 s (padrão).
2. **Sinal:** pelo `stop`, o `mestre-pty` manda `SIGTERM` ao grupo da sessão (Linux) ou faz `ClosePseudoConsole` (Windows). Espera mais 10 s.
3. **Forçado:** no Linux, o motor faz `cgroup.kill` no cgroup do terminal (alcança o `mestre-pty` e quem saiu da sessão com `setsid -f`), espera `populated 0` em `cgroup.events` (por inotify, sem consulta periódica) e remove o cgroup; sem cgroup delegado, o `mestre-pty` manda `SIGKILL` ao grupo e a todo processo da mesma sessão e sai. No Windows, o `mestre-pty` faz `TerminateJobObject` (obrigatório: um processo com console próprio e o `conhost.exe` dele sobrevivem ao `ClosePseudoConsole`), encerra o que restou da sessão de logon do terminal (o que escapou pelo WMI, [Windows](windows.md#verificar-na-implementação)) e sai; se não responder, o motor encerra o `mestre-pty` (`TerminateProcess`), e o fechamento do Job leva a árvore.

- Depois, o motor apaga o socket e atualiza a linha. Quando encerrar é seguro (e quando é forçado) é decidido em [personagens](../orquestracao/personagens.md); este documento só garante que nenhum processo da árvore sobra.
- Agente que termina sem pedido: o `mestre-pty` informa o código de saída (`exit`) e sai; o motor manda `terminal.status`, e o personagem vai para `interrupted`.

## Regras e erros

- Saída sem ninguém olhando continua indo para o buffer, o emulador e o log; nada depende de o painel estar aberto.
- Falha ao criar o `mestre-pty`, o PTY ou o ConPTY: `error` na resposta ao `terminal.open`, com o motivo, e alerta no [checklist](../plataforma/checklist.md). Com cgroup delegado, falha ao criar o cgroup do terminal também: o terminal nunca nasce fora dele.
- Pedido de tamanho vindo do navegador ou do `attach` é ignorado (não existe mensagem de redimensionar).
- O emulador nunca bloqueia a leitura do processo: se ficar para trás, a saída continua indo para o buffer, e a tela é refeita a partir dele.

## Critérios de aceite

- **CA-TERM-01** · *rápida* — **Dado** um PTY falso que escreveu 1,5 MiB, **quando** a Cidadela pede `terminal.replay` a partir do deslocamento 0, **então** recebe o trecho a partir de 0,5 MiB e a marca de "trecho perdido" antes dele.
- **CA-TERM-02** · *rápida* — **Dado** a gravação da tela de início de cada provedor, **quando** os bytes passam pelo emulador, **então** o texto da tela tem o prompt de entrada na linha esperada e nenhuma sequência de controle.
- **CA-TERM-03** · *rápida* — **Dado** uma tela que muda a cada 300 ms e depois para, **quando** passam 500 ms de relógio falso sem mudança, **então** a tela é dada como estável uma única vez.
- **CA-TERM-04** · *rápida* — **Dado** dois navegadores abrindo o mesmo terminal com larguras diferentes, **quando** recebem a saída, **então** o tamanho do terminal continua 200×50 e os dois recebem os mesmos bytes.
- **CA-TERM-05** · *completa* — **Dado** um terminal real cujo processo iniciou um neto que ignora `SIGTERM` e, no Linux, outro neto que saiu da sessão com `setsid -f`, **quando** o encerramento chega ao passo 3, **então** nenhum processo da árvore continua vivo, nem o `mestre-pty`, no Linux e no Windows, e no Linux o cgroup do terminal foi removido; o neto com `setsid -f` precisa de cgroup delegado (`systemd-run --user --scope -p Delegate=yes`) e, sem gerenciador systemd de usuário, essa parte é pulada com o motivo.
- **CA-TERM-06** · *completa* — **Dado** um terminal real que imprime um token `ghp_…`, **quando** a linha vai para o log, **então** o arquivo de log tem `[segredo]` no lugar do token e o buffer guarda os bytes como saíram.
- **CA-TERM-07** · *completa* — **Dado** o `mestre-launcher` iniciado como processo comum no CI Windows, configurado para aceitar só a conta do processo de teste e só a conta de agente do teste, **quando** um processo com outra conta abre o pipe, o cliente pede outra conta, outro executável que não o `mestre-pty` instalado ou algo que não seja `start-pty`, **então** cada pedido é recusado e registrado e nenhum processo é criado; **dado** um `mestre-pty` iniciado por ele, **então** está fora do Job do processo de teste, e o perfil é descarregado quando o último `mestre-pty` daquela sessão de logon termina.
- **CA-TERM-08** · *completa* — **Dado** um terminal real com o agente falso numa tela conhecida, **quando** o motor de teste é encerrado à força e iniciado de novo, **então** o agente segue vivo com o mesmo PID, a tela do emulador é igual à de antes, o personagem não passou por `interrupted` e uma digitação depois da volta chega ao agente; no Linux e no Windows (no CI, o `mestre-pty` roda com a conta do teste, sem launcher).
- **CA-TERM-09** · *completa* — **Dado** três terminais: um vivo no banco (e um quarto vivo com a linha ainda sem PID), um no banco cujo `mestre-pty` morreu (e outro com o PID reaproveitado por um processo de outro horário de início) e um socket vivo sem linha, **quando** o motor sobe, **então** os vivos são reconectados, os mortos viram `interrupted` sem que o processo do PID reaproveitado seja tocado, e o sem linha é encerrado sem sobrar processo.
- **CA-TERM-10** · *completa* — **Dado** um terminal criado por um `mestre-pty` da versão anterior, **quando** o motor de versão nova sobe, **então** reconecta, lê a tela, digita e encerra esse terminal, e um terminal novo nasce com a versão nova.
- **CA-TERM-11** · *completa* — **Dado** um terminal real, **quando** um processo sem o segredo conecta ao socket, ou um motor falso com o segredo errado faz o aperto de mão, **então** os dois são recusados e registrados e nada chega ao agente; no Linux, `/proc/<pid>/environ` e `ptrace` do `mestre-pty` são negados a outro processo da mesma conta.

## Depende de

- [Dublês](../qualidade/dubles.md) (PTY falso, relógio injetável)
- [Persistência](persistencia.md) · [Windows](windows.md) · [Segurança](seguranca.md) · [Componentes](componentes.md) (`Delegate=yes` e `KillMode=process` no `mestre`)

## Verificar na implementação

- **Verificar na implementação:** `github.com/charmbracelet/x/vt` renderiza fielmente as telas de Claude Code, Codex e Antigravity (modo alternativo de tela, regiões de rolagem, caracteres largos). **Padrão:** essa biblioteca. **Se falhar:** `github.com/hinshun/vt10x`, atrás da mesma interface interna do emulador. **Onde:** CA-TERM-02 com as gravações de tela capturadas pelo [teste de conformidade](../provedores/conformidade.md).
- **Verificar na implementação:** `github.com/aymanbagabas/go-pty` cobre PTY e ConPTY com o agente falso e os três CLIs (entrada, saída, modo cru, encerramento). **Padrão:** essa biblioteca. **Se falhar:** wrapper próprio do ConPTY sobre `golang.org/x/sys/windows` (e `creack/pty` no Linux), atrás da mesma interface. **Onde:** CA-TERM-05 e CA-TERM-08 no CI dos dois sistemas e o spike 5 ([Windows](windows.md#verificar-na-implementação)).
