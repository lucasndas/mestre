Você está num Windows Sandbox descartável (usuário WDAGUtilityAccount, administrador, rede ligada), no repositório do Mestre em C:\mestre, na branch `round/m3m4-windows` (criada a partir da `main`). A pasta `C:\winsandbox` é a do Dono (compartilhada com o Windows dele): ali estão os resultados da **rodada de verificações reais** que o `run-all.ps1` já rodou, em `C:\winsandbox\resultados\round\` (`RESULTADO.txt` e `detalhes\`). Esta é a **triagem**: você lê, separa o que é achado real do que é erro do script, conserta o que for do script e roda de novo os itens afetados.

Leia antes, nesta ordem: `AGENTS.md`; a seção "Rodada de verificações reais" de `docs/arquitetura/windows.md`; `tools/winsandbox/round/run-all.ps1`; `RESULTADO.txt` e os arquivos de `detalhes\` que ele cita (`item8-casos.txt`, os logs da conformidade de cada provedor, `instalador-*.log`).

## Tarefa
Para cada item FAIL ou SKIP do `RESULTADO.txt` (e para cada subitem), classifique em uma de quatro causas e diga a evidência:
1. **Erro do script ou do ambiente** (o `run-all.ps1` ou `tools/conformance` falhou por um bug, um caminho, um escape, uma versão, uma pausa que não esperou; o ambiente do Sandbox não tem o recurso). Conserte em `tools/winsandbox/round/` ou `tools/conformance/` com a menor mudança, e rode de novo só os itens afetados: `powershell -ExecutionPolicy Bypass -File C:\winsandbox\round\run-all.ps1 -Only N` (cada item de uma vez; os logins ficam salvos no perfil do script, não peça de novo o que já está pronto).
2. **Achado real do produto** (o código ou o documento afirma algo que o Windows real desmente). Não conserte o código de produção: descreva o achado, o arquivo e a linha, o que o Windows real mostrou (saída mínima) e a mudança proposta (código ou documento).
3. **Item 8, caso que diverge:** para cada um dos casos de `item8-casos.txt` que o `run-all.ps1` marca como DIVERGE, diga se o analisador de comandos (`internal/classes/shellcmd*.go`, regras de PowerShell em `docs/classes/permissoes.md`) é **mais rígido** que o PowerShell real (seguro: só anote) ou **mais permissivo** (o PowerShell executa algo que o analisador deixa passar: é achado de segurança, grave, com o comando exato).
4. **Limitação do Sandbox ou de cota** (o menu de limite de uso só aparece no limite real; um login não foi feito): anote como pendente, não como falha.

## Regras
- Nunca grave senhas, tokens ou saída de `claude auth`/`codex login` em arquivos do repositório nem nos resultados; mascare o que sair em log.
- Mudanças só em `tools/winsandbox/round/`, `tools/conformance/` e, se for achado de documento, em `docs/` seguindo o `INDEX.md` (documento dono, até ~150 linhas, cabeçalho de data). Código de produção não muda nesta rodada.
- Rode `go vet ./tools/...` e `go test ./tools/...` no Windows e anote o resultado.
- Nunca faça push para `main`. Não faça push de nada: o Dono leva o resultado de volta.
- Gaste cota real só no que precisar para repetir um item.

## Entrega (faça mesmo que algo falhe)
1. `C:\winsandbox\resultados\relatorio-4.md`, em português: uma tabela (item | estado antes | causa 1/2/3/4 | o que você fez | estado depois) e, abaixo, a lista curta de **achados reais** (arquivo, evidência mínima, mudança proposta), com os de segurança primeiro. Inclua a versão do Windows, do PowerShell 5.1 e 7, e das três CLIs.
2. Commit na branch `round/m3m4-windows` com as correções dos scripts (mensagem em inglês, Conventional Commits, terminando com a linha `Co-Authored-By: Claude <noreply@anthropic.com>`).
3. Diga no final o hash do commit, o caminho do relatório e quais itens ficaram PASS, FAIL e SKIP depois da triagem.
