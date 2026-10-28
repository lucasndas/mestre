import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AuthLayout } from "../app/AuthLayout";
import { Alert, Button, Field } from "../components/ui";
import type { FirstAccessRequest, Me } from "../gen/panel";
import { ApiError, api } from "../lib/api";
import { errorText, localError } from "../lib/errors";
import { useAuthForm } from "../lib/forms";

type Say = (k: string, o?: object) => string;

const FIRST_ACCESS_ERRORS = [
  "invalid_code",
  "password_too_short",
  "login_taken",
  "invalid_pin",
  "rate_limited",
];

/** The text of an error of this screen: in its own words and with the way out, by the `code`. */
function firstAccessText(
  t: Say,
  te: Parameters<typeof errorText>[0],
  err: unknown,
  pinEmpty: boolean,
): string {
  // the server answers a first access without PIN as an internal error: say what is missing
  if (err instanceof ApiError && err.code === "internal" && pinEmpty)
    return t("first.errors.pin_missing");
  if (err instanceof ApiError && FIRST_ACCESS_ERRORS.includes(err.code)) {
    const secs = err.params.retry_after_seconds;
    const minutes = typeof secs === "number" ? Math.max(1, Math.ceil(secs / 60)) : 1;
    return t(`first.errors.${err.code}`, { minutes });
  }
  return errorText(te, err);
}

/**
 * The first access of a user, or the new password after a reset. A reset has no
 * PIN: the user leaves the PIN fields empty.
 */
export function FirstAccess() {
  const { t } = useTranslation("auth");
  const { t: te } = useTranslation("errors");
  const [f, setF] = useState({
    code: "",
    login: "",
    password: "",
    password2: "",
    pin: "",
    pin2: "",
  });
  const set = (k: keyof typeof f) => (v: string) => setF((s) => ({ ...s, [k]: v }));
  const { error, busy, submit } = useAuthForm();
  return (
    <AuthLayout title={t("first.title")} subtitle={t("first.subtitle")} wide>
      <form
        className="flex flex-col gap-4"
        onSubmit={submit(
          () => {
            if (!f.code || !f.login || !f.password) return localError("required");
            if (f.password !== f.password2) return localError("mismatch_password");
            if (f.pin !== f.pin2) return localError("mismatch_pin");
            return null;
          },
          () =>
            api<Me>(
              "POST",
              "/api/first-access",
              {
                code: f.code.trim(),
                login: f.login.trim(),
                password: f.password,
                ...(f.pin ? { pin: f.pin } : {}),
              } satisfies FirstAccessRequest,
              { expect401: true },
            ),
        )}
      >
        <Field
          label={t("first.code")}
          autoComplete="off"
          autoFocus
          value={f.code}
          onChange={set("code")}
        />
        <Field
          label={t("first.login")}
          autoComplete="username"
          value={f.login}
          onChange={set("login")}
        />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label={t("first.password")}
            type="password"
            autoComplete="new-password"
            value={f.password}
            onChange={set("password")}
          />
          <Field
            label={t("first.password2")}
            type="password"
            autoComplete="new-password"
            value={f.password2}
            onChange={set("password2")}
          />
          <Field
            label={t("first.pin")}
            type="password"
            inputMode="numeric"
            autoComplete="off"
            value={f.pin}
            onChange={set("pin")}
          />
          <Field
            label={t("first.pin2")}
            type="password"
            inputMode="numeric"
            autoComplete="off"
            value={f.pin2}
            onChange={set("pin2")}
          />
        </div>
        <p className="text-sm text-muted">{t("first.pinWhy")}</p>
        {error ? (
          <Alert>{firstAccessText(t as unknown as Say, te, error, f.pin === "")}</Alert>
        ) : null}
        <Button type="submit" isDisabled={busy} className="mt-2">
          {t("first.submit")}
        </Button>
        <Link
          to="/login"
          search={{}}
          className="text-center text-accent underline-offset-4 hover:underline"
        >
          {t("first.back")}
        </Link>
      </form>
    </AuthLayout>
  );
}
