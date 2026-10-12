# Descoberta do Core

> **Assunto:** como o nó (motor e Guardião) encontra o Core na rede local sem configuração: broadcast UDP, mDNS (Avahi no Linux, nativo no Windows), cache e endereço configurado · **Componente:** Node · **Atualizado:** 2026-10-12

## Objetivo

O nó acha o Core sozinho na rede local e o reencontra quando o IP muda, sem intervenção do usuário. Vale para Core e nós em Linux ou Windows. Um nó fora da rede local usa o endereço do Core configurado à mão (abaixo).

## Comportamento

### Ordem de tentativa

1. **Endereço configurado** (`core.endereco` na configuração do nó), se houver.
2. **Cache:** os últimos endereços que funcionaram, do mais recente para o mais antigo.
3. **Broadcast UDP e mDNS, em paralelo,** por até 3 s. Os candidatos vão para uma lista.
4. Para cada candidato, o nó abre TLS e faz a **checagem de identidade**. O primeiro que passa é usado e entra no cache.

- A descoberta roda no início, quando não há endereço que funcione e depois de 3 falhas seguidas de conexão ([reconexão](protocolo.md#heartbeat-e-reconexão)).
- Sem nenhum candidato, o nó tenta de novo com o mesmo backoff da reconexão e mostra "Core não encontrado" no [checklist](../plataforma/checklist.md).

### Checagem de identidade

- **Descoberta é só uma dica.** Quem garante que é o Core certo é o TLS:
  - o certificado do servidor precisa ter cadeia até a CA interna guardada no pareamento;
  - e precisa trazer o SAN de URI `mestre://core/<core_id>` do Core pareado ([pareamento](pareamento.md)).
- O IP e o nome não entram na checagem. Por isso uma mudança de IP não exige certificado novo para os nós.
- Um candidato que falha na checagem é descartado e registrado em log. O nó tenta o próximo.

### Broadcast UDP

- **Porta:** `7445/udp` (padrão, configurável). O motor do Core escuta em `0.0.0.0:7445`.
- **Pedido:** o nó envia 3 pedidos, com 1 s de intervalo, para `255.255.255.255` e para o broadcast de cada interface ativa (ex.: `192.168.1.255`):

```json
{"mestre":"discover","v":1,"nonce":"9f2c…(32 hex)","core_id":"<core_id pareado ou vazio>"}
```

- **Resposta:** o Core responde em unicast para a origem do pedido:

```json
{"mestre":"here","v":1,"nonce":"9f2c…","core_id":"01J…","name":"servidor","port":7443,
 "guardian_port":7444,"addrs":["192.168.1.10"],"ca_fp":"sha256:…"}
```

- **Regras:**
  - O Core só responde se o `core_id` do pedido for o dele ou vier vazio (vazio só serve ao [pareamento](pareamento.md)).
  - O nó só aceita respostas com o `nonce` que enviou.
  - As mensagens têm menos de 512 bytes, e o Core responde a no máximo 10 pedidos por segundo.
  - Campos desconhecidos são ignorados. Com `v` diferente, o pedido é ignorado.

### mDNS

- **O que o Core publica:**
  - o nome `<host>.local`;
  - o serviço `_mestre._tcp`, na porta 7443;
  - o serviço `_mestre-guardiao._tcp`, na porta 7444;
  - TXT com `core_id=<id>` e `v=1`.
- **Quem publica, por sistema:**
  - Core Linux: o **Avahi**, com arquivos de serviço estáticos gravados na instalação;
  - Core Windows: o **Guardião do Core**, pelo mDNS nativo do Windows (`DnsServiceRegister`; o próprio Windows já responde pelo nome `<host>.local`).
- Nos dois casos, quem publica não é o motor: o anúncio continua com o motor do Core fora do ar. Isso ajuda a [recuperação](../autocura/camadas.md).
- **No nó,** a biblioteca de mDNS do binário faz a consulta DNS-SD de `_mestre._tcp.local` e lê o TXT para saber o `core_id`.
- Máquinas Windows (Core ou nó) precisam do perfil de rede "Privada" e das regras de firewall para broadcast e mDNS; o Core Linux, das regras do firewall dele. O [checklist](../plataforma/checklist.md) confere isso.

### Cache

- **Motor do nó:** guarda no próprio SQLite o `core_id` e uma lista de endereços (IP ou nome e porta), cada um com a origem (configurado, broadcast ou mDNS) e o último sucesso.
- **Guardião do nó:** faz a mesma descoberta e mantém o próprio cache num arquivo pequeno, porque não usa o banco do motor ([Guardião](../autocura/guardiao.md)).
- Cada conexão bem-sucedida atualiza o cache. Endereços sem sucesso há 30 dias (padrão) saem da lista.

### Nó fora da rede local

- O nó usa o endereço do Core configurado à mão (`core.endereco`, nome ou IP e porta), informado no instalador ou na configuração do nó.
- Broadcast e mDNS não atravessam redes; valem só o endereço configurado e o cache.
- A checagem de identidade é a mesma. Para o navegador abrir o painel por esse endereço, o nome entra nos nomes extras do certificado do Core ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)).
- Nenhuma porta do Core é exposta diretamente na internet: o caminho até o Core é responsabilidade da rede do usuário.

