// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package report

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hoplock/control/ext"
)

// DefaultMaxConcurrent is how many generations one node runs at once when the
// configuration says nothing (`north.max_concurrent_reports`).
const DefaultMaxConcurrent = 2

// maxProviderMessage bounds the provider's own words in a refusal Control
// answers on its behalf: they go into an error body, so they are one line and
// finite.
const maxProviderMessage = 512

// The problems Control's own checks name. They are the surface's vocabulary,
// the same strings internal/access names, so a client reads one grammar for
// every malformed field (M21).
const (
	// ProblemRequired is a field that must be present and is not.
	ProblemRequired = "required"
	// ProblemInvalid is a value of the wrong shape, or one the descriptor
	// does not offer.
	ProblemInvalid = "invalid"
	// ProblemNotAfterStart is a period whose end is at or before its start.
	ProblemNotAfterStart = "not_after_start"
)

// Kind classifies a failure for the surface that answers it. It is a closed
// set (M13), and its zero value is a provider's fault: the unclassified
// direction is an outage, never an answer (M11).
type Kind int

const (
	// KindFailed is a provider that broke its side of the seam: an error of
	// any kind but the three below, an error that is not an *ext.Error, a
	// panic, a descriptor Control cannot serve, or a Report that is not
	// what was asked for. It is never a denial: a provider does not decide
	// who may read a report, so its ErrDenied is a fault too.
	KindFailed Kind = iota
	// KindUnavailable is a provider that could not answer now: its
	// ErrUnavailable, or one that had not answered when the bound ran out.
	KindUnavailable
	// KindNotFound is a provider not registered, a report it does not list
	// for this tenant, or Generate's own ErrNotFound.
	KindNotFound
	// KindInvalid is a request Control's check against the descriptor
	// refused (Field and Problem name it), or Generate's own ErrInvalid
	// (Message carries the provider's words).
	KindInvalid
	// KindBusy is a generation past this node's ceiling. Neither provider
	// method was called for it.
	KindBusy
)

// String renders the kind for logs.
func (k Kind) String() string {
	switch k {
	case KindFailed:
		return "failed"
	case KindUnavailable:
		return "unavailable"
	case KindNotFound:
		return "not_found"
	case KindInvalid:
		return "invalid"
	case KindBusy:
		return "busy"
	}
	return "failed"
}

// Error is a classified failure of a listing entry or a generation.
type Error struct {
	// Kind is what the surface answers.
	Kind Kind
	// Provider and Report name what was asked for, as the caller named
	// them; Report is empty for a failure of the provider as a whole.
	Provider string
	Report   string
	// Field and Problem name what Control's own check refused. Field is
	// empty on a KindInvalid that is the provider's refusal.
	Field   string
	Problem string
	// Message is the provider's own words on its KindInvalid: one line,
	// bounded. Empty otherwise.
	Message string
	// Err is the cause, for the log line. It is never disclosed.
	Err error
}

