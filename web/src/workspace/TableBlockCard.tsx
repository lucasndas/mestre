import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useChatControl } from "../app/chatControl";
import { useFormatDateTime } from "../app/session";
import { Term } from "../components/Term";
import { Alert, Button } from "../components/ui";
import type { PauseTableRequest, PauseTableResponse, TableBlock } from "../gen/panel";
import { MaxPauseReason } from "../gen/panel";
import { api } from "../lib/api";
import { errorText } from "../lib/errors";
import { formatNumber } from "../lib/format";
import { engineText } from "../lib/messages";
import { enc } from "./queries";
import { SituationChip } from "./SituationChip";
import { CYCLE, type Situation } from "./situation";
import { useSituationLine } from "./useSituationLine";

type Say = (k: string, o?: object) => string;

const REPO = /^[\w.-]+\/[\w.-]+$/;
const github = (repo: string, path: string) =>
  REPO.test(repo) ? `https://github.com/${repo}/${path}` : "";

const BORDER: Record<string, string> = {
  accent: "border-2 border-accent",
  danger: "border-2 border-danger",
};

function ExternalLink({
  href,
  label,
  children,
}: {
  href: string;
  label: string;
  children: string;
}) {
  if (!href) return <span>{children}</span>;
  return (
    <a
      href={href}
      aria-label={label}
      target="_blank"
      rel="noreferrer noopener"
      className="nodrag inline-flex min-h-11 items-center text-accent underline"
    >
      {children}
    </a>
  );
}

/** The ladder of the life of a table: where it is among Aberta, Em trabalho, Em revisão, Encerrando and Encerrada. */
function Cycle({ s }: { s: Situation }) {
  const { t } = useTranslation("workspace");
  const say = t as unknown as Say;
  if (s.stage < 0) return null;
  const next = CYCLE[s.stage + 1];
  return (
    <div className="flex flex-col gap-1">
      <ol aria-label={t("life.cycle.label")} className="m-0 flex list-none items-center p-0">
        {CYCLE.map((c, i) => (
          <li
            key={c}
            aria-current={i === s.stage ? "step" : undefined}
            className="flex flex-1 items-center last:flex-none"
          >
            <i
              aria-hidden="true"
              className={`size-2.5 shrink-0 rounded-full border-2 ${
                i < s.stage
                  ? "border-ink bg-ink"
                  : i === s.stage
                    ? "border-accent bg-accent ring-4 ring-accent-wash"
                    : "border-line bg-surface"
              }`}
            />
            <span className="sr-only">{say(`life.cycle.${c}`)}</span>
            {i < CYCLE.length - 1 ? (
              <span
                aria-hidden="true"
                className={`mx-1 h-0.5 flex-1 ${i < s.stage ? "bg-ink" : "bg-line"}`}
              />
            ) : null}
          </li>
        ))}
      </ol>
      <p className="m-0 flex justify-between gap-2 text-sm text-muted">
        <span>{t("life.cycle.current", { stage: say(`life.cycle.${CYCLE[s.stage]}`) })}</span>
        <span>
          {next
            ? t("life.cycle.next", { stage: say(`life.cycle.${next}`).toLowerCase() })
            : t("life.cycle.end")}
        </span>
      </p>
    </div>
  );
}

/** What pausing a table asks: why, written by the user; the server refuses an empty reason too. */
export function PauseForm({
  workspace,
  block,
  onClose,
}: {
  workspace: string;
  block: TableBlock;
  onClose: () => void;
}) {
  const { t } = useTranslation("workspace");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const id = useId();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      const body: PauseTableRequest = { reason: reason.trim() };
      await api<PauseTableResponse>(
        "POST",
        `/api/workspaces/${enc(workspace)}/tables/${enc(block.id)}/pause`,
        body,
      );
      await qc.invalidateQueries({ queryKey: ["workspaces"] });
      onClose();
    } catch (err) {
      setError(errorText(te, err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form
      className="nodrag flex flex-col gap-2 rounded-field border border-line bg-paper p-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (reason.trim()) void submit();
      }}
    >
      <label htmlFor={id} className="font-semibold">
        {t("life.card.pauseReason")}
      </label>
      <textarea
        id={id}
        value={reason}
        maxLength={MaxPauseReason}
        placeholder={t("life.card.pausePlaceholder")}
        onChange={(e) => setReason(e.target.value)}
        className="min-h-16 w-full rounded-field border border-line bg-surface p-2"
      />
      <p className="m-0 text-sm text-muted">{t("life.card.pauseHelp")}</p>
      {error ? <Alert>{error}</Alert> : null}
      <div className="flex flex-wrap gap-2">
        <Button type="submit" isDisabled={busy || reason.trim().length === 0}>
          {t("block.pause")}
        </Button>
        <Button variant="secondary" onPress={onClose}>
          {t("life.card.pauseCancel")}
        </Button>
      </div>
    </form>
  );
}

