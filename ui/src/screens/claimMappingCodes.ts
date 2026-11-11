// THE CLAIM MAPPING'S REJECTION CODES, AS SENTENCES (0011, 0043; PLAN M21).
//
// A refused mapping write is `400 invalid_request` whose parameters carry
// `rejections`: one `{code, field}` per fault, in the document's order. The
// server's English is for curl; the console looks each code up here and
// shows its sentence beside the field it names, so a code this console does
// not know still renders, naming itself (claimMapping.reject.unknown).
// The codes are internal/identity's RejectMapping*; test/claimMapping.test.ts
// holds this list to them.

import type { MessageKey } from "../i18n/catalogue";

export const MAPPING_REJECTIONS: Readonly<Record<string, MessageKey>> = {
  "mapping.unparseable": "claimMapping.reject.unparseable",
  "mapping.schema_version": "claimMapping.reject.schema_version",
  "mapping.claim_empty": "claimMapping.reject.claim_empty",
  "mapping.attribute_name": "claimMapping.reject.attribute_name",
  "mapping.attribute_duplicate": "claimMapping.reject.attribute_duplicate",
  "mapping.attribute_reserved": "claimMapping.reject.attribute_reserved",
  "mapping.group_claim": "claimMapping.reject.group_claim",
  "mapping.group_name": "claimMapping.reject.group_name",
  "mapping.group_duplicate": "claimMapping.reject.group_duplicate",
  "mapping.too_large": "claimMapping.reject.too_large",
  "mapping.value_empty": "claimMapping.reject.value_empty",
  // Directory-scoped group entries (0043).
  "mapping.directory_name": "claimMapping.reject.directory_name",
  "mapping.directories_empty": "claimMapping.reject.directories_empty",
  "mapping.directory_duplicate": "claimMapping.reject.directory_duplicate",
};

/** The sentence for one rejection's code, or the named fallback for a code this console does not know. */
export function rejectionKey(code: string): { key: MessageKey; known: boolean } {
  const key = MAPPING_REJECTIONS[code];
  return key ? { key, known: true } : { key: "claimMapping.reject.unknown", known: false };
}
