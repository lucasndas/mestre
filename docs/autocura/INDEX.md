# Autocura

> **Assunto:** índice da área de autocura (Guardião, camadas de recuperação, diagnóstico com IA, página reserva, Telegram em modo reduzido) · **Componente:** Guardião · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [guardiao.md](guardiao.md) | o que é o Guardião, o que vigia, lista fechada de ações, canal local com o motor, rollback com restauração do banco, como é alcançado, o que guarda | implementar o Guardião ou qualquer ação de cura |
| [camadas.md](camadas.md) | as cinco camadas, valores padrão (heartbeat, tempos, backoff, limites), runbook, eleição do nó condutor, modo máquina única, anti-loop, auditoria | entender ou mudar a ordem e os tempos da recuperação |
| [diagnostico-com-ia.md](diagnostico-com-ia.md) | camada 4: agente local restrito, script mostrado inteiro, aprovação de Dono ou Administrador, execução única | implementar o diagnóstico com IA ou a aprovação de scripts |
| [pagina-reserva.md](pagina-reserva.md) | página do Guardião do nó (ou do Core, no modo máquina única) na rede local, com TLS e login, para quando o Core está fora | implementar a página reserva ou mexer no login dela |
| [telegram-modo-reduzido.md](telegram-modo-reduzido.md) | o Guardião condutor assume o bot com o Core fora, o que aceita e como devolve ao Core | implementar o bot no Guardião ou a volta do Core |
