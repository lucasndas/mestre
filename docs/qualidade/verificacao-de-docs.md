# Verificação de docs

> **Assunto:** checagens automáticas da documentação (índices, links, tamanho, cabeçalho, seções, critérios de aceite, grafo de "Depende de", auditoria dos marcos, cobertura dos critérios pelos testes, comentários de marco velhos e pinos do `go.mod`) na suíte rápida · **Componente:** Projeto · **Atualizado:** 2026-10-18

## Objetivo

Garantir por máquina as regras de estrutura da documentação: nenhum documento perdido fora dos índices, nenhum link quebrado, nenhum documento grande demais, todo documento com cabeçalho e critérios de aceite num formato que se pode ler por programa, uma ordem de implementação sem ciclos, e os documentos de acordo com o código (critérios com teste, marcos auditados, comentários e versões em dia). Roda na suíte rápida ([testes.md](testes.md)), então quebra antes do commit.

## Comportamento

### Onde roda

- Ferramenta Go `tools/docscheck`. O teste `TestDocs` (sem build tag, portanto da rápida) roda a verificação sobre `docs/`, `AGENTS.md` e `README.md` do repositório.
- Também roda sozinha: `go run ./tools/docscheck`.
- Lê os arquivos por `fs.FS` (documentos, comentários dos `.go` e o `go.mod`): no teste real, a pasta do repositório; nos testes da própria ferramenta, árvores pequenas em memória (`fstest.MapFS`), sem disco.
- Mostra **todos** os erros de uma vez, um por linha: `<arquivo>:<linha>: <checagem>: <mensagem>`. Avisos vêm com o prefixo `aviso:` e não falham.
- Orçamento padrão: a árvore inteira em menos de 1 s.

### As quatro checagens da política de documentação

1. **Todo documento está num índice.** Cada `.md` de `docs/` aparece no `INDEX.md` da própria pasta. Cada subpasta aparece no `INDEX.md` da pasta pai, pelo link para o `INDEX.md` dela. O `docs/INDEX.md` é a raiz.
2. **Todo item de índice existe.** Cada linha da tabela de um `INDEX.md` aponta para um arquivo que existe.
3. **Nenhum link quebrado.** Todo link relativo em `docs/`, `AGENTS.md` e `README.md` aponta para um arquivo que existe; se tiver `#âncora`, o título existe no destino. A âncora segue a regra do GitHub: minúsculas, letras acentuadas mantidas, pontuação removida (menos `-` e `_`), espaços viram `-`, repetidas ganham `-1`, `-2`. Links dentro de blocos de código e de código em linha são ignorados. Links `http(s)` externos não são acessados (a rápida não usa rede).
4. **Nenhum documento passa do limite.** Mais de **200 linhas** falha; mais de **150** gera aviso para dividir.

### Checagens de formato

5. **Cabeçalho obrigatório** em todo documento, inclusive `INDEX.md`:
   - linha 1: `# <Título>`; linha 2: vazia;
   - linha 3: `> **Assunto:** <texto> · **Componente:** <valor> · **Atualizado:** <AAAA-MM-DD>`;
   - componente: `Motor`, `Guardião`, `CLI`, `Painel`, `Todos` ou `Projeto`;
   - data válida e não futura, comparada com o dia de hoje em UTC, qualquer que seja o fuso da máquina (a ferramenta recebe o relógio injetável, ver [dubles.md](dubles.md)).
6. **Seções na ordem certa:**
   - *documento de funcionalidade* (todo documento com a seção Objetivo): as seções de nível 2 `Objetivo`, `Comportamento`, `Regras e erros`, `Critérios de aceite`, `Depende de` e, opcionais no fim, `Verificar na implementação`, `A confirmar` e `Fontes`;
   - cada item da seção `Verificar na implementação` (em qualquer documento) começa com `**Verificar na implementação:**` e traz, no próprio item, os três marcadores `**Padrão:**` (o que se implementa até lá), `**Se falhar:**` (o plano concreto) e `**Onde:**` (em que teste ou etapa é verificado);
   - *ADR* (`docs/decisoes/`): `Status`, `Contexto`, `Opções consideradas`, `Decisão`, `Consequências` e, opcional, `Fontes`;
   - *índice*: depois do cabeçalho, só a tabela com as colunas `| Documento | O que cobre | Quando ler |`; a raiz `docs/INDEX.md` pode ter parágrafos de orientação antes da tabela, que usa a primeira coluna `Área`;
   - os guias de `docs/operacao/` só precisam do cabeçalho.
