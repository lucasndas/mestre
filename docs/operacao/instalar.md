# Instalar

> **Assunto:** passo a passo para instalar o Mestre no Ubuntu ou no Windows com o instalador único: a primeira máquina cria a Guilda e as outras entram nela · **Componente:** Todos · **Atualizado:** 2026-10-19

Guia para quem usa. O que cada passo cria por dentro (pastas, contas, serviços, CA, Whisper) está em [instalação e versões](../plataforma/instalacao-e-atualizacao.md); aqui fica só o que você faz e o que vai ver.

## Como funciona

O instalador é o mesmo em toda máquina e não pergunta papel. Ele procura uma **Guilda** na rede local: a **primeira** máquina não acha nenhuma, **cria a Guilda** e vira a **Cidadela** (a máquina no comando, que também roda agentes); cada máquina instalada **depois** acha a Guilda e pede para **entrar**, como **Torre** ([Guilda](../guilda/guilda.md)).

| Composição | Faça | Limite |
|---|---|---|
| Só Linux | instale no Ubuntu | máquina única: se ela cair inteira, ninguém de fora percebe ([camadas](../autocura/camadas.md#modo-máquina-única)) |
| Cidadela Linux + Torre Windows | instale no Ubuntu → [primeiro acesso](primeiro-acesso.md) → instale no Windows e [aceite a entrada](entrar-na-guilda.md) | — |
| Cidadela Windows + Torre Linux | instale no Windows → [primeiro acesso](primeiro-acesso.md) → instale no Ubuntu e [aceite a entrada](entrar-na-guilda.md) | — |
| Só Windows | instale no Windows | máquina única, como no "só Linux" |

**Ordem completa:** instalar a primeira máquina → [primeiro acesso](primeiro-acesso.md) (CA, Dono, Apps do GitHub e perfis de conta) → instalar cada outra máquina e [aceitá-la na Guilda](entrar-na-guilda.md) → [parear o Telegram](parear-telegram.md).

## Antes de começar

- **Sistemas:** Ubuntu na versão LTS atual, com `sudo`; Windows 11 ou superior, com uma conta de administrador. Todas as máquinas na mesma rede local e com internet (para baixar o Mestre e o modelo de transcrição).
- **Contas no Windows:** o instalador não pede senha. Ele cria a conta local **`mestre-agente`**, com que os agentes trabalham: não é administradora, não entra na tela de login e não enxerga a sua pasta pessoal (`C:\Users\<você>`) ([Windows](../arquitetura/windows.md#conta-dos-agentes-mestre-agente)). Pastas fora da sua pasta pessoal que todo usuário da máquina pode ler continuam legíveis para ela; o Mestre não mexe nas permissões delas.
- **Rede do Windows como "Privada":** Configurações → Rede e Internet → sua conexão → Tipo de perfil de rede → **Privada**.
- **Conta pessoal do GitHub** e **Telegram** no celular, para os passos seguintes.
- **Provedores:** os CLIs (`claude`, `codex`, `agy`) são instalados para a conta dos agentes, em cada perfil de conta, na [configuração inicial de cada perfil](primeiro-acesso.md#4-configurar-os-perfis-de-conta), depois da instalação; lá também é feito o login de cada conta (e, no Codex, a aprovação dos hooks do Mestre). Os seus CLIs e logins pessoais não são usados. Os CLIs se atualizam sozinhos ([atualização dos CLIs](../provedores/atualizacao-dos-clis.md)).

## No Ubuntu

1. Rode o instalador:

   ```bash
   curl -fsSLO https://github.com/mestre-ia/mestre/releases/latest/download/install.sh
   sudo bash install.sh
   ```

2. Espere terminar. O passo mais demorado é baixar e testar o modelo de transcrição (centenas de MB).
3. Siga [O que aparece no fim](#o-que-aparece-no-fim).
4. Confira: `systemctl status mestre mestre-guardiao` mostra os dois `active (running)`. Os dois sobem sozinhos com o sistema e, depois de um reinício, o Mestre retoma o que estava fazendo. O `mestre` guarda o cofre cifrado (só a Cidadela o abre) e inicia os agentes com a conta `mestre-agente`.

## No Windows

1. Baixe `mestre-<versão>-x64.msi` da página de Releases, confira o SHA-256 publicado (`Get-FileHash .\mestre-<versão>-x64.msi`) e abra. O instalador não tem assinatura de código: se o Windows mostrar "O Windows protegeu o computador", clique em **Mais informações** → **Executar assim mesmo**, e aceite o pedido de administrador.
2. Siga [O que aparece no fim](#o-que-aparece-no-fim).
3. Confira: `Get-Service mestre, mestre-guardiao` mostra os dois `Running`, e `Get-LocalUser mestre-agente` mostra a conta ativa. O `mestre` roda numa conta própria do sistema e inicia os agentes com a conta `mestre-agente` ([Windows](../arquitetura/windows.md)); eles trabalham mesmo com você deslogado. Os serviços sobem sozinhos com o Windows e, depois de um reinício, o Mestre retoma o que estava fazendo.
4. **Operador do PC (opcional):** o instalador pergunta se quer habilitar o [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc), um agente que usa este PC com a sua conta, só quando o Dono aprova, por pouco tempo e com aviso na tela. Se quiser, responda **sim** e escolha o seu usuário do Windows. Antes, deixe o Controle de Conta de Usuário (UAC) ligado, com "Sempre notificar" ou o nível padrão, e o Chrome com a extensão do Playwright MCP instalada. Depois, faça a configuração inicial em **Administração → Operador do PC**. Para desligar, rode o instalador de novo e responda **não**.

## O que aparece no fim

- **Primeira máquina (cria a Guilda):** o terminal mostra o **endereço do painel** (ex.: `https://mestre.local:7443`), a **impressão digital SHA-256 da CA** e o **código de configuração** (usado uma vez para criar o Dono). **Anote os três.** Depois mostra **uma única vez** a **chave de recuperação** do cofre (52 caracteres em grupos de 4) e pede que você redigite dois grupos. Guarde-a fora do Mestre (papel, gerenciador de senhas): sem ela, o backup do cofre é inútil e uma Cidadela perdida sem nenhuma outra máquina não excluída leva junto todos os segredos ([cofre](../plataforma/cofre.md#backup-e-chave-de-recuperação)). Siga para o [primeiro acesso](primeiro-acesso.md).
- **Outras máquinas (entram na Guilda):** o instalador mostra a Guilda que achou e um **código de entrada** de 6 dígitos, e espera o Dono aceitar: siga [entrar na Guilda](entrar-na-guilda.md).

## Conferir tudo

1. No painel, abra **Guilda e saúde das máquinas**. A Cidadela e cada Torre aparecem **conectadas**, com motor e Guardião em dia. Numa máquina única, uma faixa lembra que ninguém de fora percebe se ela cair inteira.
2. Veja a coluna **Checklist** de cada máquina e use a correção oferecida; correções de rede, firewall ou serviços pedem a sua aprovação ([checklist](../plataforma/checklist.md)).
3. As portas que o firewall precisa liberar, se for conferir um, estão na tabela de [componentes](../arquitetura/componentes.md#portas).

Em seguida: [primeiro acesso](primeiro-acesso.md), [parear o Telegram](parear-telegram.md) e criar o primeiro workspace (**Workspaces → Criar**, [sessão zero do workspace](../sessao-zero/workspace.md)). Máquina em outra rede: `--join <endereço da Cidadela>` no instalador, ou `JOIN=<endereço>` no MSI ([entrar na Guilda](entrar-na-guilda.md#máquina-fora-da-rede-local)).

**Testar a Guilda numa máquina só:** instale uma segunda instância com `sudo mestre install --instance teste` (no Windows, num terminal de administrador). Ela tem pasta, portas e serviços próprios e entra na Guilda como outra máquina, o que basta para testar a entrada, a replicação e a troca de Cidadela ([instâncias](../arquitetura/componentes.md#instâncias)).
