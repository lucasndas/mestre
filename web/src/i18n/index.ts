import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { en } from "./locales/en";
import { ptBR } from "./locales/pt-BR";

export const LANGUAGES = ["pt-BR", "en"] as const;
export type Language = (typeof LANGUAGES)[number];

const STORAGE_KEY = "mestre.language";

export function isLanguage(v: unknown): v is Language {
  return LANGUAGES.includes(v as Language);
}

/** The language of the browser, for who has not chosen one (yet). */
export function browserLanguage(): Language {
  const stored = readStored();
  if (stored) return stored;
  const nav = typeof navigator === "undefined" ? "" : (navigator.language ?? "");
  return nav.toLowerCase().startsWith("en") ? "en" : "pt-BR";
}

function readStored(): Language | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return isLanguage(v) ? v : null;
  } catch {
    return null;
  }
}

/** Remembers the choice in this browser, for the screens before the login. */
export function storeLanguage(l: Language) {
  try {
    localStorage.setItem(STORAGE_KEY, l);
  } catch {
    // no storage: the choice lasts until the page is closed
  }
}

export const resources = { "pt-BR": ptBR, en } as const;

// Typed keys: t("auth.login.title") is checked against the pt-BR catalog.
declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "common";
    resources: typeof ptBR;
  }
}

export function setupI18n(language: Language = browserLanguage()) {
  const instance = i18n.createInstance();
  void instance.use(initReactI18next).init({
    resources: Object.fromEntries(Object.entries(resources).map(([lng, ns]) => [lng, ns])) as never,
    lng: language,
    fallbackLng: "pt-BR",
    defaultNS: "common",
    ns: Object.keys(ptBR),
    interpolation: { escapeValue: false },
    initAsync: false,
  });
  return instance;
}
