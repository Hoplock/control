// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/hoplock/control/internal/store"
)

// The layer above the brokers: flow rows, the claim mapping, subject
// provisioning, and the session a login produces.
//
// Everything here is protocol-agnostic. That is not tidiness — it is what makes
// the phase's security property provable. "An unmapped claim cannot influence a
// decision" is a statement about ONE function ([Mapping.Apply]) called from ONE
// place ([Federation.Complete]); if OIDC and SAML each provisioned their own
// subjects, the property would be a statement about two code paths that happen
// to agree today.

// Defaults for the federation service.
const (
	// DefaultFlowTTL is how long a started login may take to come back. It
	// bounds the window in which a stolen `state` is worth anything.
	DefaultFlowTTL = 10 * time.Minute
	// DefaultSessionTTL is how long a console session lives. Short, because
	// M7's whole point is that identity is short-lived: a session outliving
	// the IdP's own view of the person is the thing federation is supposed
	// to remove.
	DefaultSessionTTL = 8 * time.Hour
	// MaxSessionTTL caps what configuration may ask for.
	MaxSessionTTL = 24 * time.Hour
)

// Federation owns a login from start to session.
type Federation struct {
	st      *store.Store
	roles   RoleResolver
	brokers BrokerFactory
	log     *slog.Logger
	now     func() time.Time
	audit   AuditSink

	flowTTL    time.Duration
	sessionTTL time.Duration
}

// BrokerFactory builds a broker from a stored connector row.
//
// It is an interface so a test can substitute a broker without a real IdP, and
// so that the construction cost (discovery, certificate parsing) can be cached
// by an implementation without this file knowing.
type BrokerFactory interface {
	Broker(ctx context.Context, tenant store.Tenant, c store.Connector) (Broker, error)
}

// AuditSink is where this package writes the records M7 requires.
//
// It is an interface rather than the audit package so that `internal/identity`
// keeps no dependency on the audit store's shape, and so that a deployment
// without an audit store still authenticates — a login that fails because the
// audit chain is unavailable is an availability decision nobody made.
//
// A BREAK-GLASS LOGIN THAT DOES NOT REACH HERE IS AN AUDIT FAILURE, so the one
// call site that emits one treats a sink error as a refusal rather than as
// something to shrug at. See [Federation.LocalLogin].
type AuditSink interface {
	// AuthEvent records an authentication outcome. It must be durable
	// before it returns.
	AuthEvent(ctx context.Context, tenant store.Tenant, e AuthEvent) error
}

// AuthEvent is one authentication outcome, as the audit store needs it.
type AuthEvent struct {
	// Event is the record kind: "login", "login_denied", "logout",
	// "token_issued".
	Event string
	// Subject, Login and Source name who authenticated and through what.
	Subject string
	Login   string
	Source  string
	// BreakGlass is the flag M7 requires. It is asserted here rather than
	// inferred by a reader from Source, because "local" will one day mean
	// something else and this must not quietly stop meaning break-glass.
	BreakGlass bool
	// MappingVersion is the claim-mapping version that produced the
	// identity's attributes, zero when none did.
	MappingVersion int
	// Groups and Attributes are what the mapping produced.
	Groups     []string
	Attributes map[string]string
	// PrincipalID identifies the credential that was minted, where one was.
	PrincipalID string
	// Reason is the refusal code on a denial, empty otherwise. It is the
	// server's own vocabulary and is never disclosed to the caller.
	Reason string
	// CorrelationID ties the record to the request log.
	CorrelationID string
}

// FederationOption configures a Federation.
type FederationOption func(*Federation)

// WithFederationClock overrides the clock. Tests use it.
func WithFederationClock(now func() time.Time) FederationOption {
	return func(f *Federation) {
		if now != nil {
			f.now = now
		}
	}
}

// WithFlowTTL sets how long a started login may take to come back.
func WithFlowTTL(d time.Duration) FederationOption {
	return func(f *Federation) {
		if d > 0 {
			f.flowTTL = d
		}
	}
}

// WithSessionTTL sets how long a session lives, clamped to MaxSessionTTL.
func WithSessionTTL(d time.Duration) FederationOption {
	return func(f *Federation) {
		if d > 0 {
			f.sessionTTL = min(d, MaxSessionTTL)
		}
	}
}

