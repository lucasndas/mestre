import { queryOptions } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { Checkbox, CheckboxGroup, Label, Radio, RadioGroup } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useMe } from "../../app/session";
import { Term } from "../../components/Term";
import { Button } from "../../components/ui";
import type { TermKey } from "../../gen/glossary";
import type { AuditPage, GitHubMe, SecretList, TransferState, UserList } from "../../gen/panel";
import { ApiError, api } from "../../lib/api";
import { errorText } from "../../lib/errors";

// What the administration screens ask the server; the keys are what a mutation
// invalidates after it.
export const usersQuery = queryOptions({
  queryKey: ["admin", "users"],
  queryFn: () => api<UserList>("GET", "/api/users"),
});

export const secretsQuery = queryOptions({
  queryKey: ["admin", "secrets"],
  queryFn: () => api<SecretList>("GET", "/api/secrets"),
});

export const githubMeQuery = queryOptions({
  queryKey: ["admin", "github-me"],
  queryFn: () => api<GitHubMe>("GET", "/api/me/github"),
});

export const transferQuery = queryOptions({
  queryKey: ["admin", "transfer"],
  queryFn: () => api<TransferState>("GET", "/api/transfer"),
});

export interface AuditFilters {
  user: string;
  action: string;
  from: string;
  to: string;
  workspace: string;
}

export const noAuditFilters: AuditFilters = {
  user: "",
  action: "",
  from: "",
  to: "",
  workspace: "",
};

export function auditPath(f: AuditFilters, cursor: string): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) q.set(k, v);
  if (cursor) q.set("cursor", cursor);
  const s = q.toString();
  return `/api/audit${s ? `?${s}` : ""}`;
}

export const auditFetch = (f: AuditFilters, cursor: string) =>
  api<AuditPage>("GET", auditPath(f, cursor));

/** The heading and the one line under it every administration screen opens with. */
export function PageHeader({ title, intro }: { title: string; intro: ReactNode }) {
  return (
    <>
      <h1 className="text-3xl">{title}</h1>
      <p className="mt-1 mb-6 max-w-2xl text-muted">{intro}</p>
    </>
  );
}

export type Tone = "neutral" | "warn" | "ok" | "danger";

// The tone is told by a symbol and a word as well as by the color (the pattern of the
// administration screens, docs/painel/design.md#tela-de-administração).
const toneStyle: Record<Tone, { box: string; symbol: string }> = {
  neutral: { box: "border-line bg-surface", symbol: "○" },
  warn: { box: "border-warn-ink/40 bg-warn text-warn-ink", symbol: "▲" },
  ok: { box: "border-signal bg-surface", symbol: "✓" },
  danger: { box: "border-danger/40 bg-danger-wash text-danger", symbol: "✕" },
};

/**
 * What is true now on a screen and the one thing to do next: a sentence made of the same data
 * the list reads, a line of support and, on the right (below on a phone), the main action.
 */
export function StatusStrip({
  tone = "neutral",
  title,
  hint,
  children,
}: {
  tone?: Tone;
  title: string;
  hint?: ReactNode;
  /** The main action of the screen, one. */
  children?: ReactNode;
}) {
  const { t } = useTranslation("common");
  const st = toneStyle[tone];
  return (
    <div
      role="status"
      className={`mb-6 flex max-w-4xl flex-col gap-3 rounded-field border p-4 sm:flex-row sm:items-center sm:justify-between ${st.box}`}
    >
      <div className="min-w-0">
        <p className="flex items-baseline gap-2 text-lg font-semibold">
          <span aria-hidden="true">{st.symbol}</span>
          <span className="sr-only">{t(`admin.tone.${tone}`)}: </span>
          <span>{title}</span>
        </p>
        {hint ? <p className="mt-1 text-sm">{hint}</p> : null}
      </div>
      {children ? <div className="flex flex-wrap items-center gap-2">{children}</div> : null}
    </div>
  );
}

