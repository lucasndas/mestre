# Protocolo entre as máquinas da Guilda e o navegador

> **Assunto:** conexão WSS iniciada pela Torre até a Cidadela atual: endpoints, envelope com época, versão, ack e idempotência, multiplexação, tipos de mensagem, transferência em partes, Guardião e navegador · **Componente:** Todos · **Atualizado:** 2026-10-13

## Objetivo

Definir como a Cidadela, os motores das Torres, os Guardiões e o painel conversam. O canal é uma conexão WebSocket segura (WSS), sempre aberta pelo lado de fora (Torre ou navegador) até a **Cidadela atual**, em que nenhuma reconexão executa nada duas vezes e nenhuma mensagem de uma Cidadela antiga é aceita. O porquê está no [ADR 0002](../decisoes/0002-wss-iniciado-pela-torre.md) e no [ADR 0022](../decisoes/0022-guilda-cidadela-e-torres.md).

## Comportamento

### Endpoints e portas

- Portas, rotas e a autenticação de cada uma estão na tabela única de [portas](../arquitetura/componentes.md#portas): `wss /ws/tower` (motor de cada Torre, mTLS de máquina), `wss /ws/guardian` (Guardião de cada Torre, mTLS de Guardião) e `wss /ws/ui` (navegador, cookie de sessão) na 7443 da Cidadela; `/join` para a [entrada na Guilda](../guilda/guilda.md#entrada-na-guilda); `wss /ws/guardian-citadel` na 7444 do Guardião da Cidadela; e, na 7446 do Guardião de toda máquina, a sondagem `GET /guardian/alive` e a ordem `wss /guardian/promote`.
- Tudo é sempre TLS, com a CA da Guilda, inclusive na rede local. Numa Torre, a 7443 só redireciona ([painel redirecionado](../guilda/cidadela-e-torres.md#painel-redirecionado)).
- **A Torre sempre conecta na Cidadela atual:** a da certidão de época mais alta que conhece; achar o endereço é da [descoberta](descoberta.md).

### Envelope

Toda mensagem de controle é um frame de texto com um objeto JSON:

| Campo | Obrigatório | Conteúdo |
|---|---|---|
| `id` | sim | ULID único por remetente. É também a chave de idempotência |
| `type` | sim | `<domínio>.<ação>`, ex.: `terminal.open` |
| `machine_id` | sim (entre máquinas) | ID da máquina remetente. Nas mensagens do navegador, é omitido |
| `epoch` | sim (entre máquinas) | a época da Guilda que o remetente segue ([época e fencing](../guilda/cidadela-e-torres.md#época-e-fencing)). Nas mensagens do navegador, é omitido |
| `ts` | sim | RFC 3339 em UTC com milissegundos, no relógio do remetente (só informativo) |
| `payload` | sim | objeto, que depende do `type` |
| `reply_to` | não | `id` da mensagem que esta responde (resultado, ack, erro) |
| `ack` | não | `true` quando o remetente exige confirmação |

- **Erro:** `type: "error"`, com `payload` `{code, message, retryable}` e `reply_to`.
- **Tamanho:** o máximo é 1 MiB por mensagem, JSON ou frame binário (cabeçalho incluído). Conteúdo maior usa a [transferência em partes](#transferência-em-partes). Quem recebe uma maior a descarta sem derrubar a conexão e responde `too_large`; acima de 16 MiB, fecha com `1009`.
- **Campos e tipos desconhecidos:** campos são ignorados. Um `type` desconhecido recebe `error` com `code: "unknown_type"`, sem derrubar a conexão.

### Época

- Quem recebe uma mensagem com `epoch` **menor** que a sua responde `error` `stale_epoch` sem executar nada e fecha a conexão com `4006`; o remetente atualiza a época (pede a certidão) e se reconecta na Cidadela certa. O `hello` é a exceção: de época menor, ele não é recusado, e o `welcome` leva a certidão atual, que a Torre segue.
- Quem recebe `epoch` **maior** pede a certidão (`guild.epoch`), confere a assinatura e passa a seguir a nova época **antes** de tratar a mensagem; no `hello` a certidão já vem junto. Uma Cidadela que vê época maior se rebaixa a Torre antes de qualquer ação externa e fecha com `4007` ([volta da antiga Cidadela](../guilda/cidadela-e-torres.md#volta-da-antiga-cidadela)). Uma Torre que recebe um `welcome` de época menor que a sua não fala com aquela Cidadela, nem com uma máquina que o TLS identificou como outra que não a nomeada na certidão do `welcome`.

### Abertura, versão e capacidades

1. A Torre conecta e envia `hello` com: faixa de versões do protocolo que entende (`{min, max}`, inteiros; a primeira versão é **1**), versão do binário, a [ficha da máquina](../guilda/ficha-da-maquina.md), a certidão de época que segue, o último `lsn` da réplica ([replicação](../guilda/replicacao.md)) e os terminais que já estão rodando. Os dois lados podem anunciar `capabilities` (lista opcional de nomes); vale o que ambos anunciam, e um nome desconhecido é ignorado.
2. A Cidadela responde `welcome` com a versão escolhida (a maior em comum), a versão dela, a hora dela e a certidão de época atual.
   - Sem versão em comum, a Cidadela fecha com o código `4003`, informando a faixa que aceita.
3. **Mudanças compatíveis:**
   - Campo opcional novo e tipo novo **não** mudam a versão.
   - Remover um campo ou mudar o significado de algo exige versão nova.
   - Toda máquina aceita pelo menos a versão atual e a anterior: a [autoatualização](../plataforma/autoatualizacao.md) troca as Torres primeiro e a Cidadela por último, então durante ela Torres novas convivem com a Cidadela antiga, e qualquer uma pode ser promovida no meio.
4. **Relógio:** uma diferença de mais de 5 s entre o relógio da Torre e o da Cidadela vira aviso no [checklist](../plataforma/checklist.md).
5. **Ficha depois do `hello`:** mudanças vão em `machine.sheet`; a Cidadela escolhe a máquina de cada personagem pela ficha ([ficha da máquina](../guilda/ficha-da-maquina.md)).

### Ack e idempotência

- **Remetente:** toda mensagem com `ack: true` é gravada, de forma durável, na tabela `outbox` do banco do remetente (Cidadela ou Torre, [persistência](../arquitetura/persistencia.md)) até chegar o `ack`; a chave é a máquina, e na Torre é sempre "a Cidadela", para a mensagem seguir uma troca de Cidadela. Depois de reconectar, ela é reenviada **com o mesmo `id`**, na ordem original e com a época de agora. Os tipos com segredo (`vault.credential`, `agent.site_login`, `agent.site_login_input`) esperam o ack só em memória, nunca no disco. Um `error` não retentável sobre a mensagem a tira do `outbox`; `stale_epoch` não.
- **Destinatário:** reserva no banco, antes de executar, o `id` das mensagens com ack, por remetente, e guarda a resposta dada por 24 horas (padrão). Um `id` repetido devolve a mesma resposta, com `status: "duplicate"`, **sem executar de novo**; uma execução que caiu no meio também conta como feita, porque executar duas vezes é pior. Um erro `retryable` do tratamento esquece o `id`, e reenviar a mesma mensagem pode dar certo.
- **O que o ack significa:** `ack` (`payload` `{status: ok|duplicate|error}`) quer dizer "recebido e aceito", não "concluído". A conclusão vem numa mensagem de resultado própria, ligada por `reply_to`.
- A saída de terminal não tem ack. Ela se recupera por replay (abaixo).

### Multiplexação e prioridade

- **Duas classes de frame:** controle (JSON em frame de texto) e dados (frame binário, com saída de terminal, partes de transferência e replicação).
- **Prioridade:** o escritor de cada conexão esvazia primeiro a fila de controle e manda no máximo 64 KiB de dados entre duas checagens dessa fila. Assim um terminal barulhento não atrasa heartbeat nem comando. A checagem é entre frames: um frame maior que 64 KiB (uma parte de transferência de 256 KiB) vai inteiro.
- **Frame binário:** `[1 byte tipo][16 bytes ID (ULID)][8 bytes posição, big-endian][dados]`.
  - Tipo `0x01`: saída de terminal. O ID é o do terminal e a posição é o deslocamento em bytes.
  - Tipo `0x02`: parte de transferência. O ID é o da transferência e a posição é o índice da parte.
  - Tipo `0x03`: registros de mudança do banco ([replicação](../guilda/replicacao.md)). O ID é o `guild_id` e a posição é o `lsn` do primeiro registro.
- **Replay de terminal:** o buffer de cada terminal fica só na máquina dele (1 MiB, padrão; [terminal](../arquitetura/terminal.md)). Depois de reconectar, ou quando um navegador abre um terminal, a Cidadela pede `terminal.replay` a partir do deslocamento que precisa. Se o trecho já saiu do buffer, a resposta marca "trecho perdido"; o log completo continua no disco da máquina.

### Heartbeat e reconexão

- **Ping:** ping do WebSocket a cada 15 s, pelos dois lados. Sem pong em 45 s (contados do último pong, mesmo que o ping não tenha saído por uma escrita longa), a conexão é dada como morta e fechada sem o aperto de fechamento.
- **Heartbeat:** `machine.heartbeat` logo depois do `welcome` e a cada 30 s, com CPU, memória, disco livre, número de terminais e atraso da réplica. Sem heartbeat por 90 s, a Cidadela considera a Torre offline ([camadas](../autocura/camadas.md)); sem resposta da Cidadela, as Torres começam a vigilância ([Cidadela e Torres](../guilda/cidadela-e-torres.md#recuperação-e-aviso-ao-dono)).
- **Reconexão:** backoff de 1 s, dobrando até 60 s, com variação de ±20%. Depois de 3 falhas seguidas, a Torre refaz a [descoberta](descoberta.md).
- **Uma conexão por `machine_id`:** uma conexão nova substitui a antiga. "Nova" é a aceita por último (a ordem é fixada ao aceitar a conexão, antes do `welcome`), não a que se liga primeiro ao vínculo: uma conexão mais velha que chega depois da mais nova é ela quem se fecha com `4004`, e o que espera ack é reenviado na que ficou.
- **Códigos de fechamento:** `4001` credencial inválida · `4002` credencial revogada (a máquina para de tentar) · `4003` versão incompatível · `4004` substituída por conexão nova · `4005` Cidadela desligando (reconectar logo) · `4006` época vencida · `4007` não sou a Cidadela (a resposta traz a certidão atual, assinada pela CA, que a Torre segue se for mais nova antes de refazer a [descoberta](descoberta.md) na hora; vale também para o Guardião de uma Cidadela antiga, que se retira na hora ao receber certidão mais nova, [concessão do motor](../autocura/guardiao.md#concessão-do-motor)).

### Tipos de mensagem

O catálogo de tipos, com os campos principais e as respostas, e a tabela de códigos de erro estão em [mensagens](mensagens.md). Três regras valem para todos:
- **Resultado separado do ack:** um comando que termina depois (ex.: `terminal.open`, `inbox.deliver`, `transcribe.request`) responde com uma mensagem de resultado ligada por `reply_to`; recusa por estado, como terminal ocupado, vem no resultado (`inbox.result` com `busy`), nunca no ack.
- **CLI encaminhada:** `cli.call` e `cli.result` carregam exatamente o corpo do pedido e da resposta do [canal local](../arquitetura/canal-local.md), sem tradução.
- **Regras efetivas:** `agent.rules` leva o documento de [regras efetivas](../classes/permissoes.md#regras-efetivas) do personagem; a máquina guarda a última versão e o retrato para o modo degradado.

### Transferência em partes

- **Usos:** git bundles das mesas e dos espelhos ([ADR 0013](../decisoes/0013-sincronizacao-de-worktrees-por-git-bundle.md)), anexos, classes e pergaminhos materializados nas máquinas, cópia completa do banco e arquivos da Cidadela ([replicação](../guilda/replicacao.md)), áudio para transcrição e binários de atualização ([autoatualização](../plataforma/autoatualizacao.md)).
- **Passo a passo:**
  1. O remetente envia `xfer.begin`, com `xfer_id`; `kind` (`git-bundle`, `file`, `db-snapshot` ou `release`); `size` e `sha256` do total; `chunk_size` (padrão 256 KiB); e `meta` (para bundle, mesa, branch, commits pré-requisito e ponta; para arquivo, o caminho de destino).
  2. O destinatário responde `xfer.ready` com `next_index`: 0, ou o ponto de retomada se já tiver partes num arquivo temporário. Uma recusa (tipo sem receptor, disco) vem no ack.
  3. As partes vão em frames binários `0x02`, com até 16 partes em trânsito. O destinatário envia `xfer.progress` com o próximo índice esperado a cada 8 partes e ao fim, e isso libera a janela.
  4. O remetente só envia `xfer.end` depois de o destinatário confirmar todas as partes (a fila de controle passaria na frente das partes). O destinatário confere o SHA-256 total e roda o fim do `kind`; para bundle, `git bundle verify` no repositório de destino.
  5. O destinatário responde `xfer.done` com `ok` ou o erro. Um `kind` pode responder já no `xfer.begin` que tem o conteúdo (mesmo SHA-256): o `xfer.ready` dá todas as partes como recebidas e nenhuma é enviada (arquivo que não mudou, espelho em dia).
- **Bundles:**
  - São incrementais. O pré-requisito é o último commit que os dois lados têm daquela mesa.
  - Se faltar o pré-requisito, o erro `missing_prerequisite` faz o remetente mandar um bundle da branch inteira.
  - Como o bundle é aplicado (avanço, conflito) está em [mesas](../orquestracao/mesas.md).
- **Reconexão:** depois de reconectar, o remetente repete `xfer.begin` com o mesmo `xfer_id` (e uma mensagem nova, sem passar pelo `outbox`); quem já terminou responde com o resultado guardado. Uma nova tentativa depois de erro usa outro `xfer_id`. Arquivos temporários sem uso por 24 horas são apagados.

### Conexão do Guardião

- **Conexão própria:** o Guardião de cada Torre abre a própria conexão até a Cidadela atual (`/ws/guardian`), com credencial própria. Ela não depende do motor da Torre.
- **Mensagens:** mesmo envelope e mesmas regras de ack. A versão é **própria** (começa em 1 e é negociada no `guardian.hello` do mesmo jeito), porque o Guardião tem binário e versão independentes; o manifesto de cada release traz as duas faixas ([autoatualização](../plataforma/autoatualizacao.md)). Tipos: [mensagens do Guardião](mensagens.md#guardião-da-torre--cidadela). A `guardian.replica` é a única cópia de usuários que serve login (página reserva); a réplica do banco no motor da Torre não serve login.
- **Concessão:** pelo canal local, o motor de cada máquina manda ao próprio Guardião a mensagem `lease` a cada 30 s, com a época, enquanto não está suspenso (não depende do GitHub); é ela que deixa o Guardião da Cidadela publicar `mestre.local` e arbitrar ([mensagens](mensagens.md#guardião-da-torre--cidadela), [concessão do motor](../autocura/guardiao.md#concessão-do-motor)).
- **Nada derruba a conexão:** nenhuma mensagem desta conexão pede ao Guardião que a feche, se desligue ou se atualize. A atualização do Guardião é feita pelo motor saudável da própria máquina ([autoatualização](../plataforma/autoatualizacao.md)).

### Canal do navegador

- **Conexão:** `/ws/ui`, autenticado pelo cookie de sessão (`HttpOnly`, `Secure`, `SameSite=Strict`), com o `Origin` conferido. O envelope é o mesmo, sem `machine_id` nem `epoch`.
- **Assinaturas:** o navegador assina tópicos com `ui.subscribe` (`workspace:<id>`, `table:<id>`, `terminal:<id>`). Recebe um retrato inicial e depois os eventos (`ui.event`).
- **Terminais:**
  - A saída chega em frames `0x01`, repassados pela Cidadela sem guardar buffer. Ao abrir um terminal, a Cidadela pede à máquina dele o `terminal.replay` do buffer e repassa ao navegador; o tamanho do terminal é fixo e o navegador nunca o muda ([terminal](../arquitetura/terminal.md)).
  - Para cada navegador, a fila tem limite. Cliente lento perde o fluxo e recebe de novo o replay do buffer.
  - `terminal.input` pelo navegador exige o nível em [usuários e níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).

Descoberta, entrada na Guilda, ficha da máquina e replicação usam este protocolo e vêm depois dele no mesmo marco ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)).

## Regras e erros

- Um comando com ack, ao ser reenviado depois de uma queda, nunca executa duas vezes. Isso vale principalmente para `terminal.input` e `inbox.deliver`.
- Credencial revogada (`4002`): a máquina para e mostra o estado no checklist. Época vencida (`4006`): a máquina nunca reenvia a mensagem recusada para a Cidadela antiga; segue a certidão nova. Mensagem maior que 1 MiB: `error` `too_large`.

## Critérios de aceite

- **CA-PROTO-01** · *rápida* — **Dado** um `terminal.input` com ack já executado, **quando** o mesmo `id` chega depois de reconectar, **então** nada é digitado de novo e a resposta é `duplicate`.
- **CA-PROTO-02** · *rápida* — **Dado** uma Torre com versões 3–4 e uma Cidadela com 4–5, **quando** conectam, **então** usam a 4. **Dado** uma Torre só com a 2, **então** a Cidadela fecha com `4003`.
- **CA-PROTO-03** · *rápida* — **Dado** um terminal mandando 50 MiB de saída, **quando** sai um heartbeat, **então** ele é enviado antes dos próximos 64 KiB de dados.
- **CA-PROTO-04** · *rápida* — **Dado** uma transferência interrompida na parte 40, **quando** a conexão volta, **então** ela retoma da 40 e o SHA-256 final confere.
- **CA-PROTO-05** · *completa* — **Dado** um bundle cujo pré-requisito a Cidadela não tem, **quando** chega o `xfer.end`, **então** a resposta é `missing_prerequisite` e a Torre envia a branch inteira.
- **CA-PROTO-06** · *completa* — **Dado** Cidadela e Torre reais na mesma máquina, **quando** a conexão cai e volta durante uma etapa, **então** a saída do terminal continua sem buraco e nenhum comando repete.
- **CA-PROTO-07** · *rápida* — **Dado** o motor da Torre parado, **quando** a Cidadela manda `guardian.action` de status, **então** o Guardião responde pela própria conexão.
- **CA-PROTO-08** · *rápida* — **Dado** um `inbox.deliver` para um terminal que ficou ocupado, **quando** a máquina responde, **então** o ack é `ok`, o resultado é `inbox.result` com `busy` e o item volta para a fila da Cidadela.
- **CA-PROTO-09** · *rápida* — **Dado** um navegador que abre um terminal cuja máquina já mandou 300 KiB de saída, **quando** a Cidadela atende, **então** pede `terminal.replay` à máquina a partir do deslocamento 0 e o navegador recebe os 300 KiB.
- **CA-PROTO-10** · *rápida* — **Dado** uma máquina (Torre ou Cidadela) na época 2, **quando** chega uma mensagem da época 1, **então** ela responde `stale_epoch`, fecha com `4006` e nada é executado ([época e fencing](../guilda/cidadela-e-torres.md#época-e-fencing)); **dado** um `welcome` com certidão da época 3 válida, **então** a Torre passa a seguir a época 3.
- **CA-PROTO-11** · *rápida* — **Dado** a Cidadela na rede falsa, **quando** chegam a `/ws/tower` um motor com certificado de outra CA, um vencido (relógio falso), um Guardião, um cliente só com TLS 1.2, e a `/ws/guardian` um motor, **então** nenhum chega à rota; **dado** o motor válido, **então** a Cidadela vê o `machine_id` do SAN, e a Torre recusa um servidor de outra CA.
- **CA-PROTO-12** · *rápida* — **Dado** o motor e o Guardião de uma Torre conectados, **quando** a credencial da máquina é revogada, **então** as duas conexões caem com `4002`, as de outras máquinas continuam e uma nova conexão dela é recusada no handshake.
- **CA-PROTO-13** · *rápida* — **Dado** um cliente sem certificado, **quando** pede `/ca.crt`, **então** recebe a CA em DER, com o SHA-256 igual à impressão digital dela; `/healthz` responde `200` e `/ws/tower` responde `401`.
- **CA-PROTO-14** · *rápida* — **Dado** uma conexão parada por 10 min com os pongs chegando, **então** ela continua; **dado** a rede falsa meio aberta, **quando** passam de 45 s a 60 s sem pong (tempo falso), **então** os dois lados dão a conexão como morta.
- **CA-PROTO-15** · *rápida* — **Dado** a CA criada (relógio falso), **quando** ela emite o certificado do motor de uma máquina, **então** a CA é ECDSA P-256 por 10 anos, o certificado tem os SANs da [tabela](../arquitetura/seguranca.md#tls-e-ca-interna), `mestre.local` só fora da máquina excluída, 1 ano de validade, e pede renovação a 30 dias do fim; a renovação mantém a identidade, troca a chave e segue os IPs novos.
- **CA-PROTO-16** · *rápida* — **Dado** a CA guardada no cofre, **quando** a Cidadela a carrega, **então** a chave é o segredo `guild_ca_key` (tipo `ca_key`), a leitura vai à auditoria com a finalidade, e os certificados emitidos são da mesma CA; a chave não abre a CA de outra Guilda.
- **CA-PROTO-17** · *rápida* — **Dado** uma mensagem de 1 MiB + 1 byte, **quando** chega, **então** é descartada com `too_large` e a conexão segue com a mensagem seguinte; uma de 1 MiB passa.
- **CA-PROTO-18** · *completa* — **Dado** Cidadela e Torre com sockets reais em `127.0.0.1`, no Linux e no Windows, **quando** o motor e o Guardião conectam por mTLS, **então** trocam frames de texto e binários, a revogação os derruba com `4002` e um cliente TLS comum que confia na CA baixa `/ca.crt`.

## Depende de

- [Terminal](../arquitetura/terminal.md) · [Canal local](../arquitetura/canal-local.md)
- [Dublês](../qualidade/dubles.md) (rede falsa)
- [Segurança](../arquitetura/seguranca.md) · [Persistência](../arquitetura/persistencia.md) · [Componentes](../arquitetura/componentes.md)
