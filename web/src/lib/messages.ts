import { type MessageCode, messages } from "../gen/messages";

/**
 * The messages of the engine (a refusal, a notice, a card) are texts of the catalogs of the
 * Go side (internal/i18n/locales), generated into gen/messages.ts. The panel says them in
 * the language of the user from the code and the params of the engine, and never repeats
 * the text in a catalog of its own (docs/plataforma/mensagens-por-codigo.md).
 */

type Lang = keyof typeof messages;

/** The language of the generated catalogs for a language tag of the browser or of the user. */
export function messageLanguage(tag: string | undefined): Lang {
  return (tag ?? "").toLowerCase().startsWith("en") ? "en" : "pt-BR";
}

/** Whether the engine has a text for this code (the code of an area, "decree.scope_required"). */
export function isMessageCode(code: string): code is MessageCode {
  return (
    code in messages["pt-BR"] ||
    `${code}_one` in messages["pt-BR"] ||
    `${code}_other` in messages["pt-BR"]
  );
}

/** An instant as the engine says it: `25/10/2026 23:59 (America/Sao_Paulo, UTC-03:00)`, or `2026-10-25 23:59 (…)`. */
export function formatMoment(iso: string, lang: Lang, zone: string | undefined): string {
  const date = new Date(iso);
  const timeZone = zone || "UTC";
  const parts = new Intl.DateTimeFormat("en-GB", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
    timeZoneName: "longOffset",
  }).formatToParts(date);
  const p = (t: string) => parts.find((x) => x.type === t)?.value ?? "";
  const hm = `${p("hour")}:${p("minute")}`;
  const seconds =
    date.getUTCSeconds() === 0 && date.getUTCMilliseconds() === 0 ? "" : `:${p("second")}`;
  const day =
    lang === "en"
      ? `${p("year")}-${p("month")}-${p("day")}`
      : `${p("day")}/${p("month")}/${p("year")}`;
  const offset = p("timeZoneName").replace("GMT", "UTC").replace(/^UTC$/, "UTC+00:00");
  if (timeZone === "UTC") return `${day} ${hm}${seconds} UTC`;
  return `${day} ${hm}${seconds} (${timeZone}, ${offset})`;
}

type Params = Record<string, unknown>;

let guildZone: string | undefined;

/** The time zone of the Guild, where the instants of the messages of the engine are said. */
export function setGuildZone(zone: string | undefined) {
  guildZone = zone;
}

function isMsg(v: unknown): v is { code: string; params?: Params } {
  return typeof v === "object" && v !== null && typeof (v as { code?: unknown }).code === "string";
}

/** An instant as the engine writes it in JSON: `{"instant": "<RFC 3339>"}`. A text is never an instant. */
function isInstant(v: unknown): v is { instant: string } {
  return (
    typeof v === "object" && v !== null && typeof (v as { instant?: unknown }).instant === "string"
  );
}

function show(v: unknown, lang: Lang, zone: string | undefined): string {
  if (typeof v === "string") return v;
  if (isInstant(v)) return formatMoment(v.instant, lang, zone);
  if (isMsg(v)) return messageText(lang, v.code, v.params, zone) ?? v.code;
  if (Array.isArray(v)) return v.map((x) => show(x, lang, zone)).join(", ");
  if (v === null || v === undefined) return "";
  return String(v);
}

/** A message of the engine as the API carries it: the code and the params. */
export interface EngineMsg {
  code: string;
  params?: Params;
}

/** The text of a message the API carries (typed `any` by the generator) in a language; `fallback` when it has none or the code is unknown. */
export function engineText(language: string | undefined, msg: unknown, fallback = ""): string {
  if (!isMsg(msg)) return fallback;
  return messageText(language, msg.code, msg.params) ?? fallback;
}

/**
 * The text of a message of the engine in a language, from its code and params; undefined
 * when the engine has no such code (a client older than the engine). A `count` picks the
 * plural form, a param that is another message is said first, an instant comes out in
 * the zone of the Guild, and a `{{x}}` inside a text param stays as it is.
 */
export function messageText(
  language: string | undefined,
  code: string,
  params: Params = {},
  zone?: string,
): string | undefined {
  const lang = messageLanguage(language);
  zone ??= guildZone;
  const cat = messages[lang] as Record<string, string>;
  let text = cat[code];
  if (typeof params.count === "number") {
    const form = new Intl.PluralRules(lang).select(params.count);
    text = cat[`${code}_${form === "one" ? "one" : "other"}`] ?? text;
  }
  if (text === undefined) return undefined;
  return text.replace(/{{\s*(\w+)\s*}}/g, (whole, name: string) =>
    name in params ? show(params[name], lang, zone) : whole,
  );
}
