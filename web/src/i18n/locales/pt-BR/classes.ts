export default {
  title: "Classes",
  intro:
    "As classes do Mestre: as globais, as do workspace e as das mesas, com o App de cada uma, e as propostas que esperam a sua aprovação.",
  loading: "Carregando as classes…",
  band: "{{total}} de {{limit}} Apps na conta. Perto do limite do GitHub, criar mais Apps pode falhar.",
  bandLink: "Ver os Apps",
  proposals: {
    title: "Propostas esperando aprovação",
    empty: "Nenhuma proposta espera por você.",
    origin: {
      request: "Proposta pelo Grão-Mestre",
      disk: "Alterada fora do Mestre",
    },
    by: "por {{name}}",
    untested: "não testada",
    problems: "Não dá para aprovar enquanto houver problemas:",
    approve: "Aprovar {{name}}",
    approved: "Classe {{name}} aprovada.",
    global: "global",
  },
  list: {
    title: "Classes em vigor",
    empty:
      "Ainda não há nenhuma classe. Quando o Grão-Mestre propuser uma e você aprovar, ela aparece aqui.",
    caption: "Classes por escopo",
    name: "Classe",
    scope: "Escopo",
    where: "Onde",
    state: "Estado",
    app: "App",
    version: "Versão",
    noApp: "sem App",
    everywhere: "todos os workspaces",
  },
  scopes: { global: "Global", workspace: "Workspace", table: "Mesa" },
  states: {
    approved: "Aprovada",
    pending: "Mudança pendente",
    new: "Nova",
    missing: "Arquivo ausente",
    unreadable: "Ilegível",
  },
} as const;
