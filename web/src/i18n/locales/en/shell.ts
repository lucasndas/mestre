import type { Widen } from "../../shape";
import type pt from "../pt-BR/shell";

const shell: Widen<typeof pt> = {
  nav: {
    main: "Main navigation",
    home: "Home",
    approvals: "Approvals",
    workspaces: "Campaigns",
    settings: "Settings",
    guildOk: "Guild: everything connected",
    guildDown_one: "Guild: {{count}} machine without signal",
    guildDown_other: "Guild: {{count}} machines without signal",
    zone: "Times in {{zone}}",
  },
  top: {
    home: "Mestre, go to Home",
    notices: "Notices",
    menu: "Account menu",
    account: "My account",
    logout: "Sign out",
    skip: "Skip to content",
  },
  crumbs: {
    label: "You are in",
    history: "History",
    terminal: "Terminal of {{name}}",
  },
  settings: {
    title: "Settings",
    intro: "Everything you can set in Mestre, by subject.",
    none: "Your level has no settings screens besides the ones of your account.",
    groups: {
      account: { name: "Account", blurb: "You and your access" },
      people: { name: "People and access", blurb: "Who gets in and what the vault holds" },
      infra: { name: "Infrastructure", blurb: "The machines and the accounts that do the work" },
      rules: { name: "Rules and usage", blurb: "What agents may do alone and how much they spend" },
    },
    entries: {
      sessions: { name: "My sessions", blurb: "The devices you are signed in on" },
      password: { name: "Change password", blurb: "The password of your account" },
      github: {
        name: "Connect GitHub",
        blurb: "Your personal GitHub login, to approve in your name",
      },
      users: { name: "Users", blurb: "Invite people and set what each can do" },
      secrets: { name: "Secrets", blurb: "Passwords and keys kept in the vault" },
      audit: { name: "Audit", blurb: "Who did what, and when" },
      guild: { name: "Guild", blurb: "The machines of the Guild and whether they are connected" },
      profiles: { name: "Account profiles", blurb: "The AI accounts of each machine" },
      apps: { name: "GitHub Apps", blurb: "The apps that act on GitHub for you" },
      classes: { name: "Classes", blurb: "The roles agents can take" },
      presets: {
        name: "Bases and sheets",
        blurb: "How each character starts: the base and the sheet",
      },
      decrees: { name: "Decrees", blurb: "Permissions given at once, for a while or for good" },
      delegations: { name: "Delegations", blurb: "Who may decide in whose place" },
      chain: {
        name: "Chain supervision",
        blurb: "The order of the people who approve each request",
      },
      ceiling: { name: "Campaign ceiling", blurb: "The most agents may do in each campaign" },
      policy: { name: "Approval policy", blurb: "When Mestre must ask for your decision" },
      instructions: { name: "Extra instructions", blurb: "Standing notes for the Grand Master" },
      usage: { name: "Usage", blurb: "How much the agents spent and how much is left" },
    },
  },
  reconnecting: "Reconnecting to the Citadel…",
  reconnected: "Connected again.",
  overview: {
    title: "Your Guild is ready",
    welcome: "Hello, {{name}}. Your level is {{level}}.",
    emptyTitle: "There is no campaign yet",
    emptyBody: "When you connect a GitHub repository, it shows up here.",
    times: "Times are in the {{zone}} time zone.",
  },
};
export default shell;
