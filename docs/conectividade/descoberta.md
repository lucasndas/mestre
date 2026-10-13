# Descoberta da Guilda

> **Assunto:** como uma máquina (motor e Guardião) encontra a Guilda e a Cidadela atual na rede local sem configuração: broadcast UDP, mDNS (Avahi no Linux, nativo no Windows), cache e endereço configurado · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Uma instalação nova acha a Guilda sozinha na rede local para pedir a entrada ([Guilda](../guilda/guilda.md#formação)), e uma máquina que já é da Guilda reencontra a **Cidadela atual** quando o IP muda ou quando outra máquina vira Cidadela, sem intervenção do usuário. Vale para máquinas Linux e Windows. Uma máquina fora da rede local usa o endereço configurado à mão (abaixo).

## Comportamento

### Ordem de tentativa

1. **Endereço configurado** (`guild.endereco` na configuração da máquina), se houver.
2. **Cache:** os últimos endereços da Cidadela que funcionaram, do mais recente para o mais antigo, e os endereços das máquinas da Guilda recebidos em `guild.config`.
3. **Broadcast UDP e mDNS, em paralelo,** por até 3 s. Os endereços achados vão para uma lista; a resposta de uma Torre aponta para a Cidadela que ela segue.
4. Para cada endereço achado, a máquina abre TLS e faz a **checagem de identidade**. O primeiro que passa é usado e entra no cache.

- A descoberta roda no início, quando não há endereço que funcione e depois de 3 falhas seguidas de conexão ([reconexão](protocolo.md#heartbeat-e-reconexão)).
- Sem nenhum endereço, a máquina tenta de novo com o mesmo backoff da reconexão e mostra "Cidadela não encontrada" no [checklist](../plataforma/checklist.md).

### Checagem de identidade

- **Descoberta é só uma dica.** Quem garante que é a Cidadela certa é o TLS mais a certidão:
  - o certificado do servidor precisa ter cadeia até a CA da Guilda guardada na entrada;
  - precisa trazer o SAN de URI `mestre://machine/<machine_id>` de uma máquina da Guilda;
  - e essa máquina precisa ser a nomeada na certidão de época mais alta (a que a máquina conhece, ou uma mais alta e válida que a máquina nesse endereço apresenta no `welcome`; [certidão de época](../guilda/replicacao.md#certidão-de-época)).
- O IP e o nome não entram na checagem. Por isso uma mudança de IP não exige certificado novo para ninguém.
- Um endereço que falha na checagem é descartado e registrado em log. A máquina tenta o próximo. Uma máquina que é da Guilda mas não é a Cidadela responde `4007` com a certidão atual, e a máquina segue para a Cidadela nomeada nela.

### Broadcast UDP

- **Porta:** `7445/udp` (padrão, configurável). O motor de toda máquina da Guilda escuta em `0.0.0.0:7445`. As [instâncias](../arquitetura/componentes.md#instâncias) da mesma máquina compartilham a porta (`SO_REUSEADDR`, e `SO_REUSEPORT` no Linux): cada uma recebe o broadcast e responde com o próprio `machine_id`, como uma máquina separada.
- **Pedido:** a máquina envia 3 pedidos, com 1 s de intervalo, para `255.255.255.255` e para o broadcast de cada interface ativa (ex.: `192.168.1.255`):

```json
{"mestre":"discover","v":1,"nonce":"9f2c…(32 hex)","guild_id":"<guild_id ou vazio>"}
```

- **Resposta:** cada máquina da Guilda responde em unicast para a origem do pedido, dizendo o próprio papel e onde está a Cidadela que ela segue:

```json
{"mestre":"here","v":1,"nonce":"9f2c…","guild_id":"01J…","machine_id":"01J…","name":"servidor",
 "role":"cidadela","epoch":2,"port":7443,"citadel":{"machine_id":"01J…","addrs":["192.168.1.10"],"port":7443},
 "machines":3,"ca_fp":"sha256:…"}
```

- **Regras:**
  - Uma máquina só responde se o `guild_id` do pedido for o dela ou vier vazio (vazio só serve à [entrada na Guilda](../guilda/guilda.md#entrada-na-guilda)).
  - A máquina só aceita respostas com o `nonce` que enviou.
  - Com `guild_id` vazio, o instalador agrupa as respostas por `guild_id` para saber quantas Guildas existem e se a Cidadela de cada uma respondeu.
  - As mensagens têm menos de 512 bytes, e cada máquina responde a no máximo 10 pedidos por segundo.
  - Campos desconhecidos são ignorados. Com `v` diferente, o pedido é ignorado.

### mDNS

- **O que o Guardião de toda máquina anuncia**, por instância:
  - o nome `<host>.local`;
  - o serviço `_mestre._tcp`, na porta do motor da instância, com TXT `guild_id=<id>`, `machine_id=<id>`, `instance=<nome>`, `role=cidadela|torre`, `epoch=<n>` e `v=1`;
  - o serviço `_mestre-guardiao._tcp`, na porta do Guardião.
- **Só o Guardião da Cidadela** publica também o nome **`mestre.local`**, o endereço do painel, e só enquanto tem a concessão do motor, que não depende do GitHub; sem ela, ou com certidão mais nova, retira o nome em até 60 s ([concessão do motor](../autocura/guardiao.md#concessão-do-motor)). Uma Cidadela suspensa e uma Torre nunca o publicam ([Cidadela suspensa](../guilda/cidadela-e-torres.md#cidadela-suspensa)).
- **Quem publica, por sistema:**
  - Linux: o **Avahi**, com o arquivo de serviço `/etc/avahi/services/mestre.service` e a entrada de `mestre.local` em `/etc/avahi/hosts`, reescritos pelo Guardião quando o papel muda;
  - Windows: o **Guardião**, pelo mDNS nativo do Windows (`DnsServiceRegister`; o próprio Windows já responde pelo nome `<host>.local`).
- Nos dois casos, quem publica não é o motor: o anúncio continua com o motor fora do ar. Isso ajuda a [recuperação](../autocura/camadas.md).
- **Na consulta,** a biblioteca de mDNS do binário faz a consulta DNS-SD de `_mestre._tcp.local` e lê o TXT.
- Máquinas Windows precisam do perfil de rede "Privada" e das regras de firewall para broadcast e mDNS; as Linux, das regras do firewall delas. O [checklist](../plataforma/checklist.md) confere isso.

### Cache

- **Motor:** guarda no banco local de máquina o `guild_id`, a certidão de época mais alta vista e uma lista de endereços (IP ou nome e porta) da Cidadela e das outras máquinas, cada um com a origem (configurado, broadcast, mDNS ou `guild.config`) e o último sucesso.
- **Guardião:** faz a mesma descoberta e mantém o próprio cache num arquivo pequeno, porque não usa o banco do motor ([Guardião](../autocura/guardiao.md)).
- Cada conexão bem-sucedida atualiza o cache. Endereços sem sucesso há 30 dias (padrão) saem da lista.

### Máquina fora da rede local

- A máquina usa o endereço configurado à mão (`guild.endereco`, nome ou IP e porta), informado no instalador (`mestre install --join <endereço>`) ou na configuração.
- Broadcast e mDNS não atravessam redes; valem só o endereço configurado, o cache e os endereços de `guild.config`. Depois de uma troca de Cidadela, a máquina tenta os endereços das máquinas recebidos em `guild.config`.
- A checagem de identidade é a mesma. Para o navegador abrir o painel por esse endereço, o nome entra nos nomes extras do certificado das máquinas não excluídas ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)).
- Uma máquina fora da rede local não é alcançada por nenhuma outra, então **não pode ser promovida** a Cidadela ([promoção](../guilda/cidadela-e-torres.md#promoção)).
- Nenhuma porta é exposta diretamente na internet: o caminho até a Cidadela é responsabilidade da rede do usuário.

A CA, o `guild_id` e a certidão usados na checagem vêm da [entrada na Guilda](../guilda/guilda.md); os arquivos do Avahi e as regras de firewall, da [instalação](../plataforma/instalacao-e-atualizacao.md) e do [checklist](../plataforma/checklist.md).

## Regras e erros

- A descoberta nunca faz uma máquina confiar em algo novo. Confiança só nasce na entrada na Guilda.
- Uma resposta forjada só consegue apontar para um endereço que falha no TLS ou na certidão. A máquina segue para o próximo endereço.
- Com o endereço configurado e a descoberta desligada (`descoberta: false`), a máquina usa só a configuração e o cache.

## Critérios de aceite

- **CA-DESC-01** · *rápida* — **Dado** uma rede UDP falsa, **quando** chegam duas respostas, uma com o `nonce` certo e outra com `nonce` errado, **então** só a primeira entra na lista.
- **CA-DESC-02** · *rápida* — **Dado** um endereço com certificado de outra CA, **quando** a máquina testa a identidade, **então** o endereço é descartado e o próximo é tentado.
- **CA-DESC-03** · *rápida* — **Dado** um pedido com outro `guild_id`, **quando** chega a uma máquina da Guilda, **então** ela não responde.
- **CA-DESC-04** · *rápida* — **Dado** o broadcast bloqueado e um mDNS falso anunciando `_mestre._tcp` com `role=cidadela`, **quando** a máquina descobre, **então** conecta pelo endereço do mDNS.
- **CA-DESC-05** · *rápida* — **Dado** a Cidadela que mudou de IP, **quando** a conexão ao endereço em cache falha 3 vezes, **então** a máquina redescobre, conecta e atualiza o cache (MVP-1).
- **CA-DESC-06** · *completa* — **Dado** a Cidadela e uma Torre reais na mesma máquina, sem endereço configurado e com cache vazio, **quando** a Torre inicia, **então** acha a Cidadela por broadcast e conecta.
- **CA-DESC-07** · *pesada* — **Dado** uma Cidadela Windows instalada com o serviço `mestre` parado e o Guardião ativo, **quando** outra máquina consulta `_mestre._tcp.local` e `mestre.local`, **então** recebe o anúncio com o `guild_id` e `role=cidadela` no TXT e o endereço do painel (MVP-13).
- **CA-DESC-08** · *rápida* — **Dado** uma Torre que segue a época 1 e uma resposta de outra Torre apontando a Cidadela da época 2, **quando** a primeira descobre, **então** conecta na Cidadela da época 2 e guarda a certidão nova; **dado** só a antiga Cidadela respondendo com a época 1, **então** a Torre não conecta nela.
- **CA-DESC-09** · *completa* — **Dado** três instâncias na mesma máquina, uma Cidadela e duas Torres, **quando** uma instância nova da mesma máquina faz a descoberta, **então** recebe três respostas com `machine_id` diferentes pela mesma porta 7445 e acha a Cidadela.

## Depende de

- [Protocolo](protocolo.md) (reconexão)
- [Dublês](../qualidade/dubles.md) (broadcast e mDNS falsos)
- [Segurança](../arquitetura/seguranca.md) · [Windows](../arquitetura/windows.md)

## Verificar na implementação

- **Verificar na implementação:** `DnsServiceRegister`, chamado pelo Guardião (LocalSystem, sessão 0), anuncia `_mestre._tcp` e `_mestre-guardiao._tcp`, publica o nome `mestre.local` com o endereço da máquina e mantém os anúncios enquanto o Guardião roda; retirá-los ao virar Torre funciona sem reiniciar. **Padrão:** o mDNS nativo do Windows, como acima. **Se falhar:** o Guardião publica com uma biblioteca Go de mDNS, escutando a porta 5353 em compartilhamento com o respondedor do Windows. **Onde:** CA-DESC-07 e a instalação das composições com Cidadela Windows na suíte pesada.
- **Verificar na implementação:** duas instâncias ligadas à mesma `7445/udp` recebem as duas o broadcast, no Linux e no Windows. **Padrão:** `SO_REUSEADDR` (e `SO_REUSEPORT` no Linux) em todas. **Se falhar:** cada instância escuta a porta de descoberta do próprio bloco (7445, 7455, 7465…) e o pedido vai às portas dos três primeiros blocos. **Onde:** CA-DESC-09, na completa do CI Linux e Windows.
