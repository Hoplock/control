// AN INSTANT (DESIGN.md §7, §9): the one way the console shows a timestamp.
// It reads in the operator's own zone, says its ISO-8601 UTC form on hover,
// and copies as that form, because an incident is discussed across zones and
// a pasted time has to be unambiguous. Formatted through the locale helpers
// alone (src/i18n/format.ts). Every screen renders an instant through this,
// never a <time> of its own (test/consistency.test.ts).

import type { ClipboardEvent } from "react";
import { useI18n } from "../i18n/I18n";

export function Timestamp({ at, className }: { at: Date | string; className?: string }) {
  const { f } = useI18n();
  const iso = f.utc(at);
  const onCopy = (e: ClipboardEvent<HTMLElement>) => {
    const selection = window.getSelection();
    const node = e.currentTarget;
    // Only a selection wholly inside this instant copies as UTC: a selection
    // spanning a row copies what it shows, as the platform does.
    if (!selection || selection.isCollapsed || !node.contains(selection.anchorNode) || !node.contains(selection.focusNode)) return;
    e.clipboardData.setData("text/plain", iso);
    e.preventDefault();
  };
  return (
    <time className={className} dateTime={iso} title={iso} onCopy={onCopy}>
      {f.dateTime(at)}
    </time>
  );
}
