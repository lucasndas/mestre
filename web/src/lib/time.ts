import { useTranslation } from "react-i18next";
import { useMe } from "../app/session";

/** The hour of a moment, in the language of the user and the time zone of the Guild. */
export function formatTime(iso: string, language: string, timeZone: string): string {
  return new Intl.DateTimeFormat(language, { timeStyle: "short", timeZone }).format(new Date(iso));
}

/** The hour only ("14:32"), in the Guild time zone, never the one of the browser. */
export function useFormatTime(): (iso: string) => string {
  const { i18n } = useTranslation();
  const { timezone } = useMe();
  return (iso) => formatTime(iso, i18n.language, timezone);
}
