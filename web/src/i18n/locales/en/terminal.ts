import type { Widen } from "../../shape";
import type ptBR from "../pt-BR/terminal";

const terminal: Widen<typeof ptBR> = {
  title: "{{name}}'s terminal",
  region: "{{name}}'s terminal",
  machine: "Machine {{machine}}",
  state: "State: {{state}}",
  loading: "Opening the terminal…",
  noTerminal: "This character has no open terminal right now.",
  snapshotLabel: "Picture of {{name}}'s terminal, refreshed every 2 seconds",
  snapshotHint: "A picture of the screen. Move closer to watch it live.",
  noSignal: "Machine without signal since {{time}}.",
  noSignalNoTime: "Machine without signal.",
  lost: "lost stretch",
  type: "Type",
  stopTyping: "Stop typing",
  typingOn: "You are typing in this terminal. What you type reaches the agent.",
  typingNeedsLive: "To type, the terminal has to be live. Move closer to it.",
  readOnly: "Read only: your level does not allow typing here.",
  refused: "The Citadel refused what you typed: {{reason}}",
  busy: {
    title: "The agent is working",
    body: "{{name}} is in the middle of a task. What you type goes straight to the terminal and may disturb the work.",
    confirm: "Type anyway",
    cancel: "Cancel",
  },
};

export default terminal;
