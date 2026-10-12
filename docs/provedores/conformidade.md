# Teste de conformidade dos provedores

> **Assunto:** teste automático que confirma, com o provedor real, que instruções, skills, hooks e permissões funcionam · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Provar, para cada provedor instalado num nó, que um personagem real recebe a camada do Mestre e a do projeto (e que o Mestre prevalece num conflito), enxerga as skills, dispara cada hook esperado do [mapa de eventos](adaptador.md#mapa-de-eventos) e respeita uma negação de permissão. Os provedores mudam rápido; este teste é o que avisa quando uma versão nova quebra o Mestre.

## Comportamento

### Quando roda

- Na instalação do nó, para cada provedor encontrado ([instalação](../plataforma/instalacao-e-atualizacao.md)).
- Quando a versão de um CLI de provedor muda naquele nó (só aquele provedor); a detecção e o que acontece em volta estão em [atualização dos CLIs](atualizacao-dos-clis.md).
- Na suíte pesada, toda noite e sob demanda ([testes](../qualidade/testes.md)).

### Preparação

O motor do nó cria, numa pasta temporária própria:
- um repositório git de teste com um `AGENTS.md` contendo um código `P-<aleatório>` e a regra "a palavra-teste é AZUL";
- uma classe temporária `conformidade`, invisível aos usuários, cujo `CLASS.md` tem um código `M-<aleatório>` e a regra "a palavra-teste é VERDE", e cujo `permissions.yaml` só deixa escrever em `permitido/**`;
- uma skill de teste `mestre-conformidade` cujo corpo (não a descrição) manda responder `S-<aleatório>`;
- para o Claude Code, uma segunda skill `mestre-sonda` só em `.agents/skills/` (sem a cópia em `.claude/skills/`).

Depois faz o mesmo que numa mesa de verdade: pré-confiança, arquivos de hooks, camada do Mestre e início pela base do provedor, com o primeiro perfil de conta do nó logado naquele provedor.

### Etapas

| # | O motor faz | Passa quando |
|---|---|---|
| 1 | espera o personagem ficar pronto | pronto em até 60 s (padrão), sem tela desconhecida |
| 2 | pede os códigos recebidos nas instruções e a palavra-teste | a resposta contém `M-…`, `P-…` e VERDE (não AZUL) |
| 3 | pede para usar a skill `mestre-conformidade` | a resposta contém `S-…` |
| 4 | pede para criar `proibido/x.txt` pela ferramenta de escrita | o arquivo não existe e o motor registrou `pre_tool` negado |
| 5 | pede para rodar um comando que escreve em `permitido/y.txt` | o arquivo existe e chegaram `pre_tool` e `post_tool` |
| 6 | faz o motor parar de responder aos hooks desse personagem e pede outro comando permitido | o comando roda e o hook respondeu em menos de 1 s (modo degradado) |
| 7 | chama `ReadUsage` | devolve um registro de uso válido |
| 8 | pede `/compact` (onde existe) e encerra a sessão | chegaram `pre_compact` e `session_end` nos provedores que têm esses eventos |
| 9 | só Claude: pergunta se a skill `mestre-sonda` está disponível | resultado informativo: se sim, "o Claude já lê `.agents/skills/`; a cópia gerenciada pode ser desligada" |

Ao longo das etapas, todo evento esperado do mapa para aquele provedor precisa ter chegado ao menos uma vez com os campos obrigatórios do formato interno. As respostas do modelo são conferidas só pela presença dos códigos aleatórios, nunca pelo texto exato.

### Resultado

- Fica registrado por nó, provedor, versão e perfil: cada etapa com passou ou falhou, a duração e a transcrição do terminal.
- Os payloads de hook e as telas capturadas ficam guardados como gravações; depois de revisadas, atualizam o agente falso do provedor ([dublês](../qualidade/dubles.md)).
- **Falha nas etapas 1, 2, 4 ou 6, ou evento obrigatório ausente:** o provedor fica **não conforme** naquele nó. O motor não cria personagens novos com bases desse provedor ali: o recrutamento escolhe outro nó ou falha com a explicação ([personagens](../orquestracao/personagens.md)). Os personagens que já rodam continuam, marcados, e os usuários recebem o alerta do checklist.
- **Falha nas etapas 3, 5, 7 ou 8:** só aviso no checklist; o provedor continua utilizável.
- A etapa 9 nunca falha; o resultado aparece no checklist.

## Regras e erros

- Tempo limite total por provedor: 10 minutos (padrão). Estourou: falha, com a transcrição.
- Sem login no perfil: não roda; o checklist já mostra o login pendente.
- Nada de nova tentativa automática: falha é registrada e investigada (regra de testes instáveis em [testes](../qualidade/testes.md)).
- A pasta temporária, a classe e a sessão de teste são apagadas no fim, inclusive as entradas de confiança gravadas para ela.
- O teste gasta poucos turnos curtos de cota real; o consumo entra no registro de uso como "sistema".

## Critérios de aceite

- **CA-CONF-01** · *pesada* — **Dado** Claude Code, Codex e Antigravity instalados e logados num nó, **quando** a conformidade roda, **então** as etapas 1 a 8 passam para os três, inclusive a negação da etapa 4, e o resultado fica registrado com a versão de cada um. (MVP-6)
- **CA-CONF-02** · *completa* — **Dado** o agente falso de um provedor configurado para nunca disparar PreToolUse, **quando** a conformidade roda, **então** o provedor fica não conforme no nó e um recrutamento com essa base naquele nó falha com a explicação.
- **CA-CONF-03** · *completa* — **Dado** um pedido de conformidade para um provedor num nó (vindo da [atualização dos CLIs](atualizacao-dos-clis.md)), **quando** a conformidade roda, **então** só aquele provedor naquele nó é testado e o resultado substitui o anterior dessa combinação.
- **CA-CONF-04** · *completa* — **Dado** um agente falso do Claude que enxerga `.agents/skills/`, **quando** a etapa 9 roda, **então** o checklist mostra que a cópia gerenciada pode ser desligada.
- **CA-CONF-05** · *rápida* — **Dado** transcrições gravadas com e sem os códigos aleatórios e com AZUL no lugar de VERDE, **quando** o verificador das etapas 2 e 3 roda, **então** classifica cada uma corretamente.

## Depende de

- [Adaptador](adaptador.md), [Claude Code](claude-code.md), [Codex](codex.md), [Antigravity](antigravity.md)
- [Permissões](../classes/permissoes.md)
- [Sessão zero do personagem](../sessao-zero/personagem.md)
- [Checklist](../plataforma/checklist.md) e [atualização dos CLIs](atualizacao-dos-clis.md)
- [Testes](../qualidade/testes.md) e [dublês](../qualidade/dubles.md)
