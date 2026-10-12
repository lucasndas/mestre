# Decretos

> **Assunto:** ordens de usuários autorizados que valem acima das regras: tipos, vínculo ao estado, validade, conflitos, revogação, PIN e registro · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Transformar uma decisão de um usuário autorizado, dada pelo Telegram ou pelo painel, numa regra registrada que o motor aplica a todos os agentes afetados. A posição dos decretos na ordem de precedência é definida em [precedência](../arquitetura/precedencia.md). Lá também está como eles chegam aos agentes (pelo `mestre hook` e pela camada do Mestre).

## Comportamento

### Tipos

| Tipo | Vale para | Exemplo | Quem cria ([níveis](usuarios-e-niveis.md)) |
|---|---|---|---|
| **Exceção pontual** | uma ação exata, uma única vez | "aprovo o merge do PR #42" | Aprovador ou acima |
| **Regra permanente** | daqui pra frente, até ser revogada ou expirar | "o Dev pode fazer push na develop" | Administrador ou acima |

- **Aprovação vira exceção pontual:** responder "Aprovar" a um pedido de aprovação cria uma exceção pontual. O fluxo do pedido está em [fluxo de aprovação](../aprovacoes/fluxo.md).
- **Efeitos possíveis** de um decreto. Cada decreto tem um efeito e um escopo (workspace e, se houver, mesa, classe ou personagem):
  - **permissão:** libera ou proíbe arquivos, comandos, git, rede ou comandos da CLI, com o mesmo vocabulário do [permissions.yaml](../classes/permissoes.md);
  - **comportamento:** uma instrução curta, que entra na camada do Mestre dos personagens afetados;
  - **política:** ajusta a [política de aprovação](../aprovacoes/politica.md) (ex.: "me pergunte tudo esta semana");
  - **teto:** amplia o [teto do workspace](../classes/teto-do-workspace.md);
  - **delegação:** autoriza o Grão-Mestre a aprovar no GitHub em nome do autor ([aprovação no GitHub](../github/aprovacao-no-github.md)).

### Criação

1. O usuário dá a ordem em linguagem natural (texto, áudio ou painel).
2. O Grão-Mestre interpreta e envia ao motor uma **proposta estruturada**: tipo, efeito, escopo, validade e o texto original da mensagem. A sintaxe do comando está em [CLI do Grão-Mestre](../cli/grao-mestre.md).
3. O motor confere o nível do autor e mostra o que vai valer, com os botões **Confirmar** e **Cancelar**. Exemplo: "Entendi: permitir push na develop para a classe Dev, permanente. Confirma?".
   - Mensagens ambíguas: o Grão-Mestre pergunta antes de propor.
   - Ordens vindas de **áudio** sempre passam por essa confirmação, com o texto transcrito à vista ([transcrição](anexos-e-transcricao.md)). O mesmo vale para aprovações dadas por áudio.
4. Se for preciso, o motor pede a **confirmação reforçada** (abaixo).
5. O decreto passa a **ativo** e recebe um número sequencial por instalação (ex.: "decreto 7"). A resposta ao usuário cita esse número.

- O agente só propõe. Quem grava, confere nível e aplica é o motor.
- Só o motor aceita o clique em Confirmar, e só do próprio autor.

### Confirmação reforçada com PIN

