import type { Widen } from "../../shape";
import type pt from "../pt-BR/shell";

const shell: Widen<typeof pt> = {
  nav: {
    approvals: "Approvals",
    overview: "Overview",
    sessions: "My sessions",
    password: "Change password",
    users: "Users",
    secrets: "Secrets",
    audit: "Audit",
    decrees: "Decrees",
    delegations: "Delegations",
    chain: "Chain supervision",
    ceiling: "Workspace ceiling",
    policy: "Approval policy",
    instructions: "Extra instructions",
    github: "Connect GitHub",
    classes: "Classes",
    apps: "GitHub Apps",
    presets: "Bases and sheets",
    profiles: "Account profiles",
    usage: "Usage",
    groupAccount: "Account",
    groupGuild: "Guild",
    main: "Main navigation",
  },
  top: {
    workspace: "Workspace",
    approvals: "Approvals",
    notices: "Notices",
    menu: "Account menu",
    logout: "Sign out",
    skip: "Skip to content",
  },
  reconnecting: "Reconnecting to the Citadel…",
  reconnected: "Connected again.",
  overview: {
    title: "Your Guild is ready",
    welcome: "Hello, {{name}}. Your level is {{level}}.",
    emptyTitle: "There is no workspace yet",
    emptyBody: "When you connect a GitHub repository, it shows up here.",
    times: "Times are in the {{zone}} time zone.",
  },
};
export default shell;
