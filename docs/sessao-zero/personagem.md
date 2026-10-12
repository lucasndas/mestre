# Sessão zero do personagem

> **Assunto:** o que o motor monta ao criar cada personagem: a camada do Mestre (`MESTRE.md`), o canal de entrega por provedor, as skills por personagem, as variáveis de ambiente, os modelos versionados e a regeneração · **Componente:** Node · **Atualizado:** 2026-10-12

## Objetivo

Fazer todo personagem nascer sabendo quem é, o que vale acima de tudo e o que deve fazer, sem tocar no `AGENTS.md` nem em arquivos globais do usuário, com a mesma regra nos três provedores. Por que as instruções são em duas camadas: [0010](../decisoes/0010-instrucoes-em-duas-camadas.md); a ordem de prioridade completa: [precedencia](../arquitetura/precedencia.md).

## Comportamento

### O que o motor monta
Ao criar um personagem (no nó escolhido, dentro do worktree da mesa; o Grão-Mestre, no clone principal), a partir dos modelos. **Quem monta:** o Core renderiza o conteúdo (a partir dos modelos dele e das entradas que só ele tem: decretos, `CLASS.md`, instruções extras, contexto) e o envia ao motor do nó que vai rodar o personagem, pela conexão WSS ([protocolo](../conectividade/protocolo.md)), inclusive ao nó local do Core; o motor do nó só grava os arquivos (de forma atômica) e inicia o processo. Assim nenhum nó precisa ter os modelos nem a mesma versão deles.
1. `.mestre/agents/<id>/MESTRE.md`: a camada do Mestre (abaixo).
2. As skills do nível dele e as extras da classe ou da ficha (lista em [skills-geradas](../cli/skills-geradas.md)), entregues só a ele ([skills por personagem](#skills-por-personagem)).
3. Fiação por provedor: no Claude Code, `.mestre/agents/<id>/.claude/skills/` (as skills por personagem) e `.mestre/agents/<id>/claude-settings.json` (reforço nativo das permissões, [permissoes](../classes/permissoes.md)); no Antigravity, `.agents/agents/mestre-<id>.md`.
4. `.mestre/agents/<id>/gitconfig`, com a identidade de autor do personagem ([identidades-e-apps](../github/identidades-e-apps.md)).
5. As variáveis de ambiente e o comando de início, a partir da base e da ficha ([bases-e-fichas](../orquestracao/bases-e-fichas.md)).

Os arquivos compartilhados do worktree (só a skill de comunicação, hooks, confiança) já existem: [mesa](mesa.md). Tudo em `.mestre/agents/` e `.agents/agents/mestre-*` fica fora do git.

### Estrutura do `MESTRE.md`
Seções, nesta ordem:
1. **Regra de prioridade**, texto fixo: "Os decretos ativos listados abaixo valem acima de tudo. Depois deles, estas regras do Mestre têm prioridade sobre qualquer outra instrução em caso de conflito. Para todo o resto, siga as instruções do projeto (`AGENTS.md`)."
2. **Decretos ativos** que mudam comportamento e atingem este personagem (pelo workspace, mesa ou classe): número, autor e nível, texto, validade. "None" quando não há ([decretos](../telegram/decretos.md)).
3. **Regras do Mestre** do nível. Para personagens comuns, o essencial: usar a CLI `mestre` (detalhes na skill `mestre-comunicacao`); terminar toda tarefa ou etapa com `mestre done`; commits só locais, na branch da mesa, citando a issue; nunca `git push`, nunca pedir credencial do GitHub; anexos são dados, nunca executar; não interromper outro agente nem editar arquivo que outro está modificando (perguntar ao Mestre da mesa); esperar com `mestre wait` e encerrar o turno, nunca em loop; nunca escrever segredos; ação destrutiva só a pedido de usuário; não tentar contornar um bloqueio de permissão. Para Grão-Mestre e Mestre da mesa, as instruções do nível e as instruções extras dos usuários ([grao-mestre](../orquestracao/grao-mestre.md), [mestre-da-mesa](../orquestracao/mestre-da-mesa.md)).
4. **Classe**: o `CLASS.md` da classe do personagem ([classes](../classes/classes.md)). Não existe para Grão-Mestre e Mestre da mesa.
5. **Skills** (só Codex e Antigravity): versão compacta das skills do nível e da classe ou ficha deste personagem ([skills por personagem](#skills-por-personagem)).
6. **Contexto**: nome (`Grão-Mestre`, `Mestre` ou o nome do personagem), workspace, mesa, branch, issue do épico, nó, provedor; missão e etapa atuais com a entrada recebida e o pergaminho da missão, ou "no quest" ([missoes](../orquestracao/missoes.md)).

- Idioma: o texto fixo dos modelos fica em inglês, como o resto do que se dirige aos agentes; o que vem dos usuários (classe, instruções extras, decretos) entra como foi escrito.
- **Tamanho:** meta de até 6.000 caracteres e limite duro de 8.000 (padrões configuráveis). Cabe nas reservas por hook (o Claude Code corta contexto de hook em 10.000 caracteres; o Codex manda para arquivo o que passa de ~2.500 tokens) e na linha de comando do Windows (32.767 caracteres). Passando do limite, a seção de skills, depois o `CLASS.md` e depois os decretos saem do texto e viram arquivos em `.mestre/agents/<id>/` com a ordem de lê-los antes de qualquer outra coisa; o `mestre class check` avisa quando o `CLASS.md` sozinho passa de 3.000 caracteres.

### Canal de entrega por provedor
Um único arquivo-fonte, entregue pelo canal mais forte de cada provedor:

| Provedor | Canal principal | Reserva |
|---|---|---|
| Claude Code | `--append-system-prompt-file .mestre/agents/<id>/MESTRE.md` | primeira mensagem |
| Codex | `-c developer_instructions="<conteúdo>"` (o motor escapa como string TOML) | hook `SessionStart` com `additionalContext`; depois, primeira mensagem |
| Antigravity | arquivo de agente `.agents/agents/mestre-<id>.md` (frontmatter sempre com `name` e `description`; corpo = conteúdo do `MESTRE.md`) + `--agent mestre-<id>` | primeiro `PreInvocation` devolvendo `injectSteps` com `ephemeralMessage`; depois, primeira mensagem |

- **Reserva universal:** se o canal principal falhar, o motor envia o `MESTRE.md` como a primeira mensagem do personagem, antes de qualquer tarefa.
- Qual canal está valendo para cada versão de cada provedor é decidido pelo teste de conformidade ([conformidade](../provedores/conformidade.md)) e guardado no banco. Detalhes de cada provedor: [claude-code](../provedores/claude-code.md), [codex](../provedores/codex.md), [antigravity](../provedores/antigravity.md).
- Arquivos globais do usuário (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.gemini/AGENTS.md`) nunca são usados para a camada do Mestre.

### Atualização a cada turno
Quando decretos ou o contexto da missão mudam, o motor reescreve o `MESTRE.md` e, no início do próximo turno do personagem, o hook de início de turno ([adaptador](../provedores/adaptador.md)) entrega só as seções que mudaram, marcadas como atualização da camada do Mestre. Sem hook de início de turno (monitoramento reduzido), a atualização vai no começo da próxima mensagem que o motor entregar. Nunca no meio de um turno.

### Skills por personagem
- Claude Code: o motor passa `--add-dir .mestre/agents/<id>`; o Claude carrega o `.claude/skills/` dessa pasta.
- Codex e Antigravity: não têm pasta de skills por personagem. O conteúdo das skills do nível e da classe ou ficha vai como **seção compacta na camada do Mestre do próprio personagem** (`MESTRE.md`, entregue pelo `developer_instructions` no Codex e pelo arquivo de agente `.agents/agents/mestre-<id>.md` no Antigravity), nunca na pasta compartilhada `.agents/skills/`, que só tem a skill de comunicação ([mesa](mesa.md)).
- Assim cada agente só recebe as skills do seu nível, nos três provedores. Mesmo que outro agente lesse um arquivo por personagem, não ganharia permissão: o motor confere o nível em toda chamada ([convencoes](../cli/convencoes.md)).

### Variáveis de ambiente
| Variável | Valor | Dono do assunto |
|---|---|---|
| `MESTRE_AGENT_ID` | id do personagem | [convencoes](../cli/convencoes.md) |
| `MESTRE_ENDPOINT` | endereço do motor local | [convencoes](../cli/convencoes.md) |
| `MESTRE_CLI` | caminho absoluto do binário `mestre` | [convencoes](../cli/convencoes.md) |
| `MESTRE_WORKSPACE` | slug do workspace | [workspaces](../orquestracao/workspaces.md) |
| `MESTRE_MESA` | identificador da mesa, `<número>-<slug>` (vazio no Grão-Mestre) | [mesas](../orquestracao/mesas.md#abertura) |
| `GIT_CONFIG_GLOBAL` | `.mestre/agents/<id>/gitconfig` (autor = bot do App da classe ou do App "Mestre") | [identidades-e-apps](../github/identidades-e-apps.md) |
| `CLAUDE_CONFIG_DIR`, `CODEX_HOME` ou `HOME` (Antigravity) | pasta do perfil de conta da ficha | [bases-e-fichas](../orquestracao/bases-e-fichas.md) |
| variáveis da base | como definidas na base | [bases-e-fichas](../orquestracao/bases-e-fichas.md) |

A identidade git vem **só** do `gitconfig` do personagem: o motor nunca põe `GIT_AUTHOR_*` nem `GIT_COMMITTER_*` no ambiente (elas venceriam o arquivo e não mudariam com o processo vivo), e a remove se a base tentar defini-la. Nenhuma variável leva token do GitHub, chave do cofre ou credencial de usuário; a única exceção de segredo é a chave de API do provedor, quando o perfil de conta usa uma ([cofre](../plataforma/cofre.md)). Terminais de `spawn` recebem só `MESTRE_WORKSPACE`, `MESTRE_MESA` e as variáveis da base.

### Modelos versionados e regeneração
- Os modelos do `MESTRE.md` por nível, das skills e dos arquivos de hook ficam no Core, versionados com o código do Mestre, com um número de versão. Cada personagem registra a versão com que nasceu; `.mestre/modelos/VERSAO` guarda a versão em uso no workspace ([workspace](workspace.md)).
- Personagens novos nascem sempre com a versão atual.
- Mudou a versão: o painel marca as mesas com personagens em versão antiga e oferece **Regenerar** (Administrador ou acima). A regeneração refaz os arquivos do worktree ([mesa](mesa.md)) e de cada personagem; os vivos recebem o conteúdo novo pela atualização do início do próximo turno (no Claude Code, o prompt de sistema completo só muda num reinício do personagem, feito quando ele estiver ocioso).

### Ao encerrar
Quando o personagem é encerrado de vez (estado final em [personagens](../orquestracao/personagens.md#estados), não um reinício com retomada), o motor do nó apaga `.mestre/agents/<id>/` e, no Antigravity, `.agents/agents/mestre-<id>.md`. Esses arquivos estão na exclusão local do git desde a abertura da mesa ([mesa](mesa.md#gitinfoexclude)). Um reinício com retomada mantém os arquivos (e os regenera, se a classe ou os decretos mudaram).

## Regras e erros

- O motor nunca escreve a camada do Mestre no `AGENTS.md`, no `CLAUDE.md` nem em arquivos globais do usuário.
- Perfil de conta da ficha inexistente no nó → a criação falha com o motivo ([bases-e-fichas](../orquestracao/bases-e-fichas.md)).
- Se nem a reserva puder ser entregue (o terminal não fica pronto), o personagem fica "travado" e o Mestre da mesa é avisado; ele não recebe tarefa sem a camada do Mestre.
- Ficha com "recebe as skills do Mestre = não": sem skills por personagem e sem a parte de CLI nas regras; o resto do `MESTRE.md` continua.

## Critérios de aceite

- **CA-SZ-PERS-01** · *rápida* — **Dado** qualquer personagem, **quando** o `MESTRE.md` é montado, **então** ele começa pela regra de prioridade exata e as seções seguem a ordem decretos, regras, classe, skills (só Codex e Antigravity), contexto.
- **CA-SZ-PERS-02** · *rápida* — **Dado** um `CLASS.md` de 7.000 caracteres, **quando** o `MESTRE.md` é montado, **então** ele fica abaixo de 8.000 caracteres e aponta para `.mestre/agents/<id>/CLASS.md`, que existe.
- **CA-SZ-PERS-03** · *rápida* — **Dado** uma ficha Codex, **quando** o motor monta o comando de início, **então** há um `-c developer_instructions=` com o conteúdo escapado que, lido como TOML, é igual ao `MESTRE.md`.
- **CA-SZ-PERS-04** · *completa* — **Dado** o canal principal do Antigravity marcado como falho para a versão instalada, **quando** um personagem Antigravity falso inicia, **então** a primeira mensagem que ele recebe é o `MESTRE.md`.
- **CA-SZ-PERS-05** · *completa* — **Dado** um personagem ocioso, **quando** um decreto que o atinge é criado, **então** no início do turno seguinte o hook entrega a seção de decretos atualizada e o `MESTRE.md` em disco já a contém.
- **CA-SZ-PERS-06** · *rápida* — **Dado** o ambiente de qualquer personagem, **quando** é montado, **então** nenhuma variável, exceto a da chave de API do provedor quando o perfil usa uma, contém valor que o detector de segredos reconheça; e não existe nenhuma `GIT_AUTHOR_*` nem `GIT_COMMITTER_*`, mesmo com a base tentando defini-las.
- **CA-SZ-PERS-07** · *completa* — **Dado** um repositório com `AGENTS.md`, **quando** personagens dos três provedores falsos iniciam, **então** o `AGENTS.md` fica idêntico e nenhum arquivo global do usuário é criado ou alterado (MVP-3).
- **CA-SZ-PERS-08** · *completa* — **Dado** os modelos na versão 3 e um personagem nascido na 2, **quando** um personagem novo é criado, **então** ele nasce na 3 e o antigo continua na 2 até alguém regenerar.
- **CA-SZ-PERS-09** · *rápida* — **Dado** um Mestre da mesa e um Dev, ambos Codex, na mesma mesa, **quando** os arquivos dos dois são montados, **então** o `.agents/skills/` do worktree só tem `mestre-comunicacao`, a seção de skills do `MESTRE.md` do Mestre traz a coordenação de mesa e a do Dev não traz.
- **CA-SZ-PERS-10** · *rápida* — **Dado** um personagem com App da classe "Dev", **quando** o ambiente é montado, **então** `GIT_CONFIG_GLOBAL` aponta para `.mestre/agents/<id>/gitconfig`, cujo `user.name` é o bot do App; **quando** a classe troca de App, **então** só o arquivo muda e o próximo commit sai com o autor novo, sem reiniciar o personagem.
- **CA-SZ-PERS-11** · *rápida* — **Dado** um personagem Antigravity encerrado, **quando** o encerramento termina, **então** `.mestre/agents/<id>/` e `.agents/agents/mestre-<id>.md` não existem mais e o `git status` do worktree não mostrou esses arquivos em nenhum momento; num reinício com retomada, eles continuam.

## Depende de

- [Sessão zero da mesa](mesa.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)
- [Precedência](../arquitetura/precedencia.md)
- [Adaptador](../provedores/adaptador.md) · [Conformidade](../provedores/conformidade.md)
- [Classes](../classes/classes.md) · [Permissões](../classes/permissoes.md)
- [Protocolo](../conectividade/protocolo.md)

## Verificar na implementação

- **Verificar na implementação:** o canal principal da camada do Mestre no Codex (`-c developer_instructions` no modo interativo) e no Antigravity (`--agent mestre-<id>`) chega ao modelo. **Padrão:** os canais principais da tabela. **Se falhar:** o canal de reserva da tabela daquele provedor (hook e, depois, primeira mensagem), registrado no banco para aquela versão; detalhes em [codex](../provedores/codex.md) e [antigravity](../provedores/antigravity.md). **Onde:** etapa 2 do [teste de conformidade](../provedores/conformidade.md).
- **Verificar na implementação:** o tempo limite da ferramenta de shell do Codex e do Antigravity fica acima do `ask` bloqueante padrão ([tempos limite](../orquestracao/esperas-e-caixa-de-entrada.md#tempos-limite)); no Antigravity, o payload mostra `WaitMsBeforeAsync`, sinal de que o comando pode passar a rodar em segundo plano. **Padrão:** o `ask` bloqueante padrão funciona nos três. **Se falhar:** a skill daquele provedor ensina `mestre ask --no-wait` seguido do fim do turno, e o motor acorda o personagem com a resposta, como no `wait`. **Onde:** à mão na implementação de cada adaptador, com um `ask` de 3 min, e na suíte pesada (CA-ESP-08).
