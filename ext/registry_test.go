// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hoplock/control/ext"
)

// fakeSink is a minimal AuditSink for registry tests.
type fakeSink struct{ name string }

func (fakeSink) Export(context.Context, []ext.AuditRecord) error { return nil }

// fakeArchive is a minimal ArchiveStore.
type fakeArchive struct{ name string }

func (fakeArchive) Archive(context.Context, []ext.AuditRecord) error { return nil }
func (fakeArchive) Search(context.Context, ext.ArchiveQuery) (ext.ArchivePage, error) {
	return ext.ArchivePage{}, nil
}

// fakeCoordinator is a minimal ClusterCoordinator, enough to satisfy the seal
// check without pulling Control's wiring into this package's tests.
type fakeCoordinator struct{}

func (fakeCoordinator) Node(context.Context) (ext.Node, error)      { return ext.Node{ID: "n"}, nil }
func (fakeCoordinator) Members(context.Context) ([]ext.Node, error) { return nil, nil }
func (fakeCoordinator) Lead(context.Context, string) (ext.Leadership, error) {
	return nil, errors.New("not implemented")
}
func (fakeCoordinator) Publish(context.Context, ext.ClusterEvent) error { return nil }
func (fakeCoordinator) Subscribe(context.Context, string) (<-chan ext.ClusterEvent, func(), error) {
	return nil, func() {}, nil
}

// controlDefault is the registration Control's own wiring uses.
var controlDefault = ext.Registration{Provider: ext.ProviderControl, Default: true}

// withCoordinator registers the one point whose catalogue entry promises a
// default, so that Seal can succeed in a test about something else.
func withCoordinator(t *testing.T, r *ext.Registry) {
	t.Helper()
	if err := r.RegisterClusterCoordinator(controlDefault, fakeCoordinator{}); err != nil {
		t.Fatalf("register coordinator: %v", err)
	}
}

func TestSingleImplementationPointRejectsASecondExtensionNamingBoth(t *testing.T) {
	r := ext.NewRegistry()
	first := ext.Registration{Provider: "github.com/hoplock/enterprise/archive", Version: "1.2.0"}
	second := ext.Registration{Provider: "example.com/other-archive", Version: "0.1.0"}

	if err := r.RegisterArchiveStore(first, fakeArchive{name: "first"}); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	err := r.RegisterArchiveStore(second, fakeArchive{name: "second"})
	if err == nil {
		t.Fatal("a second ArchiveStore was accepted; silent last-wins is how two modules fight invisibly")
	}

	var dup *ext.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("error is %T, want *ext.DuplicateError", err)
	}
	if dup.First.Provider != first.Provider || dup.Second.Provider != second.Provider {
		t.Errorf("DuplicateError names %q and %q, want %q and %q",
			dup.First.Provider, dup.Second.Provider, first.Provider, second.Provider)
	}
	for _, want := range []string{first.Provider, second.Provider} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message %q does not name %q; both registrants must be named", err, want)
		}
	}
}

func TestMultipleImplementationPointAcceptsDistinctProvidersAndRejectsARepeat(t *testing.T) {
	r := ext.NewRegistry()
	splunk := ext.Registration{Provider: "github.com/hoplock/enterprise/siem/splunk"}
	elastic := ext.Registration{Provider: "github.com/hoplock/enterprise/siem/elastic"}

	if err := r.RegisterAuditSink(splunk, fakeSink{name: "splunk"}); err != nil {
		t.Fatalf("splunk: %v", err)
	}
	if err := r.RegisterAuditSink(elastic, fakeSink{name: "elastic"}); err != nil {
		t.Fatalf("elastic: %v", err)
	}
	err := r.RegisterAuditSink(splunk, fakeSink{name: "splunk again"})
	if err == nil {
		t.Fatal("the same provider registered twice at a multiple-implementation point")
	}
	var dup *ext.DuplicateError
	if !errors.As(err, &dup) || !dup.SameProvider {
		t.Fatalf("error is %v, want a *ext.DuplicateError with SameProvider set", err)
	}

	withCoordinator(t, r)
	x, err := r.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	sinks := x.AuditSinks()
	if len(sinks) != 2 {
		t.Fatalf("got %d sinks, want 2", len(sinks))
	}
	if sinks[0].Registration.Provider != splunk.Provider || sinks[1].Registration.Provider != elastic.Provider {
		t.Errorf("sinks came back as %q, %q; registration order must be preserved",
			sinks[0].Registration.Provider, sinks[1].Registration.Provider)
	}
}

func TestAnExtensionSupersedesControlsDefaultAndTheListingSaysSo(t *testing.T) {
	enterprise := ext.Registration{Provider: "github.com/hoplock/enterprise/ha", Version: "2.0.0"}

	// Either order: the host binary may register before or after Control's
	// wiring adds its defaults, and neither is a conflict.
	for _, name := range []string{"default first", "extension first"} {
		t.Run(name, func(t *testing.T) {
			r := ext.NewRegistry()
			if name == "default first" {
				withCoordinator(t, r)
				if err := r.RegisterClusterCoordinator(enterprise, fakeCoordinator{}); err != nil {
					t.Fatalf("extension: %v", err)
				}
			} else {
				if err := r.RegisterClusterCoordinator(enterprise, fakeCoordinator{}); err != nil {
					t.Fatalf("extension: %v", err)
				}
				withCoordinator(t, r)
			}
			x, err := r.Seal()
			if err != nil {
				t.Fatalf("seal: %v", err)
			}
			for _, st := range x.Status() {
				if st.Info.Point != ext.PointClusterCoordinator {
					continue
				}
				if len(st.Registrations) != 1 {
					t.Fatalf("got %d registrations, want 1", len(st.Registrations))
				}
				if got := st.Registrations[0].Provider; got != enterprise.Provider {
					t.Errorf("provider in play is %q, want the extension %q", got, enterprise.Provider)
				}
			}
		})
	}
}

