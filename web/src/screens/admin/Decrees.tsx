import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Checkbox } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useCan, useFormatDateTime } from "../../app/session";
import { SensitiveButton } from "../../components/PinField";
import { Alert, Button, Field } from "../../components/ui";
import type { DecreeInput, DecreeProposal, DecreeRow } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import {
  Badge,
  ChoiceGroup,
  EmptyState,
  LevelGate,
  LoadError,
  NativeSelect,
  PageHeader,
  StatusStrip,
  VocabLine,
} from "./kit";
import {
  Area,
  ConfirmButton,
  decreesQuery,
  ProposalCard,
  ruleWorkspacesQuery,
  useWords,
} from "./rulesKit";

type Say = (k: string, o?: object) => string;

export function Decrees() {
  const { t } = useTranslation("rules");
  const qc = useQueryClient();
  const canPropose = useCan("one_off_exception");
  const [all, setAll] = useState(false);
  const [creating, setCreating] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const { data, error, refetch } = useQuery(decreesQuery(all));
  const decrees = data?.workspaces.flatMap((g) => g.decrees) ?? [];
  const inForce = decrees.filter((d) => d.state === "active");
  const today = new Date().toDateString();
  const endsToday = inForce.filter((d) => d.until && new Date(d.until).toDateString() === today);
  const create = (
    <LevelGate need="approver" label={t("decrees.new")}>
      {canPropose && !creating ? (
        <Button onPress={() => setCreating(true)}>{t("decrees.new")}</Button>
      ) : null}
    </LevelGate>
  );
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  return (
    <section>
      <PageHeader title={t("decrees.title")} intro={t("decrees.intro")} />
      <VocabLine terms={["Decreto", "Regra permanente", "Exceção pontual"]} />
      {data ? (
        <StatusStrip
          tone={inForce.length > 0 ? "ok" : "neutral"}
          title={t("decrees.strip", { count: inForce.length })}
          hint={
            endsToday.length > 0
              ? t("decrees.stripToday", { count: endsToday.length })
              : inForce.length === 0
                ? t("decrees.stripNone")
                : undefined
          }
        >
          {create}
        </StatusStrip>
      ) : null}
      <div className="flex max-w-4xl flex-col gap-5">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? (
          <LoadError what={t("loadWhat.decrees")} error={error} onRetry={() => void refetch()} />
        ) : null}
        {canPropose && creating ? (
          <DecreeForm
            onClose={() => setCreating(false)}
            onMade={async () => {
              setCreating(false);
              setNotice(t("decrees.made"));
              await refresh();
            }}
          />
        ) : null}
        <Checkbox
          isSelected={all}
          onChange={setAll}
          className="group flex min-h-11 cursor-pointer items-center gap-3"
        >
          <span
            aria-hidden="true"
            className="grid size-5 shrink-0 place-items-center rounded-sm border border-muted text-xs text-accent-ink group-data-selected:border-accent group-data-selected:bg-accent group-data-focus-visible:ring-3 group-data-focus-visible:ring-accent/25"
          >
            <span className="hidden group-data-selected:block">✓</span>
          </span>
          {t("decrees.showEnded")}
        </Checkbox>
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data && decrees.length === 0 && !creating ? (
          <EmptyState
            title={t("decrees.emptyTitle")}
            why={t("decrees.emptyWhy")}
            steps={[t("decrees.emptyStep1"), t("decrees.emptyStep2")]}
          />
        ) : null}
        {data?.workspaces
          .filter((g) => g.decrees.length > 0)
          .map((g) => (
            <section
              key={g.workspace}
              aria-labelledby={`ws-${g.workspace}`}
              className="flex flex-col gap-3"
            >
              <h2 id={`ws-${g.workspace}`} className="text-2xl">
                {g.workspace}
              </h2>
              <ul className="flex flex-col gap-3">
                {g.decrees.map((d) => (
                  <li key={d.number}>
                    <DecreeCard
                      d={d}
                      onDone={async () => {
                        setNotice(t("decrees.revoked", { number: d.number }));
                        await refresh();
                      }}
                    />
                  </li>
                ))}
              </ul>
            </section>
          ))}
      </div>
    </section>
  );
}

export function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="text-muted">{label}</dt>
      <dd>{children}</dd>
    </>
  );
}

