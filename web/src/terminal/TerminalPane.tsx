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
import { socketFeed } from "./feed";
import { TerminalView } from "./TerminalView";
import { useTerminalContext } from "./useTerminalContext";

/** The path of a table with its terminal tab open on one character. */
export function tableTerminalPath(workspace: string, table: string, character: string): string {
  return `/workspaces/${encodeURIComponent(workspace)}/mesas/${encodeURIComponent(table)}?aba=terminal&personagem=${encodeURIComponent(character)}`;
}

/** The terminal of one character, as the page of the terminal and the tab of a table show it. */
export function TerminalPane({ character }: { character: string }) {
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
  if (error)
    return (
      <Alert>
        {error instanceof ApiError && error.code === "not_found"
          ? t("noTerminal")
          : errorText(te, error)}
      </Alert>
    );
  if (!data || !feed)
    return (
      <p role="status" className="text-muted">
        {t("loading")}
      </p>
    );
  return (
    <TerminalView
      terminalId={data.terminal_id}
      info={data}
      feed={feed}
      canType={canType && data.can_type}
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
  );
}
