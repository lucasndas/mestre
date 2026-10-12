# ADR 0002: Conexão WSS iniciada pelo nó

> **Assunto:** por que cada nó abre uma conexão WebSocket segura de saída até o Core · **Componente:** Todos · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- O Mestre suporta N nós desde o início. Eles podem estar na rede local ou fora dela (outra casa, nuvem, notebook em viagem), atrás de roteador e sem IP fixo.
- O Core precisa mandar comandos ao nó a qualquer momento: abrir terminal, digitar, rodar etapa. Os nós mandam ao Core saída de terminal, `done`, status e chamadas da CLI encaminhadas.
- A spec proíbe depender de SSH e de abrir porta no lado do nó.
- O painel no navegador também precisa receber atualizações e o fluxo dos terminais em tempo real.

## Opções consideradas

1. **WSS iniciado pelo nó, uma conexão multiplexada.**
   - Prós:
     - Funciona atrás de NAT sem abrir porta no nó.
     - É o mesmo transporte que o navegador usa para o canvas.
     - TLS e mTLS saem da biblioteca padrão ([ADR 0001](0001-stack-go.md)).
     - Comandos e eventos fluem nos dois sentidos pela mesma conexão.
   - Contras:
     - Multiplexação, ack e idempotência são nossos.
     - Uma conexão só concentra todo o tráfego do nó.
2. **Core chama o nó (servidor HTTP ou gRPC em cada nó).**
   - Prós: modelo cliente-servidor simples.
   - Contras:
     - Exige porta aberta e endereço alcançável em cada nó.
     - Não funciona atrás de roteador sem configuração.
     - Viola a regra de não abrir portas no nó.
3. **gRPC com streaming bidirecional iniciado pelo nó.**
   - Prós: contratos tipados e streaming nativo.
   - Contras:
     - O navegador precisaria de gRPC-web e de um proxy, o que daria dois protocolos (gRPC para nós, WebSocket para o painel).
     - Traz mais ferramentas de build (protoc).
4. **SSH (túnel reverso ou comandos remotos).**
   - Prós: ferramenta conhecida.
   - Contras:
     - Exige OpenSSH Server no Windows e troca de chaves entre máquinas.
     - Dá acesso amplo demais.
     - A spec proíbe.

## Decisão

- Cada nó abre **uma conexão WSS de saída** até o Core, mantém essa conexão aberta e reconecta sozinho com backoff.
- Tudo passa por ela, multiplexado: registro e capacidades, heartbeat, comandos, eventos, fluxo dos terminais, chamadas da CLI encaminhadas entre nós e transferência de bundles ([ADR 0013](0013-sincronizacao-de-worktrees-por-git-bundle.md)).
- **Segurança:**
  - A conexão é sempre TLS, inclusive na rede local, com certificados emitidos pela CA interna do Core.
  - O nó se autentica com uma credencial própria, recebida no pareamento.
- **Mensagens:**
  - Envelope fixo e versão do protocolo negociada na conexão.
  - Ack e idempotência nos comandos importantes.
- O painel usa o mesmo tipo de WebSocket.
- **Onde estão os detalhes:**
  - Envelope, tipos, versão e ack: [protocolo](../conectividade/protocolo.md).
  - Convite e credencial: [pareamento](../conectividade/pareamento.md).
  - Como o nó acha o Core: [descoberta](../conectividade/descoberta.md).
  - Modelo de ameaças: [segurança](../arquitetura/seguranca.md).

## Consequências

- **Positivas:**
  - O nó funciona atrás de roteador e fora da rede local sem abrir porta.
  - O Core nunca precisa "chamar" o nó.
  - Um único transporte serve aos nós e ao navegador.
- **Negativas:**
  - O Core precisa ser alcançável pelos nós. Na rede local, a descoberta resolve; um nó fora dela usa o endereço do Core configurado à mão, e o caminho até ele é da rede do usuário ([descoberta](../conectividade/descoberta.md#nó-fora-da-rede-local)).
  - A queda da conexão corta o nó inteiro do Core. Por isso o nó guarda estado local ([persistência](../arquitetura/persistencia.md)) e os comandos precisam ser idempotentes para não executar duas vezes depois de reconectar.
  - Saída de terminal com muito volume divide a conexão com comandos de controle. O protocolo precisa de prioridade ou controle de fluxo para um terminal barulhento não atrasar um heartbeat.
  - O Guardião do nó não usa essa conexão. Ele tem a dele, com credencial própria, para continuar alcançável com o motor morto ([ADR 0003](0003-autocura-com-guardiao-sem-ssh.md)).
  - Core e nós de versões diferentes precisam conviver durante atualizações, o que obriga a manter compatibilidade entre versões do protocolo.
