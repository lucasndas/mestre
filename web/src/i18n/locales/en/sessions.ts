import type { Widen } from "../../shape";
import type pt from "../pt-BR/sessions";

const sessions: Widen<typeof pt> = {
  title: "My sessions",
  titleAll: "Everyone's sessions",
  intro: "Each browser you signed in on is a session. End the ones you do not recognise.",
  introAll: "As Owner, you see and end the sessions of every user.",
  current: "This session",
  device: "Device",
  user: "User",
  ip: "Address",
  created: "Signed in",
  lastUsed: "Last used",
  end: "End",
  endNamed: "End the session on {{device}}",
  ended: "Session ended.",
  empty: "No sessions.",
  mine: "Mine",
  everyone: "Everyone's",
};
export default sessions;