// Error implements error.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "report: %s", e.Kind)
	if e.Provider != "" {
		fmt.Fprintf(&b, " [%s", e.Provider)
		if e.Report != "" {
			fmt.Fprintf(&b, " %s", e.Report)
		}
		b.WriteString("]")
	}
	if e.Field != "" {
		fmt.Fprintf(&b, ": %s is %s", e.Field, e.Problem)
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

// Unwrap exposes the cause.
func (e *Error) Unwrap() error { return e.Err }

// Options configures a Service.
type Options struct {
	// MaxConcurrent caps the generations running at once on this node. Zero
	// takes DefaultMaxConcurrent; a negative is refused.
	MaxConcurrent int
}

// Service lists and generates every registered report pack. It is safe for
// concurrent use.
type Service struct {
	providers []ext.Bound[ext.ReportProvider]
	byName    map[string]int
	slots     chan struct{}
}

// New builds the service over the registered providers, in registration
// order.
func New(providers []ext.Bound[ext.ReportProvider], o Options) (*Service, error) {
	if o.MaxConcurrent < 0 {
		return nil, fmt.Errorf("report: the generation ceiling must be at least 1, got %d", o.MaxConcurrent)
	}
	if o.MaxConcurrent == 0 {
		o.MaxConcurrent = DefaultMaxConcurrent
	}
	s := &Service{
		providers: slices.Clone(providers),
		byName:    make(map[string]int, len(providers)),
		slots:     make(chan struct{}, o.MaxConcurrent),
	}
	for i, p := range s.providers {
		if p.Impl == nil {
			return nil, fmt.Errorf("report: provider %q has no implementation", p.Registration.Provider)
		}
		s.byName[p.Registration.Provider] = i
	}
	return s, nil
}

// MaxConcurrent is this node's generation ceiling.
func (s *Service) MaxConcurrent() int { return cap(s.slots) }

// Entry is one registered provider in a listing.
type Entry struct {
	// Provider and Version are the provider's registration.
	Provider string
	Version  string
	// Reports are what it offers the tenant, in its own order. Empty when
	// Failure is set.
	Reports []ext.ReportDescriptor
	// Failure is why the provider is listed without its reports:
	// KindUnavailable or KindFailed. Nil when it answered.
	Failure *Error
}

// List is every registered provider's offer for the tenant, one entry each, in
// registration order. It always answers: a provider whose Reports failed, whose
// descriptors cannot be served, or that has not answered when ctx ends is its
// own entry's Failure, and the rest are listed intact. ctx carries the bound;
// the providers are asked concurrently.
func (s *Service) List(ctx context.Context, tenant ext.Tenant) []Entry {
	entries := make([]Entry, len(s.providers))
	type answer struct {
		i   int
		ds  []ext.ReportDescriptor
		err *Error
	}
	answers := make(chan answer, len(s.providers))
	answered := make([]bool, len(s.providers))
	for i, p := range s.providers {
		entries[i] = Entry{Provider: p.Registration.Provider, Version: p.Registration.Version}
		go func() {
			ds, err := s.descriptors(ctx, i, tenant)
			answers <- answer{i: i, ds: ds, err: err}
		}()
	}
	for pending := len(s.providers); pending > 0; pending-- {
		select {
		case a := <-answers:
			answered[a.i] = true
			if a.err != nil {
				entries[a.i].Failure = a.err
				continue
			}
			entries[a.i].Reports = a.ds
		case <-ctx.Done():
			for i := range entries {
				if !answered[i] {
					entries[i].Failure = &Error{
						Kind: KindUnavailable, Provider: entries[i].Provider,
						Err: fmt.Errorf("ReportProvider.Reports had not answered when the bound ran out: %w", ctx.Err()),
					}
				}
			}
			return entries
		}
	}
	return entries
}

// descriptors asks provider i what it offers the tenant, and refuses an offer
// Control cannot serve. It runs the call on the caller's goroutine: List and
// generate each bound it from their own.
func (s *Service) descriptors(ctx context.Context, i int, tenant ext.Tenant) ([]ext.ReportDescriptor, *Error) {
	p := s.providers[i]
	name := p.Registration.Provider
	ds, err := safeCall(func() ([]ext.ReportDescriptor, error) { return p.Impl.Reports(ctx, tenant) })
	if err != nil {
		return nil, classify(name, "", "ReportProvider.Reports", err, false)
	}
	if err := servable(ds); err != nil {
		return nil, &Error{Kind: KindFailed, Provider: name, Err: err}
	}
	return cloneDescriptors(ds), nil
}

// servable refuses a provider's offer that Control cannot serve. One bad
// descriptor refuses them all: a provider that lists nonsense for one report is
// not one whose other descriptors can be trusted to mean what they say.
func servable(ds []ext.ReportDescriptor) error {
	seen := make(map[string]bool, len(ds))
	for i, d := range ds {
		switch {
		case d.ReportID == "":
			return fmt.Errorf("descriptor %d has an empty ReportID", i)
		case seen[d.ReportID]:
			return fmt.Errorf("ReportID %q is listed twice", d.ReportID)
		case len(d.Formats) == 0:
			return fmt.Errorf("ReportID %q lists no format", d.ReportID)
		}
		seen[d.ReportID] = true
		for _, f := range d.Formats {
			// Checked explicitly: ReportFormat.String renders an unknown
			// value as "json", so an unchecked one would be served as
			// JSON.
			if !Known(f) {
				return fmt.Errorf("ReportID %q lists format %d, which is not a ReportFormat", d.ReportID, int(f))
			}
		}
		names := make(map[string]bool, len(d.Parameters))
		for _, p := range d.Parameters {
			switch {
			case p.Name == "":
				return fmt.Errorf("ReportID %q has a parameter with no name", d.ReportID)
			case names[p.Name]:
				return fmt.Errorf("ReportID %q names parameter %q twice", d.ReportID, p.Name)
			}
			names[p.Name] = true
		}
	}
	return nil
}

func cloneDescriptors(ds []ext.ReportDescriptor) []ext.ReportDescriptor {
	out := make([]ext.ReportDescriptor, len(ds))
	for i, d := range ds {
		out[i] = ext.ReportDescriptor{ReportID: d.ReportID, Formats: slices.Clone(d.Formats)}
		for _, p := range d.Parameters {
			out[i].Parameters = append(out[i].Parameters, ext.ReportParameter{
				Name: p.Name, Required: p.Required, Values: slices.Clone(p.Values),
			})
		}
	}
	return out
}

// Request asks for one report. Every field is as the caller sent it: Control's
// checks run on these, in Generate, after the descriptor is found.
type Request struct {
	// Provider and Report name the report, as the listing names it.
	Provider string
	Report   string
	// From and To are RFC 3339: From inclusive, To exclusive.
	From string
	To   string
	// Parameters are the report's own inputs.
	Parameters map[string]string
	// Format is the wire code: json, csv or pdf.
	Format string
}

// Generated is a report Control will serve. Body is the provider's, wrapped:
// closing it closes the provider's and releases this generation's place under
// the ceiling, and a panic inside the provider's Read or Close is an error.
// The caller closes it, once it has read what it will.
type Generated struct {
	// Provider is the registration it came from.
	Provider string
	// ReportID is the report asked for.
	ReportID string
	// Format is the format asked for, which the provider's Report agreed
	// with.
	Format ext.ReportFormat
	// From and To are the period, parsed.
	From time.Time
	To   time.Time
	// Body is the report.
	Body io.ReadCloser
}

// Generate produces one report for the tenant, in this order: the request
// names a provider and a report; a place under the ceiling (KindBusy without
// one, before either provider call); the provider is registered and lists the
// report for this tenant (KindNotFound); the format, the parameters and the
// period hold against the descriptor (KindInvalid naming the field); then
// Generate, whose Report must be the format asked for in that format's media
// type (KindFailed otherwise, its body closed unread). ctx carries the bound:
// the provider calls run on their own goroutine, so one that ignores ctx is
// abandoned at the bound as KindUnavailable, and keeps its place under the
// ceiling until it returns.
func (s *Service) Generate(ctx context.Context, tenant ext.Tenant, req Request) (*Generated, error) {
	switch {
	case req.Provider == "":
		return nil, &Error{Kind: KindInvalid, Field: "provider", Problem: ProblemRequired}
	case req.Report == "":
		return nil, &Error{Kind: KindInvalid, Provider: req.Provider, Field: "report", Problem: ProblemRequired}
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, &Error{Kind: KindBusy, Provider: req.Provider, Report: req.Report}
	}
	var once sync.Once
	release := func() { once.Do(func() { <-s.slots }) }

	type outcome struct {
		g   *Generated
		err error
	}
	answered := make(chan outcome)
	abandoned := make(chan struct{})
	go func() {
		g, err := s.generate(ctx, tenant, req, release)
		select {
		case answered <- outcome{g: g, err: err}:
		case <-abandoned:
			// Nobody will serve it. Closing the body releases the place;
			// a refusal released it already.
			if g != nil {
				_ = g.Body.Close()
			}
		}
	}()
	select {
	case o := <-answered:
		return o.g, o.err
	case <-ctx.Done():
		close(abandoned)
		return nil, &Error{
			Kind: KindUnavailable, Provider: req.Provider, Report: req.Report,
			Err: fmt.Errorf("the provider had not produced the report when the bound ran out: %w", ctx.Err()),
		}
	}
}

// generate is Generate's provider-facing half, on its own goroutine. Every
// path that does not hand a body back calls release before it returns.
func (s *Service) generate(ctx context.Context, tenant ext.Tenant, req Request, release func()) (*Generated, error) {
	refuse := func(err *Error) (*Generated, error) {
		release()
		return nil, err
	}
	i, ok := s.byName[req.Provider]
	if !ok {
		return refuse(&Error{Kind: KindNotFound, Provider: req.Provider, Report: req.Report,
			Err: errors.New("no provider is registered under that name")})
	}
	ds, failure := s.descriptors(ctx, i, tenant)
	if failure != nil {
		failure.Report = req.Report
		return refuse(failure)
	}
	idx := slices.IndexFunc(ds, func(d ext.ReportDescriptor) bool { return d.ReportID == req.Report })
	if idx < 0 {
		return refuse(&Error{Kind: KindNotFound, Provider: req.Provider, Report: req.Report,
			Err: errors.New("the provider does not list that report for this tenant")})
	}
	checked, invalid := check(ds[idx], req)
	if invalid != nil {
		invalid.Provider, invalid.Report = req.Provider, req.Report
		return refuse(invalid)
	}
	checked.Tenant = tenant

	p := s.providers[i]
	rep, err := safeCall(func() (ext.Report, error) { return p.Impl.Generate(ctx, checked) })
	if err != nil {
		if rep.Body != nil {
			_ = safeClose(rep.Body)
		}
		return refuse(classify(req.Provider, req.Report, "ReportProvider.Generate", err, true))
	}
	if problem := agrees(rep, checked.Format); problem != nil {
		if rep.Body != nil {
			_ = safeClose(rep.Body)
		}
		return refuse(&Error{Kind: KindFailed, Provider: req.Provider, Report: req.Report, Err: problem})
	}
	return &Generated{
		Provider: req.Provider, ReportID: req.Report, Format: checked.Format,
		From: checked.From, To: checked.To,
		Body: &body{rc: rep.Body, release: release},
	}, nil
}

// BodyError classifies an error a generated report's Body returned before its
// first byte, as Generate's own error would be: the status is not written yet,
// so it is answered like any other refusal.
func BodyError(g *Generated, err error) *Error {
	return classify(g.Provider, g.ReportID, "Report.Body.Read", err, true)
}

// check holds a request against the descriptor the provider listed, and
// builds what Generate is handed. The order is the format, the parameters
// (names the descriptor does not know, sorted; then the required ones, in the
// descriptor's order; then closed sets, sorted), and the period.
func check(d ext.ReportDescriptor, req Request) (ext.ReportRequest, *Error) {
	out := ext.ReportRequest{ReportID: d.ReportID}
	if req.Format == "" {
		return out, &Error{Kind: KindInvalid, Field: "format", Problem: ProblemRequired}
	}
	f, ok := ParseFormat(req.Format)
	if !ok || !slices.Contains(d.Formats, f) {
		return out, &Error{Kind: KindInvalid, Field: "format", Problem: ProblemInvalid}
	}
	out.Format = f

	byName := make(map[string]ext.ReportParameter, len(d.Parameters))
	for _, p := range d.Parameters {
		byName[p.Name] = p
	}
	names := make([]string, 0, len(req.Parameters))
	for name := range req.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, known := byName[name]; !known {
			return out, &Error{Kind: KindInvalid, Field: "parameters." + name, Problem: ProblemInvalid}
		}
	}
	for _, p := range d.Parameters {
		if _, present := req.Parameters[p.Name]; p.Required && !present {
			return out, &Error{Kind: KindInvalid, Field: "parameters." + p.Name, Problem: ProblemRequired}
		}
	}
	for _, name := range names {
		if p := byName[name]; len(p.Values) > 0 && !slices.Contains(p.Values, req.Parameters[name]) {
			return out, &Error{Kind: KindInvalid, Field: "parameters." + name, Problem: ProblemInvalid}
		}
	}
	if len(req.Parameters) > 0 {
		out.Parameters = make(map[string]string, len(req.Parameters))
		for k, v := range req.Parameters {
			out.Parameters[k] = v
		}
	}

	var invalid *Error
	if out.From, invalid = instant("from", req.From); invalid != nil {
		return out, invalid
	}
	if out.To, invalid = instant("to", req.To); invalid != nil {
		return out, invalid
	}
	if !out.From.Before(out.To) {
		return out, &Error{Kind: KindInvalid, Field: "to", Problem: ProblemNotAfterStart}
	}
	return out, nil
}