/** One decree: who ordered what, until when, and what it freed. */
export function DecreeCard({ d, onDone }: { d: DecreeRow; onDone: () => Promise<void> }) {
  const { t } = useTranslation("rules");
  const say = t as unknown as Say;
  const fmt = useFormatDateTime();
  const words = useWords();
  const ended = d.state === "revoked" || d.state === "expired" || d.state === "used";
  const stateTone = d.state === "active" ? "ok" : d.state === "suspended" ? "warn" : "plain";
  const revoke = (pin?: string) =>
    api("POST", `/api/decrees/${d.number}/revoke`, undefined, { pin });
  return (
    <article
      className="rounded-field border border-line bg-surface p-4"
      aria-label={t("decrees.number", { number: d.number })}
    >
      <p className="flex flex-wrap items-center gap-2 font-semibold">
        <span>{t("decrees.number", { number: d.number })}</span>
        <Badge>{say(`decrees.kinds.${d.kind}`)}</Badge>
        <Badge tone={stateTone}>{say(`decrees.states.${d.state}`)}</Badge>
        {d.frozen ? <Badge tone="warn">{t("decrees.frozen")}</Badge> : null}
      </p>
      <p className="mt-1">{d.summary}</p>
      <blockquote className="mt-2 border-l-2 border-line pl-3 text-muted">
        <span className="block text-sm font-semibold">{t("decrees.text")}:</span>“{d.text}”
      </blockquote>
      <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <Row label={t("decrees.author")}>
          {d.author_name} · {words("levels", d.level)}
        </Row>
        <Row label={t("decrees.scope")}>
          {[d.mesa, d.scope].filter(Boolean).join(" · ") || t("decrees.wholeWorkspace")}
        </Row>
        <Row label={t("decrees.validity")}>
          {d.kind === "exception"
            ? t("decrees.once")
            : d.until
              ? t("decrees.until", { when: fmt(d.until) })
              : t("decrees.permanent")}
        </Row>
        <Row label={t("decrees.created")}>{fmt(d.created_at)}</Row>
        {d.ended_at ? <Row label={t("decrees.ended")}>{fmt(d.ended_at)}</Row> : null}
        <Row label={t("decrees.freed")}>
          {d.use_count === 0 ? (
            t("decrees.neverUsed")
          ) : (
            <>
              {t("decrees.uses", { count: d.use_count })}
              <ul className="mt-1 list-disc pl-5">
                {d.uses.map((u) => (
                  <li key={`${u.at}-${u.action}`}>
                    {fmt(u.at)} · {u.character} · {u.action}
                  </li>
                ))}
              </ul>
            </>
          )}
        </Row>
      </dl>
      {!ended && d.can_revoke ? (
        <div className="mt-3">
          {d.revoke_needs_pin ? (
            <div className="flex flex-col items-start gap-1">
              <SensitiveButton
                variant="danger"
                onConfirm={async (pin) => {
                  await revoke(pin);
                  await onDone();
                }}
              >
                {t("decrees.revoke")}
              </SensitiveButton>
              <p className="text-sm text-muted">{t("decrees.revokePin")}</p>
            </div>
          ) : (
            <ConfirmButton
              warning={t("decrees.revokeWarning")}
              confirmLabel={t("decrees.revokeConfirm")}
              onConfirm={async () => {
                await revoke();
                await onDone();
              }}
            >
              {t("decrees.revoke")}
            </ConfirmButton>
          )}
        </div>
      ) : null}
    </article>
  );
}

// the validity is a span from now, so no time zone of the browser enters the decree
const VALIDITY_HOURS = { permanent: 0, "1h": 1, "1d": 24, "7d": 168, "30d": 720 } as const;
const FIELDS = ["files", "commands", "tools", "domains"] as const;
const TARGETS = ["class", "character", "table", "workspace"] as const;

