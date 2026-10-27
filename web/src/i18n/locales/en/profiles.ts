import type { Widen } from "../../shape";
import type pt from "../pt-BR/profiles";

const profiles: Widen<typeof pt> = {
  title: "Account profiles",
  intro:
    "The AI accounts (Claude, Codex, Antigravity) the agents use to work on this machine. With no account signed in, no agent runs.",
  what: "the account profiles",
  loading: "Loading the profiles…",
  machineNote:
    "This list is the one of machine {{machine}}, where the panel runs. The accounts of the other machines show on the page of each one, in the Guild; here they are coming soon.",
  strip: {
    none: "No AI account has signed in yet",
    noneHint: "Pick a provider below to sign in to the first account.",
    ready: "{{ready}} of {{total}} accounts ready",
    readyHint: "Each account is a profile. A ready account has passed the check.",
    manual: "{{total}} accounts registered",
    manualHint: "The guided setup is not available yet: the login is done by hand on each account.",
    action: "Sign in to another account",
  },
  empty: {
    title: "Pick the provider of the first account",
    why: "A profile keeps the login of a provider on this machine. Each agent uses a profile to work.",
    pick: "Sign in with {{provider}}",
  },
  providers: { "claude-code": "Claude", codex: "Codex", antigravity: "Antigravity" },
  default: "default",
  badge: { ready: "ready", missing: "a step is missing", unchecked: "login not checked" },
  manual: {
    title: "Login done by hand",
    body: "Open the terminal of machine {{machine}} and sign in to the provider account ({{provider}}).",
    codex: "In Codex, also approve the Mestre hooks in /hooks.",
  },
  steps: {
    title: "Setup steps",
    install: "Install the provider program",
    login: "Sign in to the provider account",
    hooks: "Approve the Mestre hooks",
    conformance: "Check that it works",
    done: "Ready",
    stateDone: "done",
    stateCurrent: "now",
    statePending: "later",
  },
  waiting: "Waiting for: {{what}}",
  advance: "Continue the setup of {{name}}",
  advanced: "Ready: the profile {{name}} is set up.",
  terminal: "Open the terminal to continue",
  notConformant: "It has not passed the check yet: {{reason}}",
  makeDefault: "Make {{name}} the default",
  defaultDone: "The default is now {{name}}.",
  removeTitle: "Remove this profile",
  removeNote: "The folder with the login stays on the machine; only the profile leaves Mestre.",
  remove: "Remove {{name}}",
  removed: "Profile {{name}} removed. The folder with the login stays on the machine.",
  add: {
    title: "Sign in to another account",
    hint: "Choose the profile name and the provider; the PIN confirms.",
    name: "Profile name",
    provider: "Provider",
    submit: "Create profile",
    done: "Profile {{name}} created.",
    close: "Close",
  },
};
export default profiles;