func TestTwoControlDefaultsAtOnePointAreRejected(t *testing.T) {
	r := ext.NewRegistry()
	withCoordinator(t, r)
	if err := r.RegisterClusterCoordinator(controlDefault, fakeCoordinator{}); err == nil {
		t.Fatal("Control registered two defaults at one point and nothing complained")
	}
}

func TestOnlyControlMayRegisterADefault(t *testing.T) {
	r := ext.NewRegistry()
	err := r.RegisterArchiveStore(ext.Registration{Provider: "example.com/thing", Default: true}, fakeArchive{})
	if err == nil {
		t.Fatal("an out-of-tree provider registered as Control's default")
	}
	if !ext.IsInvalid(err) {
		t.Errorf("error kind is %v, want invalid", ext.KindOf(err))
	}
}

func TestARegistrationMustNameItsProvider(t *testing.T) {
	r := ext.NewRegistry()
	if err := r.RegisterAuditSink(ext.Registration{}, fakeSink{}); err == nil {
		t.Fatal("an anonymous registration was accepted; an extension nobody can see is indistinguishable from a bug")
	}
}

func TestANilImplementationIsRejected(t *testing.T) {
	r := ext.NewRegistry()
	if err := r.RegisterArchiveStore(ext.Registration{Provider: "example.com/thing"}, nil); err == nil {
		t.Fatal("a nil ArchiveStore was accepted")
	}
}

func TestSealMakesTheSetImmutable(t *testing.T) {
	r := ext.NewRegistry()
	withCoordinator(t, r)
	if _, err := r.Seal(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	err := r.RegisterAuditSink(ext.Registration{Provider: "example.com/late"}, fakeSink{})
	if err == nil {
		t.Fatal("a registration was accepted after Seal; a request must never see a changing set")
	}
	if !ext.IsConflict(err) {
		t.Errorf("error kind is %v, want conflict", ext.KindOf(err))
	}
	if _, err := r.Seal(); err == nil {
		t.Fatal("the registry sealed twice")
	}
}

func TestSealRefusesAPointThatPromisesAControlDefaultAndHasNone(t *testing.T) {
	r := ext.NewRegistry()
	_, err := r.Seal()
	if err == nil {
		t.Fatal("an empty registry sealed; the coordinator's catalogue entry promises a default")
	}
	if !strings.Contains(err.Error(), "ClusterCoordinator") {
		t.Errorf("error %q does not name the point that is missing its default", err)
	}
}

func TestStatusCoversEveryPointIncludingTheEmptyOnes(t *testing.T) {
	x := ext.NoExtensions()
	status := x.Status()
	if len(status) != len(ext.Points()) {
		t.Fatalf("Status returned %d rows for %d points", len(status), len(ext.Points()))
	}
	for _, st := range status {
		if st.Registered() {
			t.Errorf("%v reports a registration in an empty set", st.Info.Point)
		}
		line := st.String()
		if !strings.Contains(line, st.Info.Absent) {
			t.Errorf("the line for %v does not say what happens instead: %q", st.Info.Point, line)
		}
	}
	if got := x.Providers(); len(got) != 0 {
		t.Errorf("Providers on an empty set returned %v", got)
	}
}

func TestAccessorsOnAnEmptySetReportAbsenceRatherThanReturningNil(t *testing.T) {
	x := ext.NoExtensions()
	if _, ok := x.ArchiveStore(); ok {
		t.Error("ArchiveStore reported an implementation in an empty set")
	}
	if _, ok := x.GrantWorkflow(); ok {
		t.Error("GrantWorkflow reported an implementation in an empty set")
	}
	if _, ok := x.IdentitySync(); ok {
		t.Error("IdentitySync reported an implementation in an empty set")
	}
	if _, ok := x.KeyStore(); ok {
		t.Error("KeyStore reported an implementation in an empty set")
	}
	if _, ok := x.ActionHandler(); ok {
		t.Error("ActionHandler reported an implementation in an empty set")
	}
	if _, ok := x.ClusterCoordinator(); ok {
		t.Error("ClusterCoordinator reported an implementation in an empty set")
	}
	if got := x.AuditSinks(); got != nil {
		t.Errorf("AuditSinks returned %v in an empty set", got)
	}
	if got := x.Notifiers(); got != nil {
		t.Errorf("Notifiers returned %v in an empty set", got)
	}
	if got := x.PolicyValidators(); got != nil {
		t.Errorf("PolicyValidators returned %v in an empty set", got)
	}
	if got := x.AccessContextProviders(); got != nil {
		t.Errorf("AccessContextProviders returned %v in an empty set", got)
	}
	if got := x.ReportProviders(); got != nil {
		t.Errorf("ReportProviders returned %v in an empty set", got)
	}
}

func TestProvidersListsDistinctNamesSorted(t *testing.T) {
	r := ext.NewRegistry()
	withCoordinator(t, r)
	if err := r.RegisterAuditSink(ext.Registration{Provider: "zeta"}, fakeSink{}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterNotifier(ext.Registration{Provider: "alpha"}, nil); err == nil {
		t.Fatal("a nil Notifier was accepted")
	}
	if err := r.RegisterArchiveStore(ext.Registration{Provider: "alpha"}, fakeArchive{}); err != nil {
		t.Fatal(err)
	}
	x, err := r.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	got := x.Providers()
	want := []string{"alpha", ext.ProviderControl, "zeta"}
	if len(got) != len(want) {
		t.Fatalf("Providers() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Providers() = %v, want %v", got, want)
		}
	}
}
