import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useChatControl } from "../../app/chatControl";
import { useFormatDateTime } from "../../app/session";
import { Alert, Button, Field } from "../../components/ui";
import type { ChainTable, QueueRow } from "../../gen/panel";
import { api } from "../../lib/api";
import { errorText } from "../../lib/errors";
import { Badge, EmptyState, LoadError, PageHeader, StatusStrip, VocabLine } from "./kit";
import { chainQuery, useDuration } from "./rulesKit";

type Say = (k: string, o?: object) => string;

export function Chain() {
  const { t } = useTranslation("rules");
  const chat = useChatControl();
  const qc = useQueryClient();
  const { data, error, refetch } = useQuery({ ...chainQuery, refetchInterval: 30_000 });
  const dur = useDuration();
  const [notice, setNotice] = useState<string | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });
  const tables = data?.workspaces.flatMap((g) => g.tables) ?? [];
  const paused = tables.filter((tb) => tb.pause).length;
  const open = [
    ...(data?.workspaces.flatMap((g) => g.queues) ?? []),
    ...tables.flatMap((tb) => tb.queues),
  ].filter((q) => q.open > 0);
  const waiting = open.reduce((n, q) => n + q.open, 0);
  const oldest = Math.max(0, ...open.map((q) => q.oldest_seconds));

  return (
    <section>
      <PageHeader title={t("chain.title")} intro={t("chain.intro")} />
      <VocabLine terms={["Cadeia de aprovação", "Pausa de autonomia", "Mestre da mesa"]} />
      {data ? (
        <StatusStrip
          tone={paused > 0 || waiting > 0 ? "warn" : "ok"}
          title={
            paused > 0
              ? t("chain.strip", { count: waiting, paused })
              : t("chain.stripQueue", { count: waiting })
          }
          hint={oldest > 0 ? t("chain.stripOldest", { age: dur(oldest) }) : t("chain.stripNone")}
        />
      ) : null}
      <div className="flex max-w-4xl flex-col gap-5">
        {notice ? <Alert tone="ok">{notice}</Alert> : null}
        {error ? (
          <LoadError what={t("loadWhat.chain")} error={error} onRetry={() => void refetch()} />
        ) : null}
        {!data && !error ? <p className="text-muted">{t("loading")}</p> : null}
        {data && data.workspaces.length === 0 ? (
          <EmptyState title={t("chain.emptyTitle")} why={t("noWorkspace")}>
            <Button onPress={chat.show}>{t("talk")}</Button>
          </EmptyState>
        ) : null}
        {data?.workspaces.map((g) => (
          <section
            key={g.workspace}
            aria-labelledby={`ch-${g.workspace}`}
            className="flex flex-col gap-3"
          >
            <h2 id={`ch-${g.workspace}`} className="text-2xl">
              {g.workspace}
            </h2>
            {g.queues.length > 0 ? <Queues rows={g.queues} /> : null}
            {g.tables.length === 0 ? (
              <p className="text-muted">
                {g.queues.some((q) => q.open > 0) ? t("chain.noTablesQueue") : t("chain.noTables")}
              </p>
            ) : null}
            <ul className="flex flex-col gap-3">
              {g.tables.map((tb) => (
                <li key={tb.table}>
                  <TableCard
                    workspace={g.workspace}
                    tb={tb}
                    canPause={data.can_pause}
                    onDone={async (text) => {
                      setNotice(text);
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

function Queues({ rows }: { rows: QueueRow[] }) {
  const { t } = useTranslation("rules");
  const say = t as unknown as Say;
  const dur = useDuration();
  const open = rows.filter((q) => q.open > 0);
  if (open.length === 0) return <p>{t("chain.queueEmpty")}</p>;
  return (
    <ul aria-label={t("chain.queues")} className="flex flex-col gap-1">
      {open.map((q) => (
        <li key={`${q.level}-${q.table ?? ""}`}>
          <span className="font-semibold">{say(`chain.levels.${q.level}`)}</span>:{" "}
          {t("chain.queueLine", {
            count: q.open,
            oldest: dur(q.oldest_seconds),
            avg: q.avg_wait_seconds > 0 ? dur(q.avg_wait_seconds) : "—",
          })}
        </li>
      ))}
    </ul>
  );
}

function TableCard({
  workspace,
  tb,
  canPause,
  onDone,
}: {
  workspace: string;
  tb: ChainTable;
  canPause: boolean;
  onDone: (notice: string) => Promise<void>;
}) {
  const { t } = useTranslation("rules");
  const { t: te } = useTranslation("errors");
  const fmt = useFormatDateTime();
  const [reason, setReason] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const paused = tb.pause;
  const act = async (verb: "pause" | "resume") => {
    setBusy(true);
    setError(null);
    try {
      await api(
        "POST",
        `/api/chain/${encodeURIComponent(workspace)}/tables/${encodeURIComponent(tb.table)}/${verb}`,
        { reason },
      );
      setReason("");
      await onDone(t(verb === "pause" ? "chain.paused" : "chain.resumed", { table: tb.table }));
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };
  return (
    <article className="rounded-field border border-line bg-surface p-4" aria-label={tb.table}>
      <p className="flex flex-wrap items-center gap-2 font-semibold">
        <span>{tb.table}</span>
        {paused ? (
          <Badge tone="warn">{t("chain.pausedBadge")}</Badge>
        ) : (
          <Badge tone="ok">{t("chain.activeBadge")}</Badge>
        )}
      </p>
      {paused ? (
        <p className="mt-2" role="status">
          {t("chain.pausedBy", {
            who:
              paused.by_name || (paused.kind === "character" ? t("chain.grandMaster") : paused.by),
            when: fmt(paused.since),
          })}
          <span className="block text-muted">{t("chain.reason", { reason: paused.reason })}</span>
        </p>
      ) : null}
      <div className="mt-3">
        {tb.queues.length > 0 ? (
          <Queues rows={tb.queues} />
        ) : (
          <p className="text-sm text-muted">{t("chain.emptyQueue")}</p>
        )}
      </div>
      {canPause ? (
        <div className="mt-4 flex max-w-xl flex-col gap-3">
          {paused && !tb.can_resume ? (
            <p className="text-sm text-muted">{t("chain.onlyGrandMaster")}</p>
          ) : (
            <>
              <Field
                label={t("chain.reasonLabel")}
                description={paused ? t("chain.resumeHint") : t("chain.pauseHint")}
                value={reason}
                onChange={setReason}
              />
              {error ? <Alert>{errorText(te, error)}</Alert> : null}
              <div>
                <Button
                  variant={paused ? "secondary" : "danger"}
                  isDisabled={busy || reason.trim() === ""}
                  onPress={() => void act(paused ? "resume" : "pause")}
                >
                  {paused ? t("chain.resume") : t("chain.pause")}
                </Button>
              </div>
            </>
          )}
        </div>
      ) : null}
    </article>
  );
}
