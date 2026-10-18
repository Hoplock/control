// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

func decodeConfig(t *testing.T, raw json.RawMessage) fleet.ConfigDocument {
	t.Helper()
	var doc fleet.ConfigDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return doc
}

func desired(t *testing.T, r *fleet.Registry, proxyID string) store.ProxyConfigState {
	t.Helper()
	state, err := r.DesiredConfig(t.Context(), testTenant, proxyID)
	if err != nil {
		t.Fatalf("DesiredConfig %s: %v", proxyID, err)
	}
	return state
}

func publish(t *testing.T, r *fleet.Registry, scope store.ConfigScope, doc fleet.ConfigDocument) fleet.ConfigPublication {
	t.Helper()
	p, err := r.PublishConfig(t.Context(), testTenant, scope, doc, "operator", nil)
	if err != nil {
		t.Fatalf("PublishConfig: %v", err)
	}
	return p
}

// report sends a configuration report as the proxy would.
func report(t *testing.T, r *fleet.Registry, proxyID string, rep contract.ProxyConfigReport, at time.Time) fleet.ConfigReportOutcome {
	t.Helper()
	out, err := r.RecordConfigReport(t.Context(), testTenant, proxyID, rep, at)
	if err != nil {
		t.Fatalf("RecordConfigReport: %v", err)
	}
	return out
}

// applied is the report a proxy sends once it runs what it was told.
func applied(s store.ProxyConfigState) contract.ProxyConfigReport {
	v, h := fleet.WireConfigVersion(s.DesiredVersion), fleet.WireConfigHash(s.DesiredHash)
	return contract.ProxyConfigReport{
		RunningVersion: v, RunningHash: h, DesiredVersion: v, DesiredHash: h, State: contract.ConfigStateApplied,
	}
}

// A zone document reaches its members, a per-proxy document overrides it, and
// the composition is what the proxy is told to run.
func TestConfigIsComposedFromZoneAndProxyScopes(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-b", "region", nil, fleet.Capabilities{})

	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{
		"logging.batch_size": float64(128), "control.cache.max_ttl": "5m",
	})
	publish(t, r, fleet.ProxyScope("p-b"), fleet.ConfigDocument{"logging.batch_size": float64(64)})

	if got := decodeConfig(t, desired(t, r, "p-a").DesiredDocument)["logging.batch_size"]; got != float64(128) {
		t.Errorf("p-a logging.batch_size = %v, want 128 (the zone's)", got)
	}
	doc := decodeConfig(t, desired(t, r, "p-b").DesiredDocument)
	if got := doc["logging.batch_size"]; got != float64(64) {
		t.Errorf("p-b logging.batch_size = %v, want 64 (the override)", got)
	}
	if doc["control.cache.max_ttl"] != "5m" {
		t.Error("p-b lost a zone key it did not override: the merge replaces keys, not documents")
	}
}

