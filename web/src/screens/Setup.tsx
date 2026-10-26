import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AuthLayout } from "../app/AuthLayout";
import { Term } from "../components/Term";
import { Alert, Button, Field } from "../components/ui";
import type { Me, SetupRequest } from "../gen/panel";
import { api } from "../lib/api";
import { useAuthForm } from "../lib/forms";

/** The wizard of the Owner: the Guild has no user yet. */
export function Setup() {
  const { t } = useTranslation("auth");
  const [f, setF] = useState({
    code: "",
    name: "",
    login: "",
    password: "",
    password2: "",
    pin: "",
    pin2: "",
  });
  const set = (k: keyof typeof f) => (v: string) => setF((s) => ({ ...s, [k]: v }));
  const { error, busy, submit, te } = useAuthForm();
  return (
    <AuthLayout title={t("setup.title")} subtitle={t("setup.subtitle")} wide>
      <p className="-mt-3 mb-5 text-sm text-muted">
        <Term name="Guilda" /> · <Term name="Cidadela" />
      </p>
      <form
        className="flex flex-col gap-4"
        onSubmit={submit(
          () => {
            if (!f.code || !f.name || !f.login || !f.password || !f.pin) return te("required");
            if (f.password !== f.password2) return te("mismatch_password");
            if (f.pin !== f.pin2) return te("mismatch_pin");
            return null;
          },
          () =>
            api<Me>(
              "POST",
              "/api/setup",
              {
                code: f.code.trim(),
                name: f.name.trim(),
                login: f.login.trim(),
                password: f.password,
                pin: f.pin,
              } satisfies SetupRequest,
              { expect401: true },
            ),
        )}
      >
        <Field
          label={t("setup.code")}
          autoFocus
          autoComplete="off"
          value={f.code}
          onChange={set("code")}
        />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label={t("setup.name")}
            autoComplete="name"
            value={f.name}
            onChange={set("name")}
          />
          <Field
            label={t("setup.login")}
            autoComplete="username"
            value={f.login}
            onChange={set("login")}
          />
          <Field
            label={t("setup.password")}
            type="password"
            autoComplete="new-password"
            value={f.password}
            onChange={set("password")}
          />
          <Field
            label={t("setup.password2")}
            type="password"
            autoComplete="new-password"
            value={f.password2}
            onChange={set("password2")}
          />
          <Field
            label={t("setup.pin")}
            type="password"
            inputMode="numeric"
            autoComplete="off"
            description={t("setup.pinWhy")}
            value={f.pin}
            onChange={set("pin")}
          />
          <Field
            label={t("setup.pin2")}
            type="password"
            inputMode="numeric"
            autoComplete="off"
            value={f.pin2}
            onChange={set("pin2")}
          />
        </div>
        {error ? <Alert>{error}</Alert> : null}
        <Button type="submit" isDisabled={busy} className="mt-2">
          {t("setup.submit")}
        </Button>
      </form>
    </AuthLayout>
  );
}
