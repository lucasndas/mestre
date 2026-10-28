import type { ReactNode } from "react";
import {
  Button as AriaButton,
  type ButtonProps as AriaButtonProps,
  TextField as AriaTextField,
  type TextFieldProps as AriaTextFieldProps,
  FieldError,
  Input,
  Label,
  Text,
} from "react-aria-components";

type Variant = "primary" | "secondary" | "quiet" | "danger";

const variants: Record<Variant, string> = {
  primary:
    "bg-accent text-accent-ink hover:bg-accent-strong data-pressed:bg-accent-strong disabled:opacity-60",
  secondary:
    "border border-line bg-surface text-ink hover:bg-accent-wash data-pressed:bg-accent-wash disabled:opacity-60",
  quiet: "text-accent hover:bg-accent-wash data-pressed:bg-accent-wash disabled:opacity-60",
  danger: "bg-danger text-accent-ink hover:brightness-90 disabled:opacity-60",
};

export function Button({
  variant = "primary",
  className = "",
  ...props
}: AriaButtonProps & { variant?: Variant; className?: string }) {
  return (
    <AriaButton
      {...props}
      className={`inline-flex min-h-11 items-center justify-center gap-2 rounded-field px-4 font-semibold transition-colors duration-150 ${variants[variant]} ${className}`}
    />
  );
}

export interface FieldProps extends Omit<AriaTextFieldProps, "children"> {
  label: string;
  description?: string;
  error?: string;
  placeholder?: string;
}

/** A labelled text field; the error shows under it, tied to it for screen readers. */
export function Field({
  label,
  description,
  error,
  placeholder,
  className = "",
  ...props
}: FieldProps) {
  return (
    <AriaTextField
      {...props}
      isInvalid={props.isInvalid || Boolean(error)}
      className={`flex flex-col gap-1 ${className as string}`}
    >
      <Label className="text-[0.95em] font-semibold tracking-wide">{label}</Label>
      <Input
        placeholder={placeholder}
        className="min-h-11 rounded-field border border-line bg-paper px-3 text-ink outline-none transition-shadow placeholder:text-muted focus:border-accent focus:ring-3 focus:ring-accent/25 invalid:border-danger"
      />
      {description ? (
        <Text slot="description" className="text-sm text-muted">
          {description}
        </Text>
      ) : null}
      <FieldError className="text-sm text-danger">{error}</FieldError>
    </AriaTextField>
  );
}

export function Alert({
  tone = "error",
  children,
}: {
  tone?: "error" | "ok";
  children: ReactNode;
}) {
  const cls =
    tone === "error"
      ? "bg-danger-wash text-danger border-danger/40"
      : "bg-accent-wash text-ink border-line";
  return (
    <div
      role={tone === "error" ? "alert" : "status"}
      className={`rounded-field border px-3 py-2 ${cls}`}
    >
      {children}
    </div>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <div
      className={`w-full rounded-field border border-line bg-surface p-8 shadow-card ${className}`}
    >
      {children}
    </div>
  );
}
