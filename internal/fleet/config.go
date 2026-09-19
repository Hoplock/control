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
	"time"

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
// Delivery reuses the event stream (0009) rather than inventing a second channel
// — see [ConfigPublisher], which also records why that seam cannot reach the wire
// until an upstream contract change lands.

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

// PublishConfig stores a new version of a scope's document, publishes it, and
// re-materialises every proxy it affects.
//
// It returns the version published. A publish whose composed document is
// byte-identical to what a proxy already has does NOT bump that proxy's version:
// a no-op publish must not manufacture drift, because drift that means nothing is
// drift an operator stops reading.
func (r *Registry) PublishConfig(ctx context.Context, tenant store.Tenant, scope store.ConfigScope, doc ConfigDocument, publishedBy string) (int64, error) {
	raw, hash, err := marshalConfig(doc)
	if err != nil {
		return 0, err
	}
	now := r.now()

	var version int64
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
		return r.rematerialise(ctx, tx, tenant, scope, now)
	})
	if err != nil {
		return 0, err
	}
	r.notify(ctx, tenant)
	return version, nil
}

// RollbackConfig republishes the version a scope was on before its current one.
//
// It is a first-class operation rather than "publish the old document again",
// because the old document is exactly what an operator does not have to hand
// during the incident that made them want it back. It returns the version now
// desired.
//
// Rolling back moves the desired pointer only; the version it rolls off stays
// stored, so a rollback is itself reversible.
func (r *Registry) RollbackConfig(ctx context.Context, tenant store.Tenant, scope store.ConfigScope, publishedBy string) (int64, error) {
	now := r.now()

	var version int64
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
		return r.rematerialise(ctx, tx, tenant, scope, now)
	})
	if err != nil {
		return 0, err
	}
	r.notify(ctx, tenant)
	return version, nil
}

// rematerialise recomposes the effective document for every proxy a scope
// affects.
//
// Write amplification on a zone publish is deliberate: publishing is rare and
// reading "what should this proxy be running" is not, so the cost belongs on the
// publish. The alternative — composing on every read — puts a merge and two
// lookups on the delivery path and gives a proxy no single version to report.
func (r *Registry) rematerialise(ctx context.Context, tx *store.Store, tenant store.Tenant, scope store.ConfigScope, now time.Time) error {
	switch scope.Kind {
	case store.ConfigScopeProxy:
		p, err := tx.Proxies().Get(ctx, tenant, scope.ID)
		if err != nil {
			if store.IsNotFound(err) {
				// A document staged for a proxy that has not enrolled yet is
				// legitimate: an operator may prepare a rollout before the kit
				// arrives. Enrollment materialises it.
				return nil
			}
			return err
		}
		_, err = materialiseConfig(ctx, tx, tenant, p.ID, p.Zone, now)
		return err
	case store.ConfigScopeZone:
		proxies, err := tx.Proxies().ListByZone(ctx, tenant, scope.ID)
		if err != nil {
			return err
		}
		for _, p := range proxies {
			if _, err := materialiseConfig(ctx, tx, tenant, p.ID, p.Zone, now); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("fleet: unknown config scope kind %q", scope.Kind)
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

	raw, hash, err := marshalConfig(ComposeConfig(zoneDoc, proxyDoc))
	if err != nil {
		return store.ProxyConfigState{}, err
	}

	current, err := tx.ProxyConfigs().GetState(ctx, tenant, proxyID)
	if err != nil && !store.IsNotFound(err) {
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

// notify hands the change to the event stream, best-effort.
//
// A delivery failure does not fail the publish: the desired version is already
// durable and the drift is already visible, so the proxy finds out late rather
// than never — and an operator who could not stage a rollout because the stream
// was down would be worse off than one whose rollout arrives on the next
// heartbeat.
func (r *Registry) notify(ctx context.Context, tenant store.Tenant) {
	states, err := r.st.ProxyConfigs().ListStates(ctx, tenant)
	if err != nil {
		return
	}
	for _, s := range states {
		if !s.Drifted() {
			continue
		}
		_ = r.pub.PublishConfigChange(ctx, tenant, s.ProxyID, s.DesiredVersion, s.DesiredHash)
	}
}

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

// ReportRunningConfig records what a proxy says it is running.
func (r *Registry) ReportRunningConfig(ctx context.Context, tenant store.Tenant, proxyID string, version int64, hash string) error {
	err := r.st.ProxyConfigs().ReportRunning(ctx, tenant, proxyID, version, hash, r.now())
	if err != nil && store.IsNotFound(err) {
		return ErrNotEnrolled
	}
	return err
}

// ConfigDrift returns every proxy running something other than what it was told
// to run, in id order.
//
// It is the rollout's status page. A fleet where this is empty has finished
// rolling out; a fleet where it is not is either mid-rollout or broken, and which
// one is a question about how long the entries have been there.
func (r *Registry) ConfigDrift(ctx context.Context, tenant store.Tenant) ([]store.ProxyConfigState, error) {
	states, err := r.st.ProxyConfigs().ListStates(ctx, tenant)
	if err != nil {
		return nil, err
	}
	// ListStates is already in proxy-id order, so the filter preserves it.
	drifted := make([]store.ProxyConfigState, 0)
	for _, s := range states {
		if s.Drifted() {
			drifted = append(drifted, s)
		}
	}
	return drifted, nil
}
