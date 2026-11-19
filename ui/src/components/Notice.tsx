// A NOTICE (DESIGN.md §7): a sentence set apart from the page because it is
// about the page — a rule that holds here, a request waiting, a receipt, a
// refusal at the form. One shape everywhere: a panel's ground with a hairline
// border, as the panels around it and the partial notice have — never the
// sunken ground, which §3.2 keeps for code and logs, and which on the dark
// theme reads as a hole in the page — a rule along its inline start in its
// tone's hue, and that tone's glyph, so it is never colour alone (§2). Its tone is one of the status vocabulary's (§6), and it
// is chosen for what the sentence says, never for emphasis: `info` for a
// fact or a wait, `warn` for something that holds the operator back, `allow`
// for a receipt of what was done, `deny` for a refusal. A screen never draws
// one of its own (test/consistency.test.ts).

import type { HTMLAttributes, ReactNode } from "react";
import { StatusGlyph, type Tone } from "./Status";
import styles from "./Notice.module.css";

export type NoticeTone = Extract<Tone, "info" | "warn" | "allow" | "deny">;

export function Notice({
  tone = "info",
  title,
  status,
  children,
  ...rest
}: {
  tone?: NoticeTone;
  /** A status pill that heads the notice in its glyph's place, when the notice is about one state (§6). */
  status?: ReactNode;
  /** A heading, when the notice is more than one sentence. */
  title?: ReactNode;
  children: ReactNode;
} & Omit<HTMLAttributes<HTMLDivElement>, "title" | "className" | "style">) {
  return (
    <div {...rest} className={styles.notice} data-tone={tone}>
      {status ?? (
        <span className={styles.glyph}>
          <StatusGlyph tone={tone} />
        </span>
      )}
      <div className={styles.body}>
        {title ? <p className={styles.title}>{title}</p> : null}
        {children}
      </div>
    </div>
  );
}
