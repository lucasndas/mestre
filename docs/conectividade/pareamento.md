# Pareamento de nós

> **Assunto:** convite de uso único, prova mútua no primeiro contato, credenciais mTLS do nó e do Guardião, confiança na CA interna, renovação e revogação · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Deixar um nó novo entrar no sistema com um único passo do usuário (informar o código de convite) e, a partir daí, usar só credenciais próprias, que o Dono pode revogar a qualquer momento. O passo a passo para quem usa está em [parear nó](../operacao/parear-no.md).

## Comportamento

### Convite

- **Quem gera:** o Dono gera no painel ([administração](../painel/administracao.md)), com um nome sugerido para o nó.
- **Código:** 16 caracteres em base32 Crockford, ou seja, 80 bits, no formato `XXXX-XXXX-XXXX-XXXX`.
  - É de **uso único** e vale **24 horas** (padrão).
  - O Core guarda só o hash do código.
- **Onde se usa:** na instalação do nó, o instalador pede o código. Também dá para usar `mestre node pair --invite <código> [--core <endereço>]`, por exemplo para repetir o pareamento ([componentes](../arquitetura/componentes.md#mestre)).
- **Depois disso, nada é manual:** o nó acha o Core sozinho ([descoberta](descoberta.md), com `core_id` vazio) e completa o pareamento.
- **Nó local do Core (Linux e Windows):** a própria instalação do Core gera um convite interno e pareia o `mestre-node` local, em `127.0.0.1`, pelo mesmo fluxo; o usuário não vê código ([componentes](../arquitetura/componentes.md)).

### Prova mútua no primeiro contato

O nó ainda não conhece a CA do Core, e o Core ainda não conhece o nó. O código de convite serve de segredo compartilhado, sem nunca trafegar.

```mermaid
sequenceDiagram
  participant N as Nó
  participant C as Core (/pair)
  N->>C: TLS sem validar a cadeia; N anota fp = SHA-256 do certificado visto
  N->>C: nonce_n, mac_n = HMAC(K, "node" ‖ nonce_n ‖ fp)
  C->>C: testa mac_n com cada convite ativo; acha o convite
  C-->>N: nonce_c, CA, core_id, mac_c = HMAC(K, "core" ‖ nonce_n ‖ nonce_c ‖ fp ‖ SHA-256(CA))
  N->>N: confere mac_c; guarda CA e core_id
  N->>C: CSRs do motor, do Guardião e da página reserva + dados do nó, mac_csr = HMAC(K, "csr" ‖ nonce_c ‖ SHA-256(CSRs))
  C-->>N: certificados assinados, node_id; convite marcado como usado
  N->>C: conecta em /ws/node com mTLS (protocolo normal)
```

- **Chave:** `K` é derivada do código com HKDF-SHA256.
- **Contra intermediário:** um intermediário mostraria outro certificado, então `fp` mudaria. O Core real rejeita `mac_n`, e o intermediário não consegue produzir `mac_c`.
- **Contra força bruta offline:** os 80 bits do código e a validade curta tornam a força bruta offline inviável.
- **Tentativas online:**
  - São 10 tentativas erradas por IP por hora.
  - Com 20 falhas em 24 horas, o Core invalida todos os convites ativos e alerta o Dono.

### Credenciais

- **Chaves no nó:** o nó gera **três pares de chaves** ECDSA P-256 localmente: cliente do motor, cliente do Guardião e servidor da página reserva (usada pelo Guardião). As chaves privadas nunca saem da máquina.
  - Ficam em arquivos com acesso só para a conta do serviço dono e para o sistema: no Windows, em `%ProgramData%\Mestre\` com ACL; no Linux, `0600`.
  - As duas chaves do Guardião ficam na pasta do Guardião.
- **Certificados emitidos pela CA interna,** válidos por 1 ano (padrão):
  - motor do nó: SAN de URI `mestre://node/<node_id>`;
  - Guardião do nó: `mestre://guardian/<node_id>`;
  - página reserva do nó: nome do nó, `<host>.local` e os IPs da rede local, para o navegador ([página reserva](../autocura/pagina-reserva.md));
  - emitidos na instalação do Core, não no pareamento: servidor do motor do Core (`mestre://core/<core_id>`, mais o nome da máquina, `<host>.local` e os IPs) e servidor do Guardião do Core, um certificado próprio (`mestre://core-guardian/<core_id>`, mais os mesmos nomes, para a 7444 e, no modo máquina única, a página reserva).
- A tabela completa de certificados está em [segurança](../arquitetura/seguranca.md#tls-e-ca-interna).
- **Renovação:**
  - É automática pela conexão já autenticada, **30 dias antes de vencer** (padrão), com chave nova e mensagem `cert.renew` (o motor pela conexão dele; o Guardião pela dele, para os dois certificados que usa).
  - Quando os IPs da máquina mudam, o Guardião pede na hora um certificado novo da página reserva; o Core faz o mesmo com os certificados de servidor dele.
  - Um certificado vencido, por exemplo de um nó desligado por mais de um ano, exige pareamento novo.
- **Registro do nó no Core:**
  - `node_id` (ULID) e nome;
  - prioridade, usada na [eleição do nó condutor](../autocura/camadas.md) e editável no painel;
  - últimas capacidades informadas;
  - números de série dos certificados;
  - estado;
  - datas de pareamento e de revogação.

### Confiança na CA interna

- A CA nasce na instalação do Core e vale 10 anos (padrão). A chave privada dela fica no [cofre](../plataforma/cofre.md). O modelo completo está em [segurança](../arquitetura/seguranca.md).
- **O nó só confia nessa CA para falar com o Core.** Ele ignora o repositório de certificados do sistema e confere o `core_id` no SAN ([checagem de identidade](descoberta.md#checagem-de-identidade)).
- Quem verifica os certificados dos nós e dos Guardiões são o motor do Core e o Guardião do Core. O Guardião do Core recebe do motor a lista de revogados num arquivo local.
- O navegador confia na CA uma vez, por instrução ao usuário ([primeiro acesso](../operacao/primeiro-acesso.md)).

### Revogação

- **Quem revoga:** o Dono, no painel, em "revogar nó", a qualquer momento ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)).
- **O que acontece, na hora:**
  - os certificados do nó entram na lista de revogados;
  - as conexões do motor e do Guardião caem com o código `4002`;
  - toda conexão nova com esses certificados é recusada no handshake, no motor e no Guardião do Core;
  - os personagens daquele nó ficam marcados como interrompidos ([personagens](../orquestracao/personagens.md)).
- Como só o Core verifica, não há lista de revogação a distribuir para os nós.
- Para voltar, o nó precisa de convite novo e recebe `node_id` novo. O histórico do antigo fica no banco.

O instalador que chama o pareamento está em [instalação e atualização](../plataforma/instalacao-e-atualizacao.md).

## Regras e erros

- **Convite inválido** (expirado, usado ou errado): o nó mostra "convite inválido ou expirado" e nada é criado.
- **`mac_c` que não confere:** o nó aborta, não guarda nenhuma CA e registra possível intermediário.
- **Nó com o mesmo nome:** o Core acrescenta um sufixo. O `node_id` é que identifica o nó.
- **Pareamento interrompido** depois dos certificados e antes da primeira conexão: o nó tenta conectar com o que recebeu. O convite já está usado.

## Critérios de aceite

- **CA-PAR-01** · *rápida* — **Dado** um convite válido, **quando** o nó completa a troca, **então** recebe os três certificados com os SANs certos e um segundo uso do convite é recusado.
- **CA-PAR-02** · *rápida* — **Dado** um convite com 25 horas (relógio falso), **quando** é usado, **então** o pareamento falha e nada é criado.
- **CA-PAR-03** · *rápida* — **Dado** um intermediário com outro certificado TLS, **quando** o nó pareia através dele, **então** o Core rejeita `mac_n` e o nó não guarda nenhuma CA.
- **CA-PAR-04** · *rápida* — **Dado** 10 códigos errados de um IP numa hora, **quando** chega o 11º, **então** ele é recusado sem teste.
- **CA-PAR-05** · *rápida* — **Dado** um nó revogado, **quando** o motor ou o Guardião dele conectam ao Core ou ao Guardião do Core, **então** o handshake é recusado.
- **CA-PAR-06** · *rápida* — **Dado** um certificado a 30 dias de vencer (relógio falso), **quando** o nó está conectado, **então** pede e instala um certificado novo sem cair; **dado** a mudança de IP da máquina, **então** o Guardião recebe um certificado novo da página reserva com o IP novo.
- **CA-PAR-07** · *completa* — **Dado** Core e nó reais na mesma máquina, **quando** o nó é pareado com um convite, **então** passa a conectar só por mTLS e reconecta depois de reiniciar.
- **CA-PAR-09** · *pesada* — **Dado** a instalação de um Core, no Linux e no Windows, **quando** ela termina, **então** o `mestre-node` local está pareado, conectado por mTLS em `127.0.0.1:7443` e marcado como nó local, sem convite ativo sobrando (MVP-13).
- **CA-PAR-08** · *pesada* — **Dado** Core instalado no Linux e Dono criado, **quando** instalo o nó no Windows e informo o convite, **então** o nó acha o Core sozinho na rede local, pareia e aparece online no painel com as capacidades dele (MVP-1).

## Depende de

- [Protocolo](protocolo.md) · [Descoberta](descoberta.md)
- [Cofre](../plataforma/cofre.md) (chave privada da CA)
- [Segurança](../arquitetura/seguranca.md)
