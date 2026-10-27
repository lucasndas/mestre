import { useTranslation } from "react-i18next";
import type { TableBlock } from "../gen/panel";
import type { Situation } from "./situation";

type Say = (k: string, o?: object) => string;

/** The sentence of the card for a situation, from the catalog (the engine's own words only where it says why). */
export function useSituationLine(): (b: TableBlock, s: Situation) => string {
  const { t } = useTranslation("workspace");
  const say = t as unknown as Say;
  return (b, s) => {
    switch (s.key) {
      case "delivered":
        return say(
          s.params.closing
            ? "life.situation.line.deliveredClosing"
            : "life.situation.line.delivered",
          s.params,
        );
      case "cancelled":
        return say(
          s.params.cancelling ? "life.situation.line.cancelling" : "life.situation.line.cancelled",
        );
      case "paused":
        return s.params.reason
          ? say("life.situation.line.pausedWhy", {
              reason: `${String(s.params.reason).charAt(0).toUpperCase()}${String(s.params.reason).slice(1)}`,
            })
          : say("life.situation.line.paused");
      case "waiting":
        return say(`life.situation.line.${String(s.params.kind)}`, s.params);
      case "stalled":
        return say("life.situation.line.stalled", {
          time: say(`duration.${String(s.params.unit)}`, { count: s.params.count }),
        });
      case "working": {
        if (!b.quest) return say("life.situation.line.working");
        return b.quest.step
          ? `${b.quest.name} · ${say("block.step", { step: b.quest.step })}`
          : b.quest.name;
      }
      default:
        return say(`life.situation.line.${s.key}`, s.params);
    }
  };
}
