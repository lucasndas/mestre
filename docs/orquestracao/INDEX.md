# Orquestração

> **Assunto:** índice da orquestração: campanhas, mesas, sincronia com a base, prova e devolução, dependência entre mesas, orquestradores, personagens, presets, conexões, esperas, cadeia de aprovação, regras temporárias de mesa, missões, rituais, pergaminhos e grimórios · **Componente:** Motor · **Atualizado:** 2026-10-18

| Documento | O que cobre | Quando ler |
|---|---|---|
| [workspaces.md](workspaces.md) | árvore de níveis, espelho e clone principal, slug, configuração e estados da campanha | ao mexer no que é por repositório ou no espelho |
| [mesas.md](mesas.md) | abertura, estados, pausa, worktree por máquina e sincronização por bundle, encerramento e cancelamento | ao mexer no ciclo de vida da mesa ou na sincronização de worktrees |
| [sincronia-com-a-base.md](sincronia-com-a-base.md) | a etapa `sync`: integrar a base na branch da mesa, conflito como tarefa do Dev, chegada = verificação verde | ao mexer no que acontece quando a base anda depois da mesa aberta |
| [prova-e-devolucao.md](prova-e-devolucao.md) | prova de cada critério no `done`, o `done` que a classe exige (prova e verificação pelo motor), devolução com causa e "mesma falha repetida", pergaminho de revisão | ao mexer no que uma etapa precisa provar ou em como uma devolução é contada |
| [dependencia-entre-mesas.md](dependencia-entre-mesas.md) | a espera do tipo "épico": a mesa dependente espera o PR da outra ser mesclado e confirmado na base; sobreposição e ciclo | ao mexer em pedidos encadeados ou na ordem entre mesas |
| [grao-mestre.md](grao-mestre.md) | papel, triagem e roteamento do pedido, mensagem de progresso, proteção, retomada, instruções do sistema e instruções extras do Grão-Mestre | ao mexer no orquestrador da campanha ou no modelo de instruções dele |
| [mestre-da-mesa.md](mestre-da-mesa.md) | papel, liberdade dentro do teto, proteção, instruções do sistema e extras do Mestre da mesa | ao mexer no orquestrador da mesa ou no modelo de instruções dele |
| [personagens.md](personagens.md) | criação sob demanda, escolha da máquina, limites, ciclo de vida, encerramento seguro, fixar, estados e histórico de estados, registro de terminais, retomada | ao mexer em criação, estados ou encerramento de terminais |
| [personagens-no-codigo.md](personagens-no-codigo.md) | como o código implementa os personagens: Service e Runtime, registro, limites, prontidão, encerramento, retomada e a ligação com as conexões | ao mexer em `internal/orchestration/characters` |
| [bases-e-fichas.md](bases-e-fichas.md) | campos de bases e fichas, exemplos, exportação/importação, perfis de conta | ao mexer em presets ou em várias contas por provedor |
| [conexoes.md](conexoes.md) | conexões automáticas e manuais, remoção, visibilidade e como o código as guarda | ao mexer em quem fala com quem, no `mestre list` ou no `connect` |
| [esperas-e-caixa-de-entrada.md](esperas-e-caixa-de-entrada.md) | fila e prioridade, entrega só quando livre, resposta pelo mesmo canal, ask e ask back, esperas, tempos limite, impasse, persistência e como o código as guarda | ao mexer em entrega de mensagens ou esperas |
| [cadeia-de-aprovacao.md](cadeia-de-aprovacao.md) | superior de cada personagem, níveis da cadeia e o risco até onde cada um decide, condições da mesa para o `medio`, subida por teto ou prazo, salvaguardas, pedido montado pelo motor, catálogo de respostas | ao mexer em quem decide um pedido de aprovação |
| [supervisao-da-cadeia.md](supervisao-da-cadeia.md) | o Grão-Mestre como supervisor: resumo não bloqueante das decisões das mesas, pausa de autonomia por mesa, métricas de fila por nível e alertas | ao mexer na visibilidade e nos freios das decisões de agentes ou nas filas de aprovação |
| [regras-temporarias-de-mesa.md](regras-temporarias-de-mesa.md) | regras que agentes da cadeia criam ao aprovar por um tempo ou um padrão: alcance, validade, precedência, como acabam | ao mexer no que agentes liberam por um tempo numa mesa |
| [missoes.md](missoes.md) | definição, execução pelo motor, devolução, aprovação, estados, retomada, espelho no GitHub | ao mexer em fluxos de etapas |
| [rituais.md](rituais.md) | agenda, pré-execução, alvo ocupado enfileira e volta ao dono, disparos perdidos | ao mexer em rotinas agendadas |
| [pergaminhos-e-tomos.md](pergaminhos-e-tomos.md) | arquivo e índice, nome, operações, concorrência, tomos, pergaminho da missão e como o código os guarda | ao mexer em notas do canvas |
| [grimorios.md](grimorios.md) | salvar e restaurar times entre mesas e campanhas | ao mexer em grimórios |
