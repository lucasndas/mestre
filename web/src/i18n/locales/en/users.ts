import type { Widen } from "../../shape";
import type pt from "../pt-BR/users";

const users: Widen<typeof pt> = {
  title: "Users",
  intro:
    "Who uses the Mestre, the level of each one and the campaigns they act in. Only the Guildmaster changes things here, and every change asks for your PIN.",
  loading: "Loading the users…",
  loadWhat: "the users",
  levels: {
    owner: "Guildmaster",
    administrator: "Administrator",
    approver: "Approver",
    observer: "Observer",
  },
  levelHints: {
    administrator: "Creates campaigns, decides approvals and edits the classes.",
    approver: "Decides approvals and connects their own GitHub.",
    observer: "Only follows along, decides nothing.",
  },
  levelCan: {
    administrator: "Will be able to create campaigns, decide approvals and edit the classes.",
    approver:
      "Will be able to talk to the Grand Master, decide approvals and connect their own GitHub.",
    observer: "Will be able to follow the campaigns, the tables and the canvas.",
  },
  levelCannot: {
    administrator: "Will not be able to touch secrets, users or the Guild: those stay with you.",
    approver:
      "Will not be able to create campaigns or touch classes, secrets, users and the Guild.",
    observer: "Will not be able to talk to the Grand Master or decide approvals.",
  },
  states: { active: "Active", suspended: "Suspended" },
  you: "You",
  notEntered: "Has not joined yet",
  strip: "{{count}} people in the Guild",
  stripWaiting: "{{count}} has not joined yet, waiting for the first-access code",
  stripHint: "Invite more people, or open Manage to change someone's level and access.",
  stripAlone: "Only you are in the Guild",
  stripAloneHint:
    "Invite whoever will work with you. The person receives the invite and chooses their own login and password.",
  emptyTitle: "Nobody else uses the Mestre yet",
  empty:
    'Inviting takes three steps, and you follow the last one in this list: the person shows as "Has not joined yet" until their first access.',
  emptyStep1: "Say the name and choose what the person will be able to do.",
  emptyStep2: "Hand over the invite, in three parts, through different channels.",
  emptyStep3: "When they join, the name shows here with the login.",
  emptyStep4: "Optional: the person pairs Telegram, to get the notices there.",
  levelsTitle: "What each level can do",
  noWorkspaces: "No campaigns",
  workspaceNone: "There are no campaigns yet: access to them can be given later.",
  telegramPaired: "Telegram paired",
  telegramNot: "Telegram not paired",
  loginNone: "has not joined yet",
  loginIs: "login {{login}}",
  githubState: {
    connected: "GitHub connected as {{login}}, renewed until {{until}}",
    expired: "GitHub credential expired (account {{login}}): it needs to connect again",
    none: "GitHub not connected yet (registered login: {{login}})",
    noLogin: "No GitHub login registered",
  },
  create: {
    previewLabel: "Invite summary",
    preview: "{{name}} as {{level}}",
    previewAnyone: "the person",
    pinNote: "The PIN is only the person's. You never see it.",
    open: "Invite a person",
    close: "Close",
    title: "Invite a person",
    name: "Name",
    level: "What the person will be able to do (level)",
    workspaces: "Campaigns they act in",
    more: "More options",
    githubLogin: "GitHub login",
    githubLoginHint: "Optional for now. The account that authorizes GitHub has to be this one.",
    submit: "Create the invite",
    codeTitle: "First-access code for {{name}}",
  },
  invite: {
    title: "Invite for {{name}}",
    lead: "Hand it over in three parts, each through its own channel. The first two are not secret; the third is, and goes only after the person confirms the certificate step.",
    checking: "Computing the certificate check code…",
    checkFailed:
      "I could not compute the certificate check code, so I am not handing out the invite. The first-access code stays only on this screen; try again.",
    retry: "Try again",
    part1: "1. Instructions",
    part1Hint:
      "They can go through any channel: they carry no code. They carry the rule to not install if the check code differs.",
    part2: "2. Certificate check code",
    part2Hint:
      "Say it aloud, in person or on a call, or send it through a different channel from the instructions. If the address and this code traveled together, whoever altered the message could lead the person to a fake panel. This value was computed just now from {{path}}. Check that it equals what the Citadel installer showed; if not, do not hand out the invite.",
    part3: "3. First-access code",
    part3Gate: "The person told me they checked the code and did the certificate step",
    part3Locked: "Tick above when the person confirms. The code only shows after that.",
    part3Hint:
      "Single use, valid for 24 hours, shown once and never sent by Telegram or in a link. Send it through a channel where you recognize the person.",
    copy: "Copy",
    copied: "Copied.",
    codeDone: "I sent it and wrote it down",
    instructions:
      'Hello, {{name}}! You were invited to a Mestre Guild.\n\nPanel address: {{address}}\n\nBefore signing in, make your device trust the Mestre:\n1. Download the certificate at {{address}}{{path}}.\n2. Check the file\'s check code against the one I will give you through another channel (spoken or in a separate message). If it differs, do NOT install it and tell me.\n3. Install the certificate and reopen the browser.\n4. Open the panel address, choose "I have a first-access code" and use the code I will send you later.',
    technical:
      'The certificate is the Mestre CA\'s and the check code is its SHA-256. On Linux and macOS run `sha256sum mestre-ca.crt` (on macOS, `shasum -a 256 mestre-ca.crt`); on Windows, `certutil -hashfile mestre-ca.crt SHA256`. The result must equal the code. Do not confuse it with the "Signature" field or the "Thumbprint" (SHA-1) that Windows shows.',
    technicalTitle: "Technical details",
  },
  manage: "Manage",
  manageNamed: "Manage {{name}}",
  edit: {
    title: "Details",
    save: "Save changes",
    saved: "Changes saved.",
    levelChange:
      "Changing the level ends {{name}}'s sessions and invalidates what the new level does not allow.",
  },
  codes: {
    title: "Codes",
    first: "Generate another first-access code",
    firstHint:
      "For someone who has not joined the panel yet. Valid for 24 hours, shown once, and the previous code stops working.",
    reset: "Reset the password",
    resetHint:
      "Erases the current password, ends the sessions and generates a code to choose another.",
    resetCodeTitle: "Code for {{name}}'s new password",
    telegram: "Generate the Telegram code",
    telegramHint: "The person sends this code to the bot, in a private chat, to pair Telegram.",
    telegramCodeTitle: "Telegram code for {{name}}",
    expires: "Valid until {{when}}.",
    once: "Write it down now: this code does not show again.",
    done: "I wrote it down",
    copy: "Copy",
    copied: "Copied.",
  },
  state: {
    title: "Access",
    suspend: "Suspend",
    suspendHint:
      "Immediate effect: ends the sessions and invalidates what the person approved and has not run yet.",
    suspended: "User suspended.",
    reactivate: "Reactivate",
    reactivated: "User reactivated.",
    remove: "Remove",
    removeHint:
      "Cannot be undone: the name stays in the history, but access and Telegram are released.",
    removed: "User removed.",
  },
  transfer: {
    title: "Guildmaster post",
    start: "Pass the Guildmaster post",
    startHint:
      "The person must accept with their PIN within 24 hours. On accepting, you become Administrator and both sessions end.",
    waiting: "Passing the post to {{name}} awaits acceptance until {{when}}.",
    cancel: "Cancel the request",
    started: "Request sent: {{name}} has 24 hours to accept.",
    cancelled: "Request cancelled.",
    needsPin: "Only someone who already has a PIN can receive the post, and {{name}} does not yet.",
  },
  accept: {
    title: "Guildmaster post",
    none: "No transfer of the post is waiting for you.",
    ask: "{{name}} wants to pass the Guildmaster post to you. Valid until {{when}}.",
    effectTitle: "What changes if you accept",
    effectYou: "You become the Guildmaster: you get to see the secrets, the users and the Guild.",
    effectHim: "{{name}} becomes Administrator.",
    effectSessions: "Both sessions end, and each of you needs to sign in again.",
    ignore: "If you do not want it, ignore it: the request expires by itself.",
    submit: "Accept the Guildmaster post",
    banner: "{{name}} wants to pass the Guildmaster post to you.",
    bannerLink: "See the request",
  },
  welcome: {
    title: "You joined as {{level}}",
    can: "What you can do",
    cannot: "What stays with the Guildmaster",
    workspaces: "Campaigns you have access to: {{list}}.",
    noWorkspaces: "You have no access to any campaign yet: ask the Guildmaster.",
    telegram: "To get notices and answer from your phone, pair your Telegram.",
    done: "Got it",
    telegramLink: "Open Settings",
    invite: "Invite someone to the Guild",
    inviteHint: "You are the only person in the Guild. Invite whoever will work with you.",
    can_observer: "Follow campaigns, tables and the canvas.",
    can_approver: "Talk to the Grand Master, decide approvals and connect your GitHub.",
    can_administrator:
      "Create campaigns, talk to the Grand Master, decide approvals and edit the classes.",
    cannot_observer:
      "Talk to the Grand Master, decide approvals and anything that changes something.",
    cannot_approver: "Create campaigns, touch classes, secrets, users and the Guild.",
    cannot_administrator: "Secrets, users, the Guild and the Guildmaster post.",
  },
};

export default users;
