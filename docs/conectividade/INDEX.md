# Conectividade

> **Assunto:** índice da comunicação entre as máquinas da Guilda (Cidadela, Torres e Guardiões) e o navegador · **Componente:** Todos · **Atualizado:** 2026-10-13

| Documento | O que cobre | Quando ler |
|---|---|---|
| [protocolo.md](protocolo.md) | endpoints e portas, envelope JSON com época, versão negociada (começa em 1), ack e idempotência, multiplexação e prioridade, replay de terminal, transferência em partes (git bundle, cópia do banco), conexão do Guardião, canal do navegador | implementar ou mudar qualquer mensagem entre componentes |
| [mensagens.md](mensagens.md) | catálogo dos tipos de mensagem (motor e Guardião), inclusive ficha da máquina, replicação, cofre e mudança de Cidadela, com os campos principais e as respostas, e os códigos de erro | implementar ou consumir uma mensagem específica |
| [descoberta.md](descoberta.md) | descobrir a Guilda e a Cidadela atual: broadcast UDP, mDNS (Avahi no Linux, nativo do Windows pelo Guardião), `mestre.local`, cache de endereços, checagem de identidade com a certidão de época, endereço configurado para máquina fora da rede local | a máquina não acha a Guilda, o IP mudou ou a Cidadela mudou |
