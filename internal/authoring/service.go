// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package authoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/policy/compile"
	"github.com/hoplock/control/internal/policy/model"
	"github.com/hoplock/control/internal/store"
)

// THE BUNDLE LIFECYCLE. A bundle is uploaded, validated and stored as a new
// immutable version; it is diffed against any other; it is activated by naming
// its version, and rolling back is activating an older one. Nothing edits a
// stored version and nothing deletes one: a decision record names the digest of
// the bundle that made it (M4), so every version stays readable forever.
//
//	uploaded ──activate──▶ active ──activate another──▶ inactive ──activate──▶ active
//
// Apply is upload-then-activate in one call, for CI: an identical source maps
// to the version already holding it, so a pipeline re-run does not mint
// versions.

// Options configures a Service.
type Options struct {
	Store *store.Store
	Fleet *fleet.Registry
	// Emitter writes the operator record of every upload and activation, in
	// the act's own transaction.
	Emitter *audit.Emitter
	// Reader is where the `target.algorithm_policy_unmet` evidence is read
	// from. Nil reads none.
	Reader *audit.Reader
	// Validators are the registered governance checks (ext.PolicyValidator).
	// They run after Control's own, every one of them, on every report.
	Validators []ext.Bound[ext.PolicyValidator]
	// Interpreters replaces DefaultInterpreters when non-empty.
	Interpreters []string
	// UnmetWindow is how far back an unmet-policy record still warns. Zero
	// takes DefaultUnmetWindow.
	UnmetWindow time.Duration
	Now         func() time.Time
}

// DefaultUnmetWindow is how far back a `target.algorithm_policy_unmet` record
// still warns an author: long enough to span a maintenance cycle, short enough
// that a device upgraded last quarter stops being reported.
const DefaultUnmetWindow = 30 * 24 * time.Hour

// unmetLimit bounds how many unmet records one report reads.
const unmetLimit = 1000

// Service is the policy lifecycle.
type Service struct {
	st           *store.Store
	fleet        *fleet.Registry
	emitter      *audit.Emitter
	reader       *audit.Reader
	validators   []ext.Bound[ext.PolicyValidator]
	interpreters []string
	unmetWindow  time.Duration
	now          func() time.Time
}

