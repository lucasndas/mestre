export default {
  appName: "Mestre",
  loading: "Carregando…",
  language: { label: "Idioma", pt: "Português", en: "English" },
  help: { open: "Ajuda sobre {{term}}", real: "No mundo real" },
  pin: { label: "PIN", hint: "Digite o seu PIN para confirmar." },
  actions: { back: "Voltar", cancel: "Cancelar" },
  // the pieces every administration screen shares (docs/painel/administracao-telas.md)
  admin: {
    retry: "Tentar de novo",
    technical: "Detalhes técnicos",
    vocabulary: "Vocabulário",
    loadFailed: "Não foi possível carregar {{what}}.",
    nothingChanged: "Nada foi alterado.",
    refused: "Esta tela é de outro nível.",
    needLevel: "Exige o nível {{need}}; o seu é {{have}}.",
    tone: { neutral: "Situação", warn: "Falta algo", ok: "Em dia", danger: "Falhou" },
  },
} as const;
