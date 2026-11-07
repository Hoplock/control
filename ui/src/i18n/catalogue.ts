// The catalogue the console renders with (PLAN M21, DESIGN.md §9).
//
// English is inlined in the bundle, so the first paint never waits on a
// fetch. A pseudolocale build (vite --mode en-XA | en-XB) carries the same
// catalogue, rewritten by ./pseudo at load; a production build carries none
// of that code, because import.meta.env.MODE is a constant the bundler folds.

import type { MessageFormatElement } from "@formatjs/icu-messageformat-parser";
import en from "../locales/en.json";
import { pseudoCatalogue } from "./pseudo";
import type { Locale } from "./locale";

/** A catalogue key. A key not in en.json is a type error. */
export type MessageKey = keyof typeof en;

export type Message = string | MessageFormatElement[];
export type Catalogue = Record<MessageKey, Message>;

export const english: Record<MessageKey, string> = en;

/** The locale a pseudolocale build is forced to, or null. */
export const FORCED_LOCALE: Locale | null =
  import.meta.env.MODE === "en-XA" ? "en-XA" : import.meta.env.MODE === "en-XB" ? "en-XB" : null;

/** The catalogue for a locale. Only English exists outside a pseudo build. */
export function catalogueFor(locale: Locale): Catalogue {
  if (FORCED_LOCALE !== null && locale === FORCED_LOCALE) {
    return pseudoCatalogue(english, FORCED_LOCALE as "en-XA" | "en-XB");
  }
  return english;
}
