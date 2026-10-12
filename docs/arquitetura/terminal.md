# Terminal

> **Assunto:** pseudo-terminal de cada agente no nó: PTY/ConPTY, emulador de terminal, tamanho fixo, buffer, log, digitação e encerramento da árvore de processos · **Componente:** Node · **Atualizado:** 2026-10-12

## Objetivo

Ser o dono de tudo o que acontece entre o processo de um agente (ou de um terminal simples) e o resto do Mestre: como o terminal nasce, como a tela é lida pelo motor, como a saída chega ao painel, quem pode digitar nele e como ele termina. Cada terminal vive no motor do nó onde roda (`mestre-node`, inclusive no Core: [componentes](componentes.md)).

## Comportamento

### Criação

- Só o motor do nó cria terminais, a pedido do Core (`terminal.open`, [protocolo](../conectividade/protocolo.md)), com a base já resolvida: executável, argumentos, ambiente e pasta ([adaptador](../provedores/adaptador.md)).
- **Linux:** `creack/pty`; o processo nasce com a conta `mestre-agente` ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)) numa sessão nova (`setsid`) e dentro do cgroup v2 próprio do terminal, `term-<id>`, criado sob o cgroup delegado do `mestre-node` (`Delegate=yes`, [componentes](componentes.md#serviços-e-contas-padrão)) e passado ao `clone` (`CLONE_INTO_CGROUP`). O processo já nasce no cgroup, sem janela de corrida, e todo descendente fica nele, mesmo o que troca de pai ou de sessão (`nohup`, double fork, `setsid -f`). O cgroup agrupa a árvore para o encerramento e identifica o terminal no [canal local](canal-local.md#identificação-e-versão).
- **Linux sem cgroup delegado** (suíte completa, que roda como processos comuns, ou LIN-11 do [checklist](../plataforma/checklist.md#itens-linux) falhou): o terminal nasce só na sessão nova, que agrupa a árvore dele.
- **Windows:** wrapper próprio de ConPTY dentro do serviço; o processo nasce com a conta `mestre-agente`, por `CreateProcessAsUser` com o perfil dela carregado ([Windows](windows.md#conpty)); cada terminal entra num Job Object próprio com `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, que agrupa a árvore dele.
- `TERM=xterm-256color` no ambiente de todo terminal.
- **Tamanho:** 200 colunas × 50 linhas (padrão, configurável no nó), definido na criação e **fixo**. O navegador nunca redimensiona: o xterm.js mostra o terminal com rolagem e zoom. Assim vários navegadores veem exatamente a mesma tela, e as telas reconhecidas pelo adaptador não mudam com quem está olhando.
- O terminal é registrado em `terminals` ([persistência](persistencia.md#registro-de-terminais)) antes de o processo começar a rodar.

### Emulador de terminal no nó

- Toda saída do processo passa por um emulador VT no motor do nó (biblioteca Go `github.com/charmbracelet/x/vt`, ver [Verificar](#verificar-na-implementação)), que mantém a tela atual, a posição do cursor e um histórico de 5.000 linhas (padrão).
- O emulador é a única fonte de "tela" do Mestre:
  - `mestre check` e a mensagem `terminal.screen` devolvem o texto da tela (e, com `--lines`, do histórico), sem sequências de controle;
  - a operação `Screens` do adaptador recebe a tela renderizada para reconhecer prontidão e telas interativas ([adaptador](../provedores/adaptador.md#prontidão-e-telas-interativas));
  - "tela estável" é a tela sem mudança por 500 ms (padrão), medidos pelo relógio injetável.

### Buffer, painel e log

- **Buffer circular de 1 MiB** (padrão) por terminal, **no nó**, com os bytes crus e o deslocamento de cada um. O Core não guarda buffer: para mostrar um terminal no painel, ou depois de uma reconexão, ele pede ao nó `terminal.replay` a partir de um deslocamento ([protocolo](../conectividade/protocolo.md#multiplexação-e-prioridade)). Trecho que já saiu do buffer aparece como "trecho perdido".
- **Log completo** de cada terminal no disco do nó, com rotação e mascaramento por linha ([persistência](persistencia.md#logs)). O fluxo ao vivo não é mascarado: é a tela do agente.

### Digitação

- Só o motor digita num terminal. As origens são: entregas da caixa de entrada, só com o alvo livre ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)); entrada crua de `mestre ask --raw` ([CLI](../cli/comunicacao.md#mestre-ask)); respostas a telas reconhecidas pelo adaptador; e a digitação de um usuário pelo painel, conforme o nível dele ([canvas](../painel/canvas.md)).
- Mensagens para agentes são digitadas pela operação `TypeMessage` do adaptador (colagem entre colchetes e tecla de envio do provedor; [adaptador](../provedores/adaptador.md#digitação-de-mensagens)). Entrada crua vai byte a byte, como recebida.

### Encerramento

1. **Gracioso:** o adaptador pede a saída normal do agente (comando do provedor) e o motor espera até 10 s (padrão).
2. **Sinal:** no Linux, `SIGTERM` para o grupo da sessão; no Windows, `ClosePseudoConsole`. Espera mais 10 s.
3. **Forçado:** no Linux, `cgroup.kill` no cgroup do terminal (alcança também quem saiu da sessão com `setsid -f`), espera `populated 0` em `cgroup.events` (por inotify, sem consulta periódica) e remove o cgroup; sem cgroup delegado, `SIGKILL` para o grupo e para todo processo que ainda tenha a mesma sessão. No Windows, `TerminateJobObject`.

- Quando encerrar é seguro (e quando é forçado) é decidido em [personagens](../orquestracao/personagens.md); este documento só garante que nenhum processo da árvore sobra.
- Processo que termina sem pedido: o nó registra o código de saída e informa `terminal.status`; o personagem vai para `interrupted` ([personagens](../orquestracao/personagens.md)).

## Regras e erros

- Saída sem ninguém olhando continua indo para o buffer, o emulador e o log; nada depende de o painel estar aberto.
- Falha ao criar o PTY ou o ConPTY: `error` na resposta ao `terminal.open`, com o motivo, e alerta no [checklist](../plataforma/checklist.md).
- Com cgroup delegado, falha ao criar o cgroup do terminal também é `error` no `terminal.open`: o terminal nunca nasce fora dele.
- Pedido de tamanho vindo do navegador é ignorado (não existe mensagem de redimensionar).
- O emulador nunca bloqueia a leitura do processo: se ficar para trás, a saída continua indo para buffer e log, e a tela é refeita a partir do buffer.

## Critérios de aceite

- **CA-TERM-01** · *rápida* — **Dado** um PTY falso que escreveu 1,5 MiB, **quando** o Core pede `terminal.replay` a partir do deslocamento 0, **então** recebe o trecho a partir de 0,5 MiB e a marca de "trecho perdido" antes dele.
- **CA-TERM-02** · *rápida* — **Dado** a gravação da tela de início de cada provedor, **quando** os bytes passam pelo emulador, **então** o texto da tela tem o prompt de entrada na linha esperada e nenhuma sequência de controle.
- **CA-TERM-03** · *rápida* — **Dado** uma tela que muda a cada 300 ms e depois para, **quando** passam 500 ms de relógio falso sem mudança, **então** a tela é dada como estável uma única vez.
- **CA-TERM-04** · *rápida* — **Dado** dois navegadores abrindo o mesmo terminal com larguras diferentes, **quando** recebem a saída, **então** o tamanho do terminal continua 200×50 e os dois recebem os mesmos bytes.
- **CA-TERM-05** · *completa* — **Dado** um terminal real cujo processo iniciou um neto que ignora `SIGTERM` e, no Linux, outro neto que saiu da sessão com `setsid -f`, **quando** o encerramento chega ao passo 3, **então** nenhum processo da árvore continua vivo, no Linux e no Windows, e no Linux o cgroup do terminal foi removido; o neto com `setsid -f` precisa de cgroup delegado (`systemd-run --user --scope -p Delegate=yes`) e, sem gerenciador systemd de usuário, essa parte é pulada com o motivo.
- **CA-TERM-06** · *completa* — **Dado** um terminal real que imprime um token `ghp_…`, **quando** a linha vai para o log, **então** o arquivo de log tem `[segredo]` no lugar do token e o buffer guarda os bytes como saíram.

## Depende de

- [Dublês](../qualidade/dubles.md) (PTY falso, relógio injetável)
- [Persistência](persistencia.md) · [Windows](windows.md) · [Segurança](seguranca.md) · [Componentes](componentes.md) (`Delegate=yes` no `mestre-node`)

## Verificar na implementação

- **Verificar na implementação:** `github.com/charmbracelet/x/vt` renderiza fielmente as telas de Claude Code, Codex e Antigravity (modo alternativo de tela, regiões de rolagem, caracteres largos). **Padrão:** essa biblioteca. **Se falhar:** `github.com/hinshun/vt10x`, atrás da mesma interface interna do emulador. **Onde:** CA-TERM-02 com as gravações de tela capturadas pelo [teste de conformidade](../provedores/conformidade.md).

O ConPTY com o processo iniciado como `mestre-agente` a partir do serviço é um spike do M1 ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1)); o item está em [Windows](windows.md#verificar-na-implementação).
