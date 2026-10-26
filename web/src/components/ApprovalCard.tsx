import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { useTranslation } from "react-i18next";
import type {
  ApprovalAnswer,
  ApprovalAnswered,
  ApprovalClosed,
  ApprovalDiffFile,
  ApprovalItem,
} from "../gen/panel";
import { api } from "../lib/api";
import { approvalDetailsQuery } from "../lib/approvalQueries";
import { errorText } from "../lib/errors";
import { useFormatTime } from "../lib/time";
import { Alert, Button, Field } from "./ui";

/** A text by a key that is built at run time (the name of an answer, a type, a state). */
export function useDynamicT(ns: "approvals") {
  const { t } = useTranslation(ns);
  return t as unknown as (key: string, opts?: object) => string;
}

/** "Já respondido por Bia: Aprovado às 14:32." and the other ways a request ends. */
export function ClosedBanner({ closed }: { closed: ApprovalClosed }) {
  const t = useDynamicT("approvals");
  const time = useFormatTime()(closed.at);
  let text: string;
  if (closed.state === "approved" || closed.state === "denied") {
    const verb = t(`closed.${closed.state}`);
    text = closed.self
      ? t("closed.self", { verb, time })
      : closed.by
        ? t("closed.by", { name: closed.by, verb, time })
        : t("closed.plain", { verb, time });
  } else {
    text = t(`closed.${closed.state}`, { time });
  }
  return (
    <p role="status" className="rounded-field bg-accent-wash px-3 py-2 font-semibold">
      {text}
    </p>
  );
}

/** The block of the text of agents: always marked, never styled as the engine's. */
export function AgentBlock({ text }: { text: string }) {
  const { t } = useTranslation("approvals");
  if (!text) return null;
  return (
    <section
      aria-label={t("item.agentTitle")}
      className="rounded-field border border-dashed border-muted bg-paper p-3"
    >
      <h4 className="font-display text-base font-semibold text-muted">{t("item.agentTitle")}</h4>
      <p className="mb-2 text-sm text-muted">{t("item.agentHint")}</p>
      <pre className="font-body text-[0.95em] break-words whitespace-pre-wrap">{text}</pre>
    </section>
  );
}

function Patch({ text }: { text: string }) {
  return (
    <pre className="max-h-96 overflow-auto rounded-field border border-line bg-paper p-2 text-sm leading-snug">
      {text.split("\n").map((line, i) => (
        <span
          // the lines of a patch have no identity of their own; their place is it
          // biome-ignore lint/suspicious/noArrayIndexKey: see above
          key={i}
          className={`block ${line.startsWith("+") ? "bg-accent-wash" : line.startsWith("-") ? "bg-danger-wash" : ""}`}
        >
          {line || " "}
        </span>
      ))}
    </pre>
  );
}

function DiffFile({ file }: { file: ApprovalDiffFile }) {
  const { t } = useTranslation("approvals");
  return (
    <details className="rounded-field border border-line bg-surface p-2">
      <summary className="min-h-11 cursor-pointer py-2 font-semibold">
        {t("details.file", { path: file.path, added: file.added, removed: file.removed })}
      </summary>
      {file.previous_path ? (
        <p className="text-sm text-muted">{t("details.renamed", { path: file.previous_path })}</p>
      ) : null}
      {file.patch_missing ? <p className="text-muted">{t("details.patchMissing")}</p> : null}
      {file.patch ? <Patch text={file.patch} /> : null}
    </details>
  );
}

/** "Ver detalhes": the whole question with the exact target and the whole diff of a PR. */
function Details({ id }: { id: number }) {
  const { t } = useTranslation("approvals");
  const { t: te } = useTranslation("errors");
  const { data, error } = useQuery(approvalDetailsQuery(id));
  if (error) return <Alert>{errorText(te, error)}</Alert>;
  if (!data) return <p className="text-muted">{t("details.loading")}</p>;
  return (
    <section aria-label={t("details.title", { id })} className="flex flex-col gap-3">
      <pre className="rounded-field border border-line bg-paper p-3 text-[0.95em] break-words whitespace-pre-wrap">
        {data.text}
      </pre>
      {data.diff_unavailable ? <Alert>{t("details.unavailable")}</Alert> : null}
      {data.files.length > 0 ? (
        <div className="flex flex-col gap-2">
          <h4 className="font-display text-lg">{t("details.diff")}</h4>
          {data.files.map((f) => (
            <DiffFile key={`${f.path}:${f.previous_path ?? ""}`} file={f} />
          ))}
        </div>
      ) : null}
    </section>
  );
}

