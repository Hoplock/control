// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The layer above the brokers, end to end against a real store.
//
// The first test here is the phase's acceptance criterion in its strongest
// form: an IdP asserts a claim nothing maps, and the claim reaches nothing a
// decision could read.

type federationFixture struct {
	st         *store.Store
	federation *identity.Federation
	idp        *testIdP
	sink       *recordingSink
	now        time.Time
}

func newFederation(t *testing.T, opts ...identity.FederationOption) *federationFixture {
	t.Helper()
	st := storetest.New(t)
	idp := newTestIdP(t, testClientID)
	sink := &recordingSink{}

	f := &federationFixture{
		st: st, idp: idp, sink: sink,
		now: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
	}
	federation, err := identity.NewFederation(st, append([]identity.FederationOption{
		identity.WithFederationClock(func() time.Time { return f.now }),
		identity.WithAuditSink(sink),
	}, opts...)...)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	f.federation = federation
	return f
}

// connect writes an OIDC connector for a tenant, pointing at the test IdP.
func (f *federationFixture) connect(t *testing.T, tenant store.Tenant, name string) {
	t.Helper()
	config, err := json.Marshal(identity.OIDCConfig{
		Issuer:      f.idp.issuer(),
		ClientID:    testClientID,
		RedirectURI: "https://control.example.com/api/v1/session/federated/" + name + "/callback",
		Scopes:      []string{"profile", "email", "groups"},
		LoginClaim:  "preferred_username",
	})
	if err != nil {
		t.Fatalf("connector config: %v", err)
	}
	if err := f.st.Connectors().Upsert(context.Background(), tenant, store.Connector{
		Name: name, Kind: store.ConnectorOIDC, DisplayName: "Test IdP",
		Enabled: true, Config: config,
	}); err != nil {
		t.Fatalf("connector: %v", err)
	}
}

func (f *federationFixture) mapping(t *testing.T, tenant store.Tenant, document string) *identity.Mapping {
	t.Helper()
	mapping, err := f.federation.PutMapping(context.Background(), tenant, []byte(document), "test")
	if err != nil {
		t.Fatalf("mapping: %v", err)
	}
	return mapping
}

// login drives a whole federated login and returns the session.
func (f *federationFixture) login(t *testing.T, tenant store.Tenant, connector string) (identity.Session, error) {
	t.Helper()
	ctx := context.Background()

	started, err := f.federation.Begin(ctx, tenant, connector, "")
	if err != nil {
		return identity.Session{}, err
	}
	params := f.idp.login(t, started.RedirectURL)
	return f.federation.Complete(ctx, tenant, connector, params, "corr-1")
}

// recordingSink captures what the federation service wrote down.
type recordingSink struct {
	events []identity.AuthEvent
	fail   error
}

func (s *recordingSink) AuthEvent(_ context.Context, _ store.Tenant, e identity.AuthEvent) error {
	if s.fail != nil {
		return s.fail
	}
	s.events = append(s.events, e)
	return nil
}

func (s *recordingSink) last() identity.AuthEvent {
	if len(s.events) == 0 {
		return identity.AuthEvent{}
	}
	return s.events[len(s.events)-1]
}

func TestAnUnmappedClaimCannotInfluenceADecision(t *testing.T) {
	// THE PHASE'S SECURITY TEST, end to end. The IdP asserts `role: admin`;
	// the mapping does not name it; nothing the decision path reads carries it.
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.mapping(t, "tenant-a", mappingSRE)

	session, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	subject, err := f.st.Subjects().Get(context.Background(), "tenant-a", session.Principal.Subject)
	if err != nil {
		t.Fatalf("subject: %v", err)
	}

	// What the decision path reads is THIS ROW (0008): groups and claims come
	// from this server's store, never from a request.
	if v, ok := subject.Claims["role"]; ok {
		t.Fatalf("the unmapped claim reached the subject row as role=%q", v)
	}
	if subject.Claims["email"] != "alice@example.com" {
		t.Errorf("the mapped claim did not reach the row: %v", subject.Claims)
	}
	if !slices.Equal(subject.Groups, []string{"sre"}) {
		t.Errorf("groups: %v — only the mapped IdP group should survive", subject.Groups)
	}
	if len(subject.Claims) != 2 {
		t.Errorf("the row carries %d attributes: %v", len(subject.Claims), subject.Claims)
	}
}

