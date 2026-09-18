// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package compile_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/model"
)

// compileSource parses and compiles, returning whatever refused it. Parse and
// Compile are both refusals an author sees in the same place, so a test for one
// should not have to know which of the two produced the message.
func compileSource(t *testing.T, src string) model.Rejections {
	t.Helper()
	b, err := model.Parse([]byte(src))
	if err != nil {
		return asRejections(t, err)
	}
	if _, err := compile.Compile(b); err != nil {
		return asRejections(t, err)
	}
	return nil
}

func asRejections(t *testing.T, err error) model.Rejections {
	t.Helper()
	var rs model.Rejections
	if !errors.As(err, &rs) {
		t.Fatalf("error is not model.Rejections: %v", err)
	}
	return rs
}

func mustCompile(t *testing.T, src string) *compile.Program {
	t.Helper()
	b, err := model.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse:\n%v", err)
	}
	prog, err := compile.Compile(b)
	if err != nil {
		t.Fatalf("compile:\n%v", err)
	}
	return prog
}

// placeholder catches a message that was built with a parameter nobody
// supplied. An author reading "label {label_key} is not declared" has been told
// nothing, so an unsubstituted placeholder is a bug in the rejection rather
// than a cosmetic problem.
var placeholder = regexp.MustCompile(`\{[a-z_]+\}`)

// rejectionCase is one bundle and the code it must be refused with.
type rejectionCase struct {
	name string
	code model.Code
	// rule is the rule id the rejection must name, empty for a
	// document-scope rejection.
	rule string
	// wants are substrings the message must contain, so the test asserts the
	// message is actionable rather than merely present.
	wants []string
	src   string
}