// WithAuditSink wires the audit store.
func WithAuditSink(s AuditSink) FederationOption {
	return func(f *Federation) { f.audit = s }
}

// WithFederationLogger sets the logger.
func WithFederationLogger(l *slog.Logger) FederationOption {
	return func(f *Federation) {
		if l != nil {
			f.log = l
		}
	}
}

// WithBrokerFactory overrides how brokers are built.
func WithBrokerFactory(bf BrokerFactory) FederationOption {
	return func(f *Federation) {
		if bf != nil {
			f.brokers = bf
		}
	}
}

// NewFederation builds the service.
func NewFederation(st *store.Store, opts ...FederationOption) (*Federation, error) {
	if st == nil {
		return nil, fmt.Errorf("identity.NewFederation: a store is required")
	}
	f := &Federation{
		st:         st,
		roles:      NewStoreRoles(st),
		brokers:    DefaultBrokerFactory{},
		log:        slog.Default(),
		now:        time.Now,
		flowTTL:    DefaultFlowTTL,
		sessionTTL: DefaultSessionTTL,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f, nil
}

// ConnectorSummary is what a login page needs to know about a connector.
type ConnectorSummary struct {
	Name        string              `json:"name"`
	Kind        store.ConnectorKind `json:"kind"`
	DisplayName string              `json:"display_name"`
}

// Connectors lists a tenant's enabled connectors.
func (f *Federation) Connectors(ctx context.Context, tenant store.Tenant) ([]ConnectorSummary, error) {
	rows, err := f.st.Connectors().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectorSummary, 0, len(rows))
	for _, c := range rows {
		if !c.Enabled {
			continue
		}
		out = append(out, ConnectorSummary{Name: c.Name, Kind: c.Kind, DisplayName: c.DisplayName})
	}
	return out, nil
}

// Started is where to send the browser, and the state the flow is keyed by.
type Started struct {
	RedirectURL string
	State       string
}

// Begin starts a login against one connector.
func (f *Federation) Begin(ctx context.Context, tenant store.Tenant, connector, loginHint string) (Started, error) {
	broker, err := f.broker(ctx, tenant, connector)
	if err != nil {
		return Started{}, err
	}

	state, err := randomToken(stateBytes)
	if err != nil {
		return Started{}, err
	}

	begun, err := broker.Begin(ctx, BeginRequest{State: state, LoginHint: loginHint})
	if err != nil {
		return Started{}, err
	}

	now := f.now()
	if err := f.st.FlowStates().Begin(ctx, tenant, store.FlowState{
		State:        state,
		Connector:    connector,
		Kind:         broker.Kind(),
		Nonce:        begun.Nonce,
		PKCEVerifier: begun.PKCEVerifier,
		RequestID:    begun.RequestID,
		CreatedAt:    now,
		ExpiresAt:    now.Add(f.flowTTL),
	}); err != nil {
		return Started{}, err
	}
	return Started{RedirectURL: begun.RedirectURL, State: state}, nil
}

// Session is a completed login: the credential to hand back, and who it is.
type Session struct {
	// Credential is the secret. It is returned ONCE and never stored.
	Credential Credential
	// Principal is who the session is.
	Principal *Principal
	// ExpiresAt is when the session stops working.
	ExpiresAt time.Time
}

