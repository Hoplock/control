// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The constants in enums.go are only worth having if they are checked against
// the document rather than against memory. This test reads the VENDORED
// contract — the same bytes `make contract-check` pins — pulls every `enum:`
// out of it, and compares each one with the Go constants beside it.
//
// The comparison is in BOTH directions on purpose. A value added upstream that
// nothing here knows about is the obvious drift; a value deleted upstream that
// this repository still offers is the quieter one, and it is how a server ends
// up serving a rung the proxy has stopped implementing.

// contractDoc is as much of the OpenAPI document as this test reads.
type contractDoc struct {
	Info struct {
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]*schemaNode `yaml:"schemas"`
	} `yaml:"components"`
}

type schemaNode struct {
	Type       string                 `yaml:"type"`
	Required   []string               `yaml:"required"`
	Enum       []string               `yaml:"enum"`
	Default    any                    `yaml:"default"`
	Items      *schemaNode            `yaml:"items"`
	Properties map[string]*schemaNode `yaml:"properties"`
	Ref        string                 `yaml:"$ref"`
}

func loadContract(t *testing.T) *contractDoc {
	t.Helper()
	path := filepath.Join("..", "..", "contract", "control.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vendored contract: %v", err)
	}
	var doc contractDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Components.Schemas) == 0 {
		t.Fatalf("%s carries no schemas; the vendored copy is not the contract", path)
	}
	return &doc
}

// enumAt walks Schema.property[.items] and returns the enum the document gives
// it. A missing schema or property is a failure rather than an empty result:
// that is how a renamed field is caught.
func enumAt(t *testing.T, doc *contractDoc, schema, property string, inItems bool) []string {
	t.Helper()
	s, ok := doc.Components.Schemas[schema]
	if !ok {
		t.Fatalf("contract has no schema %q", schema)
	}
	p, ok := s.Properties[property]
	if !ok {
		t.Fatalf("contract schema %s has no property %q", schema, property)
	}
	if inItems {
		if p.Items == nil {
			t.Fatalf("contract schema %s.%s has no items", schema, property)
		}
		p = p.Items
	}
	if len(p.Enum) == 0 {
		t.Fatalf("contract schema %s.%s carries no enum", schema, property)
	}
	return p.Enum
}

func assertSameSet(t *testing.T, what string, document, code []string) {
	t.Helper()
	d := append([]string(nil), document...)
	c := append([]string(nil), code...)
	sort.Strings(d)
	sort.Strings(c)

	inDoc := map[string]bool{}
	for _, v := range d {
		inDoc[v] = true
	}
	inCode := map[string]bool{}
	for _, v := range c {
		inCode[v] = true
	}
	for _, v := range d {
		if !inCode[v] {
			t.Errorf("%s: contract has %q and internal/contract has no constant for it", what, v)
		}
	}
	for _, v := range c {
		if !inDoc[v] {
			t.Errorf("%s: internal/contract offers %q and the contract no longer defines it", what, v)
		}
	}
}

func TestEnumsMatchContract(t *testing.T) {
	doc := loadContract(t)

	cases := []struct {
		what     string
		schema   string
		property string
		inItems  bool
		code     []string
	}{
		{"AuthenticateResponse.status", "AuthenticateResponse", "status", false,
			strs(AuthStatusAuthenticated, AuthStatusMFARequired)},
		{"AuthorizeRequest.auth_method", "AuthorizeRequest", "auth_method", false,
			strs(AuthMethodCert, AuthMethodPasswordMFA)},
		{"AuthorizeResponse.route_type", "AuthorizeResponse", "route_type", false,
			strs(RouteTypeDirect, RouteTypeNexthop)},
		{"AuthorizeResponse.algorithm_profile", "AuthorizeResponse", "algorithm_profile", false,
			strs(AlgorithmProfileDefault, AlgorithmProfileLegacyRSASHA1, AlgorithmProfileLegacyDevice)},
		{"RequestPolicy.types[]", "RequestPolicy", "types", true,
			strs(RequestTypePTYReq, RequestTypeShell, RequestTypeExec, RequestTypeEnv, RequestTypeX11Req, RequestTypeAuthAgentReq)},
		{"TargetAuth.method", "TargetAuth", "method", false,
			strs(TargetAuthEphemeralUser, TargetAuthBrokeredKey, TargetAuthEphemeralAccount, TargetAuthStaticKey)},
		{"EnforcementPolicy.execution", "EnforcementPolicy", "execution", false,
			strs(ExecutionProxyInspected, ExecutionNoInteractiveShell, ExecutionAccountRestricted,
				ExecutionAccountConfined, ExecutionPlatformAuthorized, ExecutionPlatformAttested)},
		{"EnforcementPolicy.reach", "EnforcementPolicy", "reach", false,
			strs(ReachProxyChannelPolicy, ReachAccountEgressRestricted, ReachAccountNetworkIsolated, ReachPlatformAttested)},
		{"HopMetadata.connection", "HopMetadata", "connection", false,
			strs(HopConnectionDial, HopConnectionRelay)},
		{"RevocationEvent.type", "RevocationEvent", "type", false,
			strs(EventTypeSessionKill, EventTypeCacheInvalidate, EventTypeHeartbeat, EventTypeResync)},
		{"FilterPolicy.mode", "FilterPolicy", "mode", false,
			strs(FilterModeWhitelist, FilterModeBlacklist)},
		{"FilterPolicy.exec_mode", "FilterPolicy", "exec_mode", false,
			strs(ExecModeFiltered, ExecModeRestricted)},
		{"RestrictedCommand.form", "RestrictedCommand", "form", false,
			strs(CommandFormExact, CommandFormPositional)},
		{"ArgumentSpec.kind", "ArgumentSpec", "kind", false,
			strs(ArgumentKindLiteral, ArgumentKindPrefix, ArgumentKindOneOf, ArgumentKindAny)},
		{"FilterRule.action", "FilterRule", "action", false,
			strs(FilterActionAllowAndLog, FilterActionBlockCommand, FilterActionWarnAndContinue, FilterActionKillSession)},
		{"HostKeyReportResponse.decision", "HostKeyReportResponse", "decision", false,
			strs(HostKeyAccept, HostKeyReject)},
		{"LogRecord.severity", "LogRecord", "severity", false,
			strs(SeverityInfo, SeverityWarn, SeverityCritical)},
	}

	for _, c := range cases {
		assertSameSet(t, c.what, enumAt(t, doc, c.schema, c.property, c.inItems), c.code)
	}
}