## Regras e erros

- A descoberta nunca faz o nó confiar num Core novo. Confiança só nasce no [pareamento](pareamento.md).
- Uma resposta forjada só consegue apontar para um endereço que falha no TLS. O nó segue para o próximo candidato.
- Com o endereço configurado e a descoberta desligada (`descoberta: false`), o nó usa só a configuração e o cache.

## Critérios de aceite

- **CA-DESC-01** · *rápida* — **Dado** uma rede UDP falsa, **quando** chegam duas respostas, uma com o `nonce` certo e outra com `nonce` errado, **então** só a primeira vira candidata.
- **CA-DESC-02** · *rápida* — **Dado** um candidato com certificado de outra CA, **quando** o nó testa a identidade, **então** o candidato é descartado e o próximo é tentado.
- **CA-DESC-03** · *rápida* — **Dado** um pedido com outro `core_id`, **quando** chega ao Core, **então** o Core não responde.
- **CA-DESC-04** · *rápida* — **Dado** o broadcast bloqueado e um mDNS falso anunciando `_mestre._tcp`, **quando** o nó descobre, **então** conecta pelo endereço do mDNS.
- **CA-DESC-05** · *rápida* — **Dado** o Core que mudou de IP, **quando** a conexão ao endereço em cache falha 3 vezes, **então** o nó redescobre, conecta e atualiza o cache (MVP-1).
- **CA-DESC-06** · *completa* — **Dado** Core e nó reais na mesma máquina, sem endereço configurado e com cache vazio, **quando** o nó inicia, **então** acha o Core por broadcast e conecta.
- **CA-DESC-07** · *completa* — **Dado** um Core Windows real com o serviço `mestre-core` parado e o Guardião ativo, **quando** outro processo consulta `_mestre._tcp.local`, **então** recebe o anúncio com o `core_id` no TXT.

## Depende de

- [Protocolo](protocolo.md)
- [Pareamento](pareamento.md)
- [Checklist](../plataforma/checklist.md)
- [Instalação e atualização](../plataforma/instalacao-e-atualizacao.md)
- [Windows](../arquitetura/windows.md)

## Verificar na implementação

- **Verificar na implementação:** `DnsServiceRegister`, chamado pelo Guardião (LocalSystem, sessão 0), anuncia `_mestre._tcp` e `_mestre-guardiao._tcp` na rede e mantém o anúncio enquanto o Guardião roda. **Padrão:** o mDNS nativo do Windows, como acima. **Se falhar:** o Guardião do Core publica com uma biblioteca Go de mDNS, escutando a porta 5353 em compartilhamento com o respondedor do Windows. **Onde:** CA-DESC-07 na suíte completa no Windows e instalação dos usos 3 e 4 na suíte pesada.
