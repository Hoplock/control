// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// The expectation file is everything the suite knows about the server in front
// of it. No login, target, fingerprint, token, or URL is hard-coded anywhere in
// this binary, which is the property phase 0017 needs: pointing the suite at the
// real server is a new expectation file, not a code change.
//
// It is decoded STRICTLY. An unknown key is a typo that would otherwise silently
// disable a case, and a suite whose cases can be disabled by a typo is a suite
// that grades nothing.

// Expectations describes the server under test.
type Expectations struct {
	// ProxyID is the proxy the suite presents itself as on every ConnMeta.
	ProxyID string `yaml:"proxy_id"`
	// SecondProxyID is a DIFFERENT proxy id, used only by the uid-lease cases:
	// exclusivity is per target, not per proxy, and a server that keyed its
	// cursor by proxy would look perfect to a single-proxy suite.
	SecondProxyID string `yaml:"second_proxy_id"`

	Auth         AuthExpectations       `yaml:"auth"`
	Authorize    AuthorizeExpectations  `yaml:"authorize"`
	HostKeys     HostKeyExpectations    `yaml:"hostkeys"`
	Capabilities CapabilityExpectations `yaml:"capabilities"`
	UIDs         UIDExpectations        `yaml:"uids"`
	Logs         LogExpectations        `yaml:"logs"`
	Events       EventExpectations      `yaml:"events"`
}

// KeyMaterial is a public key the suite offers. Blob may be empty: the contract
// carries it, the server under test decides whether it needs it, and a suite
// that insisted on real key bytes would need a real CA to write a fixture.
type KeyMaterial struct {
	Type        string `yaml:"type"`
	Fingerprint string `yaml:"fingerprint"`
	Blob        string `yaml:"blob"`
}

// AuthExpectations describes the identities the server is configured with.
type AuthExpectations struct {
	// CertAccept is a login plus a key the server accepts.
	CertAccept struct {
		Login  string      `yaml:"login"`
		Key    KeyMaterial `yaml:"key"`
		Target string      `yaml:"target"`
		// ExpectSubject, when set, is asserted on the returned identity.
		ExpectSubject string `yaml:"expect_subject"`
	} `yaml:"cert_accept"`

	// CertDeny is a login plus a key the server rejects. The assertion is that
	// the answer is 401 with the envelope — never a 200 with no identity.
	CertDeny struct {
		Login string      `yaml:"login"`
		Key   KeyMaterial `yaml:"key"`
	} `yaml:"cert_deny"`

	// PasswordNoMFA is a login whose password alone completes the flow.
	PasswordNoMFA struct {
		Login    string `yaml:"login"`
		Password string `yaml:"password"`
	} `yaml:"password_no_mfa"`

	// PasswordMFAApprove is a login whose password starts an out-of-band
	// challenge that eventually resolves to authenticated.
	PasswordMFAApprove struct {
		Login    string `yaml:"login"`
		Password string `yaml:"password"`
		MaxPolls int    `yaml:"max_polls"`
	} `yaml:"password_mfa_approve"`

	// PasswordMFADeny is a login whose challenge resolves to a denial.
	PasswordMFADeny struct {
		Login    string `yaml:"login"`
		Password string `yaml:"password"`
		MaxPolls int    `yaml:"max_polls"`
	} `yaml:"password_mfa_deny"`

	// PasswordMFAExpiry is a login whose challenge is left to expire. The suite
	// waits WaitSeconds past the challenge's own `expires_at` and then polls;
	// the answer must be 401, because an expired challenge is a deny.
	PasswordMFAExpiry struct {
		Login       string `yaml:"login"`
		Password    string `yaml:"password"`
		WaitSeconds int    `yaml:"wait_seconds"`
	} `yaml:"password_mfa_expiry"`

	// UnknownChallengeToken is a token the server has never issued.
	UnknownChallengeToken string `yaml:"unknown_challenge_token"`
}

// AuthorizeRoute names one authorize call the server is configured to answer.
type AuthorizeRoute struct {
	Login   string `yaml:"login"`
	Subject string `yaml:"subject"`
	Target  string `yaml:"target"`
	Port    int32  `yaml:"port"`
	ProxyID string `yaml:"proxy_id"`
}

