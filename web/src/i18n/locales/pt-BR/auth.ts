export default {
  login: {
    title: "Entrar na Guilda",
    subtitle: "Use o nome de login e a senha que você criou.",
    login: "Nome de login",
    password: "Senha",
    submit: "Entrar",
    firstAccess: "Tenho um código de primeiro acesso",
    postChanged: "Suas sessões terminaram porque o posto de Senhor da Guilda mudou. Entre de novo.",
  },
  setup: {
    title: "Crie a sua Guilda",
    subtitle: "Você será o Senhor da Guilda. Use o código que apareceu no terminal da instalação.",
    code: "Código de configuração",
    name: "Seu nome",
    login: "Nome de login",
    password: "Senha (mínimo 12 caracteres)",
    password2: "Repita a senha",
    pin: "PIN (6 números)",
    pin2: "Repita o PIN",
    pinWhy: "O PIN confirma ações sensíveis, como mexer no cofre.",
    submit: "Criar a Guilda",
  },
  first: {
    title: "Primeiro acesso",
    subtitle:
      "Use o código de primeiro acesso que o Senhor da Guilda lhe entregou, depois de você ter feito o aparelho confiar no Mestre.",
    code: "Código de primeiro acesso",
    login: "Escolha o nome de login",
    password: "Senha (mínimo 12 caracteres)",
    password2: "Repita a senha",
    pin: "PIN (6 a 12 números)",
    pin2: "Repita o PIN",
    pinWhy:
      "O PIN confirma ações sensíveis. Ele não é a sua senha: não use o mesmo valor nos dois.",
    submit: "Criar meu acesso",
    errors: {
      invalid_code:
        "Esse código não vale mais: pode ter vencido (vale 24 horas) ou já ter sido usado. Peça outro ao Senhor da Guilda.",
      password_too_short:
        "A senha precisa de pelo menos 12 caracteres. Uma frase longa, fácil de lembrar, serve.",
      login_taken:
        "Já existe alguém com esse nome de login. Escolha outro, por exemplo com o seu sobrenome.",
      invalid_pin: "O PIN precisa ter de 6 a 12 números, sem letras.",
      pin_missing:
        "Falta o PIN. No primeiro acesso é preciso escolher um (6 a 12 números); só quem está trocando a senha esquecida o deixa em branco.",
      rate_limited:
        "Muitas tentativas seguidas. Espere {{minutes}} min e tente de novo; se o código não funciona mais, peça outro ao Senhor da Guilda.",
    },
    back: "Voltar ao login",
  },
  password: {
    title: "Trocar a senha",
    subtitle: "As suas outras sessões serão encerradas.",
    current: "Senha atual",
    next: "Nova senha (mínimo 12 caracteres)",
    next2: "Repita a nova senha",
    submit: "Trocar a senha",
    done: "Senha trocada. As outras sessões foram encerradas.",
  },
} as const;
