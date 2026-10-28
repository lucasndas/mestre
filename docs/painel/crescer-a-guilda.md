# Crescer a Guilda

> **Assunto:** as jornadas em que a Guilda cresce ou muda de mãos: convidar uma pessoa (convite em três partes, primeiro acesso, boas-vindas), adicionar uma máquina (pedido de entrada, comparação do código, aceite, conferência) e as trocas (passar a Cidadela, passar o posto de Senhor da Guilda), do ponto de vista de cada pessoa, e as telas de Usuários e Guilda · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

Fazer estas três jornadas formarem um caminho só, em vez de telas soltas: quem convida sabe o que entregar e o que a outra pessoa vai ver; quem recebe um pedido de máquina sabe o que comparar e o que decidir; todos veem o que acontece numa troca. O desenho é o protótipo clicável desta rodada (decidida em 2026-10-27). **As telas Usuários e Guilda são desta unidade** (a 4G não as implementa): seguem o padrão de tela de administração (faixa de situação com o próximo passo, cartões, vazio com saída, erro com "Tentar de novo") e a casca de [design](design.md#estrutura-da-tela). Regras de cada passo: [login](login.md#primeiro-acesso-de-cada-usuário), [entrar na Guilda](../operacao/entrar-na-guilda.md), [Guilda](../guilda/guilda.md), [Cidadela e Torres](../guilda/cidadela-e-torres.md); a jornada maior, em [primeira jornada](primeira-jornada.md).

**Nada aqui tira uma conferência de segurança:** código, código de conferência do certificado, PIN e aceite continuam como nos documentos donos. Muda o texto, a ordem na tela e o que se mostra antes do PIN. Nenhuma rota nova.

**Limite do que existe (M6) e do que vem depois (M7):** o painel já recebe o pedido de entrada, compara o código, aceita ou recusa, confere a Torre e passa a Cidadela (M6). O **instalador**, a busca automática da Guilda, o `--join` e o fim do processo na máquina nova são do M7: na tela aparecem como "em breve" (sem citar marco), e os textos deles ficam em [o que o instalador mostra](../operacao/entrar-na-guilda.md#o-que-o-instalador-mostra), como contrato.

## Comportamento

### 1. Convidar uma pessoa

| Papel | Passo | O que vê | Do servidor |
|---|---|---|---|
| Senhor da Guilda | Começar | **Usuários**: faixa "Só você está na Guilda" com **Convidar pessoa** e um vazio que explica a sequência. Com a Guilda de uma pessoa só, o Início tem a linha opcional "Convidar alguém" | `GET /api/users` |
| Senhor da Guilda | Quem vai entrar | página própria `/usuarios/convidar` ("Passo 1 de 2"): nome, nível em cartões selecionáveis e, ao lado, "<nome> como <nível>" com as listas Poderá e Não poderá e a frase "O PIN é só dela"; campanhas, login do GitHub em "Mais opções" | `POST /api/users`, PIN depois do botão |
| Senhor da Guilda | Entregar o convite | página própria `/usuarios/convite` ("Passo 2 de 2"): **três partes** (abaixo), cada uma num cartão com o seu botão de copiar. O código de primeiro acesso fica só na memória da aba: sem ele (recarregou a página), a tela volta para Usuários | resposta do `POST /api/users` |
| Pessoa convidada | Confiar no certificado | fora do painel, no aparelho dela, em palavras do dia a dia | `/ca.crt` |
| Pessoa convidada | Primeiro acesso | código, login, senha (duas vezes), PIN (duas vezes) e a frase de que o PIN não é a senha | `POST /api/first-access` |
| Pessoa convidada | Boas-vindas | cartão "Você entrou como Aprovador": o que pode, o que fica com o Senhor da Guilda, as campanhas e o convite ao Telegram | `GET /api/me` |
| Senhor da Guilda | Conferir | a pessoa aparece "Ainda não entrou" até o primeiro acesso; **Gerenciar** abre Códigos, Acesso e Posto de Senhor da Guilda, cada ação com a frase do efeito à esquerda e o botão à direita, e **com PIN** (gerar código, suspender, passar o posto: a lista é a de [PIN pessoal](../telegram/usuarios-e-niveis.md#pin-pessoal)). **Dados** (nome, nível, campanhas e login do GitHub, com Salvar mudanças) fica de propósito, por decisão do dono: o protótipo não o desenha, mas é a única forma de editar uma pessoa | `GET /api/users`, `PATCH /api/users/{id}`, `POST …/access-code` |

**O convite em três partes** (o endereço e o código de conferência precisam de integridade; o código precisa de segredo):

1. **Instruções:** endereço do painel e os passos. Vão por qualquer canal. Não têm o código de conferência nem o de primeiro acesso, e trazem a regra de não instalar se o código de conferência for diferente.
2. **Código de conferência do certificado** (a impressão digital SHA-256 da CA, em palavras simples): dito em voz alta, pessoalmente ou numa ligação, ou enviado por um canal diferente do da parte 1, com a lembrança de que, se não for igual, a pessoa não instala. O painel **calcula** o valor no navegador a partir de `/ca.crt` (rota que já existe, pela conexão que o Senhor da Guilda já verificou) e ele só confere se é igual ao que o instalador da Cidadela mostrou; se não for, não entrega o convite. Endereço e código de conferência na mesma mensagem deixariam quem a alterasse levar a pessoa a um painel falso.
3. **Código de primeiro acesso** (`ShownOnce`): uso único, 24 h, aparece uma vez. Vai **só depois de a pessoa dizer que fez o passo do certificado**, e o painel só o mostra, dentro do cartão da parte 3 e com "Anotei, pode sumir" no fim, depois que o Senhor da Guilda **marca** "A pessoa me disse que conferiu o código de conferência"; por um canal em que o Senhor da Guilda a reconheça; quem prefere não anotar gera outro código na hora (com PIN), e o anterior deixa de valer. O Mestre nunca o envia pelo Telegram e ele nunca vai em link (a 4I tira da rota o `?codigo=`, para o código não ficar no histórico do navegador).

**Onde vive o texto do certificado:** é o conteúdo do convite (parte 1), lido no aparelho da pessoa; o Mestre não serve página nenhuma antes de o aparelho confiar nele. A parte 1 leva sozinha a regra de **não instalar se o código de conferência for diferente**.

**Certificado, em palavras do dia a dia:** a pessoa lê "Faça o seu aparelho confiar no Mestre": baixar o arquivo, **conferir o código de conferência** do arquivo com o que o Senhor da Guilda passou (se for diferente, **não instalar** e avisar), instalar e reabrir o navegador. "Detalhes técnicos" traz o nome técnico (CA, SHA-256), os comandos (`sha256sum`, `certutil`), a tabela por aparelho de [primeiro acesso](../operacao/primeiro-acesso.md#1-confiar-na-ca-do-mestre) e o aviso de que não é o campo "Assinatura" nem a "Impressão digital" (SHA-1) que o Windows mostra.

**Erros do primeiro acesso** são traduzidos pelo `code`: `invalid_code`, `password_too_short` (mínimo 12), `login_taken`, `invalid_pin`, `rate_limited` (o tempo vem em `retry_after_seconds`).

### 2. Adicionar uma máquina

| Papel | Passo | O que vê | Do servidor | Quando |
|---|---|---|---|---|
| Senhor da Guilda | Começar | **Guilda**: faixa "1 máquina, e é a Cidadela" e o cartão **Adicionar uma segunda máquina** com quatro passos; o primeiro é "em breve" | `GET /api/guild` (`single`, `pending`) | M6 |
| Quem instala | Pedir entrada | o instalador mostra a Guilda achada, o **código de entrada** de 6 dígitos e o prazo (1 h) | `POST /join`, `reveal` | M7 |
| Senhor da Guilda | Aceitar | o pedido sobe para o alto da Guilda **só depois** de a máquina revelar o código, e a faixa passa a dizer "<máquina> quer entrar na Guilda" com "Compare o código de entrada e decida"; o cartão do pedido tem o título, a hora do pedido, a instrução de olhar a tela da máquina nova, o campo "000 000", o aviso de mais de um pedido e a nota do Telegram; cada máquina mostra um selo Conectada ou Sem sinal ao lado do papel, e a conferência da Torre fica recolhida até ser pedida; ele **digita os 6 dígitos que vê na máquina nova**; se baterem, **Aceitar** liga e a tela diz "confirme com o PIN"; se não, só **Recusar** | `POST /api/guild/joins/{id}/decide` com PIN | M6 |
| Quem instala | Concluir | "Aceito", certificados, confia na Guilda, conectada como Torre | `GET /join/{id}`, `/ws/tower` | M7 |
| Senhor da Guilda | Conferir | "pc-escritorio entrou": conectada, **configurar as contas de IA** (sem isso nenhum agente roda nela), prioridade só com mais de uma Torre | `GET /api/guild` | M6 |

- **A caixa "Máquina que não deve guardar segredos"** vale como a exclusão no aceite e diz o que ela não é: a máquina não recebe o cofre nem assume o comando, **mas continua recebendo o banco e os arquivos**; não protege contra uma máquina de terceiros.
- **O painel não mostra o código do pedido antes** de o Senhor da Guilda digitar o da máquina nova (senão a conferência viraria copiar o que está na tela). No Telegram a comparação segue a olho. Quem protege é a comparação do código calculado nos dois lados; o PIN confirma que é ele quem decide ([entrada na Guilda](../guilda/guilda.md#entrada-na-guilda)).
- **De quem vem o código:** de alguém que o Senhor da Guilda reconhece, em voz alta ou numa ligação. Com mais de um pedido ao mesmo tempo, a tela avisa para recusar todos. **Recusar** também pede o PIN e tem o mesmo peso de **Aceitar**.
- **Contas de IA da máquina nova:** a Cidadela não lê o estado delas em outra máquina; o passo fica "não sei" com o link, nunca "feito" ([regra do Início](primeira-jornada.md#regras-e-erros)).

### 3. Trocas

**Passar a Cidadela** (troca planejada; M6):

- No cartão de uma Torre, **Passar a Cidadela para <máquina>**; as ações raras (excluir, prioridade, remover) ficam em "Mais…". Excluída, fora da rede local ou sem sinal: botão desligado, com o motivo.
- Antes do PIN, um resumo: a atual vira Torre; a nova espera 60 s antes de falar com o GitHub e o Telegram, e o que chegar fica na fila; o painel muda de máquina e reconecta sozinho; os agentes da máquina antiga ficam interrompidos e a nova Cidadela os retoma; não há desfazer, voltar é outra troca.
- Durante: o Senhor da Guilda vê uma linha do tempo só com o que o retrato mostra (a atual parou de agir; a nova assume; o painel passou), sem prazo prometido. As outras pessoas veem a faixa âmbar "Reconectando à Cidadela…". O endereço antigo mostra a página "Cidadela suspensa", sem login nem ações.
- Depois: "Pronto. <máquina> é a Cidadela desde HH:MM" e "confira no canvas o estado de cada agente" (sem afirmar que voltaram).

**Passar o posto de Senhor da Guilda** (a faixa e a página `/transferencia`; não é troca de Cidadela):

- Em Usuários › Gerenciar › Posto de Senhor da Guilda, só quem **já tem PIN** pode receber (as outras ficam desligadas, com o motivo). A tela diz que quem pede vira Administrador de todas as campanhas e confirma com o PIN. O pedido espera 24 h, com **Cancelar o pedido** (também com PIN).
- A pessoa pedida vê a faixa em todo o painel e a página "Aceitar o posto de Senhor da Guilda" (com o item Início marcado no menu), com o cartão "Se você aceitar" e o botão "Aceitar o posto"; aceita com o PIN dela. Não há "recusar" no servidor: o pedido vence sozinho ou o Senhor da Guilda cancela.
- Ao aceitar, as sessões dos dois acabam e o login mostra o aviso fixo "Suas sessões terminaram porque o posto de Senhor da Guilda mudou. Entre de novo.", ligado por estado do roteador e nunca por parâmetro de endereço; não cita papel nem nome.

## Regras e erros

- O servidor decide o que cada nível pode; o painel só esconde ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)). Toda ação sensível pede o PIN, no campo que aparece depois do botão ([PIN](login.md#pin-em-ações-sensíveis)).
- Os termos (Nível, PIN, Cidadela, Torre, Máquina excluída, Certificado do Mestre, Código de conferência, Código de entrada, Código de primeiro acesso) vêm de `Term`, com o balão "No mundo real". PIN, Certificado do Mestre, Código de conferência e os dois códigos de entrada não têm linha no [glossário](../produto/glossario.md): a 4I as acrescenta (português e inglês, com os arquivos gerados).
- Recusa do servidor (`forbidden_level`, `refused` com `reason`, `pin_locked`): a tela diz quem recusou e por quê e nada muda por baixo.
- **Lacunas registradas** (fora do [escopo do MVP](../produto/escopo-mvp.md), sem rota nova): o prazo do convite na lista; o estado dos perfis de conta de outra máquina; passos internos da troca de Cidadela; recusar um pedido de posto.

## Critérios de aceite

- **CA-CRESC-01** · *rápida* — **Dado** o Senhor da Guilda em Convidar pessoa, **quando** escolhe um nível, **então** a tela mostra o que a pessoa poderá e não poderá fazer, e o campo do PIN só aparece depois de **Criar convite**.
- **CA-CRESC-02** · *rápida* — **Dado** um convite criado, **quando** a entrega abre, **então** há três partes com botões de copiar separados: as instruções não contêm o código de conferência nem o de primeiro acesso, o código de conferência vem calculado de `/ca.crt`, nunca digitado, e o código de primeiro acesso só aparece depois de o Senhor da Guilda marcar que a pessoa conferiu o certificado.
- **CA-CRESC-03** · *rápida* — **Dado** o convite, **quando** o Senhor da Guilda diz que anotou, **então** o código some e não está no cache de consultas nem em nenhum link gerado; a rota de primeiro acesso não aceita mais o código na barra de endereço.
- **CA-CRESC-04** · *rápida* — **Dado** o primeiro acesso com código vencido, senha de 11 caracteres, login repetido e excesso de tentativas, **quando** cada resposta chega, **então** cada erro aparece em palavras próprias, com a saída, em português e em inglês.
- **CA-CRESC-05** · *rápida* — **Dado** a pessoa convidada na tela do certificado, **quando** a lê, **então** "SHA-256" e "CA" só aparecem em "Detalhes técnicos", e o passo de conferir o código manda não instalar se ele for diferente.
- **CA-CRESC-06** · *rápida* — **Dado** uma pessoa Aprovador que acaba de entrar, **quando** o Início abre, **então** o cartão de boas-vindas lista só o que o nível dela permite e o convite ao Telegram; para um Observador não lista conversar nem aprovar.
- **CA-CRESC-07** · *rápida* — **Dado** uma pessoa convidada sem login, **quando** o Senhor da Guilda abre Usuários, **então** ela aparece "Ainda não entrou", **Gerenciar** mostra Códigos, Acesso e Posto com a frase do efeito de cada ação, e a leitura que falha mostra o erro com **Tentar de novo**.
- **CA-CRESC-08** · *rápida* — **Dado** a Guilda de uma máquina só, **quando** o Senhor da Guilda abre Guilda, **então** a faixa diz "1 máquina, e é a Cidadela" e o cartão **Adicionar uma segunda máquina** mostra o passo do instalador como "em breve", sem a palavra "M7".
- **CA-CRESC-09** · *rápida* — **Dado** um pedido sem código (a máquina ainda não o revelou), **quando** a lista renderiza, **então** não há botão de aceitar e a tela diz que espera a máquina mostrar o código.
- **CA-CRESC-10** · *rápida* — **Dado** um pedido com o código 482 917, **quando** o Senhor da Guilda digita 482 918, **então** **Aceitar** fica desligado e a tela manda recusar sem mostrar o código do pedido; digitando 482 917, **Aceitar** liga e pede o PIN; **Recusar** pede o PIN nos dois casos.
- **CA-CRESC-11** · *rápida* — **Dado** a caixa "Máquina que não deve guardar segredos" marcada, **quando** o Senhor da Guilda aceita, **então** a requisição leva `excluded: true`, o botão diz "Aceitar como excluída" e a caixa avisa que a máquina ainda recebe o banco e os arquivos.
- **CA-CRESC-12** · *rápida* — **Dado** uma Torre recém-aceita, **quando** o Senhor da Guilda abre a conferência, **então** o passo das contas de IA mostra "não sei" com link, nunca "feito".
- **CA-CRESC-13** · *rápida* — **Dado** uma Torre excluída, outra fora da rede local e outra sem sinal, **quando** o Senhor da Guilda olha o cartão de cada uma, **então** **Passar a Cidadela** está desligado com o motivo; numa Torre apta, o resumo aparece antes do PIN e cita a contagem de agentes da máquina antiga.
- **CA-CRESC-14** · *rápida* — **Dado** o retrato do tópico `guild:main` com a Cidadela atual suspensa, **quando** o Senhor da Guilda olha a tela, **então** a linha do tempo mostra só o que o retrato diz e nenhum prazo prometido; quem não é Senhor da Guilda vê só "Reconectando à Cidadela…".
- **CA-CRESC-15** · *rápida* — **Dado** Usuários com uma pessoa sem PIN, **quando** o Senhor da Guilda escolhe para quem passar o posto, **então** essa pessoa está desligada com o motivo; com o pedido feito, a tela mostra a espera e **Cancelar o pedido**, que pede o PIN.
- **CA-CRESC-16** · *rápida* — **Dado** o pedido de posto, **quando** a pessoa abre o painel, **então** vê a faixa em todas as telas e a página com o que muda; ao aceitar com o PIN, vai ao login com o aviso fixo, sem parâmetro de endereço, que não cita papel nem nome.
- **CA-CRESC-17** · *rápida* — **Dado** a tela de 390 px, **quando** qualquer tela destas jornadas abre, **então** nada rola na horizontal, os alvos têm 44 px e os blocos de comando têm rolagem própria.
- **CA-CRESC-19** · *rápida* — **Dado** dois pedidos de entrada pendentes ao mesmo tempo, **quando** a Guilda renderiza, **então** a tela avisa para recusar todos e pedir para tentarem de novo.
- **CA-CRESC-18** · *completa* — **Dado** o painel com o `e2eserver`, **quando** o Senhor da Guilda convida uma pessoa e ela faz o primeiro acesso e vê as boas-vindas, **então** o convite, o código único e o nível aparecem como descritos, e o mesmo código não serve uma segunda vez.

## Depende de

- [Login](login.md)
- [Administração](administracao.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Guilda](../guilda/guilda.md)
- [Cidadela e Torres](../guilda/cidadela-e-torres.md)
