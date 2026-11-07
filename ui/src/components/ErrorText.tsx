// An API error, as the shell shows one: the catalogue's sentence for its code
// (0049's helper) and the correlation id to quote. 0050's error component
// replaces this with the full error state; this is the shell's minimum.

import { errorMessage, type ApiError } from "../i18n/errors";
import { useI18n } from "../i18n/I18n";
import styles from "./ErrorText.module.css";

/**
 * kind is the answer's: a refusal is a decision the server made, and an
 * outage is everything else (M11), and the two are never drawn alike.
 */
export function ErrorText({ error, kind }: { error: ApiError; kind: "refused" | "outage" }) {
  const { t } = useI18n();
  const rendered = errorMessage(t, error);
  return (
    <div className={styles.error} data-kind={kind} role="alert">
      <p className={styles.text}>{rendered.text}</p>
      {rendered.correlationId ? (
        <p className={styles.correlation}>
          {t("error.correlation", { id: "⁨" + rendered.correlationId + "⁩" })}
        </p>
      ) : null}
    </div>
  );
}
