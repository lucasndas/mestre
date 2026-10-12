# Conectividade

> **Assunto:** índice da comunicação entre Core, nós, Guardiões e navegador · **Componente:** Todos · **Atualizado:** 2026-10-12

| Documento | O que cobre | Quando ler |
|---|---|---|
| [protocolo.md](protocolo.md) | endpoints e portas, envelope JSON, versão negociada, ack e idempotência, multiplexação e prioridade, tipos de mensagem, transferência em partes (git bundle), conexão do Guardião, canal do navegador | implementar ou mudar qualquer mensagem entre componentes |
| [descoberta.md](descoberta.md) | broadcast UDP, mDNS (Avahi no Linux, nativo do Windows pelo Guardião), cache de endereços, checagem de identidade, endereço configurado para nó fora da rede local | o nó não acha o Core ou o IP mudou |
| [pareamento.md](pareamento.md) | convite de uso único, prova mútua no primeiro contato, certificados mTLS do nó e do Guardião, CA interna, renovação, revogação | parear, renovar ou revogar um nó |
