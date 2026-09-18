// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Rejections are this package's product surface, so they are structured rather
// than only prose (PLAN M21).
//
// A policy author reads the English message, `policyctl` and CI print it, and
// the console (0016) renders the Code in the operator's locale. Prose alone
// cannot be localised, and this package is pure — it has no locale and must
// never acquire one. So every rejection carries a stable Code, the typed
// Params that filled its message in, and the rule and line it is about.
//
// Codes are stable identifiers. Reword a message freely; never reuse a code for
// a different rejection.

// Code identifies a rejection. Every member has a message template in
// `messages`, and the `exhaustive` linter's map check is what keeps that true:
// adding a code without a message fails the build rather than producing a
// rejection nobody can read.
type Code string

// Document-scope codes: the bundle as a whole.
const (
	CodeDocumentMalformed        Code = "policy.document_malformed"
	CodeSchemaVersionUnsupported Code = "policy.schema_version_unsupported"
	CodeTenantMissing            Code = "policy.tenant_missing"
	CodeNoRules                  Code = "policy.no_rules"
	CodeTimezoneUnknown          Code = "policy.timezone_unknown"
	CodeLabelVocabularyMalformed Code = "policy.label_vocabulary_malformed"
	CodeGroupVocabularyMalformed Code = "policy.group_vocabulary_malformed"
)

// Rule-scope codes: the shape of one rule.
const (
	CodeRuleIDMissing        Code = "rule.id_missing"
	CodeRuleIDDuplicate      Code = "rule.id_duplicate"
	CodeRuleUnreachable      Code = "rule.unreachable"
	CodeRuleEffectMissing    Code = "rule.effect_missing"
	CodeRuleDenyCarriesRoute Code = "rule.deny_carries_route"
	CodeRuleAllowNeedsRoute  Code = "rule.allow_needs_route"
	CodeRuleDenyNeedsReason  Code = "rule.deny_needs_reason"
)

// Match-scope codes: a rule's inputs.
const (
	CodeMatchUnknownGroup    Code = "match.unknown_group"
	CodeMatchUnknownLabelKey Code = "match.unknown_label_key"
	CodeMatchUnknownLabel    Code = "match.unknown_label_value"
	CodeMatchInvalidCIDR     Code = "match.invalid_cidr"
	CodeMatchInvalidHostname Code = "match.invalid_hostname_pattern"
	CodeMatchEmptyTerm       Code = "match.empty_term"
)

// Route-scope codes: the snapshot a rule emits.
const (
	CodeRouteIntentMissing         Code = "route.intent_missing"
	CodeRouteChannelsMissing       Code = "route.channels_missing"
	CodeRouteFilterModeMissing     Code = "route.filter_mode_missing"
	CodeRouteForwardsUnconstrained Code = "route.forward_destinations_missing"
	CodeRouteSubsystemsMissing     Code = "route.subsystems_missing"
	CodeRouteFilterBothTiers       Code = "route.filter_policy_both_tiers"
	CodeRouteRestrictedExecEmpty   Code = "route.restricted_exec_empty"
	CodeRouteInvalidDestination    Code = "route.invalid_destination"
	CodeRouteLadderEmpty           Code = "route.ladder_empty"
	CodeRouteExecModeMismatch      Code = "route.exec_mode_mismatch"
	CodeRouteFilterRuleInvalid     Code = "route.filter_rule_invalid"
	CodeRouteRestrictedCmdInvalid  Code = "route.restricted_command_invalid"
	CodeRouteDeadlineInvalid       Code = "route.session_deadline_invalid"
	CodeRouteConcurrencyInvalid    Code = "route.concurrency_invalid"
)

// Cache-scope codes (PLAN §5.4).
const (
	CodeCacheKeyEmpty            Code = "cache.key_empty"
	CodeCacheKeyNotIdentityBound Code = "cache.key_not_identity_bound"
	CodeCacheTTLInvalid          Code = "cache.ttl_invalid"
	CodeCacheKeyDuplicate        Code = "cache.key_component_duplicate"
	CodeCacheOnDeny              Code = "cache.hint_on_deny"
)

