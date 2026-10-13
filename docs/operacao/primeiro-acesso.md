# Primeiro acesso

> **Assunto:** passo a passo para confiar na CA do Mestre, criar o Dono, criar os GitHub Apps fixos pelo link e configurar os perfis de conta · **Componente:** Painel · **Atualizado:** 2026-10-13

Guia para quem usa, depois de [instalar a Cidadela](instalar.md) (no Ubuntu ou no Windows). Tenha à mão o que o instalador mostrou: endereço do painel, impressão digital da CA e código de configuração. Regras do login e do assistente: [login](../painel/login.md).

## 1. Confiar na CA do Mestre

A Cidadela usa uma autoridade certificadora (CA) própria, criada na instalação ([segurança](../arquitetura/seguranca.md)). Enquanto o aparelho não confia nela, o navegador avisa que a conexão "não é particular". Faça isto **uma vez em cada aparelho** que vai abrir o painel (PC, notebook, celular).

### Baixar e conferir o certificado

1. Abra `https://<endereço do painel>/ca.crt` (ex.: `https://servidor.local:7443/ca.crt`). Desta vez o navegador avisa; aceite só para baixar esse arquivo.
2. Confira a impressão digital do arquivo com a que o instalador mostrou. Precisam ser **iguais**; se não forem, pare e não instale.
   - Windows (PowerShell): `certutil -hashfile ca.crt SHA256`
   - Linux ou macOS: `sha256sum ca.crt` (no macOS, `shasum -a 256 ca.crt`)
   - No celular, confira no PC antes e passe o arquivo já conferido.

