# Design do painel

> **Assunto:** direção visual, estrutura das telas (menu de quatro itens, migalhas, barra de cima, Início, Configurações), princípios e padrões de interface do painel (estados, recusa e desfazer, reconexão, densidade, ajuda dos termos, fuso, idioma, acessibilidade) · **Componente:** Painel · **Atualizado:** 2026-10-27

Documento de referência: toda tela do painel segue o que está aqui, e uma tela nova que precise fugir da regra muda este documento na mesma alteração. Os **valores** (cores, fontes, raios, sombras) moram no arquivo de tokens, [`web/src/index.css`](../../web/src/index.css), no bloco `@theme`; este documento não os copia. Cores, fontes e sombras de uma tela vêm sempre de um token, nunca de um valor solto. A pilha de ferramentas está no [ADR 0027](../decisoes/0027-pilha-do-painel.md) e o painel bilíngue no [ADR 0028](../decisoes/0028-painel-bilingue-e-marco-m6-mais.md).

## Direção: Pergaminho

Escolhida pelo Senhor da Guilda em 2026-10-26, entre três propostas vistas em telas reais (Pergaminho, Sala de controle e Cidadela calma), porque o público é de pessoas não técnicas e o produto quer personalidade.

- **Sensação:** um caderno de aventura bem cuidado que, ao mesmo tempo, é uma ferramenta de trabalho calma. O RPG está nas palavras e nos títulos, não em ornamento.
- **Cor:** papel quente como fundo, tinta escura no texto, um único destaque vermelho-tijolo para a ação principal e o foco. Verde só para "tudo bem" (conectado), vermelho de perigo só para erro e ação destrutiva, âmbar só para a faixa de reconexão. Tema claro; o escuro não faz parte do M6.
- **Tipografia:** títulos em fonte com serifa de caráter (display), texto em serifa de leitura. As fontes vêm empacotadas (`@fontsource`), nunca de um CDN. Texto de interface a partir de 17 px; legenda e apoio nunca menores que 14 px.
- **Forma:** cantos pouco arredondados, linhas finas em vez de sombra pesada; sombra só para o que flutua (cartão de entrada, balão de ajuda, menu).
- **Densidade:** confortável nas telas de entrada e de leitura; telas de tabela e canvas (M6, unidades seguintes) podem ser mais densas, mas mantêm alvos de toque de pelo menos 44 px e o mesmo conjunto de tokens. Se uma tela densa pedir outra fonte ou outra cor, a direção volta a este documento antes.
- **Arquivos do impeccable:** `PRODUCT.md` (fatos do produto) e `DESIGN.md` (derivado do que foi construído, gerado com `/impeccable document`) ficam na raiz para a ferramenta; a fonte da verdade é este documento e o arquivo de tokens. Se divergirem, vale este.

## Estrutura da tela