- O motor pede o PIN do autor ([PIN](usuarios-e-niveis.md#pin-pessoal)) quando o efeito líquido **afrouxa a segurança**:
  - todo decreto permanente;
  - liberar push ou merge em branch protegida;
  - liberar comandos destrutivos ou proibidos pela classe;
  - ampliar o teto;
  - delegação de aprovação no GitHub;
  - revogar um decreto que restringia algo.
- Uma aprovação comum, dentro do que a política já prevê, não pede PIN.
- PIN errado ou expirado deixa a proposta como **cancelada**.

### Vínculo ao estado (exceção pontual)

- Toda exceção pontual fica amarrada a uma **impressão do estado**: workspace, repositório, número do PR ou issue, hash do commit (head do PR), caminho e hash do arquivo, ou o comando exato, conforme a ação.
- O motor compara a impressão na hora de executar. Se algo mudou (ex.: entrou um commit novo no PR), a exceção **expira sem uso** e o Grão-Mestre reavalia. Se for o caso, ele pede de novo.
- O prazo máximo de uma exceção não usada é o mesmo das aprovações ([fluxo](../aprovacoes/fluxo.md)).
- Depois de usada uma vez, ela fica **usada** e não vale de novo.

### Validade (regra permanente)

- A validade é opcional. Exemplos: "esta semana" vira o fim do domingo, no fuso do Core; "por 3 dias" vira 72 horas.
- O Grão-Mestre converte a validade em data e hora absolutas, e elas aparecem na confirmação.
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
- **Quem pode revogar** está na [tabela de níveis](usuarios-e-niveis.md#níveis-e-capacidades). Ninguém revoga decreto de nível acima do seu.
- A revogação vale para as próximas decisões. O que já foi executado continua no histórico.

### Autor suspenso ou removido

- **Exceções pontuais** ainda não usadas: são invalidadas ([usuários](usuarios-e-niveis.md#suspensão-e-remoção)).
- **Regras permanentes:** passam a **suspensas**, e o Dono recebe a lista. Ele pode reativar cada uma, e então ela passa a ser dele, ou revogar.
- `/travar` suspende todos os decretos de uma vez ([segurança do canal](seguranca-do-canal.md)).

### Registro

Cada decreto fica no banco do Core com:

- número;
- tipo, efeito e escopo;
- autor e nível;
- texto original e origem: canal, ID da mensagem e, se veio de áudio, o anexo e a transcrição;
- data de criação e validade;
- se teve PIN;
- estado e histórico de estados;
- lista do que foi executado com base nele (ação, quando, por quem).

`/decretos` lista os decretos ativos e suspensos dos workspaces do usuário, com número, autor, resumo e validade.

## Regras e erros

- Um decreto fora do nível do autor é recusado antes da confirmação, com o nível exigido.
- Um decreto fora dos workspaces do autor é recusado.
- Um decreto nunca libera o que o motor proíbe por regra local fixa de segurança, como ler o cofre ou contornar proteção do GitHub. Nesse caso, o motor explica.
- Se a proposta do Grão-Mestre não bate com a confirmação do usuário, nada é gravado.

## Critérios de aceite

- **CA-DEC-01** · *rápida* — **Dado** um decreto permanente confirmado com PIN, **quando** um personagem da classe alvo tenta a ação liberada, **então** o `mestre hook` permite e a execução fica registrada no decreto (MVP-8).
- **CA-DEC-02** · *rápida* — **Dado** o decreto 7 ativo, **quando** o autor revoga, **então** a mesma ação volta a ser negada na próxima decisão (MVP-8).
- **CA-DEC-03** · *rápida* — **Dado** uma exceção pontual para o PR #42 no commit `abc`, **quando** entra o commit `def` antes do merge, **então** a exceção expira sem uso e o merge não é feito com ela.
- **CA-DEC-04** · *rápida* — **Dado** dois decretos opostos de Administradores no mesmo alvo, **quando** o segundo é ativado, **então** os dois ficam suspensos e os dois autores e o Dono são avisados.
- **CA-DEC-05** · *rápida* — **Dado** um decreto de Dono e outro oposto de Administrador, **quando** há decisão sobre o alvo, **então** vale o do Dono e o outro aparece como sobreposto.
- **CA-DEC-06** · *rápida* — **Dado** um decreto com validade "por 3 dias", **quando** o relógio falso avança 72 horas, **então** ele fica expirado e o autor é avisado.
- **CA-DEC-07** · *rápida* — **Dado** uma ordem vinda de áudio, **quando** o Grão-Mestre propõe o decreto, **então** a confirmação mostra o texto transcrito e nada vale sem o clique do autor.
- **CA-DEC-08** · *pesada* — **Dado** o sistema instalado, **quando** o Dono cria pelo Telegram (dublê) um decreto permanente, vê o efeito num agente e o revoga, **então** o comportamento muda e volta, e o registro mostra os dois momentos (MVP-8).

## Depende de

- [Precedência](../arquitetura/precedencia.md)
- [Usuários e níveis](usuarios-e-niveis.md)
- [Permissões das classes](../classes/permissoes.md)
- [Fluxo de aprovação](../aprovacoes/fluxo.md)
- [Adaptador de provedores](../provedores/adaptador.md)
- [CLI do Grão-Mestre](../cli/grao-mestre.md)
