# Mestre da mesa

> **Assunto:** papel do Mestre da mesa, liberdade dentro do teto, proteção, instruções do sistema e instruções extras do usuário · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

O Mestre da mesa é o agente orquestrador de **uma [mesa](mesas.md)**: monta e coordena a party, cria as missões e fala só com o [Grão-Mestre](grao-mestre.md) e com a própria party. O trabalho de execução é delegado aos personagens.

## Comportamento

### Criação e identidade

- Um por mesa, recrutado pelo motor na abertura da mesa ([abertura](mesas.md#abertura)) com a ficha padrão de Mestres de mesa do workspace (ou outra indicada pelo Grão-Mestre a pedido de um usuário).
- Nome fixo no canvas e na CLI: `Mestre` (único dentro da mesa).
- Inicia no worktree da mesa na Cidadela. Pode rodar em outra máquina se a ficha exigir, mas o worktree de referência continua sendo o da Cidadela ([sincronização](mesas.md#worktree-por-máquina-e-sincronização)).
- No GitHub, tudo o que ele pede sai com o App "Mestre" ([identidades](../github/identidades-e-apps.md)).
- Conexões automáticas: com o Grão-Mestre e com todo personagem da mesa ([conexões](conexoes.md)).

### Liberdade dentro da mesa

Pode, sem pedir aprovação, sempre dentro do [teto do workspace](../classes/teto-do-workspace.md) e dos [limites](personagens.md#limites):
- recrutar personagens a partir de fichas e criar terminais a partir de bases ([personagens](personagens.md));
- criar, rodar, pausar e cancelar [missões](missoes.md);
- criar **classes da mesa** e pedir o App delas ([classes](../classes/classes.md));
- criar conexões extras, pergaminhos e [rituais](rituais.md) da mesa;
- pedir operações no GitHub restritas ao épico da mesa (`mestre issue`, `mestre pr`; [ações por nível](../github/acoes-por-nivel.md)).

Nunca libera para uma classe algo que o teto proíbe; o motor recusa a classe se o fizer. Sintaxe dos comandos: [coordenação](../cli/coordenacao.md), [classes e rituais](../cli/coordenacao-classes-e-rituais.md) e [issues e PRs](../cli/coordenacao-github.md).

### Fixo enquanto a mesa estiver aberta

- Nasce fixado; não é encerrado por ociosidade nem por `dismiss` de outro agente. Só é encerrado no encerramento ou cancelamento da mesa, por último ([mesas](mesas.md#encerramento)).
- Se o processo morrer, o motor o reinicia com a mesma política do Grão-Mestre ([fixo e protegido](grao-mestre.md#fixo-e-protegido)); o alerta, após 3 falhas em 10 min, vai ao Grão-Mestre (e o motor avisa os usuários).
- Depois de reinício, é retomado logo após o Grão-Mestre, recebendo uma **mensagem de retomada** com: missões da mesa (estado e etapa atual), personagens e seus estados, esperas dele e itens na caixa de entrada.

### Instruções do sistema

Conteúdo obrigatório do modelo de instruções do Mestre da mesa (versionado com o código; montagem e entrega em [sessão zero do personagem](../sessao-zero/personagem.md)):

1. **Papel:** coordenar a party da mesa; o trabalho de execução é delegado. Fazer sozinho só o que for trivial (ex.: ler um arquivo para planejar, ajustar um pergaminho).
2. **Planejar:** transformar o pedido em [missão](missoes.md): etapas; classe e ficha de cada etapa; o que roda em paralelo; onde há devolução (com limite de voltas); onde é preciso aprovação; quais etapas viram sub-issue.
3. **Montar o time:** consultar classes, fichas e personagens (`mestre list`, `mestre class list`, `mestre sheet list`); reaproveitar persistentes livres; respeitar os limites; criar classes da mesa quando precisar, dentro do teto, já com o App definido.
4. **Coordenar máquinas:** não deixar dois personagens em máquinas diferentes editarem os mesmos arquivos ao mesmo tempo (dividir por arquivos/pastas nas instruções das etapas, ou sequenciar); tratar avisos de divergência de branch pedindo a um personagem que integre.
5. **Não bloquear:** delegar e encerrar o turno; reagir aos eventos da caixa de entrada. `ask` bloqueante só para perguntas rápidas; para trabalho demorado, ask back ou missão. Ao estourar um tempo limite, nunca reenviar o mesmo pedido: usar `check`, reenviar com mais contexto, trocar o personagem ou avisar o Grão-Mestre.
6. **Relatar ao Grão-Mestre** (pela caixa de entrada): andamento relevante, bloqueios, pedidos de decisão e o resultado final (o que foi feito, arquivos alterados, pendências). Ao terminar o épico, abrir o PR (`mestre pr open`) e avisar.
7. **Respeitar o trabalho dos outros:** nunca interromper um personagem ocupado nem pedir que edite arquivos que outro está modificando.

### Instruções extras do usuário

- Texto livre por workspace que vale para **todos os Mestres de mesa** dele, com as mesmas regras do Grão-Mestre ([instruções extras](grao-mestre.md#instruções-extras-do-usuário)): até 4 KiB (padrão), editado por Administrador ou acima, soma sem substituir, entra sob o título "Instruções dos usuários para os Mestres de mesa", vale a partir do próximo turno.

### Skills

Recebe as skills de comunicação, de coordenação de mesa e de rituais ([skills geradas](../cli/skills-geradas.md)).

## Regras e erros

- Não fala com usuários: `mestre reply` e `mestre notify` chamados por ele retornam erro de permissão. Para falar com usuários, pede ao Grão-Mestre.
- Não mexe em issues ou PRs de outra mesa (pede ao Grão-Mestre); o motor recusa.
- Classe da mesa com permissão acima do teto: o motor recusa a criação e lista os itens que excedem.
- Ações destrutivas (`note delete`, `ritual delete`, `class delete`) só com pedido explícito de um usuário autorizado: exceção pontual para aquela ação ou aprovação do tipo `acao_destrutiva` ([política](../aprovacoes/politica.md#tipos-de-ação), [convenções da CLI](../cli/convencoes.md)).

## Critérios de aceite

- **CA-MM-01** · *completa* — **Dado** uma mesa recém-aberta, **quando** o Mestre da mesa fica pronto, **então** ele se chama `Mestre`, está fixado, conectado ao Grão-Mestre e iniciado no worktree da mesa.
- **CA-MM-02** · *rápida* — **Dado** um teto que proíbe internet, **quando** o Mestre da mesa cria uma classe da mesa com rede liberada, **então** a criação é recusada e o erro lista o item excedente.
- **CA-MM-03** · *rápida* — **Dado** um Mestre da mesa, **quando** chama `mestre reply`, `mestre notify` ou `mestre issue update` numa issue de outra mesa, **então** recebe erro de permissão.
- **CA-MM-04** · *rápida* — **Dado** instruções extras para Mestres de mesa, **quando** a camada de dois Mestres de mesa do mesmo workspace é montada, **então** ambas contêm o texto extra depois das instruções do sistema.
- **CA-MM-05** · *pesada* — **Dado** uma mesa com pedido de funcionalidade, **quando** o Mestre da mesa planeja, **então** monta uma missão com classes diferentes em provedores e máquinas diferentes e relata o resultado ao Grão-Mestre (MVP-4).

## Depende de

- [Mesas](mesas.md)
- [Grão-Mestre](grao-mestre.md)
- [Personagens](personagens.md)
- [Missões](missoes.md)
- [Teto do workspace](../classes/teto-do-workspace.md)
- [Sessão zero do personagem](../sessao-zero/personagem.md)
- [Conexões](conexoes.md)