// DefaultPair grades one absent-value default. It needs BOTH halves: a route
// the server answers without the field, and a route it answers with it. An
// assertion that only ever sees the absent half passes vacuously against a
// server that cannot express the field at all, which is the failure mode the
// acceptance criteria name.
type DefaultPair struct {
	Default AuthorizeRoute `yaml:"default"`
	Set     AuthorizeRoute `yaml:"set"`
}

// AuthorizeExpectations describes the routes the server serves.
type AuthorizeExpectations struct {
	// Direct and Nexthop are the two route types.
	Direct  AuthorizeRoute `yaml:"direct"`
	Nexthop AuthorizeRoute `yaml:"nexthop"`
	// Deny is an identity/target pair the server refuses: a 401 decision.
	Deny AuthorizeRoute `yaml:"deny"`
	// Full is the route carrying as much of the vocabulary as the server can
	// express, for the envelope-shape assertion.
	Full AuthorizeRoute `yaml:"full"`

	// Ladder is a route whose target_auth_ladder carries a brokered-key entry.
	// `params.username` is required on every method the contract defines, and
	// this is where that tightening is graded.
	Ladder AuthorizeRoute `yaml:"ladder"`

	// Defaults grades the v4 absent-value defaults in pairs.
	Defaults struct {
		Enforcement     DefaultPair `yaml:"enforcement"`
		SessionDeadline DefaultPair `yaml:"session_deadline"`
		RequireCapture  DefaultPair `yaml:"require_session_capture"`
		Concurrency     DefaultPair `yaml:"concurrency"`
	} `yaml:"defaults"`

	// Negotiation is the route the version cases are run against. It should be
	// the richest policy the server serves: the more vocabulary it carries, the
	// more a thinned low-version answer has to drop to be caught.
	Negotiation AuthorizeRoute `yaml:"negotiation"`

	// DeviceFields grades `device_field.<name>`. WithFields is a route whose
	// ephemeral-account rung carries device fields; WithoutFields is a
	// comparable route that carries none. The suite finds the lowest
	// policy_version at which WithoutFields is served and asserts WithFields is
	// served at the same one — the namespace is open, so a name inside it is
	// not a new policy field and demands no bump. Asserting that against a
	// literal version number would make the case stale at the next revision.
	DeviceFields struct {
		WithFields    AuthorizeRoute `yaml:"with_fields"`
		WithoutFields AuthorizeRoute `yaml:"without_fields"`
	} `yaml:"device_fields"`
}

// HostKeyExpectations describes the host-key cases.
type HostKeyExpectations struct {
	// FirstSighting is a target/key pair the server has never seen. The suite
	// makes the fingerprint unique per run, so this is the target only.
	FirstSighting struct {
		Target string `yaml:"target"`
		Type   string `yaml:"type"`
	} `yaml:"first_sighting"`
	// Known is a target/key pair the server already trusts.
	Known struct {
		Target      string `yaml:"target"`
		Type        string `yaml:"type"`
		Fingerprint string `yaml:"fingerprint"`
	} `yaml:"known"`
}

// CapabilityExpectations describes the capability-report case.
type CapabilityExpectations struct {
	Target   string `yaml:"target"`
	Platform string `yaml:"platform"`
	// ExpectReportAfterSeconds, when >= 0, is asserted exactly. Leave it at -1
	// (the default) when the server has no opinion: the field is optional, and
	// absent or 0 leaves the interval to the proxy.
	ExpectReportAfterSeconds int32 `yaml:"expect_report_after_seconds"`
}

// UIDExpectations describes the uid-lease cases. This is the endpoint whose
// invariant cannot be graded one request at a time, so most of these fields
// exist to let the suite lease repeatedly and compare.
type UIDExpectations struct {
	// Target is leased against many times. Give the suite a target nothing else
	// uses: the cursor only ever advances, so these leases are not repeatable
	// against a server that has already served them — which is the point.
	Target   string `yaml:"target"`
	RangeMin int32  `yaml:"range_min"`
	RangeMax int32  `yaml:"range_max"`
	UIDCount int32  `yaml:"uid_count"`
	// AbandonedLeases is how many blocks the suite takes and never allocates
	// from. A server that reclaims one is the exact failure this endpoint
	// exists to prevent.
	AbandonedLeases int `yaml:"abandoned_leases"`
	// ExpiryWaitSeconds is how long to wait for a block's term to run out when
	// the server does not state one. When the grant carries `term_seconds`, the
	// suite waits that instead and this is the slack added to it.
	ExpiryWaitSeconds int `yaml:"expiry_wait_seconds"`
	// Exhaustion is a SEPARATE target with a deliberately narrow range, leased
	// until the cursor reaches the top of it and the server answers 409.
	Exhaustion struct {
		Target   string `yaml:"target"`
		RangeMin int32  `yaml:"range_min"`
		RangeMax int32  `yaml:"range_max"`
	} `yaml:"exhaustion"`
	// Floor is a THIRD target, used for the observed_floor cases so that
	// raising a cursor deliberately does not disturb the non-overlap target.
	Floor struct {
		Target   string `yaml:"target"`
		RangeMin int32  `yaml:"range_min"`
		RangeMax int32  `yaml:"range_max"`
	} `yaml:"floor"`
}