// New builds a Service.
func New(o Options) (*Service, error) {
	if o.Store == nil || o.Fleet == nil || o.Emitter == nil {
		return nil, fmt.Errorf("authoring: a store, a fleet registry and an audit emitter are required")
	}
	s := &Service{
		st: o.Store, fleet: o.Fleet, emitter: o.Emitter, reader: o.Reader,
		validators: o.Validators, interpreters: o.Interpreters, unmetWindow: o.UnmetWindow, now: o.Now,
	}
	if len(s.interpreters) == 0 {
		s.interpreters = DefaultInterpreters
	}
	if s.unmetWindow <= 0 {
		s.unmetWindow = DefaultUnmetWindow
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Candidate is a source that compiled.
type Candidate struct {
	Source  []byte
	Digest  string
	Bundle  *model.Bundle
	Program *compile.Program
}

// Digest renders a source's content hash the way a stored version carries it.
func Digest(source []byte) string {
	sum := sha256.Sum256(source)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Compile parses and compiles a source for a tenant. An invalid one answers
// the rejections and no candidate; it is not an error.
func Compile(tenant store.Tenant, source []byte) ([]Rejection, *Candidate) {
	var rejections []Rejection
	b, err := model.Parse(source)
	if err != nil {
		var rs model.Rejections
		if errors.As(err, &rs) {
			return rejectionsOf(rs), nil
		}
		return []Rejection{{Code: string(model.CodeDocumentMalformed), Message: err.Error()}}, nil
	}
	if string(b.Tenant) != "" && string(b.Tenant) != string(tenant) {
		rejections = append(rejections, Rejection{
			Code: CodeTenantMismatch, Parameters: map[string]string{"bundle": string(b.Tenant), "tenant": string(tenant)},
			Message: fmt.Sprintf("the bundle names tenant %q and was submitted to tenant %q; a bundle says which "+
				"tenant it serves, so change its `tenant:` or submit it there [%s]", b.Tenant, tenant, CodeTenantMismatch),
		})
	}
	prog, err := compile.Compile(b)
	if err != nil {
		var rs model.Rejections
		if !errors.As(err, &rs) {
			return append(rejections, Rejection{Code: string(model.CodeDocumentMalformed), Message: err.Error()}), nil
		}
		rejections = append(rejections, rejectionsOf(rs)...)
	}
	if len(rejections) > 0 {
		return rejections, nil
	}
	return nil, &Candidate{Source: source, Digest: Digest(source), Bundle: b, Program: prog}
}

// Validate answers whether a source may be published and what its author
// should know first. An error is an outage; a refusal is in the report.
func (s *Service) Validate(ctx context.Context, tenant store.Tenant, source []byte, author string) (Report, *Candidate, error) {
	rep := Report{Digest: Digest(source), Rejections: []Rejection{}, Findings: []Finding{}}
	rejections, cand := Compile(tenant, source)
	if cand == nil {
		rep.Rejections = rejections
		return rep, nil, nil
	}
	rep.Valid = true
	findings, err := s.findings(ctx, tenant, cand, "", author)
	if err != nil {
		return Report{}, nil, err
	}
	rep.Findings = findings
	return rep, cand, nil
}

func (s *Service) findings(ctx context.Context, tenant store.Tenant, cand *Candidate, version, author string) ([]Finding, error) {
	facts, err := s.gather(ctx, tenant, cand.Bundle)
	if err != nil {
		return nil, err
	}
	out := controlFindings(cand.Bundle, cand.Program, facts, s.interpreters)

	if len(s.validators) > 0 {
		active := ""
		if b, err := s.st.PolicyBundles().GetActive(ctx, tenant); err == nil {
			active = strconv.FormatInt(b.Version, 10)
		} else if !store.IsNotFound(err) {
			return nil, err
		}
		doc := ext.PolicyDocument{
			Ref:    ext.PolicyBundleRef{Tenant: ext.Tenant(tenant), Version: version, Digest: cand.Digest},
			Source: cand.Source, ContentType: "application/yaml", AuthorID: author,
			ProposedAt: s.now().UTC(), ActiveVersion: active,
		}
		for _, v := range s.validators {
			fs, err := v.Impl.Validate(ctx, doc)
			if err != nil {
				// An error is an outage, not a rejection: the difference
				// decides whether an author sees "refused because" or
				// "validation is down" (ext.PolicyValidator).
				return nil, fmt.Errorf("authoring: policy validator %s: %w", v.Registration.Provider, err)
			}
			for _, f := range fs {
				params := make(map[string]any, len(f.Detail))
				for k, val := range f.Detail {
					params[k] = val
				}
				out = append(out, Finding{
					ID:       findingID(f.Code, v.Registration.Provider, f.Path),
					Severity: severityOf(f.Severity), Code: f.Code, Source: v.Registration.Provider,
					Rule: f.Path, Parameters: params, Message: f.Message,
				})
			}
		}
	}
	sortFindings(out)
	return out, nil
}

func (s *Service) gather(ctx context.Context, tenant store.Tenant, b *model.Bundle) (Facts, error) {
	now := s.now()
	facts := Facts{Now: now, TTL: s.fleet.TargetObservationTTL()}
	var err error
	if facts.Satisfaction, err = s.fleet.CheckPolicy(ctx, tenant, fleet.Requirements(b)); err != nil {
		return Facts{}, err
	}
	if facts.Proxies, err = s.st.Proxies().List(ctx, tenant); err != nil {
		return Facts{}, err
	}
	if facts.Declarations, err = s.fleet.AlgorithmDeclarations(ctx, tenant); err != nil {
		return Facts{}, err
	}
	if facts.Kex, err = s.fleet.TargetKex(ctx, tenant); err != nil {
		return Facts{}, err
	}
	if s.reader != nil {
		facts.Unmet, err = s.reader.AlgorithmPolicyUnmet(ctx, tenant, "", now.Add(-s.unmetWindow), now.Add(time.Minute), unmetLimit)
		if err != nil {
			return Facts{}, err
		}
	}
	return facts, nil
}

// InvalidError is a source the compiler refused.
type InvalidError struct{ Report Report }

func (e *InvalidError) Error() string {
	parts := make([]string, 0, len(e.Report.Rejections))
	for _, r := range e.Report.Rejections {
		parts = append(parts, r.Message)
	}
	return "authoring: the bundle does not compile: " + strings.Join(parts, "; ")
}

// BlockedError is an activation a registered validator's blocking finding
// stopped. It cannot be accepted away.
type BlockedError struct{ Findings []Finding }

func (e *BlockedError) Error() string {
	return fmt.Sprintf("authoring: %d blocking finding(s) stop this activation", len(e.Findings))
}

// NotAcceptedError is an activation with warnings the author has not accepted.
// Accepting them, by id, is the override.
type NotAcceptedError struct{ Findings []Finding }

func (e *NotAcceptedError) Error() string {
	return fmt.Sprintf("authoring: %d warning(s) must be accepted before this activation", len(e.Findings))
}

// Version is one stored bundle as the lifecycle reports it.
type Version struct {
	Version    int64     `json:"version"`
	Digest     string    `json:"digest"`
	UploadedBy string    `json:"uploaded_by"`
	UploadedAt time.Time `json:"uploaded_at"`
	Active     bool      `json:"active"`
}

func versionOf(b store.PolicyBundle) Version {
	return Version{Version: b.Version, Digest: b.Hash, UploadedBy: b.UploadedBy, UploadedAt: b.UploadedAt.UTC(), Active: b.Active}
}

// UploadResult is a stored version and the report it was stored with.
type UploadResult struct {
	Version Version `json:"version"`
	// Existing reports that an identical source was already stored, and
	// this is that version: an upload never mints a duplicate.
	Existing bool   `json:"existing"`
	Report   Report `json:"report"`
}

// Upload validates a source and stores it as a new, inactive version.
func (s *Service) Upload(ctx context.Context, tenant store.Tenant, source []byte, actor audit.OperatorActor) (UploadResult, error) {
	rep, cand, err := s.Validate(ctx, tenant, source, actor.Subject)
	if err != nil {
		return UploadResult{}, err
	}
	if cand == nil {
		return UploadResult{}, &InvalidError{Report: rep}
	}
	existing, err := s.st.PolicyBundles().List(ctx, tenant)
	if err != nil {
		return UploadResult{}, err
	}
	for _, b := range existing {
		if b.Hash == cand.Digest {
			return UploadResult{Version: versionOf(b), Existing: true, Report: rep}, nil
		}
	}

	uploader := actor.Subject
	if uploader == "" {
		uploader = actor.Principal
	}
	var stored store.PolicyBundle
	// Two uploads racing for one version number: the loser's insert is a
	// conflict, and it takes the next number rather than failing.
	for attempt := 0; ; attempt++ {
		err = s.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
			next, err := tx.PolicyBundles().NextVersion(ctx, tenant)
			if err != nil {
				return err
			}
			stored = store.PolicyBundle{Version: next, Source: source, Hash: cand.Digest, UploadedBy: uploader}
			if err := tx.PolicyBundles().Insert(ctx, tenant, stored); err != nil {
				return err
			}
			return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
				Event: audit.EventPolicyUploaded, Actor: actor, After: strconv.FormatInt(next, 10),
				Fields:   map[string]string{"digest": cand.Digest},
				Message:  fmt.Sprintf("policy bundle %d uploaded", next),
				Severity: contract.SeverityInfo,
			})
		})
		if err == nil || !store.IsConflict(err) || attempt >= 2 {
			break
		}
	}
	if err != nil {
		return UploadResult{}, err
	}
	got, err := s.st.PolicyBundles().Get(ctx, tenant, stored.Version)
	if err != nil {
		return UploadResult{}, err
	}
	return UploadResult{Version: versionOf(got), Report: rep}, nil
}

