import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Alert, Button, Field } from "../components/ui";
import type { PasswordRequest } from "../gen/panel";
import { api } from "../lib/api";
import { errorText } from "../lib/errors";

export function Password() {
  const { t } = useTranslation("auth");
  const { t: te } = useTranslation("errors");
  const [f, setF] = useState({ current: "", next: "", next2: "" });
  const set = (k: keyof typeof f) => (v: string) => setF((s) => ({ ...s, [k]: v }));
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <section className="max-w-md">
      <h1 className="text-3xl">{t("password.title")}</h1>
      <p className="mt-1 mb-6 text-muted">{t("password.subtitle")}</p>
      <form
        className="flex flex-col gap-4"
        onSubmit={async (e) => {
          e.preventDefault();
          setDone(false);
          if (!f.current || !f.next) return setError(te("required"));
          if (f.next !== f.next2) return setError(te("mismatch_password"));
          setBusy(true);
          setError(null);
          try {
            await api("POST", "/api/password", {
              current: f.current,
              new: f.next,
            } satisfies PasswordRequest);
            setDone(true);
            setF({ current: "", next: "", next2: "" });
          } catch (err) {
            setError(errorText(te, err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <Field
          label={t("password.current")}
          type="password"
          autoComplete="current-password"
          value={f.current}
          onChange={set("current")}
        />
        <Field
          label={t("password.next")}
          type="password"
          autoComplete="new-password"
          value={f.next}
          onChange={set("next")}
        />
        <Field
          label={t("password.next2")}
          type="password"
          autoComplete="new-password"
          value={f.next2}
          onChange={set("next2")}
        />
        {error ? <Alert>{error}</Alert> : null}
        {done ? <Alert tone="ok">{t("password.done")}</Alert> : null}
        <Button type="submit" isDisabled={busy} className="self-start">
          {t("password.submit")}
        </Button>
      </form>
    </section>
  );
}
