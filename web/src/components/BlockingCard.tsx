import { useTranslation } from "react-i18next";
import type { BlockingView } from "../gen/panel";
import { useGoto } from "../lib/links";
import { useFormatTime } from "../lib/time";
import { AgentBlock, useDynamicT } from "./ApprovalCard";
import { Button } from "./ui";

/**
 * A notice that a table stopped: the fields of the engine, the whole evidence (already
 * masked by the server), the steps, and the text of the agent in its own block. A step
 * the panel can do is a button only for who has the level of the step.
 */
export function BlockingCard({ item, highlighted }: { item: BlockingView; highlighted?: boolean }) {
  const t = useDynamicT("approvals");
  const { t: tt } = useTranslation("approvals");
  const fmt = useFormatTime();
  const goto = useGoto();
  const motive = t(`blocking.motives.${item.motive}`);
  return (
    <article
      id={`blocking-${item.id}`}
      aria-label={t("blocking.title")}
      className={`flex flex-col gap-3 rounded-field border bg-surface p-4 ${highlighted ? "border-accent ring-2 ring-accent/30" : "border-line"}`}
    >
      <header className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h3 className="text-xl">{item.stopped || motive}</h3>
        <span className="rounded-full bg-accent-wash px-2 py-0.5 text-sm font-semibold">
          {tt("blocking.title")}
        </span>
        <span className="text-sm text-muted">{motive}</span>
        <span className="text-sm text-muted">
          {item.table
            ? tt("blocking.place", { workspace: item.workspace, table: item.table })
            : item.workspace}
        </span>
        <span className="text-sm text-muted">
          {tt("blocking.raised", { time: fmt(item.created_at) })}
        </span>
      </header>
      <div>
        <h4 className="font-display text-lg">{tt("blocking.evidence")}</h4>
        {item.evidence ? (
          <pre className="mt-1 max-h-96 overflow-auto rounded-field border border-line bg-paper p-3 text-sm break-words whitespace-pre-wrap">
            {item.evidence}
          </pre>
        ) : (
          <p className="text-muted">{tt("blocking.noEvidence")}</p>
        )}
      </div>
      {item.steps.length > 0 ? (
        <div>
          <h4 className="font-display text-lg">{tt("blocking.unblock")}</h4>
          <ul className="mt-1 flex flex-col gap-2">
            {item.steps.map((s) => (
              <li key={s.text} className="flex flex-wrap items-center gap-3">
                <span aria-hidden="true">☐</span>
                <span>{s.text}</span>
                {s.can_do && s.action === "open_vault" ? (
                  <>
                    <Button variant="secondary" onPress={() => goto("/segredos")}>
                      {tt("blocking.vault")}
                    </Button>
                    <span className="text-sm text-muted">{tt("blocking.vaultHint")}</span>
                  </>
                ) : null}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {item.owner ? (
        <p className="font-semibold">{tt("blocking.owner", { name: item.owner })}</p>
      ) : null}
      <AgentBlock text={item.agent} />
    </article>
  );
}
