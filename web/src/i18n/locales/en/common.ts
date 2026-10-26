import type { Widen } from "../../shape";
import type pt from "../pt-BR/common";

const common: Widen<typeof pt> = {
  appName: "Mestre",
  loading: "Loading…",
  language: { label: "Language", pt: "Português", en: "English" },
  help: { open: "Help about {{term}}", real: "In the real world" },
  pin: { label: "PIN", hint: "Type your PIN to confirm." },
  actions: { back: "Back", cancel: "Cancel" },
};
export default common;
