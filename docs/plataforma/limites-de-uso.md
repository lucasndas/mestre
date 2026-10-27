# Limites de uso e custo

> **Assunto:** registro de uso por provedor, limites por missão, mesa, campanha e dia, avisos em 80% e pausa em 100%, o limiar de início, a leitura antiga e o estado "aguardando cota" · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Como o Mestre cria agentes sozinho, proteger contra gasto descontrolado: registrar o uso de cada personagem, avisar antes de passar dos limites, pausar ao atingi-los e lidar com o limite de assinatura de cada provedor.

## Comportamento

### Registro de uso

- Tabela `usage_records` na Cidadela ([persistência](../arquitetura/persistencia.md)). Cada registro tem: personagem, etapa, missão, mesa, campanha, perfil de conta, provedor, modelo, horário, origem do dado, **origem do trabalho** (`etapa`, `conformidade` ou `teste_de_classe`) e o que o provedor informar:
  - tokens (entrada, saída, cache lido e escrito, raciocínio);
  - custo estimado em USD;
  - sempre, mesmo sem nada do provedor: **tempo ativo** (soma dos intervalos em `busy`, pelos eventos de início e fim de turno do [adaptador](../provedores/adaptador.md)) e **número de turnos**.
- Conformidade e teste de classe gravam sem missão nem mesa (origem `conformidade` ou `teste_de_classe`): contam só no limite diário total e no consumo por conta, nunca nos limites de missão, mesa ou campanha.
- O adaptador de cada provedor entrega um registro normalizado (`ReadUsage`) no fim de cada turno e periodicamente. Os registros são somados por personagem, missão, mesa, campanha, conta e dia.
- **Subagentes nativos** usados por um personagem são trabalho dele: o uso entra no registro do personagem e conta para os limites da missão, da mesa, da campanha e do dia como qualquer outro ([subagentes nativos](../classes/permissoes.md#subagentes-nativos)). Enquanto um subagente roda, o personagem segue `busy`, então o tempo ativo já o inclui.

### De onde vem o uso, por provedor

| Provedor | Fonte principal | O que dá | Detalhe |
|---|---|---|---|
| Claude Code | JSON do statusLine | custo estimado (preço de lista), tokens, janelas de limite de 5 h e 7 dias (planos Pro/Max) | [Claude Code](../provedores/claude-code.md) |
| Codex | eventos `token_count` no JSONL da sessão | tokens e janelas de limite; **sem custo** | [Codex](../provedores/codex.md) |
| Antigravity | `agy -p "/usage" --output-format json`, por perfil de conta | fração restante das janelas semanal e de 5 h; não gasta cota | [Antigravity](../provedores/antigravity.md) |

- No Antigravity, o `/usage` roda a cada 10 min (padrão) para cada perfil de conta com personagem ativo. Tokens por turno no modo interativo não são lidos; valem tempo ativo e turnos ([Antigravity](../provedores/antigravity.md)).
- Claude Code sem statusLine (pasta sem confiança): vale o monitoramento reduzido do [adaptador](../provedores/adaptador.md#monitoramento-reduzido).
- **Personagem de uma Torre:** o transcript do Codex e o comando do Antigravity estão na máquina onde ele roda, então é a **Torre** que faz a leitura periódica (a cada minuto, só com a Cidadela conectada, com o mesmo leitor da Cidadela e o cursor guardado na Torre) e a conta à Cidadela em `usage.reading` ([mensagens](../conectividade/mensagens.md)); a Cidadela só a aceita de uma Torre onde o registro põe o personagem e a trata como a leitura do próprio período. Os eventos (`agent.event`) já chegavam de todas as máquinas.

### Limites

- Configuráveis por **missão**, por **mesa**, por **campanha** e **por dia** (por campanha e no total da instalação).
- Cada limite tem uma **unidade**: custo estimado (USD), tokens (entrada, saída, cache lido e escrito e raciocínio, somados) ou horas ativas. Uso sem dado naquela unidade (ex.: Codex num limite em USD) não conta para ele, e o painel mostra essa lacuna ao lado do limite.
- O limite diário recomeça a cada dia; o da missão e o da mesa valem pela vida delas e o da campanha é acumulado (a "rodada" não zera). O que o pedido `custo_extra` aprovado soma vale só para o período em que foi pedido.
- O dia vira à meia-noite no [fuso da Guilda](../painel/administracao.md#fuso-da-guilda).
- Quem configura: Administrador ou acima nas campanhas dele; o limite diário total, só o Senhor da Guilda. Nenhum limite pode passar o **teto de custo da campanha** ([teto](../classes/teto-do-workspace.md)).
- Valores padrão: sem limites configurados (a instalação sugere configurar um limite diário por campanha no primeiro acesso).

### 80% e 100%

| Situação | O que o motor faz |
|---|---|
| Chegou a **80%** de um limite | alerta operacional aos usuários da campanha (Telegram e painel, [bot](../telegram/bot.md)) e evento na caixa de entrada do Grão-Mestre. Um alerta por limite por período |
| Chegou a **100%** de uma missão | pausa a missão (nenhuma etapa nova; quem está no turno termina) e cria um pedido de aprovação `custo_extra` ([fluxo](../aprovacoes/fluxo.md)) |
| Chegou a **100%** de uma mesa | marca a mesa como pausada ([mesas](../orquestracao/mesas.md#pausa)) e cria o pedido `custo_extra` |
| Chegou a **100%** do limite da campanha ou diário | pausa a campanha ([campanhas](../orquestracao/workspaces.md)) e cria o pedido `custo_extra` |

- O pedido traz o uso até agora, o limite e o valor extra sugerido (padrão: mais 50% do limite).
- O Grão-Mestre pode aprovar conforme a [política](../aprovacoes/politica.md). Se o extra fizer passar o teto de custo da campanha, só um Administrador ou acima aprova ([teto](../classes/teto-do-workspace.md)).
- Aprovado: o limite daquele período sobe pelo valor aprovado e a pausa é retirada. Negado ou expirado: continua pausado, e os usuários e o Grão-Mestre são avisados.
- A pausa nunca interrompe um turno: o motor confere o limite a cada registro de uso e a pausa vale a partir do fim do turno atual.
- Só a aprovação do extra retira a pausa que o limite pôs: uma missão ou mesa pausada por outro motivo, ou que alguém retomou, não é mexida. O pedido de um extra que passaria o teto sai de risco alto sem delegação ([política](../aprovacoes/politica.md)); o diário total pausa todas as campanhas ativas e a aprovação retoma os mesmos.

### Limiar de início

Para não perder um turno cortado no meio, o motor não espera o corte: com qualquer janela do plano de um provedor (5 h ou 7 d) **no limiar de início** ou acima (padrão 95%, configurável por campanha por um Administrador), ele não recruta nem entrega etapa nova que use aquele provedor e conta; o Administrador o ajusta na tela de consumo do painel ([administração](../painel/administracao.md)). Quem está no turno termina. A entrega a um personagem daquele provedor e conta fica na fila (qualquer entrega, não só de etapa nova; o personagem em turno não recebe nada de qualquer jeito). A mesa fica aguardando cota até o `resets_at` da janela, e o Grão-Mestre e os usuários recebem `⏸ Mesa Login pausada · 5h 96% · volta às 14:05 · faltam 2 etapas`. Ao passar do `resets_at`, o motor entrega o que esperava (como em [aguardando cota](#aguardando-cota)).

- **Leitura antiga:** o uso só cresce dentro da janela. Uma leitura **acima** do limiar continua valendo até o `resets_at`, por velha que seja; uma **abaixo** não prova nada e, depois de 15 min, vira "desconhecido": a etapa é entregue com um aviso ao Mestre da mesa de que o uso não está confirmado, e a próxima leitura decide. Janela sem `resets_at` conhecido só vale enquanto a leitura é recente (menos de 15 min).
- Os três informam `resets_at` por janela: o Claude Code pelo statusLine (só muda com turno em andamento), o Codex pelo `token_count` e o Antigravity pelo `/usage` (`used_pct = 1 − fração restante`, lido a cada 10 min). Por isso o corte da leitura antiga é de 15 min, acima do intervalo do Antigravity ([fontes](#de-onde-vem-o-uso-por-provedor)); provedor sem janela não tem limiar.

### Aguardando cota

- **Detecção** (sinais detalhados em cada documento de provedor): Claude Code pelo hook `StopFailure` com `rate_limit` ou pela tela de limite; Codex por `rate_limit_reached_type` preenchido; Antigravity pelo erro de cota no fim do turno ou por uma janela com fração restante zero.
- O personagem vai para `blocked` com o motivo `quota` ("aguardando cota") e o horário em que a cota volta, quando conhecido ([personagens](../orquestracao/personagens.md#estados)).
- O Mestre da mesa recebe um evento na caixa de entrada com: personagem, provedor, conta, quando a cota volta e as **fichas da mesma classe que usam outro provedor ou outra conta**, se existirem. Ele decide se troca a ficha (`recruit --replace`, [coordenação](../cli/coordenacao.md)) ou espera.
- Se quem ficou sem cota é o próprio Mestre da mesa, o evento vai ao Grão-Mestre; se é o Grão-Mestre, o motor avisa os usuários.
- A cota voltou (horário passou, ou o provedor voltou a responder, ou o `/usage` mostra fração maior que zero): o motor tira o `blocked` e entrega o que estava na fila.

### O que o painel mostra

Consumo por personagem, missão, mesa, campanha, conta e dia; limites e quanto falta; e, por perfil de conta, as janelas de limite do provedor em percentual com o horário de renovação ([administração](../painel/administracao.md)).

## Regras e erros

- Fonte de uso indisponível (statusLine não roda, JSONL não encontrado) → registra só tempo ativo e turnos e abre o alerta ALL-23 do [checklist](checklist.md) (quem lê a fonte avisa o motor: `Service.SourceUnavailable`).
- Evento de um personagem que o registro não conhece, ou sem ficha com provedor, é descartado com aviso no log: não há missão, mesa ou campanha onde pô-lo.
- Formato de uso mudou numa versão nova do provedor → o [teste de conformidade](../provedores/conformidade.md) acusa; até a correção, valem tempo ativo e turnos.
- Um limite configurado acima do teto é recusado na hora de salvar.

## Critérios de aceite

- **CA-USO-01** · *rápida* — **Dado** um registro de statusLine gravado, **quando** o motor soma, **então** a mesa e a campanha mostram o custo e os tokens daquele turno.
- **CA-USO-02** · *rápida* — **Dado** um limite de mesa em 10 USD e uso em 8 USD, **quando** chega novo registro, **então** sai um único alerta de 80%; um segundo registro abaixo de 100% não gera outro.
- **CA-USO-03** · *rápida* — **Dado** uma mesa a 100% com um personagem ocupado, **quando** o limite é atingido, **então** o turno dele termina normalmente, a mesa fica pausada e nasce um pedido `custo_extra`.
- **CA-USO-04** · *rápida* — **Dado** um personagem Codex com `rate_limit_reached_type` preenchido, **quando** o uso é lido, **então** ele fica `blocked` com motivo `quota` e o Mestre da mesa recebe as fichas alternativas da mesma classe.
- **CA-USO-05** · *rápida* — **Dado** um personagem aguardando cota até 14:00 (relógio falso), **quando** passa das 14:00, **então** ele sai de `blocked` e recebe a fila.
- **CA-USO-06** · *rápida* — **Dado** um provedor sem dado de tokens, **quando** o turno termina, **então** o registro tem tempo ativo e um turno.
- **CA-USO-07** · *rápida* — **Dado** um pedido `custo_extra` que passaria o teto, **quando** o Grão-Mestre tenta aprovar, **então** é recusado e o pedido vai aos usuários Administrador ou acima.
- **CA-USO-09** · *rápida* — **Dado** um registro de uso gravado de um turno em que o personagem usou um subagente nativo, **quando** o uso é somado, **então** os tokens do subagente entram no personagem, na missão e na mesa dele.
- **CA-USO-08** · *pesada* — **Dado** uma mesa trabalhando, **quando** abro o painel, **então** vejo o consumo da mesa e de cada personagem (MVP-11).
- **CA-USO-10** · *rápida* — **Dado** a janela de 5 h em 96% numa leitura de 20 min atrás com `resets_at` daqui a 1 h (relógio falso), **quando** o motor vai entregar uma etapa nova, **então** não entrega e a mesa fica aguardando cota até o reset, e passado o reset a etapa é entregue; **dado** 40% numa leitura de 20 min atrás, **então** o uso fica "desconhecido" e a etapa é entregue com aviso.
- **CA-USO-11** · *rápida* — **Dado** um limite de mesa acima do teto de custo da campanha, **quando** o Administrador salva, **então** é recusado; **dado** um limite diário total salvo por um Administrador, **então** é recusado e só o Senhor da Guilda consegue.
- **CA-USO-12** · *rápida* — **Dado** uma conformidade que grava 2 USD de origem `conformidade`, **quando** o motor soma, **então** o limite da mesa e o da campanha não mudam e o diário total sobe; **dado** a fonte de uso de um personagem indisponível, **então** só tempo ativo e turnos são gravados e o ALL-23 abre um aviso.
- **CA-USO-13** · *rápida* — **Dado** uma Cidadela com um personagem Codex que tem perfil de conta, outro Codex sem perfil, um de outra máquina e um do Claude Code, **quando** a leitura periódica roda, **então** só o primeiro é lido do transcript (a leitura seguinte continua de onde a anterior parou e, sem nada novo, não grava) e o segundo vira `SourceUnavailable` (ALL-23), sem adivinhar pasta; **dado** um `agent.event` de uma Torre, **então** o evento chega ao uso se o registro põe o personagem naquela Torre e é recusado com `forbidden` se o põe em outra.
- **CA-USO-14** · *rápida* — **Dado** uma Torre com um personagem Codex que tem perfil de conta e outro sem, **quando** a leitura periódica da Torre roda com a Cidadela conectada, **então** a Cidadela recebe um `usage.reading` com o uso do primeiro (a leitura seguinte continua de onde a anterior parou e, sem nada novo, não envia) e o segundo vira `SourceUnavailable` uma vez; **dado** a Torre sem a Cidadela, **então** nada é lido nem enviado; **dado** um `usage.reading` de uma Torre onde o registro não põe o personagem, **então** é recusado com `forbidden` e nada é gravado.

## Depende de

- [Adaptador de provedores](../provedores/adaptador.md)
- [Persistência](../arquitetura/persistencia.md)
- [Fluxo de aprovação](../aprovacoes/fluxo.md)
- [Teto da campanha](../classes/teto-do-workspace.md)
- [Mesas](../orquestracao/mesas.md)
- [Personagens](../orquestracao/personagens.md)
- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)
- [Campanhas](../orquestracao/workspaces.md)
- [Política de aprovação](../aprovacoes/politica.md)
- [Bot do Telegram](../telegram/bot.md)

## Verificar na implementação

- **Verificar na implementação:** as fontes de uso de cada provedor incluem o consumo dos subagentes. **Padrão:** Claude Code, pelo custo acumulado do statusLine e pelo uso do subagente no PostToolUse da ferramenta `Agent`; Codex, pelos eventos `token_count` das threads filhas no JSONL da sessão; Antigravity, pelo `/usage` do perfil (que já soma tudo). **Se falhar:** vale o tempo ativo do personagem (que inclui o subagente) e o registro é marcado como estimado. **Onde:** etapas 7 e 12 do [teste de conformidade](../provedores/conformidade.md).

## Fontes

- Claude Code, dados do statusLine e custos: https://code.claude.com/docs/en/statusline#available-data , https://code.claude.com/docs/en/monitoring-usage
- Claude Code, limites de uso: https://code.claude.com/docs/en/errors#usage-limits
- Codex, configuração: https://learn.chatgpt.com/docs/config-file/config-reference
- Antigravity, `/usage`: https://antigravity.google/docs/cli/commands/usage
