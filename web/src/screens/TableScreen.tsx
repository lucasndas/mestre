import { lazy, Suspense } from "react";
import { useTranslation } from "react-i18next";
import { Term } from "../components/Term";

// React Flow, xterm.js and CodeMirror are most of the weight of the panel: the canvas loads when it is opened
const TableCanvas = lazy(() =>
  import("../canvas/TableCanvas").then((m) => ({ default: m.TableCanvas })),
);

/** The canvas of a table, on a page of its own (/workspaces/<workspace>/mesas/<mesa>). */
export function TableScreen({ workspace, table }: { workspace: string; table: string }) {
  const { t } = useTranslation("canvas");
  return (
    <section>
      <div className="mb-2 flex flex-wrap items-baseline gap-x-4">
        <h1 className="text-2xl">
          <Term name="Mesa" />: {table}
        </h1>
        <p className="text-sm text-muted">{t("workspace", { workspace })}</p>
      </div>
      <Suspense
        fallback={
          <p role="status" className="text-muted">
            {t("loading")}
          </p>
        }
      >
        <TableCanvas workspace={workspace} table={table} />
      </Suspense>
    </section>
  );
}
