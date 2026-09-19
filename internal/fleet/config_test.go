// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package fleet_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

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

// A zone document reaches its members, a per-proxy document overrides it, and the
// composition is what the proxy is told to run.
func TestConfigIsComposedFromZoneAndProxyScopes(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-b", "region", nil, fleet.Capabilities{})

	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"), fleet.ConfigDocument{
		"log_shipping_seconds": float64(30),
		"serves_zones":         []any{"region"},
	}, "operator"); err != nil {
		t.Fatalf("PublishConfig zone: %v", err)
	}
	if _, err := r.PublishConfig(ctx, testTenant, fleet.ProxyScope("p-b"), fleet.ConfigDocument{
		"log_shipping_seconds": float64(5),
	}, "operator"); err != nil {
		t.Fatalf("PublishConfig proxy: %v", err)
	}

	a, err := r.DesiredConfig(ctx, testTenant, "p-a")
	if err != nil {
		t.Fatalf("DesiredConfig p-a: %v", err)
	}
	if got := decodeConfig(t, a.DesiredDocument)["log_shipping_seconds"]; got != float64(30) {
		t.Errorf("p-a log_shipping_seconds = %v, want 30 (the zone's)", got)
	}

	b, err := r.DesiredConfig(ctx, testTenant, "p-b")
	if err != nil {
		t.Fatalf("DesiredConfig p-b: %v", err)
	}
	doc := decodeConfig(t, b.DesiredDocument)
	if got := doc["log_shipping_seconds"]; got != float64(5) {
		t.Errorf("p-b log_shipping_seconds = %v, want 5 (the override)", got)
	}
	if doc["serves_zones"] == nil {
		t.Error("p-b lost a zone key it did not override: the merge replaces keys, not documents")
	}
}

// An enrolling proxy is handed its configuration by the call that admitted it: it
// must not have to ask a second time for the thing it needs in order to work.
func TestEnrollmentHandsBackTheComposedConfig(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"), fleet.ConfigDocument{
		"log_shipping_seconds": float64(30),
	}, "operator"); err != nil {
		t.Fatalf("PublishConfig: %v", err)
	}

	token, err := r.IssueGrant(ctx, testTenant, fleet.EnrollmentGrant{
		ProxyID: "p-late", GrantedZones: []fleet.Zone{"region"},
	}, time.Time{})
	if err != nil {
		t.Fatalf("IssueGrant: %v", err)
	}
	got, err := r.Enroll(ctx, fleet.EnrollmentRequest{
		Token: token.String(), ProxyID: "p-late", Zone: "region", PublicKey: []byte("k"),
	})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if got.Config.DesiredVersion == 0 {
		t.Fatal("the enrollment handed back no configuration version")
	}
	if v := decodeConfig(t, got.Config.DesiredDocument)["log_shipping_seconds"]; v != float64(30) {
		t.Errorf("the enrollment handed back %v, want the zone's 30", v)
	}
}

// Drift is visible, because silent drift across a fleet is indistinguishable from
// a broken rollout.
func TestDriftIsVisibleUntilTheProxyReports(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"),
		fleet.ConfigDocument{"k": "v1"}, "operator"); err != nil {
		t.Fatalf("PublishConfig: %v", err)
	}

	drifted, err := r.ConfigDrift(ctx, testTenant)
	if err != nil {
		t.Fatalf("ConfigDrift: %v", err)
	}
	if len(drifted) != 1 || drifted[0].ProxyID != "p-a" {
		t.Fatalf("drift = %+v, want p-a behind", drifted)
	}

	state, err := r.DesiredConfig(ctx, testTenant, "p-a")
	if err != nil {
		t.Fatalf("DesiredConfig: %v", err)
	}
	if err := r.ReportRunningConfig(ctx, testTenant, "p-a", state.DesiredVersion, state.DesiredHash); err != nil {
		t.Fatalf("ReportRunningConfig: %v", err)
	}

	drifted, err = r.ConfigDrift(ctx, testTenant)
	if err != nil {
		t.Fatalf("ConfigDrift: %v", err)
	}
	if len(drifted) != 0 {
		t.Errorf("drift = %+v, want none once the proxy has caught up", drifted)
	}
}

