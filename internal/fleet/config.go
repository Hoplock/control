// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// Configuration distribution (0006).
//
// A proxy's BOOTSTRAP config is local — it must be, to start at all — but
// everything above it belongs here, so an operator configures a fleet rather than
// N files: which zones it serves, its relay registrations, its contract
// expectations, its log shipping cadence.
//
// Four properties, and each is a thing that goes wrong without it:
//
//   - **Versioned and immutable.** A version that can be edited in place is a
//     rollback target that lies.
//   - **Composed from two scopes.** A zone document for what its members share,
//     a per-proxy document for what one member overrides. The composition is
//     materialised per proxy with its own monotonic version, so a proxy reports
//     ONE number and the delivery path is one row read.
//   - **Rollback is first-class.** The displaced version is recorded when a new
//     one is published, so going back is a call rather than an operator retyping
//     yesterday's document under pressure.
//   - **Drift is visible.** A proxy reports what it is running; the difference
//     between that and what it was told is a value in the fleet view and in the
//     API, because silent drift across a fleet is indistinguishable from a broken
//     rollout.
//
// Delivery is proxy D18's three parts, and this file owes the server half of
// each: a `config_changed` per affected proxy on the event stream (0009) naming
// its composed document and never carrying it; the document desired NOW at
// `GET /v1/proxies/{proxy_id}/config`; and the report at
// `POST .../config/report`, the only call that says what a proxy runs, from
// which drift is derived. Only D18's fleet-owned keys are published
// (configwire.go), and the wire strings are rendered there too.
//
// DELIVERY WITHOUT RECONNECT. The proxy has no periodic fetch: it fetches on a
// notification, a stream (re)connect, a `resync`, or a retry timer armed only
// after a failed fetch. So "it catches up on its next reconnect" is not a
// delivery guarantee — a notification lost while the stream stays healthy
// leaves that proxy on the old document for as long as the stream stays up.
// This side closes it by RETRYING FROM A DURABLE MARKER: each proxy's state row
// keeps the hash this server last announced (`announced_hash`); a publish
// commits the desired document, announces, and moves the marker only for the
// announcements the stream took; and [Registry.Announce] re-emits every proxy
// whose marker is behind its desired hash — on a timer, and once at start, so a
// crash between the commit and the emit is announced by the next process. A
// report naming the desired hash confirms it as well.

// ConfigDocument is a proxy configuration document.
//
// It is a flat map rather than a struct because the keys are the PROXY's
// vocabulary, not this server's: a Hoplock Control that had to be rebuilt to
// distribute a new proxy setting would make every proxy release a control-plane
// release. What this server owns is the versioning, the composition and the
// rollout; what a key means is the proxy's.
type ConfigDocument map[string]any

// Clone returns a shallow copy, which is what composition needs.
func (d ConfigDocument) Clone() ConfigDocument {
	if d == nil {
		return nil
	}
	return maps.Clone(d)
}

// ComposeConfig merges a zone document with a proxy's overrides.
//
// The merge is TOP-LEVEL KEY REPLACEMENT, deliberately, and not a deep merge. A
// deep merge cannot express "unset what the zone said": a proxy overriding a
// nested object would get the union of both, so the only way to remove an
// inherited setting would be to edit the zone document and affect everybody.
// Replacing a whole key is coarser and says exactly one thing.
func ComposeConfig(zone, proxy ConfigDocument) ConfigDocument {
	out := make(ConfigDocument, len(zone)+len(proxy))
	maps.Copy(out, zone)
	maps.Copy(out, proxy)
	return out
}

