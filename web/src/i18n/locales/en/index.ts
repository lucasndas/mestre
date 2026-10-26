import type { Widen } from "../../shape";
import type { ptBR } from "../pt-BR";
import approvals from "./approvals";
import audit from "./audit";
import auth from "./auth";
import common from "./common";
import errors from "./errors";
import github from "./github";
import secrets from "./secrets";
import sessions from "./sessions";
import shell from "./shell";
import terminal from "./terminal";
import users from "./users";

export const en: Widen<typeof ptBR> = {
  common,
  auth,
  shell,
  sessions,
  users,
  secrets,
  audit,
  approvals,
  github,
  errors,
  terminal,
};
