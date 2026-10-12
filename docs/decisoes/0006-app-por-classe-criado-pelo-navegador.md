# ADR 0006: Um GitHub App por classe, criado pelo navegador dedicado

> **Assunto:** por que cada classe tem o seu App e como ele é criado sem API · **Componente:** Core · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- O histórico do GitHub precisa mostrar quem fez cada coisa (MVP-5): commits, comentários, atualizações de issue e revisões, cada um com a identidade do papel que fez.
- Como o motor executa tudo ([ADR 0005](0005-motor-executa-github-grao-mestre-porteiro.md)), a identidade é escolhida por ele a cada ação.
- **Limitações do GitHub:**
  - **Não existe API para registrar um App.** O único caminho é o fluxo de manifesto, em que um navegador logado envia um formulário e alguém clica em "Create GitHub App".
  - A primeira instalação também exige navegador.
  - Cada conta pode registrar **até 100 Apps**.
  - O nome tem **até 34 caracteres** e precisa ser **único no GitHub inteiro**.
- As classes nascem sob demanda, criadas pelos usuários, pelo Grão-Mestre ou pelos Mestres de mesa.

## Opções consideradas

1. **Um App só para todas as classes, com a classe no texto** (ex.: "**[QA]** …").
   - Prós: zero criação de Apps, sem risco de limite.
   - Contras:
     - O GitHub mostra o mesmo autor para tudo; a autoria por papel fica só no texto.
     - Commits ficam todos com o mesmo bot.
     - Todos dividem a mesma cota de API.
2. **Um App por classe, criado à mão pelo usuário.**
   - Prós: nada de automação de tela.
   - Contras: cada classe nova trava até o usuário entrar no site, criar o App, gerar a chave e instalar. Isso contradiz a criação sob demanda.
3. **Contas de usuário extras (contas de máquina) por papel.**
   - Prós: identidades "reais".
   - Contras:
     - Cada conta precisa de e-mail, senha e 2FA próprios, gerenciados à mão.
     - Não escala.
     - Mistura identidades de pessoa com identidades de robô.
4. **Um App por classe, criado pelo Grão-Mestre pelo fluxo de manifesto num navegador dedicado.**
   - Prós:
     - Autoria nativa por papel.
     - Cota separada por instalação.
     - A criação é automática depois de uma autorização inicial.
   - Contras: depende das telas do GitHub, que podem mudar.

## Decisão

- **Toda classe usa um App**, compartilhado entre workspaces e instalado em cada repositório, para respeitar o limite de 100.
  - Variações de uma classe podem herdar o App da classe de origem; o Grão-Mestre decide.
  - Os nomes usam um prefixo configurável.
- **Criação:**
  - O Grão-Mestre monta o manifesto.
  - Um **navegador dedicado**, logado na conta do usuário, cria o App e faz a primeira instalação.
  - O motor troca o código pela chave privada, que vai para o cofre.
- **Onde roda o navegador:** no **processo da sessão do usuário do nó Windows** ([nó Windows](../arquitetura/no-windows.md)), fora do alcance dos agentes das mesas.
- **Enquanto o App não existe:** as ações da classe saem com o App "Mestre" e a classe na assinatura.
- **Isolamento e reserva:** a automação fica num componente isolado, testada contra as páginas do GitHub falso ([testes](../qualidade/testes.md#escopo-da-pesada-telegram-e-github)). Se ela quebrar na tela real, o usuário recebe o link pelo Telegram para concluir.
- **Onde estão os detalhes:**
  - Fluxo, manifesto e 2FA: [criação de Apps](../github/criacao-de-apps.md).
  - Identidades, nomes e permissões mínimas: [identidades e Apps](../github/identidades-e-apps.md).
  - Vínculo obrigatório da classe com um App: [classes](../classes/classes.md).

## Consequências

- **Positivas:**
  - Cada commit e comentário mostra o papel de quem fez.
  - Cada App tem a sua cota.
  - Uma classe nova não trava o trabalho, graças ao App "Mestre" provisório.
- **Negativas:**
  - **Disponibilidade:** a criação depende do nó Windows com o usuário logado (ou login automático), porque o navegador só roda no processo da sessão. Sem isso, a classe fica "em criação" e usa o App provisório.
  - **Fragilidade:** a automação depende do HTML do GitHub. Por isso fica isolada e tem caminho manual de reserva.
  - **Limite de Apps:** com 100 Apps por conta, o painel acompanha a contagem ([administração](../painel/administracao.md)) e o Grão-Mestre evita Apps para simples variações.
  - **Segredos:** mais chaves privadas no cofre, uma por App.
  - **Prazo do manifesto:** as três etapas do fluxo precisam terminar em até uma hora.
- **Verificar na implementação:** o redirect local (`http://localhost:<porta>`) e o user access token do App "Grão-Mestre" ([ADR 0008](0008-grao-mestre-aprovador-delegado.md)) para adicionar repositórios sem navegador. Padrão e plano B de cada um: [criação de Apps](../github/criacao-de-apps.md#verificar-na-implementação).
- **Senha e 2FA:** o componente não supõe em que etapa o GitHub os pede; reconhece a tela em qualquer etapa e cai para o link pelo Telegram.

## Fontes

- Fluxo de manifesto: https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest
- Conversão do código: https://docs.github.com/en/rest/apps/apps#create-a-github-app-from-a-manifest
- Limites e nomes: https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app
- Instalação: https://docs.github.com/en/apps/using-github-apps/installing-your-own-github-app , https://docs.github.com/en/rest/apps/installations
