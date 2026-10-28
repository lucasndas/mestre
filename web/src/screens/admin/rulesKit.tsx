import { queryOptions, useQuery } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import { Button as AriaButton, Label, TextArea, TextField } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useChatControl } from "../../app/chatControl";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button } from "../../components/ui";
import type {
  CeilingView,
  ChainStatus,
  DecreeList,
  DecreeProposal,
  DelegationList,
  Instructions,
  PolicyFile,
  RuleWorkspaces,
} from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { EmptyState, LoadError, NativeSelect } from "./kit";

// What the screens of the rules and of the chain ask the server. The keys sit under
// "admin", so the invalidation every mutation does reloads them.
export const ruleWorkspacesQuery = queryOptions({
  queryKey: ["admin", "rules", "workspaces"],
  queryFn: () => api<RuleWorkspaces>("GET", "/api/rules/workspaces"),
});

export const decreesQuery = (all: boolean) =>
  queryOptions({
    queryKey: ["admin", "rules", "decrees", all],
    queryFn: () => api<DecreeList>("GET", all ? "/api/decrees?all=1" : "/api/decrees"),
  });

export const delegationsQuery = queryOptions({
  queryKey: ["admin", "rules", "delegations"],
  queryFn: () => api<DelegationList>("GET", "/api/delegations"),
});

export const chainQuery = queryOptions({
  queryKey: ["admin", "rules", "chain"],
  queryFn: () => api<ChainStatus>("GET", "/api/chain"),
});

const enc = encodeURIComponent;

export const ceilingQuery = (ws: string) =>
  queryOptions({
    queryKey: ["admin", "rules", "ceiling", ws],
    queryFn: () => api<CeilingView>("GET", `/api/workspaces/${enc(ws)}/ceiling`),
  });

export const policyQuery = (ws: string) =>
  queryOptions({
    queryKey: ["admin", "rules", "policy", ws],
    queryFn: () => api<PolicyFile>("GET", `/api/workspaces/${enc(ws)}/policy`),
    // a file being edited is not replaced by a reload under the person's hands
    staleTime: Number.POSITIVE_INFINITY,
  });

export const instructionsQuery = (ws: string) =>
  queryOptions({
    queryKey: ["admin", "rules", "instructions", ws],
    queryFn: () => api<Instructions>("GET", `/api/workspaces/${enc(ws)}/instructions`),
    staleTime: Number.POSITIVE_INFINITY,
  });

/**
 * The workspace of a screen that works on one: the first of the person's until they pick.
 * `empty` says there is none to pick.
 */
export function WorkspacePicker({
  children,
}: {
  children: (workspace: string, picker: ReactNode) => ReactNode;
}) {
  const { t } = useTranslation("rules");
  const { data, error, refetch } = useQuery(ruleWorkspacesQuery);
  const chat = useChatControl();
  const [picked, setPicked] = useState("");
  if (error)
    return (
      <LoadError
        what={t("loadWhat.workspaces")}
        why={t("loadWhy.workspaces")}
        error={error}
        onRetry={() => void refetch()}
      />
    );
  if (!data) return <p className="text-muted">{t("loading")}</p>;
  if (data.workspaces.length === 0)
    return (
      <EmptyState
        title={t("noWorkspaceTitle")}
        why={t("noWorkspace")}
        steps={[t("noWorkspaceStep1"), t("noWorkspaceStep2")]}
      >
        <Button onPress={chat.show}>{t("talk")}</Button>
      </EmptyState>
    );
  const ws = data.workspaces.includes(picked) ? picked : (data.workspaces[0] ?? "");
  const picker =
    data.workspaces.length > 1 ? (
      <div className="mb-6 max-w-xs">
        <NativeSelect
          label={t("workspace")}
          value={ws}
          onChange={setPicked}
          options={data.workspaces.map((w) => ({ value: w, label: w }))}
        />
      </div>
    ) : null;
  return <>{children(ws, picker)}</>;
}

/** A multi-line text field: the order of a decree, the text of an instruction, a policy file. */
export function Area({
  label,
  description,
  value,
  onChange,
  rows = 4,
  mono = false,
  invalidText,
  placeholder,
  hideLabel = false,
}: {
  label: string;
  /** The heading above already says it: the label stays for screen readers only. */
  hideLabel?: boolean;
  description?: string;
  placeholder?: string;
  value: string;
  onChange: (v: string) => void;
  rows?: number;
  mono?: boolean;
  invalidText?: string;
}) {
  return (
    <TextField
      value={value}
      onChange={onChange}
      isInvalid={Boolean(invalidText)}
      className="flex flex-col gap-1"
    >
      <Label className={hideLabel ? "sr-only" : "text-[0.95em] font-semibold tracking-wide"}>
        {label}
      </Label>
      <TextArea
        rows={rows}
        placeholder={placeholder}
        spellCheck={!mono}
        className={`min-h-11 rounded-field border border-line bg-paper px-3 py-2 text-ink outline-none transition-shadow placeholder:text-muted focus:border-accent focus:ring-3 focus:ring-accent/25 invalid:border-danger ${
          mono ? "font-mono text-sm leading-relaxed whitespace-pre" : ""
        }`}
      />
      {description ? <span className="text-sm text-muted">{description}</span> : null}
    </TextField>
  );
}

