# Glossário

> **Assunto:** vocabulário do Mestre e textos de ajuda da interface e do Telegram · **Componente:** Projeto · **Atualizado:** 2026-10-12

Este glossário é a **fonte única** dos textos de ajuda: o "?" dos termos no painel ([canvas](../painel/canvas.md)) e a explicação entre parênteses na primeira vez que um termo aparece numa conversa do Telegram ([bot](../telegram/bot.md)). O build do Mestre lê a tabela abaixo e embute os textos ([convenções](../desenvolvimento/convencoes.md)).

Vocabulário: na interface os rótulos ficam em português, com estes termos; os comandos da CLI ficam em inglês ([convenções da CLI](../cli/convencoes.md)). "O Mestre", sozinho, é sempre o sistema; os agentes orquestradores são sempre "Grão-Mestre" e "Mestre da mesa".

## Formato (lido por máquina)

- A tabela tem exatamente três colunas: `Termo`, `Ajuda curta`, `Detalhes`. Uma linha por termo.
- **Termo:** como aparece na interface, no singular, com maiúscula inicial.
- **Ajuda curta:** uma frase em linguagem simples, até 120 caracteres, sem `|` e sem links. É o texto exibido ao usuário.
- **Detalhes:** um link relativo para o documento dono do assunto (não é mostrado ao usuário final).
- A [verificação de docs](../qualidade/verificacao-de-docs.md) confere o formato, o tamanho e o link.

## Termos

