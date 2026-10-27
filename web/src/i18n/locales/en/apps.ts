import type { Widen } from "../../shape";
import type pt from "../pt-BR/apps";

const apps: Widen<typeof pt> = {
  title: "GitHub Apps",
  intro:
    "Mestre's robots on GitHub: every agent action shows under the name of one of them, and you can cut the access whenever you want.",
  what: "the GitHub Apps",
  loading: "Loading the Apps…",
  strip: {
    none: "No GitHub App yet",
    noneHint: "Without the Apps, the agents cannot open PRs or comment on GitHub.",
    waiting: "{{count}} Apps waiting for a step from you",
    waitingOne: "1 App waiting for a step from you",
    waitingHint: "Each App says below what to do with it.",
    allReady: "All the Apps are ready",
    allReadyHint: "The agents can already open PRs under the name of each App.",
    action: "Generate the link of the App {{name}}",
  },
  empty: {
    title: "What a GitHub App is",
    why: "It is the identity Mestre acts with on GitHub. You create each App once, and Mestre keeps its key in the vault.",
    step1: "Generate the creation link of the App (the PIN confirms).",
    step2: "Open the link in the browser where you are logged in to GitHub and confirm.",
    step3: "Install the App on the repositories of your campaigns.",
  },
  count: "{{total}} of {{limit}} Apps on the account",
  countDetail:
    "{{mine}} from Mestre and {{others}} from others. GitHub allows up to {{limit}} per account.",
  meterTitle: "Use of the Apps limit",
  near: "Close to the GitHub limit of {{limit}} Apps: creating more may fail.",
  others: {
    summary: "Mestre cannot see your other Apps. Tell how many there are",
    label: "Other Apps on the account",
    hint: "Say how many Apps that are not Mestre's the account has, and update it when it changes.",
    save: "Save",
    saved: "Number of other Apps updated.",
  },
  list: {
    title: "Apps",
    caption: "Mestre Apps",
    fixed: "fixed",
    class: "of the class {{class}}",
    installed: "Installed on: {{repos}}",
    notInstalled: "Not installed on any campaign yet.",
  },
  next: {
    link: "Open the link in the browser where you are logged in to GitHub and confirm. Valid until {{when}}.",
    expired: "The link expired.",
    noLink: "No creation link yet.",
    failed: "The creation failed. Generate a new link to try again.",
    exchanging: "GitHub is creating the App. Wait a moment.",
    waitingOwner: "Only the Guildmaster generates the link of this App.",
  },
  requests: {
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
    new: "Generate a new link",
    make: "Generate the creation link",
    title: "Creation link of the App {{name}}",
    detail:
      "Open the link in the browser where you are logged in to GitHub with your personal account. Valid until {{when}}.",
    once: "This link shows only now. If you lose it, generate another.",
    done: "I copied it",
  },
  unavailable: "The creation of Apps is not available right now; the links come back when it is.",
  readOnly:
    "You can only read this screen: generating creation links and telling the other Apps of the account is for the Guildmaster.",
};
export default apps;
