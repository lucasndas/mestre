# Delegação

> **Assunto:** decretos `delegation` das pessoas ao Grão-Mestre por tipo e escopo, validade e lembrete, subdelegação a um Mestre da mesa e o botão de emergência do Dono que revoga todas as delegações e regras temporárias · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Deixar as pessoas passarem ao Grão-Mestre, de forma explícita, com escopo e prazo, a decisão de pedidos que estão acima do teto dele na [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md), e retirar tudo de uma vez quando algo sai do controle ([ADR 0024](../decisoes/0024-cadeia-de-aprovacao-por-risco.md)). Generaliza o efeito `delegation` dos [decretos](../telegram/decretos.md), que antes servia só à aprovação no GitHub e ao Operador do PC.

## Comportamento

### Tipos de delegação

| Delegação | O que libera | Dono das regras |
|---|---|---|
| de decisão | o Grão-Mestre decide pedidos de um tipo `alto` dentro do escopo | este documento |
| no GitHub | o Grão-Mestre aprova com a credencial pessoal de quem delegou | [aprovação no GitHub](../github/aprovacao-no-github.md) |
| da ativação do Operador | o Grão-Mestre aprova ativações do Operador do PC | [decretos](../telegram/decretos.md#delegação-da-ativação-do-operador) |
| subdelegação | um Mestre da mesa decide, na mesa dele, parte do que o Grão-Mestre pode | este documento |

### Delegação de decisão

- **Quem cria:** só o Dono, com PIN, para tipos `alto` ([risco dos tipos](../classes/classificacao-de-risco.md#risco-dos-tipos-de-pedido)), inclusive `acao_destrutiva` (decisão do Dono, 2026-10-14: continua delegável, com escopo e prazo). Para tipos `medio`, que o Grão-Mestre já decide dentro da política, quem quer afrouxar usa um decreto `policy` ([política](politica.md#decretos-ajustam-a-política)); a resposta **Delegar** num pedido `medio` monta essa proposta, para um Administrador ou acima. Tipos `fixo` e `custo_extra` acima do teto nunca são delegáveis.
- **Como nasce:** pela resposta **Delegar** a um pedido ([respostas](../orquestracao/cadeia-de-aprovacao.md#respostas)), que já traz o tipo e o escopo do pedido, ou por uma ordem ao Grão-Mestre (`mestre decree propose --effect delegation`, [CLI do Grão-Mestre](../cli/grao-mestre.md#mestre-decree)). O motor mostra o que vai valer e pede Confirmar e o PIN, como em todo decreto ([criação](../telegram/decretos.md#criação)). **Renovar** propõe o mesmo decreto por mais 30 dias, que o autor confirma com o PIN; o antigo acaba na data dele.
- **Escopo obrigatório:** o tipo e pelo menos um entre a mesa, `classe:<nome>` e `repo:<dono/nome>`; sem escopo, ou com um escopo que o motor não entende (`padrao:…`, por exemplo), é recusada. Um decreto só cobre o pedido que cai em todos os escopos dele: a mesa do pedido, a classe de quem pediu e o repositório do alvo.
- **Validade:** "até mudar ou 30 dias": vale até ser revogada ou trocada, por no máximo 30 dias (padrão e máximo; sem as chaves `delegacao.validade_padrao` e `delegacao.aviso_antes` do [arquivo de política](arquivo-de-politica.md) valem 30 dias e o aviso de 3; com elas, a validade padrão do workspace e o aviso mandam, e a renovação dá a mesma validade). 3 dias antes do fim, o autor recebe um lembrete com **Renovar** (mais 30 dias, com PIN) e **Deixar vencer**.
- **O que não muda:** regras de quórum e `exigir_usuarios` continuam mandando perguntar; as salvaguardas da cadeia continuam valendo; a delegação não libera ação nenhuma sozinha, só muda quem decide o pedido.
- Cada decisão tomada com base nela fica registrada no decreto (pedido, decisão, quando).

### Subdelegação

- O Grão-Mestre pode passar a um Mestre da mesa, só na mesa dele, uma parte **mais estreita** do que ele mesmo pode decidir: o teto dele (`medio`, dentro da política, inclusive o que falha nas [condições da mesa](../orquestracao/cadeia-de-aprovacao.md#condições-da-mesa)) ou uma delegação de decisão que ele tem. Mais estreita quer dizer: o mesmo tipo ou menos, o escopo dentro do da origem, a validade até a da origem e nunca depois do encerramento da mesa.
- **Profundidade 1:** o Mestre da mesa não subdelega. A subdelegação estende o teto dele só no escopo dela, com as mesmas salvaguardas.
- Sintaxe: `mestre approval delegate` ([CLI do Grão-Mestre](../cli/grao-mestre.md#mestre-approval)). Fica no banco em `subdelegations`, com a origem (o decreto de delegação, ou nenhuma quando vem do teto `medio` do Grão-Mestre); não é decreto de pessoa nem permanente. A CLI `mestre approval delegate` é a porta dela (`decrees.SubHandler`; o `--until` leva fuso e `--pattern` ainda não vale).
- Acaba com a origem, com o encerramento da mesa, revogada pelo Grão-Mestre ou por um Administrador ou acima.

### Revogar tudo

- **Só o Dono**, pelo botão **Revogar todas as delegações** no painel ([administração](../painel/administracao.md)) ou pelo `/revogar_tudo` no Telegram ([bot](../telegram/bot.md)), com uma confirmação por botão (sem PIN: só restringe).
- Revoga na hora, em todos os workspaces, todas as delegações (de decisão, no GitHub, do Operador e subdelegações) e todas as [regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md). Não para os agentes; para isso há o `/travar` ([segurança do canal](../telegram/seguranca-do-canal.md)).
- Fica na auditoria (um registro de quem e quando, mais o histórico de cada decreto), e avisa os Administradores e o Grão-Mestre de cada workspace. Pedidos abertos que estavam num nível que dependia de uma delegação sobem na hora. As regras temporárias de mesa saem pelo serviço delas, logo depois da transação dos decretos e das subdelegações.

## Regras e erros

- Delegação proposta por quem não tem o nível (um Administrador para um tipo `alto`), sem escopo, com validade acima do máximo ou para tipo não delegável: recusada antes da confirmação, com o motivo.
- Subdelegação mais larga que a origem, para outra mesa que não a do Mestre da mesa, ou pedida por um Mestre da mesa: recusada (`level_denied` para o Mestre da mesa).
- Autor suspenso ou removido: as delegações dele seguem a regra dos decretos ([autor suspenso](../telegram/decretos.md#autor-suspenso-ou-removido)), e as subdelegações que dependem delas acabam junto.

## Critérios de aceite

- **CA-DELEG-01** · *rápida* — **Dado** um pedido `acao_destrutiva` para apagar um pergaminho da mesa `42-login`, **quando** o Dono toca **Delegar** e confirma com o PIN, **então** nasce um decreto `delegation` com tipo `acao_destrutiva`, escopo `mesa:42-login` e validade de 30 dias; o próximo pedido desse tipo nessa mesa é decidido pelo Grão-Mestre e um da mesa `43-busca` recebe `human_required`.
- **CA-DELEG-02** · *rápida* — **Dado** um Administrador, **quando** propõe uma delegação de `envio_workflows`, **então** ela é recusada antes da confirmação; **quando** toca **Delegar** num pedido `merge` (`medio`), **então** a proposta mostrada é um decreto `policy` no tipo e na mesa do pedido.
- **CA-DELEG-03** · *rápida* — **Dado** o relógio falso e uma delegação criada hoje, **quando** passam 27 dias, **então** o Dono recebe o lembrete com **Renovar** e **Deixar vencer**; **quando** passam 30 dias sem renovar, **então** ela expira e um `approval decide` do Grão-Mestre no tipo recebe `human_required`; **dado** uma proposta com validade de 31 dias, **então** é recusada.
- **CA-DELEG-04** · *rápida* — **Dado** o Grão-Mestre com uma delegação de `envio_workflows` na mesa `42-login`, **quando** subdelega ao Mestre dessa mesa por 2 dias, **então** esse Mestre da mesa decide um pedido `envio_workflows` da mesa; **quando** o Grão-Mestre tenta subdelegar à mesa `43-busca` ou por 40 dias, **então** é recusado; **quando** o Mestre da mesa tenta subdelegar, **então** recebe `level_denied`; **quando** o Dono revoga a origem, **então** a subdelegação acaba junto.
- **CA-DELEG-05** · *rápida* — **Dado** delegações e regras temporárias em dois workspaces, **quando** o Dono confirma **Revogar todas as delegações**, **então** todas ficam `revogada` numa transação, a auditoria registra o Dono e o horário e os Administradores e os Grão-Mestres são avisados; **quando** um Administrador manda `/revogar_tudo`, **então** é recusado com o nível exigido.
- **CA-DELEG-06** · *rápida* — **Dado** uma proposta de delegação para `entrada_na_guilda` ou para `custo_extra` acima do teto, **quando** o motor confere, **então** é recusada antes da confirmação com "tipo não delegável".

## Depende de

- [Decretos](../telegram/decretos.md) · [Política de aprovação](politica.md) · [Fluxo de aprovação](fluxo.md) · [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md) · [Regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md) · [Classificação de risco](../classes/classificacao-de-risco.md)
- [Bot do Telegram](../telegram/bot.md)
