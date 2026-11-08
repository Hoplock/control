// THE FOUR STATES (DESIGN.md §7): every screen defines loading, empty, error
// and partial, and they are one frame every screen uses rather than four
// things each screen improvises.
//   - loading is a skeleton matching the final layout, so nothing shifts;
//   - empty says what would be here, why it is not, and the one action;
//   - error says what failed, quotes the correlation id, offers retry — and
//     says OUTAGE for an outage, never "denied" (PLAN M11);
//   - partial is some data and a named gap: a fleet with one node unreachable
//     is not an error page.
// The error sentence is always the catalogue's, from the code and its
// parameters (0049's errorMessage); a code the catalogue does not know still
// renders, naming the code and the correlation id.

import type { ReactNode } from "react";
import { errorMessage, type ApiError } from "../i18n/errors";
import { useI18n } from "../i18n/I18n";
import type { Load } from "../shell/load";
import { Button } from "./Button";
import { CopyableId } from "./CopyableId";
import { Status } from "./Status";
import styles from "./States.module.css";

/** Which side of M11 an error is on: a refusal the server meant, or an outage. */
export type ErrorKind = "refused" | "outage";

/** An error inline: the sentence and its correlation id. */
export function ErrorMessage({ error, kind }: { error: ApiError; kind: ErrorKind }) {
  const { t } = useI18n();
  const rendered = errorMessage(t, error);
  return (
    <div className={styles.inline} data-kind={kind} role="alert" data-error-code={error.code}>
      <p className={styles.text}>{rendered.text}</p>
      {rendered.correlationId ? <Correlation id={rendered.correlationId} /> : null}
    </div>
  );
}

function Correlation({ id }: { id: string }) {
  const { t } = useI18n();
  return (
    <p className={styles.correlation}>
      <span>{t("error.correlation.label")}</span> <CopyableId value={id} volatile />
    </p>
  );
}

/** A screen's error state: a heading that says which kind, the sentence, the id, retry. */
export function ErrorState({ error, kind, onRetry }: { error: ApiError; kind: ErrorKind; onRetry?: () => void }) {
  const { t } = useI18n();
  const rendered = errorMessage(t, error);
  return (
    <section className={styles.state} data-state="error" data-kind={kind} role="alert" data-error-code={error.code}>
      <Status
        tone={kind === "outage" ? "outage" : "deny"}
        label={kind === "outage" ? t("state.error.outage") : t("state.error.refused")}
        pill
      />
      <p className={styles.text}>{rendered.text}</p>
      {rendered.correlationId ? <Correlation id={rendered.correlationId} /> : null}
      {onRetry && kind === "outage" ? (
        <div>
          <Button onClick={onRetry}>{t("common.retry")}</Button>
        </div>
      ) : null}
    </section>
  );
}

/** What would be here, why it is not, and the one action that changes that. */
export function EmptyState({ title, body, action }: { title: ReactNode; body: ReactNode; action?: ReactNode }) {
  return (
    <section className={styles.state} data-state="empty">
      <svg className={styles.emptyMark} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <rect x="3" y="5" width="18" height="14" rx="2" />
        <path d="M3 10h18" />
      </svg>
      <h2 className={styles.title}>{title}</h2>
      <p className={styles.text}>{body}</p>
      {action ? <div>{action}</div> : null}
    </section>
  );
}

/** Some data, and the gaps in it named: never an error page for a partial answer. */
export function PartialNotice({ gaps }: { gaps: readonly string[] }) {
  const { t } = useI18n();
  if (gaps.length === 0) return null;
  return (
    <section className={styles.partial} data-notice="partial" aria-labelledby="partial-title">
      <Status tone="warn" label={t("state.partial")} pill />
      <div className={styles.gaps}>
        <h2 id="partial-title" className={styles.partialTitle}>
          {t("state.partial.title")}
        </h2>
        <ul className={styles.gapList}>
          {gaps.map((g) => (
            <li key={g}>{g}</li>
          ))}
        </ul>
      </div>
    </section>
  );
}

/** A placeholder shaped like what it stands for. */
export function Skeleton({ shape = "line", size = "m" }: { shape?: "line" | "block" | "title"; size?: "s" | "m" | "l" | "full" }) {
  return <span className={styles.skeleton} data-shape={shape} data-size={size} aria-hidden="true" />;
}

/** A spinner, only ever over content it is refreshing (DESIGN.md §7). */
export function SpinnerOverlay({ label }: { label: string }) {
  return (
    <div className={styles.overlay} role="status">
      <span className={styles.spinner} aria-hidden="true" />
      <span className={styles.overlayLabel}>{label}</span>
    </div>
  );
}

/**
 * The frame a screen renders through. `loading` is the screen's own skeleton,
 * `empty` its own empty state; an error is ErrorState, refused and outage
 * kept apart; `gaps` names what a partial answer is missing.
 */
export function ScreenFrame<T>({
  load,
  loading,
  isEmpty,
  empty,
  onRetry,
  gaps = [],
  children,
}: {
  load: Load<T>;
  loading: ReactNode;
  isEmpty(data: T): boolean;
  empty: ReactNode;
  onRetry(): void;
  gaps?: readonly string[];
  children(data: T): ReactNode;
}) {
  const { t } = useI18n();
  switch (load.state) {
    case "loading":
      return (
        <div className={styles.frame} data-state="loading" aria-busy="true">
          <span className={styles.visuallyHidden} role="status">
            {t("common.loading")}
          </span>
          {loading}
        </div>
      );
    case "refused":
      return <ErrorState error={load.error} kind="refused" />;
    case "outage":
      return <ErrorState error={load.error} kind="outage" onRetry={onRetry} />;
    case "ok":
      if (isEmpty(load.data)) return <>{empty}</>;
      return (
        <div className={styles.frame} data-state={gaps.length ? "partial" : "ok"}>
          <PartialNotice gaps={gaps} />
          {children(load.data)}
        </div>
      );
  }
}
