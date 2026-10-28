import type { Widen } from "../../shape";
import type pt from "../pt-BR/classes";

const classes: Widen<typeof pt> = {
  title: "Classes",
  intro:
    "The roles of the agents (Dev, QA, Reviewer): what each one may do and which GitHub App it uses.",
  what: "the classes",
  why: "The engine did not read the library of classes. Nothing was changed.",
  loading: "Loading the classes…",
  strip: {
    proposals: "{{count}} proposals waiting for your approval",
    proposalsOne: "1 proposal waiting for your approval",
    proposalsHint: "A class only takes effect after you approve its proposal.",
    inForce: "{{count}} classes in force",
    inForceOne: "1 class in force",
    inForceApp: "{{count}} without a ready App: without it the class cannot act on GitHub.",
    inForceHint: "The agents can already take these roles.",
    none: "No class yet",
    noneHint: "With no class, the Grand Master has no role to give the agents.",
    talk: "Talk to the Grand Master",
  },
  empty: {
    title: "What a class is and how it is born",
    why: "A class is the role of an agent on the team: its instructions and permissions, and the GitHub App it acts with.",
    step1: "You ask the Grand Master for the role you want (for example, a reviewer).",
    step2:
      "The Grand Master writes the proposal and it shows here for you to approve, with the PIN.",
    step3: "When you approve, Mestre prepares the GitHub App of the class.",
  },
  band: "{{total}} of {{limit}} Apps on the account. Close to the GitHub limit, creating more Apps may fail.",
  bandLink: "See the Apps",
  proposals: {
    title: "Proposals waiting for approval",
    origin: {
      request: "From the Grand Master",
      disk: "Changed outside Mestre",
    },
    by: "by {{name}}",
    untested: "not tested",
    problems: "It cannot be approved yet. Problems found:",
    howToFix: "Ask the Grand Master to fix it, or check the ceiling of the campaign.",
    approve: "Approve {{name}}",
    approved: "Class {{name}} approved.",
    global: "the whole Guild",
  },
  list: {
    title: "Classes in force",
    everywhere: "all campaigns",
    version: "version {{version}}",
    noApp: "no App",
    appNotReady: "The App of this class is not ready yet. Without it, the class cannot open PRs.",
    appReady: "App {{name}} ready",
    appPending: "App being created",
    appState: { "em criação": "App being created", "com erro": "App with an error" },
    appliesTo: "Applies to: {{where}}",
    appLink: "Generate the link in GitHub Apps",
  },
  groups: {
    global: "Valid in the whole Guild",
    workspace: "Of the campaign {{where}}",
    table: "Of the tables",
  },
  scopes: { global: "Global", workspace: "Campaign", table: "Table" },
  states: {
    approved: "Approved",
    pending: "Change pending",
    new: "New",
    missing: "File missing",
    unreadable: "Unreadable",
  },
};
export default classes;
