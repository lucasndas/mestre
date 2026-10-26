import type { Widen } from "../../shape";
import type pt from "../pt-BR/classes";

const classes: Widen<typeof pt> = {
  title: "Classes",
  intro:
    "The classes of the Mestre: global, workspace and table ones, with the App of each, and the proposals waiting for your approval.",
  loading: "Loading the classes…",
  band: "{{total}} of {{limit}} Apps on the account. Close to the GitHub limit, creating more Apps may fail.",
  bandLink: "See the Apps",
  proposals: {
    title: "Proposals waiting for approval",
    empty: "No proposal is waiting for you.",
    origin: {
      request: "Proposed by the Grand Master",
      disk: "Changed outside the Mestre",
    },
    by: "by {{name}}",
    untested: "not tested",
    problems: "It cannot be approved while there are problems:",
    approve: "Approve {{name}}",
    approved: "Class {{name}} approved.",
    global: "global",
  },
  list: {
    title: "Classes in force",
    empty:
      "There are no classes yet. When the Grand Master proposes one and you approve it, it shows up here.",
    caption: "Classes by scope",
    name: "Class",
    scope: "Scope",
    where: "Where",
    state: "State",
    app: "App",
    version: "Version",
    noApp: "no App",
    everywhere: "all workspaces",
  },
  scopes: { global: "Global", workspace: "Workspace", table: "Table" },
  states: {
    approved: "Approved",
    pending: "Change pending",
    new: "New",
    missing: "File missing",
    unreadable: "Unreadable",
  },
};
export default classes;
