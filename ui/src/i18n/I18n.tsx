// The locale a page renders in, for every component beneath it.

import { createContext, useContext, useMemo, type ReactNode } from "react";
import { catalogueFor, FORCED_LOCALE } from "./catalogue";
import { formatters, type Formatters } from "./format";
import { directionOf, resolveLocale, type Locale } from "./locale";
import { translator, type Translate } from "./messages";

export interface I18n {
  locale: Locale;
  dir: "ltr" | "rtl";
  t: Translate;
  f: Formatters;
}

const Context = createContext<I18n | null>(null);

/** The locale this page resolves to: forced in a pseudo build. */
export function pageLocale(stored: string | null): Locale {
  if (FORCED_LOCALE) return FORCED_LOCALE;
  const wanted = typeof navigator === "undefined" ? [] : navigator.languages;
  return resolveLocale(stored, wanted) as Locale;
}

export function makeI18n(locale: Locale, timeZone?: string): I18n {
  return { locale, dir: directionOf(locale), t: translator(locale, catalogueFor(locale)), f: formatters(locale, timeZone) };
}

export function I18nProvider({ locale, timeZone, children }: { locale: Locale; timeZone?: string; children: ReactNode }) {
  const value = useMemo(() => makeI18n(locale, timeZone), [locale, timeZone]);
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

export function useI18n(): I18n {
  const value = useContext(Context);
  if (!value) throw new Error("useI18n outside an I18nProvider");
  return value;
}
