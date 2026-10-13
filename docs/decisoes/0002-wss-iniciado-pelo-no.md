# ADR 0002: Conexão WSS iniciada pela Torre

> **Assunto:** por que cada Torre abre uma conexão WebSocket segura de saída até a Cidadela · **Componente:** Todos · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. **Revista em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): "Core" e "nó" viram Cidadela e Torre; a Torre conecta sempre na **Cidadela atual**, que pode mudar; a conexão passa a levar também a replicação do banco, a ficha da máquina e a época, e toda máquina escuta na rede local para o redirecionamento do painel e a descoberta. O texto abaixo já usa os nomes novos.

## Contexto

- O Mestre suporta N máquinas desde o início. Eles podem estar na rede local ou fora dela (outra casa, nuvem, notebook em viagem), atrás de roteador e sem IP fixo.
- A Cidadela precisa mandar comandos à Torre a qualquer momento: abrir terminal, digitar, rodar etapa. As Torres mandam à Cidadela saída de terminal, `done`, status e chamadas da CLI encaminhadas.
- A spec proíbe depender de SSH e de abrir porta no lado da Torre.
- O painel no navegador também precisa receber atualizações e o fluxo dos terminais em tempo real.

## Opções consideradas

1. **WSS iniciado pela Torre, uma conexão multiplexada.**
   - Prós:
     - Funciona atrás de NAT sem abrir porta na Torre.
     - É o mesmo transporte que o navegador usa para o canvas.
     - TLS e mTLS saem da biblioteca padrão ([ADR 0001](0001-stack-go.md)).
     - Comandos e eventos fluem nos dois sentidos pela mesma conexão.
   - Contras:
     - Multiplexação, ack e idempotência são nossos.
     - Uma conexão só concentra todo o tráfego da Torre.
2. **A Cidadela chama a Torre (servidor HTTP ou gRPC em cada Torre).**
   - Prós: modelo cliente-servidor simples.
   - Contras:
     - Exige porta aberta e endereço alcançável em cada Torre.
     - Não funciona atrás de roteador sem configuração.
     - Viola a regra de não abrir portas na Torre.
3. **gRPC com streaming bidirecional iniciado pela Torre.**
   - Prós: contratos tipados e streaming nativo.
   - Contras:
     - O navegador precisaria de gRPC-web e de um proxy, o que daria dois protocolos (gRPC entre máquinas, WebSocket para o painel).
     - Traz mais ferramentas de build (protoc).
4. **SSH (túnel reverso ou comandos remotos).**
   - Prós: ferramenta conhecida.
   - Contras:
     - Exige OpenSSH Server no Windows e troca de chaves entre máquinas.
     - Dá acesso amplo demais.
     - A spec proíbe.

## Decisão

- Cada Torre abre **uma conexão WSS de saída** até a Cidadela atual, mantém essa conexão aberta e reconecta sozinho com backoff.
- Tudo passa por ela, multiplexado: ficha da máquina, heartbeat, comandos, eventos, fluxo dos terminais, chamadas da CLI encaminhadas, replicação do banco e transferência de bundles ([ADR 0013](0013-sincronizacao-de-worktrees-por-git-bundle.md)).
- **Segurança:**
  - A conexão é sempre TLS, inclusive na rede local, com certificados emitidos pela CA da Guilda.
  - A Torre se autentica com uma credencial própria, recebida na entrada na Guilda.
  - Toda mensagem leva a época da Guilda, e a de época menor é recusada ([Cidadela e Torres](../guilda/cidadela-e-torres.md#época-e-fencing)).
- **Mensagens:**
  - Envelope fixo e versão do protocolo negociada na conexão.
  - Ack e idempotência nos comandos importantes.
- O painel usa o mesmo tipo de WebSocket.
- **Onde estão os detalhes:**
  - Envelope, tipos, versão e ack: [protocolo](../conectividade/protocolo.md).
  - Entrada e credencial: [Guilda](../guilda/guilda.md).
  - Como a Torre acha a Cidadela: [descoberta](../conectividade/descoberta.md).
  - Modelo de ameaças: [segurança](../arquitetura/seguranca.md).

## Consequências

- **Positivas:**
  - A Torre funciona atrás de roteador e fora da rede local sem abrir porta.
  - A Cidadela nunca precisa "chamar" a Torre.
  - Um único transporte serve às Torres e ao navegador.
- **Negativas:**
  - A Cidadela precisa ser alcançável pelas Torres. Na rede local, a descoberta resolve; uma máquina fora dela usa o endereço configurado à mão, e o caminho até ele é da rede do usuário ([descoberta](../conectividade/descoberta.md#máquina-fora-da-rede-local)).
  - A queda da conexão corta a Torre inteira da Cidadela. Por isso a Torre guarda estado local e a réplica do banco ([persistência](../arquitetura/persistencia.md)) e os comandos precisam ser idempotentes para não executar duas vezes depois de reconectar.
  - Saída de terminal com muito volume divide a conexão com comandos de controle. O protocolo precisa de prioridade ou controle de fluxo para um terminal barulhento não atrasar um heartbeat.
  - O Guardião da Torre não usa essa conexão. Ele tem a dele, com credencial própria, para continuar alcançável com o motor morto ([ADR 0003](0003-autocura-com-guardiao-sem-ssh.md)).
  - Cidadela e Torres de versões diferentes precisam conviver durante atualizações, o que obriga a manter compatibilidade entre versões do protocolo.
