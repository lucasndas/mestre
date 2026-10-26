import type { TFunction } from "i18next";
import { ApiError } from "../../lib/api";
import { errorText } from "../../lib/errors";

type Say = (k: string, o?: object) => string;

/**
 * The text of an error of the workspaces: the reason the engine gave, in the
 * words of the screen (workspaces.refusals.<reason>), else the message the engine
 * sent, else the generic text of the code.
 */
export function refusalText(
  t: TFunction<"workspaces">,
  te: TFunction<"errors">,
  err: unknown,
): string {
  if (err instanceof ApiError && err.code === "refused") {
    const reason = String(err.params?.reason ?? "");
    const say = t as unknown as Say;
    const own = reason ? say(`refusals.${reason}`, { ...err.params, defaultValue: "" }) : "";
    if (own) return own;
    if (err.message) return err.message;
  }
  return errorText(te, err);
}
