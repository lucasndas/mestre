# Administração no painel

> **Assunto:** telas de administração: decretos, usuários, bases, fichas, perfis de conta, classes e Apps, teto, política, consumo, saúde dos nós, segredos e diagnóstico · **Componente:** Painel · **Atualizado:** 2026-10-12

## Objetivo

Reunir num só lugar as telas para configurar e acompanhar o Mestre. Cada tela é só a interface: as regras ficam no documento dono, linkado na tabela, e quem pode fazer o quê fica na [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).

## Comportamento

### Telas

| Tela | O que mostra e permite | Dono das regras |
|---|---|---|
| Decretos | decretos ativos e suspensos por workspace, com autor, texto original, validade e o que liberaram; criar regra permanente ou exceção pontual; revogar | [decretos](../telegram/decretos.md) |
| Usuários | lista, cadastro, nível, workspaces, credencial do GitHub do usuário, códigos de pareamento do Telegram e de primeiro acesso, suspender, remover, transferir o posto de Dono | [usuários e níveis](../telegram/usuarios-e-niveis.md), [login](login.md) |
| Bases e fichas | criar, editar, apagar, exportar e importar em YAML ou JSON | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Perfis de conta | perfis por nó e provedor, com estado do login (do checklist) | [bases e fichas](../orquestracao/bases-e-fichas.md) |
| Classes | classes globais, do workspace e das mesas, com o App vinculado e o estado dele (ativo, em criação, erro); propostas de classe do workspace esperando aprovação | [classes](../classes/classes.md), [identidades e Apps](../github/identidades-e-apps.md) |
| Apps do GitHub | Apps fixos e por classe, workspaces em que estão instalados, contagem "N de 100"; para o Dono, os pedidos de criação abertos com o link de criação e o botão "novo link" para os vencidos | [identidades e Apps](../github/identidades-e-apps.md), [criação de Apps](../github/criacao-de-apps.md) |
| Teto do workspace | o teto em vigor; "ampliar" abre a criação de um decreto, nunca edita direto | [teto do workspace](../classes/teto-do-workspace.md) |
| Política de aprovação | editor do arquivo de política com validação antes de salvar | [política](../aprovacoes/politica.md) |
| Instruções extras | texto do usuário para o Grão-Mestre e para os Mestres de mesa de cada workspace | [Grão-Mestre](../orquestracao/grao-mestre.md), [Mestre da mesa](../orquestracao/mestre-da-mesa.md) |
| Workspaces | lista, criar (abre a sessão zero), pausar e retomar | [workspaces](../orquestracao/workspaces.md), [sessão zero do workspace](../sessao-zero/workspace.md) |
| Consumo de uso | gráficos e tabelas por personagem, missão, mesa, workspace, conta e dia; limites configurados e quanto falta | [limites de uso](../plataforma/limites-de-uso.md) |
| Saúde dos nós | ver seção abaixo | [camadas](../autocura/camadas.md), [checklist](../plataforma/checklist.md) |
| Segredos | nomes dos segredos do cofre, tipo, impressão curta e data; trocar e remover | [cofre](../plataforma/cofre.md) |
| Diagnóstico com IA | ficha de diagnóstico de cada máquina | [diagnóstico com IA](../autocura/diagnostico-com-ia.md#ficha-de-diagnóstico) |
| Auditoria | trilha de auditoria com filtros (quem, o quê, quando, workspace) | [persistência](../arquitetura/persistencia.md) |

- Cada usuário só vê os workspaces dele; o Dono vê todos.
- Ações sensíveis pedem PIN ([login](login.md#pin-em-ações-sensíveis)).
- Toda alteração feita aqui entra na auditoria com o usuário e o antes/depois (sem valores de segredos).

### Saúde dos nós

Uma linha por máquina (Core e cada nó), atualizada em tempo real. No Core Windows, a linha do Core mostra os dois serviços do motor (`mestre-core` e o nó local). No [modo máquina única](../autocura/camadas.md#modo-máquina-única), uma faixa fixa avisa que, se a máquina inteira cair, ninguém de fora percebe, e sugere parear um segundo nó.


| Coluna | Conteúdo |
|---|---|
| Estado | conectado, sem sinal (desde quando), revogado; selo do incidente aberto, se houver |
| Motor e Guardião | versão de cada um, protocolo negociado, último heartbeat de cada conexão |
| Capacidades | `os`, `local` (nó local do Core), `gpu`, `transcricao`, ferramentas e versões ([protocolo](../conectividade/protocolo.md)) |
| Carga | CPU, memória, disco livre, terminais rodando |
| Agentes | personagens rodando no nó, com mesa e estado (link para o canvas) |
| Autocura | ações de cura recentes (últimos 7 dias), com resultado ([camadas](../autocura/camadas.md#anti-loop-e-auditoria)) |
| Checklist | alertas abertos, com a correção oferecida ([checklist](../plataforma/checklist.md)) |
| Prioridade | prioridade para a eleição do nó condutor (editável) ([camadas](../autocura/camadas.md#eleição-do-nó-condutor)) |
| Página reserva | endereço da [página reserva](../autocura/pagina-reserva.md) do nó (no modo máquina única, a do Core) |

Ações da tela: gerar convite de nó e revogar a credencial de um nó ([pareamento](../conectividade/pareamento.md)); aplicar uma correção oferecida pelo checklist; pedir ao Guardião as ações da [lista fechada](../autocura/guardiao.md#lista-fechada-de-ações) (status, logs, reiniciar, checklist).

### Contagem de Apps

- O topo da tela de Apps mostra "N de 100 Apps na conta": os Apps do Mestre, contados pelo banco, mais o número de outros Apps da conta, que o Dono informa nessa tela (padrão 0) e atualiza quando quiser. O Mestre não lê a página de Apps do GitHub.
- Com 90 ou mais (padrão), uma faixa de aviso aparece também na tela de classes ([identidades e Apps](../github/identidades-e-apps.md)).

### Exportar e importar

- Bases e fichas: um arquivo por item ou um pacote com vários, em YAML ou JSON; a importação mostra a diferença com o que já existe e pede confirmação para sobrescrever ([bases e fichas](../orquestracao/bases-e-fichas.md)).

## Regras e erros

- O servidor confere o nível em toda requisição; telas sem permissão nem aparecem no menu.
- Salvar um arquivo inválido (política, importação) → nada é gravado; os erros aparecem com a linha.
- Valores de segredos nunca são devolvidos pela API, nem mascarados: só nome, tipo, impressão curta e datas.
- Revogar um nó com personagens rodando → o painel avisa quantos e quais antes de confirmar.

## Critérios de aceite

- **CA-ADM-01** · *rápida* — **Dado** um Administrador, **quando** chama a API de cadastrar usuário, **então** recebe a recusa com o nível exigido.
- **CA-ADM-02** · *rápida* — **Dado** 90 Apps na conta, **quando** abro a tela de classes, **então** a faixa de aviso aparece.
- **CA-ADM-03** · *rápida* — **Dado** um segredo no cofre de teste, **quando** a tela de segredos carrega, **então** a resposta da API não contém o valor nem parte dele.
- **CA-ADM-04** · *rápida* — **Dado** "ampliar teto" com um novo valor, **quando** confirmo, **então** é criado um pedido de decreto (com PIN) e o teto em vigor não muda antes dele.
- **CA-ADM-05** · *rápida* — **Dado** uma ficha exportada em YAML, **quando** importo de volta, **então** a ficha resultante é igual à original.
- **CA-ADM-06** · *completa* — **Dado** Core e nó reais, **quando** paro o serviço do motor do nó, **então** a linha do nó mostra "sem sinal" com o horário, a coluna Autocura mostra a ação do Guardião e, de volta, a coluna Checklist fica em dia (MVP-11).
- **CA-ADM-08** · *rápida* — **Dado** um Core sem nó remoto, **quando** a tela carrega, **então** aparece a faixa do modo máquina única; **dado** um nó pareado, **então** ela some.
- **CA-ADM-07** · *rápida* — **Dado** uma política com erro de formato, **quando** salvo, **então** nada muda e o erro aponta a linha.

## Depende de

- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Login](login.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)
- [Classes](../classes/classes.md)
- [Identidades e Apps](../github/identidades-e-apps.md)
- [Cofre](../plataforma/cofre.md)
- [Limites de uso](../plataforma/limites-de-uso.md)
- [Checklist](../plataforma/checklist.md)
- [Camadas](../autocura/camadas.md)
- [Pareamento](../conectividade/pareamento.md)
