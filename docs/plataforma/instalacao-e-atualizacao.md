# Instalação e versões

> **Assunto:** visão técnica da instalação no Ubuntu e no Windows, pastas de versões, versão anterior guardada, volta de versão, modelo Whisper e remoção · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Definir o que a instalação cria em cada máquina e como as versões ficam no disco, para que atualizar e voltar de versão seja só trocar um apontamento. O passo a passo para quem usa está em [instalar](../operacao/instalar.md), [primeiro acesso](../operacao/primeiro-acesso.md), [parear nó](../operacao/parear-no.md), [atualizar](../operacao/atualizar.md) e [desinstalar](../operacao/desinstalar.md). O fluxo de atualização (descoberta, assinatura, aprovação, ordem) está em [atualização automática](autoatualizacao.md).

## Comportamento

### Pacotes

| Plataforma | Artefato | O que faz |
|---|---|---|
| Ubuntu (LTS atual) | `mestre_<versão>_amd64.deb`, mais um `install.sh` que baixa o `.deb` das [Releases](https://github.com/lucasndas/mestre/releases), confere o SHA-256 e roda `apt install` | coloca os binários em `/usr/lib/mestre/pacote/` e chama `mestre install core` |
| Windows 11 ou superior | instalador `mestre-setup-<versão>.exe`, elevado | copia os binários e chama `mestre install node` |

- A lógica toda fica nos subcomandos `mestre install core|node` ([componentes](../arquitetura/componentes.md#binários)); o pacote e o instalador são finos.
- Depois da instalação, as versões novas chegam pela [atualização automática](autoatualizacao.md), fora do `dpkg` e do instalador.

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

### Instalação do Core (`mestre install core`)

1. Registra a versão do Ubuntu (o [checklist](checklist.md) acompanha).
2. Cria as contas `mestre` e `mestre-agente` e as pastas de dados com dono e permissão ([segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)).
3. Gera a chave do [cofre](cofre.md#chave-do-cofre) e cria o banco ([persistência](../arquitetura/persistencia.md)).
4. Cria a CA interna e o certificado do servidor ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)); a chave da CA vai para o cofre.
5. Instala as unidades systemd `mestre-core`, `mestre-guardiao` e o ajudante de troca `mestre-update` ([atualização automática](autoatualizacao.md)), com as opções de reinício de [camadas](../autocura/camadas.md#valores-padrão).
6. Se o `ufw` estiver ativo, libera as portas do Core ([portas](../arquitetura/componentes.md#portas)).
7. Baixa e prepara o Whisper (abaixo).
8. Inicia os serviços e mostra no terminal o endereço do painel, a impressão da CA (para conferir no navegador) e o **código de configuração** do assistente do Dono ([login](../painel/login.md#assistente-do-dono-primeira-vez)).
9. Orienta guardar uma cópia da chave do cofre fora do servidor.

Os Apps fixos "Grão-Mestre" e "Mestre" são criados depois, no primeiro acesso pelo painel ([criação de Apps](../github/criacao-de-apps.md)).

### Windows

`mestre install node` (elevado):

1. Encontra o Core pela [descoberta](../conectividade/descoberta.md) ou pelo endereço informado, e faz o [pareamento](../conectividade/pareamento.md) com o código de convite.
2. Pede a senha da conta do usuário e configura o serviço `mestre-node` com "Fazer logon como" ([nó Windows](../arquitetura/no-windows.md)).
3. Registra os serviços `mestre-node` e `mestre-guardiao` (início automático, recuperação de [camadas](../autocura/camadas.md#valores-padrão)) e o ajudante `mestre-update` (sob demanda).
4. Cria a tarefa agendada **`\Mestre\Sessao`**: gatilho no logon do usuário dono do serviço, ação `mestre node session`, só com o usuário logado, privilégios normais, sem limite de tempo, reiniciar em falha ([nó Windows](../arquitetura/no-windows.md)).
5. Cria a tarefa agendada **`\Mestre\Diagnostico`**: roda como o usuário, conectado ou não, sem gatilho (disparada pelo Guardião) ([diagnóstico com IA](../autocura/diagnostico-com-ia.md)).
6. Cria as regras de firewall da descoberta e da [página reserva](../autocura/pagina-reserva.md), só no perfil Privado.
7. Cria as pastas de dados com ACL ([persistência](../arquitetura/persistencia.md#arquivos)), inicia os serviços, roda o [checklist](checklist.md) e o [teste de conformidade](../provedores/conformidade.md) de cada provedor encontrado.

Nas atualizações, as tarefas são preservadas e o processo da sessão é reiniciado depois do motor novo.

### Whisper

- O motor do nó de transcrição (no MVP, o Core) baixa o `whisper-server` (whisper.cpp) e os modelos `large-v3-turbo` q5_0 e q8_0 ([ADR 0016](../decisoes/0016-whisper-cpp-local.md)). Endereços e SHA-256 de cada arquivo vêm fixos na versão do Mestre; arquivo com hash diferente é apagado e baixado de novo (3 tentativas).
- **Benchmark:** transcreve um áudio de teste embutido (30 s, em português) com cada quantização, usando tantas threads quanto os núcleos físicos. Fica com **q8_0 se levar até 0,3 do tempo do áudio** (padrão); senão, q5_0. O outro modelo é apagado e a escolha fica na configuração.
- Arquivos em `/var/lib/mestre/whisper/`. Falhou → o nó anuncia `transcricao` falso e o checklist acusa ([anexos e transcrição](../telegram/anexos-e-transcricao.md)).

### Versão de protocolo

Cada binário declara a faixa de versões de protocolo que entende (`mestre version`). O Core aceita a atual e a anterior, para nós ainda não atualizados conviverem ([protocolo](../conectividade/protocolo.md#abertura-versão-e-capacidades)). Um nó fora da faixa recebe `4003` e precisa da instalação manual da versão nova.

### Volta de versão

- **Só binário (padrão):** troca `atual` com `anterior` e reinicia. Funciona porque as migrações do banco são só aditivas dentro de uma versão maior ([persistência](../arquitetura/persistencia.md)). É a ação `rollback` do [Guardião](../autocura/guardiao.md#lista-fechada-de-ações).
- **Com restauração do banco:** antes de migrar, o motor grava `pre-migracao-<versão>.db`. Restaurar essa cópia perde o que foi gravado depois; por isso só com aprovação de Dono ou Administrador.
- Depois de uma volta, `anterior` passa a apontar para a versão que falhou, marcada como "não usar"; ela só volta a ser aplicada por uma atualização aprovada de novo ([atualização automática](autoatualizacao.md)).

### Remoção (`mestre uninstall`)

Para e remove serviços, unidades, tarefas agendadas, regras de firewall criadas pela instalação e as pastas de versões. Dados, banco, backups e chave do cofre ficam, a não ser que o usuário peça para apagar tudo. A credencial do nó deve ser revogada no painel ([pareamento](../conectividade/pareamento.md)).

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
- **CA-INST-05** · *completa* — **Dado** uma máquina já instalada, **quando** `mestre install node` roda de novo, **então** nenhum serviço, tarefa ou regra fica duplicado.
- **CA-INST-06** · *completa* — **Dado** a instalação do nó concluída, **quando** listo as tarefas agendadas, **então** `\Mestre\Sessao` e `\Mestre\Diagnostico` existem com as opções acima; depois de `mestre uninstall`, não existem.

## Depende de

- [Componentes](../arquitetura/componentes.md)
- [Persistência](../arquitetura/persistencia.md)
- [Segurança](../arquitetura/seguranca.md)
- [Nó Windows](../arquitetura/no-windows.md)
- [Cofre](cofre.md)
- [Pareamento](../conectividade/pareamento.md)
- [Descoberta](../conectividade/descoberta.md)
- [Atualização automática](autoatualizacao.md)

## A confirmar

- **A confirmar:** ferramenta do instalador do Windows e assinatura de código do `.exe`.
- **A confirmar:** se o whisper.cpp publica `whisper-server` pronto para Linux; se não, o Mestre compila e publica o binário nas próprias Releases.
- **A confirmar:** a tarefa `\Mestre\Diagnostico` rodar como o usuário deslogado exige guardar a senha no Agendador de Tarefas; conferir o pedido de senha junto com o de "Fazer logon como".

## Fontes

- whisper.cpp: https://github.com/ggml-org/whisper.cpp
- Serviços em Go (systemd e Windows): https://pkg.go.dev/github.com/kardianos/service
