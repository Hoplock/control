// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/report"
)

// The registered report packs (0044, PLAN §7 "Reports").
//
// Two routes, both audit:read: a report is the audit store, summarised, and
// who may read it is decided by the middleware alone — the permission and the
// tenant it resolved (§6). A provider is never asked, so nothing it answers is
// a 403, and nothing on this surface is a 401 (M11).
//
// The provider and the report are named in the BODY, never the path:
// Registration.Provider is any non-blank string, by convention a module path
// carrying `/`, and a `%2F` in a segment is decoded by ingresses and WAFs
// before this server sees it. The listing names each report by the same two
// strings, so a console posts back exactly what it was handed.
//
// The generation is THE ONE WRITER in this package besides writeJSON. A report
// body is bytes somebody else's code wrote, served on the listener the console
// runs on to a browser holding the operator's session cookie, so it is served
// as the media type of the format asked for — never the provider's — with
// `nosniff` and as an attachment. Every error, this route's included, goes
// through writeJSON.

// reportChunk is how much of a body is read, and written, at a time.
const reportChunk = 32 << 10

// reportBusyRetryAfter is the Retry-After, in seconds, a generation past the
// ceiling is answered with. A slot frees when a running report finishes, which
// nothing here can predict; this is a pace for a client's retry, not a promise.
const reportBusyRetryAfter = 10

// maxReportLogField bounds a caller-chosen name written to the access log.
const maxReportLogField = 256

// reportListing is `GET …/reports`: one entry per registered provider, in
// registration order. It is exempt from M26's paging, because its size is set
// by the binary's registrations and each provider's catalogue, never by the
// estate.
type reportListing struct {
	Providers []reportProviderView `json:"providers"`
}

type reportProviderView struct {
	Provider string       `json:"provider"`
	Version  string       `json:"version"`
	Reports  []reportView `json:"reports"`
	// Error is report_provider_unavailable or report_provider_failed, with
	// Reports empty; absent when the provider answered.
	Error string `json:"error,omitempty"`
}

type reportView struct {
	Report     string                `json:"report"`
	Formats    []string              `json:"formats"`
	Parameters []reportParameterView `json:"parameters"`
}

type reportParameterView struct {
	Name     string   `json:"name"`
	Required bool     `json:"required"`
	Values   []string `json:"values"`
}

// generateBody is `POST …/reports/generate`. It carries no tenant: the tenant
// is the one the middleware resolved (M18), and a body naming one is refused
// as an unknown field.
type generateBody struct {
	Provider   string            `json:"provider"`
	Report     string            `json:"report"`
	From       string            `json:"from"`
	To         string            `json:"to"`
	Parameters map[string]string `json:"parameters"`
	Format     string            `json:"format"`
}

// handleReports lists every registered provider's reports for the tenant. It
// answers 200 whenever Control itself can: a provider that failed, that offered
// a descriptor Control cannot serve, or that had not answered within the
// request's bound is listed with its error and no reports, beside the rest.
func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	entries := s.reports.List(withProviderCaller(r.Context()), ext.Tenant(tenant))
	out := reportListing{Providers: make([]reportProviderView, 0, len(entries))}
	for _, e := range entries {
		v := reportProviderView{Provider: e.Provider, Version: e.Version, Reports: []reportView{}}
		if e.Failure != nil {
			v.Error = listingCode(e.Failure.Kind)
			s.logReportFailure(r, "list reports", e.Failure)
			out.Providers = append(out.Providers, v)
			continue
		}
		for _, d := range e.Reports {
			rv := reportView{Report: d.ReportID, Formats: make([]string, 0, len(d.Formats)),
				Parameters: make([]reportParameterView, 0, len(d.Parameters))}
			for _, f := range d.Formats {
				rv.Formats = append(rv.Formats, f.String())
			}
			for _, p := range d.Parameters {
				values := p.Values
				if values == nil {
					values = []string{}
				}
				rv.Parameters = append(rv.Parameters, reportParameterView{Name: p.Name, Required: p.Required, Values: values})
			}
			v.Reports = append(v.Reports, rv)
		}
		out.Providers = append(out.Providers, v)
	}
	answer(w, http.StatusOK, out)
}

// listingCode is a listing entry's error.
func listingCode(k report.Kind) string {
	switch k {
	case report.KindUnavailable:
		return CodeReportProviderUnavailable
	case report.KindFailed, report.KindNotFound, report.KindInvalid, report.KindBusy:
	}
	return CodeReportProviderFailed
}