// LogExpectations describes the log cases.
type LogExpectations struct {
	// ReadURL is how the suite reads back what it ingested — the read path the
	// implementation exposes to the suite. It is an input rather than an
	// endpoint because the contract defines no read path: the priority ack
	// means DURABLE, and the only black-box way to grade that is to ask the
	// server for the record straight afterwards.
	//
	// The assertion is that the record id appears in the response body, so any
	// read path that names the record satisfies it.
	ReadURL string `yaml:"read_url"`
	// BatchSize is how many records go in the batch that is then replayed.
	BatchSize int `yaml:"batch_size"`
}

// EventExpectations describes the revocation stream cases.
type EventExpectations struct {
	// ProxyID is the stream subscribed to; it may differ from the suite's own
	// ProxyID if the server scopes streams narrowly.
	ProxyID string `yaml:"proxy_id"`
	// HeartbeatIntervalSeconds is the FALLBACK bound, for a server that
	// advertises nothing.
	//
	// It used to be the bound itself, because the contract carried no field
	// for one when this suite was written. Upstream Hoplock/proxy#56 added
	// `RevocationEvent.heartbeat_interval_seconds`, so the interval is now a
	// claim the server makes on the stream and the suite grades that claim —
	// both that the server keeps it and that it is inside the contract's
	// ceiling.
	//
	// IT IS STILL REQUIRED IN PRACTICE, AND NOT BY THIS STRUCT. Absent stays
	// a legal answer from a server: it means what every server did before
	// the field existed. But a server advertising nothing, against a file
	// configuring no fallback, is UNGRADEABLE — so the heartbeat cases fail
	// rather than pass, and this key is what makes such a server gradeable
	// again. Leaving it out is therefore safe only for a server that does
	// advertise.
	HeartbeatIntervalSeconds int `yaml:"heartbeat_interval_seconds"`
	// PublishURL is how the suite makes the server emit an event. The contract
	// deliberately defines no endpoint for this — publishing is an operator
	// action, not a proxy-facing one — so the implementation supplies the path
	// and the suite asserts nothing about its shape.
	PublishURL  string `yaml:"publish_url"`
	PublishBody string `yaml:"publish_body"`
	// PublishToken is the credential that path requires, when it is not the
	// proxy token the suite was given.
	//
	// It is a separate key because the publish path is a separate surface:
	// the proxy credential authenticates a proxy asking about decisions,
	// and publishing one is an operator action. A server that serves both
	// from one listener and one credential is free to leave this empty,
	// which is what the proxy's `cmd/mock-control` does.
	PublishToken string `yaml:"publish_token"`
}

// LoadExpectations reads and strictly decodes an expectation file.
func LoadExpectations(path string) (*Expectations, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open expectations: %w", err)
	}
	defer func() { _ = f.Close() }()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	e := &Expectations{
		Capabilities: CapabilityExpectations{ExpectReportAfterSeconds: -1},
	}
	if err := dec.Decode(e); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := e.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return e, nil
}

