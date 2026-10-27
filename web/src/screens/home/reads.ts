import { useQueries } from "@tanstack/react-query";
import type { TableBlock, WorkspaceRow } from "../../gen/panel";
import { isEnded } from "../../workspace/model";
import { viewQuery } from "../../workspace/queries";

/** A table of the Home with the campaign it belongs to. */
export interface HomeTable {
  workspace: string;
  block: TableBlock;
}

export interface HomeTables {
  /** Tables still going (not closed, not cancelled), of every campaign the user sees. */
  open: HomeTable[];
  /** Closed tables with a merged PR, the latest delivery first. */
  delivered: HomeTable[];
  /** Campaigns whose view could not be read. */
  failed: string[];
  loading: boolean;
}

const RECENT = 5;

/**
 * The tables of every active campaign. There is no list of tables across campaigns, so the Home
 * reads the `/view` of each (docs/painel/primeira-jornada.md#o-que-o-servidor-ainda-não-dá).
 * The key of a view starts with "workspaces", so the live events of that topic read it again.
 */
export function useHomeTables(list: WorkspaceRow[]): HomeTables {
  const active = list.filter((w) => w.state === "active");
  const results = useQueries({ queries: active.map((w) => viewQuery(w.slug)) });
  const open: HomeTable[] = [];
  const delivered: HomeTable[] = [];
  const failed: string[] = [];
  results.forEach((r, i) => {
    const workspace = active[i]?.slug ?? "";
    if (r.isError) failed.push(workspace);
    for (const block of r.data?.tables ?? []) {
      if (!isEnded(block)) open.push({ workspace, block });
      else if (block.state === "closed" && block.pr > 0) delivered.push({ workspace, block });
    }
  });
  delivered.sort((a, b) =>
    (b.block.last_delivery ?? "").localeCompare(a.block.last_delivery ?? ""),
  );
  return {
    open,
    delivered: delivered.slice(0, RECENT),
    failed,
    loading: results.some((r) => r.isPending),
  };
}
