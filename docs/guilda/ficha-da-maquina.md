# Ficha da máquina

> **Assunto:** o que cada máquina informa à Guilda (ficha da máquina), como a ficha se atualiza sozinha, os requisitos de bases, fichas e classes e o casamento requisito × ficha · **Componente:** Motor · **Atualizado:** 2026-10-15

## Objetivo

Cada máquina da Guilda se apresenta com uma **ficha** atualizada sozinha, e cada personagem declara do que precisa (**requisitos**). A Cidadela só põe um personagem numa máquina cuja ficha atende a todos os requisitos; se nenhuma atende, avisa o Dono em vez de rodar em outra. Nada no código pressupõe um sistema para cada papel: a escolha sai só da ficha.

## Comportamento

### Campos

| Campo | Conteúdo | Quem preenche |
|---|---|---|
| `rev` | número da revisão da ficha, sempre crescente ([atualização](#atualização-sozinha)) | motor |
| `machine_id`, `name`, `host`, `instance` | identificador e nome da máquina na Guilda, nome do computador e nome da [instância](../arquitetura/componentes.md#instâncias); `machine_id` e `name` são da Guilda, o motor manda `host` e `instance` | entrada na [Guilda](guilda.md) e motor |
| `os`, `os_version`, `arch` | `linux` \| `windows`, versão (ex.: Ubuntu 26.04, Windows 11 24H2), `amd64` | motor |
| `gui` | `true` quando há uma sessão de usuário com área de trabalho na máquina (Windows: usuário com sessão ativa ou desconectada; Linux: sessão gráfica de um usuário no systemd-logind, tipo `x11` ou `wayland`, ou servidor gráfico escutando quando o serviço enxerga o socket, o que `PrivateTmp` e o `/run/user/<uid>` 0700 não garantem); `false` quando roda sem tela | motor |
| `ips[]` | endereços IPv4 da máquina na rede local (sem loopback nem link-local); a Cidadela os passa ao registro de nomes e o certificado os acompanha ([credenciais](guilda.md#credenciais)) | motor |
| `providers[]` | `{provider, profiles: [{name, version, logged_in, conformant, default}]}` (`default` marca o perfil que uma ficha sem `account` usa) para `claude-code`, `codex` e `antigravity` instalados; versão e conformidade são por máquina e [perfil de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta) ([conformidade](../provedores/conformidade.md)) | motor, com o resultado do [checklist](../plataforma/checklist.md) e da conformidade |
| `browser` | `{name, version}` do navegador dos MCPs de navegador dos personagens (Chrome; senão, no Windows, o Edge, e no Linux, o Chromium; a versão no Windows vem do registro da máquina, HKLM, e só por último do da conta do serviço), ou ausente | motor |
| `operador` | `{enabled, ready, reason}`: **habilitado** (`enabled`) quando o [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) está instalado e ligado pelo Dono nesta máquina; **pronto** (`ready`) quando, além disso, o processo da sessão está ativo, a tela desbloqueada e os itens de UAC em dia; senão `reason` (`sem_usuario`, `tela_bloqueada`, `uac`, `tarefa`) | motor, pelo processo da sessão |
| `resources` | `cpu_cores`, `mem_bytes`, `disk_free_bytes` (do volume dos dados da máquina), `gpus[]` (modelo e, quando o sistema informa, memória); adaptador virtual (Hyper-V, VMware, VirtIO, QXL, "Basic Display") não conta como GPU | motor |
| `transcricao` | `true` com o `whisper-server` verificado ([anexos e transcrição](../telegram/anexos-e-transcricao.md)) | motor |
| `tools` | ferramentas instaladas, com versão (`git`, `ffmpeg`); a que falta fica de fora | motor |
| `excluded` | se o Dono excluiu a máquina (sem segredos, fora da troca de Cidadela; [máquina excluída](guilda.md#máquina-excluída)) | o Dono, no painel; a ficha só mostra |
| `online` | se a máquina está conectada, com heartbeat em dia | a Cidadela, pela conexão ([protocolo](../conectividade/protocolo.md#heartbeat-e-reconexão)) |

### Atualização sozinha

- A ficha vai inteira no `hello` de cada conexão da máquina com a Cidadela ([mensagens](../conectividade/mensagens.md)) e **substitui** a guardada.
- Depois, a máquina manda `machine.sheet` só com os campos que mudaram (um campo que sumiu vai como `null`), quando percebe a mudança: login de um perfil feito ou perdido, CLI de provedor atualizado ou conformidade refeita, Operador habilitado ou pronto, `whisper-server` que falhou, IPs ou disco. O motor confere os campos a cada 5 min (padrão) e logo depois de cada rodada do checklist ou de qualquer um desses eventos. Nada mudou, nada é enviado.
- **Disco:** `disk_free_bytes` só conta como mudado quando passa de 10% do último valor enviado (padrão); senão cada conferência geraria uma mensagem.
- **Leitura que falha** (o monitor de login reiniciando, por exemplo) mantém o valor anterior daquela parte: a falha não vira "perdi todos os logins". Vale também na subida do motor: a última ficha enviada fica em `maquina/ficha.json` e é o ponto de partida da próxima execução, para um monitor que ainda não responde não apresentar a máquina sem provedores.
- **`rev`:** o `hello` e cada `machine.sheet` levam um `rev` acima de todos os anteriores (acompanha o relógio, mas nunca fica abaixo da última revisão enviada, que vai para o disco com a ficha: um relógio atrasado depois de reiniciar não faz as mensagens parecerem mais velhas que a ficha guardada). A Cidadela descarta, confirmando, um `machine.sheet` cujo `rev` não passa do guardado, e assim uma mensagem que esperou na fila de saída não desfaz a ficha mais nova de um `hello` posterior; sem `rev`, é `invalid_payload`.
- A ficha tem no máximo 64 KiB; acima disso o `hello` ou o `machine.sheet` recebe `invalid_payload`. Uma ficha é de poucos KiB, e o limite mantém a linha da máquina, que vai no [registro de mudanças](replicacao.md#banco-cópia-completa-e-envio-contínuo), longe do quadro de 1 MiB, de que um registro maior só chega às Torres por cópia completa.
- A Cidadela guarda a última ficha de cada máquina no banco principal (`machines.sheet`, uma linha por máquina) e a mostra no painel ([administração](../painel/administracao.md)). Ao entregá-la, acrescenta o que é dela: `machine_id`, `name`, `excluded` e `online` (o que a máquina mandar nesses campos é ignorado). O nome do computador e os IPs da ficha vão ao registro de nomes, e o certificado acompanha. Uma máquina offline mantém a última ficha, com `online: false`.
- A máquina que é a Cidadela não tem conexão consigo mesma: o motor dela grava a própria ficha direto no registro, e a confere a cada 5 min como as outras (o mesmo agente, com o registro no lugar da conexão, só enquanto a Cidadela age), e ela aparece sempre online.

### Requisitos

Bases, fichas de personagem e classes podem declarar `requires` ([bases e fichas](../orquestracao/bases-e-fichas.md), [classes](../classes/classes.md)). Os requisitos de um personagem são a **soma** dos três; a ficha também pede, pelo campo `account`, o perfil de conta que vai usar.

| Requisito | Atendido quando a ficha da máquina tem |
|---|---|
| `os: linux` \| `os: windows` | o mesmo `os` (as `platforms` da base também restringem) |
| `gpu: true` | ao menos uma GPU em `resources.gpus` |
| `mem_min: <bytes>` | `resources.mem_bytes` maior ou igual |
| `provider: <p>` | o provedor `<p>` instalado e o perfil de conta pedido pela ficha (ou o padrão) com `logged_in: true` e `conformant: true` na versão atual daquele perfil |
| `browser: true` | `browser` presente |
| `transcricao: true` | `transcricao: true` |
| `operador: true` | `operador.enabled: true` (a ativação espera `ready`, [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc)) |
| `gui: true` | `gui: true` **e** o personagem é da base `operador` (abaixo) |

- O provedor da ficha (`agent`) vira sozinho o requisito `provider`; não é preciso repeti-lo. Dois valores que não podem valer juntos (`os: linux` numa base e `os: windows` na classe, ou `provider` contra `agent`) são recusados ao salvar; `mem_min` soma pelo maior.
- **Perfil padrão:** sem `account`, vale o perfil com `default: true` do provedor, ou o único que a máquina tem; com vários e nenhum padrão, a máquina não atende ("sem perfil de conta padrão").
- Requisito desconhecido, valor de tipo errado (`mem_min` é um inteiro de bytes) e `provider` fora de `claude-code`, `codex` e `antigravity` são recusados ao salvar a base, a ficha ou a classe, com a lista dos aceitos.

### Casamento e escolha da máquina

- **Casamento:** uma máquina atende a um personagem quando está online e atende a **todos** os requisitos dele. O resultado, para cada máquina, é "atende" ou o **primeiro requisito não atendido**, em texto curto (ex.: "windows-pc: perfil de conta `claude-trabalho` não está logado"). A ordem da conferência é a da tabela acima, com "offline" na frente, e a primeira que falha é a dita.
- **Quem escolhe:** a Cidadela, ao criar o personagem, entre as máquinas que atendem; a ordem de preferência e a espera por vaga (`waiting_slot`) são de [personagens](../orquestracao/personagens.md#escolha-da-máquina).
- **Nenhuma atende:** o personagem **não roda em outra máquina**. A criação falha com o motivo de cada máquina, e o Dono é avisado pelo Telegram e pelo painel, com o que falta em cada uma; quem pediu (o Mestre da mesa, por exemplo) recebe o mesmo motivo ([coordenação](../cli/coordenacao.md)).
- **Durante a vida do personagem:** se a máquina deixa de atender (o login do perfil caiu, por exemplo), o personagem continua até o próximo reinício; ao reiniciar, a escolha é feita de novo, e o aviso ao Dono vale do mesmo jeito.
- O Grão-Mestre roda sempre na Cidadela ([Cidadela e Torres](cidadela-e-torres.md)); o checklist avisa quando uma máquina não excluída não atende aos requisitos da ficha dele.

### No código

`internal/machinesheet`. A máquina monta a ficha a partir de sondas pequenas atrás de interfaces (sistema, provedores, Operador, navegador, ferramentas, transcrição): a sonda do sistema é real e por sistema (Linux: `/etc/os-release`, `/proc/meminfo`, `/sys/bus/pci`; Windows: registro e API do sistema), e as de provedores, Operador e transcrição são entregues pelos marcos que as criam. A versão de um programa (o navegador no Linux, `git`, `ffmpeg`) vem de um `Runner` que o motor liga às camadas que iniciam processos; o pacote não inicia nenhum. A Cidadela registra o tratador de `machine.sheet` e o gancho do `hello`, e responde "quem atende" com o casamento acima. As classes leem os `requires` ao salvar (CA-FMAQ-04, `internal/classes`); bases e fichas em `internal/orchestration/presets` e os perfis de conta em `internal/orchestration/accounts` (CA-FMAQ-03, fechado no M4).

## Regras e erros

- **"Precisa de interface"** (`gui: true`) é atendido **só** pela base do [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc): só é aceito numa base `operador` e nas fichas dela; em qualquer outra base, ficha ou classe, é recusado ao salvar. Fora do Operador não há agentes com tela ([ADR 0020](../decisoes/0020-linux-e-windows-em-qualquer-papel.md)).
- Uma ficha recebida com campo desconhecido é aceita (o campo é ignorado); sem os campos obrigatórios (`os`, `arch`, `providers`), a conexão recebe `invalid_payload`, e um `machine.sheet` que deixaria a ficha assim também. Máquina fora da Guilda (removida, ou que nunca entrou) recebe `forbidden`.
- A ficha nunca leva segredos: perfis de conta aparecem só pelo nome e pelo estado do login.

## Critérios de aceite

- **CA-FMAQ-01** · *rápida* — **Dado** uma máquina conectada, **quando** o login de um perfil de conta cai, **então** ela manda `machine.sheet` só com o provedor mudado e a Cidadela grava a ficha nova.
- **CA-FMAQ-02** · *rápida* — **Dado** uma máquina Linux com GPU e uma Windows sem GPU, **quando** se casa um personagem que exige `os: windows` e `gpu: true`, **então** nenhuma atende e o resultado traz o primeiro requisito não atendido de cada uma.
- **CA-FMAQ-03** · *rápida* — **Dado** uma ficha `agent: codex` com o perfil `codex-pessoal`, **quando** a máquina tem o Codex instalado mas o perfil com `logged_in: false`, ou logado e com `conformant: false`, **então** ela não atende, com o motivo de cada caso.
- **CA-FMAQ-04** · *rápida* — **Dado** uma classe com `requires: {gui: true}` ou `requires: {cor: azul}`, **quando** é salva, **então** é recusada com o motivo.
- **CA-FMAQ-05** · *rápida* — **Dado** uma máquina offline que atenderia, **quando** se casa o personagem, **então** ela não atende, com o motivo "offline".
- **CA-FMAQ-06** · *completa* — **Dado** a Cidadela e uma Torre reais na mesma máquina, **quando** a Torre conecta, **então** a ficha dela no banco principal traz o sistema, a memória, os núcleos reais da máquina e o nome da instância (MVP-1).

## Depende de

- [Protocolo](../conectividade/protocolo.md) · [Guilda](guilda.md)
