# Primeiro acesso

> **Assunto:** passo a passo para confiar na CA do Mestre, criar o Dono e autorizar o Grão-Mestre a criar GitHub Apps · **Componente:** Painel · **Atualizado:** 2026-10-12

Guia para quem usa, depois de [instalar o Core](instalar.md). Tenha à mão o que o instalador mostrou: endereço do painel, impressão digital da CA e código de configuração. Regras do login e do assistente: [login](../painel/login.md).

## 1. Confiar na CA do Mestre

O Core usa uma autoridade certificadora (CA) própria, criada na instalação ([segurança](../arquitetura/seguranca.md)). Enquanto o aparelho não confia nela, o navegador avisa que a conexão "não é particular". Faça isto **uma vez em cada aparelho** que vai abrir o painel (PC, notebook, celular).

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

O motor e o Guardião dos nós confiam na CA sozinhos, no pareamento ([pareamento](../conectividade/pareamento.md)). O navegador do PC Windows ainda precisa deste passo, como qualquer aparelho.

## 2. Criar o Dono

1. Abra o endereço do painel. Como ainda não existe nenhum usuário, aparece o **assistente do Dono**.
2. Informe o **código de configuração** mostrado pelo instalador.
3. Preencha nome, nome de login, senha (duas vezes) e **PIN pessoal** (duas vezes). O PIN é pedido de novo em toda ação sensível; regras em [usuários e níveis](../telegram/usuarios-e-niveis.md#pin-pessoal).
4. Pronto: você entra como **Dono**, e o painel mostra os próximos passos (nós, GitHub, Telegram).

Perdeu o código antes de usar? Há um comando local de recuperação, só para root no Core ([login](../painel/login.md#assistente-do-dono-primeira-vez)). Depois que o Dono existe, o assistente não abre mais.

Outras pessoas entram com um código de primeiro acesso que você gera em **Usuários** ([login](../painel/login.md#primeiro-acesso-de-cada-usuário)).

## 3. Autorizar o Grão-Mestre a criar GitHub Apps

Faça isto **depois de [parear o nó Windows](parear-no.md)**, com você **logado no Windows** desse PC: o navegador dedicado roda na sua sessão. O que acontece por dentro está em [criação de Apps](../github/criacao-de-apps.md).

1. No painel, siga o passo **GitHub** dos próximos passos (ou **Apps do GitHub** na administração) e clique para autorizar.
2. No PC Windows, abre-se um **navegador dedicado do Mestre** (Edge com um perfil só dele) na página de login do GitHub.
3. Entre na **sua conta pessoal** do GitHub, com senha e 2FA. Use esse navegador só para isso; os agentes não têm acesso a ele.
4. Volte ao painel e confirme que autoriza o Grão-Mestre a criar e instalar Apps em seu nome.
5. O assistente cria os dois Apps fixos, **"Grão-Mestre"** e **"Mestre"**, no navegador dedicado. Você pode acompanhar na tela; não precisa clicar em nada, a não ser que o GitHub peça senha ou 2FA de novo.
6. O painel mostra um **código** e o endereço `https://github.com/login/device`. Abra o endereço (em qualquer navegador), digite o código e autorize. Isso dá ao Mestre a sua credencial pessoal, usada para incluir repositórios nas instalações dos Apps e, se você delegar, para aprovar em seu nome ([aprovação no GitHub](../github/aprovacao-no-github.md)).
7. Em **Apps do GitHub**, os dois Apps aparecem criados. Eles são instalados nos repositórios quando você cria cada workspace ([sessão zero do workspace](../sessao-zero/workspace.md)).

Se algo travar (GitHub pediu 2FA, tela diferente, PC desligado), o painel mostra um link para você concluir com um clique; depois de [parear o Telegram](parear-telegram.md), esse link também chega por lá.

**Mantenha o login do GitHub válido no navegador dedicado.** Quando a sessão expirar, o [checklist](../plataforma/checklist.md) avisa e você entra de novo no PC Windows.

## Depois

- [Parear o Telegram](parear-telegram.md).
- Criar o primeiro workspace: **Workspaces → Criar** ([sessão zero do workspace](../sessao-zero/workspace.md)).
