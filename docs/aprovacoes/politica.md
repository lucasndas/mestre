# Política de aprovação

> **Assunto:** o que o Grão-Mestre decide no nível dele da cadeia de aprovação e quando o pedido vai às pessoas: tipos de ação, o que o motor confere e como decretos ajustam a política · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Deixar o Grão-Mestre decidir sozinho, no nível dele da [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md), o que é seguro e obrigá-lo a levar às pessoas o resto, por regras escritas num arquivo do workspace que o Dono edita. O motor confere as regras: o Grão-Mestre não consegue aprovar fora da política nem acima do risco que o nível dele permite ([ADR 0024](../decisoes/0024-cadeia-de-aprovacao-por-risco.md)).

## Comportamento

### O Grão-Mestre como aprovador delegado

- É o nível da cadeia logo acima dos Mestres de mesa e abaixo das pessoas, como supervisor: os Mestres de mesa decidem o `baixo` e o `medio` das próprias mesas, com as [condições da mesa](../orquestracao/cadeia-de-aprovacao.md#condições-da-mesa), e ele, o que eles não decidem ([níveis](../orquestracao/cadeia-de-aprovacao.md#a-cadeia)). Age como representante dos usuários, com o nível **aprovador delegado**: decide pedidos de risco até `medio` ([classificação de risco](../classes/classificacao-de-risco.md)), dentro desta política; `alto` só com um decreto `delegation` do Dono ([delegação](delegacao.md)); `fixo` nunca, salvo a regra própria do Operador (abaixo).
- **Não** cria decretos permanentes, **não** amplia o teto do workspace e **não** decide em causa própria (pedido dele ou proposta dele), como nenhum nível ([salvaguardas](../orquestracao/cadeia-de-aprovacao.md#salvaguardas)).
- Toda decisão dele precisa de um motivo escrito, que vai para a auditoria ([fluxo](fluxo.md)).
- **Fora da política** (este é o texto dono da regra; a CLI e os outros documentos linkam aqui): quando a política manda perguntar (tipo `nunca` sem delegação, risco acima do teto do nível sem delegação, condição do motor que falhou, regra de quórum que casa, `exigir_usuarios`), `mestre approval decide` recusa com `human_required` e diz o motivo. O pedido não muda até o Grão-Mestre levá-lo às pessoas com `mestre approval ask` (a resposta "Subir agora" da cadeia) ou o prazo do nível dele acabar, quando sobe sozinho ([fluxo](fluxo.md)). Sintaxe: [CLI do Grão-Mestre](../cli/grao-mestre.md).
- **`mestre gh approve`** só aprova o PR com uma **aprovação vigente** para aquele PR e aquele hash: a do próprio Grão-Mestre, decidida dentro da política, ou a de usuários pelo fluxo. Sem ela, recusa com o mesmo `human_required`.

### Tipos de ação

| Tipo | Exemplo | Grão-Mestre decide? | Nível mínimo das pessoas |
|---|---|---|---|
| `pr` | aprovar o PR de uma mesa | se as condições passarem | Aprovador |
| `merge` | fazer o merge do PR aprovado | se as condições passarem | Aprovador |
| `ponto_de_missao` | etapa que espera aprovação ([missões](../orquestracao/missoes.md)) | se as condições passarem | Aprovador |
| `permissao_de_classe` | comando marcado "pedir aprovação" ([permissões](../classes/permissoes.md)) | se as condições passarem, até `medio`; `alto` só com delegação | Aprovador; `alto`, Administrador |
| `custo_extra` | missão ou mesa em 100% do limite ([limites](../plataforma/limites-de-uso.md)) | se dentro do teto do workspace; acima, nunca, nem por delegação | Aprovador; acima do teto, Administrador |
| `envio_workflows` | enviar branch que altera `.github/workflows/`, por classe com `git.workflows: ask` ([workflows](../github/identidades-e-apps.md#workflows)) | só com delegação | Administrador |
| `configuracao_repositorio` | ruleset, proteção de branch, Apps ([aprovação no GitHub](../github/aprovacao-no-github.md)) | só com delegação do Dono; dentro dela, sem PIN a cada vez | Dono, com PIN |
| `classe_workspace` | criar classe do workspace ([teto](../classes/teto-do-workspace.md)) | só com delegação; nunca a proposta dele | Administrador |
| `acao_destrutiva` | `note delete`, `ritual delete`, `class delete`, `table close --cancel`, sem exceção pontual para aquela ação ([convenções da CLI](../cli/convencoes.md)); alvo = o comando e o objeto | só com delegação | Administrador |
| `proposta_agents_md` | criar o `AGENTS.md` proposto na [sessão zero](../sessao-zero/workspace.md#análise-do-projeto-e-agentsmd-etapa-analysis); o pedido mostra o texto inteiro | nunca: a proposta é dele | Administrador |
| `ativacao_operador` | ativar o [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) para uma tarefa, por um tempo | nunca; só com decreto de delegação do Dono (abaixo) | Dono, com PIN |
| `atualizacao_mestre` (desde o M7) | versão nova do Mestre ([autoatualização](../plataforma/autoatualizacao.md#aprovação)) | nunca | Administrador |
| `entrada_na_guilda` | aceitar uma instalação nova na Guilda; expira em 1 h ([Guilda](../guilda/guilda.md#entrada-na-guilda)) | nunca, nem por decreto | Dono, com PIN |
| `promocao_cidadela` (desde o M7) | promover outra máquina a Cidadela quando a Cidadela não volta; só manual ([Cidadela e Torres](../guilda/cidadela-e-torres.md)) | nunca, nem por decreto | Dono, com PIN |

**Pedidos "Sistema"** (`atualizacao_mestre`, desde o M7): criados pelo motor, não pertencem a um workspace e vão ao Dono e aos Administradores; o Grão-Mestre só recebe um evento informativo e **nunca** aprova. Padrões: quórum 1, prazo de 7 dias, sem PIN. Não aparecem no [arquivo de política](arquivo-de-politica.md) do workspace.

**`ativacao_operador`** é regra fixa: não aparece no arquivo de política, que não consegue mudar quem aprova nem tirar o PIN, e decreto `policy` não o alcança. Só o Dono aprova, pelo botão no Telegram ou no painel. A única forma de o Grão-Mestre aprovar é um decreto `delegation` **do Dono** com alvo `ativacao_operador` ([decretos](../telegram/decretos.md#delegação-da-ativação-do-operador)); fora do escopo, da duração ou da validade desse decreto, `approval decide` recebe `human_required`. **PIN:** a aprovação manual pelo Dono pede o PIN a cada ativação; dentro do decreto, o PIN é o de criá-lo ([decretos](../telegram/decretos.md#delegação-da-ativação-do-operador)). Prazo do pedido: 1 h (padrão).

**Pedidos da Guilda** (`entrada_na_guilda`, `promocao_cidadela`) são regra fixa, mais estrita que a do Operador: não aparecem no arquivo de política, decreto nenhum os alcança (nem `delegation`) e só o **Dono** aprova, pelo botão no Telegram ou no painel, **com PIN a cada vez**. O Grão-Mestre nunca recebe esses pedidos e `approval decide` neles sempre recebe `human_required`; Administradores também não respondem. Não pertencem a um workspace. Quem cria cada um, o prazo e o caminho: [fluxo](fluxo.md#pedidos-da-guilda) (`promocao_cidadela` é desde o M7). **Passar a Cidadela** e **excluir máquina** seguem a mesma regra fixa, mas não são pedidos: são ações diretas do Dono, com PIN a cada vez ([fluxo](fluxo.md#pedidos-da-guilda)).

**Risco e delegação:** o risco de cada tipo está na [classificação de risco](../classes/classificacao-de-risco.md#risco-dos-tipos-de-pedido); "só com delegação" quer dizer risco `alto`: o Grão-Mestre decide só dentro de um decreto `delegation` vigente para aquele tipo e escopo ([delegação](delegacao.md)), e mesmo assim as regras de quórum e `exigir_usuarios` mandam perguntar. Os pedidos chegam às pessoas com o nível mínimo do tipo, ou o do risco se for maior (padrão: `alto` exige Administrador). Os níveis de agentes abaixo do Grão-Mestre não usam esta tabela: o superior decide só `baixo`, e o Mestre da mesa, `baixo` e o `medio` de `permissao_de_classe` da própria mesa que passa nas condições da mesa, conferidas pelo motor ([cadeia](../orquestracao/cadeia-de-aprovacao.md#condições-da-mesa)); os outros tipos `medio` (`pr`, `merge`, `ponto_de_missao`, `custo_extra`) ficam com o Grão-Mestre.

Ampliar o teto do workspace não é aprovação: só por decreto ([decretos](../telegram/decretos.md)). Aprovações de cura seguem a [autocura](../autocura/camadas.md).

### Quem confere o quê

| Conferido pelo **motor** (determinístico) | Conferido pelo **Grão-Mestre** (julgamento) |
|---|---|
| CI verde no hash do pedido | mudança dentro do escopo e dos critérios de aceite da issue |
| revisão concluída por outro personagem, sem pendências | dúvida sobre a intenção do pedido |
| tamanho (arquivos, linhas sem as `linhas_ignoradas`, arquivos apagados) | regra do projeto (`AGENTS.md`) que exija uma pessoa |
| arquivos em áreas sensíveis; palavras sensíveis em linhas adicionadas; comandos sensíveis | apagar dados de forma não coberta pelas regras |
| custo dentro do teto; regra de quórum que exige pessoas | |
| gate de verificação (base integrada e comandos de `verificacao.yaml`, quando o repositório não exige a branch atualizada; sem comando confirmado, o Grão-Mestre não decide e o pedido segue às pessoas; [ações por nível](../github/acoes-por-nivel.md#merge)) | |

Se uma condição do motor falha, a decisão do Grão-Mestre é recusada como em [fora da política](#o-grão-mestre-como-aprovador-delegado), dizendo qual condição falhou. Se o Grão-Mestre tem dúvida, ele pergunta mesmo com tudo verde.

### O que o motor lê do PR

O motor obtém o diff do PR no hash do pedido e confere, sem modelo, o que o [arquivo de política](arquivo-de-politica.md) pede:

- **Tamanho:** `max_linhas` soma adições e remoções **sem** os arquivos que casam `condicoes.linhas_ignoradas` (lockfiles, snapshots, gerados); esses arquivos também ficam fora da busca de palavras. Continuam contando em `max_arquivos` e nas áreas sensíveis, que olham o caminho.
- **Palavras:** `areas_sensiveis.palavras_em_linhas_adicionadas` lista palavras (inteiras, sem diferenciar maiúsculas) que mandam para pessoa quando aparecem numa linha **adicionada**, em qualquer arquivo. Linha removida não dispara: tirar um `DROP` não é perigoso.
- **Rótulo `mestre:humano`:** um PR com esse rótulo nunca é aprovado pelo Grão-Mestre (`human_required`). O rótulo só aperta: sua ausência não libera nada, e nenhuma condição o ignora. É reservado: a mesa e o Grão-Mestre não o tiram ([ações por nível](../github/acoes-por-nivel.md#árvore-do-épico)).
- **Diff incompleto:** se o motor não obteve o diff por inteiro (lista de arquivos truncada pelo GitHub, ou arquivo de texto sem `patch` e fora de `linhas_ignoradas`), ou não conseguiu ler o rótulo, a decisão é `human_required` com o motivo `diff_incompleto`. Nunca falha para baixo.

### Arquivo de política

As regras ficam no arquivo `.mestre/regras/politica-de-aprovacao.yaml` de cada workspace, editado só pelo Dono. Onde fica, conteúdo padrão (condições, áreas e comandos sensíveis, tipos, quórum, prazos da cadeia, ajustes de risco, delegação e resumo) e regras de leitura: [arquivo de política](arquivo-de-politica.md).

### Decretos ajustam a política

Um decreto `policy` pode mudar a política sem editar o arquivo (ex.: "pode aprovar migrações neste workspace" tira `migracoes` das áreas sensíveis; "me pergunte tudo esta semana" troca todos os tipos para `nunca` até o fim da validade). Decretos valem acima do arquivo ([precedência](../arquitetura/precedencia.md), [decretos](../telegram/decretos.md)).

- **Alcance:** um decreto `policy` aperta qualquer tipo, mas só afrouxa o que está no teto do Grão-Mestre (`medio`): condições, áreas e comandos sensíveis, `sozinho` dos tipos `medio`. Não muda o risco de nada, não alcança tipos `alto` (como `acao_destrutiva`, `configuracao_repositorio`, `envio_workflows`) nem `fixo`, e não muda o teto dos níveis. Proposta assim é recusada antes da confirmação, com a explicação: para o Grão-Mestre decidir um tipo `alto`, só um decreto `delegation` do Dono ([delegação](delegacao.md)).

## Regras e erros

- Sem regra que permita, o Grão-Mestre pergunta (padrão seguro); acima do teto do nível dele, o pedido nem lhe é dado a decidir: ele recebe só um evento informativo e o pedido sobe ([cadeia](../orquestracao/cadeia-de-aprovacao.md#subida)).
- PR que altera o próprio arquivo de política, o teto ou as classes cai na área `mestre` e sempre pergunta.
- Usuário abaixo do `nivel_minimo` não recebe o pedido e não consegue responder a ele.

## Critérios de aceite

- **CA-APROV-POL-01** · *rápida* — **Dado** um PR com CI verde, revisão concluída e 10 arquivos fora das áreas sensíveis, **quando** o Grão-Mestre aprova, **então** o motor aceita e registra o motivo.
- **CA-APROV-POL-02** · *rápida* — **Dado** um PR que altera `db/migrations/0003.sql`, **quando** o Grão-Mestre tenta aprovar sozinho, **então** o motor recusa com `human_required` citando a área `migracoes`, nada é aprovado no GitHub, e o pedido só chega aos usuários quando o Grão-Mestre usa `mestre approval ask` ou quando o prazo do nível dele (10 min, relógio falso) acaba, com o mesmo id (MVP-7).
- **CA-APROV-POL-03** · *rápida* — **Dado** a regra de quórum `{tipo: merge, aprovacoes: 2}`, **quando** um merge com tudo verde chega, **então** o pedido vai aos usuários pedindo 2 aprovações.
- **CA-APROV-POL-04** · *rápida* — **Dado** um arquivo de política com erro de sintaxe, **quando** o motor recarrega, **então** a versão anterior continua valendo e o Dono recebe alerta.
- **CA-APROV-POL-05** · *rápida* — **Dado** um decreto ativo "pode aprovar migrações", **quando** chega um PR em `**/migrations/**` com tudo verde, **então** o Grão-Mestre pode aprovar sozinho.
- **CA-APROV-POL-06** · *rápida* — **Dado** um pedido `configuracao_repositorio` e nenhum decreto de delegação, **quando** o Grão-Mestre tenta aprovar, **então** o motor recusa com `human_required`; **dado** um decreto `delegation` do Dono para `configuracao_repositorio` com escopo no repositório do workspace, **quando** o Grão-Mestre aprova um pedido dentro dele, **então** o motor aceita sem pedir PIN e registra o decreto na decisão.
- **CA-APROV-POL-07** · *rápida* — **Dado** um personagem tentando escrever em `.mestre/regras/politica-de-aprovacao.yaml`, **quando** o `mestre hook` avalia, **então** a ação é negada.
- **CA-APROV-POL-08** · *rápida* — **Dado** um `mestre note delete` sem exceção pontual, **quando** o motor avalia, **então** cria um pedido `acao_destrutiva` com o comando e o pergaminho como alvo, só para Administradores e o Dono, e um `approval decide` do Grão-Mestre nele recebe `human_required`.
- **CA-APROV-POL-09** · *rápida* — **Dado** nenhuma aprovação vigente para o PR 12 no hash atual, **quando** o Grão-Mestre chama `mestre gh approve 12`, **então** recebe `human_required` e nenhum review é enviado; **dado** a aprovação de um usuário para esse hash, **então** o review sai.
- **CA-APROV-POL-10** · *rápida* — **Dado** um pedido `ativacao_operador` e nenhum decreto de delegação, **quando** o Grão-Mestre chama `approval decide` ou um Administrador toca Aprovar, **então** os dois são recusados e o pedido segue esperando o Dono; **dado** um arquivo de política com `ativacao_operador: { sozinho: se_condicoes }`, **então** o arquivo é inválido e vale a versão anterior.
- **CA-APROV-POL-11** · *rápida* — **Dado** um pedido `ativacao_operador`, **quando** o Dono aprova pelo botão, **então** o PIN é pedido e, sem ele, nada é ativado; **e dado** um decreto de delegação válido, **quando** o Grão-Mestre aprova um pedido dentro do escopo, **então** nenhum PIN é pedido e o Dono recebe o aviso com **Encerrar agora**.
- **CA-APROV-POL-12** · *rápida* — **Dado** um pedido `entrada_na_guilda` e um decreto `delegation` do Dono para o Grão-Mestre, **quando** o Grão-Mestre chama `approval decide` ou um Administrador toca Aprovar, **então** os dois são recusados; **quando** o Dono toca Aprovar sem PIN, **então** nada muda; **com** o PIN, a máquina entra na Guilda; **dado** um arquivo de política que cita `promocao_cidadela`, **então** o arquivo é inválido.

- **CA-APROV-POL-13** · *rápida* — **Dado** uma proposta de decreto `policy` "pode aprovar ações destrutivas nesta mesa", **quando** o motor confere, **então** ela é recusada antes da confirmação com a indicação do decreto `delegation`; **dado** um decreto `policy` "me pergunte tudo", **então** ele vale também para tipos `alto`.
- **CA-APROV-POL-14** · *rápida* — **Dado** um pedido `envio_workflows` e um decreto `delegation` do Dono para esse tipo na mesa `42-login`, **quando** o Grão-Mestre decide um pedido dessa mesa, **então** o motor aceita; **quando** o pedido é de outra mesa, **então** recebe `human_required`; **dado** também a regra de quórum `{tipo: envio_workflows, aprovacoes: 1}`, **então** o pedido vai às pessoas mesmo dentro da delegação.
- **CA-APROV-POL-15** · *rápida* — **Dado** um pedido `permissao_de_classe` de risco `alto` e a política padrão, **quando** ele chega às pessoas, **então** só Administradores e o Dono o recebem, e um "Aprovar" vindo de um Aprovador é recusado.
- **CA-APROV-POL-16** · *rápida* — **Dado** um arquivo de política com `acao_destrutiva: { sozinho: se_condicoes }`, **quando** o motor carrega, **então** o arquivo é inválido, vale a versão anterior e o Dono recebe o alerta com o motivo.
- **CA-APROV-POL-17** · *rápida* — **Dado** um PR que adiciona `DROP TABLE x` em `scripts/limpa.sh` (fora de `areas_sensiveis`) e outro que só remove essa linha, **quando** o motor avalia as condições, **então** o primeiro sai `human_required` citando a palavra `DROP` e o segundo segue as condições normais.
- **CA-APROV-POL-18** · *rápida* — **Dado** um PR de 40 linhas de código e 3.000 de `package-lock.json`, com `max_linhas: 600`, **quando** o motor conta, **então** conta 40 e o PR passa na condição de tamanho; **dado** o diff do GitHub incompleto, **então** sai `human_required` com o motivo `diff_incompleto`.
- **CA-APROV-POL-19** · *rápida* — **Dado** um PR com tudo verde e o rótulo `mestre:humano`, **quando** o Grão-Mestre tenta aprovar, **então** o motor recusa com `human_required`; **dado** o mesmo PR sem o rótulo, **então** segue as condições normais.
- **CA-APROV-POL-20** · *rápida* — **Dado** um PR que adiciona `config/service-account-prod.json` e outro que adiciona `docs/conta.md`, **quando** o Grão-Mestre tenta aprovar sozinho, **então** o primeiro sai `human_required` citando a área `permissoes_e_segredos` e o segundo segue as condições normais.
- **CA-APROV-POL-21** · *rápida* — **Dado** o arquivo de política de um workspace (o conteúdo padrão, e outro com `expiracao: 6h`, `tipos.custo_extra.expiracao: 3h`, `cadeia.prazo_agente: 3m` e um ajuste de risco), **quando** a cadeia e a classificação o leem, **então** valem os prazos das pessoas por tipo (os tipos sem prazo próprio usam `expiracao`; `permissao_de_classe` mantém 2 h), a seção `cadeia`, os ajustes de risco e as áreas `permissoes_e_segredos` e `mestre`, com a versão (hash) do arquivo; **dado** um arquivo inválido (seção desconhecida, sem `versao`, prazo que não é positivo, tipo desconhecido ou de regra fixa, ajuste ou `mesa_decide` inválidos, dois documentos), ilegível ou que é um link, **então** vale a última versão válida (o conteúdo padrão antes de qualquer uma), nada afrouxa e o Dono é avisado uma vez por conteúdo que falhou.

## Depende de

- [Usuários e níveis](../telegram/usuarios-e-niveis.md) · [Identidades e Apps](../github/identidades-e-apps.md) (diff e rótulo no GitHub) · [Workspaces](../orquestracao/workspaces.md) · [Precedência](../arquitetura/precedencia.md)
- [Permissões das classes](../classes/permissoes.md) · [Teto do workspace](../classes/teto-do-workspace.md)
- [Classificação de risco](../classes/classificacao-de-risco.md) · [Cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md) · [Arquivo de política](arquivo-de-politica.md)