// marshalConfig renders a document deterministically, so that the same
// configuration always hashes to the same value.
//
// encoding/json sorts map keys, which is what makes this canonical enough for the
// purpose: the hash exists to answer "is this proxy running what I published",
// and two servers composing the same document must agree on the answer.
func marshalConfig(d ConfigDocument) (json.RawMessage, string, error) {
	if d == nil {
		d = ConfigDocument{}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, "", fmt.Errorf("fleet: marshal config document: %w", err)
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

// ZoneScope and ProxyScope name the two configuration scopes.
func ZoneScope(zone Zone) store.ConfigScope {
	return store.ConfigScope{Kind: store.ConfigScopeZone, ID: string(zone)}
}

// ProxyScope names one proxy's override document.
func ProxyScope(proxyID string) store.ConfigScope {
	return store.ConfigScope{Kind: store.ConfigScopeProxy, ID: proxyID}
}

// ConfigPublication is what a publish or a rollback did.
type ConfigPublication struct {
	// Version is the scope version now desired.
	Version int64 `json:"version"`
	// Changed are the proxies whose composed document moved, each named by
	// the version it now desires on the wire. A no-op publish that bumped no
	// proxy's version changes none and announces none.
	Changed []ConfigAnnouncement `json:"changed"`
}

// ConfigAnnouncement is one proxy's `config_changed`, and whether the stream
// took it. Announced false is not a lost notification — the announcer retries
// it — but the operator is told, because it describes the attempt and not
// delivery.
type ConfigAnnouncement struct {
	ProxyID   string `json:"proxy_id"`
	Version   string `json:"version"`
	Hash      string `json:"hash"`
	Announced bool   `json:"announced"`
	Error     string `json:"error,omitempty"`
}

// Unannounced reports how many announcements the stream did not take.
func (p ConfigPublication) Unannounced() int {
	n := 0
	for _, c := range p.Changed {
		if !c.Announced {
			n++
		}
	}
	return n
}

// ConfigRecord is what a publish or a rollback did, as its record states it
// inside its transaction — before anything is announced, which follows the
// commit.
type ConfigRecord struct {
	// Version is the scope version now desired.
	Version int64
	// Before is the version it displaced, 0 when the scope had none.
	Before int64
	// Changed are the proxies whose composed document moved.
	Changed []string
}

// ConfigRecorder writes the record of a publish or a rollback inside the
// transaction that makes it, so the two commit together or not at all.
type ConfigRecorder func(ctx context.Context, tx *store.Store, rec ConfigRecord) error

// recordConfig hands a recorder what the transaction did, reading the version
// displaced from the desired row the transaction just wrote: SetDesired moves
// previous_version in the same statement that moves version, so the two
// cannot disagree.
func recordConfig(ctx context.Context, tx *store.Store, tenant store.Tenant, scope store.ConfigScope, changed []store.ProxyConfigState, record ConfigRecorder) error {
	if record == nil {
		return nil
	}
	desired, err := tx.ProxyConfigs().GetDesired(ctx, tenant, scope)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(changed))
	for _, c := range changed {
		ids = append(ids, c.ProxyID)
	}
	return record(ctx, tx, ConfigRecord{Version: desired.Version, Before: desired.PreviousVersion, Changed: ids})
}

// PublishConfig stores a new version of a scope's document, publishes it,
// re-materialises every proxy it affects, and announces each one whose composed
// document moved.
//
// A document naming any key that is not fleet-owned, or a value not of its
// key's shape, is refused before anything is stored (a [*ConfigKeyError]): the
// proxy rejects such a document whole, so staging it would produce a rollout
// that can only ever report `rejected`. A publish whose composed document is
// byte-identical to what a proxy already has does NOT bump that proxy's
// version and announces nothing to it: a no-op publish must not manufacture
// drift.
//
// An announcement the stream did not take does NOT roll the publish back —
// the desired version is durable — and it is retried (see the file comment).
// A non-nil record runs inside the publish's transaction.
func (r *Registry) PublishConfig(ctx context.Context, tenant store.Tenant, scope store.ConfigScope, doc ConfigDocument, publishedBy string, record ConfigRecorder) (ConfigPublication, error) {
	if err := ValidateConfigDocument(doc); err != nil {
		return ConfigPublication{}, err
	}
	raw, hash, err := marshalConfig(doc)
	if err != nil {
		return ConfigPublication{}, err
	}
	now := r.now()

	var (
		version int64
		changed []store.ProxyConfigState
	)
	err = r.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		next, err := tx.ProxyConfigs().NextVersion(ctx, tenant, scope)
		if err != nil {
			return err
		}
		err = tx.ProxyConfigs().InsertVersion(ctx, tenant, store.ProxyConfigVersion{
			Scope:     scope,
			Version:   next,
			Document:  raw,
			Hash:      hash,
			CreatedBy: publishedBy,
		})
		if err != nil {
			return err
		}
		if err := tx.ProxyConfigs().SetDesired(ctx, tenant, scope, next, publishedBy, now); err != nil {
			return err
		}
		version = next
		if changed, err = r.rematerialise(ctx, tx, tenant, scope, now); err != nil {
			return err
		}
		return recordConfig(ctx, tx, tenant, scope, changed, record)
	})
	if err != nil {
		return ConfigPublication{}, err
	}
	r.TrackConfigTenant(tenant)
	return ConfigPublication{Version: version, Changed: r.announce(ctx, tenant, changed)}, nil
}

