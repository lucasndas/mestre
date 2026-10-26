// Every time the panel shows is in the Guild time zone (the one the Telegram
// side also uses: guild.timezone), written by Intl in the language of the user.
export function formatDateTime(iso: string, language: string, timeZone: string): string {
  return new Intl.DateTimeFormat(language, {
    dateStyle: "short",
    timeStyle: "short",
    timeZone,
  }).format(new Date(iso));
}

/** The hour of day (HH:MM) in the Guild time zone. */
export function formatTime(iso: string, language: string, timeZone: string): string {
  return new Intl.DateTimeFormat(language, { timeStyle: "short", timeZone }).format(new Date(iso));
}

export function formatNumber(n: number, language: string): string {
  return new Intl.NumberFormat(language).format(n);
}
