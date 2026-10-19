# Classes sugeridas

> **Assunto:** o pacote de classes e receitas que vem com o Mestre desligado: o que traz, como um Administrador ativa e atualiza, as duas fichas de cada classe, a etapa opcional das receitas, a calibração pela proposta de classe, o ritual Vigia e o aviso de comando inexistente numa skill · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Deixar quem usa o Mestre começar com um time de desenvolvimento pronto sem mudar a regra de que o Mestre não traz classes prontas ([classes](classes.md)): as sugeridas vêm **desligadas**, e só passam a existir no workspace quando um Administrador as ativa. Ativadas, viram classes do workspace como qualquer outra, com as mesmas validações, o mesmo teto e o mesmo App por classe.

## Comportamento

### O que o pacote traz

Versionado com o código do Mestre: embutido no binário e gravado em `<dados da Cidadela>/sugeridas/<versão>/` na instalação e em cada atualização, como os modelos de instruções ([persistência](../arquitetura/persistencia.md#arquivos)); é pasta de dados do Mestre, que nenhum agente lê nem escreve ([regras fixas](permissoes.md#regras-fixas-de-segurança)):

- **Sete classes**, uma por papel, com `CLASS.md`, `permissions.yaml` e `done_requires`: [Pesquisador, Investigador, Designer, Dev, Revisor, QA e Documentador](sugeridas/INDEX.md). Uma classe por papel: a tecnologia vem do repositório; variações (`qa-api`, `dev-react`) o usuário cria a partir delas.
- **Receitas de missão** por tipo de pedido (bug, funcionalidade nova, ajuste, investigação) e para refinar uma interface ([receitas](sugeridas/receitas.md)): definições de missão prontas que o Mestre da mesa usa com `--recipe`. Não são [grimórios](../orquestracao/grimorios.md), que nascem de uma seleção do canvas.
- **O ritual [Vigia](#vigia)**, que procura problemas e os registra como issues candidatas.
- **Um modelo de direção do workspace** para as [instruções extras do Grão-Mestre](../orquestracao/grao-mestre.md#instruções-extras-do-usuário): prioridades, o que está fora do escopo, quem decide o quê. É o arquivo `direcao.md` do pacote, que a tela de ativação mostra para o Administrador copiar e editar; o motor não o grava no workspace.

**Skills de apoio e servidores MCP de cada papel** ([skills de apoio](skills-de-apoio.md), [MCP por classe](mcp-por-classe.md)), todos **desligados** na ativação; o Administrador liga os que quiser na mesma tela:

| Classe | Skills de apoio | MCP |
|---|---|---|
| Pesquisador | n/a | `context7` |
| Investigador | `diagnosing-bugs`, `tdd` | n/a |
| Designer | `critique`, `audit`, `polish` | `playwright` |
| Dev | `implement`, `tdd`, `codebase-design`, `resolving-merge-conflicts`, `ponytail` | `context7` |
| Revisor | `code-review`, `codebase-design`, `ponytail-review` | n/a |
| QA | `tdd`, `testing-strategy` | `playwright` |
| Documentador | n/a | n/a |

Origem e licença, conferidas em 2026-10-18 e guardadas na pasta de cada skill, com o commit de origem e os hashes no `SOURCE`: `mattpocock/skills` (MIT: `tdd`, `diagnosing-bugs`, `implement`, `codebase-design`, `resolving-merge-conflicts`, `code-review`), `pbakaus/impeccable` (Apache-2.0, com o `NOTICE`: `critique`, `audit`, `polish`), `DietrichGebert/ponytail` (MIT: `ponytail`, `ponytail-review`); `testing-strategy` é escrita pelo Mestre. Os servidores `playwright` e `context7` (`upstash/context7`, MIT) entram no catálogo do workspace só quando o Administrador liga um deles.

O PM e o PO não são classes: são o [Grão-Mestre](../orquestracao/grao-mestre.md) e o [Mestre da mesa](../orquestracao/mestre-da-mesa.md#o-pedido-que-chega-o-mestre-da-mesa-como-po).

### Ativar

- Pelo painel, na tela de classes ([administração](../painel/administracao.md)), Administrador ou acima escolhe quais classes e receitas ativar. Para cada classe:
  1. o motor copia o pacote da versão atual para `.mestre/classes/<classe>/` como **classe do workspace**, com a validação de sempre ([`class check`](classes.md#validação-mestre-class-check)) e a conferência contra o [teto](teto-do-workspace.md): o que passa do teto recusa aquela classe, listando os itens, e as outras seguem;
  2. a ativação feita por Administrador ou acima **é** a aprovação do escopo do workspace ([escopos](classes.md#escopos)); não nasce proposta;
  3. a classe nasce com `app: novo`: funciona na hora com o App "Mestre" e o Grão-Mestre prepara o App dela ([criação de Apps](../github/criacao-de-apps.md)). Ativar as sete de uma vez conta sete Apps no limite da conta ([contagem de Apps](../painel/administracao.md#contagem-de-apps)), e a tela mostra isso antes de confirmar;
  4. o motor cria **duas fichas** da classe, `<Classe> leve` e `<Classe> pesada`, com a `base`, o `agent` e o `account` que o Administrador escolhe para cada uma ([bases e fichas](../orquestracao/bases-e-fichas.md)). As fichas são globais e de nome único: se já existe uma com o mesmo nome e a mesma classe (ativada antes em outro workspace), o motor a **reaproveita** e a tela diz isso; se o nome existe com outra classe, a classe sugerida é **recusada** e a tela pede outros nomes ou para pular; o modelo é o das opções da base escolhida (uma base com o modelo mais barato e outra com o mais forte, dentro das opções que cada provedor permite, como em [Claude Code](../provedores/claude-code.md)). As receitas usam a leve; o Mestre da mesa troca pela pesada nas etapas difíceis (planejamento de mudança grande, revisão de área sensível).
  5. `npm test`, `go test`, `make test` e `python -m pytest` já são `baixo` pela [lista de seguros](lista-de-seguros.md) e rodam sem pedido; só os comandos de teste **fora da lista** (como `rake test`, `bundle exec rspec`, `docker compose run`) ou de um script que a própria mesa alterou pedem aprovação, e podem entrar em `commands.allow` da classe na tela de ativação, para Dev, QA, Revisor e Investigador não pedirem a cada rodada.
  6. os `files.write` do Investigador e da QA cobrem os nomes de teste mais comuns (`*_test.*`, `*.test.*`, `*.spec.*`, `test_*.py`, `tests/`, `__tests__/`); outra convenção (`*Test.java`, `src/test/**`) o Administrador edita na classe ativada, com a aprovação e o teto de sempre.
- Nome já usado por uma classe do workspace: aquela não é ativada, e a tela pede outro nome ou para pular.
- **Desativar** é apagar a classe do workspace, com as regras do `class delete` ([CLI de classes](../cli/coordenacao-classes-e-rituais.md)): pedido explícito e nunca com personagem vivo usando. As fichas ficam (são globais e podem servir a outro workspace); sem a classe no workspace, recrutar por elas é recusado com a explicação.

### Atualizar

- Uma versão nova do Mestre pode trazer versões novas das sugeridas. As classes já ativadas **não** são sobrescritas: para cada uma que mudou, nasce uma [proposta de classe](classes.md#onde-a-classe-é-alterada) com autor "Mestre", que segue as aprovações do escopo do workspace (Administrador ou acima) vendo a diferença. As receitas ativadas são trocadas pela versão nova, porque não são editadas pelo usuário.
- Se o usuário editou a classe depois de ativá-la, a diferença mostrada é contra a versão editada, e aprovar substitui as edições: a tela avisa.

### Receitas e etapa opcional

- O Administrador ativa as receitas junto com as classes; uma receita só fica disponível com todas as fichas que ela cita existindo. Ativadas, ficam no banco da Cidadela, por workspace.
- O Mestre da mesa cria a missão com `mestre quest create "<Missão>" --recipe <nome> [--skip <id>]…` ([coordenação](../cli/coordenacao.md); o nome casa sem diferenciar maiúsculas nem acentos, e `funcionalidade` acha `Funcionalidade`): o motor parte da receita, tira as etapas de `--skip` e então valida o resultado como qualquer [definição de missão](../orquestracao/missoes.md#definição). O Mestre da mesa vê as receitas ativadas com `mestre quest recipes` (nome, tipo, etapas e fichas) e a definição de uma com `mestre quest recipes <nome>`; para ajustar mais (outra ficha, instruções), usa essa definição como modelo e cria com `--file`. O comando `recipes` é a acrescentar na [CLI de coordenação](../cli/coordenacao.md).
- Uma etapa de receita pode ter `optional: true`; só ela pode ir em `--skip`. Tirada a etapa, um `after` que apontava para ela deixa de contar. A etapa para onde alguma outra devolve (`can_return_to`) não pode ser opcional.
- Erros: `--skip` de etapa que não é opcional ou não existe, 2 `invalid_quest`; receita inexistente ou com ficha faltando, 4 `not_found`, com as fichas.
- O Mestre da mesa decide o que tirar pelo tipo e pelo tamanho do pedido: cada passagem de bastão custa cota e contexto.

### Calibração

- A receita pode terminar com uma etapa opcional `retro`, feita pelo Revisor, que lê o pergaminho da missão e o de revisão ([prova e devolução](../orquestracao/prova-e-devolucao.md)) e escreve, no próprio `done`, o que a classe deveria dizer diferente (ex.: "o Dev deu `done` sem rodar a suíte duas vezes").
- Com a missão concluída, o Mestre da mesa leva a sugestão ao Grão-Mestre, que, se concordar, exporta a classe (`mestre class show --export`), muda o pacote em `$MESTRE_TMP` e o envia (`mestre class edit --from`); como toda edição de classe do workspace feita por ele, isso vira **proposta** com o hash que o Administrador vê e aprova ([classes](classes.md#onde-a-classe-é-alterada)). Nada muda numa classe sem essa aprovação.

### Vigia

Ritual do workspace, desligado como as classes: ativá-lo na mesma tela cria o ritual `vigia`, diário (`--daily 07:00`, editável), com o Grão-Mestre como alvo e o prompt que vem no pacote; desativar é `ritual disable` ([rituais](../orquestracao/rituais.md)). O prompt pede que ele:

- junte os candidatos: devoluções e achados repetidos nas issues das missões fechadas desde o disparo anterior, que o espelho da [missão](../orquestracao/missoes.md#espelho-no-github) comenta a cada etapa (`mestre issue list --all --state closed --since <data do disparo anterior>` lista as fechadas e `mestre issue list <n>… --comments` lê as escolhidas; a flag `--since` é a acrescentar na CLI de issues), e as issues abertas sem rótulo (`mestre issue list --all`);
- confira cada um sem rodar código: ainda acontece? um PR mesclado já corrigiu? é duplicado? Incerto não é descarte: vira candidato com a dúvida escrita;
- registre cada um com `mestre issue create --label candidato --fingerprint "<componente>: <sintoma>"` ([CLI de issues](../cli/coordenacao-github.md#mestre-issue)), que comenta "visto de novo" na issue aberta em vez de duplicar;
- nunca abra mesa: promover um candidato a épico é pedido de um usuário ao Grão-Mestre, e reproduzir o problema é a primeira etapa da receita de bug.

Registra no máximo 10 candidatos por disparo; o resto fica para o próximo. Custa uma rodada do Grão-Mestre por disparo, dentro dos [limites de uso](../plataforma/limites-de-uso.md); desligado, não roda.

### Comando inexistente numa skill

O `class check` de qualquer classe, sugerida ou não, avisa quando o `CLASS.md` ou uma skill do pacote cita `mestre <comando>` que a CLI não tem, comparando com a definição de comandos que já está no binário ([skills geradas](../cli/skills-geradas.md)). É aviso, não erro: a classe é salva.

## Regras e erros

- Só Administrador ou acima ativa, desativa e atualiza; o servidor confere o nível em cada rota ([administração](../painel/administracao.md)).
- Classe sugerida ativada é classe do workspace: o agente nunca escreve nela ([regras fixas](permissoes.md#regras-fixas-de-segurança)); muda só pelos caminhos de [classes](classes.md#onde-a-classe-é-alterada).
- Versão do pacote que não valida contra o esquema da versão do Mestre: a tela de classes mostra o erro e não oferece ativar; é bug do Mestre.
- O pacote nunca traz segredo nem `network.credentials`; credenciais o Administrador libera depois, na classe ativada.

## Critérios de aceite

- **CA-SUG-01** · *rápida* — **Dado** um Administrador, o teto padrão e duas bases escolhidas para leve e pesada, **quando** ele ativa Dev e QA, **então** a tela mostra antes que 2 Apps vão nascer, e a tela mostra o texto de `direcao.md` para copiar; depois (mesmo com o catálogo MCP vazio, porque os `mcp` vêm desligados) existem as classes do workspace `dev` e `qa` com o pacote da versão atual, sem proposta pendente, cada uma com `app: novo`, e as fichas "Dev leve", "Dev pesada", "QA leve" e "QA pesada" com as bases escolhidas; **dado** um Aprovador, **quando** chama a rota de ativar, **então** o servidor recusa com o nível exigido.
- **CA-SUG-02** · *rápida* — **Dado** um teto com `files.write: ["docs/**"]`, **quando** o Administrador ativa Dev e Documentador, **então** Documentador é recusada listando o `**/*.md` que passa do teto, e Dev, que não escreve `files.write`, é ativada (o padrão implícito é cortado pelo teto nas regras efetivas, [teto](teto-do-workspace.md)).
- **CA-SUG-03** · *rápida* — **Dado** `dev` ativada na versão 1 e editada pelo usuário, **quando** o Mestre passa a trazer a versão 2, **então** nasce uma proposta de classe com autor "Mestre" e a diferença contra a versão editada, e `dev` continua igual até a aprovação.
- **CA-SUG-04** · *rápida* — **Dado** a receita de funcionalidade nova com `pesquisa` e `design` opcionais, **quando** o Mestre da mesa roda `mestre quest create "Login" --recipe funcionalidade --skip pesquisa --skip design`, **então** a missão nasce sem as duas e `dev` começa sem esperar nada; **dado** `--skip dev`, **então** sai com 2 `invalid_quest`; **dado** a ficha "Pesquisador leve" apagada, **então** `--recipe funcionalidade` sai com 4 `not_found` citando a ficha.
- **CA-SUG-05** · *rápida* — **Dado** uma classe cuja skill cita `mestre deploy`, que a CLI não tem, **quando** o `class check` roda, **então** mostra o aviso com o arquivo e a linha, e a classe é salva.
- **CA-SUG-06** · *rápida* — **Dado** uma missão concluída com a etapa `retro` e o Grão-Mestre que roda `mestre class edit "dev" --from <pasta>`, **quando** o motor recebe, **então** nasce uma proposta com o hash e a classe `dev` não muda até um Administrador aprovar.
- **CA-SUG-07** · *pesada* — **Dado** as sete classes e as receitas ativadas num workspace real, **quando** chega um pedido de bug pelo Telegram (dublê), **então** a missão passa por Investigador, Dev, Revisor e QA (Revisor e QA em paralelo) nos três provedores, cada critério fica com prova e a mesa termina com o PR mesclado (MVP-16).
- **CA-SUG-08** · *rápida* — **Dado** o pacote sugerido embutido no binário, **quando** o teste o lê, **então** cada classe, com todas as suas skills de apoio, cabe em 64 arquivos e 512 KiB, e cada skill de apoio tem o arquivo de licença.
- **CA-SUG-09** · *rápida* — **Dado** o Vigia ativado e o Grão-Mestre do agente falso roteirizado para rodar `mestre issue create --title "Login lento" --label candidato --fingerprint "auth: login lento"` a cada item `ritual`, **quando** o ritual dispara em dois dias seguidos (relógio falso), **então** o item entregue traz o prompt do pacote, o GitHub falso tem uma issue só com o rótulo `candidato`, com um comentário "visto de novo", e nenhuma mesa foi aberta; **dado** o ritual `vigia` desativado, **então** nada dispara.
- **CA-SUG-10** · *rápida* — **Dado** a ficha "Dev leve" já existente com a classe `dev`, **quando** um Administrador ativa Dev em outro workspace, **então** a ficha é reaproveitada e a tela diz isso; **dado** uma ficha "QA leve" existente com outra classe, **então** a ativação de QA é recusada e a tela pede outro nome ou pular; **quando** `dev` é desativada, **então** as fichas ficam e `recruit` por elas sai com a explicação.

## Depende de

- [Classes](classes.md)
- [Teto do workspace](teto-do-workspace.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)
- [Missões](../orquestracao/missoes.md)
- [Mestre da mesa](../orquestracao/mestre-da-mesa.md)
- [Prova e devolução](../orquestracao/prova-e-devolucao.md)
- [Criação de Apps](../github/criacao-de-apps.md)
- [Administração](../painel/administracao.md)
- [Skills de apoio](skills-de-apoio.md)
- [MCP por classe](mcp-por-classe.md)
- [Rituais](../orquestracao/rituais.md)
- [CLI de issues e PRs](../cli/coordenacao-github.md)
- [Grão-Mestre](../orquestracao/grao-mestre.md)
- [CLI de coordenação](../cli/coordenacao.md)
- [CLI de classes e rituais](../cli/coordenacao-classes-e-rituais.md)
- [Lista de programas seguros](lista-de-seguros.md)