// Complete finishes a login and mints a session.
//
// THE ORDER IS THE POINT. The flow row is consumed FIRST — before the broker is
// asked anything — so a replayed callback is refused without a token exchange,
// and two concurrent replays cannot both pass. Then the assertion is verified,
// then the mapping is applied, then the subject is written, then the session is
// minted. Nothing reads a claim between the verification and the mapping.
func (f *Federation) Complete(ctx context.Context, tenant store.Tenant, connector string, params url.Values, correlationID string) (Session, error) {
	state := params.Get("state")
	if state == "" {
		state = params.Get("RelayState")
	}
	if state == "" {
		return Session{}, refuse(RejectFederationState,
			"this login could not be completed", "the callback carried no state")
	}

	flow, err := f.st.FlowStates().Consume(ctx, tenant, state, f.now())
	switch {
	case store.IsNotFound(err):
		return Session{}, refuse(RejectFederationState,
			"this login could not be completed", "no flow was issued for this state")
	case store.IsConflict(err):
		// Two different facts that deserve two different audit records
		// even though they share a status code (PLAN §6).
		return Session{}, refuse(RejectFederationState,
			"this login has already been completed", "the flow was already consumed")
	case err != nil:
		return Session{}, err
	}
	if flow.Connector != connector {
		return Session{}, refuse(RejectFederationState,
			"this login could not be completed",
			fmt.Sprintf("the flow belongs to connector %q", flow.Connector))
	}
	if !f.now().Before(flow.ExpiresAt) {
		return Session{}, refuse(RejectFederationExpired,
			"this login took too long; please try again", "the flow expired")
	}

	broker, err := f.broker(ctx, tenant, connector)
	if err != nil {
		return Session{}, err
	}

	assertion, err := broker.Complete(ctx, CompleteRequest{Flow: flow, Params: params})
	if err != nil {
		f.recordDenial(ctx, tenant, connector, assertion.Subject, err, correlationID)
		return Session{}, err
	}

	mapping, err := f.ActiveMapping(ctx, tenant)
	if err != nil {
		return Session{}, err
	}
	attributes := mapping.Apply(assertion)

	subject, err := f.provision(ctx, tenant, assertion, attributes)
	if err != nil {
		return Session{}, err
	}

	session, err := f.mintSession(ctx, tenant, subject, assertion, attributes)
	if err != nil {
		return Session{}, err
	}

	f.record(ctx, tenant, AuthEvent{
		Event:          "login",
		Subject:        subject.ID,
		Login:          firstNonEmpty(assertion.Login, subject.ID),
		Source:         connector,
		MappingVersion: attributes.MappingVersion,
		Groups:         attributes.Groups,
		Attributes:     attributes.Claims,
		PrincipalID:    session.Principal.ID,
		CorrelationID:  correlationID,
	})
	return session, nil
}

// Metadata returns what the IdP needs to know about this server.
func (f *Federation) Metadata(ctx context.Context, tenant store.Tenant, connector string) (string, []byte, error) {
	broker, err := f.broker(ctx, tenant, connector)
	if err != nil {
		return "", nil, err
	}
	return broker.Metadata(ctx)
}

// ActiveMapping returns the tenant's active claim mapping, or the empty mapping
// when it has none.
//
// The empty mapping maps NOTHING, which is the safe direction: a tenant that
// federates before authoring a mapping gets identities with no attributes, and
// a policy that grants on attributes grants nothing. Passing claims through
// until a mapping exists would be a hole that closes only when somebody
// remembers.
func (f *Federation) ActiveMapping(ctx context.Context, tenant store.Tenant) (*Mapping, error) {
	row, err := f.st.ClaimMappings().Active(ctx, tenant)
	if store.IsNotFound(err) {
		return EmptyMapping(), nil
	}
	if err != nil {
		return nil, err
	}
	m, err := ParseMapping([]byte(row.Document))
	if err != nil {
		// A STORED MAPPING THAT NO LONGER PARSES IS AN OUTAGE, NOT AN
		// EMPTY MAPPING. Falling back to "maps nothing" would silently
		// strip every attribute in the tenant, which reads as a
		// permissions bug and would be debugged as one.
		return nil, fmt.Errorf("identity: tenant %s's active claim mapping (version %d) no longer parses: %w",
			tenant, row.Version, err)
	}
	m.Version = row.Version
	return m, nil
}

// PutMapping validates and stores a new mapping version, activating it.
//
// Validation happens HERE and not at login: a mapping that cannot be applied is
// a document an operator has to fix, and discovering that at the first login
// after a change means discovering it during an outage.
func (f *Federation) PutMapping(ctx context.Context, tenant store.Tenant, document []byte, author string) (*Mapping, error) {
	parsed, err := ParseMapping(document)
	if err != nil {
		return nil, err
	}
	row, err := f.st.ClaimMappings().Put(ctx, tenant, parsed.Document(), parsed.Digest, author)
	if err != nil {
		return nil, err
	}
	if err := f.st.ClaimMappings().Activate(ctx, tenant, row.Version); err != nil {
		return nil, err
	}
	parsed.Version = row.Version
	return parsed, nil
}

