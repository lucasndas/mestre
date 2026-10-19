# Instalação e versões

> **Assunto:** visão técnica do instalador único no Ubuntu e no Windows (descobre a Guilda e entra, ou cria), pastas de versões, versão anterior guardada, volta de versão, CLIs dos provedores, modelo Whisper e remoção · **Componente:** Todos · **Atualizado:** 2026-10-19

## Objetivo

Definir o que a instalação cria em cada máquina e como as versões ficam no disco, para que atualizar e voltar de versão seja só trocar um apontamento. Toda máquina recebe a mesma instalação, porque qualquer uma pode virar Cidadela. O passo a passo para quem usa está em [instalar](../operacao/instalar.md), [primeiro acesso](../operacao/primeiro-acesso.md), [entrar na Guilda](../operacao/entrar-na-guilda.md), [atualizar](../operacao/atualizar.md) e [desinstalar](../operacao/desinstalar.md). O fluxo de atualização (descoberta, assinatura, aprovação, ordem) está em [atualização automática](autoatualizacao.md).

## Comportamento

### Pacotes

| Plataforma | Artefato | O que faz |
|---|---|---|
| Ubuntu (LTS atual) | `mestre_<versão>_amd64.deb` (depende de `ffmpeg` e `avahi-daemon`), mais um `install.sh` que baixa o `.deb` das [Releases](https://github.com/lucasndas/mestre/releases), confere o SHA-256 e roda `apt install` | coloca os binários em `/usr/lib/mestre/pacote/` e chama `mestre install` |
| Windows 11 ou superior | instalador `mestre-<versão>-x64.msi` (MSI gerado com WiX v4), elevado, com o `ffmpeg.exe` numa versão fixada | copia os binários e chama `mestre install`; com a propriedade `JOIN=<endereço>` (`msiexec /i mestre-<versão>-x64.msi JOIN=<endereço>`), chama `mestre install --join <endereço>` |

- **Sem papel para escolher:** o instalador é o mesmo em toda máquina. Ele procura uma Guilda na rede e **entra** nela, ou, sem nenhuma, **cria** a Guilda e a máquina vira a Cidadela ([formação](../guilda/guilda.md#formação)). As quatro composições (só Linux, Cidadela Linux + Torre Windows, Cidadela Windows + Torre Linux, só Windows) saem só da ordem em que as máquinas são instaladas ([ADR 0020](../decisoes/0020-linux-e-windows-em-qualquer-papel.md)); os critérios de aceite delas são o CA-GUILDA-10 e o CA-GUILDA-11 da [Guilda](../guilda/guilda.md).
- A lógica toda fica no subcomando `mestre install` ([componentes](../arquitetura/componentes.md#binários)); o pacote e o instalador são finos. Opções: `--join <endereço>` (máquina fora da rede local; no Windows, pela propriedade `JOIN` do MSI, porque sem ela o MSI não acha a Guilda e criaria uma nova por engano), `--new-guild` (cria outra Guilda mesmo que uma responda), `--restore <pasta do backup>`, `--instance <nome>` e, no Windows, `--operator-user <usuário>`.
- **Instâncias:** `--instance <nome>` (padrão `principal`) instala outra instância na mesma máquina, com pasta de dados, portas, serviços, conta de agente e canal local próprios, que entra na Guilda como uma máquina separada. Serve aos testes e ao teste manual do Dono; as regras estão em [instâncias](../arquitetura/componentes.md#instâncias). Os caminhos e nomes deste documento são os da instância padrão.
- **Sem assinatura de código Authenticode:** o MSI e os `.exe` são publicados sem ela. O SHA-256 de cada artefato é publicado na Release, e o guia de [instalação](../operacao/instalar.md) explica o aviso do SmartScreen. As versões seguintes chegam pelo manifesto assinado da [atualização automática](autoatualizacao.md), fora do `dpkg` e do instalador.

### Pastas de versões

| | Linux | Windows |
|---|---|---|
| Motor | `/opt/mestre/versoes/<versão>/mestre` | `%ProgramFiles%\Mestre\versoes\<versão>\mestre.exe` |
| Guardião | `/opt/mestre-guardiao/versoes/<versão>/mestre-guardiao` | `%ProgramFiles%\Mestre Guardiao\versoes\<versão>\mestre-guardiao.exe` |
| `mestre-pty` | `/opt/mestre/pty/<versão>/mestre-pty` (cada versão fica enquanto houver terminal vivo nela) | `%ProgramFiles%\Mestre\pty\<versão>\mestre-pty.exe` |
| Versão em uso | link `atual` → `versoes/<versão>` | junção de diretório `atual` → `versoes\<versão>` |
| Versão anterior | link `anterior` → `versoes/<versão anterior>` | junção `anterior` |
| No PATH | `/usr/local/bin/mestre` → `/opt/mestre/atual/mestre` | `%ProgramFiles%\Mestre\atual` |

- Os serviços apontam sempre para `atual`. Trocar de versão é trocar `atual` e `anterior` e reiniciar o serviço; o binário em execução nunca é sobrescrito.
- Ficam no disco só a versão atual e a anterior; a mais velha é apagada depois que a nova é confirmada.
- Dados, configuração e logs ficam nas pastas de [persistência](../arquitetura/persistencia.md#arquivos), fora das pastas de versões.

### `mestre install` (Linux como root; Windows elevado)

1. Registra a versão do sistema (o [checklist](checklist.md) acompanha).
2. **Contas:** no Linux, `mestre` (o motor e o `mestre-launcher`; só o launcher recebe `CAP_SETUID` e `CAP_SETGID`, para iniciar os terminais) e `mestre-agente` (os agentes), contas de sistema com grupo próprio, sem pasta pessoal, shell `nologin` ([ambiente](../desenvolvimento/ambiente.md#conta-mestre-agente-nesta-máquina-linux)); no Windows, a conta virtual `NT SERVICE\Mestre` (com a lista de privilégios exigidos declarada, [Windows](../arquitetura/windows.md#serviços-no-windows)) e a conta local `mestre-agente` ([Windows](../arquitetura/windows.md#conta-dos-agentes-mestre-agente)): fora de Administradores, senha aleatória de 32 caracteres guardada com DPAPI de máquina, "Negar logon local", "Negar logon pelos Serviços de Área de Trabalho Remota", logon em lote e oculta da tela de login. Cria as pastas de dados com dono, permissão e ACL ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes), [persistência](../arquitetura/persistencia.md#arquivos)).
3. **Serviços** ([componentes](../arquitetura/componentes.md#serviços-e-contas-padrão)), com as opções de reinício e recuperação de [camadas](../autocura/camadas.md#valores-padrão): o motor `mestre`, o `mestre-launcher` ([iniciador](../arquitetura/componentes.md#iniciador-mestre-launcher); no Linux, com cgroup delegado, onde cada terminal ganha o cgroup `term-<id>`; no Windows, LocalSystem), o `mestre-guardiao` (no Windows, LocalSystem) e o ajudante de troca `mestre-update` ([atualização automática](autoatualizacao.md)); no Linux, também os arquivos de serviço do Avahi ([descoberta](../conectividade/descoberta.md#mdns)). `mestre`, `mestre-launcher` e `mestre-guardiao` **sobem sozinhos com o sistema** (systemd habilitado no `multi-user.target`; no Windows, início automático); o `mestre-update` só roda quando o motor o inicia, a cada troca de versão, e o motor retoma o estado depois de um reinício: personagens, mesas, esperas e fila de saída ([retomada](../orquestracao/personagens.md#retomada-após-reinício)).
4. **Firewall:** libera o bloco de portas da instância ([portas](../arquitetura/componentes.md#portas)) só para a rede local (no Linux, se o `ufw` estiver ativo; no Windows, só no perfil Privado), em toda máquina, porque toda máquina responde no endereço do painel e qualquer uma pode virar Cidadela.
5. **Guilda:** procura uma Guilda e entra ou cria, conforme [formação](../guilda/guilda.md#formação):
   - **Entra:** mostra o código de entrada e espera a aceitação do Dono ([entrada na Guilda](../guilda/guilda.md#entrada-na-guilda)); aceita, a máquina conecta como Torre e recebe a configuração e a cópia de tudo: banco, arquivos e cofre cifrado ([replicação](../guilda/replicacao.md)).
   - **Cria:** gera o cofre ([chave do cofre](cofre.md#chave-do-cofre); no Windows, DPAPI de máquina com ACL só da conta virtual), o banco, a CA da Guilda e os certificados; mostra o endereço do painel, a impressão da CA (para conferir no navegador) e o **código de configuração** do assistente do Dono ([login](../painel/login.md#assistente-do-dono-primeira-vez)). Mostra **uma única vez** a chave de recuperação do cofre e só termina depois de o usuário redigitar dois grupos dela; diz que sem ela o backup do cofre é inútil ([backup e chave de recuperação](cofre.md#backup-e-chave-de-recuperação)). Com `--restore`, em vez de criar cofre e CA novos, restaura o banco e o cofre do backup pedindo essa chave.
6. **Operador do PC (Windows, opcional; padrão: não):** pergunta se habilita o [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) e para qual usuário. Se sim, cria a tarefa `\Mestre\Operador` ([Windows](../arquitetura/windows.md#operador-do-pc)) e grava o usuário na configuração da máquina; não dá à conta dele nenhuma entrada em `%ProgramData%\Mestre\`. O checklist confere o UAC (WIN-21) e a tarefa (WIN-22). Desabilitar é rodar a instalação de novo sem a opção: a tarefa é apagada e a ficha da máquina deixa de anunciar o Operador.
7. Inicia os serviços e roda o [checklist](checklist.md); o Whisper é preparado (abaixo). O [teste de conformidade](../provedores/conformidade.md) de cada provedor roda depois, ao configurar cada perfil de conta.

A instalação no Windows não pede senha nenhuma: nenhum serviço usa a conta de quem instala. Os Apps fixos "Grão-Mestre" e "Mestre" nascem no primeiro acesso, pelo link de criação ([criação de Apps](../github/criacao-de-apps.md)). Nas atualizações, a conta `mestre-agente` (com a senha guardada e o perfil dela) e as regras de firewall são preservadas.

### CLIs dos provedores

- `claude`, `codex` e `agy` são instalados pelo instalador oficial de cada provedor, rodando como `mestre-agente` com o `HOME` do perfil de conta (`/var/lib/mestre/<inst>/contas/<perfil>/` ou `%ProgramData%\Mestre\<inst>\contas\<perfil>\`; [perfis de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta)), na configuração inicial do perfil. Ficam onde esse instalador grava (ex.: o do Claude Code, em `$HOME/.local/share/claude`); o motor resolve o executável pelo perfil.
- **Os CLIs se atualizam sozinhos:** o Mestre não desliga a autoatualização deles. Ele detecta a versão nova e roda a conformidade depois ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)).
- **Cuidado:** como a pasta do CLI é gravável por `mestre-agente`, um agente poderia alterar o binário. As regras fixas negam às ferramentas dos agentes as pastas de dados do Mestre, inclusive os perfis de conta ([regras fixas](../classes/permissoes.md#regras-fixas-de-segurança)), mas só pela linha de comando: o código que o hook não lê as alcança ([risco aceito](../arquitetura/risco-do-codigo-nao-analisado.md)).
- **Codex:** precisa do pacote completo da versão (`bin/`, `codex-path/`, `codex-resources/`, `codex-package.json`); o binário solto não funciona ([Codex](../provedores/codex.md#contas-múltiplas)).

### Whisper

- **Qual máquina transcreve:** a Cidadela, por padrão. Toda máquina não excluída pelo Dono é preparada ao criar a Guilda ou ao entrar nela, porque qualquer uma pode virar Cidadela e precisa transcrever logo depois de uma promoção; a que não passa no benchmark não anuncia a transcrição. Uma máquina excluída só transcreve por escolha no painel ("Guilda e saúde das máquinas" → "Ativar transcrição", Dono ou Administrador; [administração](../painel/administracao.md)), gravada no banco da Cidadela; desativar apaga os arquivos dela e a ficha deixa de anunciar a transcrição. Como o trabalho é distribuído: [anexos e transcrição](../telegram/anexos-e-transcricao.md).
- **Arquivos:** a Cidadela baixa o `whisper-server` (Linux ou Windows) e os modelos `large-v3-turbo` q5_0 e q8_0 ([ADR 0016](../decisoes/0016-whisper-cpp-local.md)) e os entrega às Torres pela conexão WSS (transferência em partes, [protocolo](../conectividade/protocolo.md)); as Torres não acessam a internet para isso. O `whisper-server` é compilado pelo próprio Mestre (whisper.cpp numa versão fixada) e publicado nas Releases do Mestre. Endereços e SHA-256 de cada arquivo vêm fixos na versão do Mestre; arquivo com hash diferente é apagado e baixado de novo (3 tentativas).
- **Benchmark:** transcreve um áudio de teste embutido (30 s, em português) com cada quantização, usando tantas threads quanto os núcleos físicos. Fica com **q8_0 se levar até 0,3 do tempo do áudio** (padrão); senão, q5_0. O outro modelo é apagado e a escolha fica na configuração.
- Arquivos na pasta `whisper` de [persistência](../arquitetura/persistencia.md#arquivos). Benchmark feito → a ficha da máquina anuncia a transcrição. Falhou → não anuncia e o checklist acusa.

### Versão de protocolo

Cada binário declara a faixa de versões de protocolo que entende (`mestre version`). A Cidadela aceita a atual e a anterior, para Torres ainda não atualizadas conviverem ([protocolo](../conectividade/protocolo.md)). Uma Torre fora da faixa recebe `4003` e precisa da instalação manual da versão nova.

### Volta de versão

- **Só binário (padrão):** troca `atual` com `anterior` e reinicia. Funciona porque as migrações do banco são só aditivas dentro de uma versão maior ([persistência](../arquitetura/persistencia.md)). É a ação `rollback` do [Guardião](../autocura/guardiao.md#lista-fechada-de-ações).
- **Com restauração do banco:** antes de migrar, o motor grava uma cópia do banco ([persistência](../arquitetura/persistencia.md#banco-configuração)). Restaurar essa cópia é raro e nunca automático: exige Dono ou Administrador com PIN e perde tudo o que aconteceu entre a atualização e a volta ([Guardião](../autocura/guardiao.md#rollback-com-restauração-do-banco)).
- Depois de uma volta, `anterior` passa a apontar para a versão que falhou, marcada como "não usar"; ela só volta a ser aplicada por uma atualização aprovada de novo ([atualização automática](autoatualizacao.md)).

### Remoção (`mestre uninstall`)

Sem opções, preserva os dados do que sobra; com `--purge`, apaga também as pastas de dados da instância ([componentes](../arquitetura/componentes.md#binários)). Numa Torre, pede antes a saída da Guilda e **apaga a réplica** (banco, arquivos e espelhos), **a cópia do cofre**, a chave privada do motor, o que o `maquina.db` guardava da Guilda e o que guardou do tempo de Cidadela (bancos descartados, cópias completas e transferências; [lista](../guilda/replicacao.md#cofre-em-toda-máquina)), mesmo sem pedir para apagar tudo; a Cidadela com Torres na Guilda recusa e manda passar a Cidadela a outra máquina ([saída e remoção](../guilda/guilda.md#saída-e-remoção)). Antes de parar o `mestre`, encerra os terminais da máquina (o `mestre-pty` de cada um e a árvore dele), porque parar o serviço não os encerra ([componentes](../arquitetura/componentes.md#serviços-e-contas-padrão)). Para e remove serviços, unidades, regras de firewall criadas pela instalação e as pastas de versões; no Windows, também apaga a conta `mestre-agente`, o perfil dela, o arquivo da senha e a tarefa `\Mestre\Operador`. Os logins dos perfis de conta feitos por essa conta podem depender do DPAPI dela, então uma reinstalação no Windows refaz o login de cada perfil. Na Cidadela sozinha, dados, banco, backups e chave do cofre ficam, a não ser que o usuário peça para apagar tudo.

## Regras e erros

- `mestre install` recusa rodar sem privilégio de administrador e com `MESTRE_AGENT_ID` no ambiente ([componentes](../arquitetura/componentes.md#binários)).
- Rodar de novo numa máquina já instalada não duplica nada: confere e completa o que falta (contas, direitos, privilégios, pastas, serviços, regras) e, numa máquina que já está numa Guilda, não procura outra; numa máquina fora de qualquer Guilda (removida, ou que a perdeu), procura de novo.
- Falha no meio da instalação → mostra o passo que falhou e como repetir; nada do que já foi criado é apagado sozinho.
- O Guardião nunca é trocado por ação de cura ([Guardião](../autocura/guardiao.md#proteções)).

## Critérios de aceite

- **CA-INST-02** · *rápida* — **Dado** as versões 1.2.0 (atual) e 1.1.0 (anterior) num sistema de arquivos falso, **quando** roda a volta só de binário, **então** `atual` aponta para 1.1.0 e nenhum arquivo do banco muda.
- **CA-INST-03** · *rápida* — **Dado** um modelo baixado com SHA-256 diferente, **quando** a instalação confere, **então** apaga e baixa de novo, até 3 vezes, e depois a ficha da máquina não anuncia a transcrição.
- **CA-INST-04** · *rápida* — **Dado** o benchmark com q8_0 a 0,25 do tempo do áudio, **quando** termina, **então** fica q8_0 e o modelo q5_0 é apagado.
- **CA-INST-05** · *pesada* — **Dado** uma máquina de cada sistema já instalada, **quando** `mestre install` roda de novo, **então** nenhum serviço, tarefa ou regra fica duplicado e nenhuma Guilda nova é criada (MVP-13).
- **CA-INST-08** · *pesada* — **Dado** a instalação concluída no Ubuntu e no Windows, como Cidadela e como Torre, **quando** listo os serviços, **então** em toda máquina existem só `mestre`, `mestre-launcher`, `mestre-guardiao` e `mestre-update`, os três primeiros com início automático e o `mestre-update` sob demanda (oneshot no Linux, início sob demanda no Windows); o `mestre` roda como `mestre` no Linux e como `NT SERVICE\Mestre` no Windows, e os terminais como `mestre-agente` (MVP-13).
- **CA-INST-09** · *rápida* — **Dado** uma máquina excluída sem transcrição e dublês de rede e relógio, **quando** um Administrador ativa a transcrição dela no painel, **então** a Cidadela envia os arquivos por transferência, a Torre roda o benchmark e a ficha dela passa a anunciar a transcrição; com um arquivo de hash errado, nada é anunciado.
- **CA-INST-10** · *pesada* — **Dado** a instalação concluída no Windows (Cidadela e Torre), **quando** listo a ACL de `%ProgramData%\Mestre\` e de cada subpasta, **então** a herança está desligada, não há entrada para `Users`, `Authenticated Users` nem `Everyone`, e `mestre-agente` só tem as entradas de [persistência](../arquitetura/persistencia.md#arquivos); uma conta comum de teste da máquina não consegue listar a pasta (MVP-13).
- **CA-INST-11** · *pesada* — **Dado** a instalação concluída no Windows (Cidadela e Torre), o usuário logado e personagens de teste rodando (com MCP de navegador), **quando** listo os processos do Mestre e seus donos, **então** nenhum roda com a conta do usuário: `mestre` como `NT SERVICE\Mestre`, o Guardião como SYSTEM, e os terminais, seus filhos e o agente de diagnóstico como `mestre-agente`; a única exceção é o [Operador do PC](../decisoes/0021-operador-do-pc.md): com ele habilitado, só `mestre operator-session` roda com a conta do usuário e, durante uma ativação aprovada, também o terminal do Operador e seus filhos (MVP-13, MVP-14).
- **CA-INST-12** · *pesada* — **Dado** a instalação concluída no Windows e um arquivo em `C:\Users\<usuário>\Documents`, **quando** um processo como `mestre-agente` (o agente falso, por comando direto, sem passar pelo despachante) tenta listar `C:\Users\<usuário>` e ler o arquivo, **então** o Windows nega os dois (MVP-13).
- **CA-INST-13** · *pesada* — **Dado** a instalação concluída no Windows e a senha guardada do `mestre-agente`, **quando** se tenta entrar com ela na tela de login, por `runas /user:mestre-agente` e por Área de Trabalho Remota, **então** o Windows recusa os três, a conta não aparece na tela de login nem no grupo Administradores, e o `mestre` continua iniciando personagens com ela (MVP-13).
- **CA-INST-14** · *pesada* — **Dado** uma máquina Windows instalada com o Operador habilitado para o usuário de teste, **quando** listo a tarefa `\Mestre\Operador` e a ACL de `%ProgramData%\Mestre\`, **então** a tarefa tem o gatilho de logon desse usuário, privilégios normais, sem limite de tempo e reinício em falha, e a ACL não tem entrada para a conta dele; reinstalado sem a opção, a tarefa não existe mais (MVP-14).
- **CA-INST-15** · *rápida* — **Dado** uma máquina nova que entra na Guilda e dublês de rede, **quando** a entrada termina, **então** ela recebe os arquivos do Whisper e roda o benchmark; **quando** o Dono a exclui, **então** os arquivos são apagados, a não ser que a transcrição dela esteja ativada no painel.
- **CA-INST-16** · *completa* — **Dado** duas instâncias na mesma máquina, com personagens do agente falso, uma mesa aberta, uma espera e um item na fila de saída, **quando** o processo do motor de cada uma é encerrado à força e iniciado de novo, **então** personagens, mesa, espera e fila voltam ao estado anterior.
- **CA-INST-17** · *pesada* — **Dado** a instalação no Linux e no Windows, **quando** reinicio cada máquina, **então** os serviços sobem sozinhos com o sistema e o estado volta como no CA-INST-16 (MVP-12, MVP-13).

## Depende de

- [Componentes](../arquitetura/componentes.md)
- [Persistência](../arquitetura/persistencia.md)
- [Segurança](../arquitetura/seguranca.md)
- [Windows](../arquitetura/windows.md)
- [Cofre](cofre.md)
- [Guilda](../guilda/guilda.md)
- [Descoberta](../conectividade/descoberta.md)
- [Protocolo](../conectividade/protocolo.md)

## Fontes

- whisper.cpp: https://github.com/ggml-org/whisper.cpp
- Serviços em Go (systemd e Windows): https://pkg.go.dev/github.com/kardianos/service