func TestTheSubjectRowNamesTheMappingVersionADecisionRecordWillQuote(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")

	first := f.mapping(t, "tenant-a", mappingSRE)
	if first.Version != 1 {
		t.Fatalf("first version: %d", first.Version)
	}
	if _, err := f.login(t, "tenant-a", "okta"); err != nil {
		t.Fatalf("login: %v", err)
	}
	subject, err := f.st.Subjects().Get(context.Background(), "tenant-a", "okta:00u1")
	if err != nil {
		t.Fatalf("subject: %v", err)
	}
	if subject.MappingVersion != 1 {
		t.Fatalf("mapping version on the row: %d", subject.MappingVersion)
	}

	// A second version is a second number, allocated by the store rather than
	// authored, and the next login records it.
	second := f.mapping(t, "tenant-a", mappingSRE+"\n")
	if second.Version != 2 {
		t.Fatalf("second version: %d", second.Version)
	}
	if _, err := f.login(t, "tenant-a", "okta"); err != nil {
		t.Fatalf("second login: %v", err)
	}
	subject, err = f.st.Subjects().Get(context.Background(), "tenant-a", "okta:00u1")
	if err != nil {
		t.Fatalf("subject: %v", err)
	}
	if subject.MappingVersion != 2 {
		t.Fatalf("mapping version after a new mapping: %d", subject.MappingVersion)
	}

	// The old version stays readable forever: a decision record names it.
	if _, err := f.st.ClaimMappings().Get(context.Background(), "tenant-a", 1); err != nil {
		t.Fatalf("version 1 is no longer readable: %v", err)
	}
}

func TestATenantWithNoMappingGetsNoAttributesRatherThanAllOfThem(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	// No mapping is authored.

	session, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	subject, err := f.st.Subjects().Get(context.Background(), "tenant-a", session.Principal.Subject)
	if err != nil {
		t.Fatalf("subject: %v", err)
	}
	if len(subject.Claims) != 0 || len(subject.Groups) != 0 {
		t.Fatalf("a tenant that federated before authoring a mapping got %v / %v", subject.Claims, subject.Groups)
	}
}

func TestAStoredMappingThatNoLongerParsesIsAnOutageAndNotAnEmptyMapping(t *testing.T) {
	// Falling back to "maps nothing" would silently strip every attribute in
	// the tenant, which reads as a permissions bug and would be debugged as one.
	f := newFederation(t)
	ctx := context.Background()
	row, err := f.st.ClaimMappings().Put(ctx, "tenant-a", "schema_version: 9\n", "digest", "test")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := f.st.ClaimMappings().Activate(ctx, "tenant-a", row.Version); err != nil {
		t.Fatalf("activate: %v", err)
	}

	if _, err := f.federation.ActiveMapping(ctx, "tenant-a"); err == nil {
		t.Fatal("an unparseable active mapping was treated as an empty one")
	}
}