// ActivateResult is what an activation changed.
type ActivateResult struct {
	Version Version `json:"version"`
	// Previous is the version that was active before, zero for none.
	Previous int64 `json:"previous"`
	// Unchanged reports that the version was already active; nothing was
	// written and nothing was recorded.
	Unchanged bool `json:"unchanged"`
	// Rollback reports that an older version was made active.
	Rollback bool `json:"rollback"`
	// Accepted are the warnings the author accepted, which the activation's
	// audit record names.
	Accepted []string `json:"accepted"`
	Report   Report   `json:"report"`
}

// Activate makes a stored version the active one. The report is computed again
// against the fleet as it is NOW, because the fleet may have moved since the
// upload, and every warning on it must be accepted by id.
func (s *Service) Activate(ctx context.Context, tenant store.Tenant, version int64, accepted []string, actor audit.OperatorActor) (ActivateResult, error) {
	b, err := s.st.PolicyBundles().Get(ctx, tenant, version)
	if err != nil {
		return ActivateResult{}, err
	}
	rep, cand, err := s.Validate(ctx, tenant, b.Source, actor.Subject)
	if err != nil {
		return ActivateResult{}, err
	}
	if cand == nil {
		// A stored version this release no longer compiles. It is refused,
		// never served: serving it would be an outage on every connection.
		return ActivateResult{}, &InvalidError{Report: rep}
	}
	if blocking := rep.Blocking(); len(blocking) > 0 {
		return ActivateResult{}, &BlockedError{Findings: blocking}
	}
	if missing := rep.Unaccepted(accepted); len(missing) > 0 {
		return ActivateResult{}, &NotAcceptedError{Findings: missing}
	}

	res := ActivateResult{Report: rep, Accepted: rep.Accepted(accepted)}
	if res.Accepted == nil {
		res.Accepted = []string{}
	}
	err = s.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		before, err := tx.PolicyBundles().GetActive(ctx, tenant)
		switch {
		case err == nil:
			res.Previous = before.Version
		case !store.IsNotFound(err):
			return err
		}
		if res.Previous == version {
			res.Unchanged = true
			return nil
		}
		if err := tx.PolicyBundles().Activate(ctx, tenant, version); err != nil {
			return err
		}
		res.Rollback = res.Previous > version
		beforeStr := ""
		if res.Previous != 0 {
			beforeStr = strconv.FormatInt(res.Previous, 10)
		}
		return s.emitter.OperatorEvent(ctx, tx, tenant, audit.OperatorAct{
			Event: audit.EventPolicyActivated, Actor: actor,
			Before: beforeStr, After: strconv.FormatInt(version, 10), Accepted: res.Accepted,
			Fields:  map[string]string{"digest": b.Hash, "rollback": strconv.FormatBool(res.Rollback)},
			Message: fmt.Sprintf("policy bundle %d activated", version),
		})
	})
	if err != nil {
		return ActivateResult{}, err
	}
	got, err := s.st.PolicyBundles().Get(ctx, tenant, version)
	if err != nil {
		return ActivateResult{}, err
	}
	res.Version = versionOf(got)
	return res, nil
}

