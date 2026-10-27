# Autocura

> **Assunto:** índice da área de autocura (Guardião, camadas de recuperação, diagnóstico com IA, página reserva, Telegram em modo reduzido) · **Componente:** Guardião · **Atualizado:** 2026-10-13

| Documento | O que cobre | Quando ler |
|---|---|---|
| [guardiao.md](guardiao.md) | o que é o Guardião, o que vigia, lista fechada de ações, canal local com o motor, rollback com restauração do banco, como é alcançado, o que guarda | implementar o Guardião ou qualquer ação de cura |
| [camadas.md](camadas.md) | as cinco camadas, valores padrão (heartbeat, tempos, backoff, limites), runbook, eleição da Torre condutora, proposta de promoção quando a Cidadela não volta, modo máquina única, anti-loop, auditoria | entender ou mudar a ordem e os tempos da recuperação |
| [diagnostico-com-ia.md](diagnostico-com-ia.md) | camada 4: agente local restrito, script mostrado inteiro, aprovação de Senhor da Guilda ou Administrador, execução única | implementar o diagnóstico com IA ou a aprovação de scripts |
| [pagina-reserva.md](pagina-reserva.md) | página do Guardião de qualquer Torre (ou da Cidadela, no modo máquina única) na rede local, com TLS e login, para quando a Cidadela está fora | implementar a página reserva ou mexer no login dela |
| [telegram-modo-reduzido.md](telegram-modo-reduzido.md) | o Guardião da Torre condutora assume o bot com a Cidadela fora, o que aceita (inclusive a aprovação da promoção) e como devolve à Cidadela | implementar o bot no Guardião ou a volta da Cidadela |
