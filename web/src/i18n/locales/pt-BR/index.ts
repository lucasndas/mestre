import approvals from "./approvals";
import apps from "./apps";
import audit from "./audit";
import auth from "./auth";
import canvas from "./canvas";
import chat from "./chat";
import classes from "./classes";
import common from "./common";
import errors from "./errors";
import github from "./github";
import presets from "./presets";
import profiles from "./profiles";
import rules from "./rules";
import secrets from "./secrets";
import sessions from "./sessions";
import shell from "./shell";
import terminal from "./terminal";
import usage from "./usage";
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
  classes,
  apps,
  presets,
  profiles,
  usage,
} as const;
