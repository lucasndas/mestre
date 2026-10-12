Você está num Windows Sandbox descartável (usuário WDAGUtilityAccount, administrador, rede ligada), no repositório do Mestre em C:\mestre, na branch `spike/m1-windows`. Sua tarefa são os **três spikes de Windows do M1**. Tudo o que não for enviado ao GitHub ou copiado para C:\winsandbox\resultados se perde ao fechar o Sandbox.

Leia antes, nesta ordem: `AGENTS.md`; `docs/desenvolvimento/ordem-de-implementacao.md` (seção "Spikes do M1"); `docs/arquitetura/windows.md` (seções da conta `mestre-agente`, do serviço e "Verificar na implementação"); `docs/arquitetura/terminal.md`; `docs/arquitetura/seguranca.md` (contas e isolamento). Não carregue a pasta `docs/` inteira.

Os três spikes (os itens de "Verificar na implementação" de windows.md):
1. **ConPTY com o processo iniciado como `mestre-agente`** a partir de um serviço com conta virtual, na sessão 0: crie a conta local `mestre-agente` (senha aleatória, fora de Administradores, "Negar logon local" e "Negar logon pelo RDP", com "Fazer logon como trabalho em lotes"), um serviço de teste com a conta virtual `NT SERVICE\MestreSpikeNode` e um programa Go que, de dentro do serviço, faz `LogonUser` (lote) + `LoadUserProfile` + `CreateProcessAsUser` com pseudoconsole e roda o agente falso (`go build ./cmd/fakeagent`): confira saída, entrada, redimensionamento e encerramento da árvore de processos (Job Object). Se falhar, teste os planos B do documento (`mestre-launcher` como LocalSystem; `OpenConsole.exe`) e registre qual funcionou.
2. **Privilégios da conta virtual:** conceda pela LSA `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege` à conta virtual, declare-os nos privilégios exigidos do serviço e faça o serviço listar os privilégios do próprio token.
3. **Login dos CLIs no perfil do `mestre-agente`:** instale o `codex` e o `agy` pelos instaladores oficiais para Windows (consulte a documentação oficial de cada um; o `claude` já está instalado), abra pelo serviço um terminal como `mestre-agente` para cada CLI, peça que eu faça o login no navegador quando for preciso (avise claramente e espere), e depois inicie um processo novo, noutra sessão de logon da mesma conta, que confirme que o login foi encontrado. Registre onde cada CLI guardou o login (arquivo, DPAPI, Gerenciador de Credenciais).

Regras:
- Código dos spikes em Go, em `tools/winsandbox/spikes/<numero>-<nome>/` (`package main`, `//go:build windows`), usando `golang.org/x/sys/windows` (pode acrescentar ao go.mod). Não crie código em `internal/` nem altere a documentação: quem atualiza os documentos é o coordenador, a partir do seu relatório.
- Sem `sleep` nos testes do repositório; nos programas de spike, use esperas com prazo e diga quanto esperou.
- Rode também, para registro: `go vet ./...` e `go test ./...` no Windows (o `tools/testrun` exige o golangci-lint, que não está instalado aqui) e anote o resultado.
- Nunca faça push para `main`. Nunca grave senhas ou tokens em arquivos do repositório nem nos resultados.

Entrega (faça mesmo que algum spike falhe):
1. `C:\winsandbox\resultados\relatorio.md`: para cada spike, passou/falhou, a evidência (saídas resumidas), a versão do Windows (`winver`/build), o que aconteceu com os planos B e o que precisa mudar nos documentos (cite o arquivo e o item). Copie também os logs úteis para essa pasta.
2. Commit do código dos spikes na branch `spike/m1-windows` (mensagem em inglês, Conventional Commits, terminando com a linha `Co-Authored-By: Claude <noreply@anthropic.com>`), com o relatório copiado para `tools/winsandbox/resultados-spike.md` só neste commit da branch, e `git push -u origin spike/m1-windows` (o Git vai pedir login do GitHub no navegador: avise e espere).
3. Diga no final o hash do commit e o caminho do relatório.