func TestCompilerRejections(t *testing.T) {
	for _, tc := range rejectionCases {
		t.Run(tc.name, func(t *testing.T) {
			rs := compileSource(t, tc.src)
			if rs == nil {
				t.Fatalf("the bundle compiled; expected %s", tc.code)
			}
			var found *model.Rejection
			for i := range rs {
				if rs[i].Code == tc.code {
					found = &rs[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("expected %s, got %v\n%v", tc.code, rs.Codes(), rs)
			}
			if found.Rule != tc.rule {
				t.Errorf("rejection names rule %q, want %q", found.Rule, tc.rule)
			}
			if tc.rule != "" && found.Line == 0 {
				t.Error("a rejection about a rule must name the line it is on")
			}
			msg := found.Message()
			if m := placeholder.FindString(msg); m != "" {
				t.Errorf("message has an unsubstituted parameter %s: %s", m, msg)
			}
			for _, want := range tc.wants {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not mention %q: %s", want, msg)
				}
			}
			// The rendered error is what policyctl and CI print: it has to
			// place the rejection and carry the stable code.
			rendered := found.Error()
			if !strings.Contains(rendered, string(tc.code)) {
				t.Errorf("rendered error does not carry the code: %s", rendered)
			}
			if tc.rule != "" && !strings.Contains(rendered, tc.rule) {
				t.Errorf("rendered error does not name the rule: %s", rendered)
			}
		})
	}
}

// allowRoute is a minimal, valid route, so that a case about one thing is not
// also a case about six others.
const allowRoute = `{intent: direct, channels: [session], filter: {mode: whitelist}}`

func header(extra string) string {
	return "schema_version: 1\ntenant: acme\n" + extra
}

var rejectionCases = []rejectionCase{
	// -- the document ------------------------------------------------------
	{
		name: "malformed document",
		code: model.CodeDocumentMalformed,
		src:  "schema_version: 1\ntenant: acme\nrules: [ unterminated",
	},
	{
		name:  "unknown enum member",
		code:  model.CodeDocumentMalformed,
		wants: []string{"channel type"},
		src: header(`rules:
  - id: r
    effect: allow
    route: {intent: direct, channels: [teleport], filter: {mode: whitelist}}
`),
	},
	{
		name:  "unknown key",
		code:  model.CodeDocumentMalformed,
		wants: []string{"field"},
		src: header(`rules:
  - id: r
    effect: allow
    permissions: root
    route: ` + allowRoute + `
`),
	},
	{
		name:  "unsupported schema version",
		code:  model.CodeSchemaVersionUnsupported,
		wants: []string{"2"},
		src:   "schema_version: 2\ntenant: acme\nrules: [{id: r, effect: allow, route: " + allowRoute + "}]\n",
	},
	{
		name: "no tenant",
		code: model.CodeTenantMissing,
		src:  "schema_version: 1\nrules: [{id: r, effect: allow, route: " + allowRoute + "}]\n",
	},
	{
		name: "no rules",
		code: model.CodeNoRules,
		src:  "schema_version: 1\ntenant: acme\nrules: []\n",
	},
	{
		name:  "unknown timezone",
		code:  model.CodeTimezoneUnknown,
		wants: []string{"Mars/Olympus"},
		src:   header("timezone: Mars/Olympus\nrules: [{id: r, effect: allow, route: " + allowRoute + "}]\n"),
	},
	{
		name:  "label key declaring no values",
		code:  model.CodeLabelVocabularyMalformed,
		wants: []string{"env"},
		src:   header("labels:\n  env: []\nrules: [{id: r, effect: allow, route: " + allowRoute + "}]\n"),
	},
	{
		name: "empty group name",
		code: model.CodeGroupVocabularyMalformed,
		src:  header("groups: [\"\"]\nrules: [{id: r, effect: allow, route: " + allowRoute + "}]\n"),
	},

	// -- the rule ----------------------------------------------------------
	{
		name: "rule without an id",
		code: model.CodeRuleIDMissing,
		src: header(`rules:
  - effect: allow
    route: ` + allowRoute + `
`),
	},
	{
		name:  "duplicate rule id",
		code:  model.CodeRuleIDDuplicate,
		rule:  "twice",
		wants: []string{"twice"},
		src: header(`rules:
  - {id: twice, effect: allow, route: ` + allowRoute + `}
  - {id: twice, effect: deny, reason: no}
`),
	},
	{
		name:  "unreachable rule",
		code:  model.CodeRuleUnreachable,
		rule:  "narrower",
		wants: []string{"wider"},
		src: header(`labels:
  env: [prod]
rules:
  - {id: wider, effect: allow, match: {target: {labels: {env: [prod]}}}, route: ` + allowRoute + `}
  - {id: narrower, effect: deny, reason: no, match: {target: {labels: {env: [prod]}, zones: [dc1]}}}
`),
	},
	{
		name: "rule without an effect",
		code: model.CodeRuleEffectMissing,
		rule: "r",
		src: header(`rules:
  - id: r
    route: ` + allowRoute + `
`),
	},
	{
		name: "deny carrying a route",
		code: model.CodeRuleDenyCarriesRoute,
		rule: "r",
		src: header(`rules:
  - {id: r, effect: deny, reason: no, route: ` + allowRoute + `}
`),
	},
	{
		name: "allow without a route",
		code: model.CodeRuleAllowNeedsRoute,
		rule: "r",
		src:  header("rules:\n  - {id: r, effect: allow}\n"),
	},
	{
		name: "deny without a reason",
		code: model.CodeRuleDenyNeedsReason,
		rule: "r",
		src:  header("rules:\n  - {id: r, effect: deny}\n"),
	},

	// -- the match ---------------------------------------------------------
	{
		name:  "group that does not exist",
		code:  model.CodeMatchUnknownGroup,
		rule:  "r",
		wants: []string{"sre"},
		src: header(`groups: [dba]
rules:
  - {id: r, effect: allow, match: {subject: {groups: [sre]}}, route: ` + allowRoute + `}
`),
	},
	{
		name:  "label key that does not exist",
		code:  model.CodeMatchUnknownLabelKey,
		rule:  "r",
		wants: []string{"tier"},
		src: header(`labels:
  env: [prod]
rules:
  - {id: r, effect: allow, match: {target: {labels: {tier: [gold]}}}, route: ` + allowRoute + `}
`),
	},
	{
		name:  "label value that does not exist",
		code:  model.CodeMatchUnknownLabel,
		rule:  "r",
		wants: []string{"env", "production"},
		src: header(`labels:
  env: [prod]
rules:
  - {id: r, effect: allow, match: {target: {labels: {env: [production]}}}, route: ` + allowRoute + `}
`),
	},
	{
		name:  "malformed CIDR",
		code:  model.CodeMatchInvalidCIDR,
		rule:  "r",
		wants: []string{"10.0.0.0/64"},
		src: header(`rules:
  - {id: r, effect: allow, match: {context: {source_cidrs: ["10.0.0.0/64"]}}, route: ` + allowRoute + `}
`),
	},
	{
		name:  "malformed hostname pattern",
		code:  model.CodeMatchInvalidHostname,
		rule:  "r",
		wants: []string{"db*.prod"},
		src: header(`rules:
  - {id: r, effect: allow, match: {target: {hostnames: ["db*.prod"]}}, route: ` + allowRoute + `}
`),
	},
	{
		name:  "term opened and left empty",
		code:  model.CodeMatchEmptyTerm,
		rule:  "r",
		wants: []string{"subject", "groups"},
		src: header(`groups: [sre]
rules:
  - {id: r, effect: allow, match: {subject: {groups: []}}, route: ` + allowRoute + `}
`),
	},

	// -- the route ---------------------------------------------------------
	{
		name: "route without an intent",
		code: model.CodeRouteIntentMissing,
		rule: "r",
		src:  header("rules:\n  - {id: r, effect: allow, route: {channels: [session], filter: {mode: whitelist}}}\n"),
	},
	{
		name: "route naming no channels key",
		code: model.CodeRouteChannelsMissing,
		rule: "r",
		src:  header("rules:\n  - {id: r, effect: allow, route: {intent: direct, filter: {mode: whitelist}}}\n"),
	},
	{
		name: "filter policy without a mode",
		code: model.CodeRouteFilterModeMissing,
		rule: "r",
		src:  header("rules:\n  - {id: r, effect: allow, route: {intent: direct, channels: [session], filter: {}}}\n"),
	},
	{
		name:  "direct-tcpip with no destination list",
		code:  model.CodeRouteForwardsUnconstrained,
		rule:  "r",
		wants: []string{"direct-tcpip", "direct_tcpip"},
		src: header(`rules:
  - {id: r, effect: allow, route: {intent: direct, channels: [session, direct-tcpip], filter: {mode: whitelist}}}
`),
	},
	{
		name:  "forwarded-tcpip with no destination list",
		code:  model.CodeRouteForwardsUnconstrained,
		rule:  "r",
		wants: []string{"forwarded-tcpip"},
		src: header(`rules:
  - {id: r, effect: allow, route: {intent: direct, channels: [forwarded-tcpip], filter: {mode: whitelist}}}
`),
	},
	{
		name: "subsystem permission naming no subsystems",
		code: model.CodeRouteSubsystemsMissing,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      requests: {types: [exec], subsystems: []}
      filter: {mode: whitelist}
`),
	},
	{
		name: "both filter tiers at once",
		code: model.CodeRouteFilterBothTiers,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        rules: [{match: "rm -rf *", action: block_command}]
        restricted_exec:
          commands: [{executable: /bin/ls, form: exact, argv: ["-l"]}]
`),
	},
	{
		name: "restricted exec declared under the wrong exec mode",
		code: model.CodeRouteExecModeMismatch,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: filtered
        restricted_exec:
          commands: [{executable: /bin/ls, form: exact, argv: ["-l"]}]
`),
	},
	{
		name: "restricted exec with no commands",
		code: model.CodeRouteRestrictedExecEmpty,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist, exec_mode: restricted}
`),
	},
	{
		name:  "malformed forwarding destination",
		code:  model.CodeRouteInvalidDestination,
		rule:  "r",
		wants: []string{"db*.prod"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session, direct-tcpip]
      forwards: {direct_tcpip: [{host: "db*.prod", port: 5432}]}
      filter: {mode: whitelist}
`),
	},
	{
		name: "ladder declared with no entries",
		code: model.CodeRouteLadderEmpty,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route: {intent: direct, channels: [session], filter: {mode: whitelist}, credentials: []}
`),
	},
	{
		name:  "negative session deadline",
		code:  model.CodeRouteDeadlineInvalid,
		rule:  "r",
		wants: []string{"-1h"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      max_session_duration: -1h
`),
	},
	{
		name:  "negative concurrency cap",
		code:  model.CodeRouteConcurrencyInvalid,
		rule:  "r",
		wants: []string{"max_sessions_per_subject"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      concurrency: {max_sessions_per_subject: -1}
`),
	},
	{
		name: "filter rule matching nothing",
		code: model.CodeRouteFilterRuleInvalid,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter:
        mode: blacklist
        rules: [{match: "", action: block_command}]
`),
	},
	{
		name:  "restricted command with the wrong argument shape",
		code:  model.CodeRouteRestrictedCmdInvalid,
		rule:  "r",
		wants: []string{"/bin/ls"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter:
        mode: whitelist
        exec_mode: restricted
        restricted_exec:
          commands: [{executable: /bin/ls, form: positional, args: [{kind: oneof}]}]
`),
	},

	// -- the cache hint ----------------------------------------------------
	{
		name: "cache hint with no key",
		code: model.CodeCacheKeyEmpty,
		rule: "r",
		src: header(`rules:
  - {id: r, effect: allow, route: ` + allowRoute + `, cache: {key: [], ttl_seconds: 60}}
`),
	},
	{
		name: "cache key shared across identities",
		code: model.CodeCacheKeyNotIdentityBound,
		rule: "r",
		src: header(`rules:
  - {id: r, effect: allow, route: ` + allowRoute + `, cache: {key: [target, rule], ttl_seconds: 60}}
`),
	},
	{
		name:  "cache hint with no lifetime",
		code:  model.CodeCacheTTLInvalid,
		rule:  "r",
		wants: []string{"0"},
		src: header(`rules:
  - {id: r, effect: allow, route: ` + allowRoute + `, cache: {key: [subject], ttl_seconds: 0}}
`),
	},
	{
		name:  "duplicate cache key component",
		code:  model.CodeCacheKeyDuplicate,
		rule:  "r",
		wants: []string{"subject"},
		src: header(`rules:
  - {id: r, effect: allow, route: ` + allowRoute + `, cache: {key: [subject, subject], ttl_seconds: 60}}
`),
	},
	{
		name: "cache hint on a deny",
		code: model.CodeCacheOnDeny,
		rule: "r",
		src: header(`rules:
  - {id: r, effect: deny, reason: no, cache: {key: [subject], ttl_seconds: 60}}
`),
	},

	// -- the credential ladder ---------------------------------------------
	{
		name:  "ladder entry with no username",
		code:  model.CodeCredentialUsernameMissing,
		rule:  "r",
		wants: []string{"static-key", "login"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials: [{method: static-key}]
`),
	},
	{
		name: "ladder entry with an empty username",
		code: model.CodeCredentialUsernameEmpty,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials: [{method: static-key, username: ""}]
`),
	},
	{
		name:  "parameter belonging to another method",
		code:  model.CodeCredentialParamWrongMethod,
		rule:  "r",
		wants: []string{"key_type", "brokered-key"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials: [{method: brokered-key, username: svc, key_type: ed25519}]
`),
	},
	{
		name: "ephemeral-account with no platform",
		code: model.CodeCredentialPlatformMissing,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - {method: ephemeral-account, username: jit, credential_kind: password, expiry_posture: accepted-risk}
`),
	},
	{
		name: "ephemeral-account with no credential kind",
		code: model.CodeCredentialKindMissing,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - {method: ephemeral-account, username: jit, platform: fortios, expiry_posture: accepted-risk}
`),
	},
	{
		name: "ephemeral-account with no expiry posture",
		code: model.CodeCredentialPostureMissing,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - {method: ephemeral-account, username: jit, platform: fortios, credential_kind: password}
`),
	},
	{
		name:  "enforced expiry with no lifetime to enforce",
		code:  model.CodeCredentialLifetimeMissing,
		rule:  "r",
		wants: []string{"proxy-enforced"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - method: ephemeral-account
          username: jit
          platform: fortios
          credential_kind: password
          expiry_posture: proxy-enforced
`),
	},
	{
		name:  "negative lifetime",
		code:  model.CodeCredentialLifetimeInvalid,
		rule:  "r",
		wants: []string{"-5"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - {method: ephemeral-user, username: jit, lifetime_seconds: -5}
`),
	},
	{
		name:  "device field with a malformed name",
		code:  model.CodeDeviceFieldName,
		rule:  "r",
		wants: []string{"VDOM"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - method: ephemeral-account
          username: jit
          platform: fortios
          credential_kind: password
          expiry_posture: accepted-risk
          device_fields: {VDOM: root}
`),
	},
	{
		name:  "device field with an empty value",
		code:  model.CodeDeviceFieldValue,
		rule:  "r",
		wants: []string{"vdom"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - method: ephemeral-account
          username: jit
          platform: fortios
          credential_kind: password
          expiry_posture: accepted-risk
          device_fields: {vdom: ""}
`),
	},
	{
		name:  "device field on the wrong method",
		code:  model.CodeDeviceFieldWrongMethod,
		rule:  "r",
		wants: []string{"vdom", "ephemeral-user"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - {method: ephemeral-user, username: jit, device_fields: {vdom: root}}
`),
	},

	// -- enforcement -------------------------------------------------------
	{
		name:  "no-interactive-shell beside a permitted shell",
		code:  model.CodeEnforcementShellPermitted,
		rule:  "r",
		wants: []string{"shell"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      requests: {types: [shell, exec]}
      filter: {mode: whitelist}
      enforcement: {execution: no-interactive-shell}
      credentials: [{method: ephemeral-user, username: jit}]
`),
	},
	{
		name: "no-interactive-shell with an unpoliced request axis",
		code: model.CodeEnforcementRequestsMissing,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      enforcement: {execution: no-interactive-shell}
      credentials: [{method: ephemeral-user, username: jit}]
`),
	},
	{
		name:  "account-restricted without restricted exec",
		code:  model.CodeEnforcementExecModeRequired,
		rule:  "r",
		wants: []string{"account-restricted"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      enforcement: {execution: account-restricted}
      credentials: [{method: ephemeral-user, username: jit}]
`),
	},
	{
		name: "platform-authorized without a role",
		code: model.CodeEnforcementRoleMissing,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      enforcement: {execution: platform-authorized}
      credentials: [{method: ephemeral-user, username: jit}]
`),
	},
	{
		name:  "platform role on another rung",
		code:  model.CodeEnforcementRoleUnexpected,
		rule:  "r",
		wants: []string{"absent"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      enforcement: {platform_role: network-admin}
      credentials: [{method: ephemeral-user, username: jit}]
`),
	},
	{
		name: "account-egress-restricted with no destinations",
		code: model.CodeEnforcementDestsRequired,
		rule: "r",
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      enforcement: {reach: account-egress-restricted}
      credentials: [{method: ephemeral-user, username: jit}]
`),
	},
	{
		name:  "attested rung with nothing to attribute it to",
		code:  model.CodeEnforcementAttestationMissing,
		rule:  "r",
		wants: []string{"asserted_by", "reference"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      enforcement: {execution: platform-attested}
      credentials: [{method: static-key, username: admin}]
`),
	},
	{
		name:  "attestation on an applied rung",
		code:  model.CodeEnforcementAttestationUnexpected,
		rule:  "r",
		wants: []string{"no-interactive-shell"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      requests: {types: [exec]}
      filter: {mode: whitelist}
      enforcement:
        execution: no-interactive-shell
        attestation: {asserted_by: cis-benchmark, reference: REP-1}
      credentials: [{method: ephemeral-user, username: jit}]
`),
	},
	{
		name:  "applied rung on a ladder the proxy cannot administer",
		code:  model.CodeEnforcementAppliedUnprovisioned,
		rule:  "r",
		wants: []string{"account-confined", "execution"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist, exec_mode: restricted, restricted_exec: {commands: [{executable: /bin/ls, form: exact, argv: ["-l"]}]}}
      enforcement: {execution: account-confined}
      credentials:
        - {method: brokered-key, username: admin, credential_ref: vault://a}
        - {method: static-key, username: admin}
`),
	},

	// -- obligations -------------------------------------------------------
	{
		name:  "duplicate obligation",
		code:  model.CodeObligationDuplicate,
		rule:  "r",
		wants: []string{"record-session"},
		src: header(`rules:
  - id: r
    effect: allow
    route: ` + allowRoute + `
    obligations: [{kind: record-session}, {kind: record-session}]
`),
	},
	{
		name:  "obligation on a deny",
		code:  model.CodeObligationOnDeny,
		rule:  "r",
		wants: []string{"require-approval"},
		src: header(`rules:
  - id: r
    effect: deny
    reason: no
    obligations: [{kind: require-approval}]
`),
	},
	{
		name:  "obligation contradicting the route",
		code:  model.CodeObligationContradicts,
		rule:  "r",
		wants: []string{"record-session", "require_session_capture"},
		src: header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      require_session_capture: false
    obligations: [{kind: record-session}]
`),
	},
	{
		name:  "approver groups on another obligation",
		code:  model.CodeObligationContradicts,
		rule:  "r",
		wants: []string{"record-session", "approver"},
		src: header(`groups: [sre]
rules:
  - id: r
    effect: allow
    route: ` + allowRoute + `
    obligations: [{kind: record-session, approver_groups: [sre]}]
`),
	},
}

// TestHandBuiltBundleGetsTheSameChecks covers the two rejections a YAML
// document cannot reach, because the enum decoder refuses the value before the
// compiler ever sees it. A bundle built in Go — which is what the authoring API
// (0014) will hand over — gets the same answer.
func TestHandBuiltBundleGetsTheSameChecks(t *testing.T) {
	channels := []model.ChannelType{model.ChannelSession}
	b := &model.Bundle{
		SchemaVersion: model.SchemaVersion,
		Tenant:        "acme",
		Rules: []model.Rule{{
			ID:     "r",
			Effect: model.EffectAllow,
			Route: &model.Route{
				Intent:   model.RouteIntentDirect,
				Channels: &channels,
				Filter:   model.FilterPolicy{Mode: model.FilterModeWhitelist},
				Credentials: []model.CredentialEntry{{
					Username: model.Username{Source: model.UsernameLiteral, Value: "svc"},
				}},
			},
			Obligations: []model.Obligation{{}},
		}},
	}
	_, err := compile.Compile(b)
	if err == nil {
		t.Fatal("expected the bundle to be refused")
	}
	rs := asRejections(t, err)
	for _, want := range []model.Code{model.CodeCredentialMethodMissing, model.CodeObligationKindMissing} {
		if !rs.Has(want) {
			t.Errorf("expected %s, got %v", want, rs.Codes())
		}
	}
}

// TestAttestedRungOnAnApplianceLadderIsValid is the other half of the applied
// rung rule, and it matters as much: an attested rung on a brokered-key or
// static-key ladder is how an appliance carries a real enforcement claim. A
// compiler that refused it would make the appliance estate unauthorable.
func TestAttestedRungOnAnApplianceLadderIsValid(t *testing.T) {
	mustCompile(t, header(`rules:
  - id: appliance
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      enforcement:
        execution: platform-attested
        reach: platform-attested
        attestation: {asserted_by: netops, reference: CIS-2026-04, asserted_at: "2026-04-01T00:00:00Z"}
      credentials:
        - {method: brokered-key, username: admin, credential_ref: vault://fw01}
`))
}

// TestUnknownDeviceFieldNameIsNotRejected. The shape is checked here; whether a
// driver recognises the name is a capability question this compiler cannot
// answer (M17), and on the proxy it is a skipped rung rather than an error. A
// compiler that refused an unrecognised name would make a customer-written
// driver unauthorable without a release of this server.
func TestUnknownDeviceFieldNameIsNotRejected(t *testing.T) {
	mustCompile(t, header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - method: ephemeral-account
          username: jit
          platform: acme-customdriver
          credential_kind: publickey
          expiry_posture: accepted-risk
          device_fields: {nobody_has_ever_heard_of_this: yes-really}
`))
}