7. **Critérios de aceite** (cada item da seção `Critérios de aceite`):
   - formato: `- **CA-<SIGLA>-<NN>** · *<suíte>* — **Dado** …, **quando** …, **então** ….`;
   - `SIGLA`: maiúsculas e dígitos, podendo ter hífen (ex.: `CLI-COM`); `NN`: dois dígitos; o identificador é lido como `CA-` + sigla + `-` + os dois últimos dígitos;
   - suíte: `rápida`, `completa` ou `pesada`;
   - um documento usa uma sigla só, e nenhuma sigla aparece em dois documentos;
   - `NN` não se repete no documento (pode haver buracos: critério removido não renumera os outros);
   - critério *pesada* precisa citar um critério global, `(MVP-1)` a `(MVP-16)` (sozinho ou numa lista, como `(MVP-2, MVP-13)`), porque a suíte pesada só existe para eles ([testes.md](testes.md)): os testes de falha da autocura citam `(MVP-2)`, os de conformidade com provedores citam `(MVP-6)`, os que exigem instalar serviços citam `(MVP-1)` ou `(MVP-13)` e os de troca de Cidadela citam `(MVP-15)`.
8. **Nomes de arquivo:** pastas e arquivos de `docs/` só com minúsculas sem acento, dígitos e hífen, terminando em `.md` (os ADRs começam com quatro dígitos). Nomes de relatório são recusados: `SUMMARY.md`, `IMPLEMENTATION_NOTES.md`, `CHANGES.md`, `PROGRESS.md` e os equivalentes `resumo.md`, `relatorio.md`, `progresso.md`, `mudancas.md` (lista padrão, em qualquer pasta, menos nas pastas que o `go` ignora: nome começando por `.` ou `_`, como `.git` e `.claude/` com os checkouts de agentes, e `node_modules`).
9. **Glossário legível:** a tabela de [glossario.md](../produto/glossario.md) é lida pelo mesmo leitor que gera a ajuda dos termos no painel e no Telegram, que confere o formato definido lá (três colunas, tamanho da ajuda curta, link de detalhes válido); se ele não conseguir ler, falha.

### Checagem da ordem de implementação

