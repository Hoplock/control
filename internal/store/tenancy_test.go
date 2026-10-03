// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// nowForTest is a non-zero instant for arguments that refuse the zero value.
// Its value is irrelevant: every call below is expected to fail on the tenant
// before anything looks at a timestamp.
func nowForTest() time.Time { return time.Unix(1, 0).UTC() }

// repositoryInterfaces is every repository this package exposes. A new one is
// added here in the same commit that declares it; the test below is the reason
// the list is worth maintaining by hand rather than discovering.
var repositoryInterfaces = map[string]reflect.Type{
	"SubjectRepository":      reflect.TypeOf((*SubjectRepository)(nil)).Elem(),
	"TargetRepository":       reflect.TypeOf((*TargetRepository)(nil)).Elem(),
	"ProxyRepository":        reflect.TypeOf((*ProxyRepository)(nil)).Elem(),
	"PolicyBundleRepository": reflect.TypeOf((*PolicyBundleRepository)(nil)).Elem(),
	"DecisionRepository":     reflect.TypeOf((*DecisionRepository)(nil)).Elem(),
	"AuditRepository":        reflect.TypeOf((*AuditRepository)(nil)).Elem(),
	"GrantRepository":        reflect.TypeOf((*GrantRepository)(nil)).Elem(),
	"UIDCursorRepository":    reflect.TypeOf((*UIDCursorRepository)(nil)).Elem(),

	// Access grants (0012).
	"GrantRequestRepository": reflect.TypeOf((*GrantRequestRepository)(nil)).Elem(),

	// External access context (0013).
	"AccessContextBindingRepository": reflect.TypeOf((*AccessContextBindingRepository)(nil)).Elem(),

	// Identity, federation and credentials (0011). 0012 found these missing
	// from this list; every one of them already named its tenant, and now a
	// method added to them later has to as well.
	"GroupRepository":             reflect.TypeOf((*GroupRepository)(nil)).Elem(),
	"RoleBindingRepository":       reflect.TypeOf((*RoleBindingRepository)(nil)).Elem(),
	"ConnectorRepository":         reflect.TypeOf((*ConnectorRepository)(nil)).Elem(),
	"ClaimMappingRepository":      reflect.TypeOf((*ClaimMappingRepository)(nil)).Elem(),
	"FederatedIdentityRepository": reflect.TypeOf((*FederatedIdentityRepository)(nil)).Elem(),
	"FlowStateRepository":         reflect.TypeOf((*FlowStateRepository)(nil)).Elem(),
	"NorthPrincipalRepository":    reflect.TypeOf((*NorthPrincipalRepository)(nil)).Elem(),
	"CAKeyRepository":             reflect.TypeOf((*CAKeyRepository)(nil)).Elem(),
	"SSHCertificateRepository":    reflect.TypeOf((*SSHCertificateRepository)(nil)).Elem(),
	"SoftwareKeyRepository":       reflect.TypeOf((*SoftwareKeyRepository)(nil)).Elem(),

	// The fleet registry (0006).
	"ProxyEnrollmentRepository":   reflect.TypeOf((*ProxyEnrollmentRepository)(nil)).Elem(),
	"ProxyEdgeRepository":         reflect.TypeOf((*ProxyEdgeRepository)(nil)).Elem(),
	"RelayRegistrationRepository": reflect.TypeOf((*RelayRegistrationRepository)(nil)).Elem(),
	"ProxyConfigRepository":       reflect.TypeOf((*ProxyConfigRepository)(nil)).Elem(),
	"TargetCapabilityRepository":  reflect.TypeOf((*TargetCapabilityRepository)(nil)).Elem(),

	// The south-bound API (0007).
	"SubjectKeyRepository":      reflect.TypeOf((*SubjectKeyRepository)(nil)).Elem(),
	"SubjectPasswordRepository": reflect.TypeOf((*SubjectPasswordRepository)(nil)).Elem(),
	"MFARepository":             reflect.TypeOf((*MFARepository)(nil)).Elem(),
	"TargetHostKeyRepository":   reflect.TypeOf((*TargetHostKeyRepository)(nil)).Elem(),
	"UIDLeaseRepository":        reflect.TypeOf((*UIDLeaseRepository)(nil)).Elem(),
	"ProxyTokenRepository":      reflect.TypeOf((*ProxyTokenRepository)(nil)).Elem(),
}

