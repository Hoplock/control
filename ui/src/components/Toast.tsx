// TOASTS (DESIGN.md §7). `success` is for successful, reversible things, and
// it leaves by itself. `receipt` is the success of a destructive action — one
// confirmed by typing its object's name (ConfirmDialog) — and it stays until
// it is dismissed, because what it says (a grant revoked, the sessions it
// ended) should not vanish while the operator looks away; the audit trail,
// not the toast, is the record. An error is never only a toast — a failed
// mutation reports inline, at the form — so an error toast is a second
// notice of something already reported in place, and it stays too.

import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import { IconButton } from "./Button";
import { StatusGlyph } from "./Status";
import styles from "./Toast.module.css";

interface ToastItem {
  id: number;
  kind: "success" | "receipt" | "error";
  text: string;
}

interface Toasts {
  success(text: string): void;
  receipt(text: string): void;
  error(text: string): void;
}

const Context = createContext<Toasts>({ success: () => {}, receipt: () => {}, error: () => {} });

const SUCCESS_MS = 5000;

export function ToastProvider({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const [items, setItems] = useState<ToastItem[]>([]);
  const dismiss = useCallback((id: number) => setItems((xs) => xs.filter((x) => x.id !== id)), []);
  const push = useCallback(
    (kind: ToastItem["kind"], text: string) => {
      const id = Date.now() + Math.random();
      setItems((xs) => [...xs, { id, kind, text }]);
      if (kind === "success") window.setTimeout(() => dismiss(id), SUCCESS_MS);
    },
    [dismiss],
  );
  const api = useMemo(
    () => ({
      success: (s: string) => push("success", s),
      receipt: (s: string) => push("receipt", s),
      error: (s: string) => push("error", s),
    }),
    [push],
  );
  return (
    <Context.Provider value={api}>
      {children}
      <ToastRegion items={items} onDismiss={dismiss} closeLabel={t("component.toast.dismiss")} />
    </Context.Provider>
  );
}

export function ToastRegion({
  items,
  onDismiss,
  closeLabel,
}: {
  items: readonly ToastItem[];
  onDismiss(id: number): void;
  closeLabel: string;
}) {
  return (
    <div className={styles.region}>
      <div role="status" className={styles.stack}>
        {items
          .filter((i) => i.kind !== "error")
          .map((i) => (
            <Toast key={i.id} item={i} onDismiss={onDismiss} closeLabel={closeLabel} />
          ))}
      </div>
      <div role="alert" className={styles.stack}>
        {items
          .filter((i) => i.kind === "error")
          .map((i) => (
            <Toast key={i.id} item={i} onDismiss={onDismiss} closeLabel={closeLabel} />
          ))}
      </div>
    </div>
  );
}

function Toast({ item, onDismiss, closeLabel }: { item: ToastItem; onDismiss(id: number): void; closeLabel: string }) {
  return (
    <div className={styles.toast} data-kind={item.kind}>
      <StatusGlyph tone={item.kind === "error" ? "outage" : "allow"} />
      <p className={styles.text}>{item.text}</p>
      <IconButton
        label={closeLabel}
        onClick={() => onDismiss(item.id)}
        icon={
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
            <path d="M4 4l8 8M12 4l-8 8" />
          </svg>
        }
      />
    </div>
  );
}

export function useToast(): Toasts {
  return useContext(Context);
}
