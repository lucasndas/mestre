# Qualidade

> **Assunto:** índice da área de qualidade (testes, dublês e verificação da documentação) · **Componente:** Projeto · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [testes.md](testes.md) | política de testes: suítes rápida, completa e pesada, orçamentos, executor `testrun`, testes afetados, quarentena, regras e mapa MVP-1..14 → testes de ponta a ponta | antes de escrever ou rodar qualquer teste; ao decidir em que suíte um teste entra |
| [dubles.md](dubles.md) | relógio injetável e `testing/synctest`, agente falso por provedor, Telegram, GitHub, rede, PTY e git falsos, lançador em memória, gravações | ao testar código com tempo, provedores, Telegram, GitHub ou rede |
| [projetos-de-treino.md](projetos-de-treino.md) | projetos de treino que fazem o papel do repositório do usuário: modelo `go-basico`, variações, `tarefas.yaml` com teste oculto, `testkit.NewProject` e o sandbox do GitHub | ao escrever teste que precisa de um repositório; ao criar variação ou tarefa |
| [verificacao-de-docs.md](verificacao-de-docs.md) | checagens automáticas da documentação: índices, links, tamanho, cabeçalho, seções, critérios de aceite, nomes e o grafo de "Depende de" pela ordem de implementação | ao criar ou mudar um documento; quando o `TestDocs` falhar |