// An enrolling proxy is handed its configuration by the call that admitted it.
func TestEnrollmentHandsBackTheComposedConfig(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)})
	token, err := r.IssueGrant(ctx, testTenant, fleet.EnrollmentGrant{
		ProxyID: "p-late", GrantedZones: []fleet.Zone{"region"},
	}, time.Time{}, nil)
	if err != nil {
		t.Fatalf("IssueGrant: %v", err)
	}
	got, err := r.Enroll(ctx, fleet.EnrollmentRequest{
		Token: token.String(), ProxyID: "p-late", Zone: "region", PublicKey: []byte("k"),
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if v := decodeConfig(t, got.Config.DesiredDocument)["logging.batch_size"]; v != float64(128) {
		t.Errorf("the enrollment handed back %v, want the zone's 128", v)
	}
}

// Drift is derived from the report: only `applied` on the desired document is
// caught up, and each other state shows as itself.
func TestDriftIsDerivedFromTheReport(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)})
	state := desired(t, r, "p-a")

	rollout := func() fleet.ConfigRollout {
		t.Helper()
		all, err := r.ConfigRollouts(ctx, testTenant)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range all {
			if row.ProxyID == "p-a" {
				return row
			}
		}
		t.Fatal("no rollout row for p-a")
		return fleet.ConfigRollout{}
	}
	if rollout().CaughtUp {
		t.Fatal("a proxy that has never reported is caught up")
	}

	pending := applied(state)
	pending.RunningVersion, pending.RunningHash = "", "" // still on its bootstrap file
	pending.State, pending.RestartRequired = contract.ConfigStatePendingRestart, []string{"logging.batch_size"}
	report(t, r, "p-a", pending, c.now())
	if row := rollout(); row.CaughtUp || row.State != "pending_restart" || len(row.RestartRequired) != 1 {
		t.Errorf("pending_restart = %+v: it holds the new document and has NOT finished", row)
	}

	rejected := applied(state)
	rejected.RunningVersion, rejected.RunningHash = "", ""
	rejected.State, rejected.LastError = contract.ConfigStateRejected, `config field "proxy.id": not a fleet-owned setting`
	report(t, r, "p-a", rejected, c.now().Add(time.Second))
	if row := rollout(); row.CaughtUp || row.LastError == "" {
		t.Errorf("rejected = %+v, want the error shown", row)
	}

	failed := applied(state)
	failed.State, failed.LastError = contract.ConfigStateFetchFailed, "fetch: connection refused"
	report(t, r, "p-a", failed, c.now().Add(2*time.Second))
	if row := rollout(); row.CaughtUp || row.State != "fetch_failed" || row.LastError == "" {
		t.Errorf("fetch_failed = %+v", row)
	}

	// Even naming the desired document as running, pending_restart has not
	// finished: only `applied` is caught up (0021).
	claimed := applied(state)
	claimed.State, claimed.RestartRequired = contract.ConfigStatePendingRestart, []string{"logging.batch_size"}
	report(t, r, "p-a", claimed, c.now().Add(2500*time.Millisecond))
	if row := rollout(); row.CaughtUp {
		t.Errorf("pending_restart naming the desired document as running = %+v: it is NOT caught up", row)
	}

	report(t, r, "p-a", applied(state), c.now().Add(3*time.Second))
	if row := rollout(); !row.CaughtUp {
		t.Errorf("applied on the desired document = %+v, want caught up", row)
	}
	if drift, _ := r.ConfigDrift(ctx, testTenant); len(drift) != 0 {
		t.Errorf("drift = %+v after the proxy caught up", drift)
	}
	if h := healthOf(t, r, "p-a"); !h.Config.CaughtUp {
		t.Error("the fleet view still shows drift after the proxy caught up")
	}
}

// A running version that is not one of this server's is drift, recorded as it
// arrived — never a refusal: the proxy is saying truthfully what it runs.
func TestAnUnparseableRunningVersionIsDrift(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t)
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)})
	rep := applied(desired(t, r, "p-a"))
	rep.RunningVersion = "zone-eu-west/v41"
	if out := report(t, r, "p-a", rep, c.now()); !out.Changed {
		t.Fatal("the report was not recorded")
	}
	state := desired(t, r, "p-a")
	if !state.Drifted() || state.Report.RunningVersion != "zone-eu-west/v41" {
		t.Errorf("state = %+v, want drift with the version as reported", state.Report)
	}
}

// Reports repeat — after every sync, every reconnect, every 304 — so storing
// them is idempotent, and ordering has a rule.
func TestReportsAreIdempotentAndOrdered(t *testing.T) {
	t.Parallel()
	_, r, c := newRegistry(t)
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)})
	rep := applied(desired(t, r, "p-a"))

	changes := 0
	for i := range 100 {
		// A newer reported_at with identical content is still unchanged.
		if report(t, r, "p-a", rep, c.now().Add(time.Duration(i)*time.Second)).Changed {
			changes++
		}
	}
	if changes != 1 {
		t.Fatalf("a hundred identical reports produced %d changes, want 1", changes)
	}
	heard := desired(t, r, "p-a").Report.LastHeardAt
	if heard.IsZero() {
		t.Error("the last-heard time was not kept")
	}

	// A newer substantive report, then an older one arriving late.
	newer := rep
	newer.State, newer.LastError = contract.ConfigStateFetchFailed, "timeout"
	report(t, r, "p-a", newer, c.now().Add(time.Hour))
	older := rep
	older.State, older.LastError = contract.ConfigStateRejected, "stale"
	if report(t, r, "p-a", older, c.now().Add(30*time.Minute)).Changed {
		t.Error("an out-of-order older report overwrote a newer one")
	}
	if got := desired(t, r, "p-a").Report.State; got != "fetch_failed" {
		t.Errorf("state = %q, want the newer report's", got)
	}

	// The set in restart_required, not its order.
	a, b := rep, rep
	a.State, a.RestartRequired = contract.ConfigStatePendingRestart, []string{"chain.max_hops", "logging.batch_size"}
	b.State, b.RestartRequired = contract.ConfigStatePendingRestart, []string{"logging.batch_size", "chain.max_hops"}
	report(t, r, "p-a", a, c.now().Add(2*time.Hour))
	if report(t, r, "p-a", b, c.now().Add(3*time.Hour)).Changed {
		t.Error("the same restart_required set in another order was a change")
	}
}

