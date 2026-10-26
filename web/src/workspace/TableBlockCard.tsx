import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useFormatDateTime } from "../app/session";
import { Alert, Button } from "../components/ui";
import type { PauseTableResponse, TableBlock, ViewWait } from "../gen/panel";
import { api } from "../lib/api";
import { errorText } from "../lib/errors";
import { formatNumber } from "../lib/format";
import { Badge } from "../screens/admin/kit";
import { quietParts } from "./model";
import { enc } from "./queries";

type Say = (k: string, o?: object) => string;

const REPO = /^[\w.-]+\/[\w.-]+$/;
const github = (repo: string, path: string) =>
  REPO.test(repo) ? `https://github.com/${repo}/${path}` : "";

const STATE_TONE: Record<string, "plain" | "ok" | "warn" | "danger"> = {
  open: "plain",
  working: "ok",
  in_review: "plain",
  closing: "warn",
  closed: "plain",
  cancelling: "warn",
  cancelled: "plain",
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
      className="nodrag text-accent underline"
    >
      {children}
    </a>
  );
}

function Wait({ w }: { w: ViewWait }) {
  const { t } = useTranslation("workspace");
  const say = t as unknown as Say;
  return (
    <li className="text-sm">
      {say(`block.wait.${w.kind}`, { reason: w.reason, defaultValue: w.reason })}
      {w.kind === "same_failure" && (w.fingerprint || w.where) ? (
        <span className="block text-muted">
          {w.fingerprint ? t("block.wait.fingerprint", { fingerprint: w.fingerprint }) : null}
          {w.fingerprint && w.where ? " · " : null}
          {w.where ? <code>{w.where}</code> : null}
        </span>
      ) : null}
    </li>
  );
}

