import type { TFunction } from "i18next";
import { ptBR } from "../i18n/locales/pt-BR";
import { ApiError } from "./api";
import { isMessageCode, messageText } from "./messages";

type ErrorKey = keyof typeof ptBR.errors;

export function isErrorKey(code: string): code is ErrorKey {
  return code in ptBR.errors;
}

/** A problem the form found before asking the server, in the same shape as the server's. */
export function localError(code: ErrorKey): ApiError {
  return new ApiError(0, code, "");
}

/** The language a translation function says its texts in (the `locale` of the errors catalog). */
function languageOf(t: TFunction<"errors">): string {
  return t("locale");
}

/** The name of a level, from the catalogs of the engine ("owner" → "Senhor da Guilda"). */
export function levelName(language: string | undefined, level: unknown): string {
  return typeof level === "string" ? (messageText(language, `level.${level}`) ?? "") : "";
}

/**
 * The text of an error in the language of the user, from the code and params of the server.
 * The code of a refusal of the engine ("decree.scope_required") is said from the catalog the
 * engine generates (gen/messages.ts); the code of the API ("forbidden_level") from the catalog
 * of the errors of the panel.
 */
export function errorText(t: TFunction<"errors">, err: unknown, zone?: string): string {
  if (err instanceof ApiError) {
    if (isMessageCode(err.code)) {
      return messageText(languageOf(t), err.code, err.params, zone) ?? t("internal");
    }
    if (isErrorKey(err.code)) {
      const params: Record<string, unknown> = { ...err.params };
      const secs = params.retry_after_seconds;
      if (typeof secs === "number") params.minutes = Math.max(1, Math.ceil(secs / 60));
      // some texts carry the detail the server gave (the line of a YAML, who uses a base)
      if (params.message === undefined) params.message = err.message;
      // the key was checked above; the params depend on the code, which the types cannot follow
      const say = t as unknown as (k: string, o?: object) => string;
      // a refusal of level names the level that is required (CA-ADM-01), in the language of the panel
      if (err.code === "forbidden_level" && typeof params.need === "string") {
        const lng = languageOf(t);
        return say("forbidden_level_need", {
          need: levelName(lng, params.need),
          have: levelName(lng, params.have),
        });
      }
      return say(err.code, params);
    }
    return err.message || t("internal");
  }
  return t("internal");
}
