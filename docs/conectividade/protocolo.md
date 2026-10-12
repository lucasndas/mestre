# Protocolo entre Core, nós e navegador

> **Assunto:** conexão WSS iniciada pelo nó: endpoints, envelope, versão, ack e idempotência, multiplexação, tipos de mensagem, transferência em partes, Guardião e navegador · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Definir como Core, motores dos nós, Guardiões e o painel conversam. O canal é uma conexão WebSocket segura (WSS), sempre aberta pelo lado de fora (nó ou navegador), em que nenhuma reconexão executa nada duas vezes. O porquê está no [ADR 0002](../decisoes/0002-wss-iniciado-pelo-no.md).

## Comportamento

### Endpoints e portas (padrão)

| Endpoint | Quem conecta | Autenticação |
|---|---|---|
| `wss://<core>:7443/ws/node` | motor de cada nó | mTLS com o certificado do nó ([pareamento](pareamento.md)) |
| `wss://<core>:7443/ws/guardian` | Guardião de cada nó | mTLS com o certificado do Guardião do nó |
| `wss://<core>:7443/ws/ui` | navegador (painel) | cookie de sessão ([login](../painel/login.md)) |
| `https://<core>:7443/pair` | nó em pareamento | código de convite ([pareamento](pareamento.md)) |
| `wss://<core>:7444/ws/guardian-core` | nós, quando o motor do Core está fora | mTLS com o certificado do nó ou do Guardião |

- A porta 7443 é do motor do Core (também serve o painel e a API por HTTPS). A 7444 é do Guardião do Core; quem a usa e quando está em [camadas](../autocura/camadas.md).
- As portas são configuráveis. Tudo é sempre TLS, com a CA interna, inclusive na rede local.

### Envelope

Toda mensagem de controle é um frame de texto com um objeto JSON:

| Campo | Obrigatório | Conteúdo |
|---|---|---|
| `id` | sim | ULID único por remetente. É também a chave de idempotência |
| `type` | sim | `<domínio>.<ação>`, ex.: `terminal.open` |
| `node_id` | sim (nós) | ID do nó da conexão. Nas mensagens do navegador, é omitido |
| `ts` | sim | RFC 3339 em UTC com milissegundos, no relógio do remetente (só informativo) |
| `payload` | sim | objeto, que depende do `type` |
| `reply_to` | não | `id` da mensagem que esta responde (resultado, ack, erro) |
| `ack` | não | `true` quando o remetente exige confirmação |

