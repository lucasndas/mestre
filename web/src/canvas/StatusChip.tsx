import { useTranslation } from "react-i18next";
import { ToneChip } from "../workspace/SituationChip";
import type { CharacterStatus } from "./status";

/** The word of a character's state (the vocabulary of the design) with its sign, and who it waits for. */
export function StatusChip({ status, dark = false }: { status: CharacterStatus; dark?: boolean }) {
  const { t } = useTranslation("canvas");
  const say = t as unknown as (k: string, o?: object) => string;
  return (
    <ToneChip tone={status.tone} icon={status.icon} large={!dark} dark={dark}>
      {say(`status.${status.key}`, { reason: status.reason })}
    </ToneChip>
  );
}
