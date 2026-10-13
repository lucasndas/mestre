# Regras de código verificadas por máquina

> **Assunto:** as regras de código que o golangci-lint não cobre e que o `tools/codecheck` confere na suíte rápida (subtestes paralelos, `select {}`, entrada e saída dos processos filhos de teste, descritores de segurança no Windows, pastas privadas, segredos que se imprimem mascarados) e a marca de exceção `mestre:allow` · **Componente:** Projeto · **Atualizado:** 2026-10-13

## Objetivo

Impedir por máquina as classes de erro que a auditoria do M0 e do M1 achou à mão. Vale a regra "cada regra de código vive numa ferramenta só" ([convenções](../desenvolvimento/convencoes.md#estilo)): o que um linter diz com um padrão de nome fica no golangci-lint; o que precisa ler a estrutura do código, ou aceitar uma exceção com motivo, fica no `codecheck`; o que cruza código e documentos fica na [verificação de docs](verificacao-de-docs.md).

## Comportamento

### Onde roda

- Ferramenta Go `tools/codecheck`: o teste `TestCodeRules` (sem build tag, portanto da rápida) confere o repositório inteiro; `go run ./tools/codecheck` roda sozinha. Lê todos os `.go` com `go/parser`, em paralelo, menos as pastas que o `go` ignora, `testdata`, `web` e `node_modules`. Orçamento: o repositório inteiro em menos de 1 s (medido em cerca de 40 ms).
- Mostra todos os problemas de uma vez: `<arquivo>:<linha>: <regra>: <mensagem>`. O código de saída é 1 se houver algum.
- Código de spike (`tools/spikes/`, `tools/winsandbox/spikes/`) fica fora das regras novas ([convenções](../desenvolvimento/convencoes.md#estilo)); só os subtestes paralelos valem para os testes dele.

### As regras

| Regra | Proíbe | Vale em |
|---|---|---|
| `parallel` | func literal passada a `t.Run` sem `t.Parallel()`, em `TestXxx` e em qualquer função com parâmetro `*testing.T` | todo `_test.go` |
| `bare-select` | `select {}` sem casos: no Windows, um programa que só espera assim morre com "all goroutines are asleep". Espere como o `block()` de `internal/terminal/harness_full_test.go` (um temporizador pendente) | `_test.go`, `internal/testkit/`, `cmd/fakeagent/` |
| `child-stdio` | dar `os.Stdout` ou `os.Stderr` ao processo filho: atribuí-los a `Stdout`/`Stderr` (de um `exec.Cmd` ou de qualquer valor), pô-los num `exec.Cmd{…}` ou em `Files` de um `os.ProcAttr`. O filho que herda a saída do teste segura o pipe do `go test` aberto depois do fim do teste; use `bytes.Buffer`, arquivo ou `io.Discard` | mesmos lugares de `bare-select` |
| `nil-sa` | o literal `nil` como atributos de segurança de `windows.CreateJobObject` (arg. 1), `CreateNamedPipe` (último), `CreateProcess` (2 e 3), `CreateProcessAsUser` (4 e 5) e `CreateFile` que cria (4: `nil` só com `OPEN_EXISTING` ou `TRUNCATE_EXISTING`): sem descriptor, o objeto fica com o padrão do token, e não com a ACL do [projeto](../arquitetura/windows.md). Variável que guarda `nil` não é vista | `_windows.go`, fora de testes |
| `private-dir` | `os.MkdirAll` e `os.Mkdir` com `0o700` (também `0700` e `os.FileMode(0o700)`): só `privdir.Ensure` (`internal/privdir`), que confere o dono e o modo de uma pasta que já existia, coisa que o `MkdirAll` não faz. No Windows a pasta não tem modo e a ACL é do `internal/winsvc` | código fora de testes e fora de `_windows.go` |
| `secret-receiver` | `String`, `GoString`, `Format` e `LogValue` em receptor ponteiro quando o tipo tem o método `Reveal` (a única forma de ler o valor) ou o método devolve o marcador de redação (`logging.Redacted`, `"[segredo]"`); `MarshalJSON` e `MarshalText` só no segundo caso, porque um segredo que viaja cifrado se revela ali. Com receptor ponteiro, `fmt.Sprint(*segredo)` e uma cópia dentro de outra estrutura imprimem o segredo | código fora de testes, por pacote |

### Exceção: `mestre:allow`

Um único mecanismo, para todas as regras acima: um comentário `// mestre:allow <regra>: <motivo>` na linha da violação ou na linha de cima. O motivo é obrigatório; a regra precisa existir; e uma exceção que não desculpa nada falha (`allow`), para as exceções não sobreviverem ao código para o qual foram escritas. Não há `//nolint` para essas regras (o golangci-lint não as conhece).

## Regras e erros

- Uma regra nova entra aqui, na tabela e com critério de aceite, na mesma alteração que a ferramenta. Se um padrão de nome basta, é regra do `.golangci.yml`, não daqui.
- A mensagem de cada violação diz o que usar no lugar; a regra não se contorna com um rodeio (guardar o `nil` numa variável, por exemplo): é a revisão que impede.
- O código existente passa em todas as regras; um achado novo é corrigido, e `mestre:allow` fica para o caso em que o motivo cabe em uma frase.

## Critérios de aceite

- **CA-CODIGO-01** · *rápida* — **Dado** um `TestA` cujo subteste não chama `t.Parallel()`, um auxiliar com `*testing.T` com o mesmo defeito e um subteste que chama, **quando** o `codecheck` roda, **então** aponta os dois primeiros com arquivo e linha e não o terceiro.
- **CA-CODIGO-02** · *rápida* — **Dado** um `select {}` num `_test.go`, em `internal/testkit/` ou em `cmd/fakeagent/`, **quando** o `codecheck` roda, **então** falha com a regra `bare-select`; **dado** um `select` com casos, o `block()` ou o mesmo código fora de teste, **então** passa.
- **CA-CODIGO-03** · *rápida* — **Dado** um teste que atribui `os.Stdout` ou `os.Stderr` ao `Stdout`/`Stderr` de um processo filho (inclusive dentro de `io.MultiWriter`, de um `exec.Cmd{…}` ou de `os.ProcAttr.Files`), **quando** o `codecheck` roda, **então** falha com a regra `child-stdio`; **dado** um buffer, `io.Discard` ou `os.Stdin`, **então** passa.
- **CA-CODIGO-04** · *rápida* — **Dado** um `_windows.go` que chama `CreateJobObject(nil, …)`, `CreateNamedPipe(…, nil)`, `CreateProcess`/`CreateProcessAsUser` com `nil` nos atributos ou `CreateFile` que cria com `nil`, **quando** o `codecheck` roda, **então** falha com a regra `nil-sa`; **dado** um descriptor, `CreateFile` com `OPEN_EXISTING`, um arquivo de teste ou um arquivo que não é `_windows.go`, **então** passa.
- **CA-CODIGO-05** · *rápida* — **Dado** `os.MkdirAll(d, 0o700)` ou `os.Mkdir` fora de `internal/privdir`, **quando** o `codecheck` roda, **então** falha com a regra `private-dir`; **dado** o mesmo código em `internal/privdir`, em teste, em `_windows.go` ou com outro modo, **então** passa.
- **CA-CODIGO-06** · *rápida* — **Dado** um tipo com `Reveal` e `String` em receptor ponteiro, ou um `String`/`LogValue`/`Format` em ponteiro que devolve `logging.Redacted`, **quando** o `codecheck` roda, **então** falha com a regra `secret-receiver`; **dado** receptores por valor, um `Reveal` em outro pacote ou um `MarshalJSON` que revela, **então** passa.
- **CA-CODIGO-07** · *rápida* — **Dado** `// mestre:allow bare-select: <motivo>` na linha da violação ou na de cima, **quando** o `codecheck` roda, **então** a violação é desculpada; **dado** a exceção sem motivo, com regra desconhecida, de outra regra, duas linhas acima ou sem nada a desculpar, **então** o `codecheck` falha na linha do comentário.
- **CA-CODIGO-08** · *rápida* — **Dado** o repositório real, **quando** o `codecheck` roda, **então** não há problema, em menos de 1 s (a mais rápida de até três execuções).

## Depende de

- [testes.md](testes.md) (roda na suíte rápida) · [convencoes.md](../desenvolvimento/convencoes.md)