// A heartbeat can carry the running version, so a proxy reports drift on the
// channel it already has rather than needing a second call.
func TestHeartbeatCanReportTheRunningConfig(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"),
		fleet.ConfigDocument{"k": "v1"}, "operator"); err != nil {
		t.Fatalf("PublishConfig: %v", err)
	}
	state, err := r.DesiredConfig(ctx, testTenant, "p-a")
	if err != nil {
		t.Fatalf("DesiredConfig: %v", err)
	}

	if err := r.Heartbeat(ctx, testTenant, fleet.HeartbeatReport{
		ProxyID:              "p-a",
		RunningConfigVersion: state.DesiredVersion,
		RunningConfigHash:    state.DesiredHash,
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if healthOf(t, r, "p-a").ConfigDrift {
		t.Error("the fleet view still reports drift after the heartbeat caught up")
	}
}

// Rolling out a bad config must be survivable: the previous version is kept and
// rollback is a call rather than an operator retyping yesterday's document.
func TestRollbackIsFirstClass(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})

	v1, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"),
		fleet.ConfigDocument{"k": "good"}, "operator")
	if err != nil {
		t.Fatalf("PublishConfig v1: %v", err)
	}
	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"),
		fleet.ConfigDocument{"k": "bad"}, "operator"); err != nil {
		t.Fatalf("PublishConfig v2: %v", err)
	}
	if got := decodeConfig(t, desired(t, r, "p-a").DesiredDocument)["k"]; got != "bad" {
		t.Fatalf("after the second publish k = %v, want bad", got)
	}

	back, err := r.RollbackConfig(ctx, testTenant, fleet.ZoneScope("region"), "operator")
	if err != nil {
		t.Fatalf("RollbackConfig: %v", err)
	}
	if back != v1 {
		t.Errorf("rolled back to version %d, want %d", back, v1)
	}
	if got := decodeConfig(t, desired(t, r, "p-a").DesiredDocument)["k"]; got != "good" {
		t.Errorf("after the rollback k = %v, want good", got)
	}

	// A rollback is itself reversible: the version it rolled off is still
	// stored, so going forward again is another rollback.
	forward, err := r.RollbackConfig(ctx, testTenant, fleet.ZoneScope("region"), "operator")
	if err != nil {
		t.Fatalf("second RollbackConfig: %v", err)
	}
	if forward == v1 {
		t.Errorf("rolling back twice stayed on %d", forward)
	}
	if got := decodeConfig(t, desired(t, r, "p-a").DesiredDocument)["k"]; got != "bad" {
		t.Errorf("after rolling forward k = %v, want bad", got)
	}
}

func desired(t *testing.T, r *fleet.Registry, proxyID string) store.ProxyConfigState {
	t.Helper()
	state, err := r.DesiredConfig(t.Context(), testTenant, proxyID)
	if err != nil {
		t.Fatalf("DesiredConfig %s: %v", proxyID, err)
	}
	return state
}

// A scope that has never been republished has nothing to go back to, and says so
// rather than serving an empty document — which is itself a real configuration.
func TestRollbackWithNoTargetIsRefused(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	if _, err := r.RollbackConfig(ctx, testTenant, fleet.ZoneScope("region"), "operator"); !errorsIs(err, fleet.ErrNoRollbackTarget) {
		t.Errorf("rollback with no published version: %v, want ErrNoRollbackTarget", err)
	}

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"),
		fleet.ConfigDocument{"k": "v1"}, "operator"); err != nil {
		t.Fatalf("PublishConfig: %v", err)
	}
	if _, err := r.RollbackConfig(ctx, testTenant, fleet.ZoneScope("region"), "operator"); !errorsIs(err, fleet.ErrNoRollbackTarget) {
		t.Errorf("rollback from the first version: %v, want ErrNoRollbackTarget", err)
	}
}

// A publish whose composed document is unchanged for a proxy does NOT bump that
// proxy's version. Drift that means nothing is drift an operator stops reading.
func TestANoOpPublishDoesNotManufactureDrift(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	enrolled(t, r, testTenant, "p-b", "other", nil, fleet.Capabilities{})

	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"),
		fleet.ConfigDocument{"k": "v1"}, "operator"); err != nil {
		t.Fatalf("PublishConfig: %v", err)
	}
	state := desired(t, r, "p-a")
	if err := r.ReportRunningConfig(ctx, testTenant, "p-a", state.DesiredVersion, state.DesiredHash); err != nil {
		t.Fatalf("ReportRunningConfig: %v", err)
	}

	// Publishing the same document again.
	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"),
		fleet.ConfigDocument{"k": "v1"}, "operator"); err != nil {
		t.Fatalf("second PublishConfig: %v", err)
	}
	if got := desired(t, r, "p-a"); got.DesiredVersion != state.DesiredVersion {
		t.Errorf("version moved to %d on a no-op publish, want %d", got.DesiredVersion, state.DesiredVersion)
	}
	if drifted, err := r.ConfigDrift(ctx, testTenant); err != nil {
		t.Fatalf("ConfigDrift: %v", err)
	} else {
		for _, d := range drifted {
			if d.ProxyID == "p-a" {
				t.Error("a no-op publish put p-a into drift")
			}
		}
	}

	// And a zone publish does not touch a proxy in another zone.
	if got := desired(t, r, "p-b").DesiredVersion; got != 1 {
		t.Errorf("p-b's version = %d, want 1: a publish to another zone must not move it", got)
	}
}