- **Erro:** `type: "error"`, com `payload` `{code, message, retryable}` e `reply_to`.
- **Tamanho:** o máximo é 1 MiB por mensagem JSON. Conteúdo maior usa a [transferência em partes](#transferência-em-partes).
- **Campos e tipos desconhecidos:** campos são ignorados. Um `type` desconhecido recebe `error` com `code: "unknown_type"`, sem derrubar a conexão.

### Abertura, versão e capacidades

1. O nó conecta e envia `hello` com: faixa de versões do protocolo que entende (`{min, max}`, inteiros; a primeira versão é **1**), versão do binário e [registro e capacidades](#registro-e-capacidades).
2. O Core responde `welcome` com a versão escolhida (a maior em comum), a versão dele e a hora do Core.
   - Sem versão em comum, o Core fecha com o código `4003`, informando a faixa que aceita.
3. **Mudanças compatíveis:**
   - Campo opcional novo e tipo novo **não** mudam a versão.
   - Remover um campo ou mudar o significado de algo exige versão nova.
   - O Core aceita pelo menos a versão atual e a anterior, para conviver com nós ainda não atualizados ([instalação e atualização](../plataforma/instalacao-e-atualizacao.md)).
4. **Relógio:** uma diferença de mais de 5 s entre o relógio do nó e o do Core vira aviso no [checklist](../plataforma/checklist.md).

### Registro e capacidades

- **O que vai no `hello`:** `node_id` e nome; `os` e `arch`; `local` (verdadeiro para o nó local do Core, o `mestre-node` da máquina do Core, que conecta por `127.0.0.1` como qualquer nó); `gpu` e `transcricao` (booleanos); ferramentas com versão (ex.: `claude`, `codex`, `agy`, `git`, `ffmpeg`); perfis de conta do nó; terminais que já estão rodando.
- **Mudanças depois do `hello`:** vão em `node.capabilities`. Exemplo: `transcricao` passa a falso quando o modelo Whisper falha na verificação ([instalação e versões](../plataforma/instalacao-e-atualizacao.md#whisper)).
- **Uso:** o Core escolhe o nó de cada personagem por essas capacidades ([personagens](../orquestracao/personagens.md)).

### Ack e idempotência

- **Remetente:** toda mensagem com `ack: true` é gravada na tabela `outbox` do banco do remetente (Core ou nó, [persistência](../arquitetura/persistencia.md)) até chegar o `ack`. Depois de reconectar, ela é reenviada **com o mesmo `id`** e na ordem original.
- **Destinatário:** guarda os `id`s processados por remetente, com a resposta dada, por 24 horas (padrão). Um `id` repetido devolve a mesma resposta, com `status: "duplicate"`, **sem executar de novo**.
- **O que o ack significa:** `ack` (`payload` `{status: ok|duplicate|error}`) quer dizer "recebido e aceito", não "concluído". A conclusão vem numa mensagem de resultado própria, ligada por `reply_to`.
- A saída de terminal não tem ack. Ela se recupera por replay (abaixo).

### Multiplexação e prioridade

- **Duas classes de frame:** controle (JSON em frame de texto) e dados (frame binário, com saída de terminal e partes de transferência).
- **Prioridade:** o escritor de cada conexão esvazia primeiro a fila de controle e manda no máximo 64 KiB de dados entre duas checagens dessa fila. Assim um terminal barulhento não atrasa heartbeat nem comando.
- **Frame binário:** `[1 byte tipo][16 bytes ID (ULID)][8 bytes posição, big-endian][dados]`.
  - Tipo `0x01`: saída de terminal. O ID é o do terminal e a posição é o deslocamento em bytes.
  - Tipo `0x02`: parte de transferência. O ID é o da transferência e a posição é o índice da parte.
- **Replay de terminal:** o buffer de cada terminal fica só no nó (1 MiB, padrão; [terminal](../arquitetura/terminal.md)). Depois de reconectar, ou quando um navegador abre um terminal, o Core pede `terminal.replay` a partir do deslocamento que precisa. Se o trecho já saiu do buffer, a resposta marca "trecho perdido"; o log completo continua no disco do nó.

### Heartbeat e reconexão

- **Ping:** ping do WebSocket a cada 15 s. Sem pong em 45 s, a conexão é dada como morta.
- **Heartbeat:** `node.heartbeat` a cada 30 s, com CPU, memória, disco livre e número de terminais. Sem heartbeat por 90 s, o Core considera o nó offline ([camadas](../autocura/camadas.md)).
- **Reconexão:** backoff de 1 s, dobrando até 60 s, com variação de ±20%. Depois de 3 falhas seguidas, o nó refaz a [descoberta](descoberta.md).
- **Uma conexão por `node_id`:** uma conexão nova substitui a antiga.
- **Códigos de fechamento:** `4001` credencial inválida · `4002` credencial revogada (o nó para de tentar) · `4003` versão incompatível · `4004` substituída por conexão nova · `4005` Core desligando (reconectar logo).

### Tipos de mensagem

O catálogo de tipos, com os campos principais e as respostas, e a tabela de códigos de erro estão em [mensagens](mensagens.md). Três regras valem para todos:
- **Resultado separado do ack:** um comando que termina depois (ex.: `terminal.open`, `inbox.deliver`, `transcribe.request`) responde com uma mensagem de resultado ligada por `reply_to`; recusa por estado, como terminal ocupado, vem no resultado (`inbox.result` com `busy`), nunca no ack.
- **CLI encaminhada:** `cli.call` e `cli.result` carregam exatamente o corpo do pedido e da resposta do [canal local](../arquitetura/canal-local.md), sem tradução.
- **Regras efetivas:** `agent.rules` leva o documento de [regras efetivas](../classes/permissoes.md#regras-efetivas) do personagem; o nó guarda a última versão e o retrato para o modo degradado.

### Transferência em partes

- **Usos:** git bundles das mesas ([ADR 0013](../decisoes/0013-sincronizacao-de-worktrees-por-git-bundle.md)), anexos, classes e pergaminhos materializados nos nós, áudio para transcrição e binários de atualização ([autoatualização](../plataforma/autoatualizacao.md)).
- **Passo a passo:**
  1. O remetente envia `xfer.begin`, com:
     - `xfer_id`;
     - `kind` (`git-bundle`, `file` ou `release`);
     - `size` e `sha256` do total;
     - `chunk_size` (padrão 256 KiB);
     - `meta`: para bundle, mesa, branch, commits pré-requisito e ponta; para arquivo, o caminho de destino.
  2. O destinatário responde com `next_index`: 0, ou o ponto de retomada se já tiver partes num arquivo temporário.
  3. As partes vão em frames binários `0x02`, com até 16 partes em trânsito. O destinatário envia `xfer.progress` com o próximo índice esperado, e isso libera a janela.
  4. O remetente envia `xfer.end`. O destinatário confere o SHA-256 total. Se for bundle, roda `git bundle verify` no repositório de destino.
  5. O destinatário responde `xfer.done` com `ok` ou o erro.
- **Bundles:**
  - São incrementais. O pré-requisito é o último commit que os dois lados têm daquela mesa.
  - Se faltar o pré-requisito, o erro `missing_prerequisite` faz o remetente mandar um bundle da branch inteira.
  - Como o bundle é aplicado (avanço, conflito) está em [mesas](../orquestracao/mesas.md).
- **Reconexão:** depois de reconectar, o remetente repete `xfer.begin` com o mesmo `xfer_id`. Arquivos temporários sem uso por 24 horas são apagados.

### Conexão do Guardião

- **Conexão própria:** o Guardião do nó abre a própria conexão (`/ws/guardian`), com credencial própria. Ela não depende do motor do nó.
- **Mensagens:** mesmo envelope e mesmas regras de ack. A versão é **própria** (começa em 1 e é negociada no `guardian.hello` do mesmo jeito), porque o Guardião tem binário e versão independentes; o manifesto de cada release traz as duas faixas ([autoatualização](../plataforma/autoatualizacao.md)). Tipos: [mensagens do Guardião](mensagens.md#guardião-do-nó--core). A `guardian.replica` é a única cópia de usuários que serve login (página reserva); o `cache.users` do motor do nó é só cache de operação.
- **Nada derruba a conexão:** nenhuma mensagem desta conexão pede ao Guardião que a feche, se desligue ou se atualize. A atualização do Guardião é feita pelo motor saudável da própria máquina ([autoatualização](../plataforma/autoatualizacao.md)).

### Canal do navegador

- **Conexão:** `/ws/ui`, autenticado pelo cookie de sessão (`HttpOnly`, `Secure`, `SameSite=Strict`), com o `Origin` conferido. O envelope é o mesmo, sem `node_id`.
- **Assinaturas:** o navegador assina tópicos com `ui.subscribe` (`workspace:<id>`, `table:<id>`, `terminal:<id>`). Recebe um retrato inicial e depois os eventos (`ui.event`).
- **Terminais:**
  - A saída chega em frames `0x01`, repassados pelo Core sem guardar buffer. Ao abrir um terminal, o Core pede ao nó o `terminal.replay` do buffer e repassa ao navegador; o tamanho do terminal é fixo e o navegador nunca o muda ([terminal](../arquitetura/terminal.md)).
  - Para cada navegador, a fila tem limite. Cliente lento perde o fluxo e recebe de novo o replay do buffer.
  - `terminal.input` pelo navegador exige o nível em [usuários e níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).

Pareamento e descoberta usam este protocolo e vêm depois dele no mesmo marco ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)).

## Regras e erros

- Um comando com ack, ao ser reenviado depois de uma queda, nunca executa duas vezes. Isso vale principalmente para `terminal.input` e `inbox.deliver`.
- Credencial revogada (`4002`): o nó para e mostra o estado no checklist. Mensagem maior que 1 MiB: `error` `too_large`.

## Critérios de aceite

- **CA-PROTO-01** · *rápida* — **Dado** um `terminal.input` com ack já executado, **quando** o mesmo `id` chega depois de reconectar, **então** nada é digitado de novo e a resposta é `duplicate`.
- **CA-PROTO-02** · *rápida* — **Dado** um nó com versões 3–4 e um Core com 4–5, **quando** conectam, **então** usam a 4. **Dado** um nó só com a 2, **então** o Core fecha com `4003`.
- **CA-PROTO-03** · *rápida* — **Dado** um terminal mandando 50 MiB de saída, **quando** sai um heartbeat, **então** ele é enviado antes dos próximos 64 KiB de dados.
- **CA-PROTO-04** · *rápida* — **Dado** uma transferência interrompida na parte 40, **quando** a conexão volta, **então** ela retoma da 40 e o SHA-256 final confere.
- **CA-PROTO-05** · *completa* — **Dado** um bundle cujo pré-requisito o Core não tem, **quando** chega o `xfer.end`, **então** a resposta é `missing_prerequisite` e o nó envia a branch inteira.
- **CA-PROTO-06** · *completa* — **Dado** Core e nó reais na mesma máquina, **quando** a conexão cai e volta durante uma etapa, **então** a saída do terminal continua sem buraco e nenhum comando repete.
- **CA-PROTO-07** · *rápida* — **Dado** o motor do nó parado, **quando** o Core manda `guardian.action` de status, **então** o Guardião responde pela própria conexão.

- **CA-PROTO-08** · *rápida* — **Dado** um `inbox.deliver` para um terminal que ficou ocupado, **quando** o nó responde, **então** o ack é `ok`, o resultado é `inbox.result` com `busy` e o item volta para a fila do Core.
- **CA-PROTO-09** · *rápida* — **Dado** um navegador que abre um terminal cujo nó já mandou 300 KiB de saída, **quando** o Core atende, **então** pede `terminal.replay` ao nó a partir do deslocamento 0 e o navegador recebe os 300 KiB.

## Depende de

- [Terminal](../arquitetura/terminal.md) · [Canal local](../arquitetura/canal-local.md)
- [Dublês](../qualidade/dubles.md) (rede falsa)
- [Segurança](../arquitetura/seguranca.md) · [Persistência](../arquitetura/persistencia.md) · [Componentes](../arquitetura/componentes.md)
