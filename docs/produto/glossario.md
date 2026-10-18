# Glossário

> **Assunto:** vocabulário do Mestre e textos de ajuda da interface e do Telegram · **Componente:** Projeto · **Atualizado:** 2026-10-18

Este glossário é a **fonte única** dos textos de ajuda: o "?" dos termos no painel ([canvas](../painel/canvas.md)) e a explicação entre parênteses na primeira vez que um termo aparece numa conversa do Telegram ([bot](../telegram/bot.md)). O `go generate ./internal/glossary` lê a tabela abaixo e gera os textos que o Mestre embute; a suíte rápida falha se o arquivo gerado estiver desatualizado ([código gerado](../desenvolvimento/convencoes.md#código-gerado)).

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
| Guilda | O grupo de todas as máquinas com o Mestre instalado, que trabalham juntas. | [Guilda](../guilda/guilda.md) |
| Cidadela | A máquina no comando da Guilda: guarda o estado, serve o painel e roda o Grão-Mestre. Só há uma por vez. | [Cidadela e Torres](../guilda/cidadela-e-torres.md) |
| Torre | Cada outra máquina da Guilda: roda personagens, guarda uma cópia do estado e vigia a Cidadela; pode assumir o comando. | [Cidadela e Torres](../guilda/cidadela-e-torres.md) |
| Ficha da máquina | O que uma máquina tem: sistema, tela, provedores logados, navegador, CPU, memória e GPU. | [ficha da máquina](../guilda/ficha-da-maquina.md) |
| Época | O número que diz qual Cidadela vale; sobe a cada troca, e ordens de época antiga são ignoradas. | [Cidadela e Torres](../guilda/cidadela-e-torres.md#época-e-fencing) |
| Cidadela suspensa | Cidadela que viu uma época mais nova: não age para fora e manda o painel para a Cidadela atual. | [Cidadela e Torres](../guilda/cidadela-e-torres.md#cidadela-suspensa) |
| Máquina excluída | Máquina que o Dono deixou sem segredos: não guarda o cofre e nunca vira Cidadela. | [Guilda](../guilda/guilda.md#máquina-excluída) |
| Guardião | Pequeno serviço em cada máquina que vigia o motor dela e o conserta quando ele falha. | [Guardião](../autocura/guardiao.md) |
| Página reserva | Página mínima servida pelo Guardião para ver o status e aprovar curas quando a Cidadela está fora do ar. | [página reserva](../autocura/pagina-reserva.md) |
| Modo máquina única | A Guilda com uma máquina só: se ela cair inteira, ninguém de fora percebe. | [camadas](../autocura/camadas.md#modo-máquina-única) |
| Torre condutora | A Torre que conduz a recuperação da Cidadela, cuida do Telegram enquanto ela está fora e propõe quem assume. | [camadas](../autocura/camadas.md#eleição-da-torre-condutora) |
| Modo reduzido | Telegram com a Cidadela fora do ar: o Guardião assume o bot e só atende /status, aprovações de cura e a promoção. | [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) |
| Workspace | Um repositório do GitHub cuidado por um Grão-Mestre. | [workspaces](../orquestracao/workspaces.md) |
| Grão-Mestre | O agente chefe do workspace: o único que conversa com os usuários e cuida do GitHub. | [Grão-Mestre](../orquestracao/grao-mestre.md) |
| Mesa | Um pedido em andamento: uma branch própria ligada a um épico, com seu Mestre e seu canvas. | [mesas](../orquestracao/mesas.md) |
| Épico | A issue do GitHub que representa uma mesa. | [ADR 0015](../decisoes/0015-epico-como-rotulo.md) |
| Espelho | Cópia do repositório em cada máquina (a da Cidadela é a principal), de onde saem as branches das mesas. | [mesas](../orquestracao/mesas.md) |
| Mestre da mesa | O agente que coordena o time de uma mesa e presta contas ao Grão-Mestre. | [Mestre da mesa](../orquestracao/mestre-da-mesa.md) |
| Triagem | O Grão-Mestre classifica o pedido, escreve os critérios e faz até 3 perguntas antes de repassá-lo ou abrir uma mesa. | [Grão-Mestre](../orquestracao/grao-mestre.md#triagem-e-roteamento-do-pedido) |
| Aceite | O Mestre da mesa confere cada critério contra a evidência antes de abrir o PR; sem evidência, a missão volta ao time. | [Mestre da mesa](../orquestracao/mestre-da-mesa.md#o-pedido-que-chega-o-mestre-da-mesa-como-po) |
| Mensagem de progresso | Resumo do workspace feito pelo motor: conta o que não precisa de ninguém e traz uma linha com dono para cada pendência. | [Grão-Mestre](../orquestracao/grao-mestre.md#mensagem-de-progresso) |
| Personagem | Um agente de IA (ou terminal) trabalhando numa mesa, mostrado no canvas. | [personagens](../orquestracao/personagens.md) |
| Terminal simples | Um terminal sem agente (um shell), criado a partir de uma base. | [personagens](../orquestracao/personagens.md) |
| Fixado | Personagem que um usuário prendeu: nunca é encerrado automaticamente. | [personagens](../orquestracao/personagens.md#ciclo-de-vida-definido-na-ficha) |
| Efêmero | Personagem encerrado sozinho quando termina a tarefa ou a missão que o criou. | [personagens](../orquestracao/personagens.md#ciclo-de-vida-definido-na-ficha) |
| Persistente | Personagem que continua vivo entre tarefas até ficar ocioso tempo demais. | [personagens](../orquestracao/personagens.md#ciclo-de-vida-definido-na-ficha) |
| Renascido | Personagem que não pôde retomar a sessão e recomeçou com um resumo do trabalho. | [adaptador](../provedores/adaptador.md#retomada-de-sessão) |
| Terminal | Onde cada agente roda na máquina; continua vivo quando o motor reinicia ou é atualizado. | [terminal](../arquitetura/terminal.md) |
| Ponto seguro | Momento em que um personagem pode ser encerrado sem perder trabalho: livre e com a tarefa entregue. | [personagens](../orquestracao/personagens.md#encerramento-seguro) |
| Party | O time de personagens conectados desta mesa. | [conexões](../orquestracao/conexoes.md) |
| Base | A configuração de como um terminal é iniciado: comando, pasta e em quais máquinas pode rodar. | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Ficha | Um tipo de personagem pronto: usa uma base e define agente, classe, conta e limites. | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Perfil de conta | Qual assinatura do provedor (Claude, Codex, Antigravity) um personagem usa naquela máquina. | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Operador do PC | Agente que usa o seu PC Windows com a sua conta, só com aprovação do Dono, por pouco tempo e com aviso na tela. | [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) |
| Classe | O papel de um personagem (ex.: Dev, QA): instruções, permissões, hooks e skills próprios. | [classes](../classes/classes.md) |
| Classe sugerida | Classe de um time de desenvolvimento que vem com o Mestre desligada; um Administrador ativa a que quiser. | [classes sugeridas](../classes/classes-sugeridas.md) |
| Ficha leve | Das duas fichas de cada classe sugerida, a de modelo mais barato; é a que as receitas usam. | [classes sugeridas](../classes/classes-sugeridas.md#ativar) |
| Ficha pesada | Das duas fichas de cada classe sugerida, a de modelo mais forte; o Mestre da mesa a usa nas etapas difíceis. | [classes sugeridas](../classes/classes-sugeridas.md#ativar) |
| Skill de apoio | Skill de engenharia que uma classe traz com liga/desliga (TDD, revisão de código); não dá permissão nenhuma. | [skills de apoio](../classes/skills-de-apoio.md) |
| Gateway do Mestre | O único servidor MCP que o agente conhece: mostra só as ferramentas que a classe liga, e o motor julga cada chamada. | [MCP por classe](../classes/mcp-por-classe.md#o-gateway-do-mestre) |
| Catálogo MCP | Lista dos servidores MCP (como navegador e documentação) que o workspace aceita; só Administrador ou acima acrescenta. | [MCP por classe](../classes/mcp-por-classe.md#o-catálogo-do-workspace) |
| Servidor de controle | Servidor MCP que controla o computador ou o navegador do usuário; só o Dono o cadastra e só o Operador do PC o usa. | [MCP por classe](../classes/mcp-por-classe.md#o-catálogo-do-workspace) |
| Teste de classe | Roda os casos da classe com o provedor real para ver se ela faz o que diz; gasta cota (botões Testar e Testar de novo). | [teste de classe](../provedores/teste-de-classe.md) |
| Teto do workspace | O máximo de permissões e de custo que qualquer classe deste workspace pode ter. | [teto do workspace](../classes/teto-do-workspace.md) |
| Pergaminho | Nota em Markdown no canvas que os agentes conectados leem e escrevem. | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Tomo | Uma pilha de pergaminhos agrupados. | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Grimório | Um time salvo do canvas, com fluxo e notas, para restaurar em outra mesa. | [grimórios](../orquestracao/grimorios.md) |
| Receita | Missão pronta por tipo de pedido (bug, funcionalidade, ajuste, investigação) ou refino de UI; usada com `--recipe`. | [classes sugeridas](../classes/classes-sugeridas.md#receitas-e-etapa-opcional) |
| Calibração | Ajuste de uma classe pelo que uma missão mostrou: o Revisor sugere (etapa retro) e um Administrador aprova a proposta. | [classes sugeridas](../classes/classes-sugeridas.md#calibração) |
| Ritual | Rotina agendada que envia um comando ou pedido a um agente em certo horário. | [rituais](../orquestracao/rituais.md) |
| Vigia | Ritual diário das classes sugeridas, desligado de início: procura problemas e os registra como issues candidatas. | [classes sugeridas](../classes/classes-sugeridas.md#vigia) |
| Missão | Sequência de etapas em que um agente passa o trabalho ao próximo (A → B → C). | [missões](../orquestracao/missoes.md) |
| Etapa | Um passo de uma missão, feito por um personagem. | [missões](../orquestracao/missoes.md) |
| Prova | Evidência de que um critério da missão foi cumprido: declarada pelo agente ou verificada pelo motor. | [prova e devolução](../orquestracao/prova-e-devolucao.md#prova-por-critério) |
| Devolução | Uma etapa devolve o trabalho a uma anterior, com o motivo; as voltas têm limite e a mesma falha repetida pausa a missão. | [prova e devolução](../orquestracao/prova-e-devolucao.md#devolução-com-causa-e-mesma-falha) |
| Sincronia com a base | Etapa sync: o Dev integra a branch principal na da mesa, resolve os conflitos e só termina com a verificação verde. | [sincronia com a base](../orquestracao/sincronia-com-a-base.md) |
| Caixa de entrada | A fila de mensagens de um personagem, entregue só quando ele fica livre. | [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) |
| Espera | Quando um personagem ou uma mesa aguarda algo: outro agente, uma aprovação, um horário ou outra mesa. | [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) |
| Aguardando cota | Parado até a cota voltar: o personagem bateu no limite da assinatura, ou a mesa não começa etapa nova perto do limite. | [limites de uso](../plataforma/limites-de-uso.md) |
| Limiar de início | Uso da janela do plano (padrão 95%) a partir do qual o Mestre não inicia etapa nova naquele provedor e conta. | [limites de uso](../plataforma/limites-de-uso.md#limiar-de-início) |
| Monitoramento reduzido | O provedor deste personagem avisa menos eventos ao Mestre; o motor compensa por outros meios. | [adaptador](../provedores/adaptador.md) |
| Sessão zero | A preparação automática feita quando nasce um workspace, uma mesa ou um personagem. | [sessão zero do workspace](../sessao-zero/workspace.md) |
| Decreto | Ordem de um usuário autorizado que vale acima das regras: exceção pontual ou regra permanente. | [decretos](../telegram/decretos.md) |
| Exceção pontual | Decreto que libera uma ação exata uma única vez; toda aprovação é uma. | [decretos](../telegram/decretos.md#tipos) |
| Regra permanente | Decreto que vale daqui pra frente, até ser revogado ou expirar. | [decretos](../telegram/decretos.md#tipos) |
| Aprovação | Pedido para liberar uma ação; sobe do superior de quem pediu até o Grão-Mestre e as pessoas. | [fluxo de aprovação](../aprovacoes/fluxo.md) |
| Pedido que bloqueia | Mensagem que pede algo ao usuário porque a mesa não segue sem ele: traz o motivo, a evidência, o que fazer e quem age. | [pedido que bloqueia](../aprovacoes/pedido-que-bloqueia.md) |
| Cadeia de aprovação | O caminho de um pedido: do superior de quem pediu ao Grão-Mestre e às pessoas, cada um decidindo até um risco. | [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md) |
| Superior | Quem criou o personagem; é o primeiro a decidir os pedidos de aprovação dele. | [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md#superior) |
| Risco | Quanto um pedido pode estragar: baixo, médio, alto ou fixo. O sistema decide e isso diz quem pode aprovar. | [classificação de risco](../classes/classificacao-de-risco.md) |
| Regra temporária de mesa | Liberação curta, numa mesa, dada por um agente ao aprovar; nunca vence uma regra de pessoa. | [regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md) |
| Pausa de autonomia | Freio por mesa: o Mestre da mesa deixa de decidir o risco médio, que vai ao Grão-Mestre; o baixo continua com ele. | [supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md#pausa-de-autonomia) |
| Disjuntor | Pausa o personagem após 3 negações seguidas (padrão) dos pedidos dele e avisa as pessoas; Administrador ou acima retoma. | [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md#salvaguardas) |
| Entrega útil | Trabalho que pousou: commit novo na branch da mesa, PR aberto ou atualizado, ou etapa de missão concluída. | [supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira) |
| Mesa que gira | Mesa trabalhando há mais de 3 h (padrão) sem entrega útil, sem pausa nem espera: avisa o Grão-Mestre e quem aprova. | [supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira) |
| Delegação | Autorização de uma pessoa para o Grão-Mestre decidir certos pedidos acima do que ele pode, por até 30 dias. | [delegação](../aprovacoes/delegacao.md) |
| Nível | O que um usuário pode fazer no Mestre: Dono, Administrador, Aprovador ou Observador. | [usuários e níveis](../telegram/usuarios-e-niveis.md) |
| Cofre | Lugar criptografado onde ficam os segredos; só a Cidadela o abre, e os agentes nunca o leem. | [cofre](../plataforma/cofre.md) |
| Impressão | Código curto (hash) de um pedido, falha, problema, segredo ou da CA, usado para ver se é o mesmo ou se mudou. | [fluxo de aprovação](../aprovacoes/fluxo.md) |
| Em criação | O GitHub App desta classe ainda não foi criado e instalado; as ações saem com o App "Mestre". | [criação de Apps](../github/criacao-de-apps.md#estados-de-um-app) |
