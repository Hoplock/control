// THE TIMELINE (DESIGN.md §7; 0051): what happened, in the order it happened —
// a session's decisions hop by hop on Explain, a session's records on replay.
//
// An ordered list, oldest first, read top to bottom along one rule. Each event
// has an instant (a <time>, the operator's zone, UTC on hover), a marker, a
// title, and what to say beneath it. A marker that is a status is the status
// vocabulary's glyph beside its word (§6), never colour alone. When events
// can be chosen it is one tab stop: the arrows move between events, Home and
// End go to the ends, and Enter or a click chooses one, so a long session is
// read without a mouse (§2, "keyboard is a path").

import { useRef, type KeyboardEvent, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import { Skeleton } from "./States";
import { Status, StatusGlyph, type Tone } from "./Status";
import styles from "./Timeline.module.css";

export interface TimelineEvent {
  id: string;
  /** The instant, ISO-8601. */
  at: string;
  title: ReactNode;
  /** A status for the marker: its glyph on the rule, its word beside the title. */
  status?: { tone: Tone; label: string };
  /** Facts beneath the title: an id, a proxy, a rule. */
  meta?: ReactNode;
  /** An event that cannot be chosen (its detail cannot be read). */
  disabled?: boolean;
}

export function Timeline({
  label,
  events,
  selected,
  onSelect,
  empty,
}: {
  label: string;
  events: readonly TimelineEvent[];
  /** The chosen event; with onSelect, events are chosen by keyboard or click. */
  selected?: string | null;
  onSelect?(id: string): void;
  /** What the timeline says when nothing happened. */
  empty?: ReactNode;
}) {
  const { f } = useI18n();
  const root = useRef<HTMLOListElement>(null);
  const choosable = Boolean(onSelect);
  const enabled = events.filter((e) => !e.disabled);
  const active = selected && enabled.some((e) => e.id === selected) ? selected : (enabled[0]?.id ?? null);

  if (events.length === 0) {
    return (
      <div className={styles.empty} data-timeline="empty">
        {empty}
      </div>
    );
  }

  const focus = (id: string) => {
    root.current?.querySelector<HTMLElement>(`[data-event="${CSS.escape(id)}"] [data-handle]`)?.focus();
    onSelect?.(id);
  };
  const keys = (e: KeyboardEvent) => {
    if (!choosable || !active) return;
    const at = enabled.findIndex((x) => x.id === active);
    let to: TimelineEvent | undefined;
    if (e.key === "ArrowDown") to = enabled[at + 1];
    else if (e.key === "ArrowUp") to = enabled[at - 1];
    else if (e.key === "Home") to = enabled[0];
    else if (e.key === "End") to = enabled[enabled.length - 1];
    else return;
    e.preventDefault();
    if (to) focus(to.id);
  };

  return (
    <ol ref={root} className={styles.timeline} aria-label={label} onKeyDown={keys} data-choosable={choosable || undefined}>
      {events.map((e) => {
        const current = choosable && e.id === active;
        const body = (
          <>
            <span className={styles.head}>
              <span className={styles.title}>{e.title}</span>
              {e.status ? <Status tone={e.status.tone} label={e.status.label} /> : null}
            </span>
            <time className={styles.at} dateTime={f.utc(e.at)} title={f.utc(e.at)}>
              {f.dateTime(e.at)}
            </time>
            {e.meta ? <span className={styles.meta}>{e.meta}</span> : null}
          </>
        );
        return (
          <li
            key={e.id}
            className={styles.event}
            data-event={e.id}
            data-selected={current || undefined}
            data-disabled={e.disabled || undefined}
          >
            <span className={styles.marker} aria-hidden="true">
              {e.status ? <StatusGlyph tone={e.status.tone} /> : <span className={styles.dot} />}
            </span>
            {choosable ? (
              <button
                type="button"
                className={styles.handle}
                data-handle
                tabIndex={current ? 0 : -1}
                aria-current={current ? "step" : undefined}
                disabled={e.disabled}
                onClick={() => onSelect?.(e.id)}
              >
                {body}
              </button>
            ) : (
              <div className={styles.handle}>{body}</div>
            )}
          </li>
        );
      })}
    </ol>
  );
}

/** A timeline's loading state: its own rule and rows of skeleton, so nothing shifts. */
export function TimelineSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <ol className={styles.timeline} aria-hidden="true">
      {Array.from({ length: rows }, (_, i) => (
        <li key={i} className={styles.event}>
          <span className={styles.marker}>
            <span className={styles.dot} />
          </span>
          <div className={styles.handle}>
            <Skeleton size="l" />
            <Skeleton size="s" />
          </div>
        </li>
      ))}
    </ol>
  );
}
