# Plataforma

> **Assunto:** índice dos serviços transversais (cofre, limites de uso, checklist, instalação, atualização automática) · **Componente:** Todos · **Atualizado:** 2026-10-13

| Documento | O que cobre | Quando ler |
|---|---|---|
| [cofre.md](cofre.md) | segredos criptografados em toda máquina não excluída e abertos só na Cidadela, chave protegida pelo sistema (arquivo 0600 no Linux, DPAPI de máquina e ACL no Windows), pedido de credencial pela Torre, chave de recuperação, réplica restrita para os Guardiões | trabalhar com qualquer segredo (tokens, chaves de Apps, CA) |
| [limites-de-uso.md](limites-de-uso.md) | registro de uso por provedor, limites por missão, mesa, workspace e dia, 80% e 100%, "aguardando cota" | trabalhar com consumo, custo ou limite de assinatura |
| [checklist.md](checklist.md) | verificação de pré-requisitos de cada máquina, itens por sistema (Linux, Windows) e papel (Cidadela, Torre, não excluída), verificação de início no Windows, gravidade, alertas e correções | adicionar ou mudar uma verificação de ambiente |
| [instalacao-e-atualizacao.md](instalacao-e-atualizacao.md) | visão técnica do instalador único (descobre a Guilda e entra, ou cria; pacotes, contas, serviço `mestre`, tarefa do Operador, CLIs dos provedores, Whisper) em Linux e Windows, pastas de versões, versão anterior guardada, volta de versão, remoção | mexer no instalador, nas pastas de versões ou no rollback |
| [autoatualizacao.md](autoatualizacao.md) | atualização automática do motor e do Guardião via Releases, com aprovação e rollback: Torres primeiro e a Cidadela por último, nunca dois passos juntos nem motor e Guardião fora ao mesmo tempo | mexer no fluxo de atualização |