func TestTwoTenantsWithTheSameSubjectIdNeverResolveToEachOther(t *testing.T) {
	// A subject id is unique WITHIN a tenant, never globally. Both IdPs here
	// call the person `00u1`.
	f := newFederation(t)
	other := newTestIdP(t, testClientID)

	f.connect(t, "tenant-a", "okta")
	f.mapping(t, "tenant-a", mappingSRE)

	// tenant-b federates with a DIFFERENT IdP under a different connector name.
	configB, err := json.Marshal(identity.OIDCConfig{
		Issuer:      other.issuer(),
		ClientID:    testClientID,
		RedirectURI: "https://control.example.com/api/v1/session/federated/entra/callback",
		LoginClaim:  "preferred_username",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := f.st.Connectors().Upsert(context.Background(), "tenant-b", store.Connector{
		Name: "entra", Kind: store.ConnectorOIDC, Enabled: true, Config: configB,
	}); err != nil {
		t.Fatalf("connector: %v", err)
	}
	f.mapping(t, "tenant-b", mappingSRE)

	sessionA, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("tenant-a login: %v", err)
	}

	startedB, err := f.federation.Begin(context.Background(), "tenant-b", "entra", "")
	if err != nil {
		t.Fatalf("tenant-b begin: %v", err)
	}
	sessionB, err := f.federation.Complete(context.Background(), "tenant-b", "entra",
		other.login(t, startedB.RedirectURL), "corr-2")
	if err != nil {
		t.Fatalf("tenant-b login: %v", err)
	}

	if sessionA.Principal.Subject == sessionB.Principal.Subject {
		t.Fatalf("both tenants resolved to subject %q", sessionA.Principal.Subject)
	}
	// And neither subject exists in the other tenant.
	if _, err := f.st.Subjects().Get(context.Background(), "tenant-b", sessionA.Principal.Subject); !store.IsNotFound(err) {
		t.Fatalf("tenant-a's subject is visible in tenant-b: %v", err)
	}
	if _, err := f.st.Subjects().Get(context.Background(), "tenant-a", sessionB.Principal.Subject); !store.IsNotFound(err) {
		t.Fatalf("tenant-b's subject is visible in tenant-a: %v", err)
	}
	// A session minted in one tenant may act only in that one (M18).
	if sessionA.Principal.MayActIn("tenant-b") || sessionB.Principal.MayActIn("tenant-a") {
		t.Fatal("a session may act in the other tenant")
	}
}

func TestAReplayedCallbackIsRefusedAndSaysSoDifferently(t *testing.T) {
	// A state that was never issued and one that was already consumed are two
	// different facts that deserve two different records, even though they
	// share a status code (PLAN §6's rule for MFA challenges).
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.mapping(t, "tenant-a", mappingSRE)
	ctx := context.Background()

	started, err := f.federation.Begin(ctx, "tenant-a", "okta", "")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	params := f.idp.login(t, started.RedirectURL)

	if _, err := f.federation.Complete(ctx, "tenant-a", "okta", params, "corr-1"); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	_, err = f.federation.Complete(ctx, "tenant-a", "okta", params, "corr-2")
	replayed := assertRefusal(t, err, identity.RejectFederationState)
	if !strings.Contains(replayed.Detail, "already consumed") {
		t.Errorf("a replay was not told apart from an unknown state: %q", replayed.Detail)
	}

	_, err = f.federation.Complete(ctx, "tenant-a", "okta",
		url.Values{"state": {"never-issued"}}, "corr-3")
	unknown := assertRefusal(t, err, identity.RejectFederationState)
	if !strings.Contains(unknown.Detail, "no flow was issued") {
		t.Errorf("an unknown state was not told apart from a replay: %q", unknown.Detail)
	}
}

func TestAnExpiredFlowIsRefused(t *testing.T) {
	f := newFederation(t, identity.WithFlowTTL(time.Minute))
	f.connect(t, "tenant-a", "okta")
	ctx := context.Background()

	started, err := f.federation.Begin(ctx, "tenant-a", "okta", "")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	params := f.idp.login(t, started.RedirectURL)

	f.now = f.now.Add(2 * time.Minute)
	_, err = f.federation.Complete(ctx, "tenant-a", "okta", params, "corr-1")
	assertRefusal(t, err, identity.RejectFederationExpired)
}

func TestACallbackPresentedToTheWrongConnectorIsRefused(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.connect(t, "tenant-a", "entra")
	ctx := context.Background()

	started, err := f.federation.Begin(ctx, "tenant-a", "okta", "")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	params := f.idp.login(t, started.RedirectURL)

	_, err = f.federation.Complete(ctx, "tenant-a", "entra", params, "corr-1")
	be := assertRefusal(t, err, identity.RejectFederationState)
	if !strings.Contains(be.Detail, "okta") {
		t.Errorf("the refusal does not say which connector the flow belongs to: %q", be.Detail)
	}
}

func TestAnUnknownOrDisabledConnectorIsRefusedDistinctly(t *testing.T) {
	f := newFederation(t)
	ctx := context.Background()

	_, err := f.federation.Begin(ctx, "tenant-a", "nope", "")
	assertRefusal(t, err, identity.RejectFederationNoSuchOne)
	if !identity.IsUnknownConnector(err) {
		t.Error("an unknown connector is not recognised as one, so a caller cannot answer 404")
	}

	f.connect(t, "tenant-a", "okta")
	row, err := f.st.Connectors().Get(ctx, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("connector: %v", err)
	}
	row.Enabled = false
	if err := f.st.Connectors().Upsert(ctx, "tenant-a", row); err != nil {
		t.Fatalf("disable: %v", err)
	}
	_, err = f.federation.Begin(ctx, "tenant-a", "okta", "")
	assertRefusal(t, err, identity.RejectFederationDisabled)
}

func TestFederatedGroupsAndLocalGroupsAreIndistinguishableToARule(t *testing.T) {
	// M7: a rule must not care where a group came from.
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.mapping(t, "tenant-a", mappingSRE)
	ctx := context.Background()

	if err := f.st.Groups().Upsert(ctx, "tenant-a", store.Group{ID: "oncall", Source: "local"}); err != nil {
		t.Fatalf("group: %v", err)
	}
	if err := f.st.Groups().AddMember(ctx, "tenant-a", "oncall", "okta:00u1"); err != nil {
		t.Fatalf("member: %v", err)
	}

	if _, err := f.login(t, "tenant-a", "okta"); err != nil {
		t.Fatalf("login: %v", err)
	}
	subject, err := f.st.Subjects().Get(ctx, "tenant-a", "okta:00u1")
	if err != nil {
		t.Fatalf("subject: %v", err)
	}
	// One sorted list, with nothing recording which half is which.
	if !slices.Equal(subject.Groups, []string{"oncall", "sre"}) {
		t.Fatalf("groups: %v", subject.Groups)
	}
}

func TestRolesComeFromBindingsInTheTenantTheLoginHappenedIn(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.mapping(t, "tenant-a", mappingSRE)
	ctx := context.Background()

	// Bound to the MAPPED group, which is how "the SREs are fleet admins"
	// becomes a statement about the group rather than a list that drifts.
	if err := f.st.RoleBindings().Bind(ctx, "tenant-a", store.RoleBinding{
		GroupID: "sre", Role: string(identity.RoleFleetAdmin), GrantedBy: "test",
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// And a binding in ANOTHER tenant that must confer nothing.
	if err := f.st.RoleBindings().Bind(ctx, "tenant-b", store.RoleBinding{
		GroupID: "sre", Role: string(identity.RoleAdmin), GrantedBy: "test",
	}); err != nil {
		t.Fatalf("bind b: %v", err)
	}

	session, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if got := session.Principal.RolesIn("tenant-a").Strings(); !slices.Equal(got, []string{"fleet-admin"}) {
		t.Fatalf("roles in tenant-a: %v", got)
	}
	if session.Principal.MayActIn("tenant-b") {
		t.Fatal("a login in tenant-a produced scope in tenant-b")
	}
}

// ---------------------------------------------------------------------------
// break-glass
// ---------------------------------------------------------------------------

func (f *federationFixture) seedBreakGlass(t *testing.T, tenant store.Tenant, login, password string) {
	t.Helper()
	ctx := context.Background()
	if err := f.st.Subjects().Upsert(ctx, tenant, store.Subject{
		ID:          "break-glass",
		Source:      identity.SourceLocal,
		DisplayName: "Break glass",
		Principals:  []string{login},
		BreakGlass:  true,
	}); err != nil {
		t.Fatalf("subject: %v", err)
	}
	digest, err := identity.HashPassword("break-glass", password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := f.st.SubjectPasswords().Put(ctx, tenant, digest); err != nil {
		t.Fatalf("password: %v", err)
	}
}

func TestABreakGlassLoginIsFlaggedEverywhereItTouches(t *testing.T) {
	// M7: a break-glass login that looks like a normal one is an audit failure.
	f := newFederation(t)
	f.seedBreakGlass(t, "tenant-a", "root", "correct horse battery staple")

	session, err := f.federation.LocalLogin(context.Background(), "tenant-a",
		"root", "correct horse battery staple", "corr-1")
	if err != nil {
		t.Fatalf("break-glass login: %v", err)
	}

	if !session.Principal.BreakGlass {
		t.Error("the principal is not flagged")
	}
	if session.Principal.Source != identity.SourceLocal {
		t.Errorf("source: %q", session.Principal.Source)
	}

	row, err := f.st.NorthPrincipals().Get(context.Background(), "tenant-a", session.Principal.ID)
	if err != nil {
		t.Fatalf("principal row: %v", err)
	}
	if !row.BreakGlass {
		t.Error("the stored credential is not flagged")
	}

	event := f.sink.last()
	if event.Event != "login" || !event.BreakGlass {
		t.Fatalf("the audit record is not a flagged break-glass login: %+v", event)
	}
	if event.Subject != "break-glass" || event.Login != "root" {
		t.Errorf("the record does not name who: %+v", event)
	}
	if event.CorrelationID != "corr-1" {
		t.Errorf("the record does not tie to the request log: %q", event.CorrelationID)
	}
}

func TestABreakGlassLoginThisServerCannotWriteDownIsRefused(t *testing.T) {
	// An unrecorded break-glass login is worse than one that looks normal, so
	// the record is part of the login rather than a side effect of it.
	f := newFederation(t)
	f.seedBreakGlass(t, "tenant-a", "root", "correct horse battery staple")
	f.sink.fail = errSinkDown

	_, err := f.federation.LocalLogin(context.Background(), "tenant-a",
		"root", "correct horse battery staple", "corr-1")
	if err == nil {
		t.Fatal("a break-glass login succeeded with no audit record")
	}
	// And no credential was minted.
	rows, err := f.st.NorthPrincipals().List(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("principals: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("%d credentials exist for a login that was refused", len(rows))
	}
}

var errSinkDown = &sinkError{}

type sinkError struct{}

func (*sinkError) Error() string { return "the audit store is unavailable" }

func TestAWrongBreakGlassPasswordIsRefusedAndRecorded(t *testing.T) {
	f := newFederation(t)
	f.seedBreakGlass(t, "tenant-a", "root", "correct horse battery staple")

	_, err := f.federation.LocalLogin(context.Background(), "tenant-a", "root", "wrong", "corr-1")
	be := assertRefusal(t, err, identity.RejectFederationNotAllowed)
	// A login page is told one thing about a wrong login and a wrong password,
	// on purpose: telling them apart makes this an account oracle.
	if !strings.Contains(be.Message, "login or password") {
		t.Errorf("the disclosed message distinguishes the two: %q", be.Message)
	}

	event := f.sink.last()
	if event.Event != "login_denied" || !event.BreakGlass {
		t.Fatalf("the refusal was not recorded as a flagged break-glass denial: %+v", event)
	}
	if event.Reason == "" {
		t.Error("the record carries no reason, so an operator cannot tell a wrong password from an unknown login")
	}
}

func TestAnUnknownBreakGlassLoginIsRefusedTheSameWay(t *testing.T) {
	f := newFederation(t)
	f.seedBreakGlass(t, "tenant-a", "root", "correct horse battery staple")

	_, err := f.federation.LocalLogin(context.Background(), "tenant-a", "nobody", "x", "corr-1")
	be := assertRefusal(t, err, identity.RejectFederationNotAllowed)
	if !strings.Contains(be.Message, "login or password") {
		t.Errorf("message: %q", be.Message)
	}
	if got := f.sink.last().Reason; got != "unknown-login" {
		t.Errorf("the server's own reason: %q", got)
	}
}

func TestAFederatedLoginIsNeverFlaggedBreakGlass(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.mapping(t, "tenant-a", mappingSRE)

	session, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session.Principal.BreakGlass {
		t.Fatal("a federated login was flagged break-glass")
	}
	if f.sink.last().BreakGlass {
		t.Fatal("a federated login was recorded as break-glass")
	}
	if f.sink.last().MappingVersion != 1 {
		t.Errorf("the record does not name the mapping version: %d", f.sink.last().MappingVersion)
	}
}

// ---------------------------------------------------------------------------
// credentials
// ---------------------------------------------------------------------------

func TestASessionCredentialAuthenticatesAndARevokedOneDoesNot(t *testing.T) {
	f := newFederation(t)
	f.seedBreakGlass(t, "tenant-a", "root", "correct horse battery staple")
	ctx := context.Background()

	session, err := f.federation.LocalLogin(ctx, "tenant-a", "root", "correct horse battery staple", "corr-1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	principal, ok, err := f.federation.Authenticate(ctx, session.Credential.String())
	if err != nil || !ok {
		t.Fatalf("authenticate: %v %v", ok, err)
	}
	if principal.ID != session.Principal.ID || !principal.BreakGlass {
		t.Fatalf("the resolved principal is not the one that was minted: %+v", principal)
	}

	if err := f.federation.EndSession(ctx, "tenant-a", session.Principal.ID); err != nil {
		t.Fatalf("end session: %v", err)
	}
	if _, ok, err := f.federation.Authenticate(ctx, session.Credential.String()); ok || err != nil {
		t.Fatalf("a revoked session still authenticates: %v %v", ok, err)
	}
}

func TestAnExpiredSessionDoesNotAuthenticate(t *testing.T) {
	f := newFederation(t, identity.WithSessionTTL(time.Hour))
	f.seedBreakGlass(t, "tenant-a", "root", "pw")
	ctx := context.Background()

	session, err := f.federation.LocalLogin(ctx, "tenant-a", "root", "pw", "corr-1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	f.now = f.now.Add(2 * time.Hour)
	if _, ok, err := f.federation.Authenticate(ctx, session.Credential.String()); ok || err != nil {
		t.Fatalf("an expired session still authenticates: %v %v", ok, err)
	}
}

func TestAMalformedOrForgedCredentialIsNotRecognisedAndIsNotAnOutage(t *testing.T) {
	// M11: `ok == false` is the only thing that becomes a 401, and a malformed
	// credential is indistinguishable from a wrong one to the caller.
	f := newFederation(t)
	ctx := context.Background()

	for _, presented := range []string{
		"", "nonsense", "hs.tenant-a", "hs.tenant-a.", "xx.tenant-a.secret",
		"hs.tenant-a.not-the-secret", "ht.tenant-a.not-the-secret",
	} {
		principal, ok, err := f.federation.Authenticate(ctx, presented)
		if err != nil {
			t.Errorf("%q answered an outage: %v", presented, err)
		}
		if ok || principal != nil {
			t.Errorf("%q was accepted", presented)
		}
	}
}

func TestACredentialCarriesItsTenantSoNothingIsLookedUpAcrossTenants(t *testing.T) {
	// M22's shape reused on this surface: a forged prefix fails the digest
	// comparison in a tenant where no such credential exists.
	f := newFederation(t)
	f.seedBreakGlass(t, "tenant-a", "root", "pw")
	ctx := context.Background()

	session, err := f.federation.LocalLogin(ctx, "tenant-a", "root", "pw", "corr-1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	cred := session.Credential
	cred.Tenant = "tenant-b"
	if _, ok, err := f.federation.Authenticate(ctx, cred.String()); ok || err != nil {
		t.Fatalf("a tenant-a credential presented as tenant-b's was accepted: %v %v", ok, err)
	}
}

func TestAnIssuedTokenCarriesItsScopeAndNothingElse(t *testing.T) {
	f := newFederation(t)
	ctx := context.Background()

	cred, principal, err := f.federation.IssueToken(ctx, "tenant-a", identity.TokenRequest{
		DisplayName: "ci",
		Scopes: map[store.Tenant]identity.RoleSet{
			"tenant-a": {identity.RolePolicyAuthor},
		},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if principal.Kind != store.PrincipalToken {
		t.Errorf("kind: %q", principal.Kind)
	}
	if !principal.Can("tenant-a", identity.PermPolicyPublish) {
		t.Error("the token cannot publish policy in its own tenant")
	}
	if principal.Can("tenant-a", identity.PermIdentityWrite) {
		t.Error("a policy author holds identity:write")
	}
	if principal.MayActIn("tenant-b") {
		t.Error("the token may act in a tenant it was not scoped to")
	}

	resolved, ok, err := f.federation.Authenticate(ctx, cred.String())
	if err != nil || !ok {
		t.Fatalf("authenticate: %v %v", ok, err)
	}
	if resolved.ID != principal.ID {
		t.Errorf("resolved %q, minted %q", resolved.ID, principal.ID)
	}
}

func TestATokenScopedToSeveralTenantsResolvesEachIndependently(t *testing.T) {
	// A principal may hold scope in several tenants — that is what makes
	// delegated administration possible upstream of Enterprise's E11 — but a
	// role in one confers nothing in the other.
	f := newFederation(t)
	_, principal, err := f.federation.IssueToken(context.Background(), "tenant-a", identity.TokenRequest{
		DisplayName: "delegated",
		Scopes: map[store.Tenant]identity.RoleSet{
			"tenant-a": {identity.RoleAdmin},
			"tenant-b": {identity.RoleAuditor},
		},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !principal.Can("tenant-a", identity.PermCARotate) {
		t.Error("the admin scope does not hold ca:rotate in tenant-a")
	}
	if principal.Can("tenant-b", identity.PermCARotate) {
		t.Error("the admin scope leaked into tenant-b")
	}
	if !principal.Can("tenant-b", identity.PermAuditRead) {
		t.Error("the auditor scope does not hold audit:read in tenant-b")
	}
	if _, ok := principal.SoleTenant(); ok {
		t.Error("a two-tenant token was given a default tenant")
	}
}

func TestATokenWithNoScopeIsRefused(t *testing.T) {
	f := newFederation(t)
	ctx := context.Background()
	if _, _, err := f.federation.IssueToken(ctx, "tenant-a", identity.TokenRequest{}); err == nil {
		t.Error("a token scoped to nothing was issued")
	}
	if _, _, err := f.federation.IssueToken(ctx, "tenant-a", identity.TokenRequest{
		Scopes: map[store.Tenant]identity.RoleSet{"tenant-a": {}},
	}); err == nil {
		t.Error("a token whose scope names no roles was issued")
	}
}

func TestNoIdPClientSecretAppearsInALogOrAnError(t *testing.T) {
	// The acceptance criterion, and the reason the secret is named by an
	// environment variable rather than stored: there is no column it could be
	// written to and no struct field a log line could print.
	const secretValue = "s3cr3t-do-not-print"
	t.Setenv("HOPLOCK_TEST_OIDC_SECRET", secretValue)

	f := newFederation(t)
	config, err := json.Marshal(identity.OIDCConfig{
		Issuer:          f.idp.issuer(),
		ClientID:        testClientID,
		ClientSecretEnv: "HOPLOCK_TEST_OIDC_SECRET",
		RedirectURI:     "https://control.example.com/cb",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if err := f.st.Connectors().Upsert(context.Background(), "tenant-a", store.Connector{
		Name: "okta", Kind: store.ConnectorOIDC, Enabled: true, Config: config,
	}); err != nil {
		t.Fatalf("connector: %v", err)
	}

	row, err := f.st.Connectors().Get(context.Background(), "tenant-a", "okta")
	if err != nil {
		t.Fatalf("connector: %v", err)
	}
	if strings.Contains(string(row.Config), secretValue) {
		t.Fatal("the client secret is in the stored connector row")
	}
	if !strings.Contains(string(row.Config), "HOPLOCK_TEST_OIDC_SECRET") {
		t.Fatal("the connector does not name the variable it reads the secret from")
	}

	// And an error raised when the variable is MISSING names the variable and
	// not its value.
	t.Setenv("HOPLOCK_TEST_OIDC_SECRET", "")
	started, err := f.federation.Begin(context.Background(), "tenant-a", "okta", "")
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	params := f.idp.login(t, started.RedirectURL)
	_, err = f.federation.Complete(context.Background(), "tenant-a", "okta", params, "corr-1")
	if err == nil {
		t.Fatal("a login completed with no client secret")
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Fatalf("the error carries the secret: %v", err)
	}
	if !strings.Contains(err.Error(), "HOPLOCK_TEST_OIDC_SECRET") {
		t.Errorf("the error does not name the variable: %v", err)
	}
}

func TestTheJoinKeyIsTheIdPsStableIdAndNeverTheUsername(t *testing.T) {
	// A username is a thing an IdP administrator can change, and a join on one
	// turns a rename into an account takeover the next time somebody else takes
	// the old name. So a renamed person is the SAME subject, and the old login
	// is kept rather than dropped — it is also what the SSH path matches a
	// login against (0007).
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.mapping(t, "tenant-a", mappingSRE)
	ctx := context.Background()

	first, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	f.idp.overrideClaims = func(c map[string]any) {
		c["preferred_username"] = "alice.example"
		c["name"] = "Alice Renamed"
	}
	second, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("login after a rename: %v", err)
	}
	if first.Principal.Subject != second.Principal.Subject {
		t.Fatalf("a rename produced a second subject: %q then %q",
			first.Principal.Subject, second.Principal.Subject)
	}

	subject, err := f.st.Subjects().Get(ctx, "tenant-a", second.Principal.Subject)
	if err != nil {
		t.Fatalf("subject: %v", err)
	}
	if !slices.Equal(subject.Principals, []string{"alice", "alice.example"}) {
		t.Fatalf("principals after a rename: %v", subject.Principals)
	}
	if subject.DisplayName != "Alice Renamed" {
		t.Errorf("display name: %q", subject.DisplayName)
	}

	// And exactly one link exists for the person.
	links, err := f.st.FederatedIdentities().ListBySubject(ctx, "tenant-a", subject.ID)
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	if len(links) != 1 || links[0].ExternalSubject != "00u1" {
		t.Fatalf("links: %+v", links)
	}
}

func TestAnExternalSubjectCannotBeMovedOntoAnotherLocalSubject(t *testing.T) {
	// The repository refuses it outright: re-pointing a link would be an
	// account takeover in one UPDATE, so it is a conflict rather than an
	// update, whatever calls it.
	f := newFederation(t)
	ctx := context.Background()

	if err := f.st.FederatedIdentities().Link(ctx, "tenant-a", store.FederatedIdentity{
		Connector: "okta", ExternalSubject: "00u1", SubjectID: "alice",
	}); err != nil {
		t.Fatalf("first link: %v", err)
	}
	// The same link again is idempotent.
	if err := f.st.FederatedIdentities().Link(ctx, "tenant-a", store.FederatedIdentity{
		Connector: "okta", ExternalSubject: "00u1", SubjectID: "alice",
	}); err != nil {
		t.Fatalf("re-link: %v", err)
	}
	// Moving it is not.
	err := f.st.FederatedIdentities().Link(ctx, "tenant-a", store.FederatedIdentity{
		Connector: "okta", ExternalSubject: "00u1", SubjectID: "mallory",
	})
	if !store.IsConflict(err) {
		t.Fatalf("want a conflict, got %v", err)
	}
}

func TestConnectorsAreListedPerTenantAndDisabledOnesAreNot(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.connect(t, "tenant-a", "entra")
	f.connect(t, "tenant-b", "adfs")
	ctx := context.Background()

	row, err := f.st.Connectors().Get(ctx, "tenant-a", "entra")
	if err != nil {
		t.Fatalf("connector: %v", err)
	}
	row.Enabled = false
	if err := f.st.Connectors().Upsert(ctx, "tenant-a", row); err != nil {
		t.Fatalf("disable: %v", err)
	}

	methods, err := f.federation.Connectors(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("connectors: %v", err)
	}
	if len(methods) != 1 || methods[0].Name != "okta" {
		t.Fatalf("tenant-a sign-in methods: %+v", methods)
	}
	methods, err = f.federation.Connectors(ctx, "tenant-b")
	if err != nil {
		t.Fatalf("connectors: %v", err)
	}
	if len(methods) != 1 || methods[0].Name != "adfs" {
		t.Fatalf("tenant-b sign-in methods: %+v", methods)
	}
}
