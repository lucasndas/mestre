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
3. **Um processo pequeno por terminal, do próprio Mestre** (escolhida). O motor pode reiniciar à vontade; o mesmo código nos dois sistemas; custo: um processo a mais por terminal, um protocolo local e o cuidado de isolá-lo dos agentes, que usam a mesma conta.

## Decisão

- Cada terminal roda no seu `mestre-pty`, com a conta `mestre-agente`, dentro do agrupamento do terminal: segura o PTY ou ConPTY, o agente, o emulador e o buffer. O motor decide, digita, grava o log e encerra; ao voltar, reconecta e reconcilia pelo registro `terminals`.
- Socket Unix nos dois sistemas, só ao alcance da conta do motor, com aperto de mão por um segredo por terminal; processo protegido dos agentes.
- Versão própria do `mestre-pty`, lado a lado por pasta; protocolo só aditivo; terminais antigos seguem na versão em que nasceram.
- No Linux, a unidade `mestre` usa `KillMode=process`; no Windows, o `mestre-launcher` inicia o `mestre-pty` fora de qualquer Job do motor.

## Consequências

- Atualizar ou curar o motor não interrompe os agentes; só o reinício da máquina ou a morte do `mestre-pty` levam à retomada.
- O log fica no motor, alimentado pelo buffer: o mascaramento usa o cofre, que o `mestre-agente` não vê, e um arquivo da conta dos agentes seria alterável por eles.
- Um binário a mais (`mestre-pty`) e um spike a mais no Windows (spike 5: launcher, ConPTY, ACL do socket, reinício com o agente vivo).
- Nasce o `mestre attach`, para uma pessoa abrir o terminal de um agente no próprio terminal ([attach](../cli/attach.md)).
