import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useCan } from "../app/session";
import { useUiSocket } from "../app/socket";
import { actions, canvasQuery, canvasTopic } from "../canvas/api";
import { type CharacterStatus, characterStatus, missionLine } from "../canvas/status";
import type { CanvasCharacter, CanvasState, TerminalInfo } from "../gen/panel";
import { api } from "../lib/api";
import { enc, viewQuery } from "../workspace/queries";
import { pausedReason } from "../workspace/situation";

/** What the Mestre layer around a terminal knows; every field is optional, a missing one just does not show. */
export interface TerminalContext {
  character?: CanvasCharacter;
  status?: CharacterStatus;
  mission?: ReturnType<typeof missionLine>;
  /** ISO time of the last useful delivery of the table. */
  delivery?: string;
  workspace: string;
  table?: string;
  paused: boolean;
  pausedReason: string;
  /** Who edits the canvas may pin (the server decides again). */
  canPin: boolean;
  /** Approver or above may pause the table. */
  canPause: boolean;
  /** Pins or unpins the character (the server may refuse: it throws). */
  pin: (pinned: boolean) => Promise<void>;
  /** Pauses (or resumes) the table with the reason the user wrote (the server may refuse: it throws). */
  setPaused: (paused: boolean, reason: string) => Promise<void>;
}

/** The states of a character with no canvas to say more, in the words of the vocabulary. */
export function statusFromInfo(info: TerminalInfo, canDecide: boolean): CharacterStatus {
  const bare = { state: info.state, reason: "" } as CanvasCharacter;
  return characterStatus(
    bare,
    { characters: [], waits: [] },
    { noSignal: !info.signal, canDecide },
  );
}

/**
 * The mission, the step, the approval, the last delivery and the pause of the table of a character, from
 * the canvas and the view of the campaign (the same data the canvas shows), kept by their live events.
 * docs/painel/terminal-no-painel.md#o-que-já-existe-e-o-que-não.
 */
export function useTerminalContext(info: TerminalInfo | undefined): TerminalContext {
  const workspace = info?.workspace ?? "";
  const table = info?.table;
  const canDecide = useCan("decide_approvals");
  const canPause = useCan("pause_buttons");
  const qc = useQueryClient();
  const sock = useUiSocket();
  const on = Boolean(workspace && table);
  const canvas = useQuery({ ...canvasQuery(workspace, table ?? ""), enabled: on });
  const view = useQuery({ ...viewQuery(workspace), enabled: on });

  // the canvas of the table follows its own events: a snapshot opens the state, "changed" reads it again
  useEffect(() => {
    if (!sock || !on || !table) return;
    const topic = canvasTopic(workspace, table);
    const off = sock.listen({
      onEvent: (tp, ev, data) => {
        if (tp !== topic) return;
        if (ev === "snapshot")
          qc.setQueryData(canvasQuery(workspace, table).queryKey, data as CanvasState);
        else if (ev === "changed")
          void qc.invalidateQueries({ queryKey: canvasQuery(workspace, table).queryKey });
      },
    });
    sock.subscribe([topic]);
    return () => {
      off();
      sock.unsubscribe([topic]);
    };
  }, [sock, qc, on, workspace, table]);

  const state = canvas.data;
  const character = state?.characters.find((c) => c.id === info?.character);
  const block = view.data?.tables.find((b) => b.id === table);
  return {
    character,
    status:
      character && state
        ? characterStatus(character, state, { noSignal: info?.signal === false, canDecide })
        : undefined,
    mission: character && state ? missionLine(character, state) : null,
    delivery: block?.last_delivery,
    workspace,
    table,
    paused: block?.paused ?? false,
    pausedReason: block ? pausedReason(block) : "",
    canPin: Boolean(state?.can_edit),
    canPause: canPause && on,
    pin: async (pinned) => {
      if (!character || !table) return;
      await actions(workspace, table).pin(character.id, pinned);
      await qc.invalidateQueries({ queryKey: canvasQuery(workspace, table).queryKey });
    },
    setPaused: async (pause, reason) => {
      if (!table) return;
      const path = pause
        ? `/api/workspaces/${enc(workspace)}/tables/${enc(table)}/pause`
        : `/api/chain/${enc(workspace)}/tables/${enc(table)}/resume`;
      await api("POST", path, { reason });
      await qc.invalidateQueries({ queryKey: ["workspaces"] });
    },
  };
}
