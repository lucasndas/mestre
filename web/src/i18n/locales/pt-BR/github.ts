export default {
  title: "Conectar GitHub",
  intro:
    "Para agir no GitHub como você (aprovar e decidir em seu nome), o Mestre guarda uma credencial pessoal no cofre. O token vai direto do GitHub para o cofre e nunca passa por esta tela.",
  loading: "Carregando…",
  state: {
    none: "Você ainda não conectou o seu GitHub.",
    connected:
      "Conectado como {{login}}. A credencial é renovada sozinha; vale até {{until}} se não renovar.",
    expired: "A credencial da conta {{login}} venceu. Conecte de novo.",
  },
  registered: "Login do GitHub cadastrado para você: {{login}}. Só essa conta pode autorizar.",
  noLogin:
    "Nenhum login do GitHub foi cadastrado para você. Peça ao Senhor da Guilda para cadastrá-lo.",
  mayNot: "O seu nível ou as suas campanhas não permitem uma credencial pessoal.",
  connect: "Conectar GitHub",
  reconnect: "Conectar de novo",
  steps: {
    title: "Autorize no GitHub",
    open: "Abrir o GitHub",
    type: "Abra {{uri}} e digite o código abaixo. Entre com a conta {{login}}.",
    code: "Código",
    valid: "Vale até {{when}}.",
    waiting: "Esperando a sua autorização…",
    cancel: "Cancelar",
  },
  done: "GitHub conectado.",
} as const;