// RollbackConfig republishes the version a scope was on before its current one.
//
// It is a first-class operation rather than "publish the old document again",
// because the old document is exactly what an operator does not have to hand
// during the incident that made them want it back. It returns the version now
// desired.
//
// Rolling back moves the desired pointer only; the version it rolls off stays
// stored, so a rollback is itself reversible. A non-nil record runs inside the
// rollback's transaction.
func (r *Registry) RollbackConfig(ctx context.Context, tenant store.Tenant, scope store.ConfigScope, publishedBy string, record ConfigRecorder) (ConfigPublication, error) {
	now := r.now()

	var (
		version int64
		changed []store.ProxyConfigState
	)
	err := r.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		desired, err := tx.ProxyConfigs().GetDesired(ctx, tenant, scope)
		if err != nil {
			if store.IsNotFound(err) {
				return ErrNoRollbackTarget
			}
			return err
		}
		if desired.PreviousVersion == 0 {
			return ErrNoRollbackTarget
		}
		if err := tx.ProxyConfigs().SetDesired(ctx, tenant, scope, desired.PreviousVersion, publishedBy, now); err != nil {
			return err
		}
		version = desired.PreviousVersion
		if changed, err = r.rematerialise(ctx, tx, tenant, scope, now); err != nil {
			return err
		}
		return recordConfig(ctx, tx, tenant, scope, changed, record)
	})
	if err != nil {
		return ConfigPublication{}, err
	}
	r.TrackConfigTenant(tenant)
	return ConfigPublication{Version: version, Changed: r.announce(ctx, tenant, changed)}, nil
}

// rematerialise recomposes the effective document for every proxy a scope
// affects.
//
// Write amplification on a zone publish is deliberate: publishing is rare and
// reading "what should this proxy be running" is not, so the cost belongs on the
// publish. The alternative — composing on every read — puts a merge and two
// lookups on the delivery path and gives a proxy no single version to report.
func (r *Registry) rematerialise(ctx context.Context, tx *store.Store, tenant store.Tenant, scope store.ConfigScope, now time.Time) ([]store.ProxyConfigState, error) {
	var changed []store.ProxyConfigState
	apply := func(p store.Proxy) error {
		before, err := tx.ProxyConfigs().GetState(ctx, tenant, p.ID)
		if err != nil && !store.IsNotFound(err) {
			return err
		}
		after, err := materialiseConfig(ctx, tx, tenant, p.ID, p.Zone, now)
		if err != nil {
			return err
		}
		if after.DesiredVersion != before.DesiredVersion {
			changed = append(changed, after)
		}
		return nil
	}
	switch scope.Kind {
	case store.ConfigScopeProxy:
		p, err := tx.Proxies().Get(ctx, tenant, scope.ID)
		if err != nil {
			if store.IsNotFound(err) {
				// A document staged for a proxy that has not enrolled yet is
				// legitimate: an operator may prepare a rollout before the kit
				// arrives. Enrollment materialises it.
				return nil, nil
			}
			return nil, err
		}
		return changed, apply(p)
	case store.ConfigScopeZone:
		proxies, err := tx.Proxies().ListByZone(ctx, tenant, scope.ID)
		if err != nil {
			return nil, err
		}
		for _, p := range proxies {
			if err := apply(p); err != nil {
				return nil, err
			}
		}
		return changed, nil
	default:
		return nil, fmt.Errorf("fleet: unknown config scope kind %q", scope.Kind)
	}
}