// Credential-scope codes: one entry of the target-auth ladder.
const (
	CodeCredentialMethodMissing    Code = "credential.method_missing"
	CodeCredentialUsernameMissing  Code = "credential.username_missing"
	CodeCredentialUsernameEmpty    Code = "credential.username_empty"
	CodeCredentialParamWrongMethod Code = "credential.param_wrong_method"
	CodeCredentialPlatformMissing  Code = "credential.platform_missing"
	CodeCredentialKindMissing      Code = "credential.credential_kind_missing"
	CodeCredentialPostureMissing   Code = "credential.expiry_posture_missing"
	CodeCredentialLifetimeMissing  Code = "credential.lifetime_missing"
	CodeCredentialLifetimeInvalid  Code = "credential.lifetime_invalid"
	CodeDeviceFieldName            Code = "credential.device_field_name_invalid"
	CodeDeviceFieldValue           Code = "credential.device_field_value_invalid"
	CodeDeviceFieldCount           Code = "credential.device_field_too_many"
	CodeDeviceFieldWrongMethod     Code = "credential.device_field_wrong_method"
)

// Enforcement-scope codes: the rung must agree with the rest of the rule.
const (
	CodeEnforcementShellPermitted        Code = "enforcement.interactive_shell_permitted"
	CodeEnforcementRequestsMissing       Code = "enforcement.permitted_requests_missing"
	CodeEnforcementExecModeRequired      Code = "enforcement.restricted_exec_required"
	CodeEnforcementRoleMissing           Code = "enforcement.platform_role_missing"
	CodeEnforcementRoleUnexpected        Code = "enforcement.platform_role_unexpected"
	CodeEnforcementDestsRequired         Code = "enforcement.destinations_required"
	CodeEnforcementAttestationMissing    Code = "enforcement.attestation_missing"
	CodeEnforcementAttestationUnexpected Code = "enforcement.attestation_unexpected"
	CodeEnforcementAppliedUnprovisioned  Code = "enforcement.applied_rung_unprovisioned"
)

// Obligation-scope codes.
const (
	CodeObligationKindMissing Code = "obligation.kind_missing"
	CodeObligationDuplicate   Code = "obligation.duplicate"
	CodeObligationOnDeny      Code = "obligation.on_deny"
	CodeObligationContradicts Code = "obligation.contradicts_route"
)

