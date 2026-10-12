# Conectividade

> **Assunto:** índice da comunicação entre Core, nós, Guardiões e navegador · **Componente:** Todos · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [protocolo.md](protocolo.md) | endpoints e portas, envelope JSON, versão negociada (começa em 1), ack e idempotência, multiplexação e prioridade, replay de terminal, transferência em partes (git bundle), conexão do Guardião, canal do navegador | implementar ou mudar qualquer mensagem entre componentes |
| [mensagens.md](mensagens.md) | catálogo dos tipos de mensagem (motor e Guardião) com os campos principais e as respostas, e os códigos de erro | implementar ou consumir uma mensagem específica |
| [descoberta.md](descoberta.md) | broadcast UDP, mDNS (Avahi no Linux, nativo do Windows pelo Guardião), cache de endereços, checagem de identidade, endereço configurado para nó fora da rede local | o nó não acha o Core ou o IP mudou |
| [pareamento.md](pareamento.md) | convite de uso único, prova mútua no primeiro contato, certificados do nó (motor, Guardião, página reserva), pareamento automático do nó local, CA interna, renovação, revogação | parear, renovar ou revogar um nó |