// materialiseConfig composes and stores what one proxy should be running.
//
// It is a package function rather than a method so that Enroll can call it inside
// its own transaction: a proxy must be handed its configuration by the call that
// admitted it, not by a second round trip it might not make.
func materialiseConfig(ctx context.Context, tx *store.Store, tenant store.Tenant, proxyID, zone string, now time.Time) (store.ProxyConfigState, error) {
	zoneDoc, err := desiredDocument(ctx, tx, tenant, ZoneScope(Zone(zone)))
	if err != nil {
		return store.ProxyConfigState{}, err
	}
	proxyDoc, err := desiredDocument(ctx, tx, tenant, ProxyScope(proxyID))
	if err != nil {
		return store.ProxyConfigState{}, err
	}

	current, err := tx.ProxyConfigs().GetState(ctx, tenant, proxyID)
	if err != nil && !store.IsNotFound(err) {
		return store.ProxyConfigState{}, err
	}
	if zoneDoc == nil && proxyDoc == nil && current.DesiredVersion == 0 {
		// NOTHING IS PUBLISHED for this proxy, and nothing ever was: the
		// fetch answers 204 and the proxy runs on its bootstrap file. That
		// is version 0 — no document to announce, none to be behind — and
		// never the empty composition stored as a version 1 the fetch would
		// deny having. The row exists so that the report has one to write.
		empty := store.ProxyConfigState{ProxyID: proxyID}
		if err := tx.ProxyConfigs().PutState(ctx, tenant, empty); err != nil {
			return store.ProxyConfigState{}, err
		}
		return empty, nil
	}

	raw, hash, err := marshalConfig(ComposeConfig(zoneDoc, proxyDoc))
	if err != nil {
		return store.ProxyConfigState{}, err
	}
	if current.DesiredHash == hash && current.DesiredVersion > 0 {
		// Nothing changed for this proxy. Leaving the version alone is what
		// keeps drift meaningful: a zone publish that did not touch this
		// proxy's effective document must not make it look behind.
		return current, nil
	}

	next := store.ProxyConfigState{
		ProxyID:         proxyID,
		DesiredVersion:  current.DesiredVersion + 1,
		DesiredHash:     hash,
		DesiredDocument: raw,
		DesiredAt:       now,
		RunningVersion:  current.RunningVersion,
		RunningHash:     current.RunningHash,
		ReportedAt:      current.ReportedAt,
	}
	if err := tx.ProxyConfigs().PutState(ctx, tenant, next); err != nil {
		return store.ProxyConfigState{}, err
	}
	return next, nil
}

// desiredDocument reads a scope's published document, or nil when the scope has
// none. A scope nobody has published contributes nothing rather than an error:
// most proxies never carry an override, and most zones are configured before any
// proxy needs one.
func desiredDocument(ctx context.Context, tx *store.Store, tenant store.Tenant, scope store.ConfigScope) (ConfigDocument, error) {
	desired, err := tx.ProxyConfigs().GetDesired(ctx, tenant, scope)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	version, err := tx.ProxyConfigs().GetVersion(ctx, tenant, scope, desired.Version)
	if err != nil {
		if store.IsNotFound(err) {
			// A desired pointer at a version that is not stored is a broken
			// invariant, not an empty document: serving nothing would silently
			// wipe a proxy's configuration.
			return nil, fmt.Errorf("fleet: %s scope %q desires version %d, which is not stored",
				scope.Kind, scope.ID, desired.Version)
		}
		return nil, err
	}

	var doc ConfigDocument
	if err := json.Unmarshal(version.Document, &doc); err != nil {
		return nil, fmt.Errorf("fleet: %s scope %q version %d is not a document: %w",
			scope.Kind, scope.ID, desired.Version, err)
	}
	return doc, nil
}

// announce emits one `config_changed` per proxy whose composed document moved,
// and moves each one's announced marker when the stream took it. One that
// failed is reported on the answer and left for [Registry.Announce] to retry.
func (r *Registry) announce(ctx context.Context, tenant store.Tenant, changed []store.ProxyConfigState) []ConfigAnnouncement {
	out := make([]ConfigAnnouncement, 0, len(changed))
	for _, s := range changed {
		out = append(out, r.announceOne(ctx, tenant, s))
	}
	return out
}

