// THE LOCALE HELPERS (PLAN M21, DESIGN.md §9). Every date, time, number and
// list the console shows is formatted here, through Intl, and nowhere else;
// the string lint (scripts/lint-strings.mjs) fails a formatting call outside
// src/i18n. Identifiers are never formatted: they are data (see Ident).

import { intlLocale } from "./locale";

export interface Formatters {
  /** An instant in the operator's own zone, for reading. */
  dateTime(at: Date | string): string;
  /** The same instant as ISO-8601 UTC: the form a person copies and pastes. */
  utc(at: Date | string): string;
  /** A count or a quantity, with the locale's grouping and decimals. */
  number(n: number): string;
  /** A list, joined as the locale joins one ("a, b and c"). */
  list(items: readonly string[], type?: "conjunction" | "disjunction" | "unit"): string;
}

function instant(at: Date | string): Date {
  return typeof at === "string" ? new Date(at) : at;
}

/** The ISO-8601 UTC form, whatever the locale: an incident crosses zones. */
export function isoUTC(at: Date | string): string {
  return instant(at).toISOString();
}

export function formatters(locale: string, timeZone?: string): Formatters {
  const tag = intlLocale(locale);
  const dateTime = new Intl.DateTimeFormat(tag, { dateStyle: "medium", timeStyle: "medium", timeZone });
  const number = new Intl.NumberFormat(tag);
  const lists = new Map<string, Intl.ListFormat>();
  return {
    dateTime: (at) => dateTime.format(instant(at)),
    utc: isoUTC,
    number: (n) => number.format(n),
    list: (items, type = "conjunction") => {
      let f = lists.get(type);
      if (!f) {
        f = new Intl.ListFormat(tag, { style: "long", type });
        lists.set(type, f);
      }
      return f.format(items);
    },
  };
}

/** An instant as a datetime-local field's value, in the operator's own zone. */
export function localInput(at: Date | string): string {
  const d = instant(at);
  const two = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}T${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`;
}

/** A datetime-local field's value, read in the operator's own zone, as ISO-8601 UTC; undefined if it is not one. */
export function fromLocalInput(value: string): string | undefined {
  const t = new Date(value).getTime();
  return Number.isNaN(t) ? undefined : isoUTC(new Date(t));
}

/** A parameter that should be an instant, as ISO-8601 UTC; undefined if it is not one. */
export function parseInstant(value: string | null | undefined): string | undefined {
  if (!value) return undefined;
  const t = Date.parse(value);
  return Number.isNaN(t) ? undefined : isoUTC(new Date(t));
}

/** The instant `ms` milliseconds before `now`, as ISO-8601 UTC. */
export function isoBefore(now: number, ms: number): string {
  return isoUTC(new Date(now - ms));
}
