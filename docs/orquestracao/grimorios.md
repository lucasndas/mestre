# Grimórios

> **Assunto:** salvar um pedaço do canvas (time, classes, pergaminhos, conexões e missões) e restaurá-lo em mesa de qualquer workspace · **Componente:** Motor · **Atualizado:** 2026-10-15

## Objetivo

Um grimório é um time salvo: uma seleção do canvas de uma mesa que pode ser restaurada em outra mesa, **de qualquer workspace**, para reaproveitar o time inteiro com o fluxo pronto. Ele guarda a receita, não o estado: nada de sessões, filas ou histórico.

## Comportamento

### Salvar

Pelo painel, com uma seleção no canvas da mesa (usuário Administrador ou acima; [canvas](../painel/canvas.md)). O motor grava no banco da Cidadela um documento JSON (`version: 1`) com nome único, autor, data, workspace e mesa de origem, contendo:

| Elemento | O que é salvo | O que não é salvo |
|---|---|---|
| Personagens | nome, ficha, classe (se diferente da padrão da ficha), máquina indicada, posição, fixado | processo, sessão do provedor, caixa de entrada, esperas, uso |
| Classes usadas | globais: só a referência; do workspace ou da mesa: referência **e** cópia do pacote ([classes](../classes/classes.md)) com o App vinculado | — |
| Pergaminhos e tomos | nomes, conteúdo, tomos e posição (o usuário pode optar por salvar só a estrutura, sem conteúdo) | revisões anteriores |
| Conexões | as manuais entre elementos salvos (as automáticas são recriadas pelo motor) | conexões com elementos fora da seleção |
| Missões | a definição ([missões](missoes.md#definição)) cujas etapas usam personagens ou fichas da seleção | estado, rodadas, números de issue |

Grão-Mestre e Mestre da mesa nunca entram num grimório (são da mesa/workspace).

### Restaurar

Pelo painel, numa mesa em `working` ou `in_review` ([estados](mesas.md#estados)), por Administrador ou acima com acesso ao workspace de destino.

1. **Validação** (nada é criado ainda):
   - fichas citadas existem ([bases e fichas](bases-e-fichas.md)); se faltar alguma, a restauração é recusada listando quais;
   - cada classe é resolvida no destino: global → a mesma; do workspace com o mesmo nome no destino → a do destino; classe da mesa de origem → copiada como classe da mesa de destino;
   - **classe do workspace de origem que não existe no destino:** aviso, e o usuário escolhe para cada uma: **copiar como classe da mesa** (usa a cópia do pacote), **mapear para outra classe** existente no destino, ou **cancelar** a restauração;
   - toda classe copiada precisa caber no [teto do workspace](../classes/teto-do-workspace.md) de destino; se não couber, a restauração é recusada listando os itens excedentes;
   - os [limites](personagens.md#limites) de personagens precisam comportar o time; se não, recusa mostrando quantos cabem;
   - nomes de personagens, pergaminhos e tomos que já existem no destino recebem sufixo (" (2)"…), mostrado ao usuário antes de confirmar.
2. **Confirmação** do usuário com o resumo do que será criado.
3. **Criação**, nesta ordem: classes copiadas; pergaminhos e tomos; personagens (escolha de máquina normal, [personagens](personagens.md#escolha-da-máquina)); conexões; missões (criadas como qualquer missão nova, em `draft`, com issue sob o épico de destino).
4. O Mestre da mesa de destino recebe um item `system` listando o que foi restaurado.

Classe copiada mantém o App vinculado. Se o App não estiver instalado no repositório de destino, o motor o acrescenta à instalação ou o Dono recebe o link para isso ([repositórios novos](../github/criacao-de-apps.md#repositórios-novos-e-permissões-novas)); até lá, as ações saem com o App "Mestre" com a classe na assinatura ([identidades e Apps](../github/identidades-e-apps.md)).

### Listar e apagar

O painel lista os grimórios de todos os workspaces (nome, origem, autor, data, conteúdo). Apagar exige pedido explícito de Administrador ou acima e não afeta mesas já restauradas.

### Detalhes que o motor decide

- **Seleção:** o painel manda os ids (personagens, pergaminhos, tomos e missões) e as posições no canvas, que ficam no documento e voltam no relatório da restauração para o painel posicionar. Um tomo leva os pergaminhos que guarda; um pergaminho do motor ("Missão: …", "Histórico da mesa") ou um id que não é da mesa recusa o salvamento, listando todos os problemas de uma vez. Grão-Mestre e Mestre da seleção ficam de fora e são avisados em `left`.
- **Missão:** só entra se todo `character` das etapas está na seleção; as fichas são citadas pelo nome e conferidas na restauração.
- **Restaurar em duas chamadas:** `Plan` valida e devolve o que será criado (nomes com sufixo, classes e a escolha pendente); `Restore` repete a validação e cria. Com escolha de classe pendente ele recusa antes de criar qualquer coisa.
- **Classes:** a classe do destino com o mesmo nome, de qualquer escopo, é a usada; a de mesa de origem ausente no destino é copiada sem perguntar; a cópia é gravada pela biblioteca de classes (`Library.Save`), que confere pacote, teto e nome, e o pacote do documento é conferido de novo (vem do banco, que as Torres replicam).
- **Nomes:** missões repetidas também recebem " (2)"; o nome é encurtado para caber nos 32 caracteres, e os nomes que ficam são reservados antes dos que mudam. A máquina salva é só preferência. A missão restaurada segue os novos nomes dos personagens.
- **Falhas parciais:** o relatório lista personagens `interrupted`, personagens à espera de vaga (limite de máquina ou de cópias da ficha) e o que não pôde ser feito.

## Regras e erros

- Personagem que não conseguir iniciar na criação (ex.: a máquina caiu) não desfaz o resto: fica `interrupted` e entra no relatório ao Mestre da mesa.
- Restaurar em mesa `open`, `closing`, `closed`, `cancelling` ou `cancelled`: recusado.
- Grimório com `version` maior que a suportada: recusado dizendo a versão esperada.
- Nome de grimório repetido ao salvar: o usuário escolhe substituir ou outro nome.

## Critérios de aceite

- **CA-GRIM-01** · *rápida* — **Dado** uma seleção com dois personagens, um pergaminho, uma conexão manual e uma missão, **quando** é salva, **então** o documento contém esses elementos e nenhum dado de sessão, fila ou espera.
- **CA-GRIM-02** · *rápida* — **Dado** um grimório com classe do workspace A ausente no workspace B, **quando** é restaurado em mesa de B, **então** o usuário recebe o aviso com as três opções e, escolhendo "copiar como classe da mesa", a classe aparece em `.mestre/mesas/<mesa>/classes/`.
- **CA-GRIM-03** · *rápida* — **Dado** uma classe copiada que excede o teto de B, **quando** se tenta restaurar, **então** é recusado listando o item excedente e nada é criado.
- **CA-GRIM-04** · *rápida* — **Dado** um personagem "Dev" já existente no destino, **quando** se restaura um grimório com "Dev", **então** o novo se chama "Dev (2)" e o resumo de confirmação mostra isso.
- **CA-GRIM-05** · *completa* — **Dado** um grimório válido, **quando** é restaurado, **então** personagens nascem conectados conforme o salvo, pergaminhos têm o conteúdo salvo e a missão existe em `draft` com issue sob o épico de destino.
- **CA-GRIM-06** · *pesada* — **Dado** o painel, **quando** salvo um grimório de uma mesa e restauro em outra mesa, **então** o time volta funcionando e uma missão restaurada roda (MVP-11).

## Depende de

- [Mesas](mesas.md)
- [Personagens](personagens.md)
- [Bases e fichas](bases-e-fichas.md)
- [Missões](missoes.md)
- [Pergaminhos e tomos](pergaminhos-e-tomos.md)
- [Conexões](conexoes.md)
- [Classes](../classes/classes.md)
- [Teto do workspace](../classes/teto-do-workspace.md)
