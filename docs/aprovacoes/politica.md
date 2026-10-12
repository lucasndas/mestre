# Política de aprovação

> **Assunto:** quando o Grão-Mestre aprova sozinho e quando pergunta aos usuários; formato do arquivo de política · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Deixar o Grão-Mestre aprovar sozinho o que é seguro e obrigá-lo a perguntar no resto, por regras escritas num arquivo do workspace que o usuário edita. O motor confere as regras: o Grão-Mestre não consegue aprovar fora da política.

## Comportamento

### O Grão-Mestre como aprovador delegado

- É o aprovador padrão de tudo que pede aprovação. Age como representante dos usuários, com o nível **aprovador delegado**: aprova e nega ações pontuais ([ADR 0008](../decisoes/0008-grao-mestre-aprovador-delegado.md)).
- **Não** cria decretos permanentes, **não** amplia o teto do workspace e **não** aprova uma proposta feita por ele mesmo.
- Toda decisão dele precisa de um motivo escrito, que vai para a auditoria ([fluxo](fluxo.md)).
- **Fora da política** (este é o texto dono da regra; a CLI e os outros documentos linkam aqui): quando a política manda perguntar (tipo `nunca`, condição do motor que falhou, regra de quórum que casa, `exigir_usuarios`), `mestre approval decide` recusa com `human_required` e diz o motivo; o motor não abre pedido sozinho. O Grão-Mestre então leva o pedido aos usuários com `mestre approval ask` ([fluxo](fluxo.md)). Sintaxe: [CLI do Grão-Mestre](../cli/grao-mestre.md).
- **`mestre gh approve`** só aprova o PR com uma **aprovação vigente** para aquele PR e aquele hash: a do próprio Grão-Mestre, decidida dentro da política, ou a de usuários pelo fluxo. Sem ela, recusa com o mesmo `human_required`.

### Tipos de ação

