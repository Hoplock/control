// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/policy/eval"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// Decision records (M4): the other half of the proxy's deliberately vague
// "access denied".
//
// The user is told a session id and nothing else — a precise denial would make
// the proxy an oracle for probing the estate — and an operator resolves that id
// HERE into the whole story. That promise is only kept if this side is total,
// so:
//
//   - the DENY path writes a record too. It is the path that matters most and
//     the easiest one to forget: a deny with no stored explanation makes the
//     promise false for exactly the user who is complaining.
//   - a decision that was evaluated but could not be SERVED is recorded as
//     such. An allow record for a session the proxy was handed a `5xx` for
//     would be a record that lies, and the unservable cases (no path, a rung
//     the estate cannot take) are precisely the ones somebody will be reading
//     the record to understand.
//   - `conn.hop_trail` is stored as an input. "Which hop asked, and what had it
//     already been through" is the first question anybody debugging a chained
//     session asks, and it is unrecoverable afterwards.

// The effects a record can carry.
const (
	// EffectAllow is a decision that was served as a snapshot.
	EffectAllow = "allow"
	// EffectDeny is a decision to refuse, which the proxy relayed as
	// "access denied".
	EffectDeny = "deny"
	// EffectUnserved is an evaluation that allowed and an answer that could
	// not be given: no route, a capability the estate does not have, a
	// vocabulary the caller cannot read. The user saw an outage, and this is
	// the record that says why.
	EffectUnserved = "unserved"
)

// recordedInputs is the input document a record stores.
//
// It is this package's shape rather than the engine's: `model.Input` carries a
// netip.Addr and a time.Location and is tuned for evaluation, while this is
// tuned for being read back in five years by somebody with a session id. 0014
// replays from it.
type recordedInputs struct {
	Now        time.Time         `json:"now"`
	Subject    recordedSubject   `json:"subject"`
	Target     recordedTarget    `json:"target"`
	Context    recordedContext   `json:"context"`
	Grants     []recordedGrant   `json:"grants,omitempty"`
	Connection recordedConnState `json:"connection"`
}

type recordedSubject struct {
	ID     string            `json:"id"`
	Source string            `json:"source,omitempty"`
	Groups []string          `json:"groups,omitempty"`
	Claims map[string]string `json:"claims,omitempty"`
	Method string            `json:"auth_method,omitempty"`
	MFA    bool              `json:"mfa"`
	// Known reports whether this server holds a record for the subject. A
	// decision taken for a subject it does not know saw no groups and no
	// claims, and a reader has to be able to tell that from a subject that
	// genuinely has none.
	Known bool `json:"known"`
}

type recordedTarget struct {
	Hostname string            `json:"hostname"`
	Port     int32             `json:"port,omitempty"`
	Zone     string            `json:"zone,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Known    bool              `json:"known"`
}

type recordedContext struct {
	ProxyID    string `json:"proxy_id"`
	SourceAddr string `json:"source_addr,omitempty"`
	// HopTrail is the whole reason this document exists beside the digest.
	HopTrail []string `json:"hop_trail,omitempty"`
}

type recordedConnState struct {
	SessionID     string `json:"session_id,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	PolicyVersion int32  `json:"policy_version,omitempty"`
}

