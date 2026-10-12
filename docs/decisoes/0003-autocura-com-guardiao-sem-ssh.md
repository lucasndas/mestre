# ADR 0003: Autocura com Guardião local, sem SSH

> **Assunto:** por que cada máquina tem um Guardião com lista fechada de ações em vez de acesso remoto · **Componente:** Guardião · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- A autocura é requisito central. Um lado precisa conseguir recuperar o outro quando o motor cai, trava ou quebra depois de uma atualização, inclusive quando o Core está fora do ar.
- O sistema operacional reinicia um processo que caiu, mas não resolve um processo travado nem um motor que quebra a cada partida.
- A recuperação também não pode exigir que uma máquina tenha acesso total à outra.

## Opções consideradas

1. **SSH do lado saudável para o lado com problema.**
   - Prós: flexível, ferramenta conhecida.
   - Contras:
     - Exige OpenSSH Server no Windows, troca de chaves e porta aberta.
     - Dá shell livre: um erro ou um agente comprometido pode fazer qualquer coisa.
     - Não funciona com o nó atrás de roteador.
     - A spec proíbe.
2. **Só a supervisão do sistema operacional** (systemd com `Restart=always` + watchdog, recuperação de serviço do Windows).
   - Prós: zero código próprio.
   - Contras:
     - Não diagnostica nem volta versão.
     - Não age entre máquinas.
     - Não avisa ninguém com contexto.
3. **Motor se recupera sozinho** (lógica de cura dentro do próprio motor).
   - Prós: um serviço a menos.
   - Contras:
     - Se o motor quebra, ou uma atualização dele dá errado, a cura quebra junto.
     - Não há quem volte a versão.
4. **Guardião local separado, com lista fechada de ações, alcançado sem porta no nó.**
   - Prós:
     - Continua de pé quando o motor quebra.
     - Não existe shell livre.
     - Funciona atrás de roteador porque o Guardião do nó abre conexão de saída.
   - Contras: um segundo serviço para instalar, manter e vigiar.

## Decisão

- **Um Guardião por máquina:** binário `mestre-guardiao`, com serviço, processo, código e versão independentes do motor ([componentes](../arquitetura/componentes.md)).
- **O que ele aceita:** só uma **lista fechada de ações**, nunca shell livre.
- **Supervisão:** o Guardião vigia o motor, e o sistema operacional vigia o Guardião.
- **Como é alcançado:**
  - Guardião de nó: mantém **sua própria conexão de saída** até o Core, com credencial própria.
  - Guardião do Core: escuta numa **porta própria**, separada da do motor, com TLS e as credenciais dos nós.
- **Recuperação em camadas:**
  1. sistema operacional;
  2. Guardião local;
  3. supervisão cruzada com runbook;
  4. diagnóstico com IA, com script aprovado por pessoa;
  5. alerta aos usuários.
- **Onde estão os detalhes:**
  - Lista de ações e o que o Guardião guarda: [Guardião](../autocura/guardiao.md).
  - Camadas, eleição do nó condutor e anti-loop: [camadas](../autocura/camadas.md).
  - Diagnóstico com IA: [diagnóstico com IA](../autocura/diagnostico-com-ia.md).

## Consequências

- **Positivas:**
  - Nenhuma máquina tem acesso amplo à outra.
  - Um motor quebrado por atualização é revertido localmente.
  - A cura funciona fora da rede local sem abrir porta no nó.
- **Negativas:**
  - Dois serviços por máquina. O checklist precisa conferir os dois ([checklist](../plataforma/checklist.md)).
  - O Guardião precisa de exceções explícitas à regra de "não guardar estado do produto". Ele guarda, criptografados, o token do bot e a lista de usuários, porque serve a [página reserva](../autocura/pagina-reserva.md) e o [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md). Isso aumenta a superfície de segredos fora do Core ([cofre](../plataforma/cofre.md)).
  - Problemas fora da lista fechada só se resolvem pelo diagnóstico com IA, com aprovação de Dono ou Administrador, ou por intervenção humana. A cura é mais lenta, mas é a troca aceita pela segurança.
  - O Guardião precisa ser pequeno e estável. Qualquer mudança nele é rara e entra pelo processo normal de atualização ([instalação e atualização](../plataforma/instalacao-e-atualizacao.md)). Nenhuma ação de cura pode alterá-lo.
  - O Core precisa expor mais uma porta (a do Guardião) na rede local, liberada no firewall.
  - O Guardião de cada nó também escuta uma porta, só na rede local, para a [página reserva](../autocura/pagina-reserva.md); a cura em si continua sem porta no nó ([portas](../arquitetura/componentes.md#portas)).