// validate rejects a file that would make a case silently vacuous. Every field
// it checks is one whose absence turns an assertion into a no-op — which is
// worse than a failure, because it reads as a pass.
func (e *Expectations) validate() error {
	var missing []string
	need := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}

	need("proxy_id", e.ProxyID)
	need("second_proxy_id", e.SecondProxyID)
	need("auth.cert_accept.login", e.Auth.CertAccept.Login)
	need("auth.cert_accept.key.fingerprint", e.Auth.CertAccept.Key.Fingerprint)
	need("auth.cert_deny.key.fingerprint", e.Auth.CertDeny.Key.Fingerprint)
	need("auth.password_mfa_approve.login", e.Auth.PasswordMFAApprove.Login)
	need("auth.password_mfa_deny.login", e.Auth.PasswordMFADeny.Login)
	need("auth.unknown_challenge_token", e.Auth.UnknownChallengeToken)
	need("authorize.direct.target", e.Authorize.Direct.Target)
	need("authorize.nexthop.target", e.Authorize.Nexthop.Target)
	need("authorize.deny.target", e.Authorize.Deny.Target)
	need("authorize.full.target", e.Authorize.Full.Target)
	need("authorize.ladder.target", e.Authorize.Ladder.Target)
	need("authorize.negotiation.target", e.Authorize.Negotiation.Target)
	need("authorize.device_fields.with_fields.target", e.Authorize.DeviceFields.WithFields.Target)
	need("authorize.device_fields.without_fields.target", e.Authorize.DeviceFields.WithoutFields.Target)
	need("hostkeys.first_sighting.target", e.HostKeys.FirstSighting.Target)
	need("hostkeys.known.target", e.HostKeys.Known.Target)
	need("hostkeys.known.fingerprint", e.HostKeys.Known.Fingerprint)
	need("capabilities.target", e.Capabilities.Target)
	need("uids.target", e.UIDs.Target)
	need("uids.exhaustion.target", e.UIDs.Exhaustion.Target)
	need("uids.floor.target", e.UIDs.Floor.Target)
	need("logs.read_url", e.Logs.ReadURL)
	need("events.proxy_id", e.Events.ProxyID)
	need("events.publish_url", e.Events.PublishURL)

	for name, pair := range map[string]DefaultPair{
		"authorize.defaults.enforcement":             e.Authorize.Defaults.Enforcement,
		"authorize.defaults.session_deadline":        e.Authorize.Defaults.SessionDeadline,
		"authorize.defaults.require_session_capture": e.Authorize.Defaults.RequireCapture,
		"authorize.defaults.concurrency":             e.Authorize.Defaults.Concurrency,
	} {
		need(name+".default.target", pair.Default.Target)
		need(name+".set.target", pair.Set.Target)
	}

	if e.UIDs.Exhaustion.RangeMax <= e.UIDs.Exhaustion.RangeMin {
		missing = append(missing, "uids.exhaustion.range_max must exceed range_min")
	}
	// events.heartbeat_interval_seconds is deliberately NOT checked here.
	// It stopped being the bound when upstream Hoplock/proxy#56 put the
	// interval on the wire and became the fallback for a server that
	// advertises none, so a file that omits it is describing a server it
	// expects to advertise rather than a file with a hole in it. What
	// stops that from becoming a vacuous pass is in CheckEvents: a server
	// that advertises nothing with no fallback configured FAILS as
	// ungradeable, which is the outcome this validation used to buy and
	// the only one worth having.

	if len(missing) > 0 {
		return fmt.Errorf("missing or invalid: %s", strings.Join(missing, ", "))
	}
	return nil
}

// defaults fills in the values a file may leave out because the suite has a
// sensible answer, as distinct from the ones validate() insists on because it
// does not.
func (e *Expectations) defaults() {
	if e.Auth.PasswordMFAApprove.MaxPolls <= 0 {
		e.Auth.PasswordMFAApprove.MaxPolls = 20
	}
	if e.Auth.PasswordMFADeny.MaxPolls <= 0 {
		e.Auth.PasswordMFADeny.MaxPolls = 20
	}
	if e.Auth.PasswordMFAExpiry.WaitSeconds <= 0 {
		e.Auth.PasswordMFAExpiry.WaitSeconds = 2
	}
	if e.UIDs.AbandonedLeases <= 0 {
		e.UIDs.AbandonedLeases = 2
	}
	if e.UIDs.ExpiryWaitSeconds <= 0 {
		e.UIDs.ExpiryWaitSeconds = 2
	}
	if e.Logs.BatchSize <= 0 {
		e.Logs.BatchSize = 3
	}
	for _, k := range []*KeyMaterial{&e.Auth.CertAccept.Key, &e.Auth.CertDeny.Key} {
		if k.Type == "" {
			k.Type = "ssh-ed25519"
		}
	}
	if e.HostKeys.FirstSighting.Type == "" {
		e.HostKeys.FirstSighting.Type = "ssh-ed25519"
	}
	if e.HostKeys.Known.Type == "" {
		e.HostKeys.Known.Type = "ssh-ed25519"
	}
}
