import type { ReactNode } from "react";
import styles from "./VisuallyHidden.module.css";

/** Text for a screen reader alone, such as a skeleton's "Loading". */
export function VisuallyHidden({ children }: { children: ReactNode }) {
  return <span className={styles.hidden}>{children}</span>;
}
