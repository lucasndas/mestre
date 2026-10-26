# Workspaces no painel

> **Assunto:** telas de workspaces: lista, criação (com a ficha do Grão-Mestre), etapas da sessão zero e nova tentativa, pausa, pergunta de versionamento do `.mestre/` e comandos de verificação (proposta, edição, confirmação e edição por fora) · **Componente:** Painel · **Atualizado:** 2026-10-26

## Objetivo

Dar ao Dono e aos Administradores o lugar onde um workspace nasce e onde se acompanha a [sessão zero](../sessao-zero/workspace.md). A tela é só interface: as regras são as de [workspaces](../orquestracao/workspaces.md) e da sessão zero, e o servidor confere o nível e o workspace em toda requisição ([administração](administracao.md)).

## Comportamento

- **Lista** (`/workspaces`, ao vivo pelo tópico `workspaces:all` do `/ws/ui`): cada usuário vê só os workspaces dele; o Dono vê todos. Cada cartão mostra repositório, apelido, estado (em preparação, ativo, pausado), a ficha e o estado do Grão-Mestre e o pull request da sessão zero.
- **Criar** (Administrador ou acima): repositório `dono/nome`, apelido opcional (imutável) e a **ficha do Grão-Mestre**, escolhida entre as fichas que rodam um agente. A criação abre a sessão zero; a ficha fica guardada e a etapa `files` inicia o Grão-Mestre com ela. Recusas com o motivo: ficha desconhecida, repositório que já é um workspace, repositório ou apelido inválidos. Quem cria sem ser o Dono só atua no workspace novo depois que o Dono o põe na lista dele.
- **Etapas da sessão zero** (Apps, espelho, arquivos, análise, teste de aprovação), cada uma pendente, em andamento, esperando (e o quê), feita ou com erro (motivo e quantas vezes). **Tentar de novo** (Administrador) roda a etapa agora, sem esperar o recuo; o resultado aparece na própria lista.
- **Pausar e retomar** (Administrador): pausa as mesas junto; um workspace em preparação não se pausa.
- **Pergunta do versionamento:** enquanto o `.mestre/` não foi respondido, o cartão traz a pergunta (só local ou versionar no projeto, que abre o pull request da sessão zero); quem não é Administrador vê que ela espera. A resposta fica na auditoria.

### Comandos de verificação

- O cartão abre o `verificacao.yaml` do clone principal (a proposta do `analysis`, ou o que alguém escreveu), os comandos que ele propõe e a **versão em vigor**, com quem a confirmou e quando.
- Um Administrador edita o texto e **confirma**: o motor confere o formato e os limites (`verification.Store.Confirm`), guarda a versão no banco e grava o mesmo texto no arquivo do clone. Sem comando confirmado nada vale ([sessão zero](../sessao-zero/workspace.md#comandos-de-verificação)). Um Aprovador lê, mas não vê a ação, e a API dele recusa com o nível exigido.
- **Edição por fora** (CA-SZ-WS-08): se o arquivo difere da versão em vigor, a tela avisa que vale a última confirmada, e o motor avisa os Administradores uma vez para cada conteúdo diferente (`verification_commands.outside_hash`; volta ao conteúdo confirmado ou nova confirmação rearma o aviso).

### Implementação

Código: `internal/panel/routes_workspaces.go` e `types_workspaces.go` (`WorkspaceSource`), `internal/app/workspacehost.go` (o adaptador sobre `workspaces.Service`, a sessão zero e `verification.Store`) e `web/src/screens/workspaces/`. Migração `0055_workspace_panel.sql` (`session_zero.grand_master_sheet`, `verification_commands.outside_hash`).

| Rota | Nível | O que faz |
|---|---|---|
| `GET /api/workspaces` | Observador ou acima | a lista, as fichas possíveis e se o usuário cria |
| `POST /api/workspaces` | Administrador | cria com a ficha do Grão-Mestre |
| `POST /api/workspaces/{slug}/retry`, `/pause`, `/resume`, `/versioning` | Administrador, no workspace dele | tentar de novo, pausar, retomar, responder (`local` ou `versioned`) |
| `GET`, `PUT /api/workspaces/{slug}/verification` | leitura: quem vê o workspace; `PUT`: Administrador | a visão e a confirmação |

Auditoria `panel.workspace.*` (`created`, `retry`, `pause`, `resume`, `versioning`, `verification_confirmed`, esta com o antes e o depois do documento). O que o motor recusa volta como `refused` com `reason` (`unknown_sheet`, `repo_taken`, `invalid_slug`, `not_ready`, `invalid_document`…), e nada é auditado como feito.

## Regras e erros

- O servidor confere o nível e o workspace em toda requisição; quem não atua num workspace recebe "não encontrado", como no canvas.
- Criar, tentar de novo, pausar, retomar, responder e confirmar são do Administrador ou acima; o Dono vê e age em todos. Nenhuma dessas ações pede PIN ([lista de ações com PIN](../telegram/usuarios-e-niveis.md#pin-pessoal)).
- O que o motor recusa nunca vira ação feita: nada é gravado e nada vai para a auditoria.
- Sem ficha escolhida (um workspace criado fora do painel), a etapa `files` para dizendo isso.

## Critérios de aceite

- **CA-WSP-01** · *rápida* — **Dado** um Administrador e uma ficha de agente, **quando** cria o workspace, **então** a sessão zero abre, a ficha fica guardada e a etapa `files` inicia o Grão-Mestre com ela; **dado** uma ficha desconhecida, **então** a criação é recusada e nenhum workspace nasce.
- **CA-WSP-02** · *rápida* — **Dado** um Aprovador, **quando** chama a API de criar, tentar de novo, responder o versionamento ou confirmar os comandos, **então** recebe a recusa com o nível exigido; **dado** um Administrador de outro workspace, **então** recebe "não encontrado" e o motor não é chamado.
- **CA-WSP-03** · *rápida* — **Dado** um usuário que atua só em `loja`, **quando** abre a lista, **então** vê só `loja`; o Dono vê todos; sem o motor a API responde que não está disponível.

## Depende de

- [Administração](administracao.md)
- [Workspaces](../orquestracao/workspaces.md)
- [Sessão zero do workspace](../sessao-zero/workspace.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
