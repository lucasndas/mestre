import type { TFunction } from "i18next";
import { ptBR } from "../i18n/locales/pt-BR";
import { ApiError } from "./api";

type ErrorKey = keyof typeof ptBR.errors;

export function isErrorKey(code: string): code is ErrorKey {
  return code in ptBR.errors;
}

/** A problem the form found before asking the server, in the same shape as the server's. */
export function localError(code: ErrorKey): ApiError {
  return new ApiError(0, code, "");
}

/** The text of an error in the language of the user, from the code and params of the server. */
export function errorText(t: TFunction<"errors">, err: unknown): string {
  if (err instanceof ApiError) {
    if (isErrorKey(err.code)) {
      const params: Record<string, unknown> = { ...err.params };
      const secs = params.retry_after_seconds;
      if (typeof secs === "number") params.minutes = Math.max(1, Math.ceil(secs / 60));
      // the key was checked above; the params depend on the code, which the types cannot follow
      const say = t as unknown as (k: string, o?: object) => string;
      // a refusal of level names the level that is required (CA-ADM-01), in the language of the panel
      if (err.code === "forbidden_level" && typeof params.need === "string") {
        const level = (l: unknown) =>
          typeof l === "string" && isErrorKey(`level_${l}`) ? say(`level_${l}`) : "";
        return say("forbidden_level_need", { need: level(params.need), have: level(params.have) });
      }
      return say(err.code, params);
    }
    return err.message || t("internal");
  }
  return t("internal");
}
