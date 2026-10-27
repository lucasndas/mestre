import type { Widen } from "../../shape";
import type pt from "../pt-BR/github";

const github: Widen<typeof pt> = {
  title: "Connect GitHub",
  intro:
    "To act on GitHub as you (approve and decide on your behalf), the Mestre keeps a personal credential in the vault. The token goes straight from GitHub to the vault and never passes through this screen.",
  loading: "Loading…",
  loadWhat: "your GitHub connection",
  state: {
    none: "You have not connected your GitHub yet.",
    connected:
      "Connected as {{login}}. The credential renews itself; it lasts until {{until}} if it does not renew.",
    expired: "The credential of the account {{login}} expired. Connect again.",
  },
  registered: "GitHub login registered for you: {{login}}. Only that account can authorize.",
  noLogin: "No GitHub login was registered for you. Ask the Guildmaster to register it.",
  mayNot: "Your level or your campaigns do not allow a personal credential.",
  connect: "Connect GitHub",
  reconnect: "Connect again",
  steps: {
    title: "Authorize on GitHub",
    open: "Open GitHub",
    type: "Open {{uri}} and type the code below. Sign in with the account {{login}}.",
    code: "Code",
    valid: "Valid until {{when}}.",
    waiting: "Waiting for your authorization…",
    cancel: "Cancel",
  },
  done: "GitHub connected.",
};

export default github;