// handleReportGenerate generates one registered report and streams it.
//
// Its route carries its own bound (north.report_timeout), from the start of
// the request to its last byte. The context the provider sees is the
// request's, so a client that disconnects cancels the generation.
func (s *Server) handleReportGenerate(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireTenant(w, r)
	if !ok {
		return
	}
	facts := &reportFacts{}
	if f := factsFrom(r.Context()); f != nil {
		f.report = facts
	}
	var body generateBody
	if err := decodeJSON(r, &body); err != nil {
		facts.outcome = CodeInvalidRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			facts.outcome = CodePayloadTooLarge
		}
		s.refuseBody(w, r, err)
		return
	}
	facts.provider, facts.report, facts.format = logField(body.Provider), logField(body.Report), logField(body.Format)
	facts.from, facts.to = logField(body.From), logField(body.To)

	g, err := s.reports.Generate(withProviderCaller(r.Context()), ext.Tenant(tenant), report.Request{
		Provider: body.Provider, Report: body.Report, From: body.From, To: body.To,
		Parameters: body.Parameters, Format: body.Format,
	})
	if err != nil {
		facts.outcome = s.reportFailure(w, r, err)
		return
	}
	s.streamReport(w, r, g, facts)
}

// reportRead is one Read of a report's body.
type reportRead struct {
	b   []byte
	err error
}

// streamReport writes a generated report, and never a short one (M11).
//
// The status is not written until the body has produced its first byte or
// ended: an error before that is answered in the envelope like any other, and
// a body that ends at once is an empty report. After the status, a body error,
// the bound running out or a client that stopped reading aborts the response
// with http.ErrAbortHandler, which withRecovery lets through untouched and
// net/http turns into a broken response. No Content-Length is set, and every
// chunk is flushed, so the report reaches the client as it is produced and is
// never buffered here.
//
// The body is read on a goroutine of its own, so a provider that blocks in
// Read past the bound cannot hold this handler: it is abandoned, and its body
// closed beside it.
func (s *Server) streamReport(w http.ResponseWriter, r *http.Request, g *report.Generated, facts *reportFacts) {
	ctx := r.Context()
	chunks := make(chan reportRead)
	stop := make(chan struct{})
	go readReport(g.Body, chunks, stop)

	// drained is true once the body has returned io.EOF or an error, so
	// nothing is reading it and it is closed here. Otherwise a Read may
	// still be blocked in the provider, and the body is closed beside it:
	// a Read blocked at that moment is asked to return (ext.Report's doc
	// comment), and this handler never waits on it.
	drained := false
	defer func() {
		close(stop)
		if drained {
			_ = g.Body.Close()
			return
		}
		go func() { _ = g.Body.Close() }()
	}()

	rc := http.NewResponseController(w)
	started := false
	for {
		var c reportRead
		select {
		case c = <-chunks:
		case <-ctx.Done():
			if !started {
				facts.outcome = s.reportFailure(w, r, &report.Error{
					Kind: report.KindUnavailable, Provider: g.Provider, Report: g.ReportID,
					Err: fmt.Errorf("no byte of the report had arrived when the bound ran out: %w", ctx.Err()),
				})
				return
			}
			s.abortReport(r, g, facts, fmt.Errorf("the bound ran out: %w", ctx.Err()))
		}
		if len(c.b) > 0 {
			if !started {
				s.startReport(w, r, rc, g)
				started = true
			}
			n, err := w.Write(c.b)
			facts.bytes += int64(n)
			if err == nil {
				err = rc.Flush()
			}
			if err != nil {
				s.abortReport(r, g, facts, fmt.Errorf("the client stopped reading: %w", err))
			}
		}
		if c.err == nil {
			continue
		}
		drained = true
		if c.err == io.EOF {
			if !started {
				// A body that ends at once is an empty report.
				s.startReport(w, r, rc, g)
			}
			// The write deadline is the request's; the connection may
			// carry another request after this one.
			_ = rc.SetWriteDeadline(time.Time{})
			facts.outcome = "complete"
			return
		}
		if !started {
			facts.outcome = s.reportFailure(w, r, report.BodyError(g, c.err))
			return
		}
		s.abortReport(r, g, facts, c.err)
	}
}

// readReport reads body into out until it ends, fails, or stop is closed. Two
// buffers suffice: the handler receives a chunk only after it has written the
// one before, so when a send completes, the other buffer is free again.
func readReport(body io.Reader, out chan<- reportRead, stop <-chan struct{}) {
	bufs := [2][]byte{make([]byte, reportChunk), make([]byte, reportChunk)}
	for k := 0; ; k ^= 1 {
		n, err := body.Read(bufs[k])
		select {
		case out <- reportRead{b: bufs[k][:n], err: err}:
		case <-stop:
			return
		}
		if err != nil {
			return
		}
	}
}

// startReport writes the status and the headers Control chooses: the format's
// media type, never the provider's; nosniff; an attachment whose filename is
// built from the report's id and period in [A-Za-z0-9._-] only; and no-store,
// because a report is the audit store, summarised. The write deadline is the
// request's own, so a client that stops reading cannot hold the response, or
// its place under the ceiling, past the bound.
func (s *Server) startReport(w http.ResponseWriter, r *http.Request, rc *http.ResponseController, g *report.Generated) {
	h := w.Header()
	h.Set("Content-Type", report.MediaType(g.Format))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", `attachment; filename="`+reportFilename(g)+`"`)
	h.Set("Cache-Control", "no-store")
	if deadline, ok := r.Context().Deadline(); ok {
		_ = rc.SetWriteDeadline(deadline)
	}
	w.WriteHeader(http.StatusOK)
}

