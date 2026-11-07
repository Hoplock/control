// THE STATUS VOCABULARY (DESIGN.md §6): one component renders every status,
// so a status looks the same everywhere, and none is colour alone — each has
// its own glyph and its own word. Two of its rules are decisions of the plan,
// not taste: a deny and an outage are different tones (M11), and an upgrade
// in progress is `info`, never a fault (M19). 0050 grows this into the full
// pill; a state a later phase needs is added here, never rendered beside it.

import styles from "./Status.module.css";

export type Tone = "allow" | "deny" | "warn" | "outage" | "info" | "unknown";

function Glyph({ tone }: { tone: Tone }) {
  switch (tone) {
    case "allow":
      return <circle cx="5" cy="5" r="4.5" />;
    case "deny":
      return <rect x="1" y="1" width="8" height="8" />;
    case "warn":
      return <polygon points="5,0.5 9.5,9.5 0.5,9.5" />;
    case "outage":
      return <circle cx="5" cy="5" r="4" className={styles.hollow} strokeDasharray="2 1.4" />;
    case "info":
      return (
        <>
          <circle cx="5" cy="5" r="4" className={styles.hollow} />
          <path d="M5 1 A4 4 0 0 1 5 9 Z" />
        </>
      );
    case "unknown":
      return <circle cx="5" cy="5" r="4" className={styles.hollow} strokeDasharray="0.6 1.4" strokeLinecap="round" />;
  }
}

export function Status({ tone, label }: { tone: Tone; label: string }) {
  return (
    <span className={styles.status} data-tone={tone}>
      <svg className={styles.glyph} viewBox="0 0 10 10" aria-hidden="true" focusable="false">
        <Glyph tone={tone} />
      </svg>
      <span className={styles.label}>{label}</span>
    </span>
  );
}
