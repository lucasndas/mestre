# Classes sugeridas

> **Assunto:** o pacote de classes e receitas que vem com o Mestre desligado: o que traz, como um Administrador ativa e atualiza, as duas fichas de cada classe, a etapa opcional das receitas, a calibração pela proposta de classe e o aviso de comando inexistente numa skill · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Deixar quem usa o Mestre começar com um time de desenvolvimento pronto sem mudar a regra de que o Mestre não traz classes prontas ([classes](classes.md)): as sugeridas vêm **desligadas**, e só passam a existir no workspace quando um Administrador as ativa. Ativadas, viram classes do workspace como qualquer outra, com as mesmas validações, o mesmo teto e o mesmo App por classe.

## Comportamento

### O que o pacote traz

Versionado com o código do Mestre, em `<dados da Cidadela>/sugeridas/<versão>/` (só leitura para os agentes, como as outras pastas de dados, [regras fixas](permissoes.md#regras-fixas-de-segurança)):

- **Sete classes**, uma por papel, com `CLASS.md`, `permissions.yaml` e `done_requires`: [Pesquisador, Investigador, Designer, Dev, Revisor, QA e Documentador](sugeridas/INDEX.md). Uma classe por papel: a tecnologia vem do repositório; variações (`qa-api`, `dev-react`) o usuário cria a partir delas.
- **Receitas de missão** por tipo de pedido (bug, funcionalidade nova, ajuste, investigação) e o grimório "Refino de UI" ([receitas](sugeridas/receitas.md)), restauradas como [grimórios](../orquestracao/grimorios.md).
- **Um modelo de direção do workspace** para as [instruções extras do Grão-Mestre](../orquestracao/grao-mestre.md#instruções-extras-do-usuário): prioridades, o que está fora do escopo, quem decide o quê. É texto que o Administrador copia e edita; não é um arquivo novo.

O PM e o PO não são classes: são o [Grão-Mestre](../orquestracao/grao-mestre.md) e o [Mestre da mesa](../orquestracao/mestre-da-mesa.md#o-pedido-que-chega-o-mestre-da-mesa-como-po).

### Ativar

- Pelo painel, na tela de classes ([administração](../painel/administracao.md)), Administrador ou acima escolhe quais classes e receitas ativar. Para cada classe:
  1. o motor copia o pacote da versão atual para `.mestre/classes/<classe>/` como **classe do workspace**, com a validação de sempre ([`class check`](classes.md#validação-mestre-class-check)) e a conferência contra o [teto](teto-do-workspace.md): o que passa do teto recusa aquela classe, listando os itens, e as outras seguem;
  2. a ativação feita por Administrador ou acima **é** a aprovação do escopo do workspace ([escopos](classes.md#escopos)); não nasce proposta;
  3. a classe nasce com `app: novo`: funciona na hora com o App "Mestre" e o Grão-Mestre prepara o App dela ([criação de Apps](../github/criacao-de-apps.md)). Ativar as sete de uma vez conta sete Apps no limite da conta ([contagem de Apps](../painel/administracao.md#contagem-de-apps)), e a tela mostra isso antes de confirmar;
  4. o motor cria **duas fichas** da classe, `<Classe> leve` e `<Classe> pesada`, com a base padrão do workspace e o modelo que o Administrador escolhe para cada uma entre os permitidos do provedor ([bases e fichas](../orquestracao/bases-e-fichas.md)). As receitas usam a leve; o Mestre da mesa troca pela pesada nas etapas difíceis (planejamento de mudança grande, revisão de área sensível).
- Nome já usado por uma classe do workspace: aquela não é ativada, e a tela pede outro nome ou para pular.
- **Desativar** é apagar a classe do workspace, com as regras do `class delete` ([CLI de classes](../cli/coordenacao-classes-e-rituais.md)): pedido explícito e nunca com personagem vivo usando.

### Atualizar

- Uma versão nova do Mestre pode trazer versões novas das sugeridas. As classes já ativadas **não** são sobrescritas: para cada uma que mudou, nasce uma [proposta de classe](classes.md#onde-a-classe-é-alterada) com autor "Mestre", que o Administrador aprova ou recusa vendo a diferença.
- Se o usuário editou a classe depois de ativá-la, a diferença mostrada é contra a versão editada, e aprovar substitui as edições: a tela avisa.

### Receitas: etapa opcional

- Uma etapa de receita pode ter `optional: true`. Na definição de uma missão ([missões](../orquestracao/missoes.md#definição)), o campo `skip: [<id>…]` tira essas etapas; um `after` que apontava para uma etapa tirada é ignorado.
- Só etapa opcional pode ser tirada, e a etapa para onde alguma outra devolve (`can_return_to`) não pode ser opcional: os dois casos saem com 2 `invalid_quest` ([coordenação](../cli/coordenacao.md)).
- O Mestre da mesa decide o que tirar ao montar a missão, pelo tipo e pelo tamanho do pedido: cada passagem de bastão custa cota e contexto.

### Calibração

- A receita pode terminar com uma etapa opcional `retro`, feita pelo Revisor, que lê o pergaminho da missão e o de revisão ([prova e devolução](../orquestracao/prova-e-devolucao.md)) e escreve, no próprio `done`, o que a classe deveria dizer diferente (ex.: "o Dev deu `done` sem rodar a suíte duas vezes").
- Com a missão concluída, o Mestre da mesa leva a sugestão ao Grão-Mestre, que, se concordar, edita a classe do workspace (`mestre class edit`); como toda edição de classe do workspace feita por ele, isso vira **proposta** com o hash que o Administrador vê e aprova ([classes](classes.md#onde-a-classe-é-alterada)). Nada muda numa classe sem essa aprovação.

### Comando inexistente numa skill

O `class check` de qualquer classe, sugerida ou não, avisa quando o `CLASS.md` ou uma skill do pacote cita `mestre <comando>` que a CLI não tem, comparando com a definição de comandos que já está no binário ([skills geradas](../cli/skills-geradas.md)). É aviso, não erro: a classe é salva.

## Regras e erros

- Só Administrador ou acima ativa, desativa e atualiza; o servidor confere o nível em cada rota ([administração](../painel/administracao.md)).
- Classe sugerida ativada é classe do workspace: o agente nunca escreve nela ([regras fixas](permissoes.md#regras-fixas-de-segurança)); muda só pelos caminhos de [classes](classes.md#onde-a-classe-é-alterada).
- Versão do pacote que não valida contra o esquema da versão do Mestre: a tela de classes mostra o erro e não oferece ativar; é bug do Mestre.
- O pacote nunca traz segredo nem `network.credentials`; credenciais o Administrador libera depois, na classe ativada.

## Critérios de aceite

- **CA-SUG-01** · *rápida* — **Dado** um Administrador e o teto padrão, **quando** ele ativa Dev e QA, **então** existem as classes do workspace `dev` e `qa` com o pacote da versão atual, sem proposta pendente, cada uma com `app: novo` e as fichas "Dev leve", "Dev pesada", "QA leve" e "QA pesada"; **dado** um Aprovador, **quando** chama a rota de ativar, **então** o servidor recusa com o nível exigido.
- **CA-SUG-02** · *rápida* — **Dado** um teto com `files.write: ["docs/**"]`, **quando** o Administrador ativa Dev e Documentador, **então** Dev é recusada listando `files.write` e Documentador é ativada.
- **CA-SUG-03** · *rápida* — **Dado** `dev` ativada na versão 1 e editada pelo usuário, **quando** o Mestre passa a trazer a versão 2, **então** nasce uma proposta de classe com autor "Mestre" e a diferença contra a versão editada, e `dev` continua igual até a aprovação.
- **CA-SUG-04** · *rápida* — **Dado** a receita de funcionalidade nova com `pesquisa` e `design` opcionais, **quando** o Mestre da mesa cria a missão com `skip: [pesquisa, design]`, **então** `dev` começa sem esperar nada; **dado** `skip: [dev]`, ou uma receita cuja etapa `dev` é opcional e alvo de `can_return_to`, **então** sai com 2 `invalid_quest`.
- **CA-SUG-05** · *rápida* — **Dado** uma classe cuja skill cita `mestre deploy`, que a CLI não tem, **quando** o `class check` roda, **então** mostra o aviso com o arquivo e a linha, e a classe é salva.
- **CA-SUG-06** · *rápida* — **Dado** uma missão concluída com a etapa `retro` e o Grão-Mestre que roda `mestre class edit "dev" --from <pasta>`, **quando** o motor recebe, **então** nasce uma proposta com o hash e a classe `dev` não muda até um Administrador aprovar.
- **CA-SUG-07** · *pesada* — **Dado** as sete classes e as receitas ativadas num workspace real, **quando** chega um pedido de bug pelo Telegram (dublê), **então** a missão passa por Investigador, Dev, Revisor e QA (Revisor e QA em paralelo) nos três provedores, cada critério fica com prova e a mesa termina com o PR mesclado (MVP-16).

## Depende de

- [Classes](classes.md)
- [Teto do workspace](teto-do-workspace.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)
- [Grimórios](../orquestracao/grimorios.md)
- [Missões](../orquestracao/missoes.md)
- [Mestre da mesa](../orquestracao/mestre-da-mesa.md)
- [Prova e devolução](../orquestracao/prova-e-devolucao.md)
- [Criação de Apps](../github/criacao-de-apps.md)
- [Administração](../painel/administracao.md)
- [Teste de classe](../provedores/teste-de-classe.md)

## A confirmar

- **Vigia** (o coletor de problemas do estudo): um ritual sugerido que olha as falhas da verificação e as issues abertas e propõe issues "candidatas", sem duplicar. Fica de fora até a CLI ter uma forma de procurar uma issue pela impressão antes de criar (hoje `mestre issue create` não tem), para o "visto de novo" não depender do agente lembrar.
