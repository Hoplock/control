// DIALOG, DRAWER AND THE ONE DESTRUCTIVE CONFIRM (DESIGN.md §2, §7, §8).
//
// Both overlays are the native <dialog>, opened modal: the rest of the page
// is inert while one is open, so focus cannot leave it (the trap is the
// browser's, not a script's that can be wrong), Escape closes it, and focus
// goes back to whatever opened it. A dialog is for a decision; a drawer is
// for detail beside the list it came from.
//
// ConfirmDialog is how every destructive action confirms: by typing the
// object's name. Activating a bundle, killing a session, revoking a grant,
// withdrawing a proxy — each uses this component, so each looks the same and
// none can be confirmed by a reflexive Enter. A failed action reports inside
// the dialog, at the form (never by a toast alone), and the dialog stays.

import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import type { ApiError } from "../i18n/errors";
import { useI18n } from "../i18n/I18n";
import { Button, IconButton } from "./Button";
import { Field, Input } from "./Field";
import { ErrorMessage, type ErrorKind } from "./States";
import styles from "./Dialog.module.css";

function useModal(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDialogElement>(null);
  const opener = useRef<Element | null>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) {
      opener.current = document.activeElement;
      d.showModal();
    } else if (!open && d.open) {
      d.close();
    }
  }, [open]);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    const cancel = (e: Event) => {
      e.preventDefault();
      onClose();
    };
    const closed = () => {
      const back = opener.current;
      if (back instanceof HTMLElement && back.isConnected) back.focus();
    };
    d.addEventListener("cancel", cancel);
    d.addEventListener("close", closed);
    return () => {
      d.removeEventListener("cancel", cancel);
      d.removeEventListener("close", closed);
    };
  }, [onClose]);
  return ref;
}

const CloseGlyph = (
  <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
    <path d="M4 4l8 8M12 4l-8 8" />
  </svg>
);

export function Dialog({
  open,
  title,
  onClose,
  children,
  footer,
  tone = "default",
}: {
  open: boolean;
  title: ReactNode;
  onClose(): void;
  children: ReactNode;
  footer?: ReactNode;
  tone?: "default" | "danger";
}) {
  const { t } = useI18n();
  const ref = useModal(open, onClose);
  const titleId = useId();
  return (
    <dialog ref={ref} className={styles.dialog} aria-labelledby={titleId} data-tone={tone}>
      {open ? (
        <div className={styles.panel}>
          <header className={styles.head}>
            <h2 id={titleId} className={styles.title}>
              {title}
            </h2>
            <IconButton label={t("component.dialog.close")} icon={CloseGlyph} onClick={onClose} />
          </header>
          <div className={styles.body}>{children}</div>
          {footer ? <footer className={styles.foot}>{footer}</footer> : null}
        </div>
      ) : null}
    </dialog>
  );
}

export function Drawer({
  open,
  title,
  onClose,
  children,
}: {
  open: boolean;
  title: ReactNode;
  onClose(): void;
  children: ReactNode;
}) {
  const { t } = useI18n();
  const ref = useModal(open, onClose);
  const titleId = useId();
  return (
    <dialog ref={ref} className={styles.drawer} aria-labelledby={titleId}>
      {open ? (
        <div className={styles.drawerPanel}>
          <header className={styles.head}>
            <h2 id={titleId} className={styles.title}>
              {title}
            </h2>
            <IconButton label={t("component.dialog.close")} icon={CloseGlyph} onClick={onClose} />
          </header>
          <div className={styles.drawerBody}>{children}</div>
        </div>
      ) : null}
    </dialog>
  );
}

export interface ConfirmFailure {
  error: ApiError;
  kind: ErrorKind;
}

/**
 * Confirms a destructive action by typing `name`. `onConfirm` resolves to
 * null on success (the caller closes it) or to the failure, shown inline.
 */
export function ConfirmDialog({
  open,
  title,
  body,
  name,
  action,
  hint,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: ReactNode;
  body: ReactNode;
  /** What the operator types: the object's own name or id, verbatim. */
  name: string;
  /** The confirm button's label: the verb, never "OK". */
  action: string;
  /** What the field says beneath it; by default, that the action cannot be undone. */
  hint?: ReactNode;
  onConfirm(): Promise<ConfirmFailure | null>;
  onClose(): void;
}) {
  const { t } = useI18n();
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ConfirmFailure | null>(null);
  useEffect(() => {
    if (open) {
      setTyped("");
      setFailure(null);
      setBusy(false);
    }
  }, [open]);
  const matches = typed === name;
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!matches || busy) return;
    setBusy(true);
    setFailure(null);
    const result = await onConfirm();
    setBusy(false);
    if (result) setFailure(result);
  };
  return (
    <Dialog open={open} title={title} onClose={onClose} tone="danger">
      <form className={styles.form} onSubmit={submit} noValidate>
        <div className={styles.warning}>{body}</div>
        <Field
          label={t("component.confirm.type", { name: "⁨" + name + "⁩" })}
          hint={hint ?? t("component.confirm.hint")}
          error={typed !== "" && !matches ? t("component.confirm.mismatch") : undefined}
        >
          <Input
            mono
            autoComplete="off"
            spellCheck={false}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            data-confirm-input=""
          />
        </Field>
        {failure ? <ErrorMessage error={failure.error} kind={failure.kind} /> : null}
        <div className={styles.actions}>
          <Button onClick={onClose}>{t("common.cancel")}</Button>
          <Button type="submit" variant="danger" disabled={!matches} loading={busy}>
            {action}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
