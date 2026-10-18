import type { Lang } from "../../../i18n";
import { demosEn, type DemoStrings } from "../../../i18n/demos.en";
import { demosEs } from "../../../i18n/demos.es";

const DEMO_STRINGS: Record<Lang, DemoStrings> = { en: demosEn, es: demosEs };

/** Demo strings for a language. */
export function demoStrings(lang: Lang): DemoStrings {
  return DEMO_STRINGS[lang];
}

export type { DemoStrings };
