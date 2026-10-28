import type { Widen } from "../../shape";
import type pt from "../pt-BR/apps";

const apps: Widen<typeof pt> = {
  title: "GitHub Apps",
  intro:
    "Mestre's robots on GitHub: every agent action shows under the name of one of them, and you can cut the access whenever you want.",
  what: "the GitHub Apps",
  loading: "Loading the Apps…",
  strip: {
    none: "No App created yet",
    noneHint: "Without the two fixed Apps Mestre cannot act on GitHub.",
    waiting: "{{count}} Apps waiting for a step from you",
    waitingOne: "1 App waiting for a step from you",
    waitingHint: "{{mine}} of Mestre, {{total}} of {{limit}} on the account.",
    allReady: "All the Apps are ready",
    allReadyHint: "The agents can already open PRs under the name of each App.",
    action: "Generate the link of the App {{name}}",
  },
  empty: {
    title: "The two fixed Apps come first",
    why: "An App is Mestre's robot badge on GitHub: every agent action shows under its name, and you can cut the access any time. Two are born at the first access: the Grand Master and the Mestre. The class Apps (QA, Reviewer…) come later, when a class asks for one.",
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
    title: "Mestre Apps",
    caption: "Mestre Apps",
    fixed: "fixed",
    class: "Class {{class}}",
    desc: {
      grand_master: "Grand Master App: talks to you and approves on GitHub.",
      mestre: "Mestre App: opens the PRs of the agents.",
    },
    descClass: "Gives the class {{class}} an identity of its own on the PRs.",
    expiredChip: "link expired",
    installedLabel: "Installed on",
    notInstalledShort: "no campaign yet",
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
