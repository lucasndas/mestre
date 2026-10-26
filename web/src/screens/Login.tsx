import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AuthLayout } from "../app/AuthLayout";
import { Alert, Button, Field } from "../components/ui";
import type { LoginRequest, Me } from "../gen/panel";
import { api } from "../lib/api";
import { errorText, localError } from "../lib/errors";
import { useAuthForm } from "../lib/forms";

export function Login({ next }: { next?: string }) {
  const { t } = useTranslation("auth");
  const { t: te } = useTranslation("errors");
  const [login, setLogin] = useState("");
  const [password, setPassword] = useState("");
  const { error, busy, submit } = useAuthForm(next);
  return (
    <AuthLayout title={t("login.title")} subtitle={t("login.subtitle")}>
      <form
        className="flex flex-col gap-4"
        onSubmit={submit(
          () => (login && password ? null : localError("required")),
          () =>
            api<Me>("POST", "/api/login", { login, password } satisfies LoginRequest, {
              expect401: true,
            }),
        )}
      >
        <Field
          label={t("login.login")}
          autoComplete="username"
          autoFocus
          value={login}
          onChange={setLogin}
        />
        <Field
          label={t("login.password")}
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={setPassword}
        />
        {error ? <Alert>{errorText(te, error)}</Alert> : null}
        <Button type="submit" isDisabled={busy} className="mt-2">
          {t("login.submit")}
        </Button>
        <Link
          to="/primeiro-acesso"
          search={{}}
          className="text-center text-accent underline-offset-4 hover:underline"
        >
          {t("login.firstAccess")}
        </Link>
      </form>
    </AuthLayout>
  );
}
