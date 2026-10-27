import { useQuery } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { terminalQuery } from "../lib/queries";
import { TerminalPane, tableTerminalPath } from "../terminal/TerminalPane";

/**
 * The terminal of one character, on a page of its own (/terminal/<character>). A character that sits at a
 * table has its terminal as a tab of the table, so the page leads there; one without a table stays here.
 */
export function TerminalScreen({ character }: { character: string }) {
  const { t } = useTranslation("terminal");
  const router = useRouter();
  const { data } = useQuery(terminalQuery(character));
  const table = data?.table;
  const workspace = data?.workspace;
  useEffect(() => {
    // replace: the back button must not return to a page that leads on again
    if (table && workspace) router.history.replace(tableTerminalPath(workspace, table, character));
  }, [table, workspace, character, router]);
  return (
    <section>
      <h1 className="sr-only">{t("title", { name: data?.name ?? character })}</h1>
      <div className="mt-4">
        <TerminalPane character={character} />
      </div>
    </section>
  );
}