/** What pausing a table by its block says: who may, and what the engine answered. */
function PauseButton({ workspace, block }: { workspace: string; block: TableBlock }) {
  const { t } = useTranslation("workspace");
  const { t: te } = useTranslation("errors");
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<{ ok: boolean; text: string } | null>(null);
  const press = async () => {
    setBusy(true);
    setNote(null);
    try {
      const r = await api<PauseTableResponse>(
        "POST",
        `/api/workspaces/${enc(workspace)}/tables/${enc(block.id)}/pause`,
      );
      setNote({ ok: true, text: r.changed ? t("block.paused_ok") : t("block.pausedAlready") });
      await qc.invalidateQueries({ queryKey: ["workspaces"] });
    } catch (err) {
      const reason = (err as { params?: { reason?: string } }).params?.reason ?? "";
      const known = reason ? (t as unknown as Say)(`refusal.${reason}`, { defaultValue: "" }) : "";
      setNote({ ok: false, text: known || errorText(te, err) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="nodrag mt-2 flex flex-col gap-2">
      <div>
        <Button
          variant="secondary"
          isDisabled={busy}
          aria-label={t("block.pauseNamed", { table: block.id })}
          onPress={() => void press()}
        >
          {t("block.pause")}
        </Button>
      </div>
      {note ? <Alert tone={note.ok ? "ok" : "error"}>{note.text}</Alert> : null}
    </div>
  );
}

/**
 * A table of the workspace as its block shows it (docs/painel/visao-do-workspace.md#bloco-da-mesa).
 * The title opens the canvas of the table; so does a click anywhere else on the block (the
 * canvas of the workspace handles it), while the links and buttons inside do their own thing.
 */
export function TableBlockCard({
  block,
  workspace,
  repo,
  canPause,
  ended = false,
}: {
  block: TableBlock;
  workspace: string;
  repo: string;
  canPause: boolean;
  ended?: boolean;
}) {
  const { t, i18n } = useTranslation("workspace");
  const say = t as unknown as Say;
  const fmt = useFormatDateTime();
  const num = (n: number) => formatNumber(Math.round(n * 100) / 100, i18n.language);
  const quiet = quietParts(block.quiet_seconds);
  const c = block.criteria;
  const u = block.usage;
  return (
    <article
      aria-label={t("block.label", { table: block.id })}
      className={`flex w-full cursor-pointer flex-col gap-2 rounded-field border border-line bg-surface p-3 shadow-card ${ended ? "opacity-70" : ""}`}
    >
      <header className="flex flex-col gap-1">
        <h3 className="m-0 text-lg leading-snug">
          <Link
            to="/workspaces/$workspace/mesas/$table"
            params={{ workspace, table: block.id }}
            aria-label={t("block.open", { table: block.id })}
            className="nodrag text-ink hover:underline"
          >
            {block.epic_title || t("block.untitled", { table: block.id })}
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
        <p className="m-0 flex flex-wrap items-center gap-2">
          <Badge tone={STATE_TONE[block.state] ?? "plain"}>
            {say(`block.state.${block.state}`, { defaultValue: block.state })}
          </Badge>
          {block.paused ? <Badge tone="warn">{t("block.paused")}</Badge> : null}
          {block.pr > 0 ? (
            <ExternalLink
              href={github(repo, `pull/${block.pr}`)}
              label={t("block.prNamed", { pr: block.pr })}
            >
              {t("block.pr", { pr: block.pr })}
            </ExternalLink>
          ) : null}
        </p>
      </header>

      {block.issue_warning ? (
        <p
          role="note"
          className="m-0 rounded-field bg-warn px-2 py-1 text-sm font-semibold text-warn-ink"
        >
          ⚠ {t("block.noIssue", { reason: block.issue_warning })}
        </p>
      ) : null}

      <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
        <dt className="text-muted">{t("block.branch")}</dt>
        <dd className="m-0 min-w-0 break-all">
          <code>{block.branch}</code>
        </dd>

        <dt className="text-muted">{t("block.quest")}</dt>
        <dd className="m-0 min-w-0">
          {block.quest ? (
            <>
              {block.quest.issue > 0 ? (
                <>
                  <ExternalLink
                    href={github(repo, `issues/${block.quest.issue}`)}
                    label={t("block.questIssue", { issue: block.quest.issue })}
                  >
                    {`#${block.quest.issue}`}
                  </ExternalLink>{" "}
                </>
              ) : null}
              {block.quest.name}
              {block.quest.step ? (
                <span className="text-muted"> · {t("block.step", { step: block.quest.step })}</span>
              ) : null}
            </>
          ) : (
            <span className="text-muted">{t("block.noQuest")}</span>
          )}
        </dd>

        <dt className="text-muted">{t("block.party")}</dt>
        <dd className="m-0 flex min-w-0 flex-wrap items-center gap-1">
          {block.party_size > 0 ? (
            <>
              <span>{t("block.partySize", { count: block.party_size })}</span>
              {block.party.map((m) => (
                <span
                  key={m.machine}
                  title={m.machine}
                  className="rounded-field border border-line px-1.5 text-xs"
                >
                  {t("block.machine", { name: m.machine_name, count: m.characters })}
                </span>
              ))}
            </>
          ) : (
            <span className="text-muted">{t("block.partyNone")}</span>
          )}
        </dd>
      </dl>

      {block.waits.length > 0 ? (
        <section aria-label={t("block.waits")}>
          <ul className="m-0 flex list-none flex-col gap-1 rounded-field bg-warn/30 p-2">
            {block.waits.map((w) => (
              <Wait key={`${w.kind}:${w.reason}`} w={w} />
            ))}
          </ul>
        </section>
      ) : null}

      {c ? (
        <p className="m-0 text-sm">
          {t(c.verified === 1 ? "block.criteriaOne" : "block.criteriaMany", {
            proven: c.proven,
            total: c.total,
            verified: c.verified,
          })}
        </p>
      ) : null}

      <p className="m-0 flex flex-wrap items-center gap-2 text-sm">
        <span className="text-muted">{t("block.lastDelivery")}:</span>
        {block.last_delivery ? (
          <time dateTime={block.last_delivery}>{fmt(block.last_delivery)}</time>
        ) : (
          <span className="text-muted">{t("block.noDelivery")}</span>
        )}
        {block.stalled ? (
          <Badge tone="warn">
            {t("block.stalled", { time: t(`duration.${quiet.unit}`, { count: quiet.count }) })}
          </Badge>
        ) : null}
      </p>
      {block.stalled && canPause ? <PauseButton workspace={workspace} block={block} /> : null}

      {u ? (
        <div className="text-sm">
          <p className="m-0">
            <span className="text-muted">{t("block.usage")}:</span>{" "}
            {u.has_data
              ? t("block.usageOf", {
                  used: num(u.used),
                  limit: num(u.limit),
                  unit: say(`block.unit.${u.unit}`, { defaultValue: u.unit }),
                  percent: Math.round(u.percent),
                })
              : t("block.usageGap")}
          </p>
          {u.has_data ? (
            <meter
              aria-label={t("block.usageLabel", { table: block.id })}
              min={0}
              max={100}
              value={Math.min(100, u.percent)}
              low={80}
              high={90}
              optimum={0}
              className="h-2 w-full"
            />
          ) : null}
        </div>
      ) : null}
    </article>
  );
}
