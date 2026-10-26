import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AuthLayout } from "../app/AuthLayout";
import { Alert, Button, Field } from "../components/ui";
import type { FirstAccessRequest, Me } from "../gen/panel";
import { api } from "../lib/api";
import { useAuthForm } from "../lib/forms";

/**
 * The first access of a user, or the new password after a reset. A reset has no
 * PIN: the user leaves the PIN fields empty.
 */
export function FirstAccess({ code }: { code?: string }) {
  const { t } = useTranslation("auth");
  const [f, setF] = useState({
    code: code ?? "",
    login: "",
    password: "",
    password2: "",
    pin: "",
    pin2: "",
  });
  const set = (k: keyof typeof f) => (v: string) => setF((s) => ({ ...s, [k]: v }));
  const { error, busy, submit, te } = useAuthForm();
  return (
    <AuthLayout title={t("first.title")} subtitle={t("first.subtitle")} wide>
      <form
        className="flex flex-col gap-4"
        onSubmit={submit(
          () => {
            if (!f.code || !f.login || !f.password) return te("required");
            if (f.password !== f.password2) return te("mismatch_password");
            if (f.pin !== f.pin2) return te("mismatch_pin");
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
          autoFocus={!code}
          value={f.code}
          onChange={set("code")}
        />
        <Field
          label={t("first.login")}
          autoComplete="username"
          autoFocus={Boolean(code)}
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
        {error ? <Alert>{error}</Alert> : null}
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
