import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useCan, useMe } from "../app/session";
import { useUiSocket } from "../app/socket";
import { Alert } from "../components/ui";
import type { TerminalInfo } from "../gen/panel";
import { ApiError, api } from "../lib/api";
import { errorText } from "../lib/errors";
import { useGoto } from "../lib/links";
import { terminalQuery } from "../lib/queries";
import { socketFeed } from "../terminal/feed";
import { TerminalView } from "../terminal/TerminalView";
import { useTerminalContext } from "../terminal/useTerminalContext";

/** The terminal of one character, on a page of its own (/terminal/<character>). */
export function TerminalScreen({ character }: { character: string }) {
  const { t } = useTranslation("terminal");
  const { t: te } = useTranslation("errors");
  const { timezone } = useMe();
  const canType = useCan("type_in_terminal");
  const canDecide = useCan("decide_approvals");
  const goto = useGoto();
  const sock = useUiSocket();
  const feed = useMemo(() => (sock ? socketFeed(sock) : null), [sock]);
  const { data, error } = useQuery(terminalQuery(character));
  const context = useTerminalContext(data);
  const refresh = () =>
    api<TerminalInfo>("GET", `/api/characters/${encodeURIComponent(character)}/terminal`);
  return (
    <section>
      <h1 className="sr-only">{t("title", { name: data?.name ?? character })}</h1>
      <div className="mt-4">
        {error ? (
          <Alert>
            {error instanceof ApiError && error.code === "not_found"
              ? t("noTerminal")
              : errorText(te, error)}
          </Alert>
        ) : data && feed ? (
          <TerminalView
            terminalId={data.terminal_id}
            info={data}
            feed={feed}
            canType={canType}
            refreshInfo={refresh}
            timeZone={timezone}
            readable
            context={context}
            canDecide={canDecide}
            onDecide={() => goto("/aprovacoes")}
            onSeeTable={
              data.table
                ? () =>
                    goto(
                      `/workspaces/${encodeURIComponent(data.workspace)}/mesas/${encodeURIComponent(data.table ?? "")}`,
                    )
                : undefined
            }
          />
        ) : (
          <p role="status" className="text-muted">
            {t("loading")}
          </p>
        )}
      </div>
    </section>
  );
}
