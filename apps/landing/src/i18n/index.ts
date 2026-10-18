import { en, type Strings } from "./en";
import { es } from "./es";

export const LANGS = ["en", "es"] as const;
export type Lang = (typeof LANGS)[number];
export const DEFAULT_LANG: Lang = "en";

export const LANG_NAMES: Record<Lang, string> = { en: "English", es: "Español" };
export const OG_LOCALE: Record<Lang, string> = { en: "en_US", es: "es_ES" };

const dictionaries: Record<Lang, Strings> = { en, es };

/** Core landing strings for a language. */
export function t(lang: Lang): Strings {
  return dictionaries[lang];
}

/** Path of the home page in a language: "en" → "/", "es" → "/es/". */
export function homePath(lang: Lang): string {
  return lang === DEFAULT_LANG ? "/" : `/${lang}/`;
}

export type { Strings };
