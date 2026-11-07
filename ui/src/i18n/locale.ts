// Locale resolution, in the browser and nowhere else (PLAN M21, DESIGN.md §9):
// the operator's stored preference, then navigator.languages, then the
// deployment default. The server negotiates no language.

/** The locales a catalogue ships for. en.json is the only one (M21). */
export const SHIPPED = ["en"] as const;
export type ShippedLocale = (typeof SHIPPED)[number];

/** The pseudolocales, which exist only as builds (DESIGN.md §10.7, §10.8). */
export const PSEUDO = ["en-XA", "en-XB"] as const;
export type PseudoLocale = (typeof PSEUDO)[number];

export type Locale = ShippedLocale | PseudoLocale;

/** The deployment default: the locale a page falls back to. */
export const DEFAULT_LOCALE: ShippedLocale = "en";

/**
 * Picks the first wanted locale a catalogue ships for, matching a tag
 * exactly and then by its language ("en-GB" takes "en").
 */
export function resolveLocale(
  stored: string | null | undefined,
  wanted: readonly string[],
  shipped: readonly string[] = SHIPPED,
): string {
  const candidates = [stored, ...wanted].filter((c): c is string => typeof c === "string" && c !== "");
  for (const c of candidates) {
    const exact = shipped.find((s) => s.toLowerCase() === c.toLowerCase());
    if (exact) return exact;
    const language = c.split("-")[0]?.toLowerCase();
    const near = shipped.find((s) => s.toLowerCase() === language);
    if (near) return near;
  }
  return DEFAULT_LOCALE;
}

// The scripts written right to left. Intl.Locale's text info is the
// authority where the browser has it; this list is the fallback.
const RTL_LANGUAGES = new Set(["ar", "dv", "fa", "he", "ks", "ku", "ps", "sd", "ug", "ur", "yi"]);

/** The text direction a locale is written in: from the locale, never hardcoded. */
export function directionOf(locale: string): "ltr" | "rtl" {
  if (locale === "en-XB") return "rtl";
  try {
    const info = (new Intl.Locale(locale) as Intl.Locale & { getTextInfo?: () => { direction?: string } })
      .getTextInfo?.();
    if (info?.direction === "rtl" || info?.direction === "ltr") return info.direction;
  } catch {
    // An unparseable tag falls through to the language list.
  }
  return RTL_LANGUAGES.has(locale.split("-")[0]?.toLowerCase() ?? "") ? "rtl" : "ltr";
}

/** The locale Intl formats with: a pseudolocale formats as English. */
export function intlLocale(locale: string): string {
  return (PSEUDO as readonly string[]).includes(locale) ? DEFAULT_LOCALE : locale;
}
