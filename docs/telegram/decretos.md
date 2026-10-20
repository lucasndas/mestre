# Decretos

> **Assunto:** ordens de usuários autorizados que valem acima das regras: tipos, vínculo ao estado, validade, conflitos, revogação, PIN e registro · **Componente:** Motor · **Atualizado:** 2026-10-20

## Objetivo

Transformar uma decisão de um usuário autorizado, dada pelo Telegram ou pelo painel, numa regra registrada que o motor aplica a todos os agentes afetados. A posição dos decretos na ordem de precedência é definida em [precedência](../arquitetura/precedencia.md). Lá também está como eles chegam aos agentes (pelo `mestre hook` e pela camada do Mestre).

## Comportamento

### Tipos

| Tipo | Vale para | Exemplo | Quem cria ([níveis](usuarios-e-niveis.md)) |
|---|---|---|---|
| **Exceção pontual** | uma ação exata, uma única vez | "aprovo o merge do PR #42" | Aprovador ou acima |
| **Regra permanente** | daqui pra frente, até ser revogada ou expirar | "o Dev pode enviar a branch da mesa" | Administrador ou acima |

- **Respostas viram decretos:** "Aprovar agora" a um pedido de aprovação cria uma exceção pontual; "por um tempo", "o padrão", "sempre para a classe" e "negar sempre", dadas por uma pessoa, criam uma regra (com validade, ou permanente), com as regras de nível e PIN desta página ([respostas](../orquestracao/cadeia-de-aprovacao.md#respostas)). O fluxo do pedido está em [fluxo de aprovação](../aprovacoes/fluxo.md).
- **Não são decretos:** as [regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md), que agentes da cadeia criam, e as subdelegações do Grão-Mestre ([delegação](../aprovacoes/delegacao.md#subdelegação)); ficam abaixo de todo decreto de pessoa.
- **Efeitos possíveis** de um decreto (identificador entre parênteses, usado pelo `--effect` da CLI). Cada decreto tem um efeito, um alvo (classe, personagem, workspace ou mesa) e um escopo (workspace e, se houver, mesa):
  - **permissão** (`permission`): libera ou proíbe arquivos, comandos, git, rede ou comandos da CLI, com o mesmo vocabulário do [permissions.yaml](../classes/permissoes.md);
  - **comportamento** (`behavior`): uma instrução curta, que entra na camada do Mestre dos personagens afetados;
  - **política** (`policy`): ajusta a [política de aprovação](../aprovacoes/politica.md) (ex.: "me pergunte tudo esta semana");
  - **teto** (`ceiling`): amplia o [teto do workspace](../classes/teto-do-workspace.md);
  - **delegação** (`delegation`): autoriza o Grão-Mestre a decidir pedidos de um tipo `alto` num escopo ([delegação de decisão](../aprovacoes/delegacao.md#delegação-de-decisão), inclusive `configuracao_repositorio`), a aprovar no GitHub em nome do autor ([aprovação no GitHub](../github/aprovacao-no-github.md)) ou, com alvo `ativacao_operador`, a aprovar ativações do Operador do PC (abaixo).

### Criação

1. O usuário dá a ordem em linguagem natural (texto, áudio ou painel).
2. O Grão-Mestre interpreta e envia ao motor uma **proposta estruturada** com `mestre decree propose`: tipo, efeito (`--effect`), alvo (`--target`), escopo (`--scope`), validade (`--until`) e o texto original da mensagem (`--text`). A sintaxe está em [CLI do Grão-Mestre](../cli/grao-mestre.md).
3. O motor confere o nível do autor e mostra o que vai valer, com os botões **Confirmar** e **Cancelar**. Exemplo: "Entendi: permitir o envio da branch da mesa para a classe Dev, permanente. Confirma?".
   - Mensagens ambíguas: o Grão-Mestre pergunta antes de propor.
   - Ordens vindas de **áudio** sempre passam por essa confirmação, com o texto transcrito à vista ([transcrição](anexos-e-transcricao.md)). O mesmo vale para aprovações dadas por áudio.
4. Se for preciso, o motor pede a **confirmação reforçada** (abaixo).
5. O decreto passa a **ativo** e recebe um número sequencial por Guilda (ex.: "decreto 7"). A resposta ao usuário cita esse número.

- O agente só propõe. Quem grava, confere nível e aplica é o motor.
- Só o motor aceita o clique em Confirmar, e só do próprio autor.

### Confirmação reforçada com PIN

- O motor pede o PIN do autor ([PIN](usuarios-e-niveis.md#pin-pessoal)) quando o efeito líquido **afrouxa a segurança**:
  - todo decreto permanente que libera (o que só nega, como "Negar sempre", não pede PIN: só restringe);
  - liberar push ou merge em branch protegida;
  - liberar comandos destrutivos ou proibidos pela classe;
  - ampliar o teto;
  - delegação (de decisão, aprovação no GitHub ou ativação do Operador do PC);
  - revogar um decreto que restringia algo.
- Uma aprovação comum, dentro do que a política já prevê, não pede PIN.
- PIN errado ou expirado deixa a proposta como **cancelada**.

### Delegação da ativação do Operador

- Só um decreto **do Dono**, efeito `delegation` e alvo `ativacao_operador`, deixa o Grão-Mestre aprovar pedidos de ativação do [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) ([política](../aprovacoes/politica.md#tipos-de-ação)). Proposto por outro nível, é recusado antes da confirmação.
- **Escopo obrigatório:** uma missão ou uma classe específica (`--scope "missao:<nome>"` ou `"classe:<nome>"`); sem escopo, recusado.
- **PIN:** criar o decreto de delegação sempre pede o PIN do Dono; as ativações aprovadas pelo Grão-Mestre dentro dele não pedem PIN a cada vez (a aprovação manual pelo Dono continua pedindo).
- **Limites:** duração de cada ativação até o padrão (30 min); validade obrigatória, de no máximo 7 dias. Pedido fora do escopo ou com duração maior continua só com o Dono.
- Cada ativação aprovada pelo Grão-Mestre com base no decreto fica registrada nele e avisa o Dono no Telegram, com o botão **Encerrar agora** ([bot](bot.md#mensagens-de-saída)).

### Vínculo ao estado (exceção pontual)

- Toda exceção pontual fica amarrada a uma **impressão do estado**: workspace, repositório, número do PR ou issue, hash do commit (head do PR), caminho e hash do arquivo, ou o comando exato, conforme a ação.
- O motor compara a impressão na hora de executar. Se algo mudou (ex.: entrou um commit novo no PR), a exceção **expira sem uso** e o Grão-Mestre reavalia. Se for o caso, ele pede de novo.
- O prazo máximo de uma exceção não usada é o mesmo das aprovações ([fluxo](../aprovacoes/fluxo.md)).
- Depois de usada uma vez, ela fica **usada** e não vale de novo.

### Validade (regra permanente)

- A validade é opcional. Exemplos: "esta semana" vira o fim do domingo, no [fuso da Guilda](../painel/administracao.md#fuso-da-guilda); "por 3 dias" vira 72 horas.
- O Grão-Mestre converte a validade em um instante absoluto (`--until`, ISO 8601 com o deslocamento do fuso da Guilda) e o motor guarda só esse instante; a validade termina nele, ao segundo, em qualquer fuso. A confirmação, a mensagem de decreto ativo e a lista o mostram no fuso da Guilda, com o nome e o deslocamento (`até 25/10/2026 23:59:59 (America/Sao_Paulo, UTC-03:00)`, o horário de verão incluído); sem fuso utilizável (não guardado ou inválido), mostram em UTC, dizendo `UTC`. Os segundos aparecem quando não são zero e o limite é guardado em segundos inteiros (a fração é descartada), para o horário mostrado ser exatamente o instante em que a validade termina. O lembrete e a recusa de delegação usam o mesmo formato.
- Ao vencer, o decreto passa a **expirado** sozinho. O motor avisa o autor.

### Estados

`proposto` → `ativo` → (`usado` | `expirado` | `revogado`). Pode haver também `suspenso`, que volta a `ativo` ou vai a `revogado`, e `cancelado`, quando a proposta não é confirmada.

### Conflito entre decretos

- **Conflito:** dois decretos ativos com efeitos opostos sobre o mesmo alvo (ex.: um libera e outro proíbe o mesmo comando para a mesma classe). O motor detecta na criação e a cada decisão.
- **Níveis diferentes:** o de nível mais alto vence naquele ponto. O outro continua ativo, marcado como "sobreposto pelo decreto N", e vale no resto.
- **Mesmo nível:** o motor não escolhe. Ele **suspende os dois**, avisa os dois autores e o Dono e espera uma decisão: revogar um, ajustar ou reativar.
- **Qual nível conta:** o nível **atual** do autor. Se o autor é rebaixado, os decretos dele descem junto.

### Revogação

- **Pelo Telegram:** em texto ("revogo o decreto 7"), a ordem passa pelo Grão-Mestre e pelo motor. Pela lista de `/decretos`, cada decreto tem um botão.
- **Pelo painel:** em [administração](../painel/administracao.md).
- **Tudo de uma vez:** o Dono revoga todas as delegações e regras temporárias de mesa pelo botão de emergência ([revogar tudo](../aprovacoes/delegacao.md#revogar-tudo)).
- **Quem pode revogar** está na [tabela de níveis](usuarios-e-niveis.md#níveis-e-capacidades). Ninguém revoga decreto de nível acima do seu.
- A revogação vale para as próximas decisões. O que já foi executado continua no histórico.

### Autor suspenso ou removido

- **Exceções pontuais** ainda não usadas: são invalidadas ([usuários](usuarios-e-niveis.md#suspensão-e-remoção)).
- **Regras permanentes:** passam a **suspensas**, e o Dono recebe a lista. Ele pode reativar cada uma, e então ela passa a ser dele, ou revogar.
- `/travar` suspende todos os decretos de uma vez ([segurança do canal](seguranca-do-canal.md)): a lista mostra todos como suspensos, e o `/destravar` os devolve ao estado que tinham.

### Registro

Cada decreto fica no banco da Cidadela com:

- número;
- tipo, efeito e escopo;
- autor e nível;
- texto original e origem: canal, ID da mensagem e, se veio de áudio, o anexo e a transcrição;
- data de criação e validade;
- se teve PIN;
- estado e histórico de estados;
- lista do que foi executado com base nele (ação, quando, por quem).

`/decretos` lista os decretos ativos e suspensos dos workspaces do usuário, com número, autor, resumo e validade, e também as [regras temporárias de mesa](../orquestracao/regras-temporarias-de-mesa.md) ativas (marcadas como tal), cada uma com o botão **Revogar** para Administrador ou acima ([níveis](usuarios-e-niveis.md#níveis-e-capacidades)).

## Regras e erros

- Um decreto fora do nível do autor é recusado antes da confirmação, com o nível exigido.
- Um decreto fora dos workspaces do autor é recusado.
- Um decreto que libera algo numa [invariante arquitetural](../arquitetura/precedencia.md#invariantes-arquiteturais) (ex.: "o Dev pode enviar a branch da mesa") é **cumprido pelo motor**, nunca entregando a capacidade ao agente: o `git push` continua negado ao Dev, e o envio da branch é feito pelo motor quando o Dev roda `mestre pr open|update`. O que nem o motor pode fazer (contornar proteção do GitHub, entregar segredo do cofre a um agente) é recusado antes da confirmação, com a explicação.
- Se a proposta do Grão-Mestre não bate com a confirmação do usuário, nada é gravado.
- Um decreto que tenta mudar a classe `orquestrador` em si (campos, regras fixas ou `files.deny` da classe, e não uma liberação como a de qualquer personagem) é recusado antes da confirmação, com o motivo; o que o decreto de pessoa pode é liberar algo a um orquestrador, que vale como para qualquer personagem ([classe orquestrador](../classes/classe-orquestrador.md); CA-DEC-12).

- **Implementação:** `internal/decrees` guarda os decretos (`decrees`, `decree_events`, `decree_uses` e `decree_proposals`, no banco da Guilda). `Propose` confere nível, workspaces e recusas e só guarda a proposta; `Confirm` (só o autor, com o PIN pedido pelo `BeginPIN`) relê o autor do que está gravado e numera o decreto; `Revoke`, `Reactivate`, `Sweep` e `InvalidateBy` cobrem a revogação, a validade e o autor que sai. É também o `chain.Grants` e o `chain.Delegations`, o `ExceptionLedger` das exceções, a fonte dos decretos das regras efetivas (`Decrees`) e da camada do Mestre (`Notes`). O canal é `telegram.DecreeChannel` (**Confirmar**, **Cancelar**, `/decretos`, **Revogar**). No `mestre run`, o uso no hook é registrado fora do caminho da resposta, numa fila limitada (`Applied`, por `Answer.Decree`, também o evento de uma Torre, com o instante do evento): o número que o evento traz nunca vale sozinho, então só conta se o decreto libera aquela chamada (a exceção é a ação exata, e a regra cobre a ação), alcança o personagem e valia no instante do evento; um uso depois do fim fica marcado `[depois do fim]`, com aviso no log. Uma exceção pontual é gasta uma vez para o decreto todo, qualquer que seja o personagem que ela alcança (o registro do hook da Cidadela consulta o decreto), e gastá-la republica as regras efetivas; a exceção presa a um PR nunca vai ao hook. As regras efetivas são publicadas de novo, com contexto próprio e nova tentativa se falhar, a cada decreto feito, revogado, reativado, suspenso ou invalidado e quando o nível de alguém muda; a exceção presa a um PR é gasta por `Redeem`, que exige o pedido (mesmo workspace e mesmo alvo) e confere (`Targets`) só o repositório do workspace do decreto. O `mestre gh merge` o chama: com uma exceção ativa para o workspace, o tipo `merge` e a impressão exata do alvo (repositório do workspace, PR, `head` e base), o merge sai sem pedido e a exceção é gasta uma vez antes do merge (se o GitHub o recusar, ela fica gasta); uma exceção do mesmo PR com outro `head` expira sem uso (`ExpireMoved`). Pendências: [ligação de produção](../desenvolvimento/ligacao-de-producao.md).

## Critérios de aceite

- **CA-DEC-01** · *rápida* — **Dado** um decreto permanente confirmado com PIN, **quando** um personagem da classe alvo tenta a ação liberada, **então** o `mestre hook` permite e a execução fica registrada no decreto (MVP-8).
- **CA-DEC-02** · *rápida* — **Dado** o decreto 7 ativo, **quando** o autor revoga, **então** a mesma ação volta a ser negada na próxima decisão (MVP-8).
- **CA-DEC-03** · *rápida* — **Dado** uma exceção pontual para o PR #42 no commit `abc`, **quando** entra o commit `def` antes do merge, **então** a exceção expira sem uso e o merge não é feito com ela.
- **CA-DEC-04** · *rápida* — **Dado** dois decretos opostos de Administradores no mesmo alvo, **quando** o segundo é ativado, **então** os dois ficam suspensos e os dois autores e o Dono são avisados.
- **CA-DEC-05** · *rápida* — **Dado** um decreto de Dono e outro oposto de Administrador, **quando** há decisão sobre o alvo, **então** vale o do Dono e o outro aparece como sobreposto.
- **CA-DEC-06** · *rápida* — **Dado** um decreto com validade "por 3 dias", **quando** o relógio falso avança 72 horas, **então** ele fica expirado e o autor é avisado.
- **CA-DEC-07** · *rápida* — **Dado** uma ordem vinda de áudio, **quando** o Grão-Mestre propõe o decreto, **então** a confirmação mostra o texto transcrito e nada vale sem o clique do autor.
- **CA-DEC-10** · *rápida* — **Dado** um decreto do Dono `delegation` para `ativacao_operador` com escopo `classe:operador`, validade de 3 dias e duração até 30 min, **quando** o Grão-Mestre aprova um pedido dessa classe de 20 min, **então** o motor aceita, registra no decreto e avisa o Dono com o botão **Encerrar agora**; **quando** o pedido é de 45 min ou de outra classe, **então** recebe `human_required`; **dado** a mesma proposta de um Administrador ou com validade de 10 dias, **então** é recusada antes da confirmação.
- **CA-DEC-08** · *pesada* — **Dado** o sistema instalado, **quando** o Dono cria pelo Telegram (dublê) um decreto permanente, vê o efeito num agente e o revoga, **então** o comportamento muda e volta, e o registro mostra os dois momentos (MVP-8).
- **CA-DEC-09** · *rápida* — **Dado** o decreto "o Dev pode enviar a branch da mesa" ativo, **quando** o Dev tenta `git push`, **então** o `mestre hook` continua negando; **quando** o Dev roda `mestre pr update`, **então** o motor envia a branch da mesa com o App da classe e registra a execução no decreto; **dado** um decreto "pode ignorar a revisão obrigatória", **então** ele é recusado antes da confirmação com a explicação.
- **CA-DEC-11** · *rápida* — **Dado** um Administrador que responde "Negar sempre" a um pedido, **quando** confirma, **então** nasce o decreto permanente que nega sem pedir PIN; **dado** que ele propõe um decreto permanente que libera, **então** o PIN é pedido; **dado** a regra temporária de mesa ativa, **quando** `/decretos` roda, **então** ela aparece marcada com **Revogar**, e o toque de um Aprovador é recusado.
- **CA-DEC-12** · *rápida* — **Dado** um Administrador que pede um decreto que altera a classe `orquestrador` (por exemplo, tirar uma regra fixa dela), **quando** o decreto é proposto, **então** é recusado antes da confirmação, nada é gravado e a classe segue como estava; **dado** um decreto que libera uma ação a um orquestrador, **então** ele é aceito e vale como para qualquer personagem.
- **CA-DEC-13** · *rápida* — **Dado** uma proposta com `--until 2026-10-25T23:59:59-03:00` e a Guilda em `America/Sao_Paulo`, **quando** o motor monta a confirmação, **então** ela diz "até 25/10/2026 23:59:59 (America/Sao_Paulo, UTC-03:00)"; **dado** um fuso com horário de verão (`America/New_York`), **então** o mesmo horário local sai com o deslocamento certo de cada lado da virada (UTC-04:00 e UTC-05:00); **dado** o fuso ausente ou inválido, **então** ela diz "UTC"; **e** o decreto vira `expirado` no instante absoluto, não um segundo antes, seja qual for o fuso.

## Depende de

- [Precedência](../arquitetura/precedencia.md)
- [Usuários e níveis](usuarios-e-niveis.md)
- [Permissões das classes](../classes/permissoes.md)
- [Adaptador de provedores](../provedores/adaptador.md)
- [Política de aprovação](../aprovacoes/politica.md)
- [Fluxo de aprovação](../aprovacoes/fluxo.md)
- [Bot](bot.md)
