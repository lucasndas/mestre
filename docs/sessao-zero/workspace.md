# Sessão zero do workspace

> **Assunto:** o que acontece, uma vez por repositório, quando um workspace é criado: Apps, espelho, análise, `AGENTS.md`, pasta `.mestre/`, versionamento e teste de aprovação de App · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Preparar um repositório do GitHub para o Mestre sem mexer no que é da pessoa: clonar com a identidade certa, gerar a pasta `.mestre/`, as skills e os hooks, entender o projeto e descobrir como o GitHub dele trata as aprovações. O que é um workspace e o ciclo de vida dele: [workspaces](../orquestracao/workspaces.md).

## Comportamento

### Início
Um usuário Dono ou Administrador cria o workspace no painel ([administracao](../painel/administracao.md)) informando: repositório (`dono/nome`), ficha do Grão-Mestre e ficha padrão dos Mestres de mesa.

```mermaid
flowchart TD
  A[Painel: repositório + fichas] --> B{Apps fixos instalados no repo?}
  B -- não --> C[Grão-Mestre de outro workspace ou assistente instala]
  C --> B
  B -- sim --> D[Clona o espelho com o App Grão-Mestre]
  D --> E[Cria o clone principal e .mestre/]
  E --> F[Gera skills, hooks e exclusões no clone principal]
  F --> G[Inicia o Grão-Mestre: workspace pronto]
  G --> H[Grão-Mestre: análise, AGENTS.md, versionar?, teste de aprovação]
```

1. **Apps fixos:** o motor confere se "Grão-Mestre" e "Mestre" estão instalados no repositório. Se não estiverem, o workspace fica `aguardando Apps`: o Grão-Mestre de outro workspace acrescenta o repositório à instalação pela API ou, se for a primeira instalação, o assistente pede o navegador ([criacao-de-apps](../github/criacao-de-apps.md), [identidades-e-apps](../github/identidades-e-apps.md)).
2. **Espelho:** o motor clona o repositório sem área de trabalho na pasta de dados do Core (padrão `workspaces/<slug>/espelho.git`; [persistencia](../arquitetura/persistencia.md)), com um token de instalação do App "Grão-Mestre". Daí sai o **clone principal** (padrão `workspaces/<slug>/principal/`), onde o Grão-Mestre trabalha. Como mesas e nós usam o espelho: [mesas](../orquestracao/mesas.md).
3. **`.mestre/` e arquivos gerados:** o motor cria a estrutura abaixo (sem apagar nada que já exista) e gera, no clone principal, os mesmos arquivos que gera em cada worktree de mesa ([mesa](mesa.md)). Até o usuário responder à pergunta de versionamento, `.mestre/` fica só local.
4. **Grão-Mestre:** o motor inicia o Grão-Mestre (fixo e protegido) e o workspace passa a `pronto`; já é possível abrir mesas. Os passos seguintes correm em paralelo, conduzidos pelo Grão-Mestre como itens da sessão zero, cada um visível no painel como pendente ou feito.

### Análise do projeto e `AGENTS.md`
- O Grão-Mestre analisa o projeto: linguagem, estrutura, como instalar, rodar, testar e fazer build.
- **Sem `AGENTS.md`:** ele propõe um, com o conteúdo do projeto (stack, comandos, convenções), e pede aprovação a um Administrador ou acima, mostrando o texto inteiro. Só depois de aprovado o arquivo é criado, pelo PR da sessão zero (abaixo).
- **Com `AGENTS.md`:** não mexe. Pode sugerir melhorias aos usuários, que aplicam ou não.
- **Com `CLAUDE.md` ou `CLAUDE.local.md`:** avisa que os agentes Claude vão ler esse arquivo no lugar do `AGENTS.md` (os de Codex e Antigravity leem o `AGENTS.md`) e sugere unificar no `AGENTS.md`. O mesmo aviso fica no [checklist](../plataforma/checklist.md). Por que isso não quebra a camada do Mestre: [precedencia](../arquitetura/precedencia.md).