// instant reads one end of the period: required, RFC 3339.
func instant(field, raw string) (time.Time, *Error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, &Error{Kind: KindInvalid, Field: field, Problem: ProblemRequired}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, &Error{Kind: KindInvalid, Field: field, Problem: ProblemInvalid}
	}
	return t, nil
}

// agrees refuses a Report that is not what was asked for: another format, a
// media type that is not the format's, a CSV in a charset other than UTF-8, or
// no body.
func agrees(rep ext.Report, want ext.ReportFormat) error {
	if rep.Body == nil {
		return errors.New("the Report has no Body")
	}
	if rep.Format != want {
		return fmt.Errorf("the Report's Format is %d, and %s was asked for", int(rep.Format), want)
	}
	mt, params, err := mime.ParseMediaType(rep.ContentType)
	if err != nil {
		return fmt.Errorf("the Report's ContentType %q does not parse: %w", rep.ContentType, err)
	}
	if base := baseMediaType(want); mt != base {
		return fmt.Errorf("the Report's ContentType is %q, and a %s report is %s", mt, want, base)
	}
	if cs, ok := params["charset"]; ok && want == ext.ReportFormatCSV && !strings.EqualFold(cs, "utf-8") {
		return fmt.Errorf("the Report's CSV is in charset %q, and Control serves CSV as UTF-8", cs)
	}
	return nil
}

