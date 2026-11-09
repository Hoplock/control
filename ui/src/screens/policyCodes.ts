// THE POLICY CODES' SENTENCES (0053; PLAN M21). A compiler rejection and a
// publish-time finding arrive as a stable code and typed parameters beside an
// English message for policyctl and curl; the console never shows or parses
// that English. It looks the code up here and builds its own sentence from
// the parameters. A code it has no sentence for still renders — naming the
// code, and the correlation id of the answer that carried it — so an older
// console meeting a newer server says something true. test/policy.test.ts
// holds both maps to the server's codes (test/policy-codes.json).

import type { Finding, Rejection } from "../api/types";
import type { MessageKey } from "../i18n/catalogue";
import type { Translate } from "../i18n/messages";
import { findingValues, rejectionValues } from "./policyModel";

/** Each compiler rejection code, and this server's one, by its sentence. */
export const REJECTION: Readonly<Record<string, MessageKey>> = {
  "bundle.tenant_mismatch": "policies.reject.bundle.tenant_mismatch",
  "cache.hint_on_deny": "policies.reject.cache.hint_on_deny",
  "cache.key_component_duplicate": "policies.reject.cache.key_component_duplicate",
  "cache.key_empty": "policies.reject.cache.key_empty",
  "cache.key_not_identity_bound": "policies.reject.cache.key_not_identity_bound",
  "cache.ttl_invalid": "policies.reject.cache.ttl_invalid",
  "credential.credential_kind_missing": "policies.reject.credential.credential_kind_missing",
  "credential.device_field_name_invalid": "policies.reject.credential.device_field_name_invalid",
  "credential.device_field_too_many": "policies.reject.credential.device_field_too_many",
  "credential.device_field_value_invalid": "policies.reject.credential.device_field_value_invalid",
  "credential.device_field_wrong_method": "policies.reject.credential.device_field_wrong_method",
  "credential.expiry_posture_missing": "policies.reject.credential.expiry_posture_missing",
  "credential.lifetime_invalid": "policies.reject.credential.lifetime_invalid",
  "credential.lifetime_missing": "policies.reject.credential.lifetime_missing",
  "credential.method_missing": "policies.reject.credential.method_missing",
  "credential.param_wrong_method": "policies.reject.credential.param_wrong_method",
  "credential.platform_missing": "policies.reject.credential.platform_missing",
  "credential.username_empty": "policies.reject.credential.username_empty",
  "credential.username_missing": "policies.reject.credential.username_missing",
  "enforcement.applied_rung_unprovisioned": "policies.reject.enforcement.applied_rung_unprovisioned",
  "enforcement.attestation_missing": "policies.reject.enforcement.attestation_missing",
  "enforcement.attestation_unexpected": "policies.reject.enforcement.attestation_unexpected",
  "enforcement.destinations_required": "policies.reject.enforcement.destinations_required",
  "enforcement.interactive_shell_permitted": "policies.reject.enforcement.interactive_shell_permitted",
  "enforcement.permitted_requests_missing": "policies.reject.enforcement.permitted_requests_missing",
  "enforcement.platform_role_missing": "policies.reject.enforcement.platform_role_missing",
  "enforcement.platform_role_unexpected": "policies.reject.enforcement.platform_role_unexpected",
  "enforcement.restricted_exec_required": "policies.reject.enforcement.restricted_exec_required",
  "match.empty_term": "policies.reject.match.empty_term",
  "match.invalid_cidr": "policies.reject.match.invalid_cidr",
  "match.invalid_hostname_pattern": "policies.reject.match.invalid_hostname_pattern",
  "match.unknown_group": "policies.reject.match.unknown_group",
  "match.unknown_label_key": "policies.reject.match.unknown_label_key",
  "match.unknown_label_value": "policies.reject.match.unknown_label_value",
  "obligation.contradicts_route": "policies.reject.obligation.contradicts_route",
  "obligation.duplicate": "policies.reject.obligation.duplicate",
  "obligation.kind_missing": "policies.reject.obligation.kind_missing",
  "obligation.on_deny": "policies.reject.obligation.on_deny",
  "policy.document_malformed": "policies.reject.policy.document_malformed",
  "policy.group_vocabulary_malformed": "policies.reject.policy.group_vocabulary_malformed",
  "policy.label_vocabulary_malformed": "policies.reject.policy.label_vocabulary_malformed",
  "policy.no_rules": "policies.reject.policy.no_rules",
  "policy.schema_version_unsupported": "policies.reject.policy.schema_version_unsupported",
  "policy.tenant_missing": "policies.reject.policy.tenant_missing",
  "policy.timezone_unknown": "policies.reject.policy.timezone_unknown",
  "route.algorithm_ban_empty": "policies.reject.route.algorithm_ban_empty",
  "route.algorithm_ban_repeated": "policies.reject.route.algorithm_ban_repeated",
  "route.algorithm_floor_profile_conflict": "policies.reject.route.algorithm_floor_profile_conflict",
  "route.channels_missing": "policies.reject.route.channels_missing",
  "route.concurrency_invalid": "policies.reject.route.concurrency_invalid",
  "route.exec_mode_mismatch": "policies.reject.route.exec_mode_mismatch",
  "route.filter_mode_missing": "policies.reject.route.filter_mode_missing",
  "route.filter_policy_both_tiers": "policies.reject.route.filter_policy_both_tiers",
  "route.filter_rule_invalid": "policies.reject.route.filter_rule_invalid",
  "route.forward_destinations_missing": "policies.reject.route.forward_destinations_missing",
  "route.intent_missing": "policies.reject.route.intent_missing",
  "route.invalid_destination": "policies.reject.route.invalid_destination",
  "route.ladder_empty": "policies.reject.route.ladder_empty",
  "route.restricted_command_invalid": "policies.reject.route.restricted_command_invalid",
  "route.restricted_exec_empty": "policies.reject.route.restricted_exec_empty",
  "route.session_deadline_invalid": "policies.reject.route.session_deadline_invalid",
  "route.subsystems_missing": "policies.reject.route.subsystems_missing",
  "rule.allow_needs_route": "policies.reject.rule.allow_needs_route",
  "rule.deny_carries_route": "policies.reject.rule.deny_carries_route",
  "rule.deny_needs_reason": "policies.reject.rule.deny_needs_reason",
  "rule.effect_missing": "policies.reject.rule.effect_missing",
  "rule.id_duplicate": "policies.reject.rule.id_duplicate",
  "rule.id_missing": "policies.reject.rule.id_missing",
  "rule.unreachable": "policies.reject.rule.unreachable",
  "scope.name_invalid": "policies.reject.scope.name_invalid",
  "scope.privileged_fails_open": "policies.reject.scope.privileged_fails_open",
};

