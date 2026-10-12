# Componentes

> **Assunto:** Core, Node, Guardião, CLI e painel: binários, subcomandos, serviços, contas e portas · **Componente:** Todos · **Atualizado:** 2026-10-12

Este documento é a **fonte única** dos nomes de binários, subcomandos de serviço, serviços do sistema e portas. A escolha da stack está no [ADR 0001](../decisoes/0001-stack-go.md).

## Componentes

| Componente | O que faz | Onde roda |
|---|---|---|
| **Motor do Core** | fonte da verdade: workspaces, mesas, missões, esperas, rituais, decretos, aprovações; espelhos dos repositórios; GitHub e Telegram; cofre; serve o painel e a API; recebe os nós | Core (Linux ou Windows) |
| **Nó local do Core** | roda o Grão-Mestre e os agentes da máquina do Core como qualquer nó: no Linux, dentro do processo do motor do Core; no Windows, no serviço `mestre-node` separado ([Windows](windows.md#windows-como-core)) | Core |
| **Motor do nó** | registra o nó e suas capacidades, roda terminais (PTY/ConPTY), mantém os worktrees das mesas daquele nó, encaminha a CLI, guarda cache local | cada nó (Linux ou Windows) |
| **Guardião** | vigia e conserta o motor da própria máquina; lista fechada de ações; página reserva e Telegram em modo reduzido ([Guardião](../autocura/guardiao.md)) | todas as máquinas |
| **CLI `mestre`** | comandos dos agentes e das pessoas; fala com o motor local ([convenções da CLI](../cli/convencoes.md)) | todas as máquinas (no PATH, e em `MESTRE_CLI`) |
| **Despachante de hooks** | `mestre hook`: recebe os hooks dos provedores e aplica as regras ([adaptador](../provedores/adaptador.md)) | onde houver agentes |
| **Painel** | React + TypeScript + Vite, embutido no binário e servido pelo motor do Core ([ADR 0017](../decisoes/0017-canvas-react-flow.md)); a única interface gráfica do Mestre ([ADR 0020](../decisoes/0020-quatro-usos-core-em-linux-ou-windows.md)) | navegador |
| **whisper-server** | processo auxiliar (whisper.cpp) supervisionado pelo motor, para transcrever áudio ([ADR 0016](../decisoes/0016-whisper-cpp-local.md)) | nó com a capacidade de transcrição; padrão: o Core, por CPU |

## Binários

Dois binários por plataforma (Linux amd64 e Windows amd64), compilados com `CGO_ENABLED=0`:

### `mestre`

| Subcomando | Plataforma | Papel |
|---|---|---|
| `mestre core` | Linux, Windows | motor do Core (serviço `mestre-core`), com o painel e a API; no Linux, também o nó local |
| `mestre core setup-code` | Linux, Windows | gera um novo código de configuração do Dono (invalida o anterior) quando o da instalação se perdeu; só root ou administrador na máquina do Core e só enquanto não existe usuário ([login](../painel/login.md#assistente-do-dono-primeira-vez)) |
| `mestre core export-key --out <arquivo>` | Windows | grava a chave do cofre decifrada num arquivo, para a cópia de segurança fora da máquina (o arquivo protegido por DPAPI só abre nesta máquina); só administrador na máquina do Core ([cofre](../plataforma/cofre.md#chave-do-cofre)) |
| `mestre node` | Linux, Windows | motor de nó (serviço `mestre-node`); no Core Windows, o nó local |
| `mestre node pair --invite <código> [--core <endereço>]` | Linux, Windows | pareia o nó com o Core usando o código de convite; usado pelo instalador e para repetir o pareamento; só administrador/root local ([pareamento](../conectividade/pareamento.md#convite)) |
| `mestre hook --provider <p> --event <e>` | todas | despachante de hooks; uso interno dos arquivos de hooks ([adaptador](../provedores/adaptador.md)) |
| `mestre install core\|node`, `mestre uninstall` | Linux, Windows | registram ou removem os serviços (motor e Guardião) conforme o papel escolhido no instalador; comportamento em [instalação e atualização](../plataforma/instalacao-e-atualizacao.md) |
| `mestre version` | todas | versão do binário e versões de protocolo suportadas |
| demais (`list`, `ask`, `note`, `done`, `table`, `gh`, …) | todas | CLI dos agentes e das pessoas; sintaxe em [cli](../cli/convencoes.md) |

Regra: `core`, `node` (com os subcomandos, inclusive `export-key`), `install` e `uninstall` recusam rodar quando `MESTRE_AGENT_ID` está no ambiente (um agente nunca inicia ou instala serviços).

### `mestre-guardiao`

Binário separado, com código e versão próprios ([ADR 0003](../decisoes/0003-autocura-com-guardiao-sem-ssh.md)). Não importa nenhum pacote do motor ([convenções](../desenvolvimento/convencoes.md)).

| Subcomando | Papel |
|---|---|
| `mestre-guardiao run` | serviço do Guardião (Core ou nó; o papel vem da configuração) |
| `mestre-guardiao version` | versão do Guardião e do protocolo dele |
| `mestre-guardiao apply-update` | troca de binário da atualização: confere de novo a assinatura, guarda a versão anterior e reinicia o serviço alvo; só é chamado pelo ajudante `mestre-update` ([autoatualização](../plataforma/autoatualizacao.md#um-passo-do-motor)) |

O agente de diagnóstico (camada 4) é iniciado pelo Guardião a partir de uma ficha; não é um binário do Mestre ([diagnóstico com IA](../autocura/diagnostico-com-ia.md)).

## Serviços e contas (padrão)

**Princípio, nos dois sistemas:** nenhum agente roda com a conta dona do cofre ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)). Os quatro usos (só Linux, Linux + Windows, Windows + Linux, só Windows) combinam as linhas abaixo ([ADR 0020](../decisoes/0020-quatro-usos-core-em-linux-ou-windows.md)).

| Sistema | Papel | Serviço | Executa | Conta | Supervisionado por |
|---|---|---|---|---|---|
| Linux | Core | `mestre-core` (systemd, `Type=notify`, watchdog) | `mestre core` (com o nó local) | `mestre`; agentes como `mestre-agente` | systemd e Guardião |
| Linux | nó | `mestre-node` (systemd, `Type=notify`, watchdog) | `mestre node` | `mestre`; agentes como `mestre-agente` | systemd e Guardião |
| Linux | os dois | `mestre-guardiao` (systemd) | `mestre-guardiao run` | root | systemd |
| Linux | os dois | `mestre-update` (systemd, oneshot) | `mestre-guardiao apply-update` | root; só a conta `mestre` pode iniciar | — (uso único por troca) |
| Windows | Core | `mestre-core` (serviço do Windows) | `mestre core` | conta virtual `NT SERVICE\MestreCore`, dona do cofre | recuperação do serviço e Guardião |
| Windows | os dois | `mestre-node` (serviço do Windows) | `mestre node` (no Core, o nó local) | conta do usuário ("Fazer logon como"); os agentes rodam aqui | recuperação do serviço e Guardião |
| Windows | os dois | `mestre-guardiao` (serviço do Windows) | `mestre-guardiao run` | LocalSystem | recuperação do serviço |
| Windows | os dois | `mestre-update` (serviço do Windows, início sob demanda) | `mestre-guardiao apply-update` | LocalSystem; só a conta do motor que conduz a troca pode iniciar (`NT SERVICE\MestreCore` no Core, a do `mestre-node` no nó) | — (uso único por troca) |
| Windows | os dois | tarefa agendada `\Mestre\Diagnostico` | agente de diagnóstico | conta do usuário, conectado ou não | disparada pelo Guardião ([Windows](windows.md#tarefa-mestrediagnostico)) |

Detalhes do Windows (por que o Core tem dois serviços, ACLs, conta do usuário): [Windows](windows.md). As configurações de reinício (Restart=always, watchdog, recuperação do Windows) estão em [camadas](../autocura/camadas.md).

## Portas

Todas configuráveis; os valores abaixo são o **padrão**.

| Porta | Quem escuta | Para quê | Exposição |
|---|---|---|---|
| 7443/tcp | motor do Core | painel (HTTPS), API, WSS dos motores e Guardiões dos nós, WSS do navegador, retorno do manifesto de App ([criação de Apps](../github/criacao-de-apps.md)) | rede local |
| 7444/tcp | Guardião do Core | cura cruzada: os nós chegam ao Guardião com o motor do Core fora | rede local |
| 7445/udp | motor do Core | resposta ao broadcast de descoberta | rede local |
| 5353/udp | Core: Avahi no Linux; mDNS nativo do Windows, registrado pelo Guardião, no Windows | mDNS (nome `.local` e serviços) | rede local |
| 7446/tcp | Guardião de cada nó; no Core, só no modo máquina única | página reserva | só rede local |
| 7447/tcp | whisper-server | transcrição | só `127.0.0.1` |

- Nenhuma porta é exposta na internet. O motor do nó não escuta em rede.
- Mecânica da descoberta: [descoberta](../conectividade/descoberta.md). Firewall conferido pelo [checklist](../plataforma/checklist.md). Modo máquina única: [camadas](../autocura/camadas.md#modo-máquina-única).

## Canais locais (padrão)

| Canal | Linux | Windows | Acesso |
|---|---|---|---|
| CLI → motor | socket `/run/mestre/cli.sock` | named pipe `\\.\pipe\mestre-cli`, servido pelo `mestre-node` (também no Core) | Linux: dono `mestre`, grupo `mestre-agente`, modo 0660. Windows: a conta do `mestre-node` |
| Motor ↔ Guardião | socket `/run/mestre-guardiao/guardiao.sock` | named pipe `\\.\pipe\mestre-guardiao` | só as contas do motor e a do Guardião (no Core Windows, `NT SERVICE\MestreCore` e a do `mestre-node`) |
| Nó local → Core | — (mesmo processo) | WSS com mTLS em `127.0.0.1:7443` | só no Core Windows, como um nó remoto ([Windows](windows.md#windows-como-core)) |

O motor confere quem chama a CLI antes de aceitar o `MESTRE_AGENT_ID` ([segurança](seguranca.md)).

## Quem vigia quem

```mermaid
flowchart LR
  SO[systemd / SCM do Windows] --> G[Guardião]
  SO --> M[motor: mestre-core e/ou mestre-node]
  G -- reinicia, volta versão --> M
  M -- supervisiona --> W[whisper-server]
  M -- inicia e encerra --> T[terminais dos agentes]
```

O Guardião nunca é alterado por uma ação de cura ([Guardião](../autocura/guardiao.md)).
