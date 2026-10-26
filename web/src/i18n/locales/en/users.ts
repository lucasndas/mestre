import type { Widen } from "../../shape";
import type pt from "../pt-BR/users";

const users: Widen<typeof pt> = {
  title: "Users",
  intro:
    "Who uses the Mestre, the level of each one and the workspaces they act in. Only the Owner changes things here, and every change asks for your PIN.",
  loading: "Loading the users…",
  levels: {
    owner: "Owner",
    administrator: "Administrator",
    approver: "Approver",
    observer: "Observer",
  },
  levelHints: {
    administrator: "Creates workspaces, decides approvals and edits the classes.",
    approver: "Decides approvals and connects their own GitHub.",
    observer: "Only follows along, decides nothing.",
  },
  states: { active: "Active", suspended: "Suspended" },
  you: "You",
  empty: "Only the Owner uses the Mestre for now. Register whoever will work with you.",
  noWorkspaces: "No workspaces",
  workspaceNone: "There are no workspaces yet: access to them can be given later.",
  telegramPaired: "Telegram paired",
  telegramNot: "Telegram not paired",
  loginNone: "has not signed in yet",
  loginIs: "login {{login}}",
  githubState: {
    connected: "GitHub connected as {{login}}, renewed until {{until}}",
    expired: "GitHub credential expired (account {{login}}): it must be connected again",
    none: "GitHub not connected yet (registered login: {{login}})",
    noLogin: "No GitHub login registered",
  },
  create: {
    open: "Register user",
    close: "Close",
    title: "New user",
    name: "Name",
    level: "Level",
    workspaces: "Workspaces they act in",
    githubLogin: "GitHub login",
    githubLoginHint: "Optional for now. The account that authorizes GitHub must be this one.",
    submit: "Register user",
    codeTitle: "First access code of {{name}}",
  },
  manage: "Manage",
  manageNamed: "Manage {{name}}",
  edit: {
    title: "Details",
    save: "Save changes",
    saved: "Changes saved.",
    levelChange:
      "Changing the level ends the sessions of {{name}} and voids what the new level does not allow.",
  },
  codes: {
    title: "Codes",
    first: "Create first access code",
    firstHint:
      "For someone who has not signed in to the panel yet. Lasts 30 minutes and is shown once.",
    reset: "Reset the password",
    resetHint:
      "Erases the current password, ends the sessions and creates a code to choose another.",
    resetCodeTitle: "Code for the new password of {{name}}",
    telegram: "Create Telegram code",
    telegramHint: "The person sends this code to the bot, in a private chat, to pair Telegram.",
    telegramCodeTitle: "Telegram code of {{name}}",
    expires: "Valid until {{when}}.",
    once: "Write it down now: this code is not shown again.",
    done: "I wrote it down",
    copy: "Copy",
    copied: "Copied.",
  },
  state: {
    title: "Access",
    suspend: "Suspend",
    suspendHint:
      "Takes effect at once: ends the sessions and voids what the person approved and has not run yet.",
    suspended: "User suspended.",
    reactivate: "Reactivate",
    reactivated: "User reactivated.",
    remove: "Remove",
    removeHint:
      "This cannot be undone: the name stays in the history, but access and the Telegram id are freed.",
    removed: "User removed.",
  },
  transfer: {
    title: "Owner post",
    start: "Pass the Owner post",
    startHint:
      "The person must accept with their PIN within 24 hours. When they do, you become an Administrator and both sessions end.",
    waiting: "Passing the post to {{name}} waits for acceptance until {{when}}.",
    cancel: "Cancel the transfer",
    started: "Request sent: {{name}} has 24 hours to accept.",
    cancelled: "Transfer cancelled.",
    needsPin: "Only someone who already has a PIN can receive the post.",
  },
  accept: {
    title: "Owner post",
    none: "No transfer of the post is waiting for you.",
    ask: "{{name}} wants to pass the Owner post to you. Valid until {{when}}.",
    effect:
      "When you accept, you become the Owner and {{name}} becomes an Administrator. Both sessions end and you must sign in again.",
    submit: "Accept the Owner post",
    banner: "{{name}} wants to pass the Owner post to you.",
    bannerLink: "See the request",
  },
};

export default users;
