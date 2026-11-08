// A COPYABLE ID (DESIGN.md §7, §9): mono, left-to-right in every locale, never
// translated, truncated with the whole value in its title, and copied whole on
// a click. What it copies is the value, never the shortened form.

import { useState } from "react";
import { useI18n } from "../i18n/I18n";
import styles from "./CopyableId.module.css";

export function CopyableId({
  value,
  short,
  volatile = false,
}: {
  value: string;
  /** The shortened form to show; the whole value is copied and titled. */
  short?: string;
  /** A value that differs on every run (a correlation id): screenshot masks skip it. */
  volatile?: boolean;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // No clipboard (an insecure origin, a denied permission): the value
      // is still selectable text, so nothing is lost.
    }
  };
  return (
    <span className={styles.wrap}>
      <code className={styles.id} dir="ltr" translate="no" title={value} data-volatile={volatile || undefined}>
        {short ?? value}
      </code>
      <button type="button" className={styles.copy} onClick={copy} aria-label={t("component.copy.label", { value })}>
        <svg viewBox="0 0 16 16" aria-hidden="true" focusable="false">
          {copied ? <path d="M3 8.5 6.5 12 13 4.5" /> : <path d="M5 5V3h8v8h-2M3 5h8v8H3z" />}
        </svg>
      </button>
      <span className={styles.announce} role="status">
        {copied ? t("component.copy.done") : ""}
      </span>
    </span>
  );
}
