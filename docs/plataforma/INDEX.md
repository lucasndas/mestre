# Plataforma

> **Assunto:** índice dos serviços transversais (cofre, limites de uso, checklist, instalação, atualização automática) · **Componente:** Todos · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [cofre.md](cofre.md) | segredos criptografados no Core, chave protegida pelo sistema (arquivo 0600 no Linux, DPAPI de máquina e ACL no Windows), uso só pelo motor, réplica restrita para os Guardiões | trabalhar com qualquer segredo (tokens, chaves de Apps, CA) |
| [limites-de-uso.md](limites-de-uso.md) | registro de uso por provedor, limites por missão, mesa, workspace e dia, 80% e 100%, "aguardando cota" | trabalhar com consumo, custo ou limite de assinatura |
| [checklist.md](checklist.md) | verificação de pré-requisitos de cada máquina, itens por sistema (Linux, Windows) e papel (Core, nó), verificação de início no Windows, gravidade, alertas e correções | adicionar ou mudar uma verificação de ambiente |
| [instalacao-e-atualizacao.md](instalacao-e-atualizacao.md) | visão técnica da instalação (papel Core ou nó, pacotes, serviços, tarefa, CA, Whisper) em Linux e Windows, pastas de versões, versão anterior guardada, volta de versão, remoção | mexer no instalador, nas pastas de versões ou no rollback |
| [autoatualizacao.md](autoatualizacao.md) | atualização automática do motor e do Guardião via Releases, com aprovação e rollback, nunca os dois fora ao mesmo tempo | mexer no fluxo de atualização |
