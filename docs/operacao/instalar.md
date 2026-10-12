# Instalar

> **Assunto:** passo a passo para instalar o Mestre em cada um dos quatro usos: Core no Ubuntu ou no Windows, nós no Ubuntu ou no Windows · **Componente:** Todos · **Atualizado:** 2026-10-12

Guia para quem usa. O que cada passo cria por dentro (pastas, contas, serviços, CA, Whisper) está em [instalação e versões](../plataforma/instalacao-e-atualizacao.md); aqui fica só o que você faz e o que vai ver.

## Escolha o uso

Todo uso começa por **uma** máquina instalada como **Core** (ela já roda agentes). Os nós são opcionais e podem ser de qualquer sistema.

| Uso | Faça | Limite do uso |
|---|---|---|
| Só Linux | [Core no Ubuntu](#core-no-ubuntu) | máquina única: se ela cair inteira, ninguém de fora percebe ([camadas](../autocura/camadas.md#modo-máquina-única)) |
| Linux + Windows | [Core no Ubuntu](#core-no-ubuntu) → [nó no Windows](#nó-no-windows) | — |
| Windows + Linux | [Core no Windows](#core-no-windows) → [nó no Ubuntu](#nó-no-ubuntu) | — |
| Só Windows | [Core no Windows](#core-no-windows) | máquina única, como no "só Linux" |

**Ordem completa:** instalar o Core → [primeiro acesso](primeiro-acesso.md) (CA, Dono, Apps do GitHub e perfis de conta) → instalar cada nó e [pareá-lo](parear-no.md) → [parear o Telegram](parear-telegram.md).

## Antes de começar

- **Sistemas:** Ubuntu na versão LTS atual, com `sudo`; Windows 11 ou superior, com uma conta de administrador. Todas as máquinas na mesma rede local e com internet (para baixar o Mestre e o modelo de transcrição).
- **Contas no Windows:** o instalador não pede senha. Ele cria a conta local **`mestre-agente`**, com que os agentes trabalham: não é administradora, não entra na tela de login e não enxerga a sua pasta pessoal (`C:\Users\<você>`) ([Windows](../arquitetura/windows.md#conta-dos-agentes-mestre-agente)). Pastas fora da sua pasta pessoal que todo usuário da máquina pode ler continuam legíveis para ela; o Mestre não mexe nas permissões delas.
- **Rede do Windows como "Privada":** Configurações → Rede e Internet → sua conexão → Tipo de perfil de rede → **Privada**.
- **Conta pessoal do GitHub** e **Telegram** no celular, para os passos seguintes.
- **Provedores:** os CLIs que você vai usar (`claude`, `codex`, `agy`) instalados nas máquinas onde os agentes vão rodar, inclusive no Core. No Windows, a conta `mestre-agente` não enxerga um CLI instalado só na sua pasta pessoal: instale-o para todos os usuários ou pelo terminal de configuração do perfil, no painel; o [checklist](../plataforma/checklist.md) avisa. O login de cada conta (e, no Codex, a aprovação dos hooks do Mestre) é feito depois da instalação, na [configuração inicial de cada perfil de conta](primeiro-acesso.md#4-configurar-os-perfis-de-conta), com a conta dos agentes: os seus logins pessoais nos CLIs não são usados. Sem ela o perfil não recebe agentes, e o checklist mostra o que falta.

## Core no Ubuntu

1. Rode o instalador e responda **Core** quando ele perguntar o papel da máquina:

   ```bash
   curl -fsSLO https://github.com/lucasndas/mestre/releases/latest/download/install.sh
   sudo bash install.sh
   ```

2. Espere terminar. O passo mais demorado é baixar e testar o modelo de transcrição (centenas de MB).
3. No fim, o terminal mostra três coisas. **Anote as três:** o **endereço do painel** (ex.: `https://servidor.local:7443`), a **impressão digital SHA-256 da CA** e o **código de configuração** (usado uma vez para criar o Dono).
4. O instalador mostra **uma única vez** a **chave de recuperação** do cofre (52 caracteres em grupos de 4) e pede que você redigite dois grupos. Guarde-a fora do Mestre (papel, gerenciador de senhas): sem ela, o backup do cofre é inútil e um disco perdido leva junto todos os segredos ([cofre](../plataforma/cofre.md#backup-e-chave-de-recuperação)).
5. Confira: `systemctl status mestre-core mestre-node mestre-guardiao` mostra os três `active (running)`. O `mestre-core` guarda o cofre e não roda agentes; o `mestre-node` é o nó local, que roda os agentes desta máquina.
6. Siga para o [primeiro acesso](primeiro-acesso.md).

## Core no Windows

1. Baixe `mestre-<versão>-x64.msi` da página de Releases, confira o SHA-256 publicado (`Get-FileHash .\mestre-<versão>-x64.msi`) e abra. O instalador não tem assinatura de código: se o Windows mostrar "O Windows protegeu o computador", clique em **Mais informações** → **Executar assim mesmo**, e aceite o pedido de administrador.
2. Escolha **Core** como papel da máquina.
3. No fim, ele mostra o endereço do painel, a impressão digital da CA e o código de configuração. **Anote os três.**
4. Guarde a **chave de recuperação** do cofre que o instalador mostra uma única vez, como no Ubuntu (passo 4).
5. Confira: `Get-Service mestre-core, mestre-node, mestre-guardiao` mostra os três `Running`. O `mestre-core` guarda o cofre numa conta própria do sistema; o `mestre-node` roda em outra e inicia os agentes com a conta `mestre-agente` ([Windows](../arquitetura/windows.md#windows-como-core)). `Get-LocalUser mestre-agente` mostra a conta ativa.
6. Siga para o [primeiro acesso](primeiro-acesso.md).

## Nó no Windows

1. No painel, gere um **convite de nó** ([parear nó](parear-no.md)).
2. Baixe e abra o `.msi` como no Core no Windows (passo 1), e escolha **nó** como papel.
3. O instalador procura o Core na rede local; confira o que ele achou e cole o **código de convite**.
4. O instalador cria a conta `mestre-agente` e os serviços; os agentes trabalham mesmo com você deslogado.
5. Confira: `Get-Service mestre-node, mestre-guardiao` mostra os dois `Running`, e `Get-LocalUser mestre-agente` mostra a conta ativa.
6. **Operador do PC (opcional):** o instalador pergunta se quer habilitar o [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc), um agente que usa este PC com a sua conta, só quando o Dono aprova, por pouco tempo e com aviso na tela. Se quiser, responda **sim** e escolha o seu usuário do Windows. Antes, deixe o Controle de Conta de Usuário (UAC) ligado, com "Sempre notificar" ou o nível padrão, e o Chrome com a extensão Claude in Chrome instalada. Depois, faça a configuração inicial em **Administração → Operador do PC**. Também vale para o nó local de um Core no Windows. Para desligar, rode o instalador de novo e responda **não**.

## Nó no Ubuntu

1. No painel, gere um **convite de nó** ([parear nó](parear-no.md)).
2. Rode o mesmo `install.sh` do Core no Ubuntu e escolha **nó**; cole o código de convite quando ele pedir.
3. Confira: `systemctl status mestre-node mestre-guardiao` mostra os dois `active (running)`.

## Conferir tudo

1. No painel, abra **Saúde dos nós**. O Core e cada nó aparecem **conectados**, com motor e Guardião em dia. Numa máquina única, uma faixa lembra que ninguém de fora percebe se ela cair inteira.
2. Veja a coluna **Checklist** de cada máquina e use a correção oferecida; correções de rede, firewall ou serviços pedem a sua aprovação ([checklist](../plataforma/checklist.md)).
3. Portas, se precisar conferir um firewall ([componentes](../arquitetura/componentes.md#portas)): `7443/tcp` (painel e conexões do Core), `7444/tcp` (Guardião do Core), `7445/udp` (descoberta) e `7446/tcp` (página reserva).

Em seguida: [primeiro acesso](primeiro-acesso.md), [parear o Telegram](parear-telegram.md) e criar o primeiro workspace (**Workspaces → Criar**, [sessão zero do workspace](../sessao-zero/workspace.md)). Nó em outra rede: informe o endereço do Core no instalador ([descoberta](../conectividade/descoberta.md#nó-fora-da-rede-local)).
