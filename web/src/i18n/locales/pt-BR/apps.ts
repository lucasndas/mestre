export default {
  title: "Apps do GitHub",
  intro:
    "Os robôs do Mestre no GitHub: cada ação dos agentes aparece com o nome de um deles, e você pode cortar o acesso quando quiser.",
  what: "os Apps do GitHub",
  loading: "Carregando os Apps…",
  strip: {
    none: "Nenhum App criado ainda",
    noneHint: "Sem os dois Apps fixos o Mestre não age no GitHub.",
    waiting: "{{count}} Apps esperando um passo seu",
    waitingOne: "1 App esperando um passo seu",
    waitingHint: "Cada App diz abaixo o que fazer com ele.",
    allReady: "Todos os Apps estão prontos",
    allReadyHint: "Os agentes já podem abrir PRs com o nome de cada App.",
    action: "Gerar o link do App {{name}}",
  },
  empty: {
    title: "Os dois Apps fixos vêm primeiro",
    why: "Um App é o crachá de robô do Mestre no GitHub: toda ação dos agentes aparece com o nome dele, e você pode cortar o acesso a qualquer hora. Dois nascem no primeiro acesso: o Grão-Mestre e o Mestre. Os de classe (QA, Revisor…) vêm depois, quando a classe pede.",
    step1: "Gere o link de criação do App (o PIN confirma).",
    step2: "Abra o link no navegador em que você está logado no GitHub e confirme.",
    step3: "Instale o App nos repositórios das suas campanhas.",
  },
  count: "{{total}} de {{limit}} Apps na conta",
  countDetail:
    "{{mine}} do Mestre e {{others}} de outros. O GitHub aceita até {{limit}} por conta.",
  meterTitle: "Uso do limite de Apps",
  near: "Perto do limite de {{limit}} Apps do GitHub: criar mais pode falhar.",
  others: {
    summary: "O Mestre não vê os seus outros Apps. Informar quantos são",
    label: "Outros Apps da conta",
    hint: "Informe quantos Apps que não são do Mestre a conta tem e atualize quando mudar.",
    save: "Salvar",
    saved: "Número de outros Apps atualizado.",
  },
  list: {
    title: "Apps",
    caption: "Apps do Mestre",
    fixed: "fixo",
    class: "da classe {{class}}",
    installed: "Instalado em: {{repos}}",
    notInstalled: "Ainda não está instalado em nenhuma campanha.",
  },
  next: {
    link: "Abra o link no navegador em que você está logado no GitHub e confirme. Vale até {{when}}.",
    expired: "O link venceu.",
    noLink: "Ainda sem link de criação.",
    failed: "A criação falhou. Gere um novo link para tentar de novo.",
    exchanging: "O GitHub está criando o App. Aguarde um instante.",
    waitingOwner: "Só o Senhor da Guilda gera o link deste App.",
  },
  requests: {
    status: {
      link: "link emitido",
      opened: "página aberta",
      exchanging: "criando",
      expired: "vencido",
      failed: "falhou",
      unconfirmed: "sem confirmação",
    },
  },
  link: {
    new: "Gerar novo link",
    make: "Gerar o link de criação",
    title: "Link de criação do App {{name}}",
    detail:
      "Abra o link no navegador em que você está logado no GitHub com a sua conta pessoal. Vale até {{when}}.",
    once: "Este link aparece só agora. Se perder, gere outro.",
    done: "Já copiei",
  },
  unavailable:
    "A criação de Apps não está disponível neste período; os links voltam quando estiver.",
  readOnly:
    "Você só lê esta tela: gerar links de criação e informar os outros Apps da conta é do Senhor da Guilda.",
} as const;
