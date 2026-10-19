# Pendências de ligação de produção

> **Assunto:** o que o `mestre run` ainda recusa, deixa `nil` ou faz com dublê em produção, onde está cada peça, em que marco fecha (M5 por configuração injetada, M7 com a instalação real) e o critério que a prova · **Componente:** Projeto · **Atualizado:** 2026-10-19

Fonte única da lista. A [ordem de implementação](ordem-de-implementacao.md#regras-de-execução-da-ordem) manda para cá, e a auditoria do M5 ([definição de pronto](convencoes.md#definição-de-pronto)) a confere: cada linha ou fecha no M5 ou aparece com destino M7 na tabela de [critérios em marcos posteriores](ordem-de-implementacao.md#critérios-em-marcos-posteriores).

## A regra de divisão

- **M5:** o que se prova com configuração injetada. O `Config.Terminais` (pasta e socket do lançador), um spawner de teste e o agente falso substituem o que a instalação traria; sem a configuração, a peça **recusa com o motivo**, nunca finge.
- **M7:** o que só existe com a instalação real: o caminho do socket do lançador do serviço, a pasta e a versão instaladas do `mestre-pty`, as contas dos agentes para o `sessionzero.Character`, e a ligação dos terminais de verdade (`mestre-launcher` como serviço). São critérios *pesada* ([testes](../qualidade/testes.md)).

## As peças

| Peça | Onde está | Hoje em produção | Fecha em | Prova |
|---|---|---|---|---|
| `Runtime` de produção | `characters.NewRuntime` (`internal/orchestration/characters/runtime.go`), sem chamador fora dos testes; `hostOrchestration` em `internal/app/host.go` passa `Machines: characters.MachineSet{}` | vazio: todo início responde `MachineUnreachableError` | M5 (com `Config.Terminais` e spawner de teste) | [CA-CIDADELA-16](../guilda/cidadela-e-torres.md#critérios-de-aceite) |
| Terminais de produção: lançador, `Identifier`, caminhos | `internal/terminal` (`launcher.go`, `spawner.go`), `Runtime.Host` | só os testes montam | M5 por configuração; socket do serviço, pasta e versão do `mestre-pty` no M7 | CA-CIDADELA-16; [CA-INST-18](../plataforma/instalacao-e-atualizacao.md#critérios-de-aceite) |
| `Reporter` e `Notifier` do `Runtime` | `characters.Reporter` e `characters.Notifier` (`characters/ports.go`) | sem implementação de produção | M5 | CA-CIDADELA-16 |
| `ClassHookSource` e `HookExec` | `Deps.ClassHooks` e `OnHookEvent` (`internal/app/engine.go`); `hook.ClassHookSource`, `characters/hookexec.go` | `nil`: nenhum hook de classe roda; falta a operação do `mestre-pty` que roda o hook como a conta do agente | M5 | [CA-CLASSES-04](../classes/classes.md#critérios-de-aceite) |
| Catálogo da Torre: `Inspect`, `RemoveWorktree`, `Done`, `Resend` e a sessão zero da mesa | `tables.Peers` e `tables.Tower` (`internal/orchestration/tables`), `quests.Towers`; `Peers` e `QuestTowers` não são postos em `hostOrchestration`; tipos novos faltam em `internal/protocol/catalog.go` | `nil` | M5 | [CA-CIDADELA-17](../guilda/cidadela-e-torres.md#critérios-de-aceite) |
| Parciais do `ask` no `cli.result` | `protocol.TypeCLIResult` (`internal/protocol/payloads.go`), `internal/app/cliwire.go` | o resultado sai inteiro, sem parciais | M5 | CA-CIDADELA-17; [CA-PROTO-08](../conectividade/protocolo.md#critérios-de-aceite) |
| Pré-run de ritual na Torre | `rituals.PreRunner` (`rituals/ports.go`), `OrchestrationOptions.RitualPreRun` | `nil`: o disparo com `--pre-run` é recusado | M5 | [CA-RIT-11](../orquestracao/rituais.md#critérios-de-aceite) |
| Cópia de pergaminho para outra máquina | `characters.ScrollCopies` (`characters/ports.go`), ligada a `scrollsRef.Sync` em `orchestration.go`; falta o `Peers` até a Torre | só a máquina da Cidadela | M5 | [CA-PERG-10](../orquestracao/pergaminhos-e-tomos.md#critérios-de-aceite) |
| Espelhos e anexos da replicação | `replication.Mirrors` (`internal/replication/mirrors.go`); `OrchestrationOptions.Attachments` e `MessageAttachments` | `NewMirrors` não tem chamador fora dos testes; anexos `nil` | M5 | [CA-REPL-18](../guilda/replicacao.md#critérios-de-aceite) |
| `Builder` real da sessão zero do personagem | `unavailableBuilder` (`internal/app/host.go`) | recusa com o motivo | decretos e identidades no M5 (CA-SZ-PERS-05); contas dos agentes reais no M7 | CA-SZ-PERS-05 (M5); [CA-SZ-PERS-16](../sessao-zero/personagem.md#critérios-de-aceite) (M7) |
| GitHub e épicos | `unavailableGitHub` e `unavailableEpics` (`host.go`) | recusam com o motivo até as etapas 3 e 5 | M5 | critérios de [ações por nível](../github/acoes-por-nivel.md) e [mesas](../orquestracao/mesas.md) já na tabela de marcos posteriores |
| Uso e limites: eventos e leitores | `usage.Service.OnEvent` e `Reading` (`internal/orchestration/usage`, em `Orchestration.Usage`; adaptadores em `internal/app/usage.go`) | nada o chama: `Deps.OnHookEvent` (esta máquina) e `OnAgentEvent` (Torres) são `nil`, e nada lê o `ReadUsage` do Codex e do Antigravity nem avisa `SourceUnavailable`; nenhum uso é gravado | M5 (com o `Runtime` e o consumidor do `agent.event`) | CA-CIDADELA-16; `TestTheLimitsOfUseActOnTheRealServices` (`internal/app`) |
| Decretos nas regras efetivas e no hook | `decrees.Service` (`Decrees`, `Notes`, `Use`, `Fulfilled`, `Covers`); `StandardIntegration` o liga ao `chain.Grants`, ao `chain.Delegations`, ao `/decretos` e ao `mestre pr`; `CitadelClassesOptions.Decrees` e o `hook.Engine.OnEvent` (`Answer.Decree`) não são postos no `mestre run`; `Targets` das exceções presas a um PR e a CLI `mestre decree propose` (etapa 5) faltam | o `chain.Grants` vale; as regras efetivas, o registro do uso no hook e a CLI ficam `nil` | M5 (a composição do motor, etapa 5) | [CA-DEC-01](../telegram/decretos.md#critérios-de-aceite) |
| Alertas aos Administradores | `logAlerts` (`host.go`) | só o log, até o bot | M5 | [bot](../telegram/bot.md) |

## O que o M7 fecha

O M7 volta a esta lista e fecha o que o M5 deixou: o socket do lançador do serviço, a pasta e a versão instaladas do `mestre-pty` e a ligação dos terminais reais ([CA-INST-18](../plataforma/instalacao-e-atualizacao.md#critérios-de-aceite)); as contas dos agentes reais no `sessionzero.Character` ([CA-SZ-PERS-16](../sessao-zero/personagem.md#critérios-de-aceite)). Os dois estão na tabela de [critérios em marcos posteriores](ordem-de-implementacao.md#critérios-em-marcos-posteriores), que a [verificação de docs](../qualidade/verificacao-de-docs.md#checagens-de-código-e-de-marcos) cobra na auditoria do M7: o M7 não fecha sem eles.

## Como manter

- Peça nova que o `mestre run` recusa ou deixa `nil` entra aqui na mesma alteração, com o critério e o marco; peça que fecha sai da tabela, e o marcador `// CA-…` fica no teste real.
- Critério com destino M7 vai também à tabela de [critérios em marcos posteriores](ordem-de-implementacao.md#critérios-em-marcos-posteriores), para a [verificação de docs](../qualidade/verificacao-de-docs.md#checagens-de-código-e-de-marcos) exigir o marcador quando o marco for auditado. O marcador só entra quando o teste existe.