A rota e o formato do arquivo estão em [segurança](../arquitetura/seguranca.md#tls-e-ca-interna).

### Instalar, conforme o aparelho

| Aparelho | Passos |
|---|---|
| Windows (Edge e Chrome) | Clique duas vezes em `ca.crt` → **Instalar Certificado** → Usuário Atual → "Colocar todos os certificados no repositório a seguir" → **Autoridades de Certificação Raiz Confiáveis** → Concluir → confirme o aviso. |
| Firefox (qualquer sistema) | Configurações → Privacidade e Segurança → Certificados → **Ver certificados** → Autoridades → **Importar** → marque "Confiar nesta CA para identificar sites". |
| Ubuntu (sistema) | `openssl x509 -inform der -in ca.crt -out mestre-ca.crt`, depois `sudo cp mestre-ca.crt /usr/local/share/ca-certificates/` e `sudo update-ca-certificates`. |
| macOS | Abra `ca.crt` no Acesso às Chaves (chaves do Sistema) → clique duas vezes no certificado → Confiar → **Sempre confiar**. |
| Android | Configurações → Segurança → Criptografia e credenciais → Instalar um certificado → **Certificado de CA** → escolha o arquivo. O caminho muda um pouco entre fabricantes. |
| iPhone e iPad | Abra o arquivo no Safari → Ajustes → Geral → VPN e Gerenciamento de Dispositivo → instale o perfil → depois Ajustes → Geral → Sobre → **Ajustes de Confiança de Certificados** → ligue a CA do Mestre. |

Depois, feche e abra o navegador. O painel deve abrir com o cadeado, sem aviso.

O motor e o Guardião de cada máquina confiam na CA sozinhos, na entrada na Guilda ([Guilda](../guilda/guilda.md#credenciais)). O navegador de cada máquina, inclusive a da Cidadela, ainda precisa deste passo, como qualquer aparelho.

## 2. Criar o Dono

1. Abra o endereço do painel. Como ainda não existe nenhum usuário, aparece o **assistente do Dono**.
2. Informe o **código de configuração** mostrado pelo instalador.
3. Preencha nome, nome de login, senha (duas vezes) e **PIN pessoal** (duas vezes). O PIN é pedido de novo em toda ação sensível; regras em [usuários e níveis](../telegram/usuarios-e-niveis.md#pin-pessoal).
4. Pronto: você entra como **Dono**, e o painel mostra os próximos passos (outras máquinas da Guilda, GitHub, Telegram).

Perdeu o código antes de usar? Há um comando local de recuperação, só para root ou administrador na máquina da Cidadela ([login](../painel/login.md#assistente-do-dono-primeira-vez)). Depois que o Dono existe, o assistente não abre mais.

Outras pessoas entram com um código de primeiro acesso que você gera em **Usuários** ([login](../painel/login.md#primeiro-acesso-de-cada-usuário)).

## 3. Criar os Apps do GitHub

O Mestre age no GitHub com dois Apps fixos, **"Grão-Mestre"** e **"Mestre"**, criados na sua conta pessoal. O GitHub exige um clique seu para criar cada App; o resto o Mestre faz. O que acontece por dentro está em [criação de Apps](../github/criacao-de-apps.md).

1. No painel, siga o passo **GitHub** dos próximos passos (ou **Apps do GitHub** na administração). A página mostra o nome e as permissões do App "Grão-Mestre".
2. Clique em **Criar no GitHub**. O seu navegador abre o GitHub (entre na sua conta pessoal, se ele pedir) com o App já preenchido. Clique em **Create GitHub App**.
3. O GitHub volta para o painel sozinho. Na mesma página, abra o link **Ligar o device flow**, marque **Enable Device Flow** na configuração do App e salve.
4. O painel passa para o App **"Mestre"**: clique em **Criar no GitHub** e em **Create GitHub App** de novo.
5. Em **Apps do GitHub**, os dois aparecem criados. Eles são instalados nos repositórios quando você cria cada workspace: o painel, ou o Telegram, manda o link de instalação, e você só escolhe o repositório e clica em **Install** ([sessão zero do workspace](../sessao-zero/workspace.md)). Logo depois da primeira instalação do "Grão-Mestre", o Mestre cria sozinho um repositório privado `mestre-guilda-…` na sua conta, a trava que garante uma só Cidadela; não apague ([repositório da trava](../github/identidades-e-apps.md#repositório-da-trava)). Se o GitHub não deixar, o painel manda o link para você criá-lo com um clique.
6. Opcional: em **Usuários**, **Conectar GitHub** mostra um código e o endereço `https://github.com/login/device`. Autorizar ali dá ao Mestre a sua credencial pessoal, usada para incluir repositórios nas instalações sem link e, se você delegar, para aprovar em seu nome ([aprovação no GitHub](../github/aprovacao-no-github.md)).

Faça isso num aparelho na rede local e que já confia na CA (passo 1): os retornos do GitHub voltam para o painel. Os Apps de classe criados depois seguem o mesmo caminho: o Grão-Mestre manda o link pelo Telegram e pelo painel, e você clica em **Criar** e **Instalar**.

## 4. Configurar os perfis de conta

Cada conta de provedor que os agentes vão usar é um **perfil de conta** de uma máquina ([perfis de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta)). Faça a configuração inicial uma vez por perfil, em cada máquina, inclusive na Cidadela:

1. No painel, abra **Perfis de conta** e escolha a máquina e o provedor. Crie o perfil (ex.: `claude-trabalho`) ou use o padrão.
2. Clique em **Configurar**. Se o CLI do provedor ainda não está no perfil, o painel o instala com o instalador oficial do provedor (depois ele se atualiza sozinho). Em seguida abre o terminal de login do perfil, que roda com a conta dos agentes (`mestre-agente`), e guia o **login** na conta do provedor. Quando o provedor mostra um link ou um código, conclua no seu navegador (no celular ou no PC). Os seus logins pessoais nos CLIs não são usados.
3. **Só no Codex:** no mesmo terminal, o painel pede para abrir `/hooks` e **aprovar uma vez os hooks do Mestre**. Sem isso o Codex não chama o Mestre, e o perfil não recebe agentes.
4. O Mestre roda o [teste de conformidade](../provedores/conformidade.md) do provedor nesse perfil. O perfil fica **pronto** quando o teste passa e o checklist confirma login e, no Codex, a confiança dos hooks (item ALL-11, [checklist](../plataforma/checklist.md)). No Windows, só cabe um perfil Antigravity por máquina ([Antigravity](../provedores/antigravity.md#contas-múltiplas)).

Repita para cada máquina nova, depois de [aceitá-la na Guilda](entrar-na-guilda.md).

## Depois

- [Parear o Telegram](parear-telegram.md).
- Criar o primeiro workspace: **Workspaces → Criar** ([sessão zero do workspace](../sessao-zero/workspace.md)).
