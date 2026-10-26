import approvals from "./approvals";
import audit from "./audit";
import auth from "./auth";
import canvas from "./canvas";
import chat from "./chat";
import common from "./common";
import errors from "./errors";
import github from "./github";
import rules from "./rules";
import secrets from "./secrets";
import sessions from "./sessions";
import shell from "./shell";
import terminal from "./terminal";
import users from "./users";

export const ptBR = {
  common,
  auth,
  shell,
  sessions,
  users,
  secrets,
  audit,
  approvals,
  chat,
  github,
  rules,
  errors,
  terminal,
  canvas,
} as const;