/** A thing that does not exist yet: what it is, why it matters and the next step, never only "none". */
export function EmptyState({
  title,
  why,
  steps,
  children,
}: {
  title: string;
  why: ReactNode;
  steps?: string[];
  /** The way out: a button or a link. */
  children?: ReactNode;
}) {
  return (
    <section className="flex max-w-3xl flex-col gap-3 rounded-field border border-dashed border-muted p-5">
      <h2 className="text-xl">{title}</h2>
      <p>{why}</p>
      {steps && steps.length > 0 ? (
        <ol className="ml-5 list-decimal text-sm">
          {steps.map((s) => (
            <li key={s}>{s}</li>
          ))}
        </ol>
      ) : null}
      {children ? <div className="flex flex-wrap gap-2">{children}</div> : null}
    </section>
  );
}

/**
 * A read that failed: what did not load, why in the language of the user, a way to try again and
 * the raw answer of the server folded away. The screen shows this in place of the data, never
 * with the old data beside it as if it were current.
 */
export function LoadError({
  what,
  error,
  onRetry,
}: {
  what: string;
  error: unknown;
  onRetry: () => void;
}) {
  const { t } = useTranslation("common");
  const { t: te } = useTranslation("errors");
  const raw =
    error instanceof ApiError ? [error.code, error.message].filter(Boolean).join(": ") : "";
  return (
    <div
      role="alert"
      className="flex max-w-3xl flex-col gap-3 rounded-field border border-danger/40 bg-danger-wash p-4 text-danger"
    >
      <p className="font-semibold">{t("admin.loadFailed", { what })}</p>
      <p>{errorText(te, error)}</p>
      <div>
        <Button variant="secondary" onPress={onRetry}>
          {t("admin.retry")}
        </Button>
      </div>
      {raw ? (
        <details className="text-sm">
          <summary className="min-h-11 cursor-pointer py-2">{t("admin.technical")}</summary>
          <p className="font-mono break-all">{raw}</p>
        </details>
      ) : null}
    </div>
  );
}

/** The line under the intro with the words of the screen, each one a `Term`. */
export function VocabLine({ terms, extra }: { terms: TermKey[]; extra?: ReactNode }) {
  const { t } = useTranslation("common");
  return (
    <p className="-mt-4 mb-6 flex max-w-2xl flex-wrap items-baseline gap-x-3 gap-y-1 text-sm text-muted">
      <span>{t("admin.vocabulary")}:</span>
      {terms.map((name) => (
        <Term key={name} name={name} />
      ))}
      {extra}
    </p>
  );
}

const LEVELS = ["observer", "approver", "administrator", "owner"] as const;
export type Level = (typeof LEVELS)[number];

/** Whether the level of the user reaches `need` (the server still decides every request). */
export function useReaches(need: Level): boolean {
  const { level } = useMe().user;
  return LEVELS.indexOf(level as Level) >= LEVELS.indexOf(need);
}

/**
 * An action that needs a level: for who reaches it, the children; for who does not, the same
 * button off and the level it needs and the user's own, instead of nothing.
 */
export function LevelGate({
  need,
  label,
  children,
}: {
  need: Level;
  label: string;
  children: ReactNode;
}) {
  const { t } = useTranslation("common");
  const { t: te } = useTranslation("errors");
  const { level } = useMe().user;
  const reaches = useReaches(need);
  if (reaches) return <>{children}</>;
  return (
    <span className="flex flex-wrap items-center gap-2">
      <Button variant="secondary" isDisabled>
        {label}
      </Button>
      <span className="text-sm text-muted">
        {t("admin.needLevel", { need: te(`level_${need}`), have: te(`level_${level}` as never) })}
      </span>
    </span>
  );
}

export function Badge({
  children,
  tone = "plain",
}: {
  children: ReactNode;
  tone?: "plain" | "ok" | "warn" | "danger";
}) {
  const cls = {
    plain: "bg-accent-wash text-ink",
    ok: "border border-signal bg-surface text-signal",
    warn: "bg-warn text-warn-ink",
    danger: "bg-danger-wash text-danger",
  }[tone];
  return (
    <span className={`rounded-full px-2 py-0.5 text-sm font-semibold ${cls}`}>{children}</span>
  );
}