/** A button for an action that does not come back: it asks once more, in the very place. */
export function ConfirmButton({
  children,
  warning,
  confirmLabel,
  onConfirm,
  variant = "danger",
}: {
  children: string;
  warning: string;
  confirmLabel: string;
  onConfirm: () => Promise<unknown>;
  variant?: "danger" | "secondary";
}) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (!asking) {
    return (
      <Button
        variant={variant === "danger" ? "secondary" : variant}
        className={variant === "danger" ? "border-danger/50 text-danger" : ""}
        onPress={() => setAsking(true)}
      >
        {children}
      </Button>
    );
  }
  return (
    <fieldset aria-label={children} className="flex flex-col gap-2">
      <p className="text-sm">{warning}</p>
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      <div className="flex gap-2">
        <Button
          variant={variant}
          isDisabled={busy}
          onPress={async () => {
            setBusy(true);
            setError(null);
            try {
              await onConfirm();
              setAsking(false);
            } catch (err) {
              setError(err);
            } finally {
              setBusy(false);
            }
          }}
        >
          {confirmLabel}
        </Button>
        <Button variant="quiet" onPress={() => setAsking(false)}>
          {t("cancel")}
        </Button>
      </div>
    </fieldset>
  );
}

/**
 * What the engine understood, waiting for the click of its author: nothing is a decree
 * before it. The PIN goes in the very request of the click, only when the engine asks.
 */
export function ProposalCard({
  proposal,
  onConfirmed,
  onCancel,
}: {
  proposal: DecreeProposal;
  onConfirmed: () => void | Promise<void>;
  onCancel: () => void;
}) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const confirm = async (pin?: string) => {
    await api("POST", `/api/decrees/proposals/${enc(proposal.id)}/confirm`, undefined, { pin });
    await onConfirmed();
  };
  return (
    <section
      aria-label={t("proposal.title")}
      className="flex max-w-2xl flex-col gap-3 rounded-field border border-accent bg-accent-wash p-5"
    >
      <h2 className="text-xl">{t("proposal.title")}</h2>
      <p>{proposal.text}</p>
      <p className="text-sm text-muted">{t("proposal.notYet")}</p>
      {proposal.pin ? <p className="text-sm font-semibold">{t("proposal.pin")}</p> : null}
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      <div className="flex flex-wrap items-start gap-3">
        {proposal.pin ? (
          <SensitiveButton onConfirm={(pin) => confirm(pin)}>
            {t("proposal.confirm")}
          </SensitiveButton>
        ) : (
          <Button
            isDisabled={busy}
            onPress={async () => {
              setBusy(true);
              setError(null);
              try {
                await confirm();
              } catch (err) {
                setError(err);
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("proposal.confirm")}
          </Button>
        )}
        <Button variant="quiet" onPress={onCancel}>
          {t("proposal.discard")}
        </Button>
      </div>
    </section>
  );
}

/** A duration in whole seconds as "40 min" or "1 h 5 min". */
export function useDuration(): (seconds: number) => string {
  const { t } = useTranslation("rules");
  return (s) => {
    const min = Math.floor(s / 60);
    if (min < 1) return t("duration.lessThanMinute");
    if (min < 60) return t("duration.minutes", { count: min });
    const h = Math.floor(min / 60);
    const rest = min % 60;
    return rest === 0
      ? t("duration.hours", { count: h })
      : t("duration.hoursMinutes", { hours: h, minutes: rest });
  };
}

/** The words of a value the engine names with a code (a level, a risk, a type); the code itself when it has none. */
export function useWords(): (group: "levels" | "risks" | "types", code: string) => string {
  const { t } = useTranslation("rules");
  const say = t as unknown as (k: string) => string;
  return (group, code) => {
    const k = `${group}.${code}`;
    const text = say(k);
    return text === k ? code : text;
  };
}

/** A small plain button that looks like text, for an action inside a sentence. */
export function TextButton({ children, onPress }: { children: string; onPress: () => void }) {
  return (
    <AriaButton
      onPress={onPress}
      className="min-h-11 px-1 font-semibold text-accent underline-offset-2 hover:underline"
    >
      {children}
    </AriaButton>
  );
}