const FIELD_LABEL: Record<string, string> = {
  duration: "fields.duration",
  pattern: "fields.pattern",
  text: "fields.instruction",
};

/**
 * The buttons of the answers this user is given. An answer that needs data shows the
 * field first; one that asks the PIN asks it after the button, in the very request
 * (docs/painel/login.md#pin-em-ações-sensíveis). The server checks it all again.
 */
function Answers({
  item,
  onSent,
  onProblem,
  onClosed,
}: {
  item: ApprovalItem;
  onSent: (text: string) => void;
  onProblem: (err: unknown) => void;
  onClosed: (closed: ApprovalClosed) => void;
}) {
  const t = useDynamicT("approvals");
  const { t: te } = useTranslation("errors");
  const { t: tc } = useTranslation("common");
  const qc = useQueryClient();
  const [choice, setChoice] = useState<ApprovalAnswer | null>(null);
  const [value, setValue] = useState("");
  const [pin, setPin] = useState("");
  const [emptyField, setEmptyField] = useState(false);

  const send = useMutation({
    mutationFn: (a: { answer: ApprovalAnswer; value: string; pin: string }) => {
      const body: Record<string, string> = { answer: a.answer.answer };
      if (a.answer.field === "duration") body.for = a.value.trim();
      if (a.answer.field === "pattern") body.pattern = a.value.trim();
      if (a.answer.field === "text") body.text = a.value.trim();
      return api<ApprovalAnswered>("POST", `/api/approvals/${item.id}/answer`, body, {
        pin: a.pin || undefined,
      });
    },
    onSuccess: async (r) => {
      onProblem(null);
      setChoice(null);
      onSent(r.state === "open" ? t("sentOpen") : t("sent", { state: t(`states.${r.state}`) }));
      await qc.invalidateQueries({ queryKey: ["approvals"] });
    },
    onError: async (err) => {
      // the PIN is spent whatever the answer was: it is typed again
      setPin("");
      onProblem(err);
      const code = (err as { code?: string }).code;
      if (
        code === "approval_closed" ||
        code === "approval_target_changed" ||
        code === "approval_expired"
      ) {
        setChoice(null);
        // the item stays on the screen and says how it ended
        try {
          const now = await api<ApprovalItem>("GET", `/api/approvals/${item.id}`);
          if (now.closed) onClosed(now.closed);
        } catch {
          // it is not there to read: the list refresh below takes it away
        }
        await qc.invalidateQueries({ queryKey: ["approvals"] });
      }
    },
  });

  const pick = (a: ApprovalAnswer) => {
    send.reset();
    onProblem(null);
    setPin("");
    setEmptyField(false);
    if (!a.field && !a.needs_pin) {
      setChoice(null);
      send.mutate({ answer: a, value: "", pin: "" });
      return;
    }
    setValue(a.field === "pattern" ? (a.suggested ?? "") : "");
    setChoice(a);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!choice) return;
    const needsValue =
      choice.field === "duration" || choice.field === "pattern" || choice.field === "text";
    if (needsValue && value.trim() === "") {
      setEmptyField(true);
      return;
    }
    send.mutate({ answer: choice, value, pin });
  };

  const scope = item.scope;
  const fieldKey = choice?.field ? FIELD_LABEL[choice.field] : undefined;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap gap-2">
        {item.answers.map((a) => (
          <Button
            key={a.answer}
            variant={
              a.answer.startsWith("deny")
                ? "secondary"
                : a.answer === "approve"
                  ? "primary"
                  : "secondary"
            }
            isDisabled={send.isPending}
            onPress={() => pick(a)}
          >
            {t(`answers.${a.answer}`)}
          </Button>
        ))}
      </div>
      {choice ? (
        <form
          aria-label={t("confirmNamed", { answer: t(`answers.${choice.answer}`) })}
          onSubmit={submit}
          className="flex max-w-xl flex-col gap-3 rounded-field border border-line bg-paper p-4"
        >
          {choice.field === "scope" && scope ? (
            <div>
              <p className="font-semibold">{t("fields.scope")}</p>
              <p className="text-muted">
                {t("fields.scopeText", {
                  type: t(`types.${scope.type}`),
                  workspace: scope.workspace,
                  table: scope.table || t("fields.scopeNoTable"),
                  days: scope.days,
                })}
              </p>
            </div>
          ) : null}
          {fieldKey ? (
            <Field
              label={choice.answer === "ask_back" ? t("fields.question") : t(fieldKey)}
              description={
                choice.field === "duration"
                  ? t("fields.durationHint")
                  : choice.field === "pattern"
                    ? choice.suggested
                      ? t("fields.patternHint")
                      : t("fields.patternNone")
                    : choice.answer === "ask_back"
                      ? t("fields.questionHint")
                      : t("fields.instructionHint")
              }
              autoFocus
              value={value}
              onChange={(v) => {
                setValue(v);
                setEmptyField(false);
              }}
              error={emptyField ? te("required") : undefined}
            />
          ) : null}
          {choice.needs_pin ? (
            <Field
              label={tc("pin.label")}
              description={tc("pin.hint")}
              type="password"
              inputMode="numeric"
              autoComplete="off"
              value={pin}
              onChange={setPin}
            />
          ) : null}
          <div className="flex gap-2">
            <Button
              type="submit"
              isDisabled={send.isPending || (choice.needs_pin && pin.length === 0)}
            >
              {t("confirm")}
            </Button>
            <Button variant="quiet" onPress={() => setChoice(null)}>
              {tc("actions.cancel")}
            </Button>
          </div>
        </form>
      ) : null}
    </div>
  );
}