// One config_changed per re-materialised proxy, naming its composed document —
// the same version and hash the fetch then serves. A no-op publish announces
// nothing.
func TestAZonePublishAnnouncesEachProxyItMoved(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	_, r, _ := newRegistry(t, fleet.WithConfigPublisher(pub))
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-b", "region", nil, fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-c", "other", nil, fleet.Capabilities{})

	p := publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)})
	if len(p.Changed) != 2 || p.Unannounced() != 0 {
		t.Fatalf("publication = %+v, want two proxies announced", p)
	}
	if got := pub.announced(); len(got) != 2 || got[0] == "p-c" || got[1] == "p-c" {
		t.Fatalf("announced to %v, want p-a and p-b only", got)
	}
	doc, ok, err := r.FetchConfig(ctx, testTenant, "p-a")
	if err != nil || !ok {
		t.Fatalf("FetchConfig: %v %v", ok, err)
	}
	var ev contract.ConfigChangedEvent
	for i, id := range pub.announced() {
		if id == "p-a" {
			ev = pub.events[i]
		}
	}
	if ev.Version != doc.Version || ev.Hash != doc.Hash {
		t.Errorf("announced %+v, the fetch serves %s/%s", ev, doc.Version, doc.Hash)
	}

	before := len(pub.announced())
	if p := publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)}); len(p.Changed) != 0 {
		t.Errorf("a no-op publish changed %+v", p.Changed)
	}
	if len(pub.announced()) != before {
		t.Error("a no-op publish announced something")
	}
}

// DELIVERY WITHOUT RECONNECT: with the first announcement made to fail, a proxy
// still learns of the new document through the retry, and a registry built
// fresh over the same store — a restart between commit and emit — owes it too.
func TestAFailedAnnouncementIsRetriedAcrossARestart(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{fail: 1}
	st, r, _ := newRegistry(t, fleet.WithConfigPublisher(pub))
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	p := publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)})
	if p.Unannounced() != 1 || p.Changed[0].Error == "" {
		t.Fatalf("publication = %+v, want the failed announcement reported", p)
	}
	if len(pub.announced()) != 0 {
		t.Fatal("a failed announcement was recorded as made")
	}

	// "Restart": a new registry over the same store, as the next process.
	restarted := fleet.New(st, fleet.WithConfigPublisher(pub))
	tried, err := restarted.Announce(ctx, testTenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(tried) != 1 || !tried[0].Announced {
		t.Fatalf("Announce = %+v, want p-a announced", tried)
	}
	if got := pub.announced(); len(got) != 1 || got[0] != "p-a" {
		t.Fatalf("announced to %v, want p-a", got)
	}
	// And once announced, nothing more is owed.
	if again, _ := restarted.Announce(ctx, testTenant); len(again) != 0 {
		t.Errorf("an announced document was announced again: %+v", again)
	}
}