// TestDeviceFieldCountIsBounded is its own test because sixteen fields is
// tedious to write into the table above and the bound is worth asserting.
func TestDeviceFieldCountIsBounded(t *testing.T) {
	var fields strings.Builder
	for i := range model.DeviceFieldMaxPerEntry + 1 {
		fields.WriteString("            f")
		fields.WriteString(strings.Repeat("x", i))
		fields.WriteString(": v\n")
	}
	rs := compileSource(t, header(`rules:
  - id: r
    effect: allow
    route:
      intent: direct
      channels: [session]
      filter: {mode: whitelist}
      credentials:
        - method: ephemeral-account
          username: jit
          platform: fortios
          credential_kind: publickey
          expiry_posture: accepted-risk
          device_fields:
`+fields.String()))
	if !rs.Has(model.CodeDeviceFieldCount) {
		t.Fatalf("expected %s, got %v", model.CodeDeviceFieldCount, rs.Codes())
	}
}

// TestEveryRejectionIsReported: an author fixing a bundle one compile at a time
// is an author who stops trusting the compiler, so the whole list comes back at
// once and in source order.
func TestEveryRejectionIsReported(t *testing.T) {
	rs := compileSource(t, header(`groups: [sre]
rules:
  - {id: a, effect: allow, match: {subject: {groups: [nope]}}, route: `+allowRoute+`}
  - {id: b, effect: deny}
  - {id: c, effect: allow, route: {intent: direct, channels: [session], filter: {mode: whitelist}, credentials: [{method: static-key}]}}
`))
	want := []model.Code{
		model.CodeMatchUnknownGroup,
		model.CodeRuleDenyNeedsReason,
		model.CodeCredentialUsernameMissing,
	}
	for _, c := range want {
		if !rs.Has(c) {
			t.Errorf("expected %s in one compile, got %v", c, rs.Codes())
		}
	}
	for i := 1; i < len(rs); i++ {
		if rs[i-1].Line > rs[i].Line {
			t.Fatalf("rejections are not in source order: %v", rs)
		}
	}
}
