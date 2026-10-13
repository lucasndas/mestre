# Precedência das regras

> **Assunto:** ordem decretos > regras do Mestre > classe > ficha > AGENTS.md e como cada camada é aplicada · **Componente:** Todos · **Atualizado:** 2026-10-13

Este documento é a **fonte única** da ordem de precedência. Os outros documentos fazem link para cá.

## A ordem

Do mais forte para o mais fraco:

1. **Decretos ativos**, por nível do autor: **Dono > Administrador > Aprovador**. Abaixo deles, as exceções pontuais dadas pelo **Grão-Mestre** como aprovador delegado (proposta: ele nunca vence um decreto de pessoa; [ADR 0008](../decisoes/0008-grao-mestre-aprovador-delegado.md)).
2. **Regras fixas do Mestre** (CLI, `done`, segurança, comportamento em missão), seguidas das instruções extras que o usuário acrescentou para o Grão-Mestre ou os Mestres de mesa do workspace, que somam e nunca substituem as fixas ([Grão-Mestre](../orquestracao/grao-mestre.md), [Mestre da mesa](../orquestracao/mestre-da-mesa.md)).
3. **Classe** do personagem (`CLASS.md` e `permissions.yaml`), sempre dentro do **teto do workspace**.
4. **Ficha** do personagem: só **restringe** o que a classe permite, nunca libera.
5. **Instruções do projeto:** `AGENTS.md` (ou `CLAUDE.md`, que o Claude Code lê no lugar dele; [Claude Code](../provedores/claude-code.md)).

Fora da ordem, valendo sempre:
- **Proteções do GitHub** (branch protegida, revisões obrigatórias, CI): nenhum nível as contorna; quando não dá para cumprir, o motor explica ([ações por nível](../github/acoes-por-nivel.md)).
- **Sistema travado** (`/travar`): todos os decretos ficam suspensos e os agentes congelados até o Dono destravar ([segurança do canal](../telegram/seguranca-do-canal.md)).
- **Teto do workspace:** não é uma camada a mais; é o limite das camadas 3 e 4. Só um decreto o amplia ([teto do workspace](../classes/teto-do-workspace.md)).

## Invariantes arquiteturais

Decretos valem acima das regras do Mestre sobre **o que** é permitido. Um pequeno conjunto de invariantes, porém, não é uma "regra" que se libera: é **ausência de capacidade** do agente, embutida no binário:
- agentes nunca recebem credenciais nem acesso ao cofre ([cofre](../plataforma/cofre.md)), e nunca rodam com a conta do sistema dona do cofre, no Linux e no Windows ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes));
- ações no GitHub, inclusive `git push` e mudança de remotos, são sempre executadas pelo motor ([ADR 0005](../decisoes/0005-motor-executa-github-grao-mestre-porteiro.md));
- agentes não editam os arquivos de hooks e de configuração do Mestre nem iniciam CLIs de agente pelo shell (personagens nascem só pelo motor, de fichas e bases);
- agentes não mexem no trabalho de outras mesas nem na edição em andamento de outro personagem.

