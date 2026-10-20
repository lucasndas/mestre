# Resumo e sugestões

> **Assunto:** resumo periódico do que os agentes da cadeia decidiram, sugestões de regra a partir de aprovações repetidas e amostra das ações aprovadas por agentes para revisão depois · **Componente:** Motor · **Atualizado:** 2026-10-20

## Objetivo

Deixar as pessoas verem depois o que os agentes da [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md) decidiram sozinhos, e transformar aprovações que se repetem em regras explícitas, revistas pelo Dono, em vez de perguntas sem fim ([ADR 0024](../decisoes/0024-cadeia-de-aprovacao-por-risco.md)).

## Comportamento

### Resumo periódico

- No horário da seção `resumo` do [arquivo de política](arquivo-de-politica.md) (padrão: diário, 18:00, no fuso do workspace), o motor coloca na caixa de entrada do Grão-Mestre a lista do que os agentes da cadeia (ele e os Mestres de mesa) aprovaram ou negaram desde o último resumo, com nível, risco e motivo. Ele escreve um resumo curto e envia aos usuários de `resumo.para`.
- Sem nada no período, nada é enviado. Na primeira vez que o motor vê um workspace ele só anota o horário: nada de antes dele é enviado. `frequencia: semanal` junta sete dias; `desligado` não envia nada. O horário vale no fuso do workspace (até o workspace ter o seu, o da Guilda).
- `resumo.para` aceita `dono`, `administradores` (Administrador ou acima do workspace) ou o nome de um usuário.
- Junto com o resumo, o motor manda **direto**, sem passar pelo Grão-Mestre, duas partes montadas por ele: as sugestões de regra (só ao Dono) e a amostra (a todos de `resumo.para`). O agente não escolhe o que entra nelas, porque elas revisam também as decisões dele.

### Sugestões de regra