// Tenancy is unforgeable at the repository boundary (M18).
//
// The isolation tests elsewhere in this package prove that the queries filter
// on the tenant. This one proves something they cannot: that there is no
// method able to express a cross-tenant read in the first place. A signature
// that cannot say "every tenant" is worth far more than a convention a
// reviewer has to notice, and the failure mode it forecloses — a method added
// later that reads the tenant from a server-wide value — is exactly the
// retrofit M12 set out to avoid, one layer up.
func TestRepositoryMethodsTakeATenant(t *testing.T) {
	t.Parallel()

	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	tenantType := reflect.TypeOf(Tenant(""))

	for name, iface := range repositoryInterfaces {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if iface.NumMethod() == 0 {
				t.Fatalf("%s declares no methods", name)
			}
			for i := range iface.NumMethod() {
				m := iface.Method(i)
				sig := m.Type

				if sig.NumIn() < 2 {
					t.Errorf("%s.%s takes %d parameters, want at least a context and a tenant",
						name, m.Name, sig.NumIn())
					continue
				}
				if got := sig.In(0); got != ctxType {
					t.Errorf("%s.%s first parameter is %v, want context.Context",
						name, m.Name, got)
				}
				if got := sig.In(1); got != tenantType {
					t.Errorf("%s.%s second parameter is %v, want store.Tenant: "+
						"a repository method that does not name its tenant can read across tenants (M18)",
						name, m.Name, got)
				}
			}
		})
	}
}

// Every repository the Store hands out satisfies its declared interface. The
// accessors return unexported types, so this is what keeps the two in step.
func TestStoreExposesEveryRepository(t *testing.T) {
	t.Parallel()

	s := &Store{}
	got := map[string]any{
		"SubjectRepository":      s.Subjects(),
		"TargetRepository":       s.Targets(),
		"ProxyRepository":        s.Proxies(),
		"PolicyBundleRepository": s.PolicyBundles(),
		"DecisionRepository":     s.Decisions(),
		"AuditRepository":        s.Audit(),
		"GrantRepository":        s.Grants(),
		"UIDCursorRepository":    s.UIDCursors(),
		"GrantRequestRepository": s.GrantRequests(),

		"AccessContextBindingRepository": s.AccessContextBindings(),

		"GroupRepository":             s.Groups(),
		"RoleBindingRepository":       s.RoleBindings(),
		"ConnectorRepository":         s.Connectors(),
		"ClaimMappingRepository":      s.ClaimMappings(),
		"FederatedIdentityRepository": s.FederatedIdentities(),
		"FlowStateRepository":         s.FlowStates(),
		"NorthPrincipalRepository":    s.NorthPrincipals(),
		"CAKeyRepository":             s.CAKeys(),
		"SSHCertificateRepository":    s.SSHCertificates(),
		"SoftwareKeyRepository":       s.SoftwareKeys(),

		"ProxyEnrollmentRepository":   s.ProxyEnrollments(),
		"ProxyEdgeRepository":         s.ProxyEdges(),
		"RelayRegistrationRepository": s.RelayRegistrations(),
		"ProxyConfigRepository":       s.ProxyConfigs(),
		"TargetCapabilityRepository":  s.TargetCapabilities(),

		"SubjectKeyRepository":      s.SubjectKeys(),
		"SubjectPasswordRepository": s.SubjectPasswords(),
		"MFARepository":             s.MFA(),
		"TargetHostKeyRepository":   s.TargetHostKeys(),
		"UIDLeaseRepository":        s.UIDLeases(),
		"ProxyTokenRepository":      s.ProxyTokens(),
	}

	if len(got) != len(repositoryInterfaces) {
		t.Fatalf("Store exposes %d repositories, repositoryInterfaces lists %d",
			len(got), len(repositoryInterfaces))
	}
	for name, repo := range got {
		iface, ok := repositoryInterfaces[name]
		if !ok {
			t.Errorf("Store exposes %s, which repositoryInterfaces does not list", name)
			continue
		}
		if !reflect.TypeOf(repo).Implements(iface) {
			t.Errorf("%T does not implement %s", repo, name)
		}
	}
}