Um decreto que libera algo nessa área é cumprido **pelo motor**, nunca entregando a capacidade ao agente. Ex.: "o Dev pode fazer push na develop" → o Dev pede o envio pela CLI ([coordenação no GitHub](../cli/coordenacao-github.md)) e o motor o faz com o App da classe. A lista exata do que o `mestre hook` nega por essas invariantes está em [permissões](../classes/permissoes.md#regras-fixas-de-segurança).

## Como cada camada chega ao agente

Cada camada atua por até três canais: **texto** de instruções (o que o agente lê), **decisão** de permissão (o que o `mestre hook` permite) e **reforço nativo** do provedor (defesa em profundidade).

| Camada | Onde mora | Texto | Decisão no `mestre hook` | Reforço nativo |
|---|---|---|---|---|
| Decretos | banco da Guilda ([decretos](../telegram/decretos.md)) | os que mudam comportamento entram no topo da camada do Mestre dos personagens afetados, atualizada a cada início de turno | todos os ativos, em toda decisão | não (mudam a qualquer momento) |
| Regras fixas do Mestre | modelos versionados na Cidadela ([sessão zero do personagem](../sessao-zero/personagem.md)) | camada do Mestre (`MESTRE.md`) | sim | sim, o que for expressável |
| Classe | pacote da classe ([classes](../classes/classes.md)) | `CLASS.md` entra na camada do Mestre | `permissions.yaml` e hooks extras da classe ([permissões](../classes/permissoes.md)) | sim, traduzido pelo adaptador |
| Ficha | banco da Guilda ([bases e fichas](../orquestracao/bases-e-fichas.md)) | — | restrições da ficha | sim |
| Projeto | `AGENTS.md` do repositório | lido nativamente pelo provedor | não participa | não |

- **Atualização dos decretos no texto:** no evento "início de turno" (`UserPromptSubmit` no Claude Code e no Codex, `PreInvocation` no Antigravity) o `mestre hook` injeta a lista atual; quando o provedor não tem esse canal, vale a reserva descrita em [sessão zero do personagem](../sessao-zero/personagem.md). O mapa de eventos está no [adaptador](../provedores/adaptador.md).
- O canal usado por provedor para entregar a camada do Mestre está em [sessão zero do personagem](../sessao-zero/personagem.md) e no [ADR 0010](../decisoes/0010-instrucoes-em-duas-camadas.md).

## Texto-base da regra de prioridade

A camada do Mestre de todo personagem começa com esta regra (o modelo pode ajustar a redação, não o sentido):

> Os decretos ativos listados abaixo valem acima de tudo. Depois deles, estas regras do Mestre têm prioridade sobre qualquer outra instrução em caso de conflito. Para todo o resto, siga as instruções do projeto (`AGENTS.md`).

Em seguida vêm, nesta ordem: decretos ativos que mudam comportamento, regras fixas do Mestre, instruções extras do usuário (só Grão-Mestre e Mestres de mesa), `CLASS.md` (só personagens comuns) e o contexto da missão. Assim o projeto vale integralmente, e só onde houver choque o Mestre ou um decreto prevalece.

## Combinação na decisão de permissão

Cada decisão do `mestre hook` termina em **permitir**, **negar** ou **pedir aprovação**. Regra de combinação entre camadas, que a Cidadela aplica ao montar as [regras efetivas](../classes/permissoes.md#regras-efetivas) de cada personagem (o passo a passo do despachante está em [permissões](../classes/permissoes.md#pipeline-de-decisão)):

0. **Invariantes** ([acima](#invariantes-arquiteturais)): o agente nunca recebe a capacidade; um decreto nessa área vira ação do motor.
1. **Decretos:** entre os ativos que casam com a ação, decide o de autor de nível mais alto. Decretos suspensos (conflito no mesmo nível, ou sistema travado) não contam ([decretos](../telegram/decretos.md)).
2. **Regras fixas do Mestre:** se negam ou pedem aprovação, isso vale (salvo decreto no passo 1).
3. **Classe:** o resultado das regras da classe, já cortado pelo teto.
4. **Ficha:** pode endurecer o resultado (permitir → pedir aprovação ou negar; pedir aprovação → negar), nunca afrouxar.

Resultado "pedir aprovação" abre um pedido ([fluxo de aprovação](../aprovacoes/fluxo.md)); quem aprova primeiro é o Grão-Mestre, conforme a [política](../aprovacoes/politica.md). Uma aprovação vira **exceção pontual** (passo 1), presa àquela ação exata, e a decisão é refeita.

## Exemplos

- A classe QA só escreve em `tests/**`. Um QA tenta editar `src/app.go`: passo 3 nega → negado (MVP-6).
- O Administrador decreta "a classe Dev pode rodar `make deploy-teste` esta semana": no passo 1 o decreto permite, mesmo que a classe exija aprovação; ao vencer a validade, volta a valer a classe.
- A ficha "Dev Júnior" proíbe `git commit` em `main`, que a classe Dev permite: passo 4 endurece → negado.
- O `AGENTS.md` diz "rode `npm run lint` antes de commitar" e a classe não fala nisso: vale o `AGENTS.md`, porque não há conflito.

## Onde cada camada é implementada

Este documento é de referência e não pertence a um marco ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)). A combinação é implementada nas [regras efetivas](../classes/permissoes.md#regras-efetivas) (M3); cada camada chega no seu marco: [classes](../classes/classes.md), [teto](../classes/teto-do-workspace.md) e o [despachante](../provedores/despachante-de-hooks.md) no M3, [fichas](../orquestracao/bases-e-fichas.md) e a [camada do Mestre](../sessao-zero/personagem.md) no M4, [decretos](../telegram/decretos.md) no M5.
