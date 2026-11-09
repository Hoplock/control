// The words the audit view reads a record's closed sets in (0051). A kind and
// a severity are the contract's identifiers; the console names each in the
// operator's language and keeps the identifier where it is compared (the
// record detail, the body). A value outside the set is shown as it came.

import type { MessageKey } from "../i18n/catalogue";
import { useI18n } from "../i18n/I18n";
import type { Kind, Severity } from "./auditModel";

const KIND: Record<Kind, MessageKey> = {
  session_start: "audit.kind.session_start",
  session_end: "audit.kind.session_end",
  auth: "audit.kind.auth",
  authorize: "audit.kind.authorize",
  channel_open: "audit.kind.channel_open",
  channel_close: "audit.kind.channel_close",
  command: "audit.kind.command",
  policy_decision: "audit.kind.policy_decision",
  host_key: "audit.kind.host_key",
  stream: "audit.kind.stream",
  provisioning: "audit.kind.provisioning",
  error: "audit.kind.error",
};

const SEVERITY: Record<Severity, MessageKey> = {
  info: "audit.severity.info",
  warn: "audit.severity.warn",
  critical: "audit.severity.critical",
};

export function useKindLabel(): (kind: string) => string {
  const { t } = useI18n();
  return (kind) => (kind in KIND ? t(KIND[kind as Kind]) : kind);
}

export function useSeverityLabel(): (severity: string) => string {
  const { t } = useI18n();
  return (s) => (s in SEVERITY ? t(SEVERITY[s as Severity]) : s);
}