// abortReport ends a response whose status is already written. It does not
// return.
func (s *Server) abortReport(r *http.Request, g *report.Generated, facts *reportFacts, cause error) {
	facts.outcome = "aborted"
	s.log.ErrorContext(r.Context(), "a report failed after its status was written; the response is aborted, never ended short",
		"event", "northbound_report_aborted",
		"provider", g.Provider,
		"report", g.ReportID,
		"bytes", facts.bytes,
		"path", r.URL.Path,
		"correlation_id", CorrelationIDFrom(r.Context()),
		"error", cause.Error(),
	)
	panic(http.ErrAbortHandler)
}

// reportFailure answers a classified report failure and returns its code, for
// the access log. Every code is a constant: see the table in PLAN §7.
func (s *Server) reportFailure(w http.ResponseWriter, r *http.Request, err error) string {
	var re *report.Error
	if !errors.As(err, &re) {
		s.fail(w, r, "generate report", err)
		return CodeInternal
	}
	id := CorrelationIDFrom(r.Context())
	params := map[string]any{}
	if re.Provider != "" {
		params["provider"] = re.Provider
	}
	if re.Report != "" {
		params["report"] = re.Report
	}
	switch re.Kind {
	case report.KindInvalid:
		if re.Field != "" {
			s.invalid(w, r, re.Field, re.Problem, "")
			return CodeInvalidRequest
		}
		msg := re.Message
		if msg == "" {
			msg = "the report provider refused this request"
		}
		writeError(w, http.StatusBadRequest, Error{
			Code: CodeInvalidRequest, CorrelationID: id, Message: msg, Parameters: params,
		})
		return CodeInvalidRequest
	case report.KindNotFound:
		writeError(w, http.StatusNotFound, Error{
			Code: CodeNotFound, CorrelationID: id, Parameters: params,
			Message: "no registered provider offers that report in this tenant",
		})
		return CodeNotFound
	case report.KindBusy:
		params["max_concurrent_reports"] = s.reports.MaxConcurrent()
		w.Header().Set("Retry-After", strconv.Itoa(reportBusyRetryAfter))
		writeError(w, http.StatusServiceUnavailable, Error{
			Code: CodeReportGenerationBusy, CorrelationID: id, Parameters: params,
			Message: "this server is already generating as many reports as it runs at once; ask again shortly",
		})
		return CodeReportGenerationBusy
	case report.KindUnavailable:
		s.logReportFailure(r, "generate report", re)
		writeError(w, http.StatusServiceUnavailable, Error{
			Code: CodeReportProviderUnavailable, CorrelationID: id, Parameters: params,
			Message: "the report provider could not produce this report now; ask again later",
		})
		return CodeReportProviderUnavailable
	case report.KindFailed:
	}
	s.logReportFailure(r, "generate report", re)
	writeError(w, http.StatusBadGateway, Error{
		Code: CodeReportProviderFailed, CorrelationID: id, Parameters: params,
		Message: "the report provider failed to produce this report; quote the correlation id",
	})
	return CodeReportProviderFailed
}

// logReportFailure writes down what the caller is told only as a code.
func (s *Server) logReportFailure(r *http.Request, op string, re *report.Error) {
	cause := ""
	if re.Err != nil {
		cause = re.Err.Error()
	}
	s.log.WarnContext(r.Context(), "a report provider could not serve the request",
		"event", "northbound_report_provider_failure",
		"op", op,
		"kind", re.Kind.String(),
		"provider", re.Provider,
		"report", re.Report,
		"path", r.URL.Path,
		"correlation_id", CorrelationIDFrom(r.Context()),
		"error", cause,
	)
}

// reportFilename is `<report>_<from>_<to>.<format>`, every character in
// [A-Za-z0-9._-]: a ReportID is unconstrained, and a `"`, a `/`, a space or a
// newline in a header parameter is a header nobody meant to write.
func reportFilename(g *report.Generated) string {
	const stamp = "20060102T150405Z"
	return filenamePart(g.ReportID) + "_" + g.From.UTC().Format(stamp) + "_" + g.To.UTC().Format(stamp) +
		"." + g.Format.String()
}

// filenamePart keeps [A-Za-z0-9._-], replaces every other character with `_`,
// and keeps at most 128 of them.
func filenamePart(s string) string {
	const maxRunes = 128
	var b strings.Builder
	n := 0
	for _, c := range s {
		if n == maxRunes {
			break
		}
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
		n++
	}
	return b.String()
}

// logField bounds a caller-chosen value for the access log.
func logField(v string) string {
	if len(v) <= maxReportLogField {
		return v
	}
	return strings.ToValidUTF8(v[:maxReportLogField], "")
}
