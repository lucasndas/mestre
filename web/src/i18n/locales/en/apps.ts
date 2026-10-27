import type { Widen } from "../../shape";
import type pt from "../pt-BR/apps";

const apps: Widen<typeof pt> = {
  title: "GitHub Apps",
  intro:
    "The fixed Apps and the ones of each class, the campaigns they are installed in, and how many Apps the account already has.",
  loading: "Loading the Apps…",
  count: "{{total}} of {{limit}} Apps on the account",
  countDetail: "{{mine}} from the Mestre and {{others}} from others.",
  near: "Close to the GitHub limit of {{limit}} Apps: creating more may fail.",
  others: {
    label: "Other Apps on the account",
    hint: "The Mestre does not read the GitHub Apps page: tell it how many Apps that are not its own the account has, and update it when it changes.",
    save: "Save",
    saved: "Number of other Apps updated.",
  },
  list: {
    title: "Apps",
    empty: "There are no Apps yet. They show up when a class or the Mestre needs one.",
    caption: "Apps of the Mestre",
    name: "App",
    role: "Role",
    class: "Class",
    state: "State",
    repos: "Installed in",
    noRepos: "no campaign",
    request: "Request",
    actions: "Action",
    fixed: "fixed",
  },
  requests: {
    title: "Creation requests",
    empty: "No creation request is open.",
    expires: "valid until {{when}}",
    expired: "expired",
    status: {
      link: "link issued",
      opened: "page opened",
      exchanging: "creating",
      expired: "expired",
      failed: "failed",
      unconfirmed: "unconfirmed",
    },
  },
  link: {
    new: "New link for {{name}}",
    make: "Make a link for {{name}}",
    title: "Creation link of the App {{name}}",
    detail:
      "Open the link in the browser where you are signed in to GitHub with your personal account. Valid until {{when}}.",
    once: "This link shows only now. If you lose it, make another.",
    done: "I copied it",
  },
  unavailable: "Creating Apps is not available in this period; the links come back when it is.",
  ownerOnly: "Only the Guildmaster makes creation links and tells the number of other Apps.",
};
export default apps;
