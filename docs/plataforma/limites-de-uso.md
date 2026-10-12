# Limites de uso e custo

> **Assunto:** registro de uso por provedor, limites por missão, mesa, workspace e dia, avisos em 80% e pausa em 100%, e o estado "aguardando cota" · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Como o Mestre cria agentes sozinho, proteger contra gasto descontrolado: registrar o uso de cada personagem, avisar antes de passar dos limites, pausar ao atingi-los e lidar com o limite de assinatura de cada provedor.

## Comportamento

### Registro de uso

- Tabela `usage_records` no Core ([persistência](../arquitetura/persistencia.md)). Cada registro tem: personagem, etapa, missão, mesa, workspace, perfil de conta, provedor, modelo, horário, origem do dado e o que o provedor informar:
  - tokens (entrada, saída, cache lido e escrito, raciocínio);
  - custo estimado em USD;
  - sempre, mesmo sem nada do provedor: **tempo ativo** (soma dos intervalos em `busy`, pelos eventos de início e fim de turno do [adaptador](../provedores/adaptador.md)) e **número de turnos**.
- O adaptador de cada provedor entrega um registro normalizado (`ReadUsage`) no fim de cada turno e periodicamente. Os registros são somados por personagem, missão, mesa, workspace, conta e dia.

### De onde vem o uso, por provedor

| Provedor | Fonte principal | O que dá | Detalhe |
|---|---|---|---|
| Claude Code | JSON do statusLine | custo estimado (preço de lista), tokens, janelas de limite de 5 h e 7 dias (planos Pro/Max) | [Claude Code](../provedores/claude-code.md) |
| Codex | eventos `token_count` no JSONL da sessão | tokens e janelas de limite; **sem custo** | [Codex](../provedores/codex.md) |
| Antigravity | `agy -p "/usage" --output-format json`, por perfil de conta | fração restante das janelas semanal e de 5 h; não gasta cota | [Antigravity](../provedores/antigravity.md) |