/** Each of Control's publish-time finding codes, by its sentence. A validator's are its own. */
export const FINDING: Readonly<Record<string, MessageKey>> = {
  "algorithm.axis_emptied": "policies.finding.algorithm.axis_emptied",
  "algorithm.ban_unmatched": "policies.finding.algorithm.ban_unmatched",
  "algorithm.declaration_unknown": "policies.finding.algorithm.declaration_unknown",
  "algorithm.floor_undeclared": "policies.finding.algorithm.floor_undeclared",
  "algorithm.policy_unmet": "policies.finding.algorithm.policy_unmet",
  "algorithm.policy_unmet_truncated": "policies.finding.algorithm.policy_unmet_truncated",
  "algorithm.target_below_floor": "policies.finding.algorithm.target_below_floor",
  "algorithm.target_not_reported": "policies.finding.algorithm.target_not_reported",
  "authoring.cache_hint_outlives_condition": "policies.finding.authoring.cache_hint_outlives_condition",
  "authoring.cache_hint_spans_sources": "policies.finding.authoring.cache_hint_spans_sources",
  "authoring.interpreter_in_allow_list": "policies.finding.authoring.interpreter_in_allow_list",
  "satisfiability.no_proxy_provides": "policies.finding.satisfiability.no_proxy_provides",
  "satisfiability.proxies_unreported": "policies.finding.satisfiability.proxies_unreported",
  "satisfiability.target_cannot_take": "policies.finding.satisfiability.target_cannot_take",
  "satisfiability.target_unproven": "policies.finding.satisfiability.target_unproven",
};

export interface RenderedCode {
  text: string;
  /** Whether the console knew the code: false is the named fallback. */
  known: boolean;
}

/** A rejection's sentence, from its code and parameters. */
export function rejectionText(t: Translate, r: Rejection): RenderedCode {
  const key = REJECTION[r.code];
  if (key) {
    try {
      return { text: t(key, rejectionValues(r)), known: true };
    } catch {
      // A parameter the sentence names did not arrive: say which code it was.
    }
  }
  return { text: t("policies.reject.unknown", { code: `\u2068${r.code}\u2069` }), known: false };
}

/** A finding's sentence, from its code and parameters. */
export function findingText(t: Translate, f: Finding): RenderedCode {
  const key = f.source === "control" ? FINDING[f.code] : undefined;
  const values = findingValues(f);
  if (key) {
    try {
      return { text: t(key, values), known: true };
    } catch {
      // As above.
    }
  }
  return { text: t("policies.finding.unknown", values), known: false };
}