type recordedGrant struct {
	ID        string `json:"id"`
	Origin    string `json:"origin,omitempty"`
	Scope     string `json:"scope,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// recordedExplanation is the engine's account of the answer, plus what the
// layers above the engine decided.
type recordedExplanation struct {
	Effect          string   `json:"effect"`
	Basis           string   `json:"basis,omitempty"`
	Rule            string   `json:"rule,omitempty"`
	RuleLine        int      `json:"rule_line,omitempty"`
	Terms           []string `json:"terms,omitempty"`
	Obligations     []string `json:"obligations,omitempty"`
	DenyReason      string   `json:"deny_reason,omitempty"`
	Grant           string   `json:"grant,omitempty"`
	BundleDigest    string   `json:"bundle_digest,omitempty"`
	RulesConsidered int      `json:"rules_considered,omitempty"`
	// RouteType and NextProxyID are the routing layer's answer, which the
	// engine never sees: the same rule legitimately answers `nexthop` at the
	// edge and `direct` behind it.
	RouteType   string `json:"route_type,omitempty"`
	NextProxyID string `json:"next_proxy_id,omitempty"`
	// Unserved is why an allowed decision could not be answered, empty when
	// it was.
	Unserved string `json:"unserved,omitempty"`
	// CacheKey is the hint this decision was issued under, so that an
	// operator withdrawing it (0009) can name it.
	CacheKey string `json:"cache_key,omitempty"`
}

// record is one decision on its way to storage.
type record struct {
	id        string
	effect    string
	inputs    recordedInputs
	expl      recordedExplanation
	snapshot  *contract.AuthorizeResponse
	subjectID string
	targetID  string
	proxyID   string
	sessionID string
	decidedAt time.Time
}

// write stores the record.
//
// It is a SYNCHRONOUS, single-row insert on the decision path, and that is a
// deliberate trade against M5's "record without putting a synchronous write on
// the critical path if you can avoid it". The alternative — a queue drained
// behind the response — buys a fraction of a millisecond and costs the one
// guarantee this record exists for: under load, exactly when the queue is
// behind, the decisions an operator most needs to explain are the ones missing.
// "We allowed it but cannot say why" is a worse failure than a slightly slower
// allow.
//
// So a failed write is an OUTAGE and the answer is a `5xx`, on the allow path
// and the deny path alike. Turning a deny that could not be recorded into a
// `401` would keep the user's experience and break the promise made to the
// operator resolving it; turning it into a `5xx` is honest, and M11 is not bent
// by it — nothing here turns an outage into a decision.
func (s *Service) write(ctx context.Context, tenant store.Tenant, rec record) error {
	inputs, err := json.Marshal(rec.inputs)
	if err != nil {
		return fmt.Errorf("decision: encoding the inputs of %s: %w", rec.id, err)
	}
	explanation, err := json.Marshal(rec.expl)
	if err != nil {
		return fmt.Errorf("decision: encoding the explanation of %s: %w", rec.id, err)
	}
	var snapshot json.RawMessage
	if rec.snapshot != nil {
		snapshot, err = json.Marshal(rec.snapshot)
		if err != nil {
			return fmt.Errorf("decision: encoding the snapshot of %s: %w", rec.id, err)
		}
	}

	return s.store.Decisions().Insert(ctx, tenant, store.Decision{
		ID:           rec.id,
		SubjectID:    rec.subjectID,
		TargetID:     rec.targetID,
		InputsDigest: inputsDigest(inputs),
		Inputs:       inputs,
		Explanation:  explanation,
		Effect:       rec.effect,
		ProxyID:      rec.proxyID,
		SessionID:    rec.sessionID,
		MatchedRule:  rec.expl.Rule,
		Obligations:  rec.expl.Obligations,
		Snapshot:     snapshot,
		DecidedAt:    rec.decidedAt,
	})
}

// inputsDigest fixes the inputs an evaluation saw, so a simulation can say
// whether it is replaying the same question (0014).
//
// It is taken over the ENCODED document rather than over a hand-written
// concatenation of fields, so that a field added to the document is a field in
// the digest without anybody having to remember to add it.
func inputsDigest(encoded []byte) string {
	sum := sha256.Sum256(encoded)
	return "sha256:" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// newRecordedInputs renders the assembled inputs for storage.
func newRecordedInputs(in assembled, req *contract.AuthorizeRequest) recordedInputs {
	out := recordedInputs{
		Now: in.input.Now.UTC(),
		Subject: recordedSubject{
			ID:     in.input.Subject.ID,
			Source: in.input.Subject.Source,
			Groups: in.input.Subject.Groups,
			Claims: in.input.Subject.Claims,
			Method: string(in.input.Subject.AuthMethod),
			MFA:    in.input.Subject.MFA,
			Known:  in.subjectKnown,
		},
		Target: recordedTarget{
			Hostname: in.input.Target.Hostname,
			Port:     in.input.Target.Port,
			Zone:     in.input.Target.Zone,
			Labels:   in.input.Target.Labels,
			Known:    in.known,
		},
		Context: recordedContext{
			ProxyID:    in.input.Context.ProxyID,
			SourceAddr: addrString(in.input.Context.SourceAddr),
			HopTrail:   in.trail,
		},
		Connection: recordedConnState{
			SessionID:     req.Conn.SessionID,
			ClientVersion: req.Conn.ClientVersion,
		},
	}
	if req.PolicyVersion != nil {
		out.Connection.PolicyVersion = *req.PolicyVersion
	}
	for _, g := range in.input.Grants {
		out.Grants = append(out.Grants, recordedGrant{
			ID:        g.ID,
			Origin:    string(g.Origin),
			Scope:     g.Scope.Name,
			ExpiresAt: timeString(g.ExpiresAt),
		})
	}
	return out
}

// newRecordedExplanation renders the engine's explanation for storage.
func newRecordedExplanation(e eval.Explanation) recordedExplanation {
	out := recordedExplanation{
		Effect:          string(e.Effect),
		Basis:           string(e.Basis),
		Rule:            e.Rule,
		RuleLine:        e.RuleLine,
		DenyReason:      e.DenyReason,
		Grant:           e.Grant,
		BundleDigest:    e.BundleDigest,
		RulesConsidered: e.RulesConsidered,
	}
	for _, t := range e.Terms {
		out.Terms = append(out.Terms, t.String())
	}
	for _, o := range e.Obligations {
		out.Obligations = append(out.Obligations, string(o))
	}
	sort.Strings(out.Obligations)
	return out
}

func addrString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}

func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// decisionIDAlphabet is base32 without padding, upper-case: an id that survives
// being read aloud, pasted into a ticket, and typed back in.
var decisionIDAlphabet = base32.StdEncoding.WithPadding(base32.NoPadding)

// newDecisionID mints the id the contract carries and an operator resolves.
//
// It is random rather than derived from the decision, because an id derived
// from its inputs would be the same id for two identical questions asked a week
// apart — and "resolve this id" would then answer with a decision that is not
// the one the user is complaining about.
func newDecisionID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on any supported platform; if it ever
		// does, a decision with no id is not one to serve.
		panic("decision: reading randomness for a decision id: " + err.Error())
	}
	return "d_" + strings.ToLower(decisionIDAlphabet.EncodeToString(b[:]))
}

// obligationStrings renders a snapshot's obligations for the record.
func obligationStrings(obs []model.Obligation) []string {
	if len(obs) == 0 {
		return nil
	}
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		out = append(out, string(o.Kind))
	}
	sort.Strings(out)
	return out
}
