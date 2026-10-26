import auth from "./auth";
import common from "./common";
import errors from "./errors";
import sessions from "./sessions";
import shell from "./shell";

export const ptBR = { common, auth, shell, sessions, errors } as const;
