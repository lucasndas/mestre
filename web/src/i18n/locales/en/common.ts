import type { Widen } from "../../shape";
import type pt from "../pt-BR/common";

const common: Widen<typeof pt> = {
  appName: "Mestre",
  loading: "Loading…",
  language: { label: "Language", pt: "Português", en: "English" },
  help: { open: "Help about {{term}}", real: "In the real world" },
  pin: { label: "PIN", hint: "Type your PIN to confirm." },
  actions: { back: "Back", cancel: "Cancel" },
  admin: {
    retry: "Try again",
    technical: "Technical details",
    vocabulary: "Vocabulary",
    loadFailed: "Could not load {{what}}.",
    refused: "This screen belongs to another level.",
    needLevel: "Needs the level {{need}}; yours is {{have}}.",
    tone: { neutral: "Status", warn: "Something is missing", ok: "Up to date", danger: "Failed" },
  },
};
export default common;
