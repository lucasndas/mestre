# CLI: `mestre attach`

> **Assunto:** comando das pessoas para ver, e com nível digitar, o terminal de um personagem de qualquer máquina da Guilda no próprio terminal, como o `tmux attach`: login, tamanho fixo, só leitura, digitação com auditoria e desconectar sem afetar o agente · **Componente:** CLI · **Atualizado:** 2026-10-13

## Objetivo

Deixar uma pessoa abrir o terminal de um agente no terminal dela, de qualquer máquina com o Mestre instalado, e sair sem afetar o agente. É o equivalente, na linha de comando, ao terminal do [canvas](../painel/canvas.md): mesmas regras de quem vê e de quem digita, mesmo caminho pela Cidadela, mesmo tamanho fixo. Não é comando de agente: os agentes leem a tela de outro com `mestre check` ([comunicação](comunicacao.md)).

## Comportamento

- **Sintaxe:** `mestre attach <personagem> [--workspace <nome>] [--write] [--server <endereço>] [--detach-key <tecla>]`. O nome do personagem é o do canvas; existindo em mais de um workspace que a pessoa vê, `--workspace` escolhe.
- **Conexão:** pela Cidadela, como o painel, para qualquer máquina da Guilda: a CLI abre `wss` no endereço do painel (`https://mestre.local:<porta>`, padrão, ou `--server`), confere o certificado pela CA da Guilda que a instalação deixou na pasta de configuração da instância e segue o `307` de uma Torre ou de uma Cidadela suspensa ([painel redirecionado](../guilda/cidadela-e-torres.md#painel-redirecionado)). Não usa o [canal local](../arquitetura/canal-local.md), que é dos agentes.
- **Login:** pede usuário e senha (sem eco), com as regras do [login](../painel/login.md) (mensagem única de erro, bloqueio por tentativas, auditoria). A sessão é marcada como "CLI em <máquina>", fica só na memória do processo e acaba quando ele sai; sessões encerradas pela Cidadela (suspensão, troca de senha) derrubam o `attach` na hora.
- **Quem vê:** quem tem acesso ao workspace do personagem, como no canvas ([quem vê o quê](../arquitetura/seguranca.md#quem-vê-o-quê)).
- **Tela:** a Cidadela pede à máquina do personagem o `terminal.replay` e repassa o fluxo ao vivo ([terminal](../arquitetura/terminal.md#buffer-painel-e-log)); a CLI desenha com o mesmo emulador, no tamanho fixo do terminal (200×50, padrão). Janela menor que isso: a CLI mostra a parte que cabe, seguindo o cursor, com uma linha de estado que diz o tamanho; nunca pede redimensionamento.
- **Só leitura por padrão:** nenhuma tecla vai ao agente, fora a de desconectar.
- **Digitar (`--write`):** só **Administrador ou acima** no workspace, com a mesma regra do canvas ([digitar no terminal](../painel/canvas.md#digitar-no-terminal)): se o personagem está ocupado, a CLI mostra "o agente está trabalhando" e pede confirmação antes de liberar o teclado; a sessão de digitação vai para a auditoria (usuário, personagem, início e fim, sem o conteúdo, que já está no log do terminal). A entrada chega ao agente pelo motor da máquina dele ([digitação](../arquitetura/terminal.md#digitação)).
- **Desconectar:** `Ctrl-]` (padrão; `--detach-key` muda) fecha o `attach` e sai com 0; o agente segue exatamente como estava. O terminal que termina mostra "terminal encerrado" e sai com 0. Conexão perdida: a CLI tenta de novo por até 60 s (padrão) e então sai com 5.
- Linux e Windows, o mesmo código; no Windows, o console em modo VT.

## Regras e erros

Códigos de saída e famílias de [convenções da CLI](convencoes.md#regras-e-erros):

| Situação | Exit | `error.code` |
|---|---|---|
| rodado com `MESTRE_AGENT_ID` no ambiente (um agente) | 3 | `not_for_agents` |
| usuário ou senha inválidos, ou login bloqueado | 3 | `auth_failed` |
| `--write` abaixo de Administrador | 3 | `level_denied` |
| personagem inexistente ou de workspace que a pessoa não vê | 4 | `not_visible` |
| nome em mais de um workspace, sem `--workspace` | 2 | `usage` |
| Cidadela inalcançável, suspensa sem redirecionamento ou em troca | 5 | `citadel_unavailable` ou `citadel_in_failover` |

## Critérios de aceite

- **CA-ATTACH-01** · *completa* — **Dado** um personagem com o agente falso numa Torre de teste e um Aprovador, **quando** ele roda `mestre attach`, **então** vê a mesma tela do emulador da máquina, nenhuma tecla chega ao agente, e `Ctrl-]` sai com 0 com o agente no mesmo estado e PID.
- **CA-ATTACH-02** · *completa* — **Dado** o mesmo personagem `busy`, **quando** um Administrador roda `mestre attach --write`, confirma o aviso e digita, **então** a entrada chega ao agente e a auditoria tem a sessão de digitação com início e fim, sem o conteúdo; **quando** um Aprovador roda com `--write`, **então** sai com 3 `level_denied` e nada é digitado.
- **CA-ATTACH-03** · *rápida* — **Dado** `MESTRE_AGENT_ID` no ambiente, **quando** `mestre attach` roda, **então** sai com 3 `not_for_agents` antes de pedir login.
- **CA-ATTACH-04** · *rápida* — **Dado** uma janela de 120×30 e um terminal de 200×50 com o cursor na linha 45, **quando** a tela é desenhada, **então** aparecem as linhas em volta do cursor e a linha de estado com o tamanho, e nenhum pedido de redimensionar sai da CLI.
- **CA-ATTACH-05** · *completa* — **Dado** um `attach` aberto, **quando** o Dono suspende o usuário, **então** a Cidadela fecha a conexão e a CLI sai com 3.

## Depende de

- [Login](../painel/login.md) · [Canvas](../painel/canvas.md) (regra de digitação)
- [Terminal](../arquitetura/terminal.md) · [Segurança](../arquitetura/seguranca.md) · [Componentes](../arquitetura/componentes.md)
