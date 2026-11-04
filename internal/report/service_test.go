// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
)

// The service's own behaviour; the north-bound tests drive it over the wire
// (internal/httpapi/north, reports_test.go).

var review = ext.ReportDescriptor{
	ReportID: "review", Formats: []ext.ReportFormat{ext.ReportFormatJSON},
	Parameters: []ext.ReportParameter{{Name: "regime", Required: true}},
}

var valid = Request{
	Provider: "example/p", Report: "review", Format: "json",
	From: "2026-01-01T00:00:00Z", To: "2026-01-02T00:00:00Z", Parameters: map[string]string{"regime": "sox"},
}

// provider is a scriptable ext.ReportProvider.
type provider struct {
	reports  func(ctx context.Context, tenant ext.Tenant) ([]ext.ReportDescriptor, error)
	generate func(ctx context.Context, req ext.ReportRequest) (ext.Report, error)
}

func (p provider) Reports(ctx context.Context, tenant ext.Tenant) ([]ext.ReportDescriptor, error) {
	if p.reports == nil {
		return []ext.ReportDescriptor{review}, nil
	}
	return p.reports(ctx, tenant)
}

func (p provider) Generate(ctx context.Context, req ext.ReportRequest) (ext.Report, error) {
	if p.generate == nil {
		return ext.Report{Format: req.Format, ContentType: "application/json", Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}
	return p.generate(ctx, req)
}

func service(t *testing.T, max int, p ext.ReportProvider) *Service {
	t.Helper()
	s, err := New([]ext.Bound[ext.ReportProvider]{{Registration: ext.Registration{Provider: "example/p"}, Impl: p}},
		Options{MaxConcurrent: max})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func kindOf(t *testing.T, err error) Kind {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want a *report.Error", err)
	}
	return e.Kind
}

func TestTheCeilingDefaultsAndRefusesANegative(t *testing.T) {
	if s := service(t, 0, provider{}); s.MaxConcurrent() != DefaultMaxConcurrent {
		t.Errorf("zero ceiling = %d, want %d", s.MaxConcurrent(), DefaultMaxConcurrent)
	}
	if _, err := New(nil, Options{MaxConcurrent: -1}); err == nil {
		t.Error("a negative ceiling was accepted")
	}
}

func TestTheProblemsAreTheSurfacesVocabulary(t *testing.T) {
	for ours, theirs := range map[string]string{
		ProblemRequired: access.ProblemRequired, ProblemInvalid: access.ProblemInvalid,
		ProblemNotAfterStart: access.ProblemNotAfterStart,
	} {
		if ours != theirs {
			t.Errorf("problem %q is %q in internal/access", ours, theirs)
		}
	}
}

func TestADescriptorControlCannotServeRefusesTheProvider(t *testing.T) {
	json := []ext.ReportFormat{ext.ReportFormatJSON}
	for name, ds := range map[string][]ext.ReportDescriptor{
		"an empty id":               {{Formats: json}},
		"a repeated id":             {{ReportID: "a", Formats: json}, {ReportID: "a", Formats: json}},
		"no formats":                {{ReportID: "a"}},
		"a format outside the enum": {{ReportID: "a", Formats: []ext.ReportFormat{ext.ReportFormatPDF + 1}}},
		"a nameless parameter":      {{ReportID: "a", Formats: json, Parameters: []ext.ReportParameter{{}}}},
		"a parameter twice":         {{ReportID: "a", Formats: json, Parameters: []ext.ReportParameter{{Name: "x"}, {Name: "x"}}}},
	} {
		if err := servable(ds); err == nil {
			t.Errorf("%s: served", name)
		}
	}
	if err := servable([]ext.ReportDescriptor{review}); err != nil {
		t.Errorf("a servable descriptor was refused: %v", err)
	}
}

func TestAPanicInAProviderIsAFailureAndNeverACrash(t *testing.T) {
	s := service(t, 1, provider{
		reports:  func(context.Context, ext.Tenant) ([]ext.ReportDescriptor, error) { panic("listing bug") },
		generate: func(context.Context, ext.ReportRequest) (ext.Report, error) { panic("generate bug") },
	})
	entries := s.List(t.Context(), "t")
	if len(entries) != 1 || entries[0].Failure == nil || entries[0].Failure.Kind != KindFailed {
		t.Errorf("a panicking Reports listed %+v, want one failed entry", entries)
	}
	if _, err := s.Generate(t.Context(), "t", valid); kindOf(t, err) != KindFailed {
		t.Errorf("a panicking provider = %v, want KindFailed", err)
	}

	panicky := service(t, 1, provider{generate: func(_ context.Context, req ext.ReportRequest) (ext.Report, error) {
		return ext.Report{Format: req.Format, ContentType: "application/json", Body: panicBody{}}, nil
	}})
	g, err := panicky.Generate(t.Context(), "t", valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Body.Read(make([]byte, 8)); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Errorf("a panicking Read = %v, want an error", err)
	}
	if err := g.Body.Close(); err == nil {
		t.Error("a panicking Close returned no error")
	}
	// The place was released despite the panic.
	if _, err := panicky.Generate(t.Context(), "t", valid); err != nil {
		t.Errorf("the next generation = %v; the panicking body's place was not released", err)
	}
}

type panicBody struct{}

func (panicBody) Read([]byte) (int, error) { panic("read bug") }
func (panicBody) Close() error             { panic("close bug") }

func TestOnlyAnExtErrorCarriesAKindAndOnlyGenerateMeansNotFoundOrInvalid(t *testing.T) {
	notFound := ext.Errorf(ext.PointReportProvider, "example/p", "x", ext.KindNotFound, "gone")
	invalid := ext.Errorf(ext.PointReportProvider, "example/p", "x", ext.KindInvalid, "bad\nregime\x00")
	for name, c := range map[string]struct {
		err      error
		generate bool
		want     Kind
	}{
		"a sentinel without an *ext.Error":     {fmt.Errorf("wrapped: %w", ext.ErrNotFound), true, KindFailed},
		"not found from Generate":              {notFound, true, KindNotFound},
		"not found from Reports":               {notFound, false, KindFailed},
		"invalid from Reports":                 {invalid, false, KindFailed},
		"unavailable from Reports":             {ext.Errorf(ext.PointReportProvider, "", "x", ext.KindUnavailable, "later"), false, KindUnavailable},
		"denied is a fault, never a denial":    {ext.Errorf(ext.PointReportProvider, "", "x", ext.KindDenied, "no"), true, KindFailed},
		"conflict, disabled and malformed too": {ext.Errorf(ext.PointReportProvider, "", "x", ext.KindDisabled, "off"), true, KindFailed},
	} {
		if got := classify("example/p", "review", "op", c.err, c.generate).Kind; got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
	if got := classify("example/p", "review", "op", invalid, true).Message; got != "bad regime" {
		t.Errorf("the provider's message = %q, want its own words on one line without the sentinel", got)
	}
	long := ext.Errorf(ext.PointReportProvider, "", "x", ext.KindInvalid, "%s", strings.Repeat("é", maxProviderMessage))
	if got := classify("p", "r", "op", long, true).Message; len(got) > maxProviderMessage || !strings.HasPrefix(got, "é") ||
		strings.ContainsRune(got, '�') {
		t.Errorf("a long message is %d bytes, or was cut mid-rune", len(got))
	}
}

func TestACallAbandonedAtTheBoundKeepsItsPlaceUntilTheProviderReturns(t *testing.T) {
	release := make(chan struct{})
	returned := make(chan struct{})
	closed := make(chan struct{})
	var calls atomic.Int32
	s := service(t, 1, provider{generate: func(_ context.Context, req ext.ReportRequest) (ext.Report, error) {
		if calls.Add(1) > 1 {
			return ext.Report{Format: req.Format, ContentType: "application/json", Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
		<-release // ignores its context
		defer close(returned)
		return ext.Report{Format: req.Format, ContentType: "application/json", Body: closer{closed}}, nil
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Generate(ctx, "t", valid); kindOf(t, err) != KindUnavailable {
		t.Errorf("a provider past the bound = %v, want KindUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Generate waited %s for a provider ignoring its context", elapsed)
	}
	if _, err := s.Generate(t.Context(), "t", valid); kindOf(t, err) != KindBusy {
		t.Errorf("with the abandoned call still running, the next = %v, want KindBusy", err)
	}
	close(release)
	<-returned
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned report's body was never closed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		g, err := s.Generate(t.Context(), "t", valid)
		if err == nil {
			_ = g.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the abandoned call returned, the next is still %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type closer struct{ closed chan struct{} }

func (closer) Read([]byte) (int, error) { return 0, io.EOF }
func (c closer) Close() error           { close(c.closed); return nil }

func TestABodyReleasesItsPlaceOnceWhenClosed(t *testing.T) {
	s := service(t, 1, provider{})
	g, err := s.Generate(t.Context(), "t", valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Generate(t.Context(), "t", valid); kindOf(t, err) != KindBusy {
		t.Errorf("before the body is closed, the next = %v, want KindBusy", err)
	}
	_ = g.Body.Close()
	_ = g.Body.Close() // a second Close releases nothing more
	g2, err := s.Generate(t.Context(), "t", valid)
	if err != nil {
		t.Fatalf("after Close, the next = %v", err)
	}
	if _, err := s.Generate(t.Context(), "t", valid); kindOf(t, err) != KindBusy {
		t.Errorf("a double Close released two places: the ceiling of 1 admitted a second generation (%v)", err)
	}
	_ = g2.Body.Close()
}

func TestARefusedGenerationReleasesItsPlace(t *testing.T) {
	s := service(t, 1, provider{})
	bad := valid
	bad.Format = "pdf"
	for range 3 {
		if _, err := s.Generate(t.Context(), "t", bad); kindOf(t, err) != KindInvalid {
			t.Fatalf("a format not listed = %v, want KindInvalid", err)
		}
	}
	if g, err := s.Generate(t.Context(), "t", valid); err != nil {
		t.Errorf("after three refusals, a valid generation = %v; a refusal kept its place", err)
	} else {
		_ = g.Body.Close()
	}
}