// classify maps a provider's error onto a Kind. Only an *ext.Error carries a
// kind; anything else is a fault. ErrNotFound and ErrInvalid mean what they say
// only from Generate: from Reports, the only answers are a list, "not now", or
// a fault.
func classify(provider, report, op string, err error, generate bool) *Error {
	out := &Error{Kind: KindFailed, Provider: provider, Report: report, Err: fmt.Errorf("%s: %w", op, err)}
	var e *ext.Error
	if !errors.As(err, &e) {
		return out
	}
	switch e.Kind {
	case ext.KindUnavailable:
		out.Kind = KindUnavailable
	case ext.KindNotFound:
		if generate {
			out.Kind = KindNotFound
		}
	case ext.KindInvalid:
		if generate {
			out.Kind = KindInvalid
			out.Message = providerMessage(e)
		}
	case ext.KindInternal, ext.KindConflict, ext.KindDisabled, ext.KindMalformed, ext.KindDenied:
		// A provider never decides who may read a report: audit:read and
		// the tenant the middleware resolved are the whole of that (§6).
		// So its ErrDenied is a fault, like every other kind here.
	}
	return out
}

// providerMessage is the provider's own words on an *ext.Error, without the
// sentinel ext.Errorf put in front of them: one line, printable, bounded.
func providerMessage(e *ext.Error) string {
	if e.Err == nil {
		return ""
	}
	msg := e.Err.Error()
	if sentinel := ext.ErrInvalid.Error() + ": "; strings.HasPrefix(msg, sentinel) {
		msg = msg[len(sentinel):]
	}
	msg = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, msg)
	msg = strings.TrimSpace(msg)
	if len(msg) > maxProviderMessage {
		cut := maxProviderMessage
		for cut > 0 && !utf8.RuneStart(msg[cut]) {
			cut--
		}
		msg = msg[:cut]
	}
	return msg
}