// provision creates or updates the local subject behind an assertion.
//
// The join key is the IdP's own stable identifier, never the username: a
// username is a thing an IdP administrator can change, and a join on one turns
// a rename into an account takeover the next time somebody else takes the old
// name.
func (f *Federation) provision(ctx context.Context, tenant store.Tenant, a Assertion, attrs Attributes) (store.Subject, error) {
	link, err := f.st.FederatedIdentities().Resolve(ctx, tenant, a.Connector, a.Subject)
	subjectID := ""
	switch {
	case err == nil:
		subjectID = link.SubjectID
	case store.IsNotFound(err):
		// A subject id derived from the connector and the external
		// subject, so that two tenants whose IdPs both call somebody
		// `00u1` never collide — and neither do two connectors within
		// one tenant.
		subjectID = a.Connector + ":" + a.Subject
	default:
		return store.Subject{}, err
	}

	existing, err := f.st.Subjects().Get(ctx, tenant, subjectID)
	if err != nil && !store.IsNotFound(err) {
		return store.Subject{}, err
	}

	local, err := f.st.Groups().GroupsOf(ctx, tenant, subjectID)
	if err != nil {
		return store.Subject{}, err
	}

	subject := store.Subject{
		ID:          subjectID,
		Source:      a.Connector,
		DisplayName: firstNonEmpty(a.DisplayName, existing.DisplayName, subjectID),
		Principals:  principalsFor(a, existing),
		// A rule must not care where a group came from (M7), so the two
		// sources are merged into one sorted list here and nothing
		// downstream can tell them apart.
		Groups: mergeGroups(local, attrs.Groups),
		Claims: attrs.Claims,
		// A federated subject is never break-glass. The flag stays as it
		// was on an existing row so that an operator who marked a local
		// account break-glass does not have it cleared by a login.
		BreakGlass: existing.BreakGlass,
		// The version that produced Claims and the mapped half of
		// Groups. The decision record names it (M4, M7), and it is
		// written here because this is the only place the two are
		// produced together.
		MappingVersion: attrs.MappingVersion,
	}
	if err := f.st.Subjects().Upsert(ctx, tenant, subject); err != nil {
		return store.Subject{}, err
	}
	if err := f.st.FederatedIdentities().Link(ctx, tenant, store.FederatedIdentity{
		Connector:       a.Connector,
		ExternalSubject: a.Subject,
		SubjectID:       subjectID,
	}); err != nil {
		if store.IsConflict(err) {
			// This external subject is already somebody else here.
			// Refusing is the only safe answer: re-pointing it would
			// be an account takeover in one UPDATE.
			return store.Subject{}, refuse(RejectFederationNotAllowed,
				"this identity cannot be used to sign in here",
				"the external subject is linked to a different local subject")
		}
		return store.Subject{}, err
	}
	return subject, nil
}