// ApplyResult is an upload and the activation that followed it.
type ApplyResult struct {
	Upload   UploadResult   `json:"upload"`
	Activate ActivateResult `json:"activate"`
}

// Apply uploads a source and activates it: GitOps in one call. A source that
// does not compile is refused before anything is stored; one whose warnings
// are not accepted is stored, inactive, and the refusal names the warnings and
// the version, so the next run accepts them by id.
func (s *Service) Apply(ctx context.Context, tenant store.Tenant, source []byte, accepted []string, actor audit.OperatorActor) (ApplyResult, error) {
	up, err := s.Upload(ctx, tenant, source, actor)
	if err != nil {
		return ApplyResult{}, err
	}
	act, err := s.Activate(ctx, tenant, up.Version.Version, accepted, actor)
	if err != nil {
		return ApplyResult{Upload: up}, err
	}
	return ApplyResult{Upload: up, Activate: act}, nil
}

// Versions lists every stored version, newest first.
func (s *Service) Versions(ctx context.Context, tenant store.Tenant) ([]Version, error) {
	rows, err := s.st.PolicyBundles().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(rows))
	for _, b := range rows {
		out = append(out, versionOf(b))
	}
	return out, nil
}

// Bundle returns one stored version with its source.
func (s *Service) Bundle(ctx context.Context, tenant store.Tenant, version int64) (Version, []byte, error) {
	b, err := s.st.PolicyBundles().Get(ctx, tenant, version)
	if err != nil {
		return Version{}, nil, err
	}
	return versionOf(b), b.Source, nil
}

// Active returns the active version with its source. Absent is store's
// not-found: a tenant with no policy.
func (s *Service) Active(ctx context.Context, tenant store.Tenant) (Version, []byte, error) {
	b, err := s.st.PolicyBundles().GetActive(ctx, tenant)
	if err != nil {
		return Version{}, nil, err
	}
	return versionOf(b), b.Source, nil
}