// ParseFormat reads a format's wire code.
func ParseFormat(code string) (ext.ReportFormat, bool) {
	switch code {
	case "json":
		return ext.ReportFormatJSON, true
	case "csv":
		return ext.ReportFormatCSV, true
	case "pdf":
		return ext.ReportFormatPDF, true
	}
	return 0, false
}

// Known reports whether f is one of the three ReportFormat constants.
func Known(f ext.ReportFormat) bool {
	switch f {
	case ext.ReportFormatJSON, ext.ReportFormatCSV, ext.ReportFormatPDF:
		return true
	}
	return false
}

// MediaType is the media type Control serves a format as, whatever the
// provider's ContentType said: application/json, text/csv; charset=utf-8, or
// application/pdf.
func MediaType(f ext.ReportFormat) string {
	switch f {
	case ext.ReportFormatJSON:
		return "application/json"
	case ext.ReportFormatCSV:
		return "text/csv; charset=utf-8"
	case ext.ReportFormatPDF:
		return "application/pdf"
	}
	return "application/octet-stream"
}

// baseMediaType is MediaType without its parameters.
func baseMediaType(f ext.ReportFormat) string {
	mt, _, _ := strings.Cut(MediaType(f), ";")
	return mt
}

// safeCall runs a provider method, turning a panic into an error: a provider is
// somebody else's code running in this process, and its panic is its fault,
// never a crash of the decision path beside it.
func safeCall[T any](fn func() (T, error)) (v T, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			var zero T
			v, err = zero, fmt.Errorf("the provider panicked: %v", rec)
		}
	}()
	return fn()
}

// safeClose closes a provider's body, turning a panic into an error.
func safeClose(c io.Closer) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("the provider's Body.Close panicked: %v", rec)
		}
	}()
	return c.Close()
}

// body is a provider's report body as Control serves it.
//
// Close may be called while a Read is in progress — the north-bound handler
// does that when the bound runs out or the client goes away, so that a Read
// blocked in the provider is asked to return — and only the first Close
// reaches the provider. The place under the ceiling is released after the
// provider's Close returns.
type body struct {
	rc      io.ReadCloser
	release func()
	once    sync.Once
	err     error
}

func (b *body) Read(p []byte) (n int, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			n, err = 0, fmt.Errorf("the provider's Body.Read panicked: %v", rec)
		}
	}()
	return b.rc.Read(p)
}

func (b *body) Close() error {
	b.once.Do(func() {
		b.err = safeClose(b.rc)
		b.release()
	})
	return b.err
}
