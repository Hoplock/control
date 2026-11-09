// THE TIME RANGE PICKER (DESIGN.md §7; 0051): absolute and relative.
//
// A range is one of three things, and it serialises into the screen's URL
// under /console/ as one of them (DESIGN.md §4), so a filtered view is a link:
//   - all time: nothing in the URL;
//   - relative, `range=24h`: the last 15 minutes, hour, day, week or 30 days,
//     resolved to an instant when the screen reads, never stored as one, so a
//     link pasted tomorrow still means "the last day";
//   - absolute, `from=…&to=…`: ISO-8601 UTC, the form an incident is
//     discussed in across zones (§9); from inclusive, to exclusive, as the
//     API reads them. The fields edit it in the operator's own zone.
// A relative range applies when chosen; an absolute one when applied, so a
// half-typed instant reads nothing. A start that is not before its end is
// refused at the field, and nothing is read.

import { useId, useState } from "react";
import type { MessageKey } from "../i18n/catalogue";
import { useI18n } from "../i18n/I18n";
import { fromLocalInput, isoBefore, localInput, parseInstant } from "../i18n/format";
import { Button } from "./Button";
import { Input, Select } from "./Field";
import styles from "./TimeRange.module.css";

export const RELATIVE = ["15m", "1h", "24h", "7d", "30d"] as const;
export type Relative = (typeof RELATIVE)[number];

export type TimeRange =
  | { kind: "all" }
  | { kind: "relative"; last: Relative }
  | { kind: "absolute"; from?: string; to?: string };

const MS: Record<Relative, number> = {
  "15m": 15 * 60_000,
  "1h": 3_600_000,
  "24h": 86_400_000,
  "7d": 7 * 86_400_000,
  "30d": 30 * 86_400_000,
};

const LABEL: Record<Relative, MessageKey> = {
  "15m": "component.timeRange.last15m",
  "1h": "component.timeRange.last1h",
  "24h": "component.timeRange.last24h",
  "7d": "component.timeRange.last7d",
  "30d": "component.timeRange.last30d",
};

/** The range a URL names: `range=` wins, then `from`/`to`, else all time. */
export function readRange(search: URLSearchParams): TimeRange {
  const r = search.get("range") as Relative | null;
  if (r && RELATIVE.includes(r)) return { kind: "relative", last: r };
  const from = parseInstant(search.get("from"));
  const to = parseInstant(search.get("to"));
  if (from || to) return { kind: "absolute", from, to };
  return { kind: "all" };
}

/** The query a range writes: every key it owns, set or cleared. */
export function rangeQuery(range: TimeRange): Record<string, string | undefined> {
  switch (range.kind) {
    case "all":
      return { range: undefined, from: undefined, to: undefined };
    case "relative":
      return { range: range.last, from: undefined, to: undefined };
    case "absolute":
      return { range: undefined, from: range.from, to: range.to };
  }
}

/** The window the API is asked for, resolved against `now` (from inclusive, to exclusive). */
export function resolveRange(range: TimeRange, now: number): { from?: string; to?: string } {
  switch (range.kind) {
    case "all":
      return {};
    case "relative":
      return { from: isoBefore(now, MS[range.last]) };
    case "absolute":
      return { from: range.from, to: range.to };
  }
}

export function TimeRangePicker({
  label,
  value,
  onChange,
  disabled = false,
}: {
  label: string;
  value: TimeRange;
  onChange(range: TimeRange): void;
  disabled?: boolean;
}) {
  const { t } = useI18n();
  const id = useId();
  const [custom, setCustom] = useState(value.kind === "absolute");
  const [from, setFrom] = useState(value.kind === "absolute" && value.from ? localInput(value.from) : "");
  const [to, setTo] = useState(value.kind === "absolute" && value.to ? localInput(value.to) : "");
  const [invalid, setInvalid] = useState(false);
  const mode = custom ? "custom" : value.kind === "relative" ? value.last : "all";

  const choose = (v: string) => {
    setInvalid(false);
    if (v === "custom") {
      setCustom(true);
      return;
    }
    setCustom(false);
    onChange(v === "all" ? { kind: "all" } : { kind: "relative", last: v as Relative });
  };
  const apply = () => {
    const f = from ? fromLocalInput(from) : undefined;
    const e = to ? fromLocalInput(to) : undefined;
    if (f && e && Date.parse(f) >= Date.parse(e)) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    onChange(f || e ? { kind: "absolute", from: f, to: e } : { kind: "all" });
  };

  return (
    <fieldset className={styles.picker} disabled={disabled}>
      <legend className={styles.legend}>{label}</legend>
      <div className={styles.row}>
        <Select
          id={`${id}-mode`}
          aria-label={label}
          value={mode}
          onChange={(e) => choose(e.target.value)}
        >
          <option value="all">{t("component.timeRange.all")}</option>
          {RELATIVE.map((r) => (
            <option key={r} value={r}>
              {t(LABEL[r])}
            </option>
          ))}
          <option value="custom">{t("component.timeRange.custom")}</option>
        </Select>
        {custom ? (
          <form
            className={styles.absolute}
            onSubmit={(e) => {
              e.preventDefault();
              apply();
            }}
          >
            <span className={styles.field}>
              <label className={styles.label} htmlFor={`${id}-from`}>
                {t("component.timeRange.from")}
              </label>
              <Input
                id={`${id}-from`}
                type="datetime-local"
                step="1"
                value={from}
                aria-invalid={invalid || undefined}
                aria-describedby={invalid ? `${id}-error` : undefined}
                onChange={(e) => setFrom(e.target.value)}
              />
            </span>
            <span className={styles.field}>
              <label className={styles.label} htmlFor={`${id}-to`}>
                {t("component.timeRange.to")}
              </label>
              <Input
                id={`${id}-to`}
                type="datetime-local"
                step="1"
                value={to}
                aria-invalid={invalid || undefined}
                onChange={(e) => setTo(e.target.value)}
              />
            </span>
            <Button type="submit">{t("component.timeRange.apply")}</Button>
          </form>
        ) : null}
      </div>
      {invalid ? (
        <p id={`${id}-error`} className={styles.error} role="alert">
          {t("component.timeRange.order")}
        </p>
      ) : null}
    </fieldset>
  );
}
