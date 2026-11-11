// A north-bound error, as a sentence the console builds (PLAN M21, 0019).
//
// The API answers with a stable code, typed parameters, an English message for
// curl and a correlation id. The console never shows or parses that English:
// it looks the code up here and formats the catalogue's message with the
// parameters. A code this console has no message for still renders, naming
// the code, so an older console meeting a newer server says something true.
// 0050's error component renders what this returns, with its correlation id.

import type { MessageKey } from "./catalogue";
import type { Translate, Values } from "./messages";

/** The error envelope every north-bound refusal and failure carries. */
export interface ApiError {
  code: string;
  message?: string;
  parameters?: Record<string, unknown>;
  correlation_id?: string;
}

/** The code the API client gives an answer that never arrived (M11: an outage). */
export const NETWORK_FAILURE = "network";

/** Each code this console has a sentence for. A later phase adds its own. */
export const ERROR_MESSAGES: Readonly<Record<string, MessageKey>> = {
  unauthenticated: "error.unauthenticated",
  forbidden: "error.forbidden",
  tenant_out_of_scope: "error.tenant_out_of_scope",
  tenant_ambiguous: "error.tenant_ambiguous",
  login_refused: "error.login_refused",
  not_found: "error.not_found",
  invalid_request: "error.invalid_request",
  internal: "error.internal",
  unavailable: "error.unavailable",
  api_version_unsupported: "error.api_version_unsupported",
  decision_unexplainable: "error.decision_unexplainable",
  policy_invalid: "error.policy_invalid",
  findings_not_accepted: "error.findings_not_accepted",
  policy_blocked: "error.policy_blocked",
  // Standing denials and the directory (0039, 0040; shown from 0055).
  target_quarantined: "error.target_quarantined",
  subject_directory_managed: "error.subject_directory_managed",
  standing_denial_undelivered: "error.standing_denial_undelivered",
  // Report packs (0044; shown from 0056): each an outage of the pack, never a denial.
  report_provider_failed: "error.report_provider_failed",
  report_provider_unavailable: "error.report_provider_unavailable",
  report_generation_busy: "error.report_generation_busy",
  [NETWORK_FAILURE]: "error.network",
};

/** Parameters an ICU message can take: scalars only, never an object. */
function values(parameters: Record<string, unknown> | undefined): Values {
  const out: Values = {};
  for (const [k, v] of Object.entries(parameters ?? {})) {
    if (typeof v === "string" || typeof v === "number") out[k] = v;
    else if (typeof v === "boolean") out[k] = String(v);
  }
  return out;
}

export interface RenderedError {
  /** The sentence, from the catalogue. */
  text: string;
  /** Whether the console knew the code: false is the named fallback. */
  known: boolean;
  /** The id the request was logged under, to quote to an operator. */
  correlationId: string | null;
}

export function errorMessage(t: Translate, error: ApiError): RenderedError {
  const key = ERROR_MESSAGES[error.code];
  const correlationId = error.correlation_id || null;
  if (!key) return { text: t("error.unknown", { code: error.code }), known: false, correlationId };
  try {
    return { text: t(key, values(error.parameters)), known: true, correlationId };
  } catch {
    // A message whose parameter the server did not send is still a known
    // code; say what the code is rather than fail to render.
    return { text: t("error.unknown", { code: error.code }), known: false, correlationId };
  }
}
