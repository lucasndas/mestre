# Telas de Configurações

> **Assunto:** o desenho de cada tela de Configurações pelo padrão de tela de administração (para que serve, situação, próximo passo, vazio, erro), o que muda no texto e na ordem, o que fica igual e o que pede backend novo · **Componente:** Painel · **Atualizado:** 2026-10-27

Documento de referência da M6, etapa 4 (unidades 4G1 e 4G2 da [ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)). Decidido em 2026-10-27, depois de o Senhor da Guilda achar as telas densas, cheias de texto cru do servidor e de palavras sem explicação, e de ver o protótipo clicável. É **só apresentação e texto**: campos, rotas, níveis, PIN e auditoria são os de [administração](administracao.md), [rotas](administracao-rotas.md) e [regras e cadeia](administracao-regras.md), e o índice de Configurações é o de [design](design.md#estrutura-da-tela). O padrão está em [design](design.md#tela-de-administração); abaixo, o que cada tela põe em cada lugar.

## Peças comuns (unidade 4G1)

`screens/admin/kit.tsx` ganha, sobre o `PageHeader` e o `Alert` de hoje: a **faixa de situação** (`StatusStrip`: tom, título, apoio, ações), o **vazio** (`EmptyState`: título, por que importa, passos ou ação), o **erro de leitura** (`LoadError`: o que, motivo traduzido por `code`, **Tentar de novo**, detalhes técnicos recolhidos), a linha **Vocabulário** (`terms`, só `Term`) e o botão desligado com o nível exigido (`LevelGate`). Os textos comuns (Tentar de novo, Detalhes técnicos, Exige o nível…) entram em `common.ts`, nos dois idiomas. Nenhuma tela de administração escreve de novo a sua faixa, vazio ou erro.

## Telas de quem chega agora

Os nomes e os grupos são os do índice de Configurações que a 4B pôs na `main` (`settingsMap.ts` e o catálogo `shell`): **Supervisão da cadeia**, **Delegações**, **Consumo de uso**, e assim por diante.



| Tela | Para que serve (frase da tela) | Faixa de situação e botão principal | Vazio e próximo passo |
|---|---|---|---|
| Perfis de conta | "As contas de IA (Claude, Codex, Antigravity) que os agentes usam para trabalhar nesta máquina. Sem uma conta entrada, nenhum agente roda." | "N de M contas prontas"; sem perfil, "Nenhuma conta de IA entrou ainda"; botão **Entrar em outra conta** | três cartões de provedor; escolher um pede o nome (sugerido: `claude-1`) e o PIN |
| Apps do GitHub | "Os robôs do Mestre no GitHub: cada ação dos agentes aparece com o nome de um deles, e você pode cortar o acesso quando quiser." | "N Apps esperando um passo seu" ou "Todos prontos"; sem App, **Gerar o link do App Grão-Mestre** (o servidor diz qual App fixo vem agora, `next_fixed`) | explica o que é um App e os três passos (gerar o link com PIN, abrir no navegador logado no GitHub, instalar nos repositórios) |
| Segredos | "As senhas e chaves que o Mestre guarda no cofre… o valor nunca volta a aparecer." | "N credenciais de site, M perto de vencer"; **Cadastrar credencial de site** | a tabela "Onde cada chave vai" aparece sempre; sem credencial de site, a faixa (neutra) diz "Nenhum segredo seu no cofre ainda" e que só há os do Mestre, sem caixa tracejada repetindo |
| Classes | "As funções dos agentes (Dev, QA, Revisor): o que cada uma pode fazer e qual App do GitHub ela usa." | "N propostas esperando a sua aprovação" ou "N classes em vigor"; sem classe, **Falar com o Grão-Mestre** | explica o que é uma classe e como nasce: o Grão-Mestre propõe, você aprova, o App é preparado |

### Perfis de conta (o que cresce no M7)

- **Máquinas:** a tela lista os perfis da máquina onde o painel roda (a Cidadela) e diz isso; os provedores logados de cada máquina estão na ficha dela, na Guilda (4I). Os perfis das outras máquinas entram aqui "em breve", quando a configuração guiada chegar às Torres.
- **Hoje (sem configuração guiada, `advance` responde `unavailable`):** cada perfil é um cartão com nome, provedor, "padrão" e o selo "login não conferido", e um texto "Login feito à mão": abrir o terminal da máquina e entrar na conta do provedor (no Codex, aprovar também os hooks do Mestre em `/hooks`). Tornar padrão e **Remover** (que diz que a pasta com o login fica) pedem PIN. 
- **Com a configuração guiada (M7, [ligação de produção](../desenvolvimento/ligacao-de-producao.md)):** o **mesmo cartão** ganha as etapas (instalar o programa, entrar na conta, aprovar os hooks só no Codex, conferir; cada uma "feita", "agora" ou "depois"), a linha "Esperando: …", **Abrir o terminal** e **Continuar a configuração**, e o selo passa a "pronto" ou "falta um passo". A falha da conferência diz o motivo e o que fazer. Nada muda de lugar; o texto à mão sai quando a guiada está disponível.
- Igual: campos (nome, provedor), PIN para criar, padrão e remover, rotas de `/api/profiles`.

### Apps do GitHub

- A contagem "N de 100" vira um medidor com a frase de apoio; o campo "outros Apps da conta" fica recolhido ("O Mestre não vê os seus outros Apps. Informar quantos são"). Com 90 ou mais o medidor fica vermelho.
- **Os pedidos de criação deixam de ser seção solta:** cada App tem o seu estado e o próximo passo por [estado](../github/criacao-de-apps.md#estados-de-um-app): em criação com o link emitido ("abra no navegador logado no GitHub e confirme; vale até…"), link vencido ("**Gerar novo link**", com PIN) e pronto (instalado em: campanhas). O link continua aparecendo uma vez, com **Copiar** e **Já copiei**.
- Cada App é um cartão: o nome técnico em fonte de código com o selo "fixo" ou "Classe X" e o estado, a frase do que o App faz ("App Grão-Mestre: fala com você e aprova no GitHub."), o próximo passo e, à direita, **Instalado em** com as campanhas ("nenhuma campanha ainda"). Link vencido tem o botão cheio; a lista chama-se "Apps do Mestre" e a faixa diz "N do Mestre, M de 100 na conta".
- Quem não é Senhor da Guilda só lê, e a tela diz isso em vez de esconder o botão em silêncio.

### Segredos

- **"Onde cada chave vai":** site (aqui, **Cadastrar credencial de site**, preferindo senha de app, token ou chave de API); chave de App do GitHub (entra no cofre sozinha na criação, link para Apps); conta de Claude, Codex ou Antigravity (por login, link para Perfis de conta); token do bot do Telegram (guardado pelo Mestre na instalação); chave de API de provedor ([pendência](#o-que-pede-backend-novo)).
- Os segredos do próprio Mestre (`guild_ca_key`, `telegram_bot_token`, `github_app_key`…) saem da lista de credenciais e vão para "Guardados pelo Mestre": nome legível, [impressão](../plataforma/cofre.md) e o nome técnico ao lado, sem edição. As credenciais de site são cartões: cabeçalho com o nome, o selo do tipo ("Senha de app") e o selo "vence em N dias", com **Substituir** e **Apagar** (com PIN) ao lado, e uma linha de colunas (classes, domínios, quem responde ao segundo fator, último uso, impressão) e a frase do aviso de validade; embaixo do nome vai o domínio, e não a página de login: a URL fica dentro da parte cifrada ([cofre](../plataforma/cofre.md#credenciais-de-site)) e a listagem nunca abre o valor (CA-COFRE-07); a página só aparece ao substituir.
- O formulário mantém todos os campos de hoje; dono, validade, seletores e texto de sucesso ficam em "Mais opções". A chave de recuperação vira a seção "Cópia de segurança do cofre", com o mesmo aviso, PIN e `ShownOnce`, e a frase de que os backups que já existem continuam exigindo a chave antiga.

### Classes

- Cartões por escopo ("Valem em toda a Guilda", "Da campanha X", "Das mesas") em vez de uma tabela: nome, estado, selo do App e versão. "App novo · em criação" vira "O App desta classe ainda não está pronto. Sem ele, QA não consegue abrir PRs", com o link **Gerar o link em Apps do GitHub**.
- As propostas ficam em cima, com a origem (do Grão-Mestre ou alterada fora do Mestre), "não testada" e, havendo problemas, a lista deles e o caminho ("peça ao Grão-Mestre ou veja o teto da campanha"), sem **Aprovar**. **Aprovar** continua levando o hash visto e pedindo PIN. A faixa de 90 Apps continua. Do M7 e do M8 entram nos cartões **Testar de novo** e as classes sugeridas.

### Guilda e Usuários

Fora desta unidade: o desenho é o da unidade 4I (a Guilda cresce), que segue o mesmo padrão e constrói as duas telas (convidar, entrar na Guilda, trocar a Cidadela e o posto de Senhor da Guilda). O índice de Configurações já as lista como **Guilda** e **Usuários**.

## Regras e consumo, auditoria e bases e fichas

Só o padrão: a frase de para que serve, a faixa de situação, o vazio com saída e o erro. As frases e o que cada uma põe no vazio estão em [regras e cadeia](administracao-regras.md#apresentação). Consumo: o resumo do limite sobe para a faixa e "sem dado" é explicado ao lado do número. Auditoria: uma tabela Quando, Quem e O que mudou; a última coluna é uma frase traduzida da ação (uma ação que o painel ainda não conhece aparece como "Outra ação do painel", com o código ao lado), com antes/depois num detalhe recolhido. Bases e fichas: só o cabeçalho, a faixa e o vazio; o YAML e o importar tudo ou nada ficam como estão.

## O que pede backend novo

- **Chave de API de provedor pelo painel:** o cofre tem o tipo `provider_api_key` (usado quando uma conta usa chave em vez de login), mas `POST /api/secrets/site` só cria `site_credential`. Enquanto não houver rota, "Onde cada chave vai" diz "ainda não dá para cadastrar por aqui". Fora do [escopo do MVP](../produto/escopo-mvp.md) até o Senhor da Guilda pedir.
- **Termo "App" no glossário:** a ajuda de "App do GitHub" não tem linha no [glossário](../produto/glossario.md); a unidade 4G1 a acrescenta (português e inglês, com os arquivos gerados), porque o painel não escreve ajuda no código.
- Nenhuma rota nova, nenhum campo novo e nenhuma regra mudada.