// messages is the English catalogue, one entry per Code. `{name}` is replaced
// from Rejection.Params.
//
// This map literal is keyed by an enum, so `exhaustive` checks it: a Code with
// no message here fails the build. That is deliberate — an unreadable rejection
// is a rejection an author cannot act on, and this is the one place the
// omission is cheap to catch.
var messages = map[Code]string{
	CodeDocumentMalformed:        "the bundle is not valid YAML: {detail}",
	CodeSchemaVersionUnsupported: "schema_version {found} is not supported; this server understands {supported}",
	CodeTenantMissing:            "the bundle names no tenant; add `tenant: <name>` (the tenant selects which compiled program is served)",
	CodeNoRules:                  "the bundle has no rules; add at least one, or delete the bundle — an empty bundle denies everything by default-deny and says nothing about why",
	CodeTimezoneUnknown:          "timezone {timezone} is not a known IANA location; use a name such as `Europe/Lisbon`, or omit the key for UTC",
	CodeLabelVocabularyMalformed: "label key {label_key} declares no values; list the values a rule may match, or remove the key",
	CodeGroupVocabularyMalformed: "the group list contains an empty name; remove it",

	CodeRuleIDMissing:        "the rule has no id; give it one — the id is what an explanation names and what an operator searches for",
	CodeRuleIDDuplicate:      "rule id {id} is already used at line {other_line}; ids identify a rule in every explanation and audit record, so they must be unique",
	CodeRuleUnreachable:      "this rule can never match: rule {shadowed_by} at line {other_line} already matches everything it does; narrow this rule, or move it above {shadowed_by}",
	CodeRuleEffectMissing:    "the rule states no effect; add `effect: allow` or `effect: deny`",
	CodeRuleDenyCarriesRoute: "a deny rule carries a route; a denied connection has no snapshot, so remove the `route:` block or change the effect to allow",
	CodeRuleAllowNeedsRoute:  "an allow rule carries no route; add a `route:` block — an allow with no snapshot is a connection with no policy",
	CodeRuleDenyNeedsReason:  "a deny rule carries no reason; add `reason:` — it is what the operator reads when they resolve the decision id",

	CodeMatchUnknownGroup:    "group {group} is not declared in the bundle's `groups:` list; add it there, or correct the spelling",
	CodeMatchUnknownLabelKey: "label key {label_key} is not declared in the bundle's `labels:` map; add it there, or correct the spelling",
	CodeMatchUnknownLabel:    "label {label_key}={label_value} is not declared in the bundle's `labels:` map; add the value there, or correct the spelling",
	CodeMatchInvalidCIDR:     "{cidr} is not a valid address or CIDR prefix",
	CodeMatchInvalidHostname: "{pattern} is not a valid hostname pattern; use an exact name or a single leading wildcard such as `*.prod.example.com`",
	CodeMatchEmptyTerm:       "the {axis} match declares {term} with no values; list at least one, or remove the key — an empty list matches nothing and is almost never what was meant",

	CodeRouteIntentMissing:         "the route states no intent; add `intent: direct` or `intent: hops-permitted`",
	CodeRouteChannelsMissing:       "the route names no `channels:` key; state the channel types explicitly — `channels: []` denies all of them and is a policy, but an omission is not",
	CodeRouteFilterModeMissing:     "the route's filter policy states no mode; add `mode: whitelist` or `mode: blacklist` — a filter policy with no defined default can fail open by omission",
	CodeRouteForwardsUnconstrained: "the route permits the {channel} channel but names no forwarding destinations; add `forwards.{list}:` — an unconstrained forwarding axis is a mistake, not a wildcard",
	CodeRouteSubsystemsMissing:     "the route permits subsystems but names none; list them — `sftp` is deniable on its own only if subsystems are named individually",
	CodeRouteFilterBothTiers:       "the route's filter policy sets both a rule list and a restricted-exec allow-list; the two tiers are alternatives, never layers — keep one",
	CodeRouteRestrictedExecEmpty:   "the route sets `exec_mode: restricted` but names no commands; restricted exec is a default-deny allow-list, so an empty one denies every exec — say so with an empty `channels:`/`requests:` instead if that is the intent",
	CodeRouteInvalidDestination:    "{destination} is not a valid forwarding destination; use a host, a wildcard such as `*.prod`, or a CIDR prefix, with an optional port or port range",
	CodeRouteLadderEmpty:           "the route declares a `credentials:` ladder with no entries; list at least one method, or omit the key to leave the proxy its locally configured method",
	CodeRouteExecModeMismatch:      "the route declares a restricted-exec allow-list but its `exec_mode` is not `restricted`; the allow-list would never decide anything — set `exec_mode: restricted`, or remove it",
	CodeRouteFilterRuleInvalid:     "filter rule {index} is not usable: {detail}",
	CodeRouteRestrictedCmdInvalid:  "restricted-exec command {index} ({executable}) is not usable: {detail}",
	CodeRouteDeadlineInvalid:       "session deadline {duration} is not a positive duration; use a form such as `8h` or `45m`",
	CodeRouteConcurrencyInvalid:    "concurrency cap {field} is negative; use a positive cap, or 0 for uncapped",

	CodeCacheKeyEmpty:            "the rule's cache hint names no key components; a hint with no key is not cacheable — list the components the key is derived from, or remove the hint",
	CodeCacheKeyNotIdentityBound: "the rule's cache key does not include `subject`; a key shared across identities serves one user another user's policy",
	CodeCacheTTLInvalid:          "cache ttl_seconds {ttl} is not positive; a hint with no lifetime is not a hint — remove it, or give it a lifetime",
	CodeCacheOnDeny:              "a deny rule carries a cache hint; a denial is not a response the proxy may reuse, so the hint would never be read — remove it",
	CodeCacheKeyDuplicate:        "cache key component {component} is listed twice; remove the duplicate",

	CodeCredentialMethodMissing:    "ladder entry {index} names no method; every entry names one of the four the contract defines",
	CodeCredentialUsernameMissing:  "ladder entry {index} ({method}) names no username; every method the contract defines requires one, and the proxy refuses the route at the first authorize call — name the account, and never derive it from the identity's `login`",
	CodeCredentialUsernameEmpty:    "ladder entry {index} declares a literal username with no value; write the account name",
	CodeCredentialParamWrongMethod: "ladder entry {index} sets {param}, which method {method} does not take; remove it, or change the method",
	CodeCredentialPlatformMissing:  "ladder entry {index} is `ephemeral-account` and names no platform; it is never inferred, because guessing wrong runs configuration commands against the wrong parser",
	CodeCredentialKindMissing:      "ladder entry {index} is `ephemeral-account` and names no credential_kind; choose `password` or `publickey` — defaulting would hand out the weaker of two materially different exposures",
	CodeCredentialPostureMissing:   "ladder entry {index} is `ephemeral-account` and names no expiry_posture; choose `target-enforced`, `proxy-enforced` or `accepted-risk`",
	CodeCredentialLifetimeMissing:  "ladder entry {index} has expiry_posture {posture} and no lifetime_seconds; a posture that enforces an expiry with no expiry to enforce is a statement with no content",
	CodeCredentialLifetimeInvalid:  "ladder entry {index} has lifetime_seconds {lifetime}; it must be positive",
	CodeDeviceFieldName:            "ladder entry {index} declares device field {field}: a name must be 1 to {max} characters of lowercase letters, digits, hyphens and underscores",
	CodeDeviceFieldValue:           "ladder entry {index} declares device field {field} with a value that is empty or longer than {max} characters",
	CodeDeviceFieldCount:           "ladder entry {index} declares {count} device fields; at most {max} ride on one entry",
	CodeDeviceFieldWrongMethod:     "ladder entry {index} declares device field {field} on method {method}; the namespace is scoped to `ephemeral-account`, and anywhere else it is a typo that would be carried silently",

	CodeEnforcementShellPermitted:        "enforcement.execution is `no-interactive-shell` but the route still permits {request}; deny both `shell` and `pty-req`, or choose a rung the route actually stands on",
	CodeEnforcementRequestsMissing:       "enforcement.execution is `no-interactive-shell` but the route names no `requests:` allow-list; an unpoliced request axis permits a shell, so the claim cannot hold",
	CodeEnforcementExecModeRequired:      "enforcement.execution is {rung} but the route's filter policy is not `exec_mode: restricted`; that rung is the restricted allow-list rendered onto the account, so the two must agree",
	CodeEnforcementRoleMissing:           "enforcement.execution is `platform-authorized` but no `platform_role` is named; the role is the authorization the rung claims",
	CodeEnforcementRoleUnexpected:        "a `platform_role` is named but enforcement.execution is {rung}; the role belongs to `platform-authorized` and means nothing elsewhere",
	CodeEnforcementDestsRequired:         "enforcement.reach is `account-egress-restricted` but no `permitted_destinations` are named; the destination list is what the restriction is made of",
	CodeEnforcementAttestationMissing:    "enforcement.{axis} is `platform-attested` but carries no attestation; an attested rung is a claim this system does not verify, so it must name who asserts it (`asserted_by`) and the evidence (`reference`)",
	CodeEnforcementAttestationUnexpected: "an attestation is carried but no axis is `platform-attested` (execution is {execution}, reach is {reach}); an applied rung is configured by the proxy and has nobody to attribute",
	CodeEnforcementAppliedUnprovisioned:  "enforcement.{axis} is {rung}, which the proxy applies to an account it administers, but every ladder entry is `brokered-key` or `static-key`; the proxy refuses that response outright — add an `ephemeral-user` or `ephemeral-account` entry, or use `platform-attested` if the target already enforces it",

	CodeObligationKindMissing: "obligation {index} names no kind; use `record-session`, `require-approval` or `require-step-up`",
	CodeObligationDuplicate:   "obligation {obligation} is listed twice; remove the duplicate",
	CodeObligationOnDeny:      "a deny rule carries obligation {obligation}; obligations are things a permitted session must do, and a denied one has none",
	CodeObligationContradicts: "obligation {obligation} contradicts the route: {detail}",
}

