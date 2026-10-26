import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import type { Me } from "../gen/panel";
import { errorText } from "./errors";
import { meQuery } from "./queries";

/** Where a login may send the browser back: a path of the panel, never another site. */
export function safeNext(next: string | undefined): string {
  return next?.startsWith("/") && !next.startsWith("//") ? next : "/";
}

/** The state every form of the auth screens shares: busy, the error text, and the submit. */
export function useAuthForm(next?: string) {
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  /** run does the request; a Me answer opens the panel. */
  const submit = (check: () => string | null, run: () => Promise<Me>) => async (e: FormEvent) => {
    e.preventDefault();
    const problem = check();
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const me = await run();
      qc.setQueryData(meQuery.queryKey, me);
      await qc.invalidateQueries({ queryKey: ["status"] });
      await navigate({ to: safeNext(next) });
    } catch (err) {
      setError(errorText(te, err));
    } finally {
      setBusy(false);
    }
  };
  return { error, setError, busy, submit, te };
}
