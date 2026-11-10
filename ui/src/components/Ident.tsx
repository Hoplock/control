// An identifier (DESIGN.md §3.5, §9): an instance or session id, a
// fingerprint, a hostname, a label, a rule name. Mono, left to right inside
// any page, never translated and never given locale digits, because it is
// compared character by character with the one in a terminal. It is data, so
// it is never a catalogue string.

import styles from "./Ident.module.css";

/**
 * `title` sets it at the page title's size, for a page that is about one
 * object named by its id — a target, an identity, a group, a role (0054).
 */
export function Ident({ value, short, title = false }: { value: string; short?: string; title?: boolean }) {
  return (
    <code className={title ? styles.title : styles.ident} dir="ltr" translate="no" title={short && short !== value ? value : undefined}>
      {short ?? value}
    </code>
  );
}

/** The short form of an instance id: its prefix and the first eight of its hex. */
export function shortInstanceId(id: string): string {
  const m = /^(inst-)([0-9a-f]{8})[0-9a-f]+$/.exec(id);
  return m ? `${m[1]}${m[2]}` : id;
}