// A document staged for a proxy that has not enrolled yet is legitimate: an
// operator may prepare a rollout before the kit arrives. Enrollment materialises
// it, and the publish does not fail in the meantime.
func TestAConfigMayBeStagedBeforeTheProxyEnrolls(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	if _, err := r.PublishConfig(ctx, testTenant, fleet.ProxyScope("p-future"),
		fleet.ConfigDocument{"k": "staged"}, "operator"); err != nil {
		t.Fatalf("PublishConfig for an unenrolled proxy: %v", err)
	}
	if _, err := r.DesiredConfig(ctx, testTenant, "p-future"); !errorsIs(err, fleet.ErrNotEnrolled) {
		t.Errorf("DesiredConfig before enrollment: %v, want ErrNotEnrolled", err)
	}

	enrolled(t, r, testTenant, "p-future", "region", nil, fleet.Capabilities{})
	if got := decodeConfig(t, desired(t, r, "p-future").DesiredDocument)["k"]; got != "staged" {
		t.Errorf("after enrollment k = %v, want staged", got)
	}
}

// Delivery goes to the event stream (0009) rather than a second channel. The seam
// is exercised here; see [fleet.ConfigPublisher] for why it cannot reach the wire
// until an upstream contract change lands.
func TestPublishHandsTheChangeToTheEventStream(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	_, r, _ := newRegistry(t, fleet.WithConfigPublisher(pub))
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"),
		fleet.ConfigDocument{"k": "v1"}, "operator"); err != nil {
		t.Fatalf("PublishConfig: %v", err)
	}
	if len(pub.calls) == 0 {
		t.Fatal("the publish did not reach the delivery seam")
	}
	if pub.calls[0] != "p-a" {
		t.Errorf("delivered to %q, want p-a", pub.calls[0])
	}
}

// The composition replaces top-level keys rather than merging deeply, so a proxy
// can actually replace an inherited object instead of getting the union.
func TestComposeReplacesTopLevelKeys(t *testing.T) {
	t.Parallel()
	zone := fleet.ConfigDocument{"limits": map[string]any{"a": 1, "b": 2}, "keep": "yes"}
	proxy := fleet.ConfigDocument{"limits": map[string]any{"a": 9}}

	got := fleet.ComposeConfig(zone, proxy)
	limits, ok := got["limits"].(map[string]any)
	if !ok {
		t.Fatalf("limits = %T, want a map", got["limits"])
	}
	if len(limits) != 1 || limits["a"] != 9 {
		t.Errorf("limits = %v, want the override to replace the object", limits)
	}
	if got["keep"] != "yes" {
		t.Error("an unrelated key was lost")
	}
}

// The desired hash is the digest of the desired document's exact bytes, and it
// survives the round trip through storage.
//
// It is asserted because the obvious storage choice does not have this property:
// jsonb re-renders whitespace and key order, so a document stored that way comes
// back semantically equal and byte-different, and the hash beside it stops being
// the hash of it.
func TestTheDesiredHashIsTheDigestOfTheStoredDocument(t *testing.T) {
	t.Parallel()
	_, r, _ := newRegistry(t)
	ctx := t.Context()

	enrolled(t, r, testTenant, "p-a", "region", nil, fleet.Capabilities{})
	// Keys deliberately out of alphabetical order, which is what a re-render
	// would change.
	if _, err := r.PublishConfig(ctx, testTenant, fleet.ZoneScope("region"), fleet.ConfigDocument{
		"zulu":  "last",
		"alpha": "first",
		"mike":  map[string]any{"nested": true, "count": float64(2)},
	}, "operator"); err != nil {
		t.Fatalf("PublishConfig: %v", err)
	}

	state := desired(t, r, "p-a")
	sum := sha256.Sum256(state.DesiredDocument)
	if got, want := hex.EncodeToString(sum[:]), state.DesiredHash; got != want {
		t.Errorf("hash of the stored document = %s, stored hash = %s: the bytes did not survive", got, want)
	}
}