/** A request of the central: the question as the Telegram has it, and the answers. */
export function ApprovalCard({
  item,
  highlighted,
  onClosed,
}: {
  item: ApprovalItem;
  highlighted?: boolean;
  onClosed?: (id: number, closed: ApprovalClosed) => void;
}) {
  const { t: te } = useTranslation("errors");
  const [problem, setProblem] = useState<unknown>(null);
  const t = useDynamicT("approvals");
  const fmt = useFormatTime();
  const [details, setDetails] = useState(false);
  const [sent, setSent] = useState<string | null>(null);
  const open = !item.closed;
  const place = item.workspace
    ? item.table
      ? t("item.place", { workspace: item.workspace, table: item.table })
      : t("item.placeNoTable", { workspace: item.workspace })
    : t("item.system");
  return (
    <article
      id={`request-${item.id}`}
      aria-label={t("item.title", { id: item.id })}
      className={`flex flex-col gap-3 rounded-field border bg-surface p-4 ${highlighted ? "border-accent ring-2 ring-accent/30" : "border-line"}`}
    >
      <header className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h3 className="text-xl">{t("item.title", { id: item.id })}</h3>
        <span className="rounded-full bg-accent-wash px-2 py-0.5 text-sm font-semibold">
          {t(`types.${item.type}`)}
        </span>
        <span className="text-sm text-muted">{t(`risk.${item.risk}`)}</span>
        <span className="text-sm text-muted">{place}</span>
        <span className="text-sm text-muted">
          {t("item.asked", { time: fmt(item.created_at) })}
        </span>
        {item.deadline && open ? (
          <span className="text-sm text-muted">
            {t("item.expires", { time: fmt(item.deadline) })}
          </span>
        ) : null}
      </header>
      {item.closed ? <ClosedBanner closed={item.closed} /> : null}
      <pre className="font-body break-words whitespace-pre-wrap">{item.text}</pre>
      <AgentBlock text={item.agent} />
      {item.members.length > 1 ? (
        <div>
          <p className="font-semibold">{t("item.similar", { count: item.members.length })}</p>
          <ul className="list-disc pl-6 text-muted">
            {item.members.map((m) => (
              <li key={m.id}>
                #{m.id}: {m.about}
              </li>
            ))}
          </ul>
          {open ? <p className="text-sm text-muted">{t("item.similarHint")}</p> : null}
        </div>
      ) : null}
      {item.approvals > 1 && open ? (
        <p className="font-semibold">
          {t("item.quorum", { counted: item.counted, total: item.approvals })}
        </p>
      ) : null}
      {open && item.voted ? <p className="text-muted">{t("item.voted")}</p> : null}
      {sent ? <Alert tone="ok">{sent}</Alert> : null}
      {problem ? <Alert>{errorText(te, problem)}</Alert> : null}
      {open && !item.voted && item.answers.length > 0 ? (
        <Answers
          item={item}
          onSent={setSent}
          onProblem={setProblem}
          onClosed={(c) => onClosed?.(item.id, c)}
        />
      ) : null}
      <div>
        <Button variant="quiet" aria-expanded={details} onPress={() => setDetails(!details)}>
          {details ? t("item.hideDetails") : t("item.details")}
        </Button>
      </div>
      {details ? <Details id={item.id} /> : null}
    </article>
  );
}