// An empty tenant is a caller bug, and a caller bug that reaches rows written
// under "" is the vulnerability class M18 exists to close. Every method must
// refuse it before it reaches SQL — which this test can assert without a
// database precisely because the refusal comes first.
func TestEmptyTenantIsRefusedBeforeAnyQuery(t *testing.T) {
	t.Parallel()

	// A Store with no connection at all: any method that got as far as SQL
	// would panic on the nil querier rather than return.
	s := &Store{}
	ctx := t.Context()

	calls := map[string]func() error{
		"Subjects.Get":            func() error { _, err := s.Subjects().Get(ctx, "", "s"); return err },
		"Subjects.GetByPrincipal": func() error { _, err := s.Subjects().GetByPrincipal(ctx, "", "p"); return err },
		"Subjects.Upsert":         func() error { return s.Subjects().Upsert(ctx, "", Subject{ID: "s"}) },
		"Subjects.Delete":         func() error { return s.Subjects().Delete(ctx, "", "s") },
		"Targets.Get":             func() error { _, err := s.Targets().Get(ctx, "", "t"); return err },
		"Targets.GetByHostname":   func() error { _, err := s.Targets().GetByHostname(ctx, "", "h"); return err },
		"Targets.ListByLabels":    func() error { _, err := s.Targets().ListByLabels(ctx, "", nil); return err },
		"Targets.Upsert":          func() error { return s.Targets().Upsert(ctx, "", Target{ID: "t", Hostname: "h"}) },
		"Targets.Delete":          func() error { return s.Targets().Delete(ctx, "", "t") },
		"Proxies.Get":             func() error { _, err := s.Proxies().Get(ctx, "", "p"); return err },
		"Proxies.ListByZone":      func() error { _, err := s.Proxies().ListByZone(ctx, "", "z"); return err },
		"Proxies.Upsert": func() error {
			return s.Proxies().Upsert(ctx, "", Proxy{ID: "p", State: EnrollmentEnrolled})
		},
		"Proxies.List": func() error { _, err := s.Proxies().List(ctx, ""); return err },
		"Proxies.RecordHealth": func() error {
			return s.Proxies().RecordHealth(ctx, "", ProxyHealthReport{ProxyID: "p", At: nowForTest()})
		},
		"Proxies.Delete": func() error { return s.Proxies().Delete(ctx, "", "p") },
		"PolicyBundles.Insert": func() error {
			return s.PolicyBundles().Insert(ctx, "", PolicyBundle{Version: 1, Hash: "h"})
		},
		"PolicyBundles.Get":         func() error { _, err := s.PolicyBundles().Get(ctx, "", 1); return err },
		"PolicyBundles.GetActive":   func() error { _, err := s.PolicyBundles().GetActive(ctx, ""); return err },
		"PolicyBundles.Activate":    func() error { return s.PolicyBundles().Activate(ctx, "", 1) },
		"PolicyBundles.NextVersion": func() error { _, err := s.PolicyBundles().NextVersion(ctx, ""); return err },
		"Decisions.Insert": func() error {
			return s.Decisions().Insert(ctx, "", Decision{ID: "d", InputsDigest: "x"})
		},
		"Decisions.Get":           func() error { _, err := s.Decisions().Get(ctx, "", "d"); return err },
		"Decisions.ListBySubject": func() error { _, err := s.Decisions().ListBySubject(ctx, "", "s", 1); return err },
		"Decisions.ListBySession": func() error { _, err := s.Decisions().ListBySession(ctx, "", "s", 1); return err },
		"Audit.Append": func() error {
			return s.Audit().Append(ctx, "", AuditRecord{RecordID: "r", Stream: "s", ChainSeq: 1})
		},
		"Audit.Get":       func() error { _, err := s.Audit().Get(ctx, "", "r"); return err },
		"Audit.Chain":     func() error { _, err := s.Audit().Chain(ctx, "", "s", 0, 1); return err },
		"Audit.ChainHead": func() error { _, err := s.Audit().ChainHead(ctx, "", "s"); return err },
		"Audit.AppendChain": func() error {
			_, err := s.Audit().AppendChain(ctx, "", "s", []string{"r"},
				func(AuditRecord, []string) ([]AuditRecord, error) { return nil, nil })
			return err
		},
		"Audit.Streams": func() error { _, err := s.Audit().Streams(ctx, ""); return err },
		"Audit.Query":   func() error { _, err := s.Audit().Query(ctx, "", AuditQuery{}); return err },
		"Audit.BlockedCommands": func() error {
			_, err := s.Audit().BlockedCommands(ctx, "", BlockedCommandQuery{LabelName: "env"})
			return err
		},
		"Audit.PutCapture": func() error { return s.Audit().PutCapture(ctx, "", "r", []byte("x")) },
		"Audit.Capture":    func() error { _, err := s.Audit().Capture(ctx, "", "r"); return err },
		"Grants.Insert": func() error {
			return s.Grants().Insert(ctx, "", Grant{ID: "g", Origin: GrantOriginManual, ExpiresAt: nowForTest()})
		},
		"Grants.Get":      func() error { _, err := s.Grants().Get(ctx, "", "g"); return err },
		"Grants.ListLive": func() error { _, err := s.Grants().ListLive(ctx, "", "s", nowForTest()); return err },
		"Grants.Revoke": func() error {
			_, err := s.Grants().Revoke(ctx, "", "g", GrantRevocation{At: nowForTest()})
			return err
		},
		"Grants.List": func() error { _, err := s.Grants().List(ctx, "", GrantQuery{}); return err },
		"GrantRequests.Insert": func() error {
			return s.GrantRequests().Insert(ctx, "", GrantRequest{
				ID: "r", SubjectID: "s", NotBefore: nowForTest(), ExpiresAt: nowForTest().Add(time.Hour),
			})
		},
		"GrantRequests.Get":         func() error { _, err := s.GrantRequests().Get(ctx, "", "r"); return err },
		"GrantRequests.ListPending": func() error { _, err := s.GrantRequests().ListPending(ctx, "", 1); return err },
		"GrantRequests.Polled": func() error {
			return s.GrantRequests().Polled(ctx, "", "r", "ref", nowForTest())
		},
		"GrantRequests.Resolve": func() error {
			return s.GrantRequests().Resolve(ctx, "", "r", GrantRequestResolution{
				State: GrantRequestDenied, At: nowForTest(),
			})
		},
		"Decisions.ListByGrant": func() error { _, err := s.Decisions().ListByGrant(ctx, "", "g", 1); return err },
		"Decisions.SessionsByGrant": func() error {
			_, _, err := s.Decisions().SessionsByGrant(ctx, "", "g", 1)
			return err
		},
		"UIDCursors.Get":     func() error { _, err := s.UIDCursors().Get(ctx, "", "t"); return err },
		"UIDCursors.Create":  func() error { return s.UIDCursors().Create(ctx, "", "t", 0, 10) },
		"UIDCursors.Advance": func() error { _, err := s.UIDCursors().Advance(ctx, "", "t", 1); return err },
		"UIDCursors.RaiseFloor": func() error {
			_, err := s.UIDCursors().RaiseFloor(ctx, "", "t", 1)
			return err
		},

		// The fleet registry (0006).
		"ProxyEnrollments.Create": func() error {
			return s.ProxyEnrollments().Create(ctx, "", ProxyEnrollment{ProxyID: "p", TokenHash: []byte("h")})
		},
		"ProxyEnrollments.Get": func() error { _, err := s.ProxyEnrollments().Get(ctx, "", "p"); return err },
		"ProxyEnrollments.Consume": func() error {
			return s.ProxyEnrollments().Consume(ctx, "", "p", nowForTest())
		},
		"ProxyEnrollments.Delete": func() error { return s.ProxyEnrollments().Delete(ctx, "", "p") },
		"ProxyEdges.List":         func() error { _, err := s.ProxyEdges().List(ctx, ""); return err },
		"ProxyEdges.ReplaceForProxy": func() error {
			return s.ProxyEdges().ReplaceForProxy(ctx, "", "p", nil)
		},
		"RelayRegistrations.List": func() error { _, err := s.RelayRegistrations().List(ctx, ""); return err },
		"RelayRegistrations.ReplaceForUpstream": func() error {
			return s.RelayRegistrations().ReplaceForUpstream(ctx, "", "p", nil, nowForTest())
		},
		"ProxyConfigs.InsertVersion": func() error {
			return s.ProxyConfigs().InsertVersion(ctx, "", ProxyConfigVersion{
				Scope: ConfigScope{Kind: ConfigScopeZone, ID: "z"}, Version: 1,
				Document: []byte(`{}`), Hash: "h",
			})
		},
		"ProxyConfigs.GetVersion": func() error {
			_, err := s.ProxyConfigs().GetVersion(ctx, "", ConfigScope{Kind: ConfigScopeZone, ID: "z"}, 1)
			return err
		},
		"ProxyConfigs.NextVersion": func() error {
			_, err := s.ProxyConfigs().NextVersion(ctx, "", ConfigScope{Kind: ConfigScopeZone, ID: "z"})
			return err
		},
		"ProxyConfigs.SetDesired": func() error {
			return s.ProxyConfigs().SetDesired(ctx, "", ConfigScope{Kind: ConfigScopeZone, ID: "z"}, 1, "op", nowForTest())
		},
		"ProxyConfigs.GetDesired": func() error {
			_, err := s.ProxyConfigs().GetDesired(ctx, "", ConfigScope{Kind: ConfigScopeZone, ID: "z"})
			return err
		},
		"ProxyConfigs.PutState": func() error {
			return s.ProxyConfigs().PutState(ctx, "", ProxyConfigState{ProxyID: "p"})
		},
		"ProxyConfigs.GetState":   func() error { _, err := s.ProxyConfigs().GetState(ctx, "", "p"); return err },
		"ProxyConfigs.ListStates": func() error { _, err := s.ProxyConfigs().ListStates(ctx, ""); return err },
		"ProxyConfigs.ReportRunning": func() error {
			return s.ProxyConfigs().ReportRunning(ctx, "", "p", 1, "h", nowForTest())
		},
		"TargetCapabilities.Put": func() error {
			return s.TargetCapabilities().Put(ctx, "", TargetCapabilityRecord{Hostname: "h"})
		},
		"TargetCapabilities.Get": func() error {
			_, err := s.TargetCapabilities().Get(ctx, "", "h", 22, "linux")
			return err
		},
		"TargetCapabilities.List": func() error { _, err := s.TargetCapabilities().List(ctx, ""); return err },

		// The south-bound API (0007). These are the credential lookups, so
		// a method here that read across tenants would authenticate one
		// tenant's user against another's key.
		"Proxies.GetByKeyFingerprint": func() error {
			_, err := s.Proxies().GetByKeyFingerprint(ctx, "", "SHA256:x")
			return err
		},
		"SubjectKeys.GetByFingerprint": func() error {
			_, err := s.SubjectKeys().GetByFingerprint(ctx, "", "SHA256:x")
			return err
		},
		"SubjectKeys.ListBySubject": func() error {
			_, err := s.SubjectKeys().ListBySubject(ctx, "", "s")
			return err
		},
		"SubjectKeys.Put": func() error {
			return s.SubjectKeys().Put(ctx, "", SubjectKey{Fingerprint: "SHA256:x", SubjectID: "s"})
		},
		"SubjectKeys.Revoke": func() error {
			return s.SubjectKeys().Revoke(ctx, "", "SHA256:x", nowForTest())
		},
		"SubjectPasswords.Get": func() error { _, err := s.SubjectPasswords().Get(ctx, "", "s"); return err },
		"SubjectPasswords.Put": func() error {
			return s.SubjectPasswords().Put(ctx, "", PasswordDigest{
				SubjectID: "s", Algorithm: "pbkdf2-sha256", Iterations: 1,
				Salt: []byte("s"), Digest: []byte("d"),
			})
		},
		"SubjectPasswords.Delete": func() error { return s.SubjectPasswords().Delete(ctx, "", "s") },
		"MFA.GetEnrollment":       func() error { _, err := s.MFA().GetEnrollment(ctx, "", "s"); return err },
		"MFA.PutEnrollment": func() error {
			return s.MFA().PutEnrollment(ctx, "", MFAEnrollment{SubjectID: "s", Provider: "scripted"})
		},
		"MFA.CreateChallenge": func() error {
			return s.MFA().CreateChallenge(ctx, "", MFAChallenge{
				Token: "t", SubjectID: "s", IssuedAt: nowForTest(), ExpiresAt: nowForTest(),
			})
		},
		"MFA.GetChallenge": func() error { _, err := s.MFA().GetChallenge(ctx, "", "t"); return err },
		"MFA.PollChallenge": func() error {
			_, err := s.MFA().PollChallenge(ctx, "", "t", nowForTest())
			return err
		},
		"MFA.ResolveChallenge": func() error {
			return s.MFA().ResolveChallenge(ctx, "", "t", MFAChallengeApproved, nowForTest())
		},
		"TargetHostKeys.Record": func() error {
			_, _, err := s.TargetHostKeys().Record(ctx, "", TargetHostKey{
				Hostname: "h", Fingerprint: "SHA256:x", Decision: HostKeyAccepted,
			})
			return err
		},
		"TargetHostKeys.ListForTarget": func() error {
			_, err := s.TargetHostKeys().ListForTarget(ctx, "", "h", 22)
			return err
		},
		"UIDLeases.Record": func() error {
			return s.UIDLeases().Record(ctx, "", UIDLease{
				LeaseID: "l", TargetID: "h", ProxyID: "p", From: 1, To: 2,
			})
		},
		"UIDLeases.Get":           func() error { _, err := s.UIDLeases().Get(ctx, "", "l"); return err },
		"UIDLeases.ListForTarget": func() error { _, err := s.UIDLeases().ListForTarget(ctx, "", "h"); return err },
		"ProxyTokens.Insert": func() error {
			return s.ProxyTokens().Insert(ctx, "", ProxyAPIToken{TokenID: "t", TokenHash: []byte("h")})
		},
		"ProxyTokens.GetByHash": func() error {
			_, err := s.ProxyTokens().GetByHash(ctx, "", []byte("h"))
			return err
		},
		"ProxyTokens.Revoke": func() error { return s.ProxyTokens().Revoke(ctx, "", "t", nowForTest()) },
		"ProxyTokens.ListByProxy": func() error {
			_, err := s.ProxyTokens().ListByProxy(ctx, "", "p")
			return err
		},

		// External access context (0013).
		"Grants.GetByAssertion": func() error {
			_, err := s.Grants().GetByAssertion(ctx, "", "itsm", "a-1")
			return err
		},
		"AccessContextBindings.Put": func() error {
			return s.AccessContextBindings().Put(ctx, "", AccessContextBinding{
				Provider: "itsm", Mode: ExternalPush, Scope: "s", MaxWindow: time.Hour,
			})
		},
		"AccessContextBindings.Get": func() error {
			_, err := s.AccessContextBindings().Get(ctx, "", "itsm")
			return err
		},
		"AccessContextBindings.List": func() error {
			_, err := s.AccessContextBindings().List(ctx, "")
			return err
		},
		"AccessContextBindings.Delete": func() error {
			_, err := s.AccessContextBindings().Delete(ctx, "", "itsm")
			return err
		},

		// Identity, federation and credentials (0011).
		"Groups.Get":          func() error { _, err := s.Groups().Get(ctx, "", "g"); return err },
		"Groups.List":         func() error { _, err := s.Groups().List(ctx, ""); return err },
		"Groups.Upsert":       func() error { return s.Groups().Upsert(ctx, "", Group{ID: "g"}) },
		"Groups.Delete":       func() error { return s.Groups().Delete(ctx, "", "g") },
		"Groups.AddMember":    func() error { return s.Groups().AddMember(ctx, "", "g", "s") },
		"Groups.RemoveMember": func() error { return s.Groups().RemoveMember(ctx, "", "g", "s") },
		"Groups.GroupsOf":     func() error { _, err := s.Groups().GroupsOf(ctx, "", "s"); return err },
		"RoleBindings.List":   func() error { _, err := s.RoleBindings().List(ctx, ""); return err },
		"RoleBindings.RolesFor": func() error {
			_, err := s.RoleBindings().RolesFor(ctx, "", "s", nil)
			return err
		},
		"RoleBindings.Bind":   func() error { return s.RoleBindings().Bind(ctx, "", RoleBinding{}) },
		"RoleBindings.Unbind": func() error { return s.RoleBindings().Unbind(ctx, "", RoleBinding{}) },
		"Connectors.Get":      func() error { _, err := s.Connectors().Get(ctx, "", "c"); return err },
		"Connectors.List":     func() error { _, err := s.Connectors().List(ctx, ""); return err },
		"Connectors.Upsert":   func() error { return s.Connectors().Upsert(ctx, "", Connector{}) },
		"Connectors.Delete":   func() error { return s.Connectors().Delete(ctx, "", "c") },
		"ClaimMappings.Active": func() error {
			_, err := s.ClaimMappings().Active(ctx, "")
			return err
		},
		"ClaimMappings.Get":  func() error { _, err := s.ClaimMappings().Get(ctx, "", 1); return err },
		"ClaimMappings.List": func() error { _, err := s.ClaimMappings().List(ctx, ""); return err },
		"ClaimMappings.Put": func() error {
			_, err := s.ClaimMappings().Put(ctx, "", "doc", "digest", "me")
			return err
		},
		"ClaimMappings.Activate": func() error { return s.ClaimMappings().Activate(ctx, "", 1) },
		"FederatedIdentities.Resolve": func() error {
			_, err := s.FederatedIdentities().Resolve(ctx, "", "c", "x")
			return err
		},
		"FederatedIdentities.Link": func() error {
			return s.FederatedIdentities().Link(ctx, "", FederatedIdentity{})
		},
		"FederatedIdentities.ListBySubject": func() error {
			_, err := s.FederatedIdentities().ListBySubject(ctx, "", "s")
			return err
		},
		"FlowStates.Begin": func() error { return s.FlowStates().Begin(ctx, "", FlowState{}) },
		"FlowStates.Consume": func() error {
			_, err := s.FlowStates().Consume(ctx, "", "st", nowForTest())
			return err
		},
		"FlowStates.DeleteExpired": func() error {
			_, err := s.FlowStates().DeleteExpired(ctx, "", nowForTest())
			return err
		},
		"NorthPrincipals.Get": func() error { _, err := s.NorthPrincipals().Get(ctx, "", "p"); return err },
		"NorthPrincipals.ByTokenDigest": func() error {
			_, err := s.NorthPrincipals().ByTokenDigest(ctx, "", "d")
			return err
		},
		"NorthPrincipals.BySessionDigest": func() error {
			_, err := s.NorthPrincipals().BySessionDigest(ctx, "", "d")
			return err
		},
		"NorthPrincipals.Put": func() error { return s.NorthPrincipals().Put(ctx, "", NorthPrincipal{}) },
		"NorthPrincipals.Touch": func() error {
			return s.NorthPrincipals().Touch(ctx, "", "p", nowForTest())
		},
		"NorthPrincipals.Revoke": func() error {
			return s.NorthPrincipals().Revoke(ctx, "", "p", nowForTest())
		},
		"NorthPrincipals.List": func() error { _, err := s.NorthPrincipals().List(ctx, ""); return err },
		"CAKeys.Active":        func() error { _, err := s.CAKeys().Active(ctx, ""); return err },
		"CAKeys.Get":           func() error { _, err := s.CAKeys().Get(ctx, "", "k"); return err },
		"CAKeys.List":          func() error { _, err := s.CAKeys().List(ctx, ""); return err },
		"CAKeys.Insert":        func() error { return s.CAKeys().Insert(ctx, "", CAKey{}) },
		"CAKeys.Rotate": func() error {
			return s.CAKeys().Rotate(ctx, "", CAKey{}, nowForTest(), nowForTest())
		},
		"SSHCertificates.NextSerial": func() error {
			_, err := s.SSHCertificates().NextSerial(ctx, "")
			return err
		},
		"SSHCertificates.Insert": func() error { return s.SSHCertificates().Insert(ctx, "", SSHCertificate{}) },
		"SSHCertificates.Get": func() error {
			_, err := s.SSHCertificates().Get(ctx, "", 1)
			return err
		},
		"SSHCertificates.Outstanding": func() error {
			_, err := s.SSHCertificates().Outstanding(ctx, "", nowForTest())
			return err
		},
		"SSHCertificates.ListBySubject": func() error {
			_, err := s.SSHCertificates().ListBySubject(ctx, "", "s", 1)
			return err
		},
		"SSHCertificates.Revoke": func() error {
			return s.SSHCertificates().Revoke(ctx, "", 1, nowForTest(), "r")
		},
		"SSHCertificates.RevokeByCAKey": func() error {
			_, err := s.SSHCertificates().RevokeByCAKey(ctx, "", "k", nowForTest(), "r")
			return err
		},
		"SoftwareKeys.Get":    func() error { _, err := s.SoftwareKeys().Get(ctx, "", "k"); return err },
		"SoftwareKeys.Insert": func() error { return s.SoftwareKeys().Insert(ctx, "", SoftwareKey{}) },
		"SoftwareKeys.List":   func() error { _, err := s.SoftwareKeys().List(ctx, ""); return err },
		"SoftwareKeys.Delete": func() error { return s.SoftwareKeys().Delete(ctx, "", "k") },
	}

	if got, want := len(calls), totalRepositoryMethods(); got != want {
		t.Fatalf("this test covers %d methods, the repositories declare %d: "+
			"a method added without a case here is one nobody proved refuses an empty tenant", got, want)
	}

	for name, call := range calls {
		if err := call(); !IsInvalid(err) {
			t.Errorf("%s with an empty tenant returned %v, want ErrInvalid", name, err)
		}
	}
}

// totalRepositoryMethods counts the declared repository methods, so the test
// above cannot silently stop covering one.
func totalRepositoryMethods() int {
	var n int
	for _, iface := range repositoryInterfaces {
		n += iface.NumMethod()
	}
	return n
}