// principalsFor decides which logins a federated subject may present.
//
// The asserted username is added to what the row already had rather than
// replacing it, because a subject's principals are also what the SSH path
// matches a login against (0007) and dropping one would break access the person
// already has.
func principalsFor(a Assertion, existing store.Subject) []string {
	out := slices.Clone(existing.Principals)
	if a.Login != "" {
		out = append(out, a.Login)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func mergeGroups(local, mapped []string) []string {
	out := append(slices.Clone(local), mapped...)
	slices.Sort(out)
	return slices.Compact(out)
}

// mintSession stores a session credential for a subject.
func (f *Federation) mintSession(ctx context.Context, tenant store.Tenant, subject store.Subject, a Assertion, attrs Attributes) (Session, error) {
	roles, err := f.roles.RolesFor(ctx, tenant, subject.ID, subject.Groups)
	if err != nil {
		return Session{}, err
	}

	cred, err := MintCredential(store.PrincipalSession, tenant)
	if err != nil {
		return Session{}, err
	}
	id, err := randomToken(12)
	if err != nil {
		return Session{}, err
	}
	principalID := "sess-" + id

	now := f.now()
	expires := now.Add(f.sessionTTL)
	scopes := map[store.Tenant]RoleSet{tenant: roles}

	row := store.NorthPrincipal{
		ID:             principalID,
		Kind:           store.PrincipalSession,
		SubjectID:      subject.ID,
		DisplayName:    subject.DisplayName,
		Source:         firstNonEmpty(a.Connector, SourceLocal),
		BreakGlass:     subject.BreakGlass && a.Connector == "",
		MappingVersion: attrs.MappingVersion,
		SessionDigest:  cred.Digest(),
		Scopes:         scopesToRow(scopes),
		ExpiresAt:      expires,
	}
	if err := f.st.NorthPrincipals().Put(ctx, tenant, row); err != nil {
		return Session{}, err
	}

	p := principalFromRow(row)
	p.Tenant = tenant
	p.Groups = slices.Clone(subject.Groups)
	p.Attributes = attrs.Claims
	return Session{Credential: cred, Principal: p, ExpiresAt: expires}, nil
}

// LocalLogin is the break-glass and development path (M7).
//
// IT IS NEVER THE PRODUCTION PATH, and every record it produces says so. The
// flag is set here, travels on the principal, is stored on the row, and is
// written into the audit record — and the audit write is treated as part of the
// login rather than as a side effect: a break-glass login this server cannot
// write down is one it refuses, because a break-glass login that looks like a
// normal one is an audit failure and an unrecorded one is worse.
func (f *Federation) LocalLogin(ctx context.Context, tenant store.Tenant, login, password, correlationID string) (Session, error) {
	subject, err := f.st.Subjects().GetByPrincipal(ctx, tenant, login)
	if store.IsNotFound(err) {
		f.record(ctx, tenant, AuthEvent{
			Event: "login_denied", Login: login, Source: SourceLocal,
			BreakGlass: true, Reason: string(DenyUnknownLogin), CorrelationID: correlationID,
		})
		return Session{}, refuse(RejectFederationNotAllowed,
			"that login or password is not right", "no local subject presents this login")
	}
	if err != nil {
		return Session{}, err
	}

	digest, err := f.st.SubjectPasswords().Get(ctx, tenant, subject.ID)
	if store.IsNotFound(err) {
		f.record(ctx, tenant, AuthEvent{
			Event: "login_denied", Subject: subject.ID, Login: login, Source: SourceLocal,
			BreakGlass: true, Reason: string(DenyNoPassword), CorrelationID: correlationID,
		})
		return Session{}, refuse(RejectFederationNotAllowed,
			"that login or password is not right", "the subject has no local password")
	}
	if err != nil {
		return Session{}, err
	}

	ok, err := verifyPassword(digest, password)
	if err != nil {
		// An unverifiable digest is an OUTAGE, not a wrong password
		// (M11): an unknown KDF is a deployment fault and the user did
		// nothing wrong.
		return Session{}, err
	}
	if !ok {
		f.record(ctx, tenant, AuthEvent{
			Event: "login_denied", Subject: subject.ID, Login: login, Source: SourceLocal,
			BreakGlass: true, Reason: string(DenyBadPassword), CorrelationID: correlationID,
		})
		return Session{}, refuse(RejectFederationNotAllowed,
			"that login or password is not right", "the password did not verify")
	}

	roles, err := f.roles.RolesFor(ctx, tenant, subject.ID, subject.Groups)
	if err != nil {
		return Session{}, err
	}
	cred, err := MintCredential(store.PrincipalSession, tenant)
	if err != nil {
		return Session{}, err
	}
	id, err := randomToken(12)
	if err != nil {
		return Session{}, err
	}

	now := f.now()
	expires := now.Add(f.sessionTTL)
	row := store.NorthPrincipal{
		ID:            "sess-" + id,
		Kind:          store.PrincipalSession,
		SubjectID:     subject.ID,
		DisplayName:   subject.DisplayName,
		Source:        SourceLocal,
		BreakGlass:    true,
		SessionDigest: cred.Digest(),
		Scopes:        scopesToRow(map[store.Tenant]RoleSet{tenant: roles}),
		ExpiresAt:     expires,
	}

	// The audit record comes BEFORE the credential exists to be used. A
	// sink failure here refuses the login: see this method's doc comment.
	if err := f.recordStrict(ctx, tenant, AuthEvent{
		Event:         "login",
		Subject:       subject.ID,
		Login:         login,
		Source:        SourceLocal,
		BreakGlass:    true,
		Groups:        subject.Groups,
		PrincipalID:   row.ID,
		CorrelationID: correlationID,
	}); err != nil {
		return Session{}, err
	}
	if err := f.st.NorthPrincipals().Put(ctx, tenant, row); err != nil {
		return Session{}, err
	}

	f.log.WarnContext(ctx, "a break-glass login succeeded",
		"event", "break_glass_login",
		"tenant", tenant.String(),
		"subject", subject.ID,
		"principal_id", row.ID,
		"correlation_id", correlationID,
	)

	p := principalFromRow(row)
	p.Tenant = tenant
	p.Groups = slices.Clone(subject.Groups)
	return Session{Credential: cred, Principal: p, ExpiresAt: expires}, nil
}

// Authenticate resolves a presented north-bound credential.
//
// The three-way return is M11's, the same shape 0007 used for the proxy
// credential: an error is an OUTAGE, `ok == false` is the only thing that
// becomes a 401, and a principal is a principal. There is no path by which a
// database failure answers "not recognised".
func (f *Federation) Authenticate(ctx context.Context, presented string) (*Principal, bool, error) {
	cred, ok := ParseCredential(presented)
	if !ok {
		return nil, false, nil
	}

	var (
		row store.NorthPrincipal
		err error
	)
	switch cred.Kind {
	case store.PrincipalSession:
		row, err = f.st.NorthPrincipals().BySessionDigest(ctx, cred.Tenant, cred.Digest())
	case store.PrincipalToken:
		row, err = f.st.NorthPrincipals().ByTokenDigest(ctx, cred.Tenant, cred.Digest())
	default:
		return nil, false, nil
	}
	if store.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !row.Live(f.now()) {
		return nil, false, nil
	}

	// A best-effort stamp. A failure to record that a credential was used
	// must not fail the request that used it.
	if terr := f.st.NorthPrincipals().Touch(ctx, cred.Tenant, row.ID, f.now()); terr != nil {
		f.log.WarnContext(ctx, "a principal's last-used stamp could not be written",
			"event", "north_principal_touch_failed", "principal_id", row.ID, "error", terr)
	}

	p := principalFromRow(row)
	p.Tenant = cred.Tenant
	if p.Subject != "" {
		// Groups and attributes come from THIS server's store, never
		// from the credential: the same rule 0008 applies to a subject's
		// groups on the decision path.
		if s, serr := f.st.Subjects().Get(ctx, cred.Tenant, p.Subject); serr == nil {
			p.Groups = slices.Clone(s.Groups)
			p.Attributes = s.Claims
		} else if !store.IsNotFound(serr) {
			return nil, false, serr
		}
	}
	return p, true, nil
}

// EndSession revokes a session credential.
func (f *Federation) EndSession(ctx context.Context, tenant store.Tenant, principalID string) error {
	if err := f.st.NorthPrincipals().Revoke(ctx, tenant, principalID, f.now()); err != nil {
		return err
	}
	f.record(ctx, tenant, AuthEvent{Event: "logout", PrincipalID: principalID})
	return nil
}

// TokenRequest is what an operator asks for when minting an API token.
type TokenRequest struct {
	// DisplayName is what an operator recognises the token by.
	DisplayName string
	// Scopes is the set of tenants the token may act in, with the roles it
	// holds in each. A scope naming a tenant other than the issuing one is
	// delegated administration, which Enterprise governs (its E11); the
	// MECHANISM is here because the queries are here.
	Scopes map[store.Tenant]RoleSet
	// TTL bounds the token's life. Zero means it does not expire, which an
	// operator must choose explicitly.
	TTL time.Duration
}

// IssueToken mints a scoped API token.
//
// The secret is returned once and never stored. It must not be logged, echoed
// in an error, or written to an audit record: an audit record of a credential
// is a credential in the audit store, which is the one place in this system
// designed never to forget anything (0007's rule, and the same reason).
func (f *Federation) IssueToken(ctx context.Context, tenant store.Tenant, req TokenRequest) (Credential, *Principal, error) {
	if len(req.Scopes) == 0 {
		return Credential{}, nil, fmt.Errorf("identity: a token must name at least one tenant it may act in")
	}
	for t, rs := range req.Scopes {
		if t == "" {
			return Credential{}, nil, fmt.Errorf("identity: a token scope must name a tenant")
		}
		if len(rs) == 0 {
			return Credential{}, nil, fmt.Errorf("identity: a token's scope in tenant %s names no roles, so it could reach nothing", t)
		}
	}

	cred, err := MintCredential(store.PrincipalToken, tenant)
	if err != nil {
		return Credential{}, nil, err
	}
	id, err := randomToken(12)
	if err != nil {
		return Credential{}, nil, err
	}

	row := store.NorthPrincipal{
		ID:          "tok-" + id,
		Kind:        store.PrincipalToken,
		DisplayName: req.DisplayName,
		Source:      SourceLocal,
		TokenDigest: cred.Digest(),
		Scopes:      scopesToRow(req.Scopes),
	}
	if req.TTL > 0 {
		row.ExpiresAt = f.now().Add(req.TTL)
	}
	if err := f.st.NorthPrincipals().Put(ctx, tenant, row); err != nil {
		return Credential{}, nil, err
	}

	f.record(ctx, tenant, AuthEvent{
		Event:       "token_issued",
		Source:      SourceLocal,
		PrincipalID: row.ID,
	})

	p := principalFromRow(row)
	p.Tenant = tenant
	return cred, p, nil
}

// broker resolves a connector into a broker, refusing an unknown or disabled
// one.
func (f *Federation) broker(ctx context.Context, tenant store.Tenant, connector string) (Broker, error) {
	row, err := f.st.Connectors().Get(ctx, tenant, connector)
	if store.IsNotFound(err) {
		return nil, refuse(RejectFederationNoSuchOne,
			"there is no such sign-in method", "connector "+connector+" is not configured for this tenant")
	}
	if err != nil {
		return nil, err
	}
	if !row.Enabled {
		return nil, refuse(RejectFederationDisabled,
			"that sign-in method is not available", "connector "+connector+" is disabled")
	}
	return f.brokers.Broker(ctx, tenant, row)
}

// record writes an audit record, best effort.
//
// Best effort everywhere EXCEPT the break-glass path, which uses recordStrict.
// The asymmetry is deliberate and is stated in [AuditSink].
func (f *Federation) record(ctx context.Context, tenant store.Tenant, e AuthEvent) {
	if err := f.recordStrict(ctx, tenant, e); err != nil {
		f.log.ErrorContext(ctx, "an authentication record could not be written",
			"event", "auth_record_failed", "tenant", tenant.String(), "auth_event", e.Event, "error", err)
	}
}

func (f *Federation) recordStrict(ctx context.Context, tenant store.Tenant, e AuthEvent) error {
	if f.audit == nil {
		return nil
	}
	return f.audit.AuthEvent(ctx, tenant, e)
}

func (f *Federation) recordDenial(ctx context.Context, tenant store.Tenant, connector, subject string, cause error, correlationID string) {
	reason := "outage"
	if be, ok := BrokerRefusal(cause); ok {
		reason = be.Code
	}
	f.record(ctx, tenant, AuthEvent{
		Event: "login_denied", Subject: subject, Source: connector,
		Reason: reason, CorrelationID: correlationID,
	})
}

// DefaultBrokerFactory builds brokers straight from connector rows.
//
// It caches nothing: discovery is cached inside [OIDCBroker], and a SAML
// broker's construction is parsing a certificate. A deployment that wants a
// broker pool can supply its own factory.
type DefaultBrokerFactory struct {
	// Options are passed to every broker it builds. Tests use it to inject
	// a clock and an HTTP client.
	Options []BrokerOption
}

// Broker builds a broker for a connector row.
func (d DefaultBrokerFactory) Broker(_ context.Context, _ store.Tenant, c store.Connector) (Broker, error) {
	switch c.Kind {
	case store.ConnectorOIDC:
		cfg, err := ParseOIDCConfig(c.Config)
		if err != nil {
			return nil, err
		}
		return NewOIDCBroker(c.Name, cfg, d.Options...)
	case store.ConnectorSAML:
		cfg, err := ParseSAMLConfig(c.Config)
		if err != nil {
			return nil, err
		}
		return NewSAMLBroker(c.Name, cfg, d.Options...)
	}
	// A kind the closed enum does not name is a row a newer release wrote.
	// It is an outage rather than a refusal: the operator configured
	// something this build cannot serve, and the user did nothing wrong.
	return nil, fmt.Errorf("identity: connector %q is of a kind this build cannot broker (%q)", c.Name, c.Kind)
}

// IsUnknownConnector reports whether an error is an unknown-connector refusal.
//
// It exists so the transport can answer 404 for one rather than the 401 every
// other refusal gets: "there is no such sign-in method" is a fact about this
// server's configuration, not about the caller's credential.
func IsUnknownConnector(err error) bool {
	be, ok := BrokerRefusal(err)
	return ok && be.Code == RejectFederationNoSuchOne
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