10. **"Depende de" só para trás e sem ciclos.** A ferramenta lê o [mapa de documentos por marco e etapa](../desenvolvimento/ordem-de-implementacao.md#mapa-de-documentos-por-marco-e-etapa) e confere:
    - todo documento de funcionalidade está no mapa, uma vez só, e todo documento do mapa é de funcionalidade;
    - cada link da seção "Depende de" que aponta para um documento de funcionalidade aponta para um do **mesmo marco ou de marco anterior**; links para documentos de referência (sem a seção Objetivo) são aceitos e não entram no grafo;
    - o grafo formado por esses links **não tem ciclos**; num ciclo, o erro mostra o caminho inteiro (`a.md → b.md → a.md`);
    - **a ordem registrada é coerente com as dependências:** para cada link de "Depende de" entre documentos do mesmo marco, a etapa de quem depende é maior que a da dependência; o erro mostra os dois documentos, com o caminho relativo a `docs/`, e as etapas (`orquestracao/missoes.md (M4, etapa 9) depende de orquestracao/mesas.md (M4, etapa 9)`). Assim, mudar uma dependência sem ajustar a etapa faz a verificação falhar.

### Checagens de código e de marcos

Leem, além dos documentos, os **comentários** dos arquivos `.go` (`go/scanner`; ficam de fora as pastas que o `go` ignora, `testdata`, `web` e `node_modules`). **Marcador** é um comentário que começa com `// CA-<SIGLA>-<NN>`; só o marcador num `_test.go` cobre um critério.

11. **Auditoria dos marcos.** A tabela de [marcos](../desenvolvimento/ordem-de-implementacao.md#marcos) tem a coluna `Auditoria`: data `AAAA-MM-DD` válida e não futura, ou vazia ([regra](../desenvolvimento/ordem-de-implementacao.md#como-usar-no-dia-a-dia)). A linha `M4+` (o fechamento do M4, sem documentos no mapa) é um marco entre o M4 e o M5: a ordem é M4, M4+, M5. Falha se um marco tem data e um anterior não (o M5 com data e o M4+ sem), ou se há marcador de critério do marco *n* e algum marco anterior a *n*, o M4+ incluído, está sem data. Os marcadores dos critérios de "Critérios antecipados" da ordem não contam para o marco do documento.
12. **Cobertura dos critérios.** *Marco fechado* é o que tem data de auditoria (um marco em andamento não quebra a suíte, e a data só entra com a cobertura completa); o M4+ não tem documentos, então a data dele não cobra critério novo, e o que ele corrige já é cobrado nos documentos dos marcos fechados. Todo critério de documento de funcionalidade de marco fechado precisa de um marcador em `_test.go`, salvo se for *pesada* ou se a ordem o listar em "Critérios em marcos posteriores" ou "Operador do PC no M7" (um intervalo, "CA-COFRE-10 a CA-COFRE-17", vale para todos). E todo marcador no código precisa nomear um critério que existe em algum documento.
13. **Comentários de marco velhos.** Comentário que diz que algo chega, espera ou fica para um marco já auditado, ou que espera um spike que a tabela de spikes dá por **feito**, falha. Os padrões, estreitos de propósito (`<n>` é o número e `M` é maiúsculo): `arrives|comes|lands` e, em até 40 caracteres da mesma frase, `in|at|with [the] M<n>`; `until|till M<n>`; `waits for|waiting for|pending|deferred to|postponed to|left for M<n>`; `chega|vem|fica|espera|aguarda` (singular ou plural) e, em até 40 caracteres, `no|com o|para o|até o|do M<n>`; `até [o] M<n>`; `waits for|waiting for|until|pending|espera|aguarda [the|o] spike <n>`. `M4+` vale como o M4+, não como o M4: o comentário só falha depois da data do M4+. Frases quebradas em duas linhas de comentário valem como uma. Código de spike (`tools/spikes/`, `tools/winsandbox/spikes/`) fica de fora. Para dizer que algo foi feito, escreva sem esses padrões ("done in M1").
14. **Pinos do `go.mod`.** A seção "Pinos do `go.mod`" do [ambiente](../desenvolvimento/ambiente.md#pinos-do-gomod) é a única fonte da diretiva `go`, da `toolchain` e de módulos fixados; o `go.mod` precisa ter os mesmos valores, e o erro aponta para o ambiente.

O formato conferido vem dos documentos de referência [ordem de implementação](../desenvolvimento/ordem-de-implementacao.md), [glossário](../produto/glossario.md) e [convenções](../desenvolvimento/convencoes.md).

## Regras e erros

- Qualquer erro faz o `TestDocs` falhar e, com ele, a suíte rápida e o commit.
- A verificação não corrige nada sozinha; só aponta o arquivo e a linha.
- O que a máquina não verifica (fonte única, documentação atualizada junto com o código, "ver também" fora de "Depende de") é cobrado na revisão e na definição de pronto de [convencoes.md](../desenvolvimento/convencoes.md).
- Mudou uma regra de formato? Atualize este documento e a ferramenta na mesma alteração.

## Critérios de aceite

- **CA-VERDOCS-01** · *rápida* — **Dado** uma árvore com `docs/a/novo.md` que não aparece em `docs/a/INDEX.md`, **quando** a verificação roda, **então** ela falha citando `docs/a/novo.md`.
- **CA-VERDOCS-02** · *rápida* — **Dado** um `INDEX.md` com uma linha para `sumiu.md`, que não existe, **quando** a verificação roda, **então** ela falha com o arquivo e a linha do índice.
- **CA-VERDOCS-03** · *rápida* — **Dado** um documento com link para `../b/x.md#parte-dois` e o destino sem esse título, **quando** a verificação roda, **então** ela falha com o arquivo e a linha do link; **dado** o mesmo link dentro de um bloco de código, **então** não há erro.
- **CA-VERDOCS-04** · *rápida* — **Dado** documentos de 151 e de 201 linhas, **quando** a verificação roda, **então** o primeiro gera aviso e o segundo faz falhar.
- **CA-VERDOCS-05** · *rápida* — **Dado** um documento com componente `Servidor` ou data futura no cabeçalho, **quando** a verificação roda, **então** ela falha apontando a linha 3.
- **CA-VERDOCS-06** · *rápida* — **Dado** um documento de funcionalidade com a seção `Regras e erros` antes de `Comportamento`, e um item de "Verificar na implementação" sem `**Onde:**`, **quando** a verificação roda, **então** ela falha indicando a ordem esperada e o item incompleto.
- **CA-VERDOCS-07** · *rápida* — **Dado** um critério sem a suíte, outro com sigla diferente no mesmo documento e uma sigla já usada em outro documento, **quando** a verificação roda, **então** ela aponta os três erros de uma vez.
- **CA-VERDOCS-08** · *rápida* — **Dado** um critério *pesada* sem `(MVP-n)`, **quando** a verificação roda, **então** ela falha; **dado** o mesmo critério com `(MVP-4)` ou `(MVP-16)`, **então** passa, e **dado** `(MVP-17)`, falha.
- **CA-VERDOCS-09** · *rápida* — **Dado** um arquivo `docs/a/PROGRESS.md` ou `docs/a/configuração.md`, **quando** a verificação roda, **então** ela falha pelo nome.
- **CA-VERDOCS-10** · *rápida* — **Dado** a pasta `docs/` real do repositório, **quando** a suíte rápida roda, **então** a verificação passa em menos de 1 s (a mais rápida de até três execuções, para a carga dos outros testes em paralelo não contar); com o detector de corridas (`-race`), o limite é multiplicado pelo mesmo fator que o `testrun` aplica aos orçamentos ([testes](testes.md)).
- **CA-VERDOCS-11** · *rápida* — **Dado** uma árvore em que `a.md` depende de `b.md` e `b.md` depende de `a.md`, ambos no M3, **quando** a verificação roda, **então** ela falha mostrando o ciclo `a.md → b.md → a.md`.
- **CA-VERDOCS-12** · *rápida* — **Dado** um documento do M3 cujo "Depende de" aponta para um documento do M5, e um documento de funcionalidade fora do mapa, **quando** a verificação roda, **então** ela aponta os dois erros; **dado** o mesmo documento apontando para `../arquitetura/seguranca.md` (referência), **então** não há erro.
- **CA-VERDOCS-13** · *rápida* — **Dado** um documento na etapa 3 do M4 que passa a depender de outro na etapa 5 do mesmo marco, **quando** a verificação roda, **então** ela falha com os dois nomes e as etapas; ajustando a etapa do primeiro para 6, passa.
- **CA-VERDOCS-14** · *rápida* — **Dado** código com marcador de um critério do M2 e a tabela de marcos com o M1 sem data de auditoria, **quando** a verificação roda, **então** ela falha na linha do M1 citando o marcador; **dado** a data no M1, **então** passa; **dado** o M2 com data e o M1 sem, ou uma data futura ou mal formada, **então** falha.
- **CA-VERDOCS-15** · *rápida* — **Dado** um marco auditado com um critério sem marcador em `_test.go`, **quando** a verificação roda, **então** ela falha na linha do critério; **dado** o marcador num teste, o critério *pesada*, o critério listado em "Critérios em marcos posteriores" (inclusive num intervalo) ou o marco sem data, **então** passa.
- **CA-VERDOCS-16** · *rápida* — **Dado** um comentário `// CA-ZZZ-99` em código e nenhum documento com esse critério, **quando** a verificação roda, **então** ela falha com o arquivo e a linha do marcador.
- **CA-VERDOCS-17** · *rápida* — **Dado** um comentário "arrives in M1" (ou "chega no M1", "until M1", "waits for spike 5") e o M1 auditado (ou o spike 5 feito), **quando** a verificação roda, **então** ela falha na linha do comentário, inclusive quando a frase quebra em duas linhas; **dado** o mesmo texto para um marco sem data, uma frase sem o padrão ou código de spike, **então** passa.
- **CA-VERDOCS-18** · *rápida* — **Dado** um `go.mod` com `go 1.26.0` e o ambiente fixando `1.25.0`, ou o `golang.org/x/sys` numa versão diferente da fixada, **quando** a verificação roda, **então** ela falha na linha do `go.mod` apontando para o ambiente; **dado** os valores iguais, **então** passa.
- **CA-VERDOCS-19** · *rápida* — **Dado** a tabela de marcos com a linha `M4+` entre o M4 e o M5, o M4 auditado e código de um critério do M5, **quando** a verificação roda, **então** ela falha na linha do M4+ citando o marcador; **dado** a data no M4+, **então** passa; **dado** o M5 com data e o M4+ sem, ou uma data futura ou mal formada no M4+, **então** falha; **dado** um comentário "until M4+", **então** ele só falha depois da data do M4+, e "until M4" continua falhando com o M4 auditado.

## Depende de

- [testes.md](testes.md) (roda na suíte rápida)
- [dubles.md](dubles.md) (relógio injetável para a data do cabeçalho)
