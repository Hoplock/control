// CODE AND DATA (DESIGN.md §7): a code block and a JSON viewer. Both are
// data — policy source, a log body, a proxy's declared capabilities — so both
// render LTR, in mono, untranslated, on the sunken ground, and both copy.

import { useState, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import styles from "./Code.module.css";

function CopyButton({ text }: { text: string }) {
  const { t } = useI18n();
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className={styles.copy}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
          window.setTimeout(() => setDone(false), 1500);
        } catch {
          // The text is selectable; a refused clipboard loses nothing.
        }
      }}
    >
      {done ? t("component.copy.done") : t("component.copy.button")}
    </button>
  );
}

export function CodeBlock({ code, label }: { code: string; label: string }) {
  return (
    <figure className={styles.figure}>
      <figcaption className={styles.caption}>
        <span>{label}</span>
        <CopyButton text={code} />
      </figcaption>
      <pre className={styles.pre} dir="ltr" translate="no" tabIndex={0}>
        <code>{code}</code>
      </pre>
    </figure>
  );
}

type Json = null | boolean | number | string | Json[] | { [k: string]: Json };

function Scalar({ value }: { value: Json }) {
  const kind = value === null ? "null" : typeof value;
  return (
    <span className={styles.scalar} data-kind={kind}>
      {JSON.stringify(value)}
    </span>
  );
}

function Node({ name, value, depth }: { name: ReactNode; value: Json; depth: number }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(depth < 2);
  if (value === null || typeof value !== "object") {
    return (
      <li className={styles.row}>
        {name}
        <Scalar value={value} />
      </li>
    );
  }
  const entries: [string, Json][] = Array.isArray(value)
    ? value.map((v, i) => [String(i), v])
    : Object.entries(value);
  const brackets = Array.isArray(value) ? ["[", "]"] : ["{", "}"];
  return (
    <li className={styles.row}>
      <button
        type="button"
        className={styles.toggle}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        aria-label={open ? t("component.json.collapse") : t("component.json.expand")}
      >
        <svg viewBox="0 0 10 10" aria-hidden="true" focusable="false">
          <path d={open ? "M2 3.5 5 6.5 8 3.5" : "M3.5 2 6.5 5 3.5 8"} />
        </svg>
      </button>
      {name}
      <span className={styles.punct}>{brackets[0]}</span>
      {open ? (
        <ul className={styles.list}>
          {entries.map(([k, v]) => (
            <Node
              key={k}
              depth={depth + 1}
              value={v}
              name={Array.isArray(value) ? null : <span className={styles.key}>{JSON.stringify(k)}: </span>}
            />
          ))}
        </ul>
      ) : (
        <span className={styles.punct}>{t("component.json.collapsed", { count: entries.length })}</span>
      )}
      <span className={styles.punct}>{brackets[1]}</span>
    </li>
  );
}

/** A collapsible, copyable view of a JSON value. */
export function JsonViewer({ value, label }: { value: unknown; label: string }) {
  const text = JSON.stringify(value, null, 2);
  return (
    <figure className={styles.figure}>
      <figcaption className={styles.caption}>
        <span>{label}</span>
        <CopyButton text={text} />
      </figcaption>
      <ul className={[styles.list, styles.json].join(" ")} dir="ltr" translate="no">
        <Node name={null} value={value as Json} depth={0} />
      </ul>
    </figure>
  );
}
