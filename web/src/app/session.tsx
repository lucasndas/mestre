import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { Me } from "../gen/panel";
import { isLanguage, type Language, storeLanguage } from "../i18n";
import { api } from "../lib/api";
import { formatDateTime } from "../lib/format";
import { meQuery } from "../lib/queries";

/** The user of the session. Only the screens behind the login call it. */
export function useMe(): Me {
  const { data } = useQuery(meQuery);
  if (!data) throw new Error("useMe outside a session");
  return data;
}

/** Whether the level may do something: the nav hides what it cannot; the server still decides. */
export function useCan(capability: string): boolean {
  return useMe().capabilities.includes(capability);
}

/** Dates in the language of the user and the time zone of the Guild. */
export function useFormatDateTime(): (iso: string) => string {
  const { i18n } = useTranslation();
  const { timezone } = useMe();
  return (iso) => formatDateTime(iso, i18n.language, timezone);
}

/** Changes the language: here at once, and on the Citadel when there is a session. */
export function useChangeLanguage(): (l: Language) => void {
  const { i18n } = useTranslation();
  const qc = useQueryClient();
  return (l) => {
    if (!isLanguage(l)) return;
    void i18n.changeLanguage(l);
    storeLanguage(l);
    if (qc.getQueryData(meQuery.queryKey)) {
      void api<Me>("PUT", "/api/me/language", { language: l }).then((me) =>
        qc.setQueryData(meQuery.queryKey, me),
      );
    }
  };
}