- No Antigravity, o `/usage` roda a cada 10 min (padrão) para cada perfil de conta com personagem ativo. Tokens por turno no modo interativo não são lidos; valem tempo ativo e turnos ([Antigravity](../provedores/antigravity.md)).
- Claude Code sem statusLine (pasta sem confiança): vale o monitoramento reduzido do [adaptador](../provedores/adaptador.md#monitoramento-reduzido).

### Limites

- Configuráveis por **missão**, por **mesa**, por **workspace** e **por dia** (por workspace e no total da instalação).
- Cada limite tem uma **unidade**: custo estimado (USD), tokens ou horas ativas. Uso sem dado naquela unidade (ex.: Codex num limite em USD) não conta para ele, e o painel mostra essa lacuna ao lado do limite.
- O dia vira à meia-noite no fuso configurado do Core (padrão: o fuso do sistema).
- Quem configura: Administrador ou acima nos workspaces dele; o limite diário total, só o Dono. Nenhum limite pode passar o **teto de custo do workspace** ([teto](../classes/teto-do-workspace.md)).
- Valores padrão: sem limites configurados (a instalação sugere configurar um limite diário por workspace no primeiro acesso).

### 80% e 100%

| Situação | O que o motor faz |
|---|---|
| Chegou a **80%** de um limite | alerta operacional aos usuários do workspace (Telegram e painel, [bot](../telegram/bot.md)) e evento na caixa de entrada do Grão-Mestre. Um alerta por limite por período |
| Chegou a **100%** de uma missão | pausa a missão (nenhuma etapa nova; quem está no turno termina) e cria um pedido de aprovação `custo_extra` ([fluxo](../aprovacoes/fluxo.md)) |
| Chegou a **100%** de uma mesa | marca a mesa como pausada ([mesas](../orquestracao/mesas.md#pausa)) e cria o pedido `custo_extra` |
| Chegou a **100%** do limite do workspace ou diário | pausa o workspace ([workspaces](../orquestracao/workspaces.md)) e cria o pedido `custo_extra` |

- O pedido traz o uso até agora, o limite e o valor extra sugerido (padrão: mais 50% do limite).
- O Grão-Mestre pode aprovar conforme a [política](../aprovacoes/politica.md). Se o extra fizer passar o teto de custo do workspace, só um Administrador ou acima aprova ([teto](../classes/teto-do-workspace.md)).
- Aprovado: o limite daquele período sobe pelo valor aprovado e a pausa é retirada. Negado ou expirado: continua pausado, e os usuários e o Grão-Mestre são avisados.
- A pausa nunca interrompe um turno: o motor confere o limite a cada registro de uso e a pausa vale a partir do fim do turno atual.

### Aguardando cota

- **Detecção** (sinais detalhados em cada documento de provedor): Claude Code pelo hook `StopFailure` com `rate_limit` ou pela tela de limite; Codex por `rate_limit_reached_type` preenchido; Antigravity pelo erro de cota no fim do turno ou por uma janela com fração restante zero.
- O personagem vai para `blocked` com o motivo "aguardando cota" e o horário em que a cota volta, quando conhecido ([personagens](../orquestracao/personagens.md#estados)).
- O Mestre da mesa recebe um evento na caixa de entrada com: personagem, provedor, conta, quando a cota volta e as **fichas da mesma classe que usam outro provedor ou outra conta**, se existirem. Ele decide se troca a ficha (`recruit --replace`, [coordenação](../cli/coordenacao.md)) ou espera.
- Se quem ficou sem cota é o próprio Mestre da mesa, o evento vai ao Grão-Mestre; se é o Grão-Mestre, o motor avisa os usuários.
- A cota voltou (horário passou, ou o provedor voltou a responder, ou o `/usage` mostra fração maior que zero): o motor tira o `blocked` e entrega o que estava na fila.

### O que o painel mostra

Consumo por personagem, missão, mesa, workspace, conta e dia; limites e quanto falta; e, por perfil de conta, as janelas de limite do provedor em percentual com o horário de renovação ([administração](../painel/administracao.md)).

## Regras e erros

- Fonte de uso indisponível (statusLine não roda, JSONL não encontrado) → registra só tempo ativo e turnos e abre um alerta do [checklist](checklist.md).
- Formato de uso mudou numa versão nova do provedor → o [teste de conformidade](../provedores/conformidade.md) acusa; até a correção, valem tempo ativo e turnos.
- Um limite configurado acima do teto é recusado na hora de salvar.

## Critérios de aceite

- **CA-USO-01** · *rápida* — **Dado** um registro de statusLine gravado, **quando** o motor soma, **então** a mesa e o workspace mostram o custo e os tokens daquele turno.
- **CA-USO-02** · *rápida* — **Dado** um limite de mesa em 10 USD e uso em 8 USD, **quando** chega novo registro, **então** sai um único alerta de 80%; um segundo registro abaixo de 100% não gera outro.
- **CA-USO-03** · *rápida* — **Dado** uma mesa a 100% com um personagem ocupado, **quando** o limite é atingido, **então** o turno dele termina normalmente, a mesa fica pausada e nasce um pedido `custo_extra`.
- **CA-USO-04** · *rápida* — **Dado** um personagem Codex com `rate_limit_reached_type` preenchido, **quando** o uso é lido, **então** ele fica `blocked` "aguardando cota" e o Mestre da mesa recebe as fichas alternativas da mesma classe.
- **CA-USO-05** · *rápida* — **Dado** um personagem aguardando cota até 14:00 (relógio falso), **quando** passa das 14:00, **então** ele sai de `blocked` e recebe a fila.
- **CA-USO-06** · *rápida* — **Dado** um provedor sem dado de tokens, **quando** o turno termina, **então** o registro tem tempo ativo e um turno.
- **CA-USO-07** · *rápida* — **Dado** um pedido `custo_extra` que passaria o teto, **quando** o Grão-Mestre tenta aprovar, **então** é recusado e o pedido vai aos usuários Administrador ou acima.
- **CA-USO-08** · *pesada* — **Dado** uma mesa trabalhando, **quando** abro o painel, **então** vejo o consumo da mesa e de cada personagem (MVP-11).

## Depende de

- [Adaptador de provedores](../provedores/adaptador.md)
- [Persistência](../arquitetura/persistencia.md)
- [Fluxo de aprovação](../aprovacoes/fluxo.md)
- [Teto do workspace](../classes/teto-do-workspace.md)
- [Mesas](../orquestracao/mesas.md)
- [Personagens](../orquestracao/personagens.md)
- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)

## Fontes

- Claude Code, dados do statusLine e custos: https://code.claude.com/docs/en/statusline#available-data , https://code.claude.com/docs/en/monitoring-usage
- Claude Code, limites de uso: https://code.claude.com/docs/en/errors#usage-limits
- Codex, configuração: https://learn.chatgpt.com/docs/config-file/config-reference
- Antigravity, `/usage`: https://antigravity.google/docs/cli/commands/usage