// The fetch: 204 with nothing published, the document when there is one, and
// ErrNotEnrolled for an id this tenant does not hold — another tenant's
// included, which is the tenancy leak on this route.
func TestTheFetchServesTheDocumentDesiredNow(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	enrolled(t, r, tenantB, "p-other", "region", nil, fleet.Capabilities{})

	if _, ok, err := r.FetchConfig(ctx, testTenant, "p-a"); err != nil || ok {
		t.Fatalf("nothing published: ok=%v err=%v, want the bootstrap file alone", ok, err)
	}
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)})
	doc, ok, err := r.FetchConfig(ctx, testTenant, "p-a")
	if err != nil || !ok || doc.Settings["logging.batch_size"] != float64(128) {
		t.Fatalf("fetch = %+v %v %v", doc, ok, err)
	}
	if _, _, err := r.FetchConfig(ctx, testTenant, "p-nobody"); !errorsIs(err, fleet.ErrNotEnrolled) {
		t.Errorf("an unknown id: %v, want ErrNotEnrolled", err)
	}
	if _, _, err := r.FetchConfig(ctx, testTenant, "p-other"); !errorsIs(err, fleet.ErrNotEnrolled) {
		t.Errorf("another tenant's proxy: %v, want ErrNotEnrolled", err)
	}
	if _, err := r.RecordConfigReport(ctx, testTenant, "p-other", contract.ProxyConfigReport{State: "applied"}, time.Now()); !errorsIs(err, fleet.ErrNotEnrolled) {
		t.Errorf("a report for another tenant's proxy: %v, want ErrNotEnrolled", err)
	}
}

// A publish naming anything but a fleet-owned key is refused before anything
// is stored, at either scope.
func TestAPublishOfANonFleetOwnedKeyStoresNothing(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	for _, scope := range []store.ConfigScope{fleet.ZoneScope("region"), fleet.ProxyScope("p-a")} {
		_, err := r.PublishConfig(ctx, testTenant, scope, fleet.ConfigDocument{"control.endpoint": "https://x"}, "operator", nil)
		var ke *fleet.ConfigKeyError
		if !asKeyError(err, &ke) || ke.Key != "control.endpoint" {
			t.Errorf("%s: %v, want a refusal naming control.endpoint", scope.Kind, err)
		}
	}
	if _, ok, err := r.FetchConfig(ctx, testTenant, "p-a"); err != nil || ok {
		t.Errorf("a refused publish left something to fetch: %v %v", ok, err)
	}
}

// Rolling out a bad config must be survivable.
func TestRollbackIsFirstClass(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})

	v1 := publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"chain.max_hops": float64(2)})
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"chain.max_hops": float64(9)})
	back, err := r.RollbackConfig(ctx, testTenant, fleet.ZoneScope("region"), "operator", nil)
	if err != nil {
		t.Fatalf("RollbackConfig: %v", err)
	}
	if back.Version != v1.Version {
		t.Errorf("rolled back to version %d, want %d", back.Version, v1.Version)
	}
	if got := decodeConfig(t, desired(t, r, "p-a").DesiredDocument)["chain.max_hops"]; got != float64(2) {
		t.Errorf("after the rollback chain.max_hops = %v, want 2", got)
	}
	forward, err := r.RollbackConfig(ctx, testTenant, fleet.ZoneScope("region"), "operator", nil)
	if err != nil || forward.Version == v1.Version {
		t.Errorf("rolling back twice = %+v %v", forward, err)
	}
}

func TestRollbackWithNoTargetIsRefused(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()
	if _, err := r.RollbackConfig(ctx, testTenant, fleet.ZoneScope("region"), "operator", nil); !errorsIs(err, fleet.ErrNoRollbackTarget) {
		t.Errorf("rollback with no published version: %v, want ErrNoRollbackTarget", err)
	}
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"chain.max_hops": float64(2)})
	if _, err := r.RollbackConfig(ctx, testTenant, fleet.ZoneScope("region"), "operator", nil); !errorsIs(err, fleet.ErrNoRollbackTarget) {
		t.Errorf("rollback from the first version: %v, want ErrNoRollbackTarget", err)
	}
}

// A document staged for a proxy that has not enrolled yet is legitimate.
func TestAConfigMayBeStagedBeforeTheProxyEnrolls(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	publish(t, r, fleet.ProxyScope("p-future"), fleet.ConfigDocument{"chain.max_hops": float64(3)})
	if _, err := r.DesiredConfig(t.Context(), testTenant, "p-future"); !errorsIs(err, fleet.ErrNotEnrolled) {
		t.Errorf("DesiredConfig before enrollment: %v, want ErrNotEnrolled", err)
	}
	enrolled(t, r, testTenant, "p-future", "region", nil, fleet.Capabilities{})
	if got := decodeConfig(t, desired(t, r, "p-future").DesiredDocument)["chain.max_hops"]; got != float64(3) {
		t.Errorf("after enrollment chain.max_hops = %v, want 3", got)
	}
}

