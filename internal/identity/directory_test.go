// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/provision"
	"github.com/hoplock/control/internal/store"
)

// A login through a connector tied to the directory (0040): it reaches the
// subject the directory provisioned or nobody, it rewrites nothing on that
// subject, and a deactivated subject is refused through every door.

const directorySubjectID = provision.SubjectIDPrefix + "abc123"

// directorySource is the directory the fixture's sync names (0042), and
// otherSource a second one feeding the same tenant.
const (
	directorySource = "okta-a"
	otherSource     = "azure-b"
)

// provisioned seeds what the identity sync leaves for a person: the subject
// row, with its groups, claims and one login name, and the link.
func (f *federationFixture) provisioned(t *testing.T, tenant store.Tenant, externalID, subjectID string, deactivated bool) store.Subject {
	t.Helper()
	return f.provisionedIn(t, tenant, directorySource, externalID, subjectID, "alice", deactivated)
}

// provisionedIn is provisioned for a source and a login name.
func (f *federationFixture) provisionedIn(t *testing.T, tenant store.Tenant, source, externalID, subjectID, login string, deactivated bool) store.Subject {
	t.Helper()
	ctx := context.Background()
	s := store.Subject{
		ID: subjectID, Source: "hoplock/scim", DisplayName: "Alice (directory)",
		Principals: []string{login}, Groups: []string{"dba"}, Claims: map[string]string{"department": "finance"},
		MappingVersion: 7,
	}
	if err := f.st.Subjects().Upsert(ctx, tenant, s); err != nil {
		t.Fatalf("subject: %v", err)
	}
	link := store.DirectoryLink{
		Source: source, ExternalID: externalID, SubjectID: subjectID, Provider: "hoplock/scim", LinkedAt: time.Now().UTC(),
		Provisioned: true, UpdatedAt: time.Now().UTC(), Username: login,
	}
	if deactivated {
		link.DeactivatedAt, link.DeactivationReason = time.Now().UTC(), "terminated"
	}
	if err := f.st.DirectoryLinks().Put(ctx, tenant, link); err != nil {
		t.Fatalf("link: %v", err)
	}
	return s
}

// tieToDirectory gives a connector a directory claim in the fixture's source,
// and an empty claim unties it.
func (f *federationFixture) tieToDirectory(t *testing.T, tenant store.Tenant, name, claim string) {
	t.Helper()
	source := ""
	if claim != "" {
		source = directorySource
	}
	f.tieToSource(t, tenant, name, claim, source)
}

// tieToSource gives a connector a directory claim and the source it is an id
// in.
func (f *federationFixture) tieToSource(t *testing.T, tenant store.Tenant, name, claim, source string) {
	t.Helper()
	ctx := context.Background()
	c, err := f.st.Connectors().Get(ctx, tenant, name)
	if err != nil {
		t.Fatal(err)
	}
	c.DirectoryClaim, c.DirectorySource = claim, source
	if err := f.st.Connectors().Upsert(ctx, tenant, c); err != nil {
		t.Fatal(err)
	}
}

func subjectCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(context.Background(), `SELECT count(*) FROM subjects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestALoginThroughALinkedConnectorReachesTheProvisionedSubjectAndRewritesNothing(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.tieToDirectory(t, "tenant-a", "okta", "oid")
	f.mapping(t, "tenant-a", mappingSRE)
	want := f.provisioned(t, "tenant-a", "u-1", directorySubjectID, false)
	f.idp.overrideClaims = func(c map[string]any) { c["oid"] = "u-1" }

	session, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if session.Principal.Subject != directorySubjectID {
		t.Fatalf("the login reached %q, want the provisioned %q", session.Principal.Subject, directorySubjectID)
	}
	got, err := f.st.Subjects().Get(context.Background(), "tenant-a", directorySubjectID)
	if err != nil {
		t.Fatal(err)
	}
	// The token carried okta-sre, department=engineering and a login of its
	// own: none of it was written.
	if !slices.Equal(got.Groups, want.Groups) || !slices.Equal(got.Principals, want.Principals) ||
		got.Claims["department"] != "finance" || got.MappingVersion != 7 || got.Source != want.Source {
		t.Errorf("a linked login rewrote the subject: %+v", got)
	}
	if !slices.Equal(session.Principal.Groups, []string{"dba"}) {
		t.Errorf("the session's groups = %v, want the directory's", session.Principal.Groups)
	}
	links, err := f.st.FederatedIdentities().ListBySubject(context.Background(), "tenant-a", directorySubjectID)
	if err != nil || len(links) != 1 || links[0].Connector != "okta" || links[0].ExternalSubject != "00u1" {
		t.Errorf("federated links = %+v, %v", links, err)
	}
	if n := subjectCount(t, f.st); n != 1 {
		t.Errorf("%d subjects after a linked login, want only the directory's", n)
	}
	if f.sink.last().Event != "login" || f.sink.last().Subject != directorySubjectID || f.sink.last().MappingVersion != 7 {
		t.Errorf("the login record = %+v", f.sink.last())
	}
	// The next login resolves the same way.
	if again, err := f.login(t, "tenant-a", "okta"); err != nil || again.Principal.Subject != directorySubjectID {
		t.Errorf("a second login = %+v, %v", again.Principal, err)
	}
}

func TestALinkedLoginWithNoProvisionedMatchIsRefusedAndCreatesNothing(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.tieToDirectory(t, "tenant-a", "okta", "oid")
	f.mapping(t, "tenant-a", mappingSRE)

	for name, claims := range map[string]func(map[string]any){
		"an unknown id":   func(c map[string]any) { c["oid"] = "u-nobody" },
		"no claim at all": func(c map[string]any) {},
		"an empty claim":  func(c map[string]any) { c["oid"] = "  " },
	} {
		f.idp.overrideClaims = claims
		_, err := f.login(t, "tenant-a", "okta")
		be := refusalFor(t, err, identity.RejectFederationNotProvisioned)
		if strings.Contains(be.Message, "oid") {
			t.Errorf("%s: the refusal shown names the claim: %q", name, be.Message)
		}
		if f.sink.last().Event != "login_denied" || f.sink.last().Reason != identity.RejectFederationNotProvisioned {
			t.Errorf("%s: recorded %+v", name, f.sink.last())
		}
	}
	if n := subjectCount(t, f.st); n != 0 {
		t.Errorf("%d subjects were created by refused logins", n)
	}
	var links int
	_ = f.st.Pool().QueryRow(context.Background(), `SELECT count(*) FROM federated_identities`).Scan(&links)
	if links != 0 {
		t.Errorf("%d federated identities were linked by refused logins", links)
	}
}

func TestADeactivatedSubjectIsRefusedThroughEveryDoor(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.tieToDirectory(t, "tenant-a", "okta", "oid")
	f.mapping(t, "tenant-a", mappingSRE)
	f.provisioned(t, "tenant-a", "u-1", directorySubjectID, false)
	f.idp.overrideClaims = func(c map[string]any) { c["oid"] = "u-1" }
	ctx := context.Background()

	// A session minted while the person was active.
	session, err := f.login(t, "tenant-a", "okta")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, ok, err := f.federation.Authenticate(ctx, session.Credential.String()); !ok || err != nil {
		t.Fatalf("the session does not authenticate before the deactivation: %v, %v", ok, err)
	}
	// A local password on the same subject, the break-glass door.
	digest, err := identity.HashPassword(directorySubjectID, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SubjectPasswords().Put(ctx, "tenant-a", digest); err != nil {
		t.Fatal(err)
	}

	link, err := f.st.DirectoryLinks().Get(ctx, "tenant-a", directorySource, "u-1")
	if err != nil {
		t.Fatal(err)
	}
	link.DeactivatedAt, link.DeactivationReason = time.Now().UTC(), "terminated"
	if err := f.st.DirectoryLinks().Put(ctx, "tenant-a", link); err != nil {
		t.Fatal(err)
	}

	// The credential minted before stops authenticating.
	if p, ok, err := f.federation.Authenticate(ctx, session.Credential.String()); ok || err != nil || p != nil {
		t.Errorf("a deactivated subject's session authenticates: %v, %v", ok, err)
	}
	// The linked connector.
	_, err = f.login(t, "tenant-a", "okta")
	_ = refusalFor(t, err, identity.RejectFederationSubjectDeactivated)
	if f.sink.last().Reason != identity.RejectFederationSubjectDeactivated {
		t.Errorf("recorded %+v", f.sink.last())
	}
	// The same connector with its tie removed: the federated identity still
	// resolves to the directory's subject, which is still deactivated.
	f.tieToDirectory(t, "tenant-a", "okta", "")
	_, err = f.login(t, "tenant-a", "okta")
	_ = refusalFor(t, err, identity.RejectFederationSubjectDeactivated)
	// The local path.
	_, err = f.federation.LocalLogin(ctx, "tenant-a", "alice", "correct horse", "corr-2")
	_ = refusalFor(t, err, identity.RejectFederationSubjectDeactivated)
	if f.sink.last().Reason != identity.RejectFederationSubjectDeactivated || !f.sink.last().BreakGlass {
		t.Errorf("the local refusal was recorded as %+v", f.sink.last())
	}
}

// A connector whose name is the allocated ids' prefix cannot reach the
// directory's subject: the link row, not the id's spelling, is the join.
func TestAConnectorNamedLikeTheDirectoryIDPrefixCannotCollideWithIt(t *testing.T) {
	f := newFederation(t)
	prefix := strings.TrimSuffix(provision.SubjectIDPrefix, "-")
	for _, name := range []string{prefix, provision.SubjectIDPrefix} {
		f.connect(t, "tenant-a", name)
	}
	f.mapping(t, "tenant-a", mappingSRE)
	want := f.provisioned(t, "tenant-a", "u-1", directorySubjectID, false)
	suffix := strings.TrimPrefix(directorySubjectID, provision.SubjectIDPrefix)
	f.idp.overrideClaims = func(c map[string]any) {
		c["sub"] = suffix
		c["preferred_username"] = "carol"
	}

	for _, name := range []string{prefix, provision.SubjectIDPrefix} {
		session, err := f.login(t, "tenant-a", name)
		if err != nil {
			t.Fatalf("login through %q: %v", name, err)
		}
		if session.Principal.Subject == directorySubjectID {
			t.Fatalf("a login through %q reached the directory's subject", name)
		}
	}
	got, err := f.st.Subjects().Get(context.Background(), "tenant-a", directorySubjectID)
	if err != nil || !slices.Equal(got.Groups, want.Groups) || !slices.Equal(got.Principals, want.Principals) {
		t.Errorf("the directory's subject after the logins = %+v, %v", got, err)
	}
}

// A login through a connector not tied to the directory that would add a
// login name a directory-managed subject presents is refused.
func TestAnUnlinkedLoginCannotTakeALoginTheDirectoryGave(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.mapping(t, "tenant-a", mappingSRE)
	f.provisioned(t, "tenant-a", "u-1", directorySubjectID, false)

	_, err := f.login(t, "tenant-a", "okta") // asserts preferred_username alice
	_ = refusalFor(t, err, identity.RejectFederationNotAllowed)
	if n := subjectCount(t, f.st); n != 1 {
		t.Errorf("%d subjects after a refused login, want only the directory's", n)
	}
	// Under another name it is an ordinary federated login, as before.
	f.idp.overrideClaims = func(c map[string]any) { c["preferred_username"] = "alice.okta" }
	if _, err := f.login(t, "tenant-a", "okta"); err != nil {
		t.Errorf("an unlinked login under a free name: %v", err)
	}
}

// The login page learns which connectors are tied to the directory.
func TestTheSignInMethodsShowADirectoryClaim(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.connect(t, "tenant-a", "azure")
	f.tieToDirectory(t, "tenant-a", "azure", "oid")
	methods, err := f.federation.Connectors(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range methods {
		got[m.Name] = m.DirectoryClaim + "@" + m.DirectorySource
	}
	if got["azure"] != "oid@"+directorySource || got["okta"] != "@" {
		t.Errorf("directory claims and sources = %v", got)
	}
	doc, _ := json.Marshal(methods)
	if strings.Count(string(doc), "directory_claim") != 1 || strings.Count(string(doc), `"directory_source":"`+directorySource+`"`) != 1 {
		t.Errorf("the methods render %s, want the tied connector's claim and source and nothing for the untied one", doc)
	}
}

// A login through a tied connector looks the person up in the connector's own
// source: one id in two directories is two people, and an id only another
// directory provisioned is nobody here (0042).
func TestATiedConnectorResolvesInItsOwnSource(t *testing.T) {
	f := newFederation(t)
	f.connect(t, "tenant-a", "okta")
	f.connect(t, "tenant-a", "azure")
	f.tieToSource(t, "tenant-a", "okta", "oid", directorySource)
	f.tieToSource(t, "tenant-a", "azure", "oid", otherSource)
	f.mapping(t, "tenant-a", mappingSRE)
	const inA, inB, onlyB = provision.SubjectIDPrefix + "in-a", provision.SubjectIDPrefix + "in-b", provision.SubjectIDPrefix + "only-b"
	f.provisionedIn(t, "tenant-a", directorySource, "u-1", inA, "alice", false)
	f.provisionedIn(t, "tenant-a", otherSource, "u-1", inB, "alice.b", false)
	f.provisionedIn(t, "tenant-a", otherSource, "u-2", onlyB, "bob", false)

	for _, tc := range []struct {
		connector, oid, want string
	}{
		{"okta", "u-1", inA},
		{"azure", "u-1", inB},
		{"azure", "u-2", onlyB},
		{"okta", "u-2", ""},
	} {
		f.idp.overrideClaims = func(c map[string]any) { c["oid"] = tc.oid; c["sub"] = tc.connector + "-" + tc.oid }
		session, err := f.login(t, "tenant-a", tc.connector)
		if tc.want == "" {
			_ = refusalFor(t, err, identity.RejectFederationNotProvisioned)
			continue
		}
		if err != nil || session.Principal.Subject != tc.want {
			t.Errorf("a login through %s carrying %s reached %q (%v), want %q", tc.connector, tc.oid, session.Principal.Subject, err, tc.want)
		}
	}
	if n := subjectCount(t, f.st); n != 3 {
		t.Errorf("%d subjects after the logins, want only the three the directories provisioned", n)
	}
}

func TestADirectorySourceIsHeldToItsPattern(t *testing.T) {
	for source, ok := range map[string]bool{
		"okta": true, "okta-a": true, "0": true, "a-": true, strings.Repeat("a", 63): true,
		"": false, "Okta": false, "-okta": false, "okta_a": false, "okta.a": false, "okta a": false,
		strings.Repeat("a", 64): false, "é": false,
	} {
		if got := identity.DirectorySourceProblem(source) == ""; got != ok {
			t.Errorf("DirectorySourceProblem(%q) accepted = %v, want %v", source, got, ok)
		}
	}
}

func TestADirectoryClaimIsOneToken(t *testing.T) {
	for claim, ok := range map[string]bool{
		"oid": true, "sub": true, "http://schemas.microsoft.com/identity/claims/objectidentifier": true,
		"": false, "two words": false, "tab\there": false, strings.Repeat("c", 300): false, "é": false,
	} {
		if got := identity.DirectoryClaimProblem(claim) == ""; got != ok {
			t.Errorf("DirectoryClaimProblem(%q) accepted = %v, want %v", claim, got, ok)
		}
	}
}