- **Entrada** (assistente, login, primeiro acesso): um cartão centrado sobre o papel, com a marca, o título, uma linha de apoio e o formulário; o seletor de idioma no canto.
- **Dentro do painel:** barra de cima, menu e área de conteúdo, com a [jornada de primeiro uso](primeira-jornada.md) como fio: o painel é desenhado para quem acabou de instalar e continua servindo a quem volta todo dia. A navegação mostra só o que o nível do usuário pode fazer; o servidor decide de qualquer forma ([nível em cada tela](login.md#nível-em-cada-tela)).
- **Menu de quatro itens**, nunca mais: **Início**, **Aprovações** (com o contador de pendentes, só para quem decide), **Campanhas** e **Configurações**. No computador é uma coluna à esquerda; no celular, quatro abas fixas embaixo, com alvo de 56 px. O item da seção atual fica marcado em toda tela dela, inclusive visão da campanha, mesa e terminal (`aria-current`).
- **Uma árvore só:** Campanhas › campanha › mesa (quadro, terminal e conversa). Toda tela com mais de um nível mostra **migalhas** acima do título: cada nível acima é um link, o último é o texto da página, e a campanha da migalha abre a troca de campanha. Telas de configuração mostram Configurações › grupo › tela. Nunca há tela sem caminho de volta.
- **Uma campanha atual por vez** (a da migalha; fora de uma campanha, a última aberta, [visão da campanha](visao-do-workspace.md#como-se-chega-e-a-campanha-atual)), o mesmo para a conversa, o botão **Falar com o Grão-Mestre** e os atalhos; a conversa diz sempre sobre qual campanha fala.
- **Barra de cima:** marca (leva ao Início), sino de avisos, botão **Grão-Mestre** e menu da conta, nessa ordem, numa linha só em qualquer largura (no celular o botão é só o ícone, com `aria-label`). O seletor de campanha e o contador de aprovações saem da barra: um vira migalha, o outro vira o número do item Aprovações. O menu da conta traz Minha conta (sessões e senha), idioma e Sair. O botão **Grão-Mestre** abre o painel lateral ([conversa](conversa-e-aprovacoes.md#conversa-com-o-grão-mestre)): uma coluna à direita no computador, tela inteira no celular; o texto do Grão-Mestre é Markdown sem HTML ativo (sem tag, sem imagem, só links `http`, `https` e `mailto`, que abrem em outra página). O número do item Aprovações vem de `/api/approvals/count` e de eventos `count` do assunto `approvals:<usuário>`; o sino é um balão (`DialogTrigger`) com a lista de avisos.
- **Início** é "o que está acontecendo agora" ([primeira jornada e Início](primeira-jornada.md#início-no-dia-a-dia)), não uma tela de boas-vindas.
- **Configurações** é um índice de quatro grupos, cada um com a frase do que a tela faz: **Conta** (sessões, senha, GitHub pessoal), **Pessoas e acesso** (usuários, segredos, auditoria), **Infraestrutura** (Guilda e máquinas, perfis de conta, Apps do GitHub, classes, bases e fichas) e **Regras e consumo** (decretos, delegações, cadeia de aprovação, teto da campanha, política de aprovação, instruções extras, consumo) ([administração](administracao.md#telas)). Os nomes do vocabulário ficam, com `Term` e a frase ao lado; as rotas antigas continuam valendo.
- Um cabeçalho de página por tela, `h1` único; um link "Ir para o conteúdo" para quem navega por teclado.

## Estados

- **Carregando:** texto curto no lugar do conteúdo, sem "spinner" sozinho; o que já se sabe continua na tela enquanto atualiza.
- **Vazio:** diz o que falta e o que fazer, com o termo do vocabulário e a ajuda dele ("Ainda não há nenhuma campanha…"). Nunca uma tela em branco, e nunca contra o que outra parte da mesma tela mostra: o vazio nasce do mesmo dado que a tela lê.
- **Erro:** em cima do campo ou do formulário, com o motivo e a saída; texto traduzido pelo `code` e os `params` do servidor ([login](login.md#implementação)). Um código sem texto cai na mensagem em português do servidor, nunca em "erro desconhecido".
- **Ocupado:** o botão que dispara fica desabilitado até a resposta; o PIN e as senhas são limpos depois de usados.
- **Sucesso:** confirmação discreta no próprio lugar ("Sessão encerrada."), sem janela.
- **Situação da mesa:** palavra, ícone de forma própria e tom (nunca só cor); o que muda por baixo avisa com um aviso na tela (`role="status"`, 9 s, fecha com ×), sem janela ([vida da mesa](vida-da-mesa.md)).

## Recusa e desfazer

- Quando o servidor recusa (nível, PIN, limite), a tela diz **quem recusou e por quê**, no idioma do usuário, e nada muda por baixo: a interface só reflete o estado depois da resposta, nunca antes.
- Ações que se desfazem oferecem "Desfazer" no lugar do aviso de sucesso; as que não se desfazem (encerrar sessão de outro aparelho, apagar) dizem isso antes de executar.
- **PIN:** o campo aparece **depois** do botão da ação sensível e vai na própria requisição; não há "lembrar PIN" ([PIN em ações sensíveis](login.md#pin-em-ações-sensíveis)). O componente é `SensitiveButton`.
- **Mostrado uma vez** (código de acesso, chave de recuperação): o componente `ShownOnce` mostra o valor grande, com "Copiar" e o aviso de que não volta; some quando a pessoa diz que anotou, e nada o guarda (nem o cache de consultas). Valores de segredos não têm tela: a lista mostra só nome, tipo, impressão, datas e metadados.

## Tempo real

O canal `/ws/ui` chega às telas pelo contexto `LiveContext` do `Shell` (`useLiveEvents(prefixo, fn)`): a tela segue os assuntos dela e ouve os eventos. O evento só diz que algo mudou (e, num pedido encerrado, quem respondeu): a tela busca de novo o que mostra e mantém na lista, sem botões, o item que acabou de ser respondido ("Já respondido por …").

## Reconexão

Quando o canal `/ws/ui` cai, uma faixa âmbar "Reconectando à Cidadela…" fica sob a barra de cima até voltar; as tentativas seguem um recuo exponencial (1 s, 2 s, 4 s… até 30 s). Ao reconectar, o painel **recarrega o estado** (nada que veio por evento é dado como certo) e assina de novo os assuntos. Se o servidor encerra a sessão, o painel vai ao login sem tentar de novo.

## Ajuda dos termos

- Todo termo do vocabulário usado na tela vem do componente `Term`, com um "?" ao lado: passar o mouse ou focar mostra, tocar ou apertar fixa. O balão traz a **ajuda curta** e, embaixo, **"No mundo real"**, a frase em palavras do dia a dia para quem não conhece RPG.
- Os textos vêm do [glossário](../produto/glossario.md) e do [glossário em inglês](../produto/glossario-en.md), embutidos no build (`web/src/gen/glossary.ts`); nenhum texto de ajuda é escrito no código do painel. Um termo sem linha no glossário não compila.
- O termo aparece com o nome do idioma da tela (Cidadela / Citadel). Em textos corridos o termo não vem sozinho na primeira vez que importa: usa-se `Term`.

## Terminal

- Tela escura com cores de terminal, a única que foge do papel (tokens `--color-term-*` e `--font-mono`, no `@theme`), no tamanho fixo do PTY (200 × 50), com rolagem; o navegador nunca redimensiona.
- Só leitura (cursor oculto, sem foco) é o padrão; o botão "Digitar" aparece só para quem pode e, num personagem ocupado, pede a confirmação "o agente está trabalhando" antes de liberar o teclado. A recusa do servidor diz quem recusou e por quê.
- Máquina sem sinal: o terminal fica cinza, com "máquina sem sinal desde HH:MM" em `role="status"`, e a tela continua visível. A tela também existe como texto para o leitor de tela.

## Idioma, datas e números

- Dois idiomas, pt-BR e en, com um catálogo por área e a **mesma lista de chaves** (verificada na suíte rápida e pelo compilador). A chave faltando é erro de `tsc`.
- O idioma é escolhido por usuário e guardado na Cidadela; sem escolha, vale o do navegador. O seletor fica no menu da conta e também nas telas de entrada (onde só vale neste navegador).
- **Fuso:** todo horário aparece no fuso da **Guilda** (o mesmo do Telegram, `guild.timezone`), nunca no do navegador, formatado com `Intl` no idioma do usuário. A tela diz o fuso em uso onde mostra horários.

## Acessibilidade e movimento

- Tudo se usa só com o teclado, com foco visível; componentes interativos vêm de `react-aria-components`; uma escolha simples numa lista (filtro de quem, de campanha) usa o `select` do navegador, que o teclado e o leitor de tela já tratam bem. Códigos e chaves usam a fonte monoespaçada do sistema.
- Contraste de texto de pelo menos 4,5:1 (3:1 para texto grande); a cor nunca é a única informação.
- Alvos de toque de pelo menos 44 px. Movimento curto e só para dar retorno; `prefers-reduced-motion` o desliga.
- Faixas e erros usam `role="alert"` ou `role="status"` para o leitor de tela.
