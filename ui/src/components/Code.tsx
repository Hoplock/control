// CODE AND DATA (DESIGN.md §7): a code block, a source view and a JSON
// viewer. Each is data — policy source, a log body, a proxy's declared
// capabilities — so each renders LTR, in mono, untranslated, and each copies.
// The source view numbers its lines and sets what is said about a line (a
// compiler's rejection, 0053) directly beneath it, in the operator's words.

import { Fragment, useState, type ReactNode } from "react";
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

/** Something said about one line of a source: 0 is the document as a whole. */
export interface SourceNote {
  line: number;
  key: string;
  /** The status tone of what is said, which marks the line (DESIGN.md §6). */
  tone: "deny" | "warn" | "info";
  content: ReactNode;
}

const TONE_RANK = { deny: 0, warn: 1, info: 2 } as const;

/** The id a source view gives line `n`, so a list beside it can go there. */
export function sourceLineId(prefix: string, n: number): string {
  return `${prefix}-L${n}`;
}

/**
 * A source with its lines numbered, and each note set beneath the line it is
 * about. The gutter follows the page's direction and each line's text is LTR
 * (DESIGN.md §9); a note is the operator's language, so it follows the page. A line with a note is marked, and a note about the whole document
 * (line 0, or past the end) heads the view.
 */
export function SourceView({
  source,
  label,
  notes = [],
  idPrefix,
}: {
  source: string;
  label: string;
  notes?: readonly SourceNote[];
  /** Prefixes each line's id (sourceLineId). */
  idPrefix: string;
}) {
  const lines = source.split("\n");
  if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
  const byLine = new Map<number, SourceNote[]>();
  const head: SourceNote[] = [];
  for (const n of notes) {
    if (n.line >= 1 && n.line <= lines.length) byLine.set(n.line, [...(byLine.get(n.line) ?? []), n]);
    else head.push(n);
  }
  return (
    <figure className={styles.figure} data-source-view="">
      <figcaption className={styles.caption}>
        <span>{label}</span>
        <CopyButton text={source} />
      </figcaption>
      {head.length ? (
        <div className={styles.headNotes}>
          {head.map((n) => (
            <Fragment key={n.key}>{n.content}</Fragment>
          ))}
        </div>
      ) : null}
      <div className={styles.sourceScroll}>
        <table className={styles.source}>
          <caption className={styles.hidden}>{label}</caption>
          <tbody>
            {lines.map((text, i) => {
              const n = i + 1;
              const here = byLine.get(n);
              const worst = here?.reduce((a, b) => (TONE_RANK[b.tone] < TONE_RANK[a.tone] ? b : a)).tone;
              return (
                <Fragment key={n}>
                  <tr id={sourceLineId(idPrefix, n)} data-line={n} data-noted={worst} tabIndex={-1}>
                    <th scope="row" className={styles.lineNo} translate="no">
                      {n}
                    </th>
                    <td className={styles.lineText} dir="ltr" translate="no">
                      {text}
                    </td>
                  </tr>
                  {here ? (
                    <tr className={styles.noteRow} data-note-line={n}>
                      <td />
                      <td className={styles.notes}>
                        {here.map((x) => (
                          <Fragment key={x.key}>{x.content}</Fragment>
                        ))}
                      </td>
                    </tr>
                  ) : null}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
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
