# Crescer a Guilda

> **Assunto:** as jornadas em que a Guilda cresce ou muda de mãos: convidar uma pessoa (convite, primeiro acesso, boas-vindas), adicionar uma máquina (comando, código de entrada, aceite, conferência) e as trocas (passar a Cidadela, passar o posto de Senhor da Guilda), do ponto de vista de cada pessoa · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

Fazer estas três jornadas formarem um caminho só, em vez de telas soltas: quem convida sabe o que entregar e o que a outra pessoa vai ver; quem instala uma máquina sabe o que fazer na linha de comando e o que o Senhor da Guilda decide no painel; todos veem o que acontece numa troca. O desenho é o protótipo clicável desta rodada (decidida em 2026-10-27); as telas por dentro (Usuários, Guilda e máquinas) são das telas de [administração](administracao.md), e a estrutura geral é a de [design](design.md#estrutura-da-tela). Ver também a [primeira jornada](primeira-jornada.md) e, para as regras de cada passo, [login](login.md#primeiro-acesso-de-cada-usuário), [entrar na Guilda](../operacao/entrar-na-guilda.md), [Guilda](../guilda/guilda.md) e [Cidadela e Torres](../guilda/cidadela-e-torres.md).

**Nada aqui tira uma conferência de segurança:** código, impressão digital, PIN e aceite continuam como nos documentos donos. O que muda é o texto, a ordem na tela e o que se mostra antes do PIN. Nenhuma rota nova.

## Comportamento

### 1. Convidar uma pessoa

| Papel | Passo | O que vê | Do servidor |
|---|---|---|---|
| Senhor da Guilda | Começar | Usuários tem **Convidar pessoa** (antes, "cadastrar"). Com a Guilda de uma pessoa só, o Início do Senhor tem a linha opcional "Convidar alguém" (derivada de `GET /api/users`) | `GET /api/users` |
| Senhor da Guilda | Quem vai entrar | nome, nível em palavras ("Aprovador: decide aprovações e conecta o próprio GitHub"), campanhas, e ao lado o que a pessoa **poderá** e **não poderá** fazer naquele nível; login do GitHub em "Mais opções" | `POST /api/users`, com PIN depois do botão |
| Senhor da Guilda | Entregar o convite | o código num `ShownOnce`, validade (24 h) e uso único; as **instruções** (endereço do painel, como confiar na CA, a impressão digital) num texto à parte, com botões **Copiar código** e **Copiar instruções** separados | resposta do `POST /api/users` |
| Pessoa convidada | Confiar na CA | fora do painel, no aparelho dela: baixar `ca.crt`, conferir a impressão digital, instalar ([primeiro acesso](../operacao/primeiro-acesso.md#1-confiar-na-ca-do-mestre)) | `/ca.crt` |
| Pessoa convidada | Primeiro acesso | código, login, senha (duas vezes) e PIN (duas vezes), com a frase de que o PIN não é a senha e ninguém o vê | `POST /api/first-access` |
| Pessoa convidada | Boas-vindas | o Início com um cartão "Você entrou como Aprovador": o que pode e o que fica com o Senhor da Guilda, as campanhas dela e o convite ao Telegram ("peça um código ao Senhor da Guilda") | `GET /api/me` |
| Senhor da Guilda | Conferir | a pessoa aparece "Ainda não entrou" até o primeiro acesso, com **Gerar outro código de acesso** (com PIN); redefinir a senha é o mesmo caminho | `GET /api/users`, `POST …/access-code` |

- **Código e instruções vão em dois botões porque quem tem os dois juntos cria a conta.** A tela aconselha mandar em momentos ou canais diferentes, ou o código pessoalmente; o Mestre nunca o envia pelo Telegram.
- **A impressão digital da CA** vai nas instruções. O painel não a conhece (só o instalador da Cidadela a mostra): o Senhor da Guilda a cola num campo que vive só no navegador, para montar a mensagem; sem ela, **Copiar instruções** fica desligado e a tela diz por quê. Ela deve chegar por um canal que a pessoa sabe ser do Senhor da Guilda, porque quem troca o endereço e a impressão digital leva a pessoa a um painel falso. Mostrá-la no painel pediria uma rota nova e fica fora do MVP.
- **O código não vai em link.** A rota aceita `?codigo=`, mas o convite não o usa: um código na barra de endereço fica no histórico do navegador; a 4I tira esse suporte da rota.
- O código só aparece uma vez; ele some quando o Senhor da Guilda diz que anotou, e nada o guarda.
- **Erros do primeiro acesso** são traduzidos pelo `code` do servidor: `invalid_code` (código errado ou vencido: "peça um código novo"), `password_too_short` (mínimo de 12), `login_taken`, `invalid_pin`, `rate_limited` (o tempo vem em `retry_after_seconds`).

### 2. Adicionar uma máquina

| Papel | Passo | O que vê | Do servidor |
|---|---|---|---|
| Senhor da Guilda | Começar | na faixa "uma máquina só" de **Guilda e máquinas**, o cartão **Adicionar uma máquina**: três passos, o comando por sistema (Ubuntu ou Windows) e, se a máquina está em outra rede, o comando com `--join` ou `JOIN=` com o endereço da Cidadela; "Esperando uma máquina pedir entrada…" | `GET /api/guild` (`single`, `pending`), tópico `guild:main` |
| Quem instala | Pedir entrada | o instalador mostra a Guilda achada e o **código de entrada** de 6 dígitos, o prazo (1 h) e o que fazer: mostrar o código ao Senhor da Guilda, que só aceita se for igual | `POST /join`, `reveal` |
| Senhor da Guilda | Aceitar | o pedido aparece no painel e no Telegram **só depois** de a máquina revelar o código; no painel, ele **digita os 6 dígitos que vê na máquina nova**: se baterem, **Aceitar** liga; se não, só **Recusar** e a frase de que alguém pode estar no meio da conexão. O painel **não mostra o código do pedido antes** de ele digitar o da máquina nova (senão a conferência viraria copiar o que está na tela) e, quando batem, diz "confirme com o PIN", não "pode aceitar". A caixa "Máquina que não deve guardar segredos" vale como a exclusão no aceite e diz o que ela não é: a máquina continua recebendo o banco e os arquivos, então não protege contra uma máquina de terceiros | `POST /api/guild/joins/{id}/decide` com PIN |
| Quem instala | Concluir | o instalador diz que foi aceito, que confia na CA da Guilda e que conectou como Torre; lembra que faltam as contas de IA | `GET /join/{id}`, `/ws/tower` |
| Senhor da Guilda | Conferir | "pc-escritorio entrou": conectada, ficha, **configurar as contas de IA** (sem isso a máquina não recebe agentes), prioridade (só com mais de uma Torre) | `GET /api/guild` |

- **No Telegram a comparação segue a olho** ("Aceitar … Código 482 917"); digitar o código é um reforço só do painel. Quem protege é a comparação feita pelo Senhor da Guilda do código calculado nos dois lados; o PIN confirma que é ele quem decide ([entrada na Guilda](../guilda/guilda.md#entrada-na-guilda)).
- **De quem vem o código:** deve chegar de alguém que o Senhor da Guilda reconhece, em voz alta ou numa ligação; mensagem de quem não se conhece não vale. Com mais de um pedido ao mesmo tempo, recusar todos.
- **Recusar** também pede o PIN e tem o mesmo peso visual de **Aceitar**. Pedido vencido (1 h) some da tela.
- **Perfis de conta da máquina nova:** a Cidadela não lê o estado deles em outra máquina; o passo fica "não sei" com o link para **Perfis de conta**, nunca "feito" ([regra do Início](primeira-jornada.md#regras-e-erros)).
- **Textos do instalador** (os blocos acima, e a variante "não achei nenhuma Guilda") são da implementação do instalador no M7; este documento e o de [entrar na Guilda](../operacao/entrar-na-guilda.md#o-que-o-instalador-mostra) são o contrato do conteúdo.

### 3. Trocas

**Passar a Cidadela** (troca planejada; o endereço do painel segue a Cidadela):

- Na linha de uma Torre, **Passar a Cidadela para <máquina>**. Excluída, fora da rede local ou sem sinal: botão desligado, com o motivo ao lado.
- Antes do PIN, um resumo em cinco linhas: a atual vira Torre; a nova espera 60 s antes de falar com o GitHub e o Telegram, e o que chegar fica na fila; o painel muda de máquina e reconecta sozinho; os agentes da máquina antiga ficam interrompidos e a nova Cidadela os retoma (a conta vem de `agents` da linha; depois, a tela mostra o estado real de cada um, sem afirmar que voltaram); não há desfazer, voltar é outra troca.
- Durante: o Senhor da Guilda vê uma linha do tempo só com o que o retrato mostra (a atual parou de agir; a nova está assumindo; o painel passou). As outras pessoas veem a faixa âmbar "Reconectando à Cidadela…" e o que já estava na tela continua. Quem abre o endereço antigo vê a página "Cidadela suspensa", sem login nem ações. A espera é de pelo menos 1 minuto; o texto não promete prazo.
- Depois: o cartão "Pronto. <máquina> é a Cidadela desde HH:MM", os papéis trocados e a conferência do estado dos agentes.

**Passar o posto de Senhor da Guilda** (a faixa e a página `/transferencia`; não é troca de Cidadela):

- Em Usuários, "Passar o posto": escolhe uma pessoa que **já tem PIN** (as outras aparecem desligadas, com o motivo), vê "você vira Administrador de todas as campanhas" e confirma com o PIN. O pedido espera 24 h, com **Cancelar o pedido** (com PIN).
- A pessoa pedida vê a faixa no alto de todo o painel e a página com o que muda ("você passa a ver e mudar tudo; Lucas vira Administrador; as sessões dos dois terminam") e aceita com o PIN dela. Não há "recusar" no servidor: o texto diz que o pedido vence sozinho ou que o Senhor da Guilda cancela.
- Ao aceitar, as sessões dos dois acabam e o painel vai ao login com o aviso fixo "Suas sessões terminaram porque o posto de Senhor da Guilda mudou. Entre de novo." Ele é ligado por estado do roteador, nunca por parâmetro de endereço, e não cita papel nem nome.

## Regras e erros

- O servidor decide o que cada nível pode; o painel só esconde. Convidar, aceitar máquinas, passar a Cidadela e passar o posto são do Senhor da Guilda ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)).
- Toda ação sensível deste documento pede o PIN, no campo que aparece depois do botão ([PIN em ações sensíveis](login.md#pin-em-ações-sensíveis)).
- Recusa do servidor (`forbidden_level`, `refused` com `reason`, `pin_locked`): a tela diz quem recusou e por quê e nada muda por baixo.
- **Lacunas registradas** (não entram nesta rodada, nenhuma está no [escopo do MVP](../produto/escopo-mvp.md)): a impressão digital da CA no painel; o prazo do convite e se já venceu na linha do usuário; o estado dos perfis de conta de outra máquina; passos internos da troca de Cidadela; recusar um pedido de posto.

## Critérios de aceite

- **CA-CRESC-01** · *rápida* — **Dado** o Senhor da Guilda em Convidar pessoa, **quando** escolhe um nível, **então** a tela mostra o que a pessoa poderá e não poderá fazer, e o campo do PIN só aparece depois de **Criar convite**.
- **CA-CRESC-02** · *rápida* — **Dado** um convite criado, **quando** a tela de entrega abre, **então** o código aparece uma vez, as instruções não o contêm, **Copiar código** e **Copiar instruções** são botões separados e, sem a impressão digital preenchida, **Copiar instruções** fica desligado com o motivo.
- **CA-CRESC-03** · *rápida* — **Dado** o convite, **quando** o Senhor da Guilda diz que anotou, **então** o código some e não está no cache de consultas nem em nenhum link gerado.
- **CA-CRESC-04** · *rápida* — **Dado** o primeiro acesso com código vencido, senha de 11 caracteres, login repetido e excesso de tentativas, **quando** cada resposta chega, **então** cada erro aparece em palavras próprias, com a saída, em português e em inglês.
- **CA-CRESC-05** · *rápida* — **Dado** uma pessoa Aprovador que acaba de entrar, **quando** o Início abre, **então** o cartão de boas-vindas lista só o que o nível dela permite e o convite ao Telegram, e para um Observador não lista conversar nem aprovar.
- **CA-CRESC-06** · *rápida* — **Dado** uma pessoa convidada sem login, **quando** o Senhor da Guilda abre Usuários, **então** ela aparece "Ainda não entrou" com **Gerar outro código de acesso**; depois do primeiro acesso, o selo muda.
- **CA-CRESC-07** · *rápida* — **Dado** a Guilda de uma máquina só, **quando** o Senhor da Guilda abre Guilda e máquinas, **então** o cartão Adicionar uma máquina mostra o comando do sistema escolhido, e com "outra rede" marcada o comando traz `--join` ou `JOIN=` com o endereço digitado.
- **CA-CRESC-08** · *rápida* — **Dado** um pedido de entrada sem código (a máquina ainda não o revelou), **quando** a lista renderiza, **então** não há botão de aceitar e a tela diz que espera a máquina mostrar o código.
- **CA-CRESC-09** · *rápida* — **Dado** um pedido com o código 482 917, **quando** o Senhor da Guilda digita 482 918, **então** **Aceitar** fica desligado, a tela manda recusar e explica por quê, sem mostrar o código do pedido; digitando 482 917, **Aceitar** liga, a tela diz "confirme com o PIN" e pede o PIN; **Recusar** pede o PIN nos dois casos.
- **CA-CRESC-10** · *rápida* — **Dado** a caixa "Máquina compartilhada" marcada, **quando** o Senhor da Guilda aceita, **então** a requisição leva `excluded: true`, o botão diz "Aceitar como excluída" e a caixa avisa que a máquina ainda recebe o banco e os arquivos.
- **CA-CRESC-11** · *rápida* — **Dado** uma Torre recém-aceita, **quando** o Senhor da Guilda abre a conferência, **então** o passo das contas de IA mostra "não sei" com link, nunca "feito", e a prioridade só aparece com mais de uma Torre.
- **CA-CRESC-12** · *rápida* — **Dado** uma Torre excluída, outra fora da rede local e outra sem sinal, **quando** o Senhor da Guilda olha a linha de cada uma, **então** **Passar a Cidadela** está desligado com o motivo; para uma Torre apta, o resumo de cinco linhas aparece antes do campo do PIN e cita a contagem de agentes da máquina antiga.
- **CA-CRESC-13** · *rápida* — **Dado** o retrato do tópico `guild:main` com a Cidadela atual suspensa, **quando** o Senhor da Guilda olha a tela, **então** a linha do tempo mostra só os passos que o retrato sabe dizer e nenhum prazo prometido; quem não é Senhor da Guilda vê só a faixa "Reconectando à Cidadela…".
- **CA-CRESC-14** · *rápida* — **Dado** Usuários com uma pessoa sem PIN, **quando** o Senhor da Guilda escolhe para quem passar o posto, **então** essa pessoa está desligada com o motivo; com o pedido feito, a tela mostra a espera e **Cancelar o pedido**, que pede o PIN.
- **CA-CRESC-15** · *rápida* — **Dado** o pedido de posto para uma pessoa, **quando** ela abre o painel, **então** vê a faixa em todas as telas e a página com o que muda; ao aceitar com o PIN, vai ao login com o aviso fixo, sem parâmetro de endereço, que não cita papel nem nome.
- **CA-CRESC-16** · *rápida* — **Dado** a tela de 390 px, **quando** qualquer tela destas jornadas abre, **então** nada rola na horizontal, os alvos têm 44 px e os blocos de comando têm rolagem própria.
- **CA-CRESC-17** · *completa* — **Dado** o painel com o `e2eserver`, **quando** o Senhor da Guilda convida uma pessoa e ela faz o primeiro acesso e vê as boas-vindas, **então** o convite, o código único e o nível aparecem como descritos, e o mesmo código não serve uma segunda vez.

## Depende de

- [Login](login.md)
- [Administração](administracao.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Guilda](../guilda/guilda.md)
- [Cidadela e Torres](../guilda/cidadela-e-torres.md)
