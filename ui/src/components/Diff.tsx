// THE DIFF VIEW (DESIGN.md §7; 0053). A unified diff of two sources, read line
// by line: each line's number on both sides, its side, and its text, which is
// data — policy source is an identifier (§9), so it is LTR, mono and never
// translated. The two sides are pale grounds of their own (§3.8), not status
// hues, and each changed line says its side in a marker and in words, so the
// view reads the same in greyscale and aloud.
//
// The table follows the page's direction, so its gutter is at the line's
// start in either; each line's text is LTR whatever the page's (§9).
//
// It renders text the server diffed; it never diffs. Its states: the diff; no
// difference; too far apart to show line by line (the server's own bound,
// `text_omitted`); and loading, as DiffSkeleton. An error is the caller's
// ErrorMessage, where the read that failed is.

import { useI18n } from "../i18n/I18n";
import { Skeleton } from "./States";
import { VisuallyHidden } from "./VisuallyHidden";
import styles from "./Diff.module.css";

export type DiffLineKind = "hunk" | "context" | "add" | "remove";

export interface DiffLine {
  kind: DiffLineKind;
  /** The line's number in the old source, for a context or removed line. */
  old?: number;
  /** The line's number in the new source, for a context or added line. */
  new?: number;
  /** The line itself, without its marker; a hunk's header whole. */
  text: string;
}

const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

/**
 * Reads a unified diff into lines, numbering each from its hunk's header.
 * The server writes hunks alone, with no `---`/`+++` file header — so a
 * removed line that begins `-- ` (a YAML document marker, say) is a line,
 * never a header. A "\ No newline" note is dropped.
 */
export function parseUnified(text: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldAt = 0;
  let newAt = 0;
  const lines = text.split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  for (const raw of lines) {
    const hunk = HUNK.exec(raw);
    if (hunk) {
      oldAt = Number(hunk[1]);
      newAt = Number(hunk[3]);
      out.push({ kind: "hunk", text: raw });
      continue;
    }
    if (raw.startsWith("\\")) continue;
    const marker = raw.charAt(0);
    const body = raw.slice(1);
    if (marker === "+") out.push({ kind: "add", new: newAt++, text: body });
    else if (marker === "-") out.push({ kind: "remove", old: oldAt++, text: body });
    else out.push({ kind: "context", old: oldAt++, new: newAt++, text: body });
  }
  return out;
}

/** How many lines a diff adds and removes. */
export function diffCounts(lines: readonly DiffLine[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const l of lines) {
    if (l.kind === "add") added++;
    else if (l.kind === "remove") removed++;
  }
  return { added, removed };
}

function Marker({ kind }: { kind: DiffLineKind }) {
  const { t } = useI18n();
  if (kind === "add") {
    return (
      <>
        <span aria-hidden="true">+</span>
        <VisuallyHidden>{t("component.diff.added")}</VisuallyHidden>
      </>
    );
  }
  if (kind === "remove") {
    return (
      <>
        <span aria-hidden="true">−</span>
        <VisuallyHidden>{t("component.diff.removed")}</VisuallyHidden>
      </>
    );
  }
  return null;
}

export function DiffView({
  label,
  text,
  omitted = false,
}: {
  /** What is compared, e.g. "v3 against v2": the table's caption. */
  label: string;
  /** The unified diff, as the server wrote it. */
  text: string;
  /** The server did not render a line diff: the sources were too far apart. */
  omitted?: boolean;
}) {
  const { t, f } = useI18n();
  if (omitted) {
    return (
      <section className={styles.notice} data-diff="omitted">
        <p className={styles.noticeTitle}>{t("component.diff.omitted.title")}</p>
        <p className={styles.noticeBody}>{t("component.diff.omitted.body")}</p>
      </section>
    );
  }
  const lines = parseUnified(text);
  if (lines.length === 0) {
    return (
      <section className={styles.notice} data-diff="empty">
        <p className={styles.noticeTitle}>{t("component.diff.empty.title")}</p>
        <p className={styles.noticeBody}>{t("component.diff.empty.body")}</p>
      </section>
    );
  }
  const counts = diffCounts(lines);
  return (
    <figure className={styles.figure} data-diff="lines">
      <figcaption className={styles.caption}>
        <span>{label}</span>
        <span className={styles.counts}>
          {t("component.diff.counts", { added: f.number(counts.added), removed: f.number(counts.removed) })}
        </span>
      </figcaption>
      <div className={styles.scroll} tabIndex={0} role="region" aria-label={label}>
        <table className={styles.table}>
          <caption className={styles.hidden}>{label}</caption>
          <thead className={styles.hidden}>
            <tr>
              <th scope="col">{t("component.diff.col.old")}</th>
              <th scope="col">{t("component.diff.col.new")}</th>
              <th scope="col">{t("component.diff.col.change")}</th>
              <th scope="col">{t("component.diff.col.line")}</th>
            </tr>
          </thead>
          <tbody>
            {lines.map((l, i) =>
              l.kind === "hunk" ? (
                <tr key={i} className={styles.hunk} data-line="hunk">
                  <td colSpan={4} dir="ltr" translate="no">
                    {l.text}
                  </td>
                </tr>
              ) : (
                <tr key={i} data-line={l.kind}>
                  <td className={styles.number} translate="no">
                    {l.old ?? ""}
                  </td>
                  <td className={styles.number} translate="no">
                    {l.new ?? ""}
                  </td>
                  <td className={styles.marker}>
                    <Marker kind={l.kind} />
                  </td>
                  <td className={styles.text} dir="ltr" translate="no">
                    {l.text}
                  </td>
                </tr>
              ),
            )}
          </tbody>
        </table>
      </div>
    </figure>
  );
}

/** A diff's loading state: the shape of a few hunks. */
export function DiffSkeleton({ rows = 8 }: { rows?: number }) {
  const sizes = ["l", "m", "full", "s", "m", "l", "full", "m"] as const;
  return (
    <div className={styles.skeleton} aria-hidden="true" data-diff="loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} size={sizes[i % sizes.length]} />
      ))}
    </div>
  );
}
