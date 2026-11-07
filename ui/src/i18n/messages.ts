// Turning a key and its values into a sentence. Interpolation, plurals and
// ordinals belong to the ICU message, never to the caller (DESIGN.md §9): a
// component never concatenates catalogue text.

import { IntlMessageFormat } from "intl-messageformat";
import type { Catalogue, MessageKey } from "./catalogue";
import { intlLocale } from "./locale";

export type Values = Record<string, string | number | Date>;

export type Translate = (key: MessageKey, values?: Values) => string;

/** A translator over one catalogue, caching each compiled message. */
export function translator(locale: string, catalogue: Catalogue): Translate {
  const cache = new Map<MessageKey, IntlMessageFormat>();
  const formatLocale = intlLocale(locale);
  return (key, values) => {
    let format = cache.get(key);
    if (!format) {
      format = new IntlMessageFormat(catalogue[key], formatLocale);
      cache.set(key, format);
    }
    const out = format.format(values);
    return Array.isArray(out) ? out.join("") : String(out);
  };
}
