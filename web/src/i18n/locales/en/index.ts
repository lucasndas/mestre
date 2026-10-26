import type { Widen } from "../../shape";
import type { ptBR } from "../pt-BR";
import auth from "./auth";
import common from "./common";
import errors from "./errors";
import sessions from "./sessions";
import shell from "./shell";

export const en: Widen<typeof ptBR> = { common, auth, shell, sessions, errors };