// Rejection is one reason a bundle was refused. It is a value, not a sentence:
// Code and Params are what the console renders, Message is what a terminal
// prints, and neither is derived from the other after the fact.
type Rejection struct {
	// Code is the stable identifier. It never changes meaning.
	Code Code
	// Rule is the id of the rule this is about, empty at document scope.
	Rule string
	// Line is the 1-based line in the authored source, 0 when the
	// rejection is about the document rather than a place in it.
	Line int
	// Params are the typed values that filled the message in. They are the
	// localisable payload: the console reads these, not the English.
	Params map[string]string
}

// Reject builds a Rejection. Params arrive as alternating key/value pairs,
// which keeps call sites to one line; an odd count is a programming error and
// panics in tests long before it reaches an author.
func Reject(code Code, rule string, line int, kv ...string) Rejection {
	if len(kv)%2 != 0 {
		panic("model: Reject called with an odd number of parameter strings")
	}
	var params map[string]string
	if len(kv) > 0 {
		params = make(map[string]string, len(kv)/2)
		for i := 0; i < len(kv); i += 2 {
			params[kv[i]] = kv[i+1]
		}
	}
	return Rejection{Code: code, Rule: rule, Line: line, Params: params}
}

// Message renders the English message with this rejection's parameters
// substituted. It is a convenience for a terminal; Code and Params are the
// rejection.
func (r Rejection) Message() string {
	tmpl, ok := messages[r.Code]
	if !ok {
		// Unreachable while the exhaustive map check holds, but a
		// rejection nobody can read must still say which code it was.
		return string(r.Code)
	}
	if len(r.Params) == 0 {
		return tmpl
	}
	keys := slices.Sorted(maps.Keys(r.Params))
	pairs := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		pairs = append(pairs, "{"+k+"}", r.Params[k])
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// Error renders the rejection the way `policyctl` and CI print it: where it is,
// which rule it is about, and what to do instead.
func (r Rejection) Error() string {
	var b strings.Builder
	if r.Line > 0 {
		fmt.Fprintf(&b, "line %d: ", r.Line)
	}
	if r.Rule != "" {
		fmt.Fprintf(&b, "rule %q: ", r.Rule)
	}
	b.WriteString(r.Message())
	fmt.Fprintf(&b, " [%s]", r.Code)
	return b.String()
}

// Rejections is every reason a bundle was refused, in source order.
//
// The whole list is returned rather than the first one: an author fixing a
// bundle one compile at a time is an author who stops trusting the compiler.
type Rejections []Rejection

// Error renders every rejection, one per line.
func (rs Rejections) Error() string {
	if len(rs) == 0 {
		return "policy: rejected, but no reason was recorded"
	}
	parts := make([]string, 0, len(rs)+1)
	parts = append(parts, fmt.Sprintf("policy: %s", plural(len(rs), "rejection", "rejections")))
	for _, r := range rs {
		parts = append(parts, "  "+r.Error())
	}
	return strings.Join(parts, "\n")
}

// Has reports whether any rejection carries this code. It is what tests and
// the console's "did this specific thing go wrong" checks read.
func (rs Rejections) Has(code Code) bool {
	return slices.ContainsFunc(rs, func(r Rejection) bool { return r.Code == code })
}

// Codes lists the codes present, in order and without repeats.
func (rs Rejections) Codes() []Code {
	var out []Code
	for _, r := range rs {
		if !slices.Contains(out, r.Code) {
			out = append(out, r.Code)
		}
	}
	return out
}

// Sorted returns the rejections in a deterministic order — by line, then by
// code, then by rule. The same bundle must be refused with the same text every
// time, or a CI diff of two compiles is unreadable.
func (rs Rejections) Sorted() Rejections {
	out := slices.Clone(rs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
