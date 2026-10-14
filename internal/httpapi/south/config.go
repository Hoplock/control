// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
)

// Fleet configuration (proxy D18, Hoplock/proxy#65): the document a proxy is
// desired to run, and its report of what became of it.
//
// Both routes are authenticated and tenant-resolved exactly as the event stream
// is: the tenant comes from the proxy's credential (M22), and a credential
// bound to one proxy speaks for that proxy only. An id the registry does not
// hold IN THE CALLER'S TENANT is `404 not_enrolled` — a registry fact, never a
// deny, so never a 401 (M11). A 401 would also be indistinguishable to the
// proxy from a rejected token, which is the one answer on which its
// subscription stops retrying.

var _ contract.FleetConfigServer = handlers{}

// serveConfig answers GET /v1/proxies/{proxy_id}/config.
//
// It writes its own status line, as the event stream does, because its
// success has three shapes the endpoint adapter does not: 200 with the
// document and its hash as the ETag, 304 with no body when If-None-Match names
// the hash still desired — which is what makes the fetch the proxy makes on
// every (re)connect and after `resync` cheap — and 204 when nothing is
// published, in which case the proxy runs on its bootstrap file alone. Every
// refusal still goes through the mapper.
func (h handlers) serveConfig(w http.ResponseWriter, r *http.Request) {
	proxyID, err := h.configCaller(r.Context(), r.PathValue("proxy_id"), contract.PathProxyConfig)
	if err != nil {
		h.s.writeError(w, r, err)
		return
	}
	doc, notModified, err := h.FetchConfig(r.Context(), proxyID, r.Header.Get("If-None-Match"))
	switch {
	case err != nil:
		h.s.writeError(w, r, err)
	case doc == nil:
		w.WriteHeader(http.StatusNoContent)
	case notModified:
		w.Header().Set("ETag", fleet.ETagOfWireHash(doc.Hash))
		w.WriteHeader(http.StatusNotModified)
	default:
		w.Header().Set("ETag", fleet.ETagOfWireHash(doc.Hash))
		writeJSON(w, http.StatusOK, doc)
	}
}

// FetchConfig implements contract.FleetConfigServer.
func (h handlers) FetchConfig(ctx context.Context, proxyID, ifNoneMatch string) (*contract.ProxyConfigDocument, bool, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, false, errNoCaller
	}
	doc, ok, err := h.s.fleet.FetchConfig(ctx, caller.Tenant, proxyID)
	switch {
	case errors.Is(err, fleet.ErrNotEnrolled):
		return nil, false, notEnrolled(proxyID)
	case err != nil:
		return nil, false, err
	case !ok:
		return nil, false, nil
	}
	if ifNoneMatch != "" {
		if digest, ok := fleet.ParseWireConfigHash(doc.Hash); ok && fleet.IfNoneMatchHolds(ifNoneMatch, digest) {
			return doc, true, nil
		}
	}
	return doc, false, nil
}

func (h handlers) reportConfig(ctx context.Context, r *http.Request) (any, error) {
	proxyID, err := h.configCaller(ctx, r.PathValue("proxy_id"), contract.PathProxyConfigReport)
	if err != nil {
		return nil, err
	}
	var rep contract.ProxyConfigReport
	if err := decode(r, &rep); err != nil {
		return nil, err
	}
	return h.ReportConfig(ctx, proxyID, &rep)
}

// ReportConfig implements contract.FleetConfigServer. It answers
// `{"accepted": true}` once the report is durably recorded, and never before;
// a report that is the previous one again — the usual case — is accepted as
// readily as a new one, because a server that refused the repeat would break
// the proxy's report after every sync.
func (h handlers) ReportConfig(ctx context.Context, proxyID string, rep *contract.ProxyConfigReport) (*contract.ProxyConfigReportResponse, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return nil, errNoCaller
	}
	switch rep.State {
	case contract.ConfigStateApplied, contract.ConfigStatePendingRestart,
		contract.ConfigStateRejected, contract.ConfigStateFetchFailed:
	default:
		return nil, invalid("state must be applied, pending_restart, rejected or fetch_failed")
	}
	reportedAt, err := time.Parse(time.RFC3339, rep.ReportedAt)
	if err != nil {
		return nil, invalid("reported_at is required and must be an RFC 3339 instant")
	}
	if _, err := h.s.fleet.RecordConfigReport(ctx, caller.Tenant, proxyID, *rep, reportedAt); err != nil {
		if errors.Is(err, fleet.ErrNotEnrolled) {
			return nil, notEnrolled(proxyID)
		}
		return nil, err
	}
	return &contract.ProxyConfigReportResponse{Accepted: true}, nil
}

// configCaller checks that the credential may speak for the path's proxy, the
// way the event stream does.
func (h handlers) configCaller(ctx context.Context, proxyID, path string) (string, error) {
	caller, ok := callerFrom(ctx)
	if !ok {
		return "", errNoCaller
	}
	if proxyID == "" {
		return "", invalid("proxy_id is required")
	}
	if !caller.Authorises(proxyID) {
		h.s.logDeny(ctx, path, "the proxy credential does not authorise this proxy id",
			"proxy_id", proxyID, "token_id", caller.TokenID)
		return "", rejectCredential()
	}
	return proxyID, nil
}
