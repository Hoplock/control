// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package decision

import (
	"context"
	"fmt"
	"sort"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

// The capability constraint on the ISSUE path (M17).
//
// A rung, a platform or a device field the enforcing proxy cannot honour is a
// decision that cannot be served, and the two sides of it fail differently —
// which is why the two checks are kept apart here rather than folded together:
//
//   - an UNREADABLE FIELD is an outage. The proxy decodes the response strictly
//     and fails the session closed on a field it does not understand.
//   - an UNHONOURABLE RUNG is a SHORTER LADDER. The proxy skips a rung whose
//     fields its driver does not declare and walks on (proxy D14) — with no
//     error anywhere, so a one-rung ladder quietly becomes a denial the
//     operator never authored.
//
// Both are caught before the response is written, and they are answered from
// different data. The rung check reads what the proxy declared ON THIS CALL
// (`AuthorizeRequest.capabilities`), which is the one declaration that cannot be
// stale. The platform and device-field checks read the fleet registry's stored
// declaration, which is a readiness signal rather than authority — so it
// constrains only what it actually declares, and a proxy that has declared
// nothing constrains nothing.
//
// The TARGET's own reported capabilities are the second source, and their
// fail-safe rule belongs to 0006: stale, undated and absent are ONE case
// ([fleet.ResolveTargetRungs]), providing nothing that has to be applied while
// leaving the two proxy-side defaults and an attested rung available. That is
// how an appliance nobody can probe still carries a real enforcement claim.

// CapabilityError is a rung, platform or device field the enforcing proxy or
// the target cannot take.
//
// It is an OUTAGE: nobody authored a refusal of this user, and what has
// happened is that a policy names something this estate cannot serve. Serving
// it anyway would run a session below the rung its own audit record claims,
// which is the failure the whole enforcement vocabulary exists to prevent.
type CapabilityError struct {
	// Axis is `execution`, `reach`, `platform` or `device_field`.
	Axis string
	// Value is the rung, platform or field name that cannot be served.
	Value string
	// Source names who cannot take it: `proxy` or `target`.
	Source string
	// ProxyID is the enforcing proxy.
	ProxyID string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("decision: %s %q is not available: the %s cannot take it (proxy %q)",
		e.Axis, e.Value, e.Source, e.ProxyID)
}

// checkCapabilities refuses a response naming something the enforcing proxy or
// the target cannot provide.
//
// The ENFORCING proxy is the one that will act on this response — the proxy
// that asked. On a chained route the far end asks for its own leg and is
// answered against its own declaration, which is exactly why each hop calls
// this endpoint for itself.
func (s *Service) checkCapabilities(
	ctx context.Context,
	tenant store.Tenant,
	req *contract.AuthorizeRequest,
	resp *contract.AuthorizeResponse,
	g *fleet.Graph,
) error {
	declared := req.Capabilities
	proxyID := req.Conn.ProxyID

	if resp.Enforcement != nil {
		exec, reach := resp.EnforcedExecution(), resp.EnforcedReach()

		// A rung the proxy build does not declare is never emitted.
		// `capabilities` absent declares NOTHING, so only a rung needing no
		// capability at all may be chosen — and since the two defaults are
		// never emitted at all, that means nothing here.
		if exec != contract.ExecutionProxyInspected && !declaresExecution(declared, exec) {
			return &CapabilityError{Axis: "execution", Value: string(exec), Source: "proxy", ProxyID: proxyID}
		}
		if reach != contract.ReachProxyChannelPolicy && !declaresReach(declared, reach) {
			return &CapabilityError{Axis: "reach", Value: string(reach), Source: "proxy", ProxyID: proxyID}
		}

		// The target's half. It is read only when a rung beyond the
		// defaults was named, because a route standing on the defaults asks
		// nothing of the target and a read on the decision path that
		// changes no answer is latency spent for nothing (M5).
		rungs, err := s.fleet.TargetRungs(ctx, tenant, fleet.TargetCapabilityKey{
			Hostname: req.Target,
			Port:     req.TargetPort,
			Platform: ladderPlatform(resp),
		})
		if err != nil {
			// NOT the fail-safe set: a read failure is this server not
			// knowing, and answering the fail-safe set here would make a
			// database outage look like an appliance estate (M11, 0006).
			return err
		}
		if exec != contract.ExecutionProxyInspected && !rungs.AllowsExecution(exec) {
			return &CapabilityError{Axis: "execution", Value: string(exec), Source: "target", ProxyID: proxyID}
		}
		if reach != contract.ReachProxyChannelPolicy && !rungs.AllowsReach(reach) {
			return &CapabilityError{Axis: "reach", Value: string(reach), Source: "target", ProxyID: proxyID}
		}
	}

	return s.checkLadderCapabilities(resp, proxyID, g)
}

// checkLadderCapabilities holds the ladder to what the enforcing proxy's build
// declared: the platform it has a driver for, and the device fields that driver
// accepts.
//
// It reads the STORED declaration, which is operational data rather than
// authority (M17), so an axis the proxy has declared nothing about constrains
// nothing: a fleet that has never reported would otherwise have every
// `ephemeral-account` route refused, which is a bootstrap that cannot start.
// What it catches is the case that actually bites — a proxy that declared its
// drivers and does not have this one.
func (s *Service) checkLadderCapabilities(resp *contract.AuthorizeResponse, proxyID string, g *fleet.Graph) error {
	state, entries := resp.Ladder()
	if state != contract.LadderWalk {
		return nil
	}
	node, ok := g.Node(proxyID)
	if !ok || node.Capabilities.Empty() {
		return nil
	}
	caps := node.Capabilities

	for _, entry := range entries {
		platform := entry.Params[contract.ParamPlatform]
		if platform != "" && len(caps.Platforms) > 0 && !caps.HasPlatform(platform) {
			// Naming a platform the enforcing proxy has no driver for is a
			// decision that cannot be served: the rung would be skipped and
			// a one-rung ladder becomes a denial nobody authored.
			return &CapabilityError{Axis: "platform", Value: platform, Source: "proxy", ProxyID: proxyID}
		}
		if platform == "" || len(caps.DeviceFields) == 0 {
			continue
		}
		accepted, declared := caps.DeviceFields[platform]
		if !declared || len(accepted) == 0 {
			continue
		}
		for _, name := range deviceFieldNames(entry) {
			if !caps.HasDeviceField(platform, name) {
				return &CapabilityError{
					Axis: "device_field", Value: platform + "." + name,
					Source: "proxy", ProxyID: proxyID,
				}
			}
		}
	}
	return nil
}

// ladderPlatform is the device platform the target is reached through, for the
// capability lookup.
//
// A target capability record is keyed by platform because a device is observed
// THROUGH a driver and two drivers can legitimately see one host differently
// (M17). A ladder naming no platform looks the record up with an empty one,
// which is the key an `ephemeral-user` or `brokered-key` route was reported
// under.
func ladderPlatform(resp *contract.AuthorizeResponse) string {
	state, entries := resp.Ladder()
	if state != contract.LadderWalk {
		return ""
	}
	for _, entry := range entries {
		if p := entry.Params[contract.ParamPlatform]; p != "" {
			return p
		}
	}
	return ""
}

func deviceFieldNames(entry contract.TargetAuth) []string {
	var names []string
	for key := range entry.Params {
		if len(key) > len(contract.DeviceFieldPrefix) && key[:len(contract.DeviceFieldPrefix)] == contract.DeviceFieldPrefix {
			names = append(names, key[len(contract.DeviceFieldPrefix):])
		}
	}
	sort.Strings(names)
	return names
}

func declaresExecution(c *contract.ProxyCapabilities, rung contract.ExecutionRung) bool {
	if c == nil {
		return false
	}
	for _, have := range c.Execution {
		if contract.ExecutionRung(have) == rung {
			return true
		}
	}
	return false
}

func declaresReach(c *contract.ProxyCapabilities, rung contract.ReachRung) bool {
	if c == nil {
		return false
	}
	for _, have := range c.Reach {
		if contract.ReachRung(have) == rung {
			return true
		}
	}
	return false
}
