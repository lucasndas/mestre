# Instalar

> **Assunto:** passo a passo para instalar o Core no Ubuntu e o nó no Windows, na rede local · **Componente:** Todos · **Atualizado:** 2026-10-12

Guia para quem usa. O que cada passo cria por dentro (pastas, contas, serviços, CA, Whisper) está em [instalação e versões](../plataforma/instalacao-e-atualizacao.md); aqui fica só o que você faz e o que vai ver.

**Ordem completa:** instalar o Core → [primeiro acesso](primeiro-acesso.md) (CA e Dono) → instalar o nó e [pareá-lo](parear-no.md) → autorizar o GitHub ([primeiro acesso](primeiro-acesso.md#3-autorizar-o-grão-mestre-a-criar-github-apps)) → [parear o Telegram](parear-telegram.md).

## Antes de começar

- **Servidor do Core:** Ubuntu na versão LTS atual, ligado na rede local, com acesso de administrador (`sudo`) e internet (para baixar o Mestre e o modelo de transcrição).
- **PC do nó:** Windows 11 ou superior, na mesma rede local, com uma conta de administrador.
- **Senha da sua conta do Windows:** o serviço do nó roda com a sua conta e o Windows pede a senha dela. É a senha da conta (local ou da Microsoft), não o PIN do Windows Hello. Se você só entra por PIN, descubra ou redefina a senha antes.
- **Rede do Windows como "Privada":** Configurações → Rede e Internet → sua conexão → Tipo de perfil de rede → **Privada**. Sem isso o nó não acha o Core sozinho.
- **Conta pessoal do GitHub** (com 2FA) e **Telegram** no celular, para os passos seguintes.
- **Provedores:** os CLIs que você vai usar (`claude`, `codex`, `agy`) instalados nas máquinas onde os agentes vão rodar. O login de cada conta é feito depois; o [checklist](../plataforma/checklist.md) mostra o que falta.

## Parte 1: Core (Ubuntu)

1. Baixe o instalador da página de Releases do repositório do Mestre e rode:

   ```bash
   curl -fsSLO https://github.com/lucasndas/mestre/releases/latest/download/install.sh
   sudo bash install.sh
   ```

   Se preferir, baixe o `mestre_<versão>_amd64.deb` da mesma página e rode `sudo apt install ./mestre_<versão>_amd64.deb`.
2. Espere terminar. O passo mais demorado é baixar e testar o modelo de transcrição (centenas de MB). Se o `ufw` estiver ativo, as portas do Mestre são liberadas sozinhas.
3. No fim, o terminal mostra três coisas. **Anote as três:**
   - o **endereço do painel** (ex.: `https://servidor.local:7443`);
   - a **impressão digital SHA-256 da CA** (para conferir no navegador);
   - o **código de configuração** (usado uma vez para criar o Dono).
4. O instalador também orienta guardar uma **cópia da chave do cofre** fora do servidor. Faça isso: sem ela, um disco perdido leva junto todos os segredos ([cofre](../plataforma/cofre.md)).
5. Confira os serviços:

   ```bash
   systemctl status mestre-core mestre-guardiao
   ```

   Os dois devem aparecer como `active (running)`.
6. Siga para o [primeiro acesso](primeiro-acesso.md) antes de instalar o nó.

## Parte 2: nó (Windows)

1. No painel, gere um **convite de nó** ([parear nó](parear-no.md), passo 1). Deixe o código à mão.
2. Baixe `mestre-<versão>-x64.msi` da página de Releases. Confira o SHA-256 publicado na Release (`Get-FileHash .\mestre-<versão>-x64.msi`) e abra. O instalador não tem assinatura de código no MVP: se o Windows mostrar "O Windows protegeu o computador", clique em **Mais informações** → **Executar assim mesmo**. Aceite o pedido de permissão de administrador.
3. O instalador procura o Core na rede local e mostra o que encontrou. Confira e cole o **código de convite** quando ele pedir. Detalhes: [parear nó](parear-no.md).
4. O instalador pede a **senha da sua conta do Windows** (com conta Microsoft, a senha dela, não o PIN) e confere na hora. É só isso que você precisa fazer. Para que serve:
   - o serviço `mestre-node` roda com a sua conta ("Fazer logon como"), para os CLIs acharem o seu perfil e os seus logins;
   - assim, agentes **sem tela** continuam trabalhando mesmo com você deslogado;
   - quem guarda a senha é o próprio Windows; o Mestre não a grava ([nó Windows](../arquitetura/no-windows.md)).
5. O instalador cria, sozinho:
   - o serviço **`mestre-node`**, com a sua conta;
   - o serviço **`mestre-guardiao`**, com a conta do sistema (LocalSystem);
   - a tarefa agendada **`\Mestre\Sessao`**, que inicia o processo de sessão do Mestre sempre que você faz logon;
   - as regras de firewall da descoberta e da página reserva (só na rede Privada).
6. No fim, ele roda o [checklist](../plataforma/checklist.md) e o teste de cada provedor que encontrar.

### Conferir no Windows

- **Serviços:** abra o PowerShell e rode `Get-Service mestre-node, mestre-guardiao`. Os dois devem estar `Running`.
- **Tarefa de sessão:** abra o **Agendador de Tarefas** → Biblioteca do Agendador de Tarefas → pasta **Mestre**. A tarefa **Sessao** deve estar lá, com o gatilho "Ao fazer logon". Ao lado dela aparece também a tarefa **Diagnostico**, usada pelo Guardião ([diagnóstico com IA](../autocura/diagnostico-com-ia.md)).
- **CLI:** num PowerShell novo, `mestre version` responde com a versão.

### Agentes com tela

Agentes marcados com `gui: true` e o navegador dedicado do GitHub **só funcionam com você logado no Windows** (ou com login automático ligado). Deslogado, o nó continua online e roda os agentes sem tela; o painel mostra `gui` como falso até você entrar de novo.

**Trocou a senha do Windows?** O serviço `mestre-node` para de subir. O Guardião avisa e o [checklist](../plataforma/checklist.md) mostra como informar a senha nova (rodar o instalador de novo também resolve: ele confere e completa o que falta, sem duplicar).

## Parte 3: conferir tudo

1. No painel, abra **Saúde dos nós**. O Core e o nó Windows devem aparecer como **conectados**, com motor e Guardião em dia.
2. Veja a coluna **Checklist** de cada máquina. Se houver alerta, use a correção oferecida; correções de rede, firewall ou serviços pedem a sua aprovação ([checklist](../plataforma/checklist.md)).
3. Portas usadas, caso precise conferir um firewall (padrão, [componentes](../arquitetura/componentes.md#portas)): `7443/tcp` (painel e conexões do Core), `7444/tcp` (Guardião do Core), `7445/udp` (descoberta) e `7446/tcp` (página reserva do nó).

## Próximos passos

- Autorizar o Grão-Mestre a criar GitHub Apps: [primeiro acesso](primeiro-acesso.md#3-autorizar-o-grão-mestre-a-criar-github-apps).
- Ligar o Telegram: [parear Telegram](parear-telegram.md).
- Criar o primeiro workspace pelo painel (**Workspaces → Criar**): [sessão zero do workspace](../sessao-zero/workspace.md).
- Nó fora da rede local (opção futura, por Tailscale): [descoberta](../conectividade/descoberta.md).
