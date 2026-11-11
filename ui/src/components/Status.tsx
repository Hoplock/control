// THE STATUS VOCABULARY (DESIGN.md §6): one component renders every status,
// so a status looks the same everywhere, and none is colour alone — each has
// its own glyph and its own word. Three of its rules are decisions of the
// plan, not taste, and the vocabulary below is where they are encoded:
//   - a deny and an outage are different tones, glyphs and words (M11);
//   - an upgrade in progress is `info`, never a fault (M19);
//   - not reported is its own state and never reads as healthy (M17).
// A state a later phase needs is added to STATUS (and to DESIGN.md §6), never
// rendered beside this component.

import type { MessageKey } from "../i18n/catalogue";
import { useI18n } from "../i18n/I18n";
import styles from "./Status.module.css";

export type Tone = "allow" | "deny" | "warn" | "outage" | "info" | "unknown";

/** A status as the console names it: a tone and the word it is read as. */
export interface StatusView {
  tone: Tone;
  label: MessageKey;
}

/** Every status this console renders (DESIGN.md §6, by row). */
export const STATUS = {
  allowed: { tone: "allow", label: "status.allowed" },
  healthy: { tone: "allow", label: "status.healthy" },
  active: { tone: "allow", label: "status.active" },
  applied: { tone: "allow", label: "status.applied" },
  valid: { tone: "allow", label: "status.valid" },
  // Extensions (0056): a handler whose Start succeeded, a consumer with nothing unconfirmed.
  running: { tone: "allow", label: "status.running" },
  caughtUp: { tone: "allow", label: "status.caughtUp" },
  denied: { tone: "deny", label: "status.denied" },
  revoked: { tone: "deny", label: "status.revoked" },
  expired: { tone: "deny", label: "status.expired" },
  invalid: { tone: "deny", label: "status.invalid" },
  blocking: { tone: "deny", label: "status.blocking" },
  // Standing denials (M28, 0055): each a deny made on purpose, never an outage (M11).
  locked: { tone: "deny", label: "status.locked" },
  quarantined: { tone: "deny", label: "status.quarantined" },
  deactivated: { tone: "deny", label: "status.deactivated" },
  degraded: { tone: "warn", label: "status.degraded" },
  stale: { tone: "warn", label: "status.stale" },
  expiring: { tone: "warn", label: "status.expiring" },
  rejected: { tone: "warn", label: "status.rejected" },
  warning: { tone: "warn", label: "status.warning" },
  // A registered extension whose Start failed, and a downstream consumer whose
  // delivery is failing: each degraded, never an outage of Control (M11, M8).
  notStarted: { tone: "warn", label: "status.notStarted" },
  failing: { tone: "warn", label: "status.failing" },
  unreachable: { tone: "outage", label: "status.unreachable" },
  fetchFailed: { tone: "outage", label: "status.fetchFailed" },
  unserved: { tone: "outage", label: "status.unserved" },
  // A report pack that could not list its reports, and a generation that ended broken (0056).
  unavailable: { tone: "outage", label: "status.unavailable" },
  failed: { tone: "outage", label: "status.failed" },
  pending: { tone: "info", label: "status.pending" },
  scheduled: { tone: "info", label: "status.scheduled" },
  inProgress: { tone: "info", label: "status.inProgress" },
  pendingRestart: { tone: "info", label: "status.pendingRestart" },
  advice: { tone: "info", label: "status.advice" },
  behind: { tone: "info", label: "status.behind" },
  notReported: { tone: "unknown", label: "status.notReported" },
  left: { tone: "unknown", label: "status.left" },
  superseded: { tone: "unknown", label: "status.superseded" },
  // A point with nothing registered: Control does what it says instead, which is not a fault (M15).
  notRegistered: { tone: "unknown", label: "status.notRegistered" },
} as const satisfies Record<string, StatusView>;

export type StatusName = keyof typeof STATUS;

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

/** The glyph alone, for a legend or a chart key; always beside its word. */
export function StatusGlyph({ tone }: { tone: Tone }) {
  return (
    <svg className={styles.glyph} data-tone={tone} viewBox="0 0 10 10" aria-hidden="true" focusable="false">
      <Glyph tone={tone} />
    </svg>
  );
}

/**
 * A status: glyph in the hue, word in text colour. `pill` sets it on a tinted
 * ground with a border, for a status that heads a page or a row's state;
 * inline is for a status inside running text or a dense cell.
 */
export function Status({ tone, label, pill = false }: { tone: Tone; label: string; pill?: boolean }) {
  return (
    <span className={styles.status} data-tone={tone} data-pill={pill || undefined}>
      <StatusGlyph tone={tone} />
      <span className={styles.label}>{label}</span>
    </span>
  );
}

/** A named status from the vocabulary. */
export function StatusOf({ name, pill = false }: { name: StatusName; pill?: boolean }) {
  const { t } = useI18n();
  const s = STATUS[name];
  return <Status tone={s.tone} label={t(s.label)} pill={pill} />;
}