/** The form of a new decree: a permanent rule or a one-time exception. */
function DecreeForm({ onClose, onMade }: { onClose: () => void; onMade: () => Promise<void> }) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const say = t as unknown as Say;
  const workspaces = useQuery(ruleWorkspacesQuery).data?.workspaces ?? [];
  const [workspace, setWorkspace] = useState("");
  const [kind, setKind] = useState<"rule" | "exception">("rule");
  const [effect, setEffect] = useState<"permission" | "behavior">("permission");
  const [targetKind, setTargetKind] = useState<(typeof TARGETS)[number]>("class");
  const [targetName, setTargetName] = useState("");
  const [field, setField] = useState<(typeof FIELDS)[number]>("commands");
  const [side, setSide] = useState<"allow" | "deny">("allow");
  const [value, setValue] = useState("");
  const [command, setCommand] = useState("");
  const [instruction, setInstruction] = useState("");
  const [validity, setValidity] = useState<keyof typeof VALIDITY_HOURS>("permanent");
  const [text, setText] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [proposal, setProposal] = useState<DecreeProposal | null>(null);
  const ws = workspaces.includes(workspace) ? workspace : (workspaces[0] ?? "");

  if (proposal) {
    return (
      <ProposalCard proposal={proposal} onConfirmed={onMade} onCancel={() => setProposal(null)} />
    );
  }
  const exception = kind === "exception";
  const input: DecreeInput = {
    workspace: ws,
    kind,
    effect: exception ? "permission" : effect,
    target_kind: exception ? "workspace" : targetKind,
    target_name: exception || targetKind === "workspace" ? ws : targetName.trim(),
    field,
    side,
    value,
    command,
    instruction,
    until:
      !exception && validity !== "permanent"
        ? new Date(Date.now() + VALIDITY_HOURS[validity] * 3_600_000).toISOString()
        : undefined,
    text,
  };
  const ready =
    ws !== "" &&
    text.trim() !== "" &&
    (exception
      ? command.trim() !== ""
      : (targetKind === "workspace" || targetName.trim() !== "") &&
        (effect === "behavior" ? instruction.trim() !== "" : value.trim() !== ""));
  return (
    <form
      aria-label={t("decrees.form.title")}
      className="flex max-w-2xl flex-col gap-4 rounded-field border border-line bg-surface p-5"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(null);
        try {
          setProposal(await api<DecreeProposal>("POST", "/api/decrees", input));
        } catch (err) {
          setError(err);
        } finally {
          setBusy(false);
        }
      }}
    >
      <h2 className="text-xl">{t("decrees.form.title")}</h2>
      {workspaces.length > 1 ? (
        <NativeSelect
          label={t("workspace")}
          value={ws}
          onChange={setWorkspace}
          options={workspaces.map((w) => ({ value: w, label: w }))}
        />
      ) : null}
      <ChoiceGroup
        label={t("decrees.form.kind")}
        value={kind}
        onChange={(v) => setKind(v as "rule" | "exception")}
        options={[
          { value: "rule", label: t("decrees.kinds.rule"), hint: t("decrees.form.ruleHint") },
          {
            value: "exception",
            label: t("decrees.kinds.exception"),
            hint: t("decrees.form.exceptionHint"),
          },
        ]}
      />
      {exception ? (
        <Field
          label={t("decrees.form.command")}
          description={t("decrees.form.commandHint")}
          value={command}
          onChange={setCommand}
        />
      ) : (
        <>
          <ChoiceGroup
            label={t("decrees.form.effect")}
            value={effect}
            onChange={(v) => setEffect(v as "permission" | "behavior")}
            options={[
              { value: "permission", label: t("decrees.form.permission") },
              { value: "behavior", label: t("decrees.form.behavior") },
            ]}
          />
          <NativeSelect
            label={t("decrees.form.target")}
            value={targetKind}
            onChange={(v) => setTargetKind(v as (typeof TARGETS)[number])}
            options={TARGETS.map((k) => ({ value: k, label: say(`decrees.form.targets.${k}`) }))}
          />
          {targetKind !== "workspace" ? (
            <Field
              label={t("decrees.form.targetName")}
              value={targetName}
              onChange={setTargetName}
            />
          ) : null}
          {effect === "permission" ? (
            <>
              <NativeSelect
                label={t("decrees.form.side")}
                value={side}
                onChange={(v) => setSide(v as "allow" | "deny")}
                options={[
                  { value: "allow", label: t("decrees.form.allow") },
                  { value: "deny", label: t("decrees.form.deny") },
                ]}
              />
              <NativeSelect
                label={t("decrees.form.field")}
                value={field}
                onChange={(v) => setField(v as (typeof FIELDS)[number])}
                options={FIELDS.map((f) => ({ value: f, label: say(`decrees.form.fields.${f}`) }))}
              />
              <Field
                label={t("decrees.form.value")}
                description={t("decrees.form.valueHint")}
                value={value}
                onChange={setValue}
              />
            </>
          ) : (
            <Area
              label={t("decrees.form.instruction")}
              description={t("decrees.form.instructionHint")}
              value={instruction}
              onChange={setInstruction}
              rows={2}
            />
          )}
          <NativeSelect
            label={t("decrees.form.validity")}
            value={validity}
            onChange={(v) => setValidity(v as keyof typeof VALIDITY_HOURS)}
            options={Object.keys(VALIDITY_HOURS).map((k) => ({
              value: k,
              label: say(`decrees.form.validities.${k}`),
            }))}
          />
        </>
      )}
      <Area
        label={t("decrees.form.order")}
        description={t("decrees.form.orderHint")}
        value={text}
        onChange={setText}
      />
      {error ? <Alert>{errorText(te, error)}</Alert> : null}
      <div className="flex gap-3">
        <Button type="submit" isDisabled={busy || !ready}>
          {t("decrees.form.submit")}
        </Button>
        <Button variant="quiet" onPress={onClose}>
          {t("cancel")}
        </Button>
      </div>
    </form>
  );
}