### Estrutura de `.mestre/`
```
.mestre/
├── regras/
│   ├── teto.yaml                    teto de permissões e custo   → teto-do-workspace
│   ├── politica-de-aprovacao.yaml   quando o Grão-Mestre aprova  → aprovacoes/politica
│   └── instrucoes/                  instruções extras dos usuários
│       ├── grao-mestre.md                                         → orquestracao/grao-mestre
│       └── mestre-da-mesa.md                                      → orquestracao/mestre-da-mesa
├── classes/<classe>/                classes do workspace          → classes/classes
├── pergaminhos/                     pergaminhos do canvas do workspace → pergaminhos-e-tomos
├── mesas/<mesa>/
│   ├── classes/<classe>/            classes criadas na mesa
│   └── pergaminhos/                 pergaminhos e tomos da mesa   → pergaminhos-e-tomos
├── modelos/VERSAO                   versão dos modelos do Core em uso (personagem)
├── agents/<id>/                     execução: camada do Mestre e arquivos por personagem
├── anexos/                          execução: anexos recebidos    → anexos-e-transcricao
└── run/                             execução: estado local, cópias de classes nos worktrees
```
- O formato de cada arquivo é do documento indicado à direita ([teto-do-workspace](../classes/teto-do-workspace.md), [politica](../aprovacoes/politica.md), [grao-mestre](../orquestracao/grao-mestre.md), [mestre-da-mesa](../orquestracao/mestre-da-mesa.md), [classes](../classes/classes.md), [pergaminhos-e-tomos](../orquestracao/pergaminhos-e-tomos.md), [anexos-e-transcricao](../telegram/anexos-e-transcricao.md), [personagem](personagem.md)).
- `teto.yaml` e `politica-de-aprovacao.yaml` nascem com o padrão sensato definido nos documentos donos.
- **Arquivos de execução** (`agents/`, `anexos/`, `run/`) nunca vão para o git, qualquer que seja a escolha abaixo.
- A fonte de `.mestre/` é o clone principal no Core; o que cada worktree recebe está em [mesa](mesa.md).

### Versionar ou não `.mestre/`
- O Grão-Mestre pergunta aos usuários (Administrador ou acima) se `.mestre/`, menos os arquivos de execução, deve ser versionado no projeto ou ficar só local.
- **Só local (padrão até a resposta):** `/.mestre/` entra no bloco do Mestre em `.git/info/exclude`; o `.gitignore` do projeto nunca é tocado.
- **Versionar:** o bloco passa a excluir só os arquivos de execução, e o conteúdo entra pelo PR da sessão zero.
- A escolha fica no banco e pode ser trocada depois pelo painel (trocar para "versionar" abre um novo PR; para "só local", o Mestre não apaga nada do repositório, só deixa de propor mudanças).

### PR da sessão zero
O que precisa entrar no repositório (o `AGENTS.md` aprovado e/ou `.mestre/` versionado) vai num único PR: o motor cria a branch `mestre/sessao-zero` a partir da principal, faz o commit com o App "Grão-Mestre" como autor, envia e abre o PR. O merge segue as regras do repositório ([acoes-por-nivel](../github/acoes-por-nivel.md)); como um App não aprova o próprio PR, se houver revisão obrigatória o Grão-Mestre pede a um usuário.

### Teste de aprovação de App
Descobre se a aprovação do App "Grão-Mestre" conta para as revisões obrigatórias deste repositório ([aprovacao-no-github](../github/aprovacao-no-github.md)):
1. Se a branch principal não exige revisão (nem por proteção nem por ruleset), registra `sem_exigencia` e termina.
2. Senão, o motor cria a branch `mestre/teste-aprovacao` com um commit vazio, abre um PR contra a principal com o App "Mestre" e aprova com o App "Grão-Mestre".
3. Lê o estado da revisão do PR (GraphQL `reviewDecision`): `APPROVED` → registra `conta`; senão → `nao_conta`.
4. Fecha o PR sem merge e apaga a branch, sempre, mesmo se um passo falhar.
O resultado aparece em `mestre gh settings show` e no painel; `nao_conta` leva ao plano B do documento dono.