// The composition replaces top-level keys rather than merging deeply.
func TestComposeReplacesTopLevelKeys(t *testing.T) {
	t.Parallel()
	zone := fleet.ConfigDocument{"limits": map[string]any{"a": 1, "b": 2}, "keep": "yes"}
	proxy := fleet.ConfigDocument{"limits": map[string]any{"a": 9}}
	got := fleet.ComposeConfig(zone, proxy)
	limits, ok := got["limits"].(map[string]any)
	if !ok || len(limits) != 1 || limits["a"] != 9 || got["keep"] != "yes" {
		t.Errorf("composed = %v", got)
	}
}

// The desired hash is the digest of the stored document's exact bytes.
func TestTheDesiredHashIsTheDigestOfTheStoredDocument(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{
		"logging.retry_max": "1m", "chain.max_hops": float64(2), "dial.dial_timeout": "10s",
	})
	state := desired(t, r, "p-a")
	sum := sha256.Sum256(state.DesiredDocument)
	if got := hex.EncodeToString(sum[:]); got != state.DesiredHash {
		t.Errorf("hash of the stored document = %s, stored hash = %s", got, state.DesiredHash)
	}
}

// NOTHING PUBLISHED IS ONE ANSWER EVERYWHERE (0021). The fetch answers 204 and
// the proxy runs on its bootstrap file, so there is no document to announce,
// none to be behind, and a proxy reporting its bootstrap file is not drift. It
// used to be all three: enrollment materialised the empty composition as
// version 1, which the announcer named in a `config_changed` the fetch then
// denied, and which the rollout view reported as never reached.
func TestNothingPublishedIsNeitherAnnouncedNorDrift(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	_, r, c := newRegistry(t, fleet.WithConfigPublisher(pub))
	ctx := t.Context()
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})

	if _, ok, err := r.FetchConfig(ctx, testTenant, "p-a"); err != nil || ok {
		t.Fatalf("fetch with nothing published: ok=%v err=%v, want 204", ok, err)
	}
	if tried, err := r.Announce(ctx, testTenant); err != nil || len(tried) != 0 || len(pub.announced()) != 0 {
		t.Errorf("announced %+v (%v) for a proxy with nothing published", tried, err)
	}
	report(t, r, "p-a", contract.ProxyConfigReport{State: contract.ConfigStateApplied}, c.now())
	if drift, err := r.ConfigDrift(ctx, testTenant); err != nil || len(drift) != 0 {
		t.Errorf("drift = %+v (%v) for a proxy on its bootstrap file with nothing published", drift, err)
	}

	// The first publish is the proxy's first document, announced once.
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(128)})
	doc, ok, err := r.FetchConfig(ctx, testTenant, "p-a")
	if err != nil || !ok {
		t.Fatalf("fetch after a publish: %v %v", ok, err)
	}
	if got := pub.announced(); len(got) != 1 || pub.events[0].Version != doc.Version || pub.events[0].Hash != doc.Hash {
		t.Errorf("announced %v %+v, want the one document the fetch serves (%s)", got, pub.events, doc.Version)
	}
	if drift, _ := r.ConfigDrift(ctx, testTenant); len(drift) != 1 {
		t.Errorf("drift = %+v, want p-a behind its first document", drift)
	}
}

// A publish is a tenant the announcer walks (0021): an announcement lost in a
// tenant no proxy had subscribed in on this node, and that is not the
// configured one, was never retried. Killed between commit and emit here, the
// announcement reaches the proxy on the announcer's next turn and the proxy
// converges.
func TestAnAnnouncementLostInAnyTenantIsRetriedUntilTheProxyConverges(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{fail: 1}
	_, r, c := newRegistry(t, fleet.WithConfigPublisher(pub))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	enrolled(t, r, tenantB, "p-b", "region", nil, fleet.Capabilities{})

	p, err := r.PublishConfig(ctx, tenantB, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(64)}, "operator", nil)
	if err != nil || p.Unannounced() != 1 {
		t.Fatalf("publication = %+v, %v; want its one announcement lost", p, err)
	}
	// An interval longer than the test: only the pass the announcer makes
	// as it starts can deliver this.
	go r.WatchAnnouncements(ctx, time.Hour)
	waitAnnounced(t, pub, 1)

	state, err := r.DesiredConfig(ctx, tenantB, "p-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordConfigReport(ctx, tenantB, "p-b", applied(state), c.now()); err != nil {
		t.Fatal(err)
	}
	if drift, err := r.ConfigDrift(ctx, tenantB); err != nil || len(drift) != 0 {
		t.Errorf("drift = %+v (%v) after the proxy applied the retried document", drift, err)
	}
}