/** A native select: a simple choice from a list, reachable by keyboard and by screen readers. */
export function NativeSelect({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[0.95em] font-semibold tracking-wide">{label}</span>
      <select
        className="min-h-11 rounded-field border border-line bg-paper px-3 text-ink outline-none focus:border-accent focus:ring-3 focus:ring-accent/25"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}

/** One choice among a few, each with a short hint. */
export function ChoiceGroup({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string; hint?: string }[];
}) {
  return (
    <RadioGroup value={value} onChange={onChange} className="flex flex-col gap-1">
      <Label className="text-[0.95em] font-semibold tracking-wide">{label}</Label>
      {options.map((o) => (
        <Radio
          key={o.value}
          value={o.value}
          className="group flex min-h-11 cursor-pointer items-center gap-3 rounded-field px-2 hover:bg-accent-wash"
        >
          <span className="grid size-5 shrink-0 place-items-center rounded-full border border-muted group-data-selected:border-accent group-data-focus-visible:ring-3 group-data-focus-visible:ring-accent/25">
            <span className="hidden size-2.5 rounded-full bg-accent group-data-selected:block" />
          </span>
          <span>
            {o.label}
            {o.hint ? <span className="block text-sm text-muted">{o.hint}</span> : null}
          </span>
        </Radio>
      ))}
    </RadioGroup>
  );
}

/** Several choices from a list (the workspaces a user acts in). */
export function MultiChoice({
  label,
  value,
  onChange,
  options,
  empty,
}: {
  label: string;
  value: string[];
  onChange: (v: string[]) => void;
  options: string[];
  empty: string;
}) {
  return (
    <CheckboxGroup value={value} onChange={onChange} className="flex flex-col gap-1">
      <Label className="text-[0.95em] font-semibold tracking-wide">{label}</Label>
      {options.length === 0 ? <p className="text-sm text-muted">{empty}</p> : null}
      {options.map((o) => (
        <Checkbox
          key={o}
          value={o}
          className="group flex min-h-11 cursor-pointer items-center gap-3 rounded-field px-2 hover:bg-accent-wash"
        >
          <span
            aria-hidden="true"
            className="grid size-5 shrink-0 place-items-center rounded-sm border border-muted text-xs text-accent-ink group-data-selected:border-accent group-data-selected:bg-accent group-data-focus-visible:ring-3 group-data-focus-visible:ring-accent/25"
          >
            <span className="hidden group-data-selected:block">✓</span>
          </span>
          {o}
        </Checkbox>
      ))}
    </CheckboxGroup>
  );
}

/**
 * A secret shown once (a code, a recovery key): big, copyable, with the warning that it
 * does not come back. Nothing keeps it after the person says it was written down.
 */
export function ShownOnce({
  title,
  secret,
  detail,
  note,
  doneLabel,
  onDone,
}: {
  title: string;
  secret: string;
  detail?: string;
  /** What is said about it not coming back (default: for a code). */
  note?: string;
  doneLabel?: string;
  onDone: () => void;
}) {
  const { t } = useTranslation("users");
  const [copied, setCopied] = useState(false);
  return (
    <section
      role="status"
      aria-label={title}
      className="mb-6 flex max-w-2xl flex-col gap-3 rounded-field border border-accent bg-accent-wash p-5"
    >
      <h2 className="text-xl">{title}</h2>
      <p
        className="font-mono text-2xl font-semibold tracking-wider break-all select-all"
        data-testid="shown-once"
      >
        {secret}
      </p>
      {detail ? <p className="text-sm">{detail}</p> : null}
      <p className="text-sm font-semibold">{note ?? t("codes.once")}</p>
      <div className="flex gap-2">
        <Button
          variant="secondary"
          onPress={() => {
            void navigator.clipboard
              ?.writeText(secret)
              .then(() => setCopied(true))
              .catch(() => undefined);
          }}
        >
          {t("codes.copy")}
        </Button>
        <Button onPress={onDone}>{doneLabel ?? t("codes.done")}</Button>
      </div>
      {copied ? (
        <p role="status" className="text-sm">
          {t("codes.copied")}
        </p>
      ) : null}
    </section>
  );
}

/** The list of a comma separated field, without the blanks. */
export function splitList(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
}
