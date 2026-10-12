# Instalação e versões

> **Assunto:** visão técnica da instalação no Ubuntu e no Windows, como Core ou como nó, pastas de versões, versão anterior guardada, volta de versão, modelo Whisper e remoção · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Definir o que a instalação cria em cada máquina e como as versões ficam no disco, para que atualizar e voltar de versão seja só trocar um apontamento. O passo a passo para quem usa está em [instalar](../operacao/instalar.md), [primeiro acesso](../operacao/primeiro-acesso.md), [parear nó](../operacao/parear-no.md), [atualizar](../operacao/atualizar.md) e [desinstalar](../operacao/desinstalar.md). O fluxo de atualização (descoberta, assinatura, aprovação, ordem) está em [atualização automática](autoatualizacao.md).

## Comportamento

### Pacotes

| Plataforma | Artefato | O que faz |
|---|---|---|
| Ubuntu (LTS atual) | `mestre_<versão>_amd64.deb` (depende de `ffmpeg` e `avahi-daemon`), mais um `install.sh` que baixa o `.deb` das [Releases](https://github.com/lucasndas/mestre/releases), confere o SHA-256 e roda `apt install` | coloca os binários em `/usr/lib/mestre/pacote/`, pergunta o papel e chama `mestre install core` ou `mestre install node` |
| Windows 11 ou superior | instalador `mestre-<versão>-x64.msi` (MSI gerado com WiX v4), elevado, com o `ffmpeg.exe` numa versão fixada | copia os binários, pergunta o papel e chama `mestre install core` ou `mestre install node` |

- **Papel da máquina:** os dois instaladores perguntam primeiro: **"Core"** (o servidor principal, que já inclui o nó local) ou **"nó"** (pareia com um Core existente). Os quatro usos saem dessa escolha em cada máquina ([ADR 0020](../decisoes/0020-quatro-usos-core-em-linux-ou-windows.md)); no `install.sh`, também por `--papel core|no`.
- A lógica toda fica nos subcomandos `mestre install core|node` ([componentes](../arquitetura/componentes.md#binários)); o pacote e o instalador são finos.
- **Sem assinatura de código Authenticode:** o MSI e os `.exe` são publicados sem ela. O SHA-256 de cada artefato é publicado na Release, e o guia de [instalação](../operacao/instalar.md) explica o aviso do SmartScreen. As versões seguintes chegam pelo manifesto assinado da [atualização automática](autoatualizacao.md), fora do `dpkg` e do instalador.

### Pastas de versões

| | Linux | Windows |
|---|---|---|
| Motor | `/opt/mestre/versoes/<versão>/mestre` | `%ProgramFiles%\Mestre\versoes\<versão>\mestre.exe` |
| Guardião | `/opt/mestre-guardiao/versoes/<versão>/mestre-guardiao` | `%ProgramFiles%\Mestre Guardiao\versoes\<versão>\mestre-guardiao.exe` |
| Versão em uso | link `atual` → `versoes/<versão>` | junção de diretório `atual` → `versoes\<versão>` |
| Versão anterior | link `anterior` → `versoes/<versão anterior>` | junção `anterior` |
| No PATH | `/usr/local/bin/mestre` → `/opt/mestre/atual/mestre` | `%ProgramFiles%\Mestre\atual` |

- Os serviços apontam sempre para `atual`. Trocar de versão é trocar `atual` e `anterior` e reiniciar o serviço; o binário em execução nunca é sobrescrito.
- Ficam no disco só a versão atual e a anterior; a mais velha é apagada depois que a nova é confirmada.
- Dados, configuração e logs ficam nas pastas de [persistência](../arquitetura/persistencia.md#arquivos), fora das pastas de versões.

### Core no Linux (`mestre install core`)

1. Registra a versão do Ubuntu (o [checklist](checklist.md) acompanha).
2. Cria as contas `mestre` e `mestre-agente` e as pastas de dados com dono e permissão ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)).
3. Gera a chave do [cofre](cofre.md#chave-do-cofre) e cria o banco ([persistência](../arquitetura/persistencia.md)).
4. Cria a CA interna, o certificado do servidor do Core e o certificado próprio do Guardião do Core ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)); a chave da CA vai para o cofre.
5. Instala as unidades systemd `mestre-core`, `mestre-node` (o nó local, com a conta `mestre`, que inicia os agentes como `mestre-agente`, e banco de nó próprio), `mestre-guardiao` e o ajudante de troca `mestre-update` ([atualização automática](autoatualizacao.md)), com as opções de reinício de [camadas](../autocura/camadas.md#valores-padrão), e os arquivos de serviço do Avahi ([descoberta](../conectividade/descoberta.md#mdns)).
6. Se o `ufw` estiver ativo, libera as portas do Core ([portas](../arquitetura/componentes.md#portas)).
7. Pareia sozinho o nó local com o Core, em `127.0.0.1:7443` ([pareamento](../conectividade/pareamento.md#convite)), e baixa e prepara o Whisper no nó local (abaixo).
8. Inicia os serviços e mostra no terminal o endereço do painel, a impressão da CA (para conferir no navegador) e o **código de configuração** do assistente do Dono ([login](../painel/login.md#assistente-do-dono-primeira-vez)).
9. Orienta guardar uma cópia da chave do cofre fora do servidor.

### Core no Windows (`mestre install core`, elevado)

1. Registra a versão do Windows e pede a senha da conta do usuário, validada com `LogonUser` ([Windows](../arquitetura/windows.md#conta-do-usuário-fazer-logon-como)).
2. Registra o serviço `mestre-core` com a conta virtual `NT SERVICE\MestreCore` e cria as pastas de dados com as ACLs de [persistência](../arquitetura/persistencia.md#arquivos).
3. Gera a chave do [cofre](cofre.md#chave-do-cofre) (DPAPI de máquina, ACL só da conta virtual), cria o banco, a CA interna, o certificado do servidor do Core e o certificado próprio do Guardião do Core.
4. Registra o `mestre-node` local com a conta do usuário e banco de nó próprio, e o pareia sozinho com o Core em `127.0.0.1:7443` ([pareamento](../conectividade/pareamento.md#convite)).
5. Registra `mestre-guardiao` (LocalSystem), o ajudante `mestre-update` (sob demanda) e a tarefa `\Mestre\Diagnostico` (como no nó Windows, abaixo), com a recuperação de [camadas](../autocura/camadas.md#valores-padrão).
6. Cria as regras de firewall das portas do Core, só no perfil Privado ([portas](../arquitetura/componentes.md#portas)).
7. Baixa e prepara o Whisper no nó local (abaixo); o `ffmpeg.exe` já vem no instalador.
8. Inicia os serviços, mostra o endereço do painel, a impressão da CA e o código de configuração, e orienta a cópia da chave do cofre por `mestre core export-key`.

Nos dois sistemas, os Apps fixos "Grão-Mestre" e "Mestre" nascem no primeiro acesso, pelo link de criação ([criação de Apps](../github/criacao-de-apps.md)).

### Nó no Windows (`mestre install node`, elevado)

1. Encontra o Core pela [descoberta](../conectividade/descoberta.md) ou pelo endereço informado, e faz o [pareamento](../conectividade/pareamento.md) com o código de convite. Pergunta também se o nó vai transcrever áudio (padrão: não; abaixo).
2. Pede a senha da conta do usuário e configura o serviço `mestre-node` com "Fazer logon como" ([Windows](../arquitetura/windows.md#conta-do-usuário-fazer-logon-como)).
3. Registra os serviços `mestre-node` e `mestre-guardiao` (início automático, recuperação de [camadas](../autocura/camadas.md#valores-padrão)) e o ajudante `mestre-update` (sob demanda).
4. Cria a tarefa agendada **`\Mestre\Diagnostico`**: roda como o usuário, conectado ou não, sem gatilho (disparada pelo Guardião) ([Windows](../arquitetura/windows.md#tarefa-mestrediagnostico)). Usa a mesma senha do passo 2, pedida uma vez só; quem a guarda é o Agendador de Tarefas, e o Mestre não a grava. Refazer a configuração da conta (checklist WIN-06) refaz as duas.
5. Cria as regras de firewall da descoberta e da [página reserva](../autocura/pagina-reserva.md), só no perfil Privado.
6. Cria as pastas de dados com ACL ([persistência](../arquitetura/persistencia.md#arquivos)), inicia os serviços, roda o [checklist](checklist.md) e o [teste de conformidade](../provedores/conformidade.md) de cada provedor encontrado.

### Nó no Linux (`mestre install node`)

1. Registra a versão do Ubuntu; encontra o Core e faz o pareamento, como no Windows.
2. Cria as contas `mestre` e `mestre-agente` e as pastas de dados ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)).
3. Instala as unidades systemd `mestre-node`, `mestre-guardiao` e `mestre-update`, com as opções de [camadas](../autocura/camadas.md#valores-padrão).
4. Se o `ufw` estiver ativo, libera a porta da página reserva só para a rede local.
5. Inicia os serviços e roda o [checklist](checklist.md) e o [teste de conformidade](../provedores/conformidade.md) de cada provedor encontrado.

Nas atualizações, a tarefa `\Mestre\Diagnostico` e as regras de firewall são preservadas.

### Whisper

- **Qual nó transcreve:** o nó local do Core, por padrão, preparado na instalação do Core. Outro nó passa a transcrever por escolha no instalador dele ou depois, pelo painel ("Saúde dos nós" → "Ativar transcrição", Dono ou Administrador; [administração](../painel/administracao.md)); a escolha fica na configuração do nó, no banco do Core. Desativar apaga os arquivos e o nó deixa de anunciar a capacidade. Como o trabalho é distribuído: [anexos e transcrição](../telegram/anexos-e-transcricao.md).
- **Arquivos:** o Core baixa o `whisper-server` (Linux ou Windows) e os modelos `large-v3-turbo` q5_0 e q8_0 ([ADR 0016](../decisoes/0016-whisper-cpp-local.md)) e os entrega ao nó de transcrição pela conexão WSS (transferência em partes, [protocolo](../conectividade/protocolo.md)); os nós não acessam a internet para isso. O `whisper-server` é compilado pelo próprio Mestre (whisper.cpp numa versão fixada) e publicado nas Releases do Mestre, sem depender do que o projeto whisper.cpp publica. Endereços e SHA-256 de cada arquivo vêm fixos na versão do Mestre; arquivo com hash diferente é apagado e baixado de novo (3 tentativas).
- **Benchmark:** transcreve um áudio de teste embutido (30 s, em português) com cada quantização, usando tantas threads quanto os núcleos físicos. Fica com **q8_0 se levar até 0,3 do tempo do áudio** (padrão); senão, q5_0. O outro modelo é apagado e a escolha fica na configuração.
- Arquivos na pasta `whisper` de [persistência](../arquitetura/persistencia.md#arquivos). Benchmark feito → o nó passa a anunciar `transcricao: true`. Falhou → anuncia `transcricao` falso e o checklist acusa.

### Versão de protocolo

Cada binário declara a faixa de versões de protocolo que entende (`mestre version`). O Core aceita a atual e a anterior, para nós ainda não atualizados conviverem ([protocolo](../conectividade/protocolo.md#abertura-versão-e-capacidades)). Um nó fora da faixa recebe `4003` e precisa da instalação manual da versão nova.

### Volta de versão

- **Só binário (padrão):** troca `atual` com `anterior` e reinicia. Funciona porque as migrações do banco são só aditivas dentro de uma versão maior ([persistência](../arquitetura/persistencia.md)). É a ação `rollback` do [Guardião](../autocura/guardiao.md#lista-fechada-de-ações).
- **Com restauração do banco:** antes de migrar, o motor grava `pre-migracao-<versão>.db`. Restaurar essa cópia é raro e nunca automático: exige Dono ou Administrador com PIN e perde tudo o que aconteceu entre a atualização e a volta ([Guardião](../autocura/guardiao.md#rollback-com-restauração-do-banco)).
- Depois de uma volta, `anterior` passa a apontar para a versão que falhou, marcada como "não usar"; ela só volta a ser aplicada por uma atualização aprovada de novo ([atualização automática](autoatualizacao.md)).

### Remoção (`mestre uninstall`)

Para e remove serviços, unidades, a tarefa agendada, regras de firewall criadas pela instalação e as pastas de versões. Dados, banco, backups e chave do cofre ficam, a não ser que o usuário peça para apagar tudo. A credencial do nó deve ser revogada no painel ([pareamento](../conectividade/pareamento.md)).

## Regras e erros

- `mestre install` recusa rodar sem privilégio de administrador e com `MESTRE_AGENT_ID` no ambiente ([componentes](../arquitetura/componentes.md#binários)).
- Rodar de novo numa máquina já instalada não duplica nada: confere e completa o que falta (contas, pastas, serviços, tarefas, regras).
- Falha no meio da instalação → mostra o passo que falhou e como repetir; nada do que já foi criado é apagado sozinho.
- O Guardião nunca é trocado por ação de cura ([Guardião](../autocura/guardiao.md#proteções)).

## Critérios de aceite

- **CA-INST-01** · *pesada* — **Dado** um Ubuntu e um Windows 11 limpos, **quando** instalo o Core, crio o Dono e instalo o nó, **então** o nó encontra o Core na rede local, pareia e aparece conectado no painel (MVP-1).
- **CA-INST-02** · *rápida* — **Dado** as versões 1.2.0 (atual) e 1.1.0 (anterior) num sistema de arquivos falso, **quando** roda a volta só de binário, **então** `atual` aponta para 1.1.0 e nenhum arquivo do banco muda.
- **CA-INST-03** · *rápida* — **Dado** um modelo baixado com SHA-256 diferente, **quando** a instalação confere, **então** apaga e baixa de novo, até 3 vezes, e depois marca `transcricao` falso.
- **CA-INST-04** · *rápida* — **Dado** o benchmark com q8_0 a 0,25 do tempo do áudio, **quando** termina, **então** fica q8_0 e o modelo q5_0 é apagado.
- **CA-INST-05** · *pesada* — **Dado** uma máquina de cada sistema já instalada, **quando** `mestre install node` roda de novo, **então** nenhum serviço, tarefa ou regra fica duplicado (MVP-13).
- **CA-INST-06** · *pesada* — **Dado** a instalação concluída numa máquina Windows (Core ou nó), **quando** listo as tarefas agendadas, **então** só `\Mestre\Diagnostico` existe, com as opções acima; depois de `mestre uninstall`, não existe (MVP-13).
- **CA-INST-07** · *pesada* — **Dado** máquinas limpas, **quando** instalo cada um dos quatro usos (só Linux, Linux + Windows, Windows + Linux, só Windows), escolhendo o papel no instalador, **então** em cada um o painel abre, o Dono é criado, o nó local e os nós remotos aparecem conectados, e um personagem de teste roda em cada máquina (MVP-13).
- **CA-INST-08** · *pesada* — **Dado** a instalação do Core concluída no Ubuntu e no Windows, **quando** listo os serviços, **então** existem `mestre-core` e o nó local `mestre-node`, pareado sozinho e conectado em `127.0.0.1:7443`; no Windows, `mestre-core` roda como `NT SERVICE\MestreCore` e `mestre-node` e os terminais com a conta do usuário; no Linux, `mestre-core` e `mestre-node` como `mestre` e os terminais como `mestre-agente` (MVP-13).
- **CA-INST-09** · *rápida* — **Dado** um nó sem transcrição e dublês de rede e relógio, **quando** um Administrador ativa a transcrição dele no painel, **então** o Core envia os arquivos por transferência, o nó roda o benchmark e passa a anunciar `transcricao: true`; com um arquivo de hash errado, nada é anunciado.
- **CA-INST-10** · *pesada* — **Dado** a instalação concluída no Windows (Core e nó), **quando** listo a ACL de `%ProgramData%\Mestre\` e de cada subpasta, **então** a herança está desligada e não há entrada para `Users`, `Authenticated Users` nem `Everyone`; uma conta comum de teste da máquina não consegue listar a pasta (MVP-13).

## Depende de

- [Componentes](../arquitetura/componentes.md)
- [Persistência](../arquitetura/persistencia.md)
- [Segurança](../arquitetura/seguranca.md)
- [Windows](../arquitetura/windows.md)
- [Cofre](cofre.md)
- [Pareamento](../conectividade/pareamento.md)
- [Descoberta](../conectividade/descoberta.md)
- [Protocolo](../conectividade/protocolo.md)

## Fontes

- whisper.cpp: https://github.com/ggml-org/whisper.cpp
- Serviços em Go (systemd e Windows): https://pkg.go.dev/github.com/kardianos/service