| Tipo | Exemplo | Grão-Mestre sozinho? | Nível mínimo quando pergunta |
|---|---|---|---|
| `pr` | aprovar o PR de uma mesa | se as condições passarem | Aprovador |
| `merge` | fazer o merge do PR aprovado | se as condições passarem | Aprovador |
| `ponto_de_missao` | etapa que espera aprovação ([missões](../orquestracao/missoes.md)) | se as condições passarem | Aprovador |
| `permissao_de_classe` | comando marcado "pedir aprovação" ([permissões](../classes/permissoes.md)) | se as condições passarem | Aprovador |
| `custo_extra` | missão ou mesa em 100% do limite ([limites](../plataforma/limites-de-uso.md)) | se dentro do teto do workspace | Aprovador; acima do teto, Administrador |
| `envio_workflows` | enviar branch que altera `.github/workflows/`, por classe com `git.workflows: ask` ([workflows](../github/identidades-e-apps.md#workflows)) | nunca | Administrador |
| `configuracao_repositorio` | ruleset, proteção de branch, Apps ([aprovação no GitHub](../github/aprovacao-no-github.md)) | nunca | Dono, com PIN |
| `classe_workspace` | criar classe do workspace ([teto](../classes/teto-do-workspace.md)) | nunca | Administrador |
| `acao_destrutiva` | `note delete`, `ritual delete`, `class delete`, `table close --cancel`, sem exceção pontual para aquela ação ([convenções da CLI](../cli/convencoes.md)); alvo = o comando e o objeto | nunca | Administrador |
| `proposta_agents_md` | criar o `AGENTS.md` proposto na [sessão zero](../sessao-zero/workspace.md#análise-do-projeto-e-agentsmd-etapa-analysis); o pedido mostra o texto inteiro | nunca | Administrador |
| `ativacao_operador` | ativar o [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) para uma tarefa, por um tempo | nunca; só com decreto de delegação do Dono (abaixo) | Dono, com PIN |
| `atualizacao_mestre` | versão nova do Mestre ([autoatualização](../plataforma/autoatualizacao.md#aprovação)) | nunca | Administrador |
| `atualizacao_provedor` | versão nova de um CLI de provedor ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md#política-por-provedor)) | nunca | Administrador |

**Pedidos "Sistema"** (`atualizacao_mestre`, `atualizacao_provedor`): criados pelo motor, não pertencem a um workspace e vão ao Dono e aos Administradores; o Grão-Mestre só recebe um evento informativo e **nunca** aprova. Padrões: quórum 1, prazo de 7 dias, sem PIN. Não aparecem no arquivo de política do workspace (abaixo).

**`ativacao_operador`** é regra fixa: não aparece no arquivo de política, que não consegue mudar quem aprova nem tirar o PIN, e decreto `policy` não o alcança. Só o Dono aprova, pelo botão no Telegram ou no painel. A única forma de o Grão-Mestre aprovar é um decreto `delegation` **do Dono** com alvo `ativacao_operador` ([decretos](../telegram/decretos.md#delegação-da-ativação-do-operador)); fora do escopo, da duração ou da validade desse decreto, `approval decide` recebe `human_required`. Prazo do pedido: 1 h (padrão).

Ampliar o teto do workspace não é aprovação: só por decreto ([decretos](../telegram/decretos.md)). Aprovações de cura seguem a [autocura](../autocura/camadas.md).

### Quem confere o quê

| Conferido pelo **motor** (determinístico) | Conferido pelo **Grão-Mestre** (julgamento) |
|---|---|
| CI verde no hash do pedido | mudança dentro do escopo e dos critérios de aceite da issue |
| revisão concluída por outro personagem, sem pendências | dúvida sobre a intenção do pedido |
| tamanho (arquivos, linhas, arquivos apagados) | regra do projeto (`AGENTS.md`) que exija uma pessoa |
| arquivos em áreas sensíveis; comandos sensíveis | apagar dados de forma não coberta pelas regras |
| custo dentro do teto; regra de quórum que exige pessoas | |

Se uma condição do motor falha, a decisão do Grão-Mestre é recusada como em [fora da política](#o-grão-mestre-como-aprovador-delegado), dizendo qual condição falhou. Se o Grão-Mestre tem dúvida, ele pergunta mesmo com tudo verde.

### Arquivo de política

- Fica em `.mestre/regras/politica-de-aprovacao.yaml`, no clone principal do workspace no Core. É criado na [sessão zero](../sessao-zero/workspace.md) com o conteúdo padrão abaixo.
- Editado só pelo Dono ([tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)), no painel ([administração](../painel/administracao.md)) ou direto no arquivo. Nenhum agente pode editá-lo: é regra fixa do Mestre.
- O motor valida ao carregar. Arquivo inválido: continua valendo a última versão válida e o Dono recebe alerta com o erro.

Conteúdo padrão (é também o exemplo do formato):

```yaml
versao: 1
expiracao: 24h                 # prazo padrão de um pedido (ver fluxo.md)

condicoes:                     # o motor confere antes de aceitar a aprovação do Grão-Mestre
  ci_verde: true
  revisao_por_outro_personagem: true
  max_arquivos: 25
  max_linhas: 600              # adições + remoções
  max_arquivos_apagados: 5
  dentro_do_teto_de_custo: true

areas_sensiveis:               # caminhos (glob); tocou, pergunta
  autenticacao: ["**/auth/**", "**/*login*", "**/*session*"]
  pagamentos: ["**/payment*/**", "**/billing/**", "**/checkout/**"]
  migracoes: ["**/migrations/**", "**/*.sql"]
  infraestrutura: ["infra/**", "terraform/**", "k8s/**", "**/Dockerfile", "**/docker-compose*.yml", ".github/**"]
  permissoes_e_segredos: ["**/.env*", "**/secrets/**", "**/*permission*", "**/*.pem", "**/*.key"]
  mestre: [".mestre/**", "AGENTS.md", "CLAUDE.md"]

comandos_sensiveis:            # para permissao_de_classe; casou, pergunta
  - "rm -rf *"
  - "git push*"
  - "*DROP TABLE*"
  - "*TRUNCATE*"
  - "*DELETE FROM*"

tipos:                         # sozinho: se_condicoes | nunca
  pr: { sozinho: se_condicoes, nivel_minimo: aprovador }
  merge: { sozinho: se_condicoes, nivel_minimo: aprovador }
  ponto_de_missao: { sozinho: se_condicoes, nivel_minimo: aprovador }
  permissao_de_classe: { sozinho: se_condicoes, nivel_minimo: aprovador, expiracao: 2h }
  custo_extra: { sozinho: se_condicoes, nivel_minimo: aprovador }
  envio_workflows: { sozinho: nunca, nivel_minimo: administrador }
  configuracao_repositorio: { sozinho: nunca, nivel_minimo: dono, pin: true }
  classe_workspace: { sozinho: nunca, nivel_minimo: administrador }
  acao_destrutiva: { sozinho: nunca, nivel_minimo: administrador }
  proposta_agents_md: { sozinho: nunca, nivel_minimo: administrador }

quorum:                        # quando o pedido vai aos usuários
  padrao: { aprovacoes: 1 }
  regras: []                   # ex.: { tipo: merge, mesa: "epico/42-login", aprovacoes: 2, exigir_usuarios: [lucas] }

resumo:                        # resumo do que o Grão-Mestre aprovou sozinho
  frequencia: diario           # diario | semanal | desligado
  horario: "18:00"
  para: [dono]
```

Regras de leitura:

- `quorum.regras`: cada regra casa por `tipo` e/ou `mesa` (aceita `*`). Uma regra que casa com o pedido **obriga a perguntar** aos usuários, mesmo que as condições passem. Se várias casam, vale a mais exigente.
- `exigir_usuarios` lista usuários do cadastro ([usuários e níveis](../telegram/usuarios-e-niveis.md)) cuja aprovação é obrigatória.
- Campos ausentes assumem o valor do conteúdo padrão. Um `tipo` desconhecido torna o arquivo inválido.
- O comportamento de quórum, expiração e resumo está em [fluxo](fluxo.md).

### Decretos ajustam a política

Um decreto pode mudar a política sem editar o arquivo (ex.: "pode aprovar migrações neste workspace" tira `migracoes` das áreas sensíveis; "me pergunte tudo esta semana" troca todos os tipos para `nunca` até o fim da validade). Decretos valem acima do arquivo ([precedência](../arquitetura/precedencia.md), [decretos](../telegram/decretos.md)).

## Regras e erros

- Sem regra que permita, o Grão-Mestre pergunta (padrão seguro).
- PR que altera o próprio arquivo de política, o teto ou as classes cai na área `mestre` e sempre pergunta.
- Usuário abaixo do `nivel_minimo` não recebe o pedido e não consegue responder a ele.

## Critérios de aceite

- **CA-APROV-POL-01** · *rápida* — **Dado** um PR com CI verde, revisão concluída e 10 arquivos fora das áreas sensíveis, **quando** o Grão-Mestre aprova, **então** o motor aceita e registra o motivo.
- **CA-APROV-POL-02** · *rápida* — **Dado** um PR que altera `db/migrations/0003.sql`, **quando** o Grão-Mestre tenta aprovar sozinho, **então** o motor recusa com `human_required` citando a área `migracoes`, nada é aprovado no GitHub, e o pedido só chega aos usuários quando o Grão-Mestre usa `mestre approval ask` (MVP-7).
- **CA-APROV-POL-03** · *rápida* — **Dado** a regra de quórum `{tipo: merge, aprovacoes: 2}`, **quando** um merge com tudo verde chega, **então** o pedido vai aos usuários pedindo 2 aprovações.
- **CA-APROV-POL-04** · *rápida* — **Dado** um arquivo de política com erro de sintaxe, **quando** o motor recarrega, **então** a versão anterior continua valendo e o Dono recebe alerta.
- **CA-APROV-POL-05** · *rápida* — **Dado** um decreto ativo "pode aprovar migrações", **quando** chega um PR em `**/migrations/**` com tudo verde, **então** o Grão-Mestre pode aprovar sozinho.
- **CA-APROV-POL-06** · *rápida* — **Dado** um pedido `configuracao_repositorio`, **quando** o Grão-Mestre tenta aprovar, **então** o motor recusa sempre.
- **CA-APROV-POL-07** · *rápida* — **Dado** um personagem tentando escrever em `.mestre/regras/politica-de-aprovacao.yaml`, **quando** o `mestre hook` avalia, **então** a ação é negada.
- **CA-APROV-POL-08** · *rápida* — **Dado** um `mestre note delete` sem exceção pontual, **quando** o motor avalia, **então** cria um pedido `acao_destrutiva` com o comando e o pergaminho como alvo, só para Administradores e o Dono, e um `approval decide` do Grão-Mestre nele recebe `human_required`.
- **CA-APROV-POL-09** · *rápida* — **Dado** nenhuma aprovação vigente para o PR 12 no hash atual, **quando** o Grão-Mestre chama `mestre gh approve 12`, **então** recebe `human_required` e nenhum review é enviado; **dado** a aprovação de um usuário para esse hash, **então** o review sai.
- **CA-APROV-POL-10** · *rápida* — **Dado** um pedido `ativacao_operador` e nenhum decreto de delegação, **quando** o Grão-Mestre chama `approval decide` ou um Administrador toca Aprovar, **então** os dois são recusados e o pedido segue esperando o Dono; **dado** um arquivo de política com `ativacao_operador: { sozinho: se_condicoes }`, **então** o arquivo é inválido e vale a versão anterior.

## Depende de

- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Workspaces](../orquestracao/workspaces.md)
- [Permissões das classes](../classes/permissoes.md)
- [Teto do workspace](../classes/teto-do-workspace.md)
- [Precedência](../arquitetura/precedencia.md)
