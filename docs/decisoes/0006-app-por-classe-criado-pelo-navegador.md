# ADR 0006: Um GitHub App por classe, criado pelo usuário com um clique

> **Assunto:** por que cada classe tem o seu App e por que ele é preparado pelo Grão-Mestre e criado pelo usuário, por link, no próprio navegador · **Componente:** Motor · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. **Decisão revista em 2026-10-12:** os Apps passam a ser criados pelo usuário com um clique, a partir do manifesto preparado pelo Grão-Mestre, no lugar do navegador dedicado. O histórico está em "Opções consideradas" e "Decisão". **Termos atualizados em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): Core e nó viram Cidadela e Torre (ou máquina); a decisão não muda.

## Contexto

- O histórico do GitHub precisa mostrar quem fez cada coisa (MVP-5): commits, comentários, atualizações de issue e revisões, cada um com a identidade do papel que fez.
- Como o motor executa tudo ([ADR 0005](0005-motor-executa-github-grao-mestre-porteiro.md)), a identidade é escolhida por ele a cada ação.
- **Limitações do GitHub:**
  - **Não existe API para registrar um App.** O único caminho é o fluxo de manifesto, em que um navegador logado envia um formulário e alguém clica em "Create GitHub App".
  - A primeira instalação também exige navegador.
  - Cada conta pode registrar **até 100 Apps**.
  - O nome tem **até 34 caracteres** e precisa ser **único no GitHub inteiro**.
- As classes nascem sob demanda, criadas pelos usuários, pelo Grão-Mestre ou pelos Mestres de mesa.
- O Mestre não tem interface gráfica em nenhuma máquina ([ADR 0020](0020-quatro-usos-core-em-linux-ou-windows.md)).

## Opções consideradas

1. **Um App só para todas as classes, com a classe no texto** (ex.: "**[QA]** …").
   - Prós: zero criação de Apps, sem risco de limite.
   - Contras: o GitHub mostra o mesmo autor para tudo; commits ficam todos com o mesmo bot; todos dividem a mesma cota de API.
2. **Um App por classe, criado à mão pelo usuário do zero.**
   - Prós: nada de automação.
   - Contras: o usuário teria de preencher nome, permissões e chave a cada classe, com risco de erro.
3. **Contas de usuário extras (contas de máquina) por papel.**
   - Contras: e-mail, senha e 2FA próprios por conta, gerenciados à mão; não escala; mistura identidades de pessoa e de robô.
4. **Um App por classe, criado pelo Grão-Mestre num navegador dedicado** (decisão original de 2026-10-12).
   - Prós: criação automática depois de uma autorização inicial.
   - Contras: exigia um navegador logado no GitHub, rodando na sessão do usuário no Windows (só com usuário logado), automação das telas do GitHub (frágil a qualquer mudança de HTML) e uma autorização ampla para o Grão-Mestre agir em nome do Dono. Não existia no Linux.
5. **Um App por classe, preparado pelo Grão-Mestre e criado pelo usuário com um clique, por link** (decisão revista).
   - Prós: sem navegador do Mestre, sem automação de tela, sem autorização ampla; funciona nos quatro usos, do celular ou do PC; a senha e o 2FA do GitHub ficam só no navegador do usuário.
   - Contras: cada App novo espera um clique do Dono.

## Decisão

- **Toda classe usa um App**, compartilhado entre workspaces e instalado em cada repositório, para respeitar o limite de 100.
  - Variações de uma classe podem herdar o App da classe de origem; o Grão-Mestre decide.
  - Os nomes usam um prefixo configurável.
- **Criação (opção 5):**
  - O Grão-Mestre pede o App; o motor monta o manifesto e gera um link de uso único.
  - O link vai ao Dono pelo Telegram e aparece no painel. Ele abre no próprio navegador, clica em "Criar" e depois em "Instalar".
  - Os redirecionamentos do manifesto voltam ao painel da Cidadela, que troca o código pela chave privada, guarda no cofre e segue sozinho.
  - Os Apps fixos "Grão-Mestre" e "Mestre" seguem o mesmo fluxo no primeiro acesso.
- **Por que a opção 4 saiu:** a decisão de não ter interface gráfica em lugar nenhum ([ADR 0020](0020-quatro-usos-core-em-linux-ou-windows.md)) tirou o processo da sessão do Windows, onde o navegador dedicado rodava; e o fluxo por link já era o caminho de reserva da opção 4. Ficou o caminho mais simples, que vale em qualquer sistema.
- **Enquanto o App não existe:** as ações da classe saem com o App "Mestre" e a classe na assinatura.
- **Onde estão os detalhes:**
  - Fluxo, manifesto e link: [criação de Apps](../github/criacao-de-apps.md).
  - Identidades, nomes e permissões mínimas: [identidades e Apps](../github/identidades-e-apps.md).
  - Vínculo obrigatório da classe com um App: [classes](../classes/classes.md).

## Consequências

- **Positivas:**
  - Cada commit e comentário mostra o papel de quem fez; cada App tem a sua cota.
  - Uma classe nova não trava o trabalho, graças ao App "Mestre" provisório.
  - Nenhuma credencial do GitHub do Dono fica com o Mestre para criar Apps; nada depende de telas do GitHub além do clique do próprio usuário.
- **Negativas:**
  - **Espera pelo Dono:** o App só nasce quando o Dono clica. Até lá, a classe usa o App provisório e aparece "em criação".
  - **Rede local:** os redirecionamentos só chegam à Cidadela se o aparelho do Dono estiver na rede local e confiar na CA.
  - **Limite de Apps:** com 100 Apps por conta, o painel acompanha a contagem ([administração](../painel/administracao.md)) e o Grão-Mestre evita Apps para simples variações.
  - **Segredos:** mais chaves privadas no cofre, uma por App.
  - **Prazo do manifesto:** a troca do código precisa acontecer em até uma hora depois do clique.
- **Pontos em aberto, conferidos na implementação** (endereço da Cidadela na rede local como `redirect_url` e `setup_url`; o user access token do App "Grão-Mestre", [ADR 0008](0008-grao-mestre-aprovador-delegado.md), para adicionar repositórios sem link): os itens completos estão em [criação de Apps](../github/criacao-de-apps.md#verificar-na-implementação).

## Fontes

- Fluxo de manifesto: https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest
- Conversão do código: https://docs.github.com/en/rest/apps/apps#create-a-github-app-from-a-manifest
- Limites e nomes: https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app
- Instalação: https://docs.github.com/en/apps/using-github-apps/installing-your-own-github-app , https://docs.github.com/en/rest/apps/installations
