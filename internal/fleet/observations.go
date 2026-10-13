// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store"
)

// The two things Hoplock/proxy#69 and #72 give this registry to remember: what
// each proxy BUILD offers, declared on every authorize request, and what each
// TARGET negotiated, reported beside its rungs.

// AlgorithmDeclaration is the part of a proxy's capability declaration that
// describes its algorithm offer: the floor levels it enforces, what each
// profile offers per axis, and every identifier it can offer. The three change
// together, and only when a proxy's build does.
type AlgorithmDeclaration struct {
	AlgorithmFloors   []contract.AlgorithmFloorCapability   `json:"algorithm_floors,omitempty"`
	AlgorithmProfiles []contract.AlgorithmProfileCapability `json:"algorithm_profiles,omitempty"`
	Algorithms        *contract.OfferableAlgorithms         `json:"algorithms,omitempty"`
}

// Capabilities renders it as the request's shape, for [JudgeAlgorithms].
func (d AlgorithmDeclaration) Capabilities() *contract.ProxyCapabilities {
	return &contract.ProxyCapabilities{
		AlgorithmFloors: d.AlgorithmFloors, AlgorithmProfiles: d.AlgorithmProfiles, Algorithms: d.Algorithms,
	}
}

// Empty reports whether the declaration says nothing.
func (d AlgorithmDeclaration) Empty() bool {
	return len(d.AlgorithmFloors) == 0 && len(d.AlgorithmProfiles) == 0 && d.Algorithms == nil
}

// DeclaredAlgorithms is one proxy's latest recorded declaration.
type DeclaredAlgorithms struct {
	ProxyID     string               `json:"proxy_id"`
	Declaration AlgorithmDeclaration `json:"declaration"`
	DeclaredAt  time.Time            `json:"declared_at"`
}

// RecordAlgorithmDeclaration keeps a proxy's latest algorithm declaration.
//
// IT IS A WRITE THE DECISION PATH CAUSES, AND M5 BOUNDS THE DECISION PATH. So
// it costs one hash per call and one write per BUILD CHANGE: the digest of what
// the request declared is compared with the one this process last wrote for the
// proxy, and only a difference reaches the store — where the upsert is itself a
// no-op when the stored digest matches, so a restart costs one write per proxy
// at most. A failure is the caller's to log and not to answer: the declaration
// on the request is the authority the decision path judges against, and this
// copy is a readiness signal for the publish-time report. The cache is updated
// only after a write lands, so a failed write is retried on the next call.
func (r *Registry) RecordAlgorithmDeclaration(ctx context.Context, tenant store.Tenant, proxyID string, caps *contract.ProxyCapabilities) error {
	if proxyID == "" || caps == nil {
		return nil
	}
	decl := AlgorithmDeclaration{
		AlgorithmFloors: caps.AlgorithmFloors, AlgorithmProfiles: caps.AlgorithmProfiles, Algorithms: caps.Algorithms,
	}
	if decl.Empty() {
		return nil
	}
	body, err := json.Marshal(decl)
	if err != nil {
		return fmt.Errorf("fleet: an algorithm declaration does not encode: %w", err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	key := string(tenant) + "\x00" + proxyID
	if last, ok := r.declared.Load(key); ok && last.(string) == digest {
		return nil
	}
	if _, err := r.st.ProxyAlgorithmDeclarations().Put(ctx, tenant, store.ProxyAlgorithmDeclaration{
		ProxyID: proxyID, Declaration: body, Digest: digest, DeclaredAt: r.now(),
	}); err != nil {
		return err
	}
	r.declared.Store(key, digest)
	return nil
}

// AlgorithmDeclarations returns every proxy's latest recorded declaration.
func (r *Registry) AlgorithmDeclarations(ctx context.Context, tenant store.Tenant) ([]DeclaredAlgorithms, error) {
	rows, err := r.st.ProxyAlgorithmDeclarations().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]DeclaredAlgorithms, 0, len(rows))
	for _, row := range rows {
		var d AlgorithmDeclaration
		if err := json.Unmarshal(row.Declaration, &d); err != nil {
			// A stored declaration that does not decode is this server's
			// own write gone wrong: an outage, never "declared nothing".
			return nil, fmt.Errorf("fleet: proxy %s's algorithm declaration does not decode: %w", row.ProxyID, err)
		}
		out = append(out, DeclaredAlgorithms{ProxyID: row.ProxyID, Declaration: d, DeclaredAt: row.DeclaredAt})
	}
	return out, nil
}

// KexObservation is one target's key exchange, as a proxy saw it. It grants
// nothing: the authorize response is the authority for a floor, and the
// handshake re-checks it on every connection.
type KexObservation struct {
	Hostname   string            `json:"target"`
	Port       int32             `json:"target_port,omitempty"`
	FloorMet   contract.FloorMet `json:"floor_met"`
	Negotiated string            `json:"negotiated,omitempty"`
	Offered    []string          `json:"offered,omitempty"`
	ObservedAt time.Time         `json:"observed_at"`
	ReportedBy string            `json:"reported_by,omitempty"`
}

// Fresh reports whether the observation is within ttl of now. A stale one is
// "not reported", never "met".
func (o KexObservation) Fresh(now time.Time, ttl time.Duration) bool {
	return !o.ObservedAt.IsZero() && now.Sub(o.ObservedAt) <= ttl
}

// ReportTargetKex replaces the key-exchange observation for one host and port,
// and nothing else: the rung observation in the target capability store is
// left exactly as it was (the contract's merge rule).
func (r *Registry) ReportTargetKex(ctx context.Context, tenant store.Tenant, o KexObservation) (time.Duration, error) {
	if o.Hostname == "" {
		return 0, fmt.Errorf("fleet.ReportTargetKex: hostname is required")
	}
	if err := r.st.TargetKexObservations().Put(ctx, tenant, store.TargetKexObservation{
		Hostname: o.Hostname, Port: o.Port, FloorMet: string(o.FloorMet),
		Negotiated: o.Negotiated, Offered: o.Offered, ObservedAt: o.ObservedAt,
		ReportedBy: o.ReportedBy, ReceivedAt: r.now(),
	}); err != nil {
		return 0, err
	}
	return r.liveness.ReportAfter, nil
}

// TargetKex returns every stored key-exchange observation in a tenant.
func (r *Registry) TargetKex(ctx context.Context, tenant store.Tenant) ([]KexObservation, error) {
	rows, err := r.st.TargetKexObservations().List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]KexObservation, 0, len(rows))
	for _, row := range rows {
		out = append(out, KexObservation{
			Hostname: row.Hostname, Port: row.Port, FloorMet: contract.FloorMet(row.FloorMet),
			Negotiated: row.Negotiated, Offered: row.Offered, ObservedAt: row.ObservedAt,
			ReportedBy: row.ReportedBy,
		})
	}
	return out, nil
}
