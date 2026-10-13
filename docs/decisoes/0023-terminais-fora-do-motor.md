# ADR 0023: Terminais fora do motor (`mestre-pty`)

> **Assunto:** por que cada terminal roda num processo próprio, o `mestre-pty`, que sobrevive ao reinício e à atualização do motor, e por que sem tmux nem screen · **Componente:** Motor · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-13, por decisão do Dono, como fundação do M1. Revê o [ADR 0001](0001-stack-go.md) (a biblioteca de PTY passa a ser `aymanbagabas/go-pty` nos dois sistemas, e o ConPTY roda no `mestre-pty`, não no serviço) e o [ADR 0019](0019-autoatualizacao-por-releases.md) (a atualização do motor não espera mais o ponto seguro dos terminais). O comportamento está no documento dono, [terminal](../arquitetura/terminal.md).

## Contexto

- O terminal vivia dentro do motor: qualquer reinício do motor (atualização, queda, cura) matava os agentes da máquina, que voltavam pela retomada, com perda do contexto que o provedor não guarda.
- A autoatualização tinha de esperar o ponto seguro de cada personagem, até 30 min, com adiamentos; a autocura interrompia o trabalho de todos para consertar o motor.
- O tmux (ou o screen) resolveria a sobrevivência, mas tira os processos do cgroup do terminal (LIN-14 do [checklist](../plataforma/checklist.md)), o que quebra a identificação do chamador no Linux, e não existe no Windows.

## Opções consideradas

1. **Terminal no motor** (o desenho anterior). Simples, um processo só; mas todo reinício do motor derruba os agentes.
2. **tmux/screen como dono do PTY.** Pronto e conhecido; mas sai do cgroup, não existe no Windows e põe um programa de terceiros entre o motor e a tela.
3. **Uma unidade transitória do systemd por terminal** (`mestre-pty-<id>.scope` numa `mestre-terminais.slice`, criada pelo D-Bus do sistema), em vez de `KillMode=process` no `mestre`. Avaliada em 2026-10-13 e recusada: até o systemd 256 (o do Ubuntu 24.04 é o 255), o `StartTransientUnit` não passa o nome da unidade ao polkit, então a regra que deixasse a conta `mestre` criar esses scopes a deixaria criar qualquer unidade, inclusive um serviço como root; mesmo no 257 ou maior, um scope adota PIDs que já existem (perde-se o `CLONE_INTO_CGROUP`, o processo nasce fora e é movido) e encerrar exigiria também `KillUnit` pelo polkit. O `KillMode=process` deixa o aviso do systemd "left-over process" a cada subida da unidade, um por processo dos terminais; por isso ele foi para o launcher, que só reinicia quando a versão dele muda.
4. **Um processo pequeno por terminal, do próprio Mestre** (escolhida). O motor pode reiniciar à vontade; o mesmo código nos dois sistemas; custo: um processo a mais por terminal, um protocolo local e o cuidado de isolá-lo dos agentes, que usam a mesma conta.

## Decisão

- Cada terminal roda no seu `mestre-pty`, com a conta `mestre-agente`, dentro do agrupamento do terminal: segura o PTY ou ConPTY, o agente, o emulador e o buffer. O motor decide, digita, grava o log e encerra; ao voltar, reconecta e reconcilia pelo registro `terminals`.
- Socket Unix nos dois sistemas, só ao alcance da conta do motor, com aperto de mão por um segredo por terminal; processo protegido dos agentes.
- Versão própria do `mestre-pty`, lado a lado por pasta; protocolo só aditivo; terminais antigos seguem na versão em que nasceram.
- Nos dois sistemas, quem inicia o `mestre-pty` é um serviço mínimo, o `mestre-launcher`, e o motor não troca de conta. No Linux, o launcher é o único com `CAP_SETUID` e `CAP_SETGID` (que permitem virar qualquer conta, inclusive root; antes estavam no motor, exposto ao Telegram, ao GitHub e ao painel) e tem o cgroup delegado e `KillMode=process`; o motor reinicia com o `KillMode` padrão sem tocar nos terminais. No Windows, o launcher inicia o `mestre-pty` fora de qualquer Job do motor. Decidido pelo Dono em 2026-10-13, depois da avaliação da opção 3.

## Consequências

- Atualizar ou curar o motor não interrompe os agentes; só o reinício da máquina ou a morte do `mestre-pty` levam à retomada.
- O log fica no motor, alimentado pelo buffer: o mascaramento usa o cofre, que o `mestre-agente` não vê, e um arquivo da conta dos agentes seria alterável por eles.
- Um binário a mais (`mestre-pty`) e um spike a mais no Windows (spike 5: launcher, ConPTY, ACL do socket, reinício com o agente vivo).
- Nasce o `mestre attach`, para uma pessoa abrir o terminal de um agente no próprio terminal ([attach](../cli/attach.md)).