// The announcer retries on its timer as well as at start: an announcement
// that fails at publish and again on the first pass is delivered by a later
// one.
func TestTheAnnouncerRetriesOnItsTimer(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{fail: 2}
	_, r, _ := newRegistry(t, fleet.WithConfigPublisher(pub))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(64)})
	go r.WatchAnnouncements(ctx, 20*time.Millisecond)
	waitAnnounced(t, pub, 1)
}

func waitAnnounced(t *testing.T, pub *recordingPublisher, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(pub.announced()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("announced %v, want %d: the lost announcement was never retried", pub.announced(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A report naming the desired document settles its announcement (0021): the
// proxy knows of it, so nothing more is owed even though the stream never took
// the `config_changed` — and the announcer, which would otherwise retry it,
// leaves it alone.
func TestAReportNamingTheDesiredDocumentSettlesItsAnnouncement(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{fail: 1}
	_, r, c := newRegistry(t, fleet.WithConfigPublisher(pub))
	ctx := t.Context()
	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	if p := publish(t, r, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(64)}); p.Unannounced() != 1 {
		t.Fatalf("publication = %+v, want its announcement lost", p)
	}
	state := desired(t, r, "p-a")
	rep := applied(state)
	rep.RunningVersion, rep.RunningHash = "", ""
	rep.State, rep.LastError = contract.ConfigStateFetchFailed, "fetch: connection refused"
	report(t, r, "p-a", rep, c.now())
	if tried, err := r.Announce(ctx, testTenant); err != nil || len(tried) != 0 {
		t.Errorf("Announce = %+v, %v after the proxy reported knowing the document; want nothing owed", tried, err)
	}
}

// SEVERAL NODES (0021). A node's event bus reaches only the subscriptions held
// on that node, so a `config_changed` the publishing node's bus takes reaches
// nobody when the proxy's stream is held by another node. The announcement is
// owed until a live subscription takes it, and the node holding the stream —
// whose announcer walks the same durable markers — delivers it.
func TestAnAnnouncementReachesAProxySubscribedOnAnotherNode(t *testing.T) {
	t.Parallel()
	nodeA := &recordingPublisher{elsewhere: true}
	st, a, _ := newRegistry(t, fleet.WithConfigPublisher(nodeA), fleet.WithSubscriptionState(subs{}))
	ctx := t.Context()
	enrolled(t, a, testTenant, "p-a", "region", nil, fleet.Capabilities{})

	p := publish(t, a, fleet.ZoneScope("region"), fleet.ConfigDocument{"logging.batch_size": float64(64)})
	if len(p.Changed) != 1 || p.Changed[0].Announced || p.Changed[0].Error == "" {
		t.Fatalf("publication on a node without the stream = %+v, want it not announced, saying why", p)
	}
	if tried, _ := a.Announce(ctx, testTenant); len(tried) != 0 || len(nodeA.missed) != 1 {
		t.Errorf("node A re-announced %+v to a proxy it holds no stream for (missed %v)", tried, nodeA.missed)
	}

	// Node B holds p-a's stream: its announcer delivers what A could not.
	nodeB := &recordingPublisher{}
	b := fleet.New(st, fleet.WithConfigPublisher(nodeB),
		fleet.WithSubscriptionState(subs{"p-a": time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}))
	tried, err := b.Announce(ctx, testTenant)
	if err != nil || len(tried) != 1 || !tried[0].Announced {
		t.Fatalf("node B's announcer = %+v, %v; want p-a announced", tried, err)
	}
	if got := nodeB.announced(); len(got) != 1 || got[0] != "p-a" {
		t.Errorf("node B announced to %v, want p-a", got)
	}
	// Delivered once, it is owed by no node.
	for name, r := range map[string]*fleet.Registry{"A": a, "B": b} {
		if again, _ := r.Announce(ctx, testTenant); len(again) != 0 {
			t.Errorf("node %s announced %+v after the stream took it", name, again)
		}
	}
}
