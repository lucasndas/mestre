# Diagnóstico com IA

> **Assunto:** camada 4 da autocura: agente local restrito propõe um script, uma pessoa aprova, o Guardião executa uma vez · **Componente:** Guardião · **Atualizado:** 2026-10-12

## Objetivo

Quando o runbook não resolve, usar um agente de IA na própria máquina com problema para entender o erro e propor uma correção, sem dar a ele poder de agir. Só um Dono ou Administrador transforma a proposta em ação, depois de ver o script inteiro.

## Comportamento

### Quando roda

- Automaticamente, quando a camada 3 esgota as tentativas ou, no [modo máquina única](camadas.md#modo-máquina-única), quando o runbook do próprio Guardião do Core se esgota.
- A pedido de um Dono ou Administrador (painel, [página reserva](pagina-reserva.md) ou [Telegram em modo reduzido](telegram-modo-reduzido.md)), em incidente aberto.
- No máximo **2 execuções do agente por incidente** (padrão). Depois disso, só camada 5.

### Ficha de diagnóstico

- O Dono escolhe no painel uma ficha de diagnóstico **por máquina** ([administração](../painel/administracao.md)), porque cada máquina tem os próprios provedores e logins ([bases e fichas](../orquestracao/bases-e-fichas.md)).
- O Core envia a cada Guardião a ficha resolvida: provedor, comando por sistema, perfil de conta. Não é segredo; as credenciais do provedor ficam onde já estão (perfil da conta).
- Sem ficha configurada, ou provedor não instalado/logado (checklist), a camada 4 é pulada e o incidente vai para a camada 5 com o motivo.

### Execução restrita

1. O Guardião cria uma pasta temporária do incidente e copia para ela, já mascarado ([segurança](../arquitetura/seguranca.md)):
   - últimos logs do motor e do Guardião (padrão 2.000 linhas) e do sistema (journal do serviço no Linux; Log de Eventos do serviço no Windows);
   - configuração do Mestre **sem segredos**;
   - saída de `status`, `checklist` e o histórico de ações do incidente.
2. Inicia o agente **em modo não interativo**, com a pasta temporária como pasta de trabalho, como a conta dos agentes (nunca root ou LocalSystem):
   - Linux: troca para a conta dos agentes ([componentes](../arquitetura/componentes.md#serviços-e-contas-padrão));
   - Windows (Core ou nó): dispara a tarefa agendada `\Mestre\Diagnostico`, registrada na instalação para rodar como o usuário, conectado ou não; por que ela continua: [Windows](../arquitetura/windows.md#tarefa-mestrediagnostico).
3. Restrições nativas de só leitura, por provedor:

   | Provedor | Restrição proposta |
   |---|---|
   | Claude Code | `claude -p` com `--tools "Read,Grep,Glob"` e `--permission-mode plan` ([Claude Code](../provedores/claude-code.md)) |
   | Codex | `codex exec -s read-only` ([Codex](../provedores/codex.md)) |
   | Antigravity | `agy -p` com `toolPermission` `strict` (só ferramentas de leitura) ([Antigravity](../provedores/antigravity.md)) |

4. O prompt é fixo, versionado com o Guardião. Ele pede uma resposta JSON com: `diagnosis`, `probable_cause`, `platform` (`bash` ou `powershell`), `script`, `risks`, `how_to_revert`.
5. Tempo limite do agente: 15 min (padrão). Resposta fora do formato conta como execução sem solução.

O agente não recebe segredos, não enxerga o sistema vivo (só cópias) e não executa nada: ele só lê e responde.

### Proposta e aprovação

- O Guardião calcula o **SHA-256** do script e registra a proposta no incidente.
- **Verificação automática** antes de mostrar:
  - script que cita o Guardião (serviço, binário, pasta, credencial, firewall dele) é **recusado** e não pode ser aprovado ([Guardião](guardiao.md#proteções));
  - trechos destrutivos (apagar arquivos em massa, mexer em firewall, serviços ou usuários do sistema) são **destacados** como "ação destrutiva".
- O pedido de aprovação mostra: máquina, incidente, diagnóstico, causa provável, riscos, como reverter, os 8 primeiros caracteres do hash e o **script inteiro, nunca resumido nem cortado**.
- Canais:
  - Core no ar: [central de aprovações](../painel/conversa-e-aprovacoes.md) e Telegram pelo motor ([fluxo](../aprovacoes/fluxo.md));
  - Core fora: [página reserva](pagina-reserva.md) e [Telegram em modo reduzido](telegram-modo-reduzido.md). No Telegram, script com mais de 3.500 caracteres vai como arquivo `.sh` ou `.ps1` anexado.
- Só **Dono ou Administrador** aprova, sempre com **PIN** ([PIN](../telegram/usuarios-e-niveis.md#pin-pessoal)). O Grão-Mestre nunca aprova scripts de cura.
- A aprovação vale para aquele hash e expira em 30 min (padrão). Negar ou expirar → camada 5 (ou nova proposta, se ainda couber no limite).

### Execução do script aprovado

1. O Guardião confere que o hash do script guardado é igual ao aprovado.
2. Executa **uma única vez** (`run_script`), com `bash --noprofile --norc -e` no Linux ou `powershell -NoProfile -NonInteractive -File` no Windows, com a conta do Guardião (o conserto pode exigir privilégio, e por isso a aprovação humana é obrigatória).
3. Tempo limite 10 min (padrão). Guarda a saída (até 1 MiB, mascarada) e o código de saída.
4. Volta ao runbook só para a confirmação de sucesso ([camadas](camadas.md#valores-padrão)).
5. O arquivo do script é apagado; o conteúdo e o hash ficam na auditoria.

### Auditoria

Cada etapa gera registro: início do agente (ficha, provedor, versão), proposta (hash, script completo, diagnóstico), verificação automática, quem aprovou ou negou (usuário, canal, horário), execução (início, fim, código de saída, saída mascarada) e resultado da confirmação.

## Regras e erros

- Aprovador de nível abaixo de Administrador → recusado, com o nível exigido.
- Hash diferente do aprovado → não executa, alerta grave ao Dono (possível adulteração).
- Pedido de executar de novo um script já executado → `already_executed`. Repetir exige nova proposta e nova aprovação.
- Provedor sem cota ou sem login → execução sem solução, com o motivo no incidente.
- Ações da lista fechada do Guardião não passam por aqui.

## Critérios de aceite

- **CA-DIAG-01** · *rápida* — **Dado** um script aprovado, **quando** o conteúdo guardado muda antes da execução, **então** o Guardião não executa e alerta o Dono.
- **CA-DIAG-02** · *rápida* — **Dado** um Aprovador, **quando** tenta aprovar um script de cura, **então** é recusado com "exige Administrador".
- **CA-DIAG-03** · *rápida* — **Dado** uma proposta pendente (relógio falso), **quando** passam 30 min, **então** a aprovação expira e o incidente vai para a camada 5.
- **CA-DIAG-04** · *rápida* — **Dado** um script já executado, **quando** chega novo pedido de execução, **então** a resposta é `already_executed`.
- **CA-DIAG-05** · *rápida* — **Dado** um script que contém `systemctl stop mestre-guardiao`, **quando** a proposta chega, **então** é recusada sem ir para aprovação.
- **CA-DIAG-06** · *completa* — **Dado** um agente falso de diagnóstico, **quando** o Guardião monta a pasta temporária, **então** ela contém logs e configuração mascarados e nenhum valor do cofre de teste.
- **CA-DIAG-07** · *rápida* — **Dado** um script de 5.000 caracteres e o Core fora, **quando** vai ao Telegram em modo reduzido, **então** segue como arquivo anexado e a mensagem traz o hash.
- **CA-DIAG-08** · *completa* — **Dado** o motor do Core fora e um script aprovado na página reserva, **quando** o Guardião do Core executa, **então** a auditoria local chega ao Core depois que ele volta.

## Depende de

- [Guardião](guardiao.md)
- [Camadas](camadas.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)
- [Adaptador de provedores](../provedores/adaptador.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Página reserva](pagina-reserva.md)
- [Telegram em modo reduzido](telegram-modo-reduzido.md)

## Verificar na implementação

- **Verificar na implementação:** as restrições de só leitura da tabela impedem, em cada CLI no modo não interativo, que o agente escreva ou rode comandos. **Padrão:** a tabela acima. **Se falhar:** aquele provedor não faz diagnóstico naquele nó; o Guardião usa outro provedor que passou e, sem nenhum, o incidente segue sem diagnóstico com IA para a camada seguinte ([camadas](camadas.md)). **Onde:** etapa 11 do [teste de conformidade](../provedores/conformidade.md).
- **Verificar na implementação:** no Windows, o Guardião (LocalSystem) dispara a tarefa `\Mestre\Diagnostico`, que roda como o usuário mesmo deslogado, com a senha guardada pelo Agendador de Tarefas. **Padrão:** funciona. **Se falhar:** o diagnóstico com IA não é oferecido nas máquinas Windows; o incidente segue sem ele para a camada 5, com o motivo, e a ficha de diagnóstico dessas máquinas fica bloqueada no painel com a explicação. **Onde:** suíte pesada, nos testes de falha da autocura com a VM Windows como Core e como nó, sem usuário logado.

## Fontes

- Claude Code, flags: https://code.claude.com/docs/en/cli-reference
- Codex, sandbox e modo não interativo: https://learn.chatgpt.com/docs/sandboxing , https://learn.chatgpt.com/docs/non-interactive-mode
- Antigravity, permissões e modo headless: https://antigravity.google/docs/permissions , https://antigravity.google/docs/cli/headless