/**
 * A table of the campaign as its card shows it (docs/painel/vida-da-mesa.md): the situation in the
 * words of the vocabulary of states, one sentence, the cycle, what the situation asks of the user and,
 * folded, the technical details. `clickable` makes the whole card open the table (the list; the canvas
 * handles the click of its nodes).
 */
export function TableBlockCard({
  block,
  workspace,
  repo,
  canPause,
  situation,
  newNotices = 0,
  clickable = false,
}: {
  block: TableBlock;
  workspace: string;
  repo: string;
  canPause: boolean;
  situation: Situation;
  newNotices?: number;
  clickable?: boolean;
}) {
  const { t, i18n } = useTranslation("workspace");
  const fmt = useFormatDateTime();
  const navigate = useNavigate();
  const chat = useChatControl();
  const line = useSituationLine();
  const [pausing, setPausing] = useState(false);
  const num = (n: number) => formatNumber(Math.round(n * 100) / 100, i18n.language);
  const s = situation;
  const ended = s.key === "delivered" || s.key === "closed" || s.key === "cancelled";
  const title = block.epic_title || t("block.untitled", { table: block.id });
  const c = block.criteria;
  const u = block.usage;
  const failure = block.waits.find((w) => w.kind === "same_failure");
  const sub =
    s.key === "working" && block.party_size > 0
      ? t("block.partySize", { count: block.party_size }) +
        ` (${block.party.map((m) => t("block.machine", { name: m.machine_name, count: m.characters })).join(", ")})`
      : s.key === "stuck" || s.key === "stalled"
        ? (block.quest?.name ?? "")
        : "";
  const open = (
    <Link
      to="/workspaces/$workspace/mesas/$table"
      params={{ workspace, table: block.id }}
      aria-label={t("life.card.openNamed", { table: block.id })}
      className="nodrag inline-flex min-h-11 items-center justify-center rounded-field border border-line bg-surface px-4 font-semibold text-accent hover:bg-accent-wash"
    >
      {t("life.card.openTable")}
    </Link>
  );
  return (
    // biome-ignore lint/a11y/useKeyWithClickEvents: the title link and the button open the table for the keyboard; the click is a convenience
    <article
      aria-label={t("block.label", { table: block.id })}
      onClick={
        clickable
          ? (e) => {
              if ((e.target as HTMLElement).closest("a, button, summary, textarea, form")) return;
              void navigate({
                to: "/workspaces/$workspace/mesas/$table",
                params: { workspace, table: block.id },
              });
            }
          : undefined
      }
      className={`flex w-full flex-col gap-3 rounded-field border border-line bg-surface p-3 shadow-card ${
        BORDER[s.tone] ?? ""
      } ${clickable ? "cursor-pointer" : ""} ${ended ? "opacity-75" : ""}`}
    >
      <header className="flex flex-col gap-2">
        <p className="m-0 flex flex-wrap items-center gap-2">
          <SituationChip s={s} />
          {newNotices > 0 ? (
            <span className="inline-flex items-center gap-1 text-sm font-semibold text-accent">
              <span aria-hidden="true" className="size-2 rounded-full bg-accent" />
              {t("life.card.newNotice", { count: newNotices })}
            </span>
          ) : null}
        </p>
        <h3 className="m-0 text-lg leading-snug">
          <Link
            to="/workspaces/$workspace/mesas/$table"
            params={{ workspace, table: block.id }}
            aria-label={t("block.open", { table: block.id })}
            className="nodrag text-ink hover:underline"
          >
            {title}
          </Link>{" "}
          {block.epic > 0 ? (
            <ExternalLink
              href={github(repo, `issues/${block.epic}`)}
              label={t("block.issue", { epic: block.epic })}
            >
              {`#${block.epic}`}
            </ExternalLink>
          ) : null}
        </h3>
      </header>

      <p className="m-0">
        {line(block, s)}
        {sub ? <span className="block text-sm text-muted">{sub}</span> : null}
      </p>

      {block.issue_warning ? (
        <p
          role="note"
          className="m-0 rounded-field bg-warn px-2 py-1 text-sm font-semibold text-warn-ink"
        >
          ⚠ {t("block.noIssue", { reason: engineText(i18n.language, block.issue_warning) })}
        </p>
      ) : null}

      <Cycle s={s} />

      <div className="flex flex-wrap gap-2">
        {s.key === "approval" && s.urgent ? (
          <Link
            to="/aprovacoes"
            search={{ mesa: block.id, campanha: workspace }}
            className="nodrag inline-flex min-h-11 items-center justify-center rounded-field bg-accent px-4 font-semibold text-accent-ink hover:bg-accent-strong"
          >
            {t("life.card.viewRequest")}
          </Link>
        ) : null}
        {s.key === "delivered" && block.pr > 0 && github(repo, `pull/${block.pr}`) ? (
          <a
            href={github(repo, `pull/${block.pr}`)}
            target="_blank"
            rel="noreferrer noopener"
            className="nodrag inline-flex min-h-11 items-center justify-center rounded-field border border-line bg-surface px-4 font-semibold text-accent hover:bg-accent-wash"
          >
            {t("life.card.viewPr", { pr: block.pr })}
          </a>
        ) : null}
        {open}
        {s.key === "stuck" ? (
          <Button variant="secondary" className="nodrag" onPress={chat.show}>
            {t("life.card.talk")}
          </Button>
        ) : null}
        {s.key === "stalled" && canPause && !pausing ? (
          <Button
            className="nodrag"
            aria-label={t("block.pauseNamed", { table: block.id })}
            onPress={() => setPausing(true)}
          >
            {t("block.pause")}
          </Button>
        ) : null}
      </div>
      {pausing ? (
        <PauseForm workspace={workspace} block={block} onClose={() => setPausing(false)} />
      ) : null}

      <details className="nodrag text-sm">
        <summary className="flex min-h-11 cursor-pointer items-center text-muted">
          {t("life.card.tech")}
        </summary>
        <dl className="m-0 mb-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
          {failure?.fingerprint ? (
            <>
              <dt className="text-muted">{t("life.card.fingerprint")}</dt>
              <dd className="m-0 min-w-0 break-all">
                <code>{failure.fingerprint}</code>
              </dd>
            </>
          ) : null}
          {failure?.where ? (
            <>
              <dt className="text-muted">{t("life.card.where")}</dt>
              <dd className="m-0 min-w-0 break-all">
                <code>{failure.where}</code>
              </dd>
            </>
          ) : null}
          <dt className="text-muted">{t("block.branch")}</dt>
          <dd className="m-0 min-w-0 break-all">
            <code>{block.branch}</code>
          </dd>
          <dt className="text-muted">
            <Term name="Companhia" />
          </dt>
          <dd className="m-0 min-w-0">
            {block.party_size > 0
              ? `${t("block.partySize", { count: block.party_size })} · ${block.party
                  .map((m) => t("block.machine", { name: m.machine_name, count: m.characters }))
                  .join(", ")}`
              : t("block.partyNone")}
          </dd>
          {c ? (
            <dd className="col-span-2 m-0">
              {t(c.verified === 1 ? "block.criteriaOne" : "block.criteriaMany", {
                proven: c.proven,
                total: c.total,
                verified: c.verified,
              })}
            </dd>
          ) : null}
          <dt className="text-muted">{t("block.lastDelivery")}</dt>
          <dd className="m-0">
            {block.last_delivery ? (
              <time dateTime={block.last_delivery}>{fmt(block.last_delivery)}</time>
            ) : (
              t("block.noDelivery")
            )}
          </dd>
          {block.pr > 0 ? (
            <>
              <dt className="text-muted">PR</dt>
              <dd className="m-0">
                <ExternalLink
                  href={github(repo, `pull/${block.pr}`)}
                  label={t("block.prNamed", { pr: block.pr })}
                >
                  {t("block.pr", { pr: block.pr })}
                </ExternalLink>
              </dd>
            </>
          ) : null}
          {u ? (
            <>
              <dt className="text-muted">{t("block.usage")}</dt>
              <dd className="m-0">
                {u.has_data
                  ? t("block.usageOf", {
                      used: num(u.used),
                      limit: num(u.limit),
                      unit: (t as unknown as Say)(`block.unit.${u.unit}`, { defaultValue: u.unit }),
                      percent: Math.round(u.percent),
                    })
                  : t("block.usageGap")}
                {u.has_data ? (
                  <meter
                    aria-label={t("block.usageLabel", { table: block.id })}
                    min={0}
                    max={100}
                    value={Math.min(100, u.percent)}
                    low={80}
                    high={90}
                    optimum={0}
                    className="mt-1 block h-2 w-full"
                  />
                ) : null}
              </dd>
            </>
          ) : null}
        </dl>
      </details>
    </article>
  );
}
