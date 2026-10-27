import { useQueries } from "@tanstack/react-query";
import { viewQuery } from "./queries";

/**
 * The human name of a table (the title of its epic) by campaign and table, read from the views the
 * panel already holds or fetches; `""` while it is not known, and then the caller says the identifier.
 * `enabled` false asks nothing.
 */
export function useTableNames(
  workspaces: string[],
  enabled: boolean,
): (ws: string, table: string) => string {
  const slugs = [...new Set(workspaces.filter(Boolean))].sort();
  const results = useQueries({
    queries: slugs.map((w) => ({ ...viewQuery(w), enabled })),
  });
  const names = new Map<string, string>();
  results.forEach((r, i) => {
    for (const b of r.data?.tables ?? []) {
      if (b.epic_title) names.set(`${slugs[i]}/${b.id}`, b.epic_title);
    }
  });
  return (ws, table) => names.get(`${ws}/${table}`) ?? "";
}
