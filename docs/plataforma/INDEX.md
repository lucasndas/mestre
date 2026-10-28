# Plataforma

> **Assunto:** índice dos serviços transversais (cofre, limites de uso, checklist, instalação, atualização automática, mensagens por código e idioma por canal) · **Componente:** Todos · **Atualizado:** 2026-10-28

| Documento | O que cobre | Quando ler |
|---|---|---|
| [cofre.md](cofre.md) | segredos criptografados em toda máquina não excluída e abertos só na Cidadela, chave protegida pelo sistema (arquivo 0600 no Linux, DPAPI de máquina e ACL no Windows), pedido de credencial pela Torre, chave de recuperação, réplica restrita para os Guardiões | trabalhar com qualquer segredo (tokens, chaves de Apps, CA) |
| [limites-de-uso.md](limites-de-uso.md) | registro de uso por provedor, limites por missão, mesa, campanha e dia, 80% e 100%, "aguardando cota" | trabalhar com consumo, custo ou limite de assinatura |
| [checklist.md](checklist.md) | verificação de pré-requisitos de cada máquina, itens por sistema (Linux, Windows) e papel (Cidadela, Torre, não excluída), verificação de início no Windows, gravidade, alertas e correções | adicionar ou mudar uma verificação de ambiente |
| [instalacao-e-atualizacao.md](instalacao-e-atualizacao.md) | visão técnica do instalador único (descobre a Guilda e entra, ou cria; pacotes, contas, serviço `mestre`, tarefa do Operador, CLIs dos provedores, Whisper) em Linux e Windows, pastas de versões, versão anterior guardada, volta de versão, remoção | mexer no instalador, nas pastas de versões ou no rollback |
| [autoatualizacao.md](autoatualizacao.md) | atualização automática do motor e do Guardião via Releases, com aprovação e rollback: Torres primeiro e a Cidadela por último, nunca dois passos juntos nem motor e Guardião fora ao mesmo tempo | mexer no fluxo de atualização |
| [mensagens-por-codigo.md](mensagens-por-codigo.md) | recusas e avisos do motor como código e parâmetros, catálogos do Go em pt-BR e en, paridade de chaves, textos guardados traduzidos na leitura, datas por idioma | escrever qualquer texto que uma pessoa lê |
| [idioma-por-canal.md](idioma-por-canal.md) | de onde vem o idioma no painel, no Telegram (`/idioma`) e na CLI de pessoa, o que fica em inglês por ser para agente, o Grão-Mestre no idioma do usuário | mexer num canal com pessoas ou nas instruções do Grão-Mestre |