// The two ephemeral-account parameter vocabularies are documented in prose
// rather than as `enum:` — `params` is an open map, so the document has nowhere
// to put them. They are still closed sets the server must not invent values
// for, so they are asserted against the prose that defines them.
func TestCredentialParameterVocabulariesAreDocumented(t *testing.T) {
	doc := loadContract(t)
	s := doc.Components.Schemas["TargetAuth"]
	if s == nil || s.Properties["params"] == nil {
		t.Fatal("contract schema TargetAuth has no params property")
	}
	text := rawDocument(t)
	for _, want := range []string{
		string(CredentialKindPassword), string(CredentialKindPublicKey),
		string(ExpiryPostureTargetEnforced), string(ExpiryPostureProxyEnforced), string(ExpiryPostureAcceptedRisk),
		ParamUsername, ParamKeyType, ParamLifetimeSeconds, ParamCredentialRef,
		ParamPlatform, ParamCredentialKind, ParamExpiryPosture, DeviceFieldPrefix,
	} {
		if !containsWord(text, want) {
			t.Errorf("internal/contract names %q and the vendored contract does not mention it", want)
		}
	}
}

// policy_version is REQUIRED with NO absent-value default. It previously carried
// `default: 1`; upstream Hoplock/proxy#53 removed it, and that removal is what
// makes "a request without it is refused" expressible at all. A default
// creeping back would make the 400 case untestable and is caught here.
func TestPolicyVersionIsRequiredWithNoDefault(t *testing.T) {
	doc := loadContract(t)
	s := doc.Components.Schemas["AuthorizeRequest"]
	if s == nil {
		t.Fatal("contract has no AuthorizeRequest schema")
	}
	var required bool
	for _, r := range s.Required {
		if r == "policy_version" {
			required = true
		}
	}
	if !required {
		t.Error("AuthorizeRequest.required does not list policy_version")
	}
	p := s.Properties["policy_version"]
	if p == nil {
		t.Fatal("AuthorizeRequest has no policy_version property")
	}
	if p.Default != nil {
		t.Errorf("AuthorizeRequest.policy_version carries default %v; a request that omits it must be refused, not guessed for", p.Default)
	}
}

// The paths are the other half of the wire surface, and a constant naming a
// path the document does not define is a handler routed to nothing.
func TestPathsMatchContract(t *testing.T) {
	doc := loadContract(t)
	code := []string{
		PathAuthCert, PathAuthPassword, PathAuthMFAPoll, PathAuthorize,
		PathHostKeyReport, PathCapabilitiesReport, PathUIDLease,
		PathLogsBatch, PathLogsPriority, PathProxyEvents,
	}
	document := make([]string, 0, len(doc.Paths))
	for p := range doc.Paths {
		document = append(document, p)
	}
	assertSameSet(t, "paths", document, code)
}

// The document version and the policy vocabulary are two numbers. This test
// asserts only that each is present and legible — deliberately NOT that one
// implies the other, and not that either only ever rises: #53 moved
// info.version DOWN, 4.3.0 to 4.0.0, while the vocabulary stood still at 4.
func TestTheTwoNumbersAreTwoNumbers(t *testing.T) {
	doc := loadContract(t)
	if doc.Info.Version == "" {
		t.Error("contract carries no info.version")
	}
	if PolicyVersion < 1 {
		t.Errorf("PolicyVersion = %d, which is not a vocabulary", PolicyVersion)
	}
	if !containsWord(rawDocument(t), "policy_version") {
		t.Error("contract no longer carries policy_version; negotiation is not optional")
	}
}

func rawDocument(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "contract", "control.yaml"))
	if err != nil {
		t.Fatalf("read vendored contract: %v", err)
	}
	return string(b)
}

func containsWord(haystack, needle string) bool {
	return needle != "" && strings.Contains(haystack, needle)
}

// strs renders a set of typed enum constants as the strings the document holds.
func strs[T ~string](vs ...T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return out
}
