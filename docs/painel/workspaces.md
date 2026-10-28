# Campanhas no painel

> **Assunto:** telas de campanhas: lista, criação (com a ficha do Grão-Mestre), etapas da sessão zero e nova tentativa, pausa, pergunta de versionamento do `.mestre/` e comandos de verificação (proposta, edição, confirmação e edição por fora) · **Componente:** Painel · **Atualizado:** 2026-10-28

## Objetivo

Dar ao Senhor da Guilda e aos Administradores o lugar onde uma campanha nasce e onde se acompanha a [sessão zero](../sessao-zero/workspace.md). A tela é só interface: as regras são as de [campanhas](../orquestracao/workspaces.md) e da sessão zero, e o servidor confere o nível e a campanha em toda requisição ([administração](administracao.md)).

## Comportamento

- **Lista** (`/workspaces`, ao vivo pelo tópico `workspaces:all` do `/ws/ui`): cada usuário vê só as campanhas dele; o Senhor da Guilda vê todos. Cada cartão mostra repositório, apelido, estado (em preparação, ativo, pausado), a ficha e o estado do Grão-Mestre, quantas mesas andam e quantos pedidos esperam o usuário, e o pull request da sessão zero; os botões são **Abrir** (a [visão da campanha](visao-do-workspace.md#como-se-chega-e-a-campanha-atual)) e **Conversar**, e o botão **Mais** abre **Pausar campanha** e **Comandos de verificação**, o protótipo desenha o cartão só com **Abrir** e **Conversar**, e o botão **Mais** é a divergência consciente dele: este documento exige que o cartão ofereça também a pausa ([Regras e erros](#regras-e-erros)) e os [comandos de verificação](#comandos-de-verificação), e as duas ações ficam atrás de **Mais** para o cartão manter os dois botões do protótipo à vista. Sem campanha, o vazio diz que é preciso criar um (e, para o Senhor da Guilda sem os Apps do GitHub, que o passo antes é conectá-los: [Primeiros passos](primeira-jornada.md#primeiros-passos)); "Criar campanha" fica no alto da lista, e o endereço `/workspaces?criar=1` abre a lista com o formulário já aberto (é para onde o passo "Criar a primeira campanha" leva).
- **Criar** (Administrador ou acima): repositório `dono/nome`, apelido opcional (imutável) e a **ficha do Grão-Mestre**, escolhida entre as fichas que rodam um agente, não usam a base do Operador do PC e têm base executável em toda máquina não excluída (`characters.GrandMasterSheetProblem`, a mesma regra de quem cria o Grão-Mestre por qualquer caminho). A criação abre a sessão zero; a ficha fica guardada e a etapa `files` inicia o Grão-Mestre com ela. Recusas com o motivo: ficha desconhecida, repositório que já é uma campanha, repositório ou apelido inválidos. Quem cria sem ser o Senhor da Guilda só atua na campanha nova depois que o Senhor da Guilda o põe na lista dele.
- **Etapas da sessão zero** (Apps, espelho, arquivos, análise, teste de aprovação), cada uma pendente, em andamento, esperando (e o quê), feita ou com erro (motivo e quantas vezes). **Tentar de novo** (Administrador) roda a etapa agora, sem esperar o recuo; o resultado aparece na própria lista.
- **Pausar e retomar** (Administrador): pausa as mesas junto; uma campanha em preparação não se pausa.
- **Pergunta do versionamento:** enquanto o `.mestre/` não foi respondido, o cartão traz a pergunta (só local ou versionar no projeto, que abre o pull request da sessão zero); quem não é Administrador vê que ela espera. A resposta fica na auditoria.

### Comandos de verificação

- Pelo botão **Mais** do cartão, **Comandos de verificação** abre o `verificacao.yaml` do clone principal (a proposta do `analysis`, ou o que alguém escreveu), os comandos que ele propõe e a **versão em vigor**, com quem a confirmou e quando.
- Um Administrador edita o texto e **confirma**: o motor confere o formato e os limites (`verification.Store.Confirm`), guarda a versão no banco e grava o mesmo texto no arquivo do clone. Sem comando confirmado nada vale ([sessão zero](../sessao-zero/workspace.md#comandos-de-verificação)). Um Aprovador lê, mas não vê a ação, e a API dele recusa com o nível exigido.
- Um `verificacao.yaml` que é link, pasta, pipe ou grande demais não é lido: a tela diz o motivo, não trava e a confirmação continua valendo (ela grava o arquivo se puder); o motor avisa os Administradores como numa edição por fora. A auditoria guarda o texto com os segredos mascarados.
- **Edição por fora** (CA-SZ-WS-08): se o arquivo difere da versão em vigor, a tela avisa que vale a última confirmada, e o motor avisa os Administradores uma vez para cada conteúdo diferente (`verification_commands.outside_hash`; volta ao conteúdo confirmado ou nova confirmação rearma o aviso).

### Implementação

Código: `internal/panel/routes_workspaces.go` e `types_workspaces.go` (`WorkspaceSource`), `internal/app/workspacehost.go` (o adaptador sobre `workspaces.Service`, a sessão zero e `verification.Store`) e `web/src/screens/workspaces/`. Migração `0055_workspace_panel.sql` (`session_zero.grand_master_sheet`, `verification_commands.outside_hash`).

| Rota | Nível | O que faz |
|---|---|---|
| `GET /api/workspaces` | Observador ou acima | a lista, as fichas possíveis e se o usuário cria |
| `POST /api/workspaces` | Administrador | cria com a ficha do Grão-Mestre |
| `POST /api/workspaces/{slug}/retry`, `/pause`, `/resume`, `/versioning` | Administrador, na campanha dele | tentar de novo, pausar, retomar, responder (`local` ou `versioned`) |
| `GET`, `PUT /api/workspaces/{slug}/verification` | leitura: quem vê a campanha; `PUT`: Administrador | a visão e a confirmação |

Auditoria `panel.workspace.*` (`created`, `retry`, `pause`, `resume`, `versioning`, `verification_confirmed`, esta com o antes e o depois do documento). O que o motor recusa volta com o código da área em `code` (`workspace.unknown_sheet`, `workspace.bad_grand_master_sheet`, `workspace.repo_taken`, `workspace.invalid_slug`, `workspace.not_ready`, `workspace.invalid_document`…), e nada é auditado como feito.

## Regras e erros

- O servidor confere o nível e a campanha em toda requisição; quem não atua numa campanha recebe "não encontrado", como no canvas.
- Criar, tentar de novo, pausar, retomar, responder e confirmar são do Administrador ou acima; o Senhor da Guilda vê e age em todos. Nenhuma dessas ações pede PIN ([lista de ações com PIN](../telegram/usuarios-e-niveis.md#pin-pessoal)).
- O que o motor recusa nunca vira ação feita: nada é gravado e nada vai para a auditoria.
- Sem ficha escolhida (uma campanha criada fora do painel), a etapa `files` para dizendo isso.

## Critérios de aceite

- **CA-WSP-01** · *rápida* — **Dado** um Administrador e uma ficha de agente, **quando** cria a campanha, **então** a sessão zero abre, a ficha fica guardada e a etapa `files` inicia o Grão-Mestre com ela; **dado** uma ficha desconhecida, **então** a criação é recusada e nenhuma campanha nasce.
- **CA-WSP-02** · *rápida* — **Dado** um Aprovador, **quando** chama a API de criar, tentar de novo, responder o versionamento ou confirmar os comandos, **então** recebe a recusa com o nível exigido; **dado** um Administrador de outra campanha, **então** recebe "não encontrado" e o motor não é chamado.
- **CA-WSP-03** · *rápida* — **Dado** um usuário que atua só em `loja`, **quando** abre a lista, **então** vê só `loja`; o Senhor da Guilda vê todos; sem o motor a API responde que não está disponível.

## Depende de

- [Administração](administracao.md)
- [Campanhas](../orquestracao/workspaces.md)
- [Sessão zero da campanha](../sessao-zero/workspace.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
