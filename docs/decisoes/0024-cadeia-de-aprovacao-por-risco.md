# ADR 0024: Cadeia de aprovação por risco

> **Assunto:** por que cada pedido de aprovação recebe um risco dado pelo motor e sobe do superior de quem pediu até as pessoas, com cada nível decidindo só até um teto de risco · **Componente:** Motor · **Atualizado:** 2026-10-14

## Status

Aceita, 2026-10-14, por decisão do Dono. **Substitui** o [ADR 0008](0008-grao-mestre-aprovador-delegado.md): o Grão-Mestre deixa de ser o aprovador padrão de tudo e passa a ser um nível da cadeia. A parte do GitHub do 0008 (aprovação pelo App "Grão-Mestre", plano B só por ruleset, credencial pessoal por device flow) continua valendo sem mudança e fica registrada aqui.

**Revisão 2026-10-14** (Dono, no mesmo dia, antes do M4): o Grão-Mestre é um por workspace e atende muitas mesas, agentes e máquinas, então não pode ser o gargalo do `medio`; as decisões vão para perto do trabalho. Mudou, e a Decisão abaixo já está revista:
- **classificação (M3):** parte sem linha na tabela e sem sinal de risco é `baixo`, não `medio`; os sinais (rede para domínio não listado, apagar, caminho fora do worktree, instalação global ou do sistema, contêiner ou VM, interpretador com código, o que não se lê) sobem; um padrão aprovado só cobre um comando cuja classificação própria cabe no risco dele ([classificação](../classes/classificacao-de-risco.md#sinais-de-risco));
- **quem decide o `medio` (M4, M5):** o Mestre da mesa decide o `medio` dos personagens da própria mesa com condições que o motor confere; o Grão-Mestre vira supervisor ([cadeia](../orquestracao/cadeia-de-aprovacao.md#a-cadeia));
- **freios e visibilidade (M4, M5):** resumo não bloqueante ao Grão-Mestre, amostra das decisões das mesas no resumo do Dono, pausa de autonomia por mesa, métricas de fila por nível com alerta ([supervisão](../orquestracao/supervisao-da-cadeia.md));
- **classe embutida `orquestrador` (M4):** as regras efetivas dos orquestradores, para a atenuação ter o que conferir ([classe](../classes/classe-orquestrador.md));
- **respostas do Dono:** `acao_destrutiva` continua delegável pelo Dono (decreto com PIN, escopo e prazo); `make *` e `npm run *` continuam `baixo`.

## Contexto

- Pelo ADR 0008, todo pedido ia primeiro ao Grão-Mestre, que decidia dentro da [política](../aprovacoes/politica.md) ou perguntava às pessoas. Na prática:
  - pedidos triviais de um personagem (instalar um pacote, rodar um script do projeto) paravam a mesa até o Grão-Mestre, ocupado com conversa e GitHub, ter um turno livre;
  - o Mestre da mesa, que conhece a tarefa, não podia decidir nada;
  - o mesmo agente decidia coisas muito diferentes (um `npm install` e uma ação destrutiva) com o mesmo critério: "se as condições passarem" ou "nunca".
- Um agente aprovador é um alvo: pode ser manipulado pelo conteúdo que lê, inclusive pela justificativa do agente que pede. Quem decide até onde ele pode ir precisa ser o motor, não um texto.
- As regras fixas, as regras efetivas e o teto ([precedência](../arquitetura/precedencia.md)) já decidem a maior parte das ações sem perguntar; só o que termina em `ask` precisa de alguém.

## Opções consideradas

1. **Manter o 0008** (Grão-Mestre decide tudo, pessoas na dúvida).
   - Prós: simples, um aprovador só.
   - Contras: gargalo no Grão-Mestre; o mesmo julgamento para riscos diferentes.
2. **Cada agente aprova o que quiser dos subordinados, e o agente classifica o risco.**
   - Prós: rápido.
   - Contras: um agente manipulado classifica a própria ação como baixa e a aprova; sem limite verificável.
3. **Só regras determinísticas, sem agentes aprovando** (tudo que é `ask` vai às pessoas).
   - Prós: previsível.
   - Contras: as pessoas viram gargalo de pedidos triviais.
4. **Risco dado pelo motor, cadeia do superior até as pessoas, teto de risco por nível e salvaguardas conferidas pelo motor.**
   - Prós: o trivial se decide perto de quem pediu; o arriscado chega às pessoas; o que cada nível pode é verificável; tudo auditado.
   - Contras: mais estados e regras; os agentes aprovadores ainda podem errar dentro do teto deles.

## Decisão

Opção 4.

1. **As regras decidem primeiro** (regras fixas, exceções, regras efetivas, como hoje). Só o que termina em `ask` entra na cadeia.
2. **Risco por pedido**, classificado pelo motor, de forma determinística, pelo tipo da ação e pelo alvo: `baixo`, `medio`, `alto` e `fixo` (este segue as regras próprias de hoje: ninguém, ou o Dono com PIN). A tabela, o risco de cada tipo de pedido e os ajustes do Dono estão em [classificação de risco](../classes/classificacao-de-risco.md).
3. **A cadeia:** quem pediu → o superior (quem o criou) → … → Mestre da mesa → Grão-Mestre → pessoas. Agentes superiores decidem até `baixo`; o Mestre da mesa, `baixo` e o `medio` dos personagens da própria mesa quando passam as condições da mesa, conferidas pelo motor por sinal (rede só para domínios que a classe ou o teto já listam, apagar só dentro do worktree, nada que cruze mesas), nunca os pedidos dele nem os pontos de aprovação que desenhou; o Grão-Mestre, como supervisor, `baixo` e `medio` dos pedidos dos Mestres de mesa, dos que cruzam mesas, dos de mesa sem Mestre que responda e dos `medio` em que as condições falham, dentro da política, e `alto` só com delegação do Dono com PIN; as pessoas pelo nível mínimo, com quórum. Acima do teto ou no fim do prazo, o pedido sobe sozinho, e subir não é reenviar. Superior de cada caso, prazos e subida: [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md).
4. **Salvaguardas do motor:** ninguém aprova além do que poderia fazer pelas próprias regras efetivas (as dos orquestradores vêm da classe embutida `orquestrador`, igual em todo workspace, que o teto só restringe); ninguém decide em causa própria (generaliza o "não aprova proposta feita por ele mesmo"); o Mestre da mesa não decide os pontos de aprovação que desenhou; o agente vê o pedido montado pelo motor, com a justificativa marcada como texto não verificado; tudo é auditado; 3 negações seguidas pausam o personagem ([cadeia](../orquestracao/cadeia-de-aprovacao.md#salvaguardas)).
5. **Respostas** filtradas por quem responde: aprovar agora, por um tempo, o padrão, sempre para a classe; delegar; negar agora, com instrução, sempre; perguntar de volta. Prazo esgotado é negar ([respostas](../orquestracao/cadeia-de-aprovacao.md#respostas)).
6. **Prazos por nível:** agentes 2 min, Grão-Mestre 10 min, pessoas pela política; configuráveis no [arquivo de política](../aprovacoes/arquivo-de-politica.md).
7. **Regras temporárias de mesa:** criadas por agentes dentro do que podem decidir, distintas dos decretos; só transformam `ask` em `allow`, abaixo dos decretos de pessoas e acima das regras da classe ([regras temporárias](../orquestracao/regras-temporarias-de-mesa.md)). O Grão-Mestre continua sem criar decretos permanentes.
8. **Aprendizado e freios:** aprovações repetidas viram sugestão de regra no resumo do Dono, com exemplos; o resumo traz também uma amostra do que os agentes aprovaram, à parte a dos Mestres de mesa; pedidos parecidos se agrupam ([resumo e sugestões](../aprovacoes/resumo-e-sugestoes.md)). O Grão-Mestre recebe um resumo não bloqueante das decisões das mesas e, com as pessoas, pode pausar a autonomia `medio` de uma mesa; filas por nível têm métricas e alerta ([supervisão](../orquestracao/supervisao-da-cadeia.md)).
9. **Delegação generalizada** das pessoas ao Grão-Mestre, por tipo e escopo, por até 30 dias, com subdelegação mais estreita a um Mestre da mesa; e o **botão de emergência** do Dono, que revoga todas as delegações e regras temporárias ([delegação](../aprovacoes/delegacao.md)).
10. **`default` ausente numa classe vale `ask`** (em `commands` e em `tools`), no lugar de `deny` ([formato das permissões](../classes/formato-das-permissoes.md)): um comando novo vira pedido, que agora se decide perto de quem pediu, em vez de uma negação sem caminho.
11. **No GitHub** (do 0008, sem mudança): o Grão-Mestre aprova com o App "Grão-Mestre"; se a aprovação do App não contar para revisões obrigatórias, vale o plano B só por ruleset e só com aprovação do usuário; sem isso, o repositório pede uma pessoa. A credencial pessoal é um user access token do App obtido por device flow, guardado no cofre e usado só com delegação ou com a aprovação da pessoa ([aprovação no GitHub](../github/aprovacao-no-github.md)).

**Marcos:** a classificação (com os sinais e o que um padrão cobre) e o `default` ausente no M3; a cadeia, as condições da mesa, a classe `orquestrador`, os prazos, as esperas, as regras temporárias, a pausa de autonomia e as filas no M4; respostas, botões, delegação, resumo e comandos de decisão no M5; a central do painel no M6 ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)).

## Consequências

- **Positivas:**
  - Pedidos baixos e os médios com as condições da mesa se resolvem em minutos dentro da mesa; o Grão-Mestre supervisiona e decide o que cruza mesas ou falha nas condições; as pessoas, o alto e as dúvidas.
  - Até onde cada agente pode aprovar é uma regra do motor, testável, que nenhum texto muda.
  - Aprovações repetidas viram regras explícitas, revistas pelo Dono, em vez de perguntas sem fim.
- **Negativas:**
  - **Erro dentro do teto:** um Mestre da mesa pode aprovar um `baixo` ou um `medio` ruim da própria mesa. Mitigações: as condições da mesa, conferidas pelo motor; atenuação; auditoria; resumo ao Grão-Mestre e amostra ao Dono; pausa de autonomia; disjuntor.
  - **Classificação por sinais:** o que não tem linha e não mostra sinal é `baixo`; um comando novo e arriscado sem sinal reconhecido passa como `baixo`. A lista de sinais erra para cima, e o Dono pode subir qualquer padrão; descer, só de `medio` para `baixo`.
  - **`default` ausente mais aberto:** uma classe sem `default` passa a pedir aprovação onde antes negava. Classes que querem negar o resto precisam escrever `default: deny`; o teto continua podendo fechar.
  - **Mais estados e mais tipos de regra** (níveis da cadeia, regras temporárias, subdelegação), todos com auditoria e prazo.
- **Pontos do 0008 que seguem abertos** (review do App próprio conta, `actor_type: Integration` em repositório pessoal, token de usuário conta como dele): [aprovação no GitHub](../github/aprovacao-no-github.md#verificar-na-implementação).
