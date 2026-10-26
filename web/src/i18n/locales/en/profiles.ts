import type { Widen } from "../../shape";
import type pt from "../pt-BR/profiles";

const profiles: Widen<typeof pt> = {
  title: "Account profiles",
  intro:
    "The accounts the agents use on this machine. The guided setup takes each profile from installing the program to the final check.",
  loading: "Loading the profiles…",
  machine: "Machine: {{machine}}",
  empty: "There is no account profile on this machine yet. Create the first one below.",
  noSetup:
    "The guided setup is not available on this machine yet. Profiles can be created and removed, but the login is done by hand.",
  default: "default",
  steps: {
    title: "Setup steps",
    install: "Install the provider's program",
    login: "Sign in to the provider account",
    hooks: "Approve the Mestre hooks",
    conformance: "Check that it works",
    done: "Done",
    stateDone: "done",
    stateCurrent: "now",
    statePending: "later",
  },
  waiting: "Waiting for: {{what}}",
  advance: "Continue the setup of {{name}}",
  advanced: "Done: the profile {{name}} is set up.",
  terminal: "Open the terminal to continue",
  notConformant: "It has not passed the check yet: {{reason}}",
  makeDefault: "Make {{name}} the default",
  defaultDone: "The default is now {{name}}.",
  remove: "Remove {{name}}",
  removed: "Profile {{name}} removed. The folder with the login stays on the machine.",
  add: {
    title: "New profile",
    name: "Profile name",
    provider: "Provider",
    submit: "Create profile",
    done: "Profile {{name}} created.",
  },
};
export default profiles;