- **Aprovações repetidas:** quando ações aprovadas na cadeia (por agentes ou pessoas) para a mesma classe e o mesmo tipo de ferramenta casam um mesmo padrão candidato `resumo.sugerir_apos` vezes (padrão: 3) em 7 dias, o motor sugere uma regra. O padrão candidato é o programa e o subcomando literais seguidos de ` *` (`npm install lodash` e `npm install react` dão `npm install *`), e só entra se for um [padrão aprovável](../classes/classificacao-de-risco.md#padrões-aprováveis). Comando com menos de três palavras, encadeado (`;`, `&&`, `|`, redirecionamento, aspas, `$`) ou com opção no lugar do subcomando não gera candidato.
- **"Sempre" de agentes:** as respostas "aprovar sempre" e "negar sempre" dos agentes ([respostas](../orquestracao/cadeia-de-aprovacao.md#respostas)) também viram sugestão, com o motivo deles.
- Cada sugestão mostra o padrão, a classe, o escopo (a mesa, ou o workspace se veio de mais de uma), o risco do padrão, até 5 ações já aprovadas que ele casa e exemplos do que ele liberaria a mais (a forma geral, como `npm install <qualquer pacote>`, e o que a tabela de risco diz do padrão).
- Botões ao Dono: **Aceitar**, **Recusar** e **Ver exemplos** (a forma geral que o padrão libera e o que a tabela de risco diz dele). **Aceitar** cria um decreto `rule` de efeito `permission` (ou de negação, no "negar sempre") do Dono, na classe e no escopo da sugestão, com o PIN de todo decreto permanente ([decretos](../telegram/decretos.md#confirmação-reforçada-com-pin)). **Recusar** registra a recusa e o mesmo padrão não é sugerido de novo por 30 dias.
- Sugestão sem resposta continua no resumo seguinte, até 7 dias; depois sai. Uma sugestão igual a outra aberta não se repete.

### Amostra para revisão

- Cada resumo traz uma amostra sorteada de `resumo.amostra` (padrão: 5) ações aprovadas por agentes no período **de cada nível** (todas, se forem menos): uma das decisões dos Mestres de mesa, que agora decidem o `medio` das mesas ([cadeia](../orquestracao/cadeia-de-aprovacao.md#condições-da-mesa)), e outra das do Grão-Mestre; cada ação vem com o pedido montado pelo motor, quem aprovou, o nível, o risco e o motivo. O sorteio usa uma fonte aleatória injetada no motor, para o teste fixar a semente.
- Botão **Foi um erro** (só aparece para Administrador ou acima, [níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)): registra a marcação na auditoria da decisão e revoga a regra temporária que tenha nascido dela; a pessoa pode então criar um decreto que proíbe pelo caminho normal.
- Botão **Pausar autonomia** (um por mesa que aparece na amostra, para Aprovador ou acima): chama a mesma pausa do aviso de fila ([supervisão](../orquestracao/supervisao-da-cadeia.md#pausa-de-autonomia)), sem PIN, em nome de quem tocou.

## Regras e erros

- Resumo, sugestões e amostra nunca mostram segredos: o texto passa pelo mascaramento ([segurança](../arquitetura/seguranca.md#mascaramento-em-logs)).
- Telegram fora do ar: as partes ficam no painel e são entregues quando ele volta, como as outras mensagens ([bot](../telegram/bot.md)).
- Aceitar uma sugestão cujo padrão a tabela de risco passou a dar como `fixo` ou que um ajuste tornou inválido: recusado, com o motivo.

## Critérios de aceite

- **CA-APROV-RES-01** · *rápida* — **Dado** três aprovações do Grão-Mestre e duas do Mestre da mesa no dia e o horário do resumo, **quando** o relógio falso chega a 18:00, **então** a caixa de entrada do Grão-Mestre recebe a lista das cinco, com nível e risco; **dado** um dia sem decisões de agentes, **então** nada é enviado.
- **CA-APROV-RES-02** · *rápida* — **Dado** três aprovações de `npm install <pacote>` para a classe Dev em 7 dias no relógio falso, **quando** sai o resumo, **então** o Dono recebe a sugestão `npm install *` com as três ações e os exemplos; **quando** toca **Aceitar** e confirma o PIN, **então** nasce um decreto do Dono na classe Dev; **dado** **Recusar**, **então** o padrão não volta por 30 dias.
- **CA-APROV-RES-03** · *rápida* — **Dado** aprovações repetidas de `python script.py` e de `sh -c x`, **quando** o motor procura sugestões, **então** nenhuma é feita para `python *` nem `sh *`.
- **CA-APROV-RES-04** · *rápida* — **Dado** doze ações aprovadas por agentes no período e a fonte aleatória falsa com semente fixa, **quando** sai o resumo, **então** a amostra de cada nível com mais de cinco tem cinco ações, as mesmas a cada execução com a mesma semente, enviadas pelo motor mesmo que o texto do Grão-Mestre não as cite; **quando** alguém toca **Foi um erro** numa que criou uma regra temporária, **então** a regra fica `revogada` e a marcação vai à auditoria.
- **CA-APROV-RES-05** · *rápida* — **Dado** um "aprovar sempre" do Mestre da mesa, **quando** sai o resumo, **então** o Dono recebe a sugestão com o motivo do agente, e nenhum decreto existe antes do **Aceitar** com PIN.
- **CA-APROV-RES-06** · *rápida* — **Dado** oito ações aprovadas por Mestres de mesa (três `medio`) e duas pelo Grão-Mestre no período e a fonte aleatória falsa, **quando** sai o resumo, **então** o Dono recebe cinco ações dos Mestres de mesa, sorteadas entre as oito, e as duas do Grão-Mestre, em partes separadas, cada uma com nível e risco.

## Depende de

- [Fluxo de aprovação](fluxo.md) · [Decretos](../telegram/decretos.md) · [Arquivo de política](arquivo-de-politica.md)
- [Cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md) · [Regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md) · [Classificação de risco](../classes/classificacao-de-risco.md)
- [Bot do Telegram](../telegram/bot.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
