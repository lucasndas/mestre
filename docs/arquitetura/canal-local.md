# Canal local

> **Assunto:** contrato entre a CLI `mestre` (e o `mestre hook`) e o motor local: endpoint, enquadramento, pedido, resposta, respostas parciais, tempo limite, versão e encaminhamento · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Definir o único caminho pelo qual um agente fala com o Mestre: o processo `mestre` (um comando da CLI ou o despachante `mestre hook`) abre o canal local do motor da máquina onde roda, manda um pedido e recebe a resposta. O motor local é sempre o serviço `mestre`, na Cidadela e nas Torres ([componentes](componentes.md#canais-locais-padrão)). A forma dos comandos e os códigos de saída estão em [convenções da CLI](../cli/convencoes.md).

## Comportamento

### Endpoint

| Sistema | Endereço | Acesso |
|---|---|---|
| Linux | socket Unix `/run/mestre/cli.sock` | dono `mestre`, grupo `mestre-agente`, modo 0660 |
| Windows | named pipe `\\.\pipe\mestre-cli` | ACL só para `NT SERVICE\Mestre` (dono), `mestre-agente` (ler e escrever) e SYSTEM |

- `MESTRE_ENDPOINT`, posto pelo motor no ambiente de cada personagem, tem a forma `unix:///run/mestre/cli.sock` ou `npipe:////./pipe/mestre-cli`. Sem a variável, a CLI usa o endereço padrão do sistema.
- Nenhuma porta TCP. Uma conexão por chamada; o motor atende muitas ao mesmo tempo.

### Enquadramento

- **JSON Lines:** cada mensagem é um objeto JSON em uma linha UTF-8 terminada por `\n`. Máximo de 1 MiB por linha (padrão).
- Conteúdo maior (`--file`, `--attach`) vai como **caminho absoluto**; o motor lê o arquivo no disco da máquina e confere que o chamador pode lê-lo pelas regras da classe ([permissões](../classes/permissoes.md)).

### Pedido

```json
{"v":1,"id":"01J…","agent_id":"01J…","cmd":"ask","args":["Revisor","revise o PR"],"flags":{"timeout":"10m"},"cwd":"/var/lib/mestre/workspaces/…"}
```

| Campo | Conteúdo |
|---|---|
| `v` | versão do canal (1) |
| `id` | ULID gerado pela CLI; é o `request_id` e a chave de idempotência |
| `agent_id` | valor de `MESTRE_AGENT_ID` (vazio se não houver) |
| `cmd` | comando e subcomando, separados por espaço (ex.: `note write`); `hook` para o despachante |
| `args` | argumentos posicionais, como texto; no `hook`, um único objeto: o evento normalizado ([adaptador](../provedores/adaptador.md#formato-interno-normalizado-de-evento)) |
| `flags` | flags já validadas pela definição de comandos, com os nomes longos sem `--` |
| `cwd` | pasta atual do processo chamador |

### Resposta

```json
{"v":1,"id":"01J…","ok":true,"data":{}}
{"v":1,"id":"01J…","ok":false,"error":{"code":"not_connected","message":"…","hint":"…","details":{}}}
```

- `data` tem a forma documentada em cada comando; `error.code` é o de [convenções da CLI](../cli/convencoes.md#códigos-de-saída).
- Campo opcional `exit` (inteiro): o código de saída quando não é o óbvio (ex.: 9 com `ok: true` num pedido que ficou pendente, 6 num `ask` com tempo estourado). Sem `exit`, vale 0 para `ok: true` e a família do `error.code` para `ok: false`.
- Campos novos podem ser acrescentados; um campo existente nunca muda de sentido.

### Respostas parciais

- Comandos longos (o `ask` que espera a resposta) mandam linhas `{"v":1,"id":"01J…","partial":true,"data":{"status":"queued"}}` antes da resposta final: `queued`, `delivered`, `answering` e um sinal de vida a cada 15 s (padrão).
- A CLI não imprime as parciais (só com `--debug`); usa o sinal de vida para saber que o motor está vivo.

### Tempo limite

- O tempo limite do lado da CLI é o do próprio comando (`--timeout` ou o padrão dele) mais 5 s de folga; comandos sem tempo limite próprio usam 30 s (padrão).
- Estourou: a CLI fecha a conexão e sai com 6. O pedido **continua** no motor: a resposta tardia vai para a caixa de entrada de quem pediu ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)).
- O mesmo vale quando o processo da CLI morre (ex.: tempo limite da ferramenta de shell do provedor): conexão fechada não cancela pedido.

### Identificação e versão

- Antes de ler o pedido, o motor identifica o processo do outro lado e o terminal dele, como em [segurança](seguranca.md#identidade-de-quem-chama-a-cli): no Linux, `SO_PEERCRED` e `SO_PEERPIDFD` e o cgroup do terminal em `/proc/<pid>/cgroup` (sem cgroup delegado, a subida pela árvore de processos); no Windows, `GetNamedPipeClientProcessId` e a subida pela árvore. O terminal precisa ser do personagem de `agent_id`; senão, `not_identified`.
- Também é `not_identified`: chamador sem pidfd (já colhido antes de o motor aceitar a conexão), pidfd que deixou de apontar para o mesmo PID depois da leitura do `/proc`, e subida pela árvore que encontrou pai mais novo que o filho em 3 tentativas seguidas.
- `v` desconhecido: resposta `protocol_mismatch` (exit 5). O motor aceita a versão atual e a anterior, como no [protocolo](../conectividade/protocolo.md#abertura-versão-e-capacidades).

### Encaminhamento à Cidadela

- O motor local resolve sozinho `help`, `debug` e `hook`; todo o resto vai à Cidadela na mensagem `cli.call` (na própria Cidadela, a chamada é atendida no mesmo processo, pelo mesmo código), cujo corpo é **o mesmo pedido**, acrescido da identidade já verificada. A Cidadela devolve `cli.result` com **a mesma resposta** (inclusive as parciais), que o motor repassa linha a linha ([mensagens](../conectividade/mensagens.md)).
- Cidadela fora: `citadel_unreachable`, exceto `done`, que o motor guarda e entrega uma vez quando a Cidadela volta ([convenções da CLI](../cli/convencoes.md#encaminhamento-à-cidadela)).

## Regras e erros

| Situação | `error.code` | Exit |
|---|---|---|
| linha maior que o limite | `too_large` | 8 |
| JSON inválido ou campo obrigatório faltando | `bad_request` | 1 (é bug da CLI) |
| processo fora do terminal de `agent_id` (cgroup no Linux, árvore no Windows), chamador já encerrado ou árvore que mudou em 3 tentativas | `not_identified` | 3 |
| motor não aceita a conexão | `engine_unreachable` (gerado pela CLI) | 5 |
| versão do canal não suportada | `protocol_mismatch` | 5 |

- Um pedido com `id` já recebido nunca executa de novo: o motor devolve a resposta guardada.

## Critérios de aceite

- **CA-LOCAL-01** · *rápida* — **Dado** respostas com `ok: true` sem `exit`, com `exit: 9` e com `ok: false` e `error.code` de cada família, **quando** a CLI as recebe, **então** sai com 0, 9 e o código da família, respectivamente.
- **CA-LOCAL-02** · *rápida* — **Dado** um `ask` que recebe três parciais e depois a resposta, **quando** a CLI termina, **então** stdout tem só a resposta final e o código é 0.
- **CA-LOCAL-03** · *rápida* — **Dado** um `ask --timeout 1m` sem resposta, **quando** o relógio falso passa de 1 min e 5 s, **então** a CLI sai com 6 e o pedido continua pendente no motor.
- **CA-LOCAL-04** · *rápida* — **Dado** um `ask` em andamento cuja conexão fecha (CLI morta), **quando** o alvo responde, **então** a resposta vai para a caixa de entrada de quem perguntou.
- **CA-LOCAL-05** · *rápida* — **Dado** o mesmo pedido enviado duas vezes com o mesmo `id`, **quando** o motor recebe o segundo, **então** devolve a resposta do primeiro sem executar de novo.
- **CA-LOCAL-06** · *rápida* — **Dado** um pedido com `v: 99`, **quando** chega ao motor, **então** a resposta é `protocol_mismatch` e a CLI sai com 5.
- **CA-LOCAL-07** · *completa* — **Dado** motor e CLI reais, **quando** um agente falso chama `mestre list` pelo socket (ou pipe), **então** recebe a lista e o motor registrou o pedido com o `agent_id` verificado.
- **CA-LOCAL-08** · *rápida* — **Dado** um `/proc` falso em que o chamador está em `…/term-A`, terminal de outro personagem, e outro chamador em `…/term-B`, terminal do personagem de `agent_id`, **quando** o motor identifica, **então** o primeiro recebe `not_identified` e o segundo é aceito.
- **CA-LOCAL-09** · *rápida* — **Dado** um chamador sem pidfd, e outro cujo pidfd deixa de apontar para o PID depois da leitura do `/proc` falso, **quando** o motor identifica, **então** os dois recebem `not_identified`, mesmo com o cgroup certo.
- **CA-LOCAL-10** · *rápida* — **Dado** um `/proc` falso em que o pai lido começou depois do filho, **quando** o motor sobe a árvore, **então** recomeça do chamador; se a corrida continua em 3 tentativas, responde `not_identified`; se some na segunda, identifica o terminal.
- **CA-LOCAL-11** · *completa* — **Dado** um cgroup delegado real (`systemd-run --user --scope -p Delegate=yes`) e um terminal real cujo processo dispara `nohup mestre list &`, `( mestre list & )` e `setsid -f mestre list` e sai, **quando** os órfãos chamam, **então** os três são aceitos com o terminal certo, e sem o cgroup (só a subida pela árvore) os três recebem `not_identified`; sem gerenciador systemd de usuário, o teste é pulado com o motivo.

## Depende de

- [Terminal](terminal.md) (registro e cgroup de cada terminal)
- [Dublês](../qualidade/dubles.md)
- [Segurança](seguranca.md) · [Componentes](componentes.md)
