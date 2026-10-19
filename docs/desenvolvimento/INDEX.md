# Desenvolvimento

> **Assunto:** índice da área de desenvolvimento · **Componente:** Projeto · **Atualizado:** 2026-10-19

| Documento | O que cobre | Quando ler |
|---|---|---|
| [ambiente.md](ambiente.md) | o que instalar para desenvolver (Go, git, golangci-lint e, por marco, provedores, ffmpeg e Node), versões e pinos do `go.mod`, comandos das suítes, ganchos de git, CI e proteção da `main`, jobs da noite, a conta `mestre-agente` da máquina Linux e o Windows Sandbox dos spikes | ao preparar uma máquina; quando faltar uma ferramenta; ao mexer no CI ou no ruleset |
| [convencoes.md](convencoes.md) | layout do código Go e do painel, idioma e nomes no código, estilo, código gerado, Conventional Commits, branches, PRs, versões e definição de pronto | antes do primeiro commit; ao criar pacote, branch ou PR |
| [ligacao-de-producao.md](ligacao-de-producao.md) | o que o `mestre run` ainda recusa, deixa `nil` ou faz com dublê, onde está cada peça, se fecha no M5 (configuração injetada) ou no M7 (instalação real) e o critério que a prova | ao fechar o M5 ou o M7; ao deixar uma peça de produção para depois |
| [ordem-de-implementacao.md](ordem-de-implementacao.md) | marcos M0 a M8 e o M4+ (o fechamento do M4), spikes do M1, "tudo roda nos dois sistemas", o marco de cada documento de funcionalidade, os critérios que fecham em marco posterior e a regra de "Depende de" | antes de planejar o trabalho; ao criar um documento de funcionalidade ou mudar o "Depende de" de um |
