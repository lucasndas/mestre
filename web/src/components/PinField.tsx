import { useState } from "react";
import { useTranslation } from "react-i18next";
import { errorText } from "../lib/errors";
import { Alert, Button, Field } from "./ui";

/**
 * A button for a sensitive action. The PIN field shows up after the button is
 * pressed and goes to the server in the very request of the action
 * (docs/painel/login.md#pin-em-ações-sensíveis); nothing remembers it.
 */
export function SensitiveButton({
  children,
  onConfirm,
  variant = "primary",
  isDisabled = false,
}: {
  children: string;
  onConfirm: (pin: string) => Promise<unknown>;
  variant?: "primary" | "danger" | "secondary";
  /** The action is not ready (a form that is not filled): the button cannot be pressed. */
  isDisabled?: boolean;
}) {
  const { t } = useTranslation("common");
  const { t: te } = useTranslation("errors");
  const [asking, setAsking] = useState(false);
  const [pin, setPin] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  if (!asking) {
    return (
      <Button variant={variant} isDisabled={isDisabled} onPress={() => setAsking(true)}>
        {children}
      </Button>
    );
  }
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(null);
        try {
          await onConfirm(pin);
          setAsking(false);
        } catch (err) {
          setError(err);
        } finally {
          setPin("");
          setBusy(false);
        }
      }}
    >
      <Field
        label={t("pin.label")}
        description={t("pin.hint")}
        type="password"
        inputMode="numeric"
        autoComplete="off"
        autoFocus
        value={pin}
        onChange={setPin}
      />
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      <div className="flex gap-2">
        <Button type="submit" variant={variant} isDisabled={busy || pin.length === 0}>
          {children}
        </Button>
        <Button variant="quiet" onPress={() => setAsking(false)}>
          {t("actions.cancel")}
        </Button>
      </div>
    </form>
  );
}