| Termo | Ajuda curta | Detalhes |
|---|---|---|
| Mestre | O sistema inteiro: o orquestrador que inicia, conecta e acompanha os agentes de IA. | [visão](visao.md) |
| Motor | A parte invisível do Mestre, sempre ligada, que faz o trabalho; fechar o navegador não a interrompe. | [visão geral](../arquitetura/visao-geral.md) |
| Core | O servidor principal (Linux ou Windows): guarda o estado e serve o painel; o nó local dele roda o Grão-Mestre. | [componentes](../arquitetura/componentes.md) |
| Nó local | O nó que existe dentro da máquina do Core e roda os agentes dela, como qualquer outro nó. | [componentes](../arquitetura/componentes.md) |
| Nó | Uma máquina (Linux ou Windows) que roda agentes para o Core. | [componentes](../arquitetura/componentes.md) |
| Guardião | Pequeno serviço em cada máquina que vigia o motor dela e o conserta quando ele falha. | [Guardião](../autocura/guardiao.md) |
| Página reserva | Página mínima servida pelo Guardião para ver o status e aprovar curas quando o Core está fora do ar. | [página reserva](../autocura/pagina-reserva.md) |
| Modo máquina única | O Mestre numa máquina só, sem nó remoto: se ela cair inteira, ninguém de fora percebe. | [camadas](../autocura/camadas.md#modo-máquina-única) |
| Nó condutor | O nó escolhido para conduzir a recuperação do Core e cuidar do Telegram enquanto o Core está fora. | [camadas](../autocura/camadas.md#eleição-do-nó-condutor) |
| Workspace | Um repositório do GitHub cuidado por um Grão-Mestre. | [workspaces](../orquestracao/workspaces.md) |
| Grão-Mestre | O agente chefe do workspace: o único que conversa com os usuários e cuida do GitHub. | [Grão-Mestre](../orquestracao/grao-mestre.md) |
| Mesa | Um pedido em andamento: uma branch própria ligada a um épico, com seu Mestre e seu canvas. | [mesas](../orquestracao/mesas.md) |
| Épico | A issue do GitHub que representa uma mesa. | [ADR 0015](../decisoes/0015-epico-como-rotulo.md) |
| Espelho | Cópia do repositório guardada no Core, de onde saem as branches das mesas. | [mesas](../orquestracao/mesas.md) |
| Mestre da mesa | O agente que coordena o time de uma mesa e presta contas ao Grão-Mestre. | [Mestre da mesa](../orquestracao/mestre-da-mesa.md) |
| Personagem | Um agente de IA (ou terminal) trabalhando numa mesa, mostrado no canvas. | [personagens](../orquestracao/personagens.md) |
| Terminal simples | Um terminal sem agente (um shell), criado a partir de uma base. | [personagens](../orquestracao/personagens.md) |
| Fixado | Personagem que um usuário prendeu: nunca é encerrado automaticamente. | [personagens](../orquestracao/personagens.md#ciclo-de-vida-definido-na-ficha) |
| Efêmero | Personagem encerrado sozinho quando termina a tarefa ou a missão que o criou. | [personagens](../orquestracao/personagens.md#ciclo-de-vida-definido-na-ficha) |
| Persistente | Personagem que continua vivo entre tarefas até ficar ocioso tempo demais. | [personagens](../orquestracao/personagens.md#ciclo-de-vida-definido-na-ficha) |
| Renascido | Personagem que não pôde retomar a sessão e recomeçou com um resumo do trabalho. | [atualização dos CLIs](../provedores/atualizacao-dos-clis.md) |
| Ponto seguro | Momento em que um personagem pode ser reiniciado sem perder trabalho: livre e com a tarefa entregue. | [autoatualização](../plataforma/autoatualizacao.md#ponto-seguro-dos-terminais) |
| Party | O time de personagens conectados desta mesa. | [conexões](../orquestracao/conexoes.md) |
| Base | A receita de como um terminal é iniciado: comando, pasta e em quais máquinas pode rodar. | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Ficha | Um tipo de personagem pronto: usa uma base e define agente, classe, conta e limites. | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Perfil de conta | Qual assinatura do provedor (Claude, Codex, Antigravity) um personagem usa naquele nó. | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Operador do PC | Agente que usa o seu PC Windows com a sua conta, só com aprovação do Dono, por pouco tempo e com aviso na tela. | [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) |
| Classe | O papel de um personagem (ex.: Dev, QA): instruções, permissões, hooks e skills próprios. | [classes](../classes/classes.md) |
| Teto do workspace | O máximo de permissões e de custo que qualquer classe deste workspace pode ter. | [teto do workspace](../classes/teto-do-workspace.md) |
| Pergaminho | Nota em Markdown no canvas que os agentes conectados leem e escrevem. | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Tomo | Uma pilha de pergaminhos agrupados. | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Grimório | Um time salvo do canvas, com fluxo e notas, para restaurar em outra mesa. | [grimórios](../orquestracao/grimorios.md) |
| Ritual | Rotina agendada que envia um comando ou pedido a um agente em certo horário. | [rituais](../orquestracao/rituais.md) |
| Missão | Sequência de etapas em que um agente passa o trabalho ao próximo (A → B → C). | [missões](../orquestracao/missoes.md) |
| Etapa | Um passo de uma missão, feito por um personagem. | [missões](../orquestracao/missoes.md) |
| Caixa de entrada | A fila de mensagens de um personagem, entregue só quando ele fica livre. | [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) |
| Espera | Quando um personagem aguarda algo (outro agente, uma aprovação ou um horário). | [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) |
| Aguardando cota | O personagem parou porque a assinatura dele atingiu o limite de uso do provedor. | [limites de uso](../plataforma/limites-de-uso.md) |
| Monitoramento reduzido | O provedor deste personagem avisa menos eventos ao Mestre; o motor compensa por outros meios. | [adaptador](../provedores/adaptador.md) |
| Sessão zero | A preparação automática feita quando nasce um workspace, uma mesa ou um personagem. | [sessão zero do workspace](../sessao-zero/workspace.md) |
| Decreto | Ordem de um usuário autorizado que vale acima das regras: exceção pontual ou regra permanente. | [decretos](../telegram/decretos.md) |
| Exceção pontual | Decreto que libera uma ação exata uma única vez; toda aprovação é uma. | [decretos](../telegram/decretos.md#tipos) |
| Regra permanente | Decreto que vale daqui pra frente, até ser revogado ou expirar. | [decretos](../telegram/decretos.md#tipos) |
| Aprovação | Pedido para liberar uma ação; o Grão-Mestre decide ou pergunta aos usuários. | [fluxo de aprovação](../aprovacoes/fluxo.md) |
| Nível | O que um usuário pode fazer no Mestre: Dono, Administrador, Aprovador ou Observador. | [usuários e níveis](../telegram/usuarios-e-niveis.md) |
| Cofre | Lugar criptografado do Core onde ficam os segredos; os agentes nunca o leem. | [cofre](../plataforma/cofre.md) |
| Em criação | O GitHub App desta classe ainda não foi criado e instalado; as ações saem com o App "Mestre". | [criação de Apps](../github/criacao-de-apps.md#estados-de-um-app) |