func (r *Registry) announceOne(ctx context.Context, tenant store.Tenant, s store.ProxyConfigState) ConfigAnnouncement {
	ev := contract.ConfigChangedEvent{
		Version: WireConfigVersion(s.DesiredVersion),
		Hash:    WireConfigHash(s.DesiredHash),
	}
	a := ConfigAnnouncement{ProxyID: s.ProxyID, Version: ev.Version, Hash: ev.Hash}
	if err := r.pub.PublishConfigChange(ctx, tenant, s.ProxyID, ev); err != nil {
		a.Error = err.Error()
		r.log.WarnContext(ctx, "a config_changed was not taken by the event stream; it will be retried",
			"event", "config_announce_failed", "tenant", tenant.String(), "proxy_id", s.ProxyID,
			"version", ev.Version, "error", err.Error())
		return a
	}
	if err := r.st.ProxyConfigs().MarkAnnounced(ctx, tenant, s.ProxyID, s.DesiredHash, r.now()); err != nil {
		// The proxy was told; the marker is behind. The worst this costs is
		// one more announcement, which the proxy drops without a fetch.
		r.log.WarnContext(ctx, "an announcement was sent and could not be recorded",
			"event", "config_announce_unrecorded", "tenant", tenant.String(), "proxy_id", s.ProxyID,
			"error", err.Error())
	}
	a.Announced = true
	return a
}

