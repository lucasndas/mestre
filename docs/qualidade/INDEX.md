# Qualidade

> **Assunto:** índice da área de qualidade (testes, dublês, regras de código e verificação da documentação) · **Componente:** Projeto · **Atualizado:** 2026-10-13

| Documento | O que cobre | Quando ler |
|---|---|---|
| [testes.md](testes.md) | política de testes: suítes rápida, completa e pesada, orçamentos, executor `testrun`, testes afetados, quarentena, regras, troca de Cidadela e replicação na completa, CI em Linux e Windows e mapa MVP-1..15 → testes de ponta a ponta | antes de escrever ou rodar qualquer teste; ao decidir em que suíte um teste entra |
| [contas-reais.md](contas-reais.md) | os testes que exigem contas reais do sistema (launcher trocando de usuário, logon em lote, conta de serviço virtual): subconjunto da pesada que roda em máquina descartável no CI (`real-accounts.yml`) e no laboratório, o que cobre em cada sistema e o que fica para o M7 | ao escrever ou rodar teste que precisa de conta real; ao mover uma parte de critério do laboratório para o CI |
| [dubles.md](dubles.md) | relógio injetável e `testing/synctest`, agente falso por provedor, Telegram, GitHub, rede, PTY e git falsos, lançador em memória, gravações | ao testar código com tempo, provedores, Telegram, GitHub ou rede |
| [projetos-de-treino.md](projetos-de-treino.md) | projetos de treino que fazem o papel do repositório do usuário: modelo `go-basico`, variações, `tarefas.yaml` com teste oculto, `testkit.NewProject` e o sandbox do GitHub | ao escrever teste que precisa de um repositório; ao criar variação ou tarefa |
| [regras-de-codigo.md](regras-de-codigo.md) | as regras de código que o golangci-lint não cobre e o `codecheck` confere: subtestes paralelos, `select {}`, saída herdada por processos filhos de teste, descritores de segurança `nil` no Windows, pastas `0700`, segredos com receptor ponteiro; a exceção `mestre:allow` | ao mudar uma regra de código; quando o `TestCodeRules` falhar |
| [verificacao-de-docs.md](verificacao-de-docs.md) | checagens automáticas da documentação: índices, links, tamanho, cabeçalho, seções, critérios de aceite, nomes, o grafo de "Depende de" pela ordem de implementação, auditoria dos marcos, cobertura dos critérios pelos testes, comentários de marco velhos e pinos do `go.mod` | ao criar ou mudar um documento; quando o `TestDocs` falhar |
