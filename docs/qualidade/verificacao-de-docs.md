# Verificação de docs

> **Assunto:** checagens automáticas da documentação (índices, links, tamanho, cabeçalho, seções e critérios de aceite) na suíte rápida · **Componente:** Projeto · **Atualizado:** 2026-10-12

## Objetivo

Garantir por máquina as regras de estrutura da documentação: nenhum documento perdido fora dos índices, nenhum link quebrado, nenhum documento grande demais, todo documento com cabeçalho e critérios de aceite num formato que se pode ler por programa. Roda na suíte rápida ([testes.md](testes.md)), então quebra antes do commit.

## Comportamento

### Onde roda

- Ferramenta Go `tools/docscheck`. O teste `TestDocs` (sem build tag, portanto da rápida) roda a verificação sobre `docs/`, `AGENTS.md` e `README.md` do repositório.
- Também roda sozinha: `go run ./tools/docscheck`.
- Lê os arquivos por `fs.FS`: no teste real, a pasta do repositório; nos testes da própria ferramenta, árvores pequenas em memória (`fstest.MapFS`), sem disco.
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
   - componente: `Core`, `Node`, `Guardião`, `CLI`, `Painel`, `Todos` ou `Projeto`;
   - data válida e não futura (a ferramenta recebe o relógio injetável, ver [dubles.md](dubles.md)).
6. **Seções na ordem certa:**
   - *documento de funcionalidade* (todo documento com `## Objetivo`): `## Objetivo`, `## Comportamento`, `## Regras e erros`, `## Critérios de aceite`, `## Depende de` e, opcionais no fim, `## Verificar na implementação` e `## Fontes` (cada item dessa seção traz o que verificar, o **Padrão** que se implementa até lá, o plano **Se falhar** e **Onde** é verificado);
   - *ADR* (`docs/decisoes/`): `## Status`, `## Contexto`, `## Opções consideradas`, `## Decisão`, `## Consequências` e, opcional, `## Fontes`;
   - *índice*: depois do cabeçalho, só a tabela com as colunas `| Documento | O que cobre | Quando ler |`;
   - os guias de `docs/operacao/` só precisam do cabeçalho.
7. **Critérios de aceite** (cada item da seção `## Critérios de aceite`):
   - formato: `- **CA-<SIGLA>-<NN>** · *<suíte>* — **Dado** …, **quando** …, **então** ….`;
   - `SIGLA`: maiúsculas e dígitos, podendo ter hífen (ex.: `CLI-COM`); `NN`: dois dígitos; o identificador é lido como `CA-` + sigla + `-` + os dois últimos dígitos;
   - suíte: `rápida`, `completa` ou `pesada`;
   - um documento usa uma sigla só, e nenhuma sigla aparece em dois documentos;
   - `NN` não se repete no documento (pode haver buracos: critério removido não renumera os outros);
   - critério *pesada* precisa citar um critério global, `(MVP-1)` a `(MVP-12)`, porque a suíte pesada só existe para eles ([testes.md](testes.md)): os testes de falha da autocura citam `(MVP-2)` e os de conformidade com provedores citam `(MVP-6)`.
8. **Nomes de arquivo:** pastas e arquivos de `docs/` só com minúsculas sem acento, dígitos e hífen, terminando em `.md` (os ADRs começam com quatro dígitos). Nomes de relatório são recusados: `SUMMARY.md`, `IMPLEMENTATION_NOTES.md`, `CHANGES.md`, `PROGRESS.md` e os equivalentes `resumo.md`, `relatorio.md`, `progresso.md`, `mudancas.md` (lista padrão, em qualquer pasta).
9. **Glossário legível:** a tabela de [glossario.md](../produto/glossario.md) é lida pelo mesmo leitor que gera a ajuda dos termos no painel e no Telegram, que confere o formato definido lá (três colunas, tamanho da ajuda curta, link de detalhes válido); se ele não conseguir ler, falha.

## Regras e erros

- Qualquer erro faz o `TestDocs` falhar e, com ele, a suíte rápida e o commit.
- A verificação não corrige nada sozinha; só aponta o arquivo e a linha.
- O que a máquina não verifica (fonte única, documentação atualizada junto com o código) é cobrado na revisão e na definição de pronto de [convencoes.md](../desenvolvimento/convencoes.md).
- Mudou uma regra de formato? Atualize este documento e a ferramenta na mesma alteração.

## Critérios de aceite

- **CA-VERDOCS-01** · *rápida* — **Dado** uma árvore com `docs/a/novo.md` que não aparece em `docs/a/INDEX.md`, **quando** a verificação roda, **então** ela falha citando `docs/a/novo.md`.
- **CA-VERDOCS-02** · *rápida* — **Dado** um `INDEX.md` com uma linha para `sumiu.md`, que não existe, **quando** a verificação roda, **então** ela falha com o arquivo e a linha do índice.
- **CA-VERDOCS-03** · *rápida* — **Dado** um documento com link para `../b/x.md#parte-dois` e o destino sem esse título, **quando** a verificação roda, **então** ela falha com o arquivo e a linha do link; **dado** o mesmo link dentro de um bloco de código, **então** não há erro.
- **CA-VERDOCS-04** · *rápida* — **Dado** documentos de 151 e de 201 linhas, **quando** a verificação roda, **então** o primeiro gera aviso e o segundo faz falhar.
- **CA-VERDOCS-05** · *rápida* — **Dado** um documento com componente `Servidor` ou data futura no cabeçalho, **quando** a verificação roda, **então** ela falha apontando a linha 3.
- **CA-VERDOCS-06** · *rápida* — **Dado** um documento de funcionalidade com `## Regras e erros` antes de `## Comportamento`, **quando** a verificação roda, **então** ela falha indicando a ordem esperada.
- **CA-VERDOCS-07** · *rápida* — **Dado** um critério sem a suíte, outro com sigla diferente no mesmo documento e uma sigla já usada em outro documento, **quando** a verificação roda, **então** ela aponta os três erros de uma vez.
- **CA-VERDOCS-08** · *rápida* — **Dado** um critério *pesada* sem `(MVP-n)`, **quando** a verificação roda, **então** ela falha; **dado** o mesmo critério com `(MVP-4)`, **então** passa.
- **CA-VERDOCS-09** · *rápida* — **Dado** um arquivo `docs/a/PROGRESS.md` ou `docs/a/configuração.md`, **quando** a verificação roda, **então** ela falha pelo nome.
- **CA-VERDOCS-10** · *rápida* — **Dado** a pasta `docs/` real do repositório, **quando** a suíte rápida roda, **então** a verificação passa em menos de 1 s.

## Depende de

- [testes.md](testes.md)
- [dubles.md](dubles.md)
- [glossario.md](../produto/glossario.md)
- [convencoes.md](../desenvolvimento/convencoes.md)