// Announce re-emits `config_changed` for every proxy in a tenant whose
// announced marker is behind its desired document: the retry half of delivery
// without reconnect. It is run on a timer and once at start; it returns what it
// tried, and an error only when it could not read the states.
//
// What it retries is an announcement the stream never took — a publish whose
// announcement failed, or a crash between the desired document's commit and its
// announcement, which are two transactions. Once the stream takes one it is
// logged, and the event stream delivers it on whichever node holds the proxy's
// stream (0027), so every node's announcer may announce any owed proxy: two
// nodes announcing one together cost the proxy a duplicate it drops without a
// fetch.
func (r *Registry) Announce(ctx context.Context, tenant store.Tenant) ([]ConfigAnnouncement, error) {
	states, err := r.st.ProxyConfigs().ListStates(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var owed []store.ProxyConfigState
	for _, s := range states {
		if s.DesiredVersion == 0 || s.DesiredHash == "" || s.AnnouncedHash == s.DesiredHash {
			continue
		}
		owed = append(owed, s)
	}
	return r.announce(ctx, tenant, owed), nil
}

// TrackConfigTenant adds a tenant to the set the announcer walks. There is no
// store read that lists tenants (M18), so the set is learned: the configured
// tenant, each tenant a proxy subscribes in, and each one a publish is made in.
func (r *Registry) TrackConfigTenant(tenant store.Tenant) {
	if tenant != "" {
		r.configTenants.Store(tenant, struct{}{})
	}
}

// WatchAnnouncements runs [Registry.Announce] over every tracked tenant once
// now and then every interval, until ctx ends.
func (r *Registry) WatchAnnouncements(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultAnnounceInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		r.configTenants.Range(func(k, _ any) bool {
			if _, err := r.Announce(ctx, k.(store.Tenant)); err != nil && ctx.Err() == nil {
				r.log.WarnContext(ctx, "pending configuration announcements could not be read",
					"event", "config_announce_read_failed", "tenant", k.(store.Tenant).String(), "error", err.Error())
			}
			return ctx.Err() == nil
		})
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// DefaultAnnounceInterval is how often owed announcements are retried. It is
// short, because a notification lost while a stream stays healthy is a proxy on
// the old document until it is retried, and an announcement the proxy already
// holds costs it nothing.
const DefaultAnnounceInterval = 15 * time.Second

// DesiredConfig returns what a proxy should be running.
//
// Absent is [ErrNotEnrolled] rather than an empty document, because an empty
// document is a real configuration — "inherit nothing, override nothing" — and a
// proxy that received it in place of an error would apply it.
func (r *Registry) DesiredConfig(ctx context.Context, tenant store.Tenant, proxyID string) (store.ProxyConfigState, error) {
	state, err := r.st.ProxyConfigs().GetState(ctx, tenant, proxyID)
	if err != nil {
		if store.IsNotFound(err) {
			return store.ProxyConfigState{}, ErrNotEnrolled
		}
		return store.ProxyConfigState{}, err
	}
	return state, nil
}

// FetchConfig answers `GET /v1/proxies/{proxy_id}/config`: the document desired
// for a proxy NOW, or ok false when nothing is published for it — its state
// desires version 0, because neither its zone nor the proxy itself has ever had
// a document — in which case the proxy runs on its bootstrap file alone. It
// reads the same row the announcer and the drift view read, so the three never
// disagree about whether there is a document. A proxy the registry does not
// hold in this tenant is [ErrNotEnrolled]: a registry fact, never a deny (M11).
func (r *Registry) FetchConfig(ctx context.Context, tenant store.Tenant, proxyID string) (*contract.ProxyConfigDocument, bool, error) {
	if _, err := r.st.Proxies().Get(ctx, tenant, proxyID); err != nil {
		if store.IsNotFound(err) {
			return nil, false, ErrNotEnrolled
		}
		return nil, false, err
	}
	state, err := r.st.ProxyConfigs().GetState(ctx, tenant, proxyID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if state.DesiredVersion == 0 {
		return nil, false, nil
	}
	var settings map[string]any
	if err := json.Unmarshal(state.DesiredDocument, &settings); err != nil {
		return nil, false, fmt.Errorf("fleet: proxy %s's composed document does not decode: %w", proxyID, err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return &contract.ProxyConfigDocument{
		Version:  WireConfigVersion(state.DesiredVersion),
		Hash:     WireConfigHash(state.DesiredHash),
		Settings: settings,
	}, true, nil
}

// ConfigReportOutcome is what recording a report did.
type ConfigReportOutcome struct {
	// Changed is false for a report identical in substance to the stored one
	// — the usual case, since the proxy reports after every sync — and for
	// one older than the stored one. Neither is a fleet event.
	Changed bool
	// Before and After are the substantive state either side, for the
	// caller that records the transition.
	Before, After store.ConfigReport
}

// RecordConfigReport records `POST .../config/report` and drives drift from it.
//
// REPORTS REPEAT, AND STORING THEM IS IDEMPOTENT. The proxy reports after every
// sync, every reconnect, every failed-fetch retry and every 304, so most reports
// are the previous one again. "Unchanged" is decided from the substantive fields
// — running_*, desired_*, state, the SET in restart_required, last_error — and
// never from reported_at, which the proxy resets each time it re-evaluates a
// document. An unchanged report moves only the last-heard time; it writes no
// history and is no fleet event, or every reconnect storm would be a flood of
// apparent configuration activity. A report whose reported_at is older than the
// stored one overwrites nothing, because two in flight can arrive out of order.
//
// It returns once the report is durable, and never before.
func (r *Registry) RecordConfigReport(ctx context.Context, tenant store.Tenant, proxyID string, rep contract.ProxyConfigReport, reportedAt time.Time) (ConfigReportOutcome, error) {
	if _, err := r.st.Proxies().Get(ctx, tenant, proxyID); err != nil {
		if store.IsNotFound(err) {
			return ConfigReportOutcome{}, ErrNotEnrolled
		}
		return ConfigReportOutcome{}, err
	}
	now := r.now()
	incoming := store.ConfigReport{
		RunningVersion: rep.RunningVersion, RunningHash: rep.RunningHash,
		DesiredVersion: rep.DesiredVersion, DesiredHash: rep.DesiredHash,
		State: string(rep.State), RestartRequired: normaliseSet(rep.RestartRequired),
		LastError: rep.LastError, ReportedAt: reportedAt, LastHeardAt: now,
	}
	var out ConfigReportOutcome
	err := r.st.InTx(ctx, func(ctx context.Context, tx *store.Store) error {
		state, err := tx.ProxyConfigs().LockState(ctx, tenant, proxyID)
		if err != nil {
			return err
		}
		out.Before = state.Report
		if !state.Report.ReportedAt.IsZero() && reportedAt.Before(state.Report.ReportedAt) {
			return tx.ProxyConfigs().TouchReport(ctx, tenant, proxyID, now)
		}
		if sameReport(state.Report, incoming) {
			return tx.ProxyConfigs().TouchReport(ctx, tenant, proxyID, now)
		}
		// The running document as this server reads it. Absent is the
		// bootstrap file alone — never version zero reported as a number —
		// and a version or hash that is not one of this server's is drift,
		// recorded as it arrived.
		var runningVersion int64
		var runningHash string
		if v, ok := ParseWireConfigVersion(rep.RunningVersion); ok {
			if h, ok := ParseWireConfigHash(rep.RunningHash); ok {
				runningVersion, runningHash = v, h
			}
		}
		if err := tx.ProxyConfigs().PutReport(ctx, tenant, proxyID, incoming, runningVersion, runningHash); err != nil {
			return err
		}
		// A report naming the desired document confirms its announcement.
		if h, ok := ParseWireConfigHash(rep.DesiredHash); ok && h == state.DesiredHash {
			if err := tx.ProxyConfigs().MarkAnnounced(ctx, tenant, proxyID, h, now); err != nil {
				return err
			}
		}
		out.Changed, out.After = true, incoming
		return nil
	})
	if err != nil {
		return ConfigReportOutcome{}, err
	}
	return out, nil
}

// sameReport compares the substantive fields, never reported_at.
func sameReport(a, b store.ConfigReport) bool {
	return a.RunningVersion == b.RunningVersion && a.RunningHash == b.RunningHash &&
		a.DesiredVersion == b.DesiredVersion && a.DesiredHash == b.DesiredHash &&
		a.State == b.State && a.LastError == b.LastError &&
		slices.Equal(normaliseSet(a.RestartRequired), normaliseSet(b.RestartRequired))
}

func normaliseSet(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// ConfigRollout is one proxy's place in a rollout, derived from its report.
type ConfigRollout struct {
	ProxyID string `json:"proxy_id"`
	// DesiredVersion and DesiredHash are what it should run, on the wire.
	DesiredVersion string `json:"desired_version,omitempty"`
	DesiredHash    string `json:"desired_hash,omitempty"`
	// RunningVersion and RunningHash are what it reported running, as it
	// reported them; both empty means its bootstrap file alone.
	RunningVersion string `json:"running_version,omitempty"`
	RunningHash    string `json:"running_hash,omitempty"`
	// State is the report's state, empty when it has never reported.
	State           string    `json:"state,omitempty"`
	RestartRequired []string  `json:"restart_required,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	ReportedAt      time.Time `json:"reported_at,omitzero"`
	LastHeardAt     time.Time `json:"last_heard_at,omitzero"`
	// CaughtUp is true only for `applied` on the desired document. A
	// `pending_restart` proxy holds the new document and is NOT caught up.
	CaughtUp bool `json:"caught_up"`
	// Announced is false while a `config_changed` for the desired document
	// is still owed.
	Announced bool `json:"announced"`
}

// ConfigRollouts is the rollout's status page, one row per proxy, in id order.
func (r *Registry) ConfigRollouts(ctx context.Context, tenant store.Tenant) ([]ConfigRollout, error) {
	states, err := r.st.ProxyConfigs().ListStates(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]ConfigRollout, 0, len(states))
	for _, s := range states {
		out = append(out, rolloutOf(s))
	}
	return out, nil
}

func rolloutOf(s store.ProxyConfigState) ConfigRollout {
	c := ConfigRollout{
		ProxyID:        s.ProxyID,
		RunningVersion: s.Report.RunningVersion, RunningHash: s.Report.RunningHash,
		State: s.Report.State, RestartRequired: s.Report.RestartRequired,
		LastError: s.Report.LastError, ReportedAt: s.Report.ReportedAt, LastHeardAt: s.Report.LastHeardAt,
		CaughtUp:  s.DesiredVersion > 0 && !s.Drifted(),
		Announced: s.DesiredHash != "" && s.AnnouncedHash == s.DesiredHash,
	}
	if s.DesiredVersion > 0 {
		c.DesiredVersion, c.DesiredHash = WireConfigVersion(s.DesiredVersion), WireConfigHash(s.DesiredHash)
	}
	return c
}

// ConfigDrift returns every proxy that has not caught up, in id order.
//
// A fleet where this is empty has finished rolling out; a fleet where it is not
// is either mid-rollout or broken, and which one the row's state says:
// `pending_restart` is waiting for a restart, `rejected` needs an operator and
// carries the error, `fetch_failed` is retrying.
func (r *Registry) ConfigDrift(ctx context.Context, tenant store.Tenant) ([]ConfigRollout, error) {
	all, err := r.ConfigRollouts(ctx, tenant)
	if err != nil {
		return nil, err
	}
	drifted := make([]ConfigRollout, 0)
	for _, c := range all {
		if c.DesiredVersion != "" && !c.CaughtUp {
			drifted = append(drifted, c)
		}
	}
	return drifted, nil
}