## Regras e erros

- O `AGENTS.md` existente nunca é editado; o `.gitignore` nunca é tocado.
- `.mestre/` já presente no repositório (de outra instalação) é reaproveitado: o motor valida, cria só o que falta e avisa o que estiver inválido; nunca sobrescreve.
- Repositório inacessível ao App "Grão-Mestre" → o workspace fica `erro` com o motivo no painel; nenhum outro token é tentado.
- Falha no clone → nova tentativa com espera crescente; persistindo, alerta aos usuários.
- Itens da sessão zero sem resposta não bloqueiam o workspace; ficam pendentes no painel e o Grão-Mestre lembra no resumo periódico.

## Critérios de aceite

- **CA-SZ-WS-01** · *pesada* — **Dado** um repositório de teste no GitHub (dublê) com `AGENTS.md` e regra de revisão obrigatória, **quando** crio o workspace pelo painel, **então** `.mestre/`, skills e hooks são gerados, o `AGENTS.md` fica idêntico byte a byte, `git status` no clone principal fica limpo, um clone novo feito por outra pessoa não traz nenhum arquivo do Mestre, e o resultado do teste de aprovação aparece no painel (MVP-3).
- **CA-SZ-WS-02** · *completa* — **Dado** um repositório sem os Apps fixos, **quando** o workspace é criado, **então** fica `aguardando Apps`, nenhum clone é feito com outra credencial e, instalados os Apps, segue sozinho.
- **CA-SZ-WS-03** · *completa* — **Dado** um repositório sem `AGENTS.md`, **quando** a sessão zero roda, **então** a proposta chega a um Administrador e nenhum `AGENTS.md` existe antes da aprovação; aprovado, o PR `mestre/sessao-zero` contém o arquivo.
- **CA-SZ-WS-04** · *completa* — **Dado** um repositório com `CLAUDE.md` e `AGENTS.md`, **quando** a sessão zero roda, **então** os usuários recebem o aviso de leitura diferente e o checklist mostra o item.
- **CA-SZ-WS-05** · *completa* — **Dado** a resposta "só local", **quando** a sessão zero termina, **então** `.git/info/exclude` contém `/.mestre/` dentro do bloco do Mestre e o `.gitignore` não mudou.
- **CA-SZ-WS-06** · *completa* — **Dado** um GitHub falso em que a aprovação de App não satisfaz a revisão, **quando** roda o teste, **então** registra `nao_conta`, e o PR e a branch de teste deixam de existir.
- **CA-SZ-WS-07** · *rápida* — **Dado** um `.mestre/` existente com `teto.yaml` próprio, **quando** a estrutura é criada, **então** o `teto.yaml` não muda e só as pastas que faltavam são criadas.

## Depende de

- [workspaces](../orquestracao/workspaces.md) · [mesas](../orquestracao/mesas.md) · [grao-mestre](../orquestracao/grao-mestre.md)
- [identidades-e-apps](../github/identidades-e-apps.md) · [criacao-de-apps](../github/criacao-de-apps.md) · [aprovacao-no-github](../github/aprovacao-no-github.md)
- [mesa](mesa.md) · [personagem](personagem.md) · [persistencia](../arquitetura/persistencia.md)

## Verificar na implementação

- **Verificar na implementação:** `reviewDecision` reflete as revisões obrigatórias de rulesets (não só da proteção clássica) num repositório pessoal. **Padrão:** reflete, e o teste de aprovação usa só ele. **Se falhar:** o teste deixa de confiar só em `reviewDecision`: um merge aprovado pelo App e recusado por falta de revisão registra `nao_conta` direto e leva ao plano B ([aprovação no GitHub](../github/aprovacao-no-github.md)). **Onde:** à mão, na sessão zero do primeiro repositório real com ruleset de revisão.
