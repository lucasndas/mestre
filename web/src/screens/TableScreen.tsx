import { useQuery } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { type CSSProperties, lazy, Suspense } from "react";
import { Button as AriaButton, Tab, TabList, TabPanel, Tabs } from "react-aria-components";
import { useTranslation } from "react-i18next";
import { useChatControl } from "../app/chatControl";
import { useCan, useFormatDateTime } from "../app/session";
import { canvasQuery } from "../canvas/api";
import { focusCharacter } from "../canvas/model";
import { Term } from "../components/Term";
import { Alert, Button } from "../components/ui";
import { TerminalPane, tableTerminalPath } from "../terminal/TerminalPane";
import { viewQuery } from "../workspace/queries";
import { SituationChip } from "../workspace/SituationChip";
import { situationOf } from "../workspace/situation";

// React Flow, xterm.js and CodeMirror are most of the weight of the panel: the canvas loads when it is opened
const TableCanvas = lazy(() =>
  import("../canvas/TableCanvas").then((m) => ({ default: m.TableCanvas })),
);

const tabClass =
  "min-h-11 cursor-pointer border-b-2 border-transparent px-4 text-lg outline-none data-hovered:bg-accent-wash data-selected:border-accent data-selected:font-semibold data-focus-visible:ring-2 data-focus-visible:ring-accent";

/** The terminal tab: one terminal at a time, with a choice among the characters of the table that have one. */
function TerminalTab({
  workspace,
  table,
  character,
}: {
  workspace: string;
  table: string;
  character?: string;
}) {
  const { t } = useTranslation("canvas");
  const router = useRouter();
  const { data: state, error } = useQuery(canvasQuery(workspace, table));
  if (error) return <Alert>{t("notFound")}</Alert>;
  if (!state)
    return (
      <p role="status" className="text-muted">
        {t("loading")}
      </p>
    );
  const withTerminal = state.characters.filter((c) => c.has_terminal);
  const wanted = withTerminal.find((c) => c.id === character);
  const focused = focusCharacter(state).replace(/^character:/, "");
  const chosen = wanted ?? withTerminal.find((c) => c.id === focused) ?? withTerminal[0];
  if (!chosen) return <p className="text-muted">{t("head.noTerminal")}</p>;
  return (
    // the header of the table is above the window: the window gives it that much of the height of the screen
    <div className="flex flex-col gap-3" style={{ "--term-offset": "470px" } as CSSProperties}>
      {withTerminal.length > 1 ? (
        <div role="toolbar" aria-label={t("head.pick")} className="flex flex-wrap gap-2">
          {withTerminal.map((c) => (
            <AriaButton
              key={c.id}
              aria-pressed={c.id === chosen.id}
              onPress={() => router.history.replace(tableTerminalPath(workspace, table, c.id))}
              className="min-h-11 rounded-field border border-line bg-surface px-3 font-semibold hover:bg-accent-wash aria-pressed:border-accent aria-pressed:bg-accent aria-pressed:text-accent-ink"
            >
              {c.name}
            </AriaButton>
          ))}
        </div>
      ) : null}
      <TerminalPane character={chosen.id} />
    </div>
  );
}

/** A table, on a page of its own (/workspaces/<workspace>/mesas/<mesa>): its state, and the board or the terminal. */
export function TableScreen({
  workspace,
  table,
  tab = "board",
  character,
}: {
  workspace: string;
  table: string;
  tab?: "board" | "terminal";
  character?: string;
}) {
  const { t } = useTranslation("canvas");
  const { t: tw } = useTranslation("workspace");
  const router = useRouter();
  const chat = useChatControl();
  const canDecide = useCan("decide_approvals");
  const fmt = useFormatDateTime();
  const { data: view } = useQuery({ ...viewQuery(workspace) });
  const block = view?.tables.find((b) => b.id === table);
  const base = `/workspaces/${encodeURIComponent(workspace)}/mesas/${encodeURIComponent(table)}`;
  return (
    <section>
      <header className="mb-3 flex flex-col gap-1">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <h1 className="m-0 text-2xl">{block?.epic_title || table}</h1>
          {block ? <SituationChip s={situationOf(block, canDecide)} /> : null}
          <span
            title={t("head.identifier")}
            className="inline-flex items-center gap-1.5 rounded-field border border-line px-2 text-base"
          >
            <Term name="Mesa" />
            <code>{table}</code>
          </span>
          <div className="flex-1" />
          <Button variant="secondary" onPress={chat.show}>
            {t("head.talk")}
          </Button>
        </div>
        <p className="m-0 flex flex-wrap items-center gap-x-4 text-base text-muted">
          <span>{t("workspace", { workspace })}</span>
          {block?.branch ? (
            <span>
              {t("head.branch")}: <code className="text-ink">{block.branch}</code>
            </span>
          ) : null}
          {block ? (
            <span>
              {t("head.lastDelivery")}:{" "}
              {block.last_delivery ? (
                <time dateTime={block.last_delivery} className="text-ink">
                  {fmt(block.last_delivery)}
                </time>
              ) : (
                tw("block.noDelivery")
              )}
            </span>
          ) : null}
        </p>
      </header>
      <Tabs
        selectedKey={tab}
        onSelectionChange={(k) =>
          router.history.push(
            k === "terminal" ? tableTerminalPath(workspace, table, character ?? "") : base,
          )
        }
      >
        <TabList aria-label={t("head.tabs")} className="mb-3 flex border-b border-line">
          <Tab id="board" className={tabClass}>
            {t("head.board")}
          </Tab>
          <Tab id="terminal" className={tabClass}>
            {t("head.terminal")}
          </Tab>
        </TabList>
        <TabPanel id="board">
          <Suspense
            fallback={
              <p role="status" className="text-muted">
                {t("loading")}
              </p>
            }
          >
            <TableCanvas workspace={workspace} table={table} />
          </Suspense>
        </TabPanel>
        <TabPanel id="terminal">
          <TerminalTab workspace={workspace} table={table} character={character} />
        </TabPanel>
      </Tabs>
    </section>
  );
}
