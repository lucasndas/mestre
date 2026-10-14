# Sessão zero do workspace

> **Assunto:** o que acontece, uma vez por repositório, quando um workspace é criado: Apps, espelho, análise, `AGENTS.md`, pasta `.mestre/`, versionamento e teste de aprovação de App · **Componente:** Motor · **Atualizado:** 2026-10-14

## Objetivo

Preparar um repositório do GitHub para o Mestre sem mexer no que é da pessoa: clonar com a identidade certa, gerar a pasta `.mestre/`, as skills e os hooks, entender o projeto e descobrir como o GitHub dele trata as aprovações. O que é um workspace e o ciclo de vida dele: [workspaces](../orquestracao/workspaces.md). Durante toda a sessão zero o workspace está `preparing`, e cada passo abaixo é uma das **etapas** registradas ali (`apps`, `mirror`, `files`, `analysis`, `approval_test`); erro numa etapa fica registrado nela ([estados do workspace](../orquestracao/workspaces.md#estados)).

## Comportamento

### Início
Um usuário Dono ou Administrador cria o workspace no painel ([administracao](../painel/administracao.md)) informando: repositório (`dono/nome`), ficha do Grão-Mestre e ficha padrão dos Mestres de mesa.

```mermaid
flowchart TD
  A[Painel: repositório + fichas] --> B{Apps fixos instalados no repo?}
  B -- não --> C[API com a credencial do Dono ou link de instalação ao Dono]
  C --> B
  B -- sim --> D[Clona o espelho com o App Grão-Mestre]
  D --> E[Cria o clone principal e .mestre/]
  E --> F[Gera skills, hooks e exclusões no clone principal]
  F --> G[Inicia o Grão-Mestre]
  G --> H[Grão-Mestre: análise e proposta de AGENTS.md]
  H --> I[Teste de aprovação de App]
  I --> J[workspace active]
```

1. **Apps fixos** (etapa `apps`)**:** o motor confere se "Grão-Mestre" e "Mestre" estão instalados no repositório. Se não estiverem, a etapa `apps` fica esperando: o motor acrescenta o repositório à instalação pela API, com a credencial pessoal do Dono, ou o Dono recebe o link para instalar (primeira instalação) ou marcar o repositório na instalação ([criacao-de-apps](../github/criacao-de-apps.md#repositórios-novos-e-permissões-novas), [identidades-e-apps](../github/identidades-e-apps.md)).
2. **Espelho** (etapa `mirror`)**:** o motor clona o repositório sem cópia de trabalho (`--bare`) na pasta de dados da Cidadela (padrão `workspaces/<slug>/espelho.git`; [persistencia](../arquitetura/persistencia.md)), com um token de instalação do App "Grão-Mestre". Daí sai o **clone principal** (padrão `workspaces/<slug>/principal/`), onde o Grão-Mestre trabalha. Como as mesas e as máquinas da Guilda usam o espelho: [mesas](../orquestracao/mesas.md).
3. **`.mestre/` e arquivos gerados** (etapa `files`)**:** o motor cria a estrutura abaixo (sem apagar nada que já exista) e gera, no clone principal, os mesmos arquivos que gera em cada worktree de mesa ([mesa](mesa.md)). Até o usuário responder à pergunta de versionamento, `.mestre/` fica só local.
4. **Grão-Mestre:** ao fim da etapa `files`, o motor inicia o Grão-Mestre (fixo e protegido); ele já conversa com os usuários, mas mesas só se abrem com o workspace `active`. As etapas seguintes (`analysis` e `approval_test`) são conduzidas pelo Grão-Mestre e pelo motor, cada uma visível no painel como pendente, feita ou com erro; concluída a `approval_test`, o workspace vai para `active`.

### Análise do projeto e `AGENTS.md` (etapa `analysis`)
- O Grão-Mestre analisa o projeto: linguagem, estrutura, como instalar, rodar, testar e fazer build.
- **Sem `AGENTS.md`:** ele propõe um, com o conteúdo do projeto (stack, comandos, convenções), e pede a aprovação do tipo `proposta_agents_md` ([política](../aprovacoes/politica.md#tipos-de-ação)), mostrando o texto inteiro. A etapa `analysis` termina com a proposta enviada; a aprovação fica pendente sem segurar o workspace. Só depois de aprovado o arquivo é criado, pelo PR da sessão zero (abaixo).
- **Com `AGENTS.md`:** não mexe. Pode sugerir melhorias aos usuários, que aplicam ou não.
- **Com `CLAUDE.md` ou `CLAUDE.local.md`:** avisa que os agentes Claude vão ler esse arquivo no lugar do `AGENTS.md` (os de Codex e Antigravity leem o `AGENTS.md`) e sugere unificar no `AGENTS.md`. O mesmo aviso fica no [checklist](../plataforma/checklist.md). Por que isso não quebra a camada do Mestre: [precedencia](../arquitetura/precedencia.md).

### Estrutura de `.mestre/`
```
.mestre/
├── regras/
│   ├── teto.yaml                    teto de permissões e custo   → teto-do-workspace
│   ├── politica-de-aprovacao.yaml   política de aprovação        → arquivo-de-politica
│   └── instrucoes/                  instruções extras dos usuários
│       ├── grao-mestre.md                                         → orquestracao/grao-mestre
│       └── mestre-da-mesa.md                                      → orquestracao/mestre-da-mesa
├── classes/<classe>/                classes do workspace          → classes/classes
├── mesas/<mesa>/
│   ├── classes/<classe>/            classes criadas na mesa
│   └── pergaminhos/                 pergaminhos e tomos da mesa   → pergaminhos-e-tomos
├── modelos/VERSAO                   versão dos modelos da Cidadela em uso (personagem)
├── agents/<id>/                     execução: camada do Mestre e arquivos por personagem
├── anexos/                          execução: anexos recebidos    → anexos-e-transcricao
└── run/                             execução: estado local, cópias de classes nos worktrees
```
- O formato de cada arquivo é do documento indicado à direita ([teto-do-workspace](../classes/teto-do-workspace.md), [politica](../aprovacoes/politica.md), [grao-mestre](../orquestracao/grao-mestre.md), [mestre-da-mesa](../orquestracao/mestre-da-mesa.md), [classes](../classes/classes.md), [pergaminhos-e-tomos](../orquestracao/pergaminhos-e-tomos.md), [anexos-e-transcricao](../telegram/anexos-e-transcricao.md), [personagem](personagem.md)).
- `teto.yaml` e `politica-de-aprovacao.yaml` nascem com o padrão sensato definido nos documentos donos.
- **Arquivos de execução** (`agents/`, `anexos/`, `run/`) nunca vão para o git, qualquer que seja a escolha abaixo.
- A fonte de `.mestre/` é o clone principal na Cidadela; o que cada worktree recebe está em [mesa](mesa.md).

### Versionar ou não `.mestre/`
- O Grão-Mestre pergunta aos usuários (Administrador ou acima) se `.mestre/`, menos os arquivos de execução, deve ser versionado no projeto ou ficar só local.
- **Só local (padrão até a resposta):** `/.mestre/` entra no bloco do Mestre em `.git/info/exclude`; o `.gitignore` do projeto nunca é tocado.
- **Versionar:** o bloco passa a excluir só os arquivos de execução, e o conteúdo entra pelo PR da sessão zero.
- A escolha fica no banco e pode ser trocada depois pelo painel (trocar para "versionar" abre um novo PR; para "só local", o Mestre não apaga nada do repositório, só deixa de propor mudanças).

### PR da sessão zero
O que precisa entrar no repositório (o `AGENTS.md` aprovado e/ou `.mestre/` versionado) vai num único PR: o motor cria a branch `mestre/sessao-zero` a partir da principal, faz o commit com o App "Grão-Mestre" como autor, envia e abre o PR. O merge segue as regras do repositório ([acoes-por-nivel](../github/acoes-por-nivel.md)); como um App não aprova o próprio PR, se houver revisão obrigatória o Grão-Mestre pede a um usuário.

### Teste de aprovação de App (etapa `approval_test`)
O motor descobre se a aprovação do App "Grão-Mestre" conta para as revisões obrigatórias deste repositório. O passo a passo, os resultados (`sem_exigencia`, `conta`, `nao_conta`) e o plano B são de [aprovação no GitHub](../github/aprovacao-no-github.md#teste-na-sessão-zero-o-review-do-app-conta); o resultado aparece em `mestre gh settings show` e no painel. Registrado o resultado, a etapa termina.

## Regras e erros

- O `AGENTS.md` existente nunca é editado; o `.gitignore` nunca é tocado.
- `.mestre/` já presente no repositório (de outra instalação) é reaproveitado: o motor valida, cria só o que falta e avisa o que estiver inválido; nunca sobrescreve.
- Repositório inacessível ao App "Grão-Mestre" → a etapa `mirror` fica com erro e o motivo no painel (o workspace continua `preparing`); nenhum outro token é tentado.
- Falha numa etapa → nova tentativa com espera crescente; persistindo, alerta aos usuários e botão "tentar de novo" no painel.
- Perguntas da sessão zero sem resposta (aprovar o `AGENTS.md`, versionar `.mestre/`) não seguram o workspace em `preparing`; ficam pendentes no painel e o Grão-Mestre lembra no resumo periódico.

## Critérios de aceite

- **CA-SZ-WS-01** · *pesada* — **Dado** um repositório de teste no GitHub (dublê) com `AGENTS.md` e regra de revisão obrigatória, **quando** crio o workspace pelo painel, **então** `.mestre/`, skills e hooks são gerados, o `AGENTS.md` fica idêntico byte a byte, `git status` no clone principal fica limpo, um clone novo feito por outra pessoa não traz nenhum arquivo do Mestre, e o resultado do teste de aprovação aparece no painel (MVP-3).
- **CA-SZ-WS-02** · *completa* — **Dado** um repositório sem os Apps fixos, **quando** o workspace é criado, **então** fica `preparing` na etapa `apps`, nenhum clone é feito com outra credencial e, instalados os Apps, segue sozinho para `mirror`.
- **CA-SZ-WS-03** · *completa* — **Dado** um repositório sem `AGENTS.md`, **quando** a sessão zero roda, **então** a proposta chega aos usuários como aprovação `proposta_agents_md`, o workspace vai para `active` sem esperar a resposta e nenhum `AGENTS.md` existe antes da aprovação; aprovado, o PR `mestre/sessao-zero` contém o arquivo.
- **CA-SZ-WS-04** · *completa* — **Dado** um repositório com `CLAUDE.md` e `AGENTS.md`, **quando** a sessão zero roda, **então** os usuários recebem o aviso de leitura diferente e o checklist mostra o item.
- **CA-SZ-WS-05** · *completa* — **Dado** a resposta "só local", **quando** a sessão zero termina, **então** `.git/info/exclude` contém `/.mestre/` dentro do bloco do Mestre e o `.gitignore` não mudou.
- **CA-SZ-WS-06** · *completa* — **Dado** um GitHub falso em que a aprovação de App não satisfaz a revisão, **quando** a etapa `approval_test` roda, **então** o resultado `nao_conta` é registrado, a etapa termina e o workspace vai para `active`.
- **CA-SZ-WS-07** · *rápida* — **Dado** um `.mestre/` existente com `teto.yaml` próprio, **quando** a estrutura é criada, **então** o `teto.yaml` não muda e só as pastas que faltavam são criadas.

## Depende de

- [Workspaces](../orquestracao/workspaces.md)
- [Persistência](../arquitetura/persistencia.md)
- [Componentes](../arquitetura/componentes.md)
