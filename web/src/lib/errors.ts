import type { TFunction } from "i18next";
import { ptBR } from "../i18n/locales/pt-BR";
import { ApiError } from "./api";

type ErrorKey = keyof typeof ptBR.errors;

export function isErrorKey(code: string): code is ErrorKey {
  return code in ptBR.errors;
}

/** The text of an error in the language of the user, from the code and params of the server. */
export function errorText(t: TFunction<"errors">, err: unknown): string {
  if (err instanceof ApiError) {
    if (isErrorKey(err.code)) {
      const params: Record<string, unknown> = { ...err.params };
      const secs = params.retry_after_seconds;
      if (typeof secs === "number") params.minutes = Math.max(1, Math.ceil(secs / 60));
      // the key was checked above; the params depend on the code, which the types cannot follow
      return (t as unknown as (k: string, o: object) => string)(err.code, params);
    }
    return err.message || t("internal");
  }
  return t("internal");
}
