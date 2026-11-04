// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package north_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/access"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/httpapi/north"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/report"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The registered report packs over the wire (0044). Every test shares one fake
// provider, packs, whose behaviour is set before the server starts and whose
// calls are counted.

// accessReview is the descriptor most tests serve.
var accessReview = ext.ReportDescriptor{
	ReportID: "access-review",
	Formats:  []ext.ReportFormat{ext.ReportFormatJSON, ext.ReportFormatCSV},
	Parameters: []ext.ReportParameter{
		{Name: "regime", Required: true, Values: []string{"sox", "pci"}},
		{Name: "label"},
	},
}

// packs is the shared fake ext.ReportProvider.
type packs struct {
	// offers is what Reports lists, per tenant.
	offers map[ext.Tenant][]ext.ReportDescriptor
	// reportsErr fails Reports.
	reportsErr error
	// hang makes Reports block, ignoring its context, until it is closed.
	hang chan struct{}
	// generate answers Generate; nil serves `{"ok":true}` as JSON.
	generate func(ctx context.Context, req ext.ReportRequest) (ext.Report, error)

	mu              sync.Mutex
	reportsCalls    int
	generateCalls   int
	reportsCallers  []north.HostCaller
	generateCallers []north.HostCaller
	last            ext.ReportRequest
}

func (p *packs) Reports(ctx context.Context, tenant ext.Tenant) ([]ext.ReportDescriptor, error) {
	caller, _ := north.HostCallerFrom(ctx)
	p.mu.Lock()
	p.reportsCalls++
	p.reportsCallers = append(p.reportsCallers, caller)
	p.mu.Unlock()
	if p.hang != nil {
		<-p.hang
	}
	if p.reportsErr != nil {
		return nil, p.reportsErr
	}
	return p.offers[tenant], nil
}

func (p *packs) Generate(ctx context.Context, req ext.ReportRequest) (ext.Report, error) {
	caller, _ := north.HostCallerFrom(ctx)
	p.mu.Lock()
	p.generateCalls++
	p.generateCallers = append(p.generateCallers, caller)
	p.last = req
	p.mu.Unlock()
	if p.generate != nil {
		return p.generate(ctx, req)
	}
	return ext.Report{ReportID: req.ReportID, Format: req.Format, ContentType: "application/json",
		Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
}

func (p *packs) calls() (reports, generate int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reportsCalls, p.generateCalls
}

// everywhere offers d in tenant-a and tenant-b.
func everywhere(d ...ext.ReportDescriptor) map[ext.Tenant][]ext.ReportDescriptor {
	return map[ext.Tenant][]ext.ReportDescriptor{"tenant-a": d, "tenant-b": d}
}

// tracked is a report body that records its Close.
type tracked struct {
	io.Reader
	closed atomic.Bool
}

func (b *tracked) Close() error { b.closed.Store(true); return nil }

// gated is a body that serves first, then blocks until gate is closed (or ctx
// ends, when ctx is set), then serves rest — or fails with err.
type gated struct {
	first, rest []byte
	gate        chan struct{}
	ctx         context.Context
	err         error
	state       int
	closed      atomic.Bool
}

func (b *gated) Read(p []byte) (int, error) {
	switch b.state {
	case 0:
		b.state = 1
		return copy(p, b.first), nil
	case 1:
		if b.ctx != nil {
			select {
			case <-b.gate:
			case <-b.ctx.Done():
				return 0, b.ctx.Err()
			}
		} else {
			<-b.gate
		}
		b.state = 2
		if b.err != nil {
			return 0, b.err
		}
		return copy(p, b.rest), nil
	}
	return 0, io.EOF
}

func (b *gated) Close() error { b.closed.Store(true); return nil }

type reportReg struct {
	name, version string
	impl          ext.ReportProvider
}

type reportConfig struct {
	requestTimeout time.Duration
	reportTimeout  time.Duration
	maxConcurrent  int
}

// slowHost is the host route that waits for its request's deadline, to show
// another route keeps the listener's bound.
const slowHost = "example/slow"

type reportFixture struct {
	*serverFixture
	logs *syncBuffer
	// slowElapsed receives how long the slow host route waited.
	slowElapsed chan time.Duration
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lines returns every logged line carrying event.
func (b *syncBuffer) lines(event string) []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) == nil && line["event"] == event {
			out = append(out, line)
		}
	}
	return out
}

func newReportFixture(t *testing.T, c reportConfig, regs ...reportReg) *reportFixture {
	t.Helper()
	st := storetest.New(t)
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatalf("federation: %v", err)
	}
	reg := ext.NewRegistry()
	for _, r := range regs {
		if err := reg.RegisterReportProvider(ext.Registration{Provider: r.name, Version: r.version}, r.impl); err != nil {
			t.Fatalf("register %s: %v", r.name, err)
		}
	}
	if err := extdefault.Register(reg, extdefault.Deps{}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	extensions, err := reg.Seal()
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	reports, err := report.New(extensions.ReportProviders(), report.Options{MaxConcurrent: c.maxConcurrent})
	if err != nil {
		t.Fatalf("reports: %v", err)
	}
	logs := &syncBuffer{}
	slow := make(chan time.Duration, 4)
	server, err := north.New(withLifecycle(t, st, north.Options{
		Federation: federation, Grants: newGrants(t, st, access.Options{}),
		Extensions: extensions, Reports: reports,
		RequestTimeout: c.requestTimeout, ReportTimeout: c.reportTimeout,
		Logger:        slog.New(slog.NewJSONHandler(logs, nil)),
		DefaultTenant: "tenant-a", InsecureCookies: true,
		HostProvider: slowHost,
		HostRoutes: []north.HostRoute{{
			Method: http.MethodGet, Pattern: "slow", Access: north.AccessTenant, Permission: identity.PermAuditRead,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start := time.Now()
				<-r.Context().Done()
				slow <- time.Since(start)
				w.WriteHeader(http.StatusNoContent)
			}),
		}},
	}))
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	f := &serverFixture{st: st, federation: federation, server: server}
	f.http = httptest.NewServer(server)
	t.Cleanup(f.http.Close)
	return &reportFixture{serverFixture: f, logs: logs, slowElapsed: slow}
}

func reportsPath(tenant string) string { return "/api/v1/tenants/" + tenant + "/reports" }
func generatePath(tenant string) string {
	return "/api/v1/tenants/" + tenant + "/reports/generate"
}

// generation is a valid request for accessReview, as a body.
func generation(provider string) map[string]any {
	return map[string]any{
		"provider": provider, "report": "access-review", "format": "csv",
		"from": "2026-01-01T00:00:00Z", "to": "2026-02-01T00:00:00Z",
		"parameters": map[string]string{"regime": "sox"},
	}
}

type listingBody struct {
	Providers []struct {
		Provider string `json:"provider"`
		Version  string `json:"version"`
		Error    string `json:"error"`
		Reports  []struct {
			Report     string   `json:"report"`
			Formats    []string `json:"formats"`
			Parameters []struct {
				Name     string   `json:"name"`
				Required bool     `json:"required"`
				Values   []string `json:"values"`
			} `json:"parameters"`
		} `json:"reports"`
	} `json:"providers"`
}

func (f *reportFixture) listing(t *testing.T, tenant, token string) listingBody {
	t.Helper()
	resp := f.send(t, http.MethodGet, reportsPath(tenant), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing = %d, want 200", resp.StatusCode)
	}
	var out listingBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("listing body: %v", err)
	}
	return out
}

func TestTheListingNamesEveryProviderInRegistrationOrderForTheTenantItResolved(t *testing.T) {
	sox := &packs{offers: everywhere(accessReview)}
	// pci licenses its pack to tenant-a only.
	pci := &packs{offers: map[ext.Tenant][]ext.ReportDescriptor{"tenant-a": {{
		ReportID: "pci-quarterly", Formats: []ext.ReportFormat{ext.ReportFormatPDF},
	}}}}
	f := newReportFixture(t, reportConfig{},
		reportReg{"example/sox", "1.0.0", sox}, reportReg{"example/pci", "2.1.0", pci})

	session, _ := f.session(t, "alice", "oidc-corp", false, identity.RoleAuditor)
	for name, token := range map[string]string{
		"an auditor session":      session,
		"an audit:read API token": f.roleToken(t, identity.RoleAuditor),
	} {
		got := f.listing(t, "tenant-a", token)
		raw, _ := json.Marshal(got)
		want := `{"providers":[` +
			`{"provider":"example/sox","version":"1.0.0","error":"","reports":[{"report":"access-review","formats":["json","csv"],` +
			`"parameters":[{"name":"regime","required":true,"values":["sox","pci"]},{"name":"label","required":false,"values":[]}]}]},` +
			`{"provider":"example/pci","version":"2.1.0","error":"","reports":[{"report":"pci-quarterly","formats":["pdf"],"parameters":[]}]}]}`
		if string(raw) != want {
			t.Errorf("%s: listing\n%s\nwant\n%s", name, raw, want)
		}
	}

	// tenant-b sees pci's entry, with nothing in it: listed, not licensed.
	b := f.token(t, "tenant-b", map[store.Tenant]identity.RoleSet{"tenant-b": {identity.RoleAuditor}})
	got := f.listing(t, "tenant-b", b)
	if len(got.Providers) != 2 || got.Providers[1].Provider != "example/pci" || len(got.Providers[1].Reports) != 0 ||
		got.Providers[1].Error != "" {
		t.Errorf("tenant-b's listing = %+v, want pci listed with no reports and no error", got.Providers)
	}

	// The caller rode along into Reports, for attribution.
	sox.mu.Lock()
	callers := slices.Clone(sox.reportsCallers)
	sox.mu.Unlock()
	i := slices.IndexFunc(callers, func(c north.HostCaller) bool { return c.Subject.ID == "alice" })
	if i < 0 || callers[i].Tenant != "tenant-a" || callers[i].Principal == "" || callers[i].CorrelationID == "" {
		t.Errorf("Reports saw callers %+v, want tenant-a's alice with a principal and a correlation id", callers)
	}
}

func TestTheListingDegradesPerProviderAndNeverAsAWhole(t *testing.T) {
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	f := newReportFixture(t, reportConfig{requestTimeout: 400 * time.Millisecond},
		reportReg{"example/down", "", &packs{reportsErr: ext.Errorf(ext.PointReportProvider, "example/down", "Reports", ext.KindUnavailable, "warehouse offline")}},
		reportReg{"example/broken", "", &packs{reportsErr: errors.New("nil map")}},
		reportReg{"example/good", "", &packs{offers: everywhere(accessReview)}},
		reportReg{"example/silent", "", &packs{hang: hang}},
		reportReg{"example/bad-format", "", &packs{offers: everywhere(ext.ReportDescriptor{ReportID: "x", Formats: []ext.ReportFormat{7}})}},
		reportReg{"example/twice", "", &packs{offers: everywhere(accessReview, accessReview)}},
		reportReg{"example/formatless", "", &packs{offers: everywhere(ext.ReportDescriptor{ReportID: "x"})}},
		reportReg{"example/nameless", "", &packs{offers: everywhere(ext.ReportDescriptor{ReportID: "x",
			Formats: []ext.ReportFormat{ext.ReportFormatJSON}, Parameters: []ext.ReportParameter{{Name: ""}}})}},
	)
	start := time.Now()
	got := f.listing(t, "tenant-a", f.roleToken(t, identity.RoleAuditor))
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("the listing took %s with a silent provider; the request's bound is 400ms", elapsed)
	}
	want := map[string]string{
		"example/down": north.CodeReportProviderUnavailable, "example/broken": north.CodeReportProviderFailed,
		"example/good": "", "example/silent": north.CodeReportProviderUnavailable,
		"example/bad-format": north.CodeReportProviderFailed, "example/twice": north.CodeReportProviderFailed,
		"example/formatless": north.CodeReportProviderFailed, "example/nameless": north.CodeReportProviderFailed,
	}
	if len(got.Providers) != len(want) {
		t.Fatalf("listed %d providers, want %d: %+v", len(got.Providers), len(want), got.Providers)
	}
	for _, p := range got.Providers {
		if p.Error != want[p.Provider] {
			t.Errorf("%s: error = %q, want %q", p.Provider, p.Error, want[p.Provider])
		}
		if p.Error != "" && len(p.Reports) != 0 {
			t.Errorf("%s: failed and still lists %d reports", p.Provider, len(p.Reports))
		}
		if p.Provider == "example/good" && (len(p.Reports) != 1 || p.Reports[0].Report != "access-review") {
			t.Errorf("the provider that answered is not intact: %+v", p)
		}
	}

	empty := newReportFixture(t, reportConfig{})
	resp := empty.send(t, http.MethodGet, reportsPath("tenant-a"), empty.roleToken(t, identity.RoleAuditor), nil)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(raw)) != `{"providers":[]}` {
		t.Errorf("with nothing registered: %d %s, want 200 {\"providers\":[]}", resp.StatusCode, raw)
	}
}

func TestOnlyTheAuditReadersOfTheTenantReachAReport(t *testing.T) {
	p := &packs{offers: map[ext.Tenant][]ext.ReportDescriptor{"tenant-a": {accessReview}}}
	f := newReportFixture(t, reportConfig{}, reportReg{"example/sox", "", p})

	for _, role := range []identity.Role{identity.RoleIntegration, identity.RoleRequester} {
		token := f.roleToken(t, role)
		for _, c := range []struct {
			method, path string
			body         any
		}{{http.MethodGet, reportsPath("tenant-a"), nil}, {http.MethodPost, generatePath("tenant-a"), generation("example/sox")}} {
			resp := f.send(t, c.method, c.path, token, c.body)
			if e := decodeError(t, resp); resp.StatusCode != http.StatusForbidden || e.Code != north.CodeForbidden {
				t.Errorf("%s %s %s = %d %s, want 403 forbidden", role, c.method, c.path, resp.StatusCode, e.Code)
			}
		}
	}

	auditorA := f.roleToken(t, identity.RoleAuditor)
	resp := f.send(t, http.MethodPost, generatePath("tenant-b"), auditorA, generation("example/sox"))
	if e := decodeError(t, resp); resp.StatusCode != http.StatusForbidden || e.Code != north.CodeTenantOutOfScope {
		t.Errorf("tenant-a's auditor in tenant-b = %d %s, want 403 tenant_out_of_scope", resp.StatusCode, e.Code)
	}

	// A body cannot select a tenant: the field does not exist.
	body := generation("example/sox")
	body["tenant"] = "tenant-b"
	resp = f.send(t, http.MethodPost, generatePath("tenant-a"), auditorA, body)
	if e := decodeError(t, resp); resp.StatusCode != http.StatusBadRequest || e.Parameters["field"] != "body" {
		t.Errorf("a body naming a tenant = %d %+v, want 400 naming the body", resp.StatusCode, e)
	}

	// A report listed only in tenant-a is not found from tenant-b, and
	// Generate is never called.
	auditorB := f.token(t, "tenant-b", map[store.Tenant]identity.RoleSet{"tenant-b": {identity.RoleAuditor}})
	resp = f.send(t, http.MethodPost, generatePath("tenant-b"), auditorB, generation("example/sox"))
	if e := decodeError(t, resp); resp.StatusCode != http.StatusNotFound || e.Code != north.CodeNotFound {
		t.Errorf("tenant-b asking for tenant-a's report = %d %s, want 404 not_found", resp.StatusCode, e.Code)
	}
	if _, generated := p.calls(); generated != 0 {
		t.Errorf("Generate was called %d times; a report the tenant is not listed for is never generated", generated)
	}
}

func TestEveryRequestIsCheckedAgainstTheDescriptorBeforeGenerate(t *testing.T) {
	p := &packs{offers: everywhere(accessReview)}
	f := newReportFixture(t, reportConfig{}, reportReg{"example/sox", "", p})
	auditor := f.roleToken(t, identity.RoleAuditor)

	with := func(change func(map[string]any)) map[string]any {
		b := generation("example/sox")
		change(b)
		return b
	}
	for name, c := range map[string]struct {
		body           map[string]any
		field, problem string
	}{
		"no provider":           {with(func(b map[string]any) { delete(b, "provider") }), "provider", "required"},
		"no report":             {with(func(b map[string]any) { delete(b, "report") }), "report", "required"},
		"no format":             {with(func(b map[string]any) { delete(b, "format") }), "format", "required"},
		"a format not listed":   {with(func(b map[string]any) { b["format"] = "pdf" }), "format", "invalid"},
		"a format nobody knows": {with(func(b map[string]any) { b["format"] = "xml" }), "format", "invalid"},
		"a required parameter missing": {with(func(b map[string]any) {
			b["parameters"] = map[string]string{"label": "x"}
		}), "parameters.regime", "required"},
		"a parameter the descriptor does not name": {with(func(b map[string]any) {
			b["parameters"] = map[string]string{"regime": "sox", "tenant": "tenant-b"}
		}), "parameters.tenant", "invalid"},
		"a value outside the closed set": {with(func(b map[string]any) {
			b["parameters"] = map[string]string{"regime": "hipaa"}
		}), "parameters.regime", "invalid"},
		"no from":             {with(func(b map[string]any) { delete(b, "from") }), "from", "required"},
		"no to":               {with(func(b map[string]any) { delete(b, "to") }), "to", "required"},
		"an unparseable from": {with(func(b map[string]any) { b["from"] = "yesterday" }), "from", "invalid"},
		"an unparseable to":   {with(func(b map[string]any) { b["to"] = "2026-02-31" }), "to", "invalid"},
		"from not before to":  {with(func(b map[string]any) { b["to"] = b["from"] }), "to", "not_after_start"},
	} {
		resp := f.send(t, http.MethodPost, generatePath("tenant-a"), auditor, c.body)
		e := decodeError(t, resp)
		if resp.StatusCode != http.StatusBadRequest || e.Code != north.CodeInvalidRequest ||
			e.Parameters["field"] != c.field || e.Parameters["problem"] != c.problem {
			t.Errorf("%s: %d %s %v, want 400 invalid_request {field: %s, problem: %s}",
				name, resp.StatusCode, e.Code, e.Parameters, c.field, c.problem)
		}
	}
	for name, body := range map[string]map[string]any{
		"an unknown provider": with(func(b map[string]any) { b["provider"] = "example/nobody" }),
		"an unknown report":   with(func(b map[string]any) { b["report"] = "sox-404" }),
	} {
		resp := f.send(t, http.MethodPost, generatePath("tenant-a"), auditor, body)
		e := decodeError(t, resp)
		if resp.StatusCode != http.StatusNotFound || e.Code != north.CodeNotFound ||
			e.Parameters["provider"] != body["provider"] || e.Parameters["report"] != body["report"] {
			t.Errorf("%s: %d %s %v, want 404 not_found naming the provider and the report", name, resp.StatusCode, e.Code, e.Parameters)
		}
	}
	if _, generated := p.calls(); generated != 0 {
		t.Errorf("Generate was called %d times for requests Control refused", generated)
	}
}

func TestAReportIsServedAsItsFormatAsAnAttachmentAndWithItsCaller(t *testing.T) {
	const content = "subject,target\nalice,db-1\n"
	weird := `a/b "c` + "\n" + `d`
	descriptor := accessReview
	descriptor.ReportID = weird
	p := &packs{offers: everywhere(descriptor)}
	p.generate = func(_ context.Context, req ext.ReportRequest) (ext.Report, error) {
		return ext.Report{ReportID: req.ReportID, Format: ext.ReportFormatCSV, ContentType: "text/csv",
			Body: io.NopCloser(strings.NewReader(content))}, nil
	}
	f := newReportFixture(t, reportConfig{}, reportReg{"github.com/example/sox", "", p})
	session, principal := f.session(t, "alice", "oidc-corp", false, identity.RoleAuditor)

	body := generation("github.com/example/sox")
	body["report"] = weird
	body["parameters"] = map[string]string{"regime": "pci", "label": "env=prod"}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, f.http.URL+generatePath("tenant-a"), bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+session)
	req.Header.Set(north.CorrelationHeader, "corr-report-1")
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(got) != content {
		t.Fatalf("generate = %d %q, want 200 and the body byte for byte", resp.StatusCode, got)
	}
	for header, want := range map[string]string{
		"Content-Type":           "text/csv; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
		"Content-Disposition":    `attachment; filename="a_b__c_d_20260101T000000Z_20260201T000000Z.csv"`,
		"Content-Length":         "",
	} {
		if v := resp.Header.Get(header); v != want {
			t.Errorf("%s = %q, want %q", header, v, want)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	want := ext.ReportRequest{
		Tenant: "tenant-a", ReportID: weird, Format: ext.ReportFormatCSV,
		From: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		Parameters: map[string]string{"regime": "pci", "label": "env=prod"},
	}
	if p.last.Tenant != want.Tenant || p.last.ReportID != want.ReportID || p.last.Format != want.Format ||
		!p.last.From.Equal(want.From) || !p.last.To.Equal(want.To) || fmt.Sprint(p.last.Parameters) != fmt.Sprint(want.Parameters) {
		t.Errorf("Generate saw %+v, want %+v", p.last, want)
	}
	for name, callers := range map[string][]north.HostCaller{"Reports": p.reportsCallers, "Generate": p.generateCallers} {
		if len(callers) != 1 {
			t.Fatalf("%s saw %d callers, want 1", name, len(callers))
		}
		c := callers[0]
		if c.Tenant != "tenant-a" || c.Subject.ID != "alice" || c.Principal != principal || c.CorrelationID != "corr-report-1" || c.BreakGlass {
			t.Errorf("%s saw caller %+v, want tenant-a, alice, %s, corr-report-1", name, c, principal)
		}
	}

	// The access log carries the generation; nothing is recorded on the
	// control stream.
	lines := f.logs.lines("northbound_request")
	var line map[string]any
	for _, l := range lines {
		if l["path"] == generatePath("tenant-a") {
			line = l
		}
	}
	if line == nil || line["report_outcome"] != "complete" || line["report_bytes"] != float64(len(content)) ||
		line["report_provider"] != "github.com/example/sox" || line["report_format"] != "csv" ||
		line["report_from"] != "2026-01-01T00:00:00Z" || line["report_to"] != "2026-02-01T00:00:00Z" {
		t.Errorf("the access log line is %v", line)
	}
	var records int
	if err := f.st.Pool().QueryRow(t.Context(), `SELECT count(*) FROM audit_records WHERE tenant = 'tenant-a'`).Scan(&records); err != nil {
		t.Fatalf("count: %v", err)
	}
	if records != 0 {
		t.Errorf("a generation wrote %d audit records; Control records nothing of one", records)
	}
}

func TestAReportStreamsAsItIsProducedAndIsNotBuffered(t *testing.T) {
	gate := make(chan struct{})
	body := &gated{first: []byte("first "), rest: []byte("second"), gate: gate}
	p := &packs{offers: everywhere(accessReview)}
	p.generate = func(_ context.Context, req ext.ReportRequest) (ext.Report, error) {
		return ext.Report{ReportID: req.ReportID, Format: req.Format, ContentType: "text/csv", Body: body}, nil
	}
	f := newReportFixture(t, reportConfig{}, reportReg{"example/sox", "", p})
	resp := f.send(t, http.MethodPost, generatePath("tenant-a"), f.roleToken(t, identity.RoleAuditor), generation("example/sox"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate = %d", resp.StatusCode)
	}
	first := make([]byte, len("first "))
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatalf("the first bytes never arrived while the provider was still producing: %v", err)
	}
	close(gate)
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(first)+string(rest) != "first second" {
		t.Fatalf("body = %q %q (%v)", first, rest, err)
	}
	if !body.closed.Load() {
		t.Error("the provider's body was not closed")
	}
}

func TestAReportThatIsNotWhatWasAskedForIsAFaultAndNoByteOfItIsServed(t *testing.T) {
	for name, rep := range map[string]func(body io.ReadCloser) ext.Report{
		"text/html": func(b io.ReadCloser) ext.Report {
			return ext.Report{Format: ext.ReportFormatCSV, ContentType: "text/html", Body: b}
		},
		"another format": func(b io.ReadCloser) ext.Report {
			return ext.Report{Format: ext.ReportFormatJSON, ContentType: "application/json", Body: b}
		},
		"CSV in latin-1": func(b io.ReadCloser) ext.Report {
			return ext.Report{Format: ext.ReportFormatCSV, ContentType: "text/csv; charset=iso-8859-1", Body: b}
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := &tracked{Reader: strings.NewReader("<script>alert(1)</script>")}
			p := &packs{offers: everywhere(accessReview)}
			p.generate = func(context.Context, ext.ReportRequest) (ext.Report, error) { return rep(body), nil }
			f := newReportFixture(t, reportConfig{}, reportReg{"example/sox", "", p})
			resp := f.send(t, http.MethodPost, generatePath("tenant-a"), f.roleToken(t, identity.RoleAuditor), generation("example/sox"))
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusBadGateway || strings.Contains(string(raw), "script") ||
				!strings.Contains(string(raw), north.CodeReportProviderFailed) {
				t.Errorf("= %d %s, want 502 report_provider_failed and none of the body", resp.StatusCode, raw)
			}
			if !body.closed.Load() {
				t.Error("the refused body was not closed")
			}
		})
	}
}

func TestEachErrorKindFromGenerateAnswersItsOwnStatusAndNeverADenial(t *testing.T) {
	failWith := func(kind ext.Kind, msg string) error {
		return ext.Errorf(ext.PointReportProvider, "example/sox", "Generate", kind, "%s", msg)
	}
	for name, c := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"not found":   {failWith(ext.KindNotFound, "no such pack"), http.StatusNotFound, north.CodeNotFound},
		"invalid":     {failWith(ext.KindInvalid, "regime sox needs a label"), http.StatusBadRequest, north.CodeInvalidRequest},
		"unavailable": {failWith(ext.KindUnavailable, "warehouse offline"), http.StatusServiceUnavailable, north.CodeReportProviderUnavailable},
		"denied":      {failWith(ext.KindDenied, "not licensed"), http.StatusBadGateway, north.CodeReportProviderFailed},
		"malformed":   {failWith(ext.KindMalformed, "bad answer"), http.StatusBadGateway, north.CodeReportProviderFailed},
		"internal":    {failWith(ext.KindInternal, "boom"), http.StatusBadGateway, north.CodeReportProviderFailed},
		"plain":       {errors.New("boom"), http.StatusBadGateway, north.CodeReportProviderFailed},
		"panic":       {nil, http.StatusBadGateway, north.CodeReportProviderFailed},
	} {
		p := &packs{offers: everywhere(accessReview)}
		p.generate = func(context.Context, ext.ReportRequest) (ext.Report, error) {
			if c.err == nil {
				panic("a provider bug")
			}
			return ext.Report{}, c.err
		}
		f := newReportFixture(t, reportConfig{}, reportReg{"example/sox", "", p})
		resp := f.send(t, http.MethodPost, generatePath("tenant-a"), f.roleToken(t, identity.RoleAuditor), generation("example/sox"))
		e := decodeError(t, resp)
		if resp.StatusCode != c.status || e.Code != c.code {
			t.Errorf("%s: %d %s, want %d %s", name, resp.StatusCode, e.Code, c.status, c.code)
		}
		if name == "invalid" && e.Message != "regime sox needs a label" {
			t.Errorf("invalid: message %q, want the provider's own words", e.Message)
		}
	}
}

func TestABodyThatFailsIsAnsweredInTheEnvelopeBeforeItsFirstByteAndAbortedAfter(t *testing.T) {
	serve := func(t *testing.T, body io.ReadCloser) (*reportFixture, *http.Response) {
		t.Helper()
		p := &packs{offers: everywhere(accessReview)}
		p.generate = func(_ context.Context, req ext.ReportRequest) (ext.Report, error) {
			return ext.Report{ReportID: req.ReportID, Format: req.Format, ContentType: "text/csv", Body: body}, nil
		}
		f := newReportFixture(t, reportConfig{}, reportReg{"example/sox", "", p})
		return f, f.send(t, http.MethodPost, generatePath("tenant-a"), f.roleToken(t, identity.RoleAuditor), generation("example/sox"))
	}

	t.Run("before the first byte", func(t *testing.T) {
		_, resp := serve(t, io.NopCloser(iotestErrReader{errors.New("disk gone")}))
		if e := decodeError(t, resp); resp.StatusCode != http.StatusBadGateway || e.Code != north.CodeReportProviderFailed {
			t.Errorf("= %d %s, want 502 report_provider_failed in the envelope", resp.StatusCode, e.Code)
		}
	})
	t.Run("after the first byte", func(t *testing.T) {
		gate := make(chan struct{})
		close(gate)
		body := &gated{first: []byte("subject,target\nalice,"), gate: gate, err: errors.New("disk gone")}
		f, resp := serve(t, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want the 200 already written", resp.StatusCode)
		}
		got, err := io.ReadAll(resp.Body)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("read the body to %v, want io.ErrUnexpectedEOF: a broken response, never a complete one", err)
		}
		if strings.Contains(string(got), "code") || strings.Contains(string(got), "correlation_id") {
			t.Errorf("the response carries an envelope appended to the report: %q", got)
		}
		if !body.closed.Load() {
			t.Error("the body was not closed")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			var aborted map[string]any
			for _, l := range f.logs.lines("northbound_request") {
				if l["path"] == generatePath("tenant-a") {
					aborted = l
				}
			}
			if aborted != nil {
				if aborted["aborted"] != true || aborted["report_outcome"] != "aborted" || aborted["report_bytes"] != float64(len(body.first)) {
					t.Errorf("the access log line is %v, want the abort with its bytes", aborted)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("no access log line for the aborted generation")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if len(f.logs.lines("northbound_panic")) != 0 {
			t.Error("withRecovery treated the abort as a panic")
		}
	})
	t.Run("an empty body", func(t *testing.T) {
		_, resp := serve(t, io.NopCloser(strings.NewReader("")))
		got, err := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || err != nil || len(got) != 0 {
			t.Errorf("= %d %q (%v), want 200 with no bytes", resp.StatusCode, got, err)
		}
	})
}

type iotestErrReader struct{ err error }

func (r iotestErrReader) Read([]byte) (int, error) { return 0, r.err }

func TestAGenerationHasItsOwnBoundAndNoOtherRouteDoes(t *testing.T) {
	request, reportBound := 300*time.Millisecond, 1500*time.Millisecond
	p := &packs{offers: everywhere(accessReview)}
	var delay atomic.Int64
	p.generate = func(ctx context.Context, req ext.ReportRequest) (ext.Report, error) {
		gate := make(chan struct{})
		go func() {
			time.Sleep(time.Duration(delay.Load()))
			close(gate)
		}()
		return ext.Report{ReportID: req.ReportID, Format: req.Format, ContentType: "text/csv",
			Body: &gated{first: []byte("a,"), rest: []byte("b\n"), gate: gate}}, nil
	}
	f := newReportFixture(t, reportConfig{requestTimeout: request, reportTimeout: reportBound}, reportReg{"example/sox", "", p})
	auditor := f.roleToken(t, identity.RoleAuditor)

	delay.Store(int64(2 * request))
	resp := f.send(t, http.MethodPost, generatePath("tenant-a"), auditor, generation("example/sox"))
	if got, err := io.ReadAll(resp.Body); err != nil || string(got) != "a,b\n" {
		t.Errorf("a generation past request_timeout and within report_timeout = %q (%v), want it complete", got, err)
	}

	delay.Store(int64(3 * reportBound))
	start := time.Now()
	resp = f.send(t, http.MethodPost, generatePath("tenant-a"), auditor, generation("example/sox"))
	if _, err := io.ReadAll(resp.Body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("a generation past report_timeout read to %v, want it aborted", err)
	}
	if elapsed := time.Since(start); elapsed > reportBound+2*time.Second {
		t.Errorf("the abort came after %s; the bound is %s", elapsed, reportBound)
	}

	// Another route still stops at request_timeout.
	resp = f.send(t, http.MethodGet, "/api/v1/hosts/"+slowHost+"/tenants/tenant-a/slow", auditor, nil)
	_ = resp.Body.Close()
	if waited := <-f.slowElapsed; waited < request/2 || waited > reportBound {
		t.Errorf("the host route's request ended after %s; the listener's bound is %s", waited, request)
	}
}

func TestEveryRouteButTheGenerationKeepsTheListenersBound(t *testing.T) {
	f := newServer(t)
	timeout := f.server.ReportTimeout()
	if timeout != north.DefaultReportTimeout {
		t.Fatalf("the generation's bound = %s, want the default %s", timeout, north.DefaultReportTimeout)
	}
	generate := "POST /api/v1/tenants/{tenant}/reports/generate"
	seen := false
	for _, route := range f.server.Routes() {
		req := httptest.NewRequest(route.Method, samplePath(route.Pattern, "tenant-a"), nil)
		want := north.DefaultRequestTimeout
		if route.String() == generate {
			want, seen = timeout, true
		}
		if got := f.server.BoundFor(req); got != want {
			t.Errorf("%s is bound by %s, want %s", route, got, want)
		}
	}
	if !seen {
		t.Errorf("%s is not registered", generate)
	}
	for _, path := range []string{"/nowhere", "/api/v1/tenants/tenant-a/reports/generate/more"} {
		if got := f.server.BoundFor(httptest.NewRequest(http.MethodPost, path, nil)); got != north.DefaultRequestTimeout {
			t.Errorf("POST %s is bound by %s, want the listener's", path, got)
		}
	}
	if got := f.server.BoundFor(httptest.NewRequest(http.MethodGet, "/api/v1/tenants/tenant-a/reports/generate", nil)); got != north.DefaultRequestTimeout {
		t.Errorf("a GET of the generation path (a 405) is bound by %s, want the listener's", got)
	}
}

func TestAClientThatDisconnectsCancelsTheGeneration(t *testing.T) {
	seen := make(chan error, 1)
	p := &packs{offers: everywhere(accessReview)}
	var body *gated
	p.generate = func(ctx context.Context, req ext.ReportRequest) (ext.Report, error) {
		body = &gated{first: []byte("a,"), gate: make(chan struct{}), ctx: ctx}
		go func() {
			<-ctx.Done()
			seen <- ctx.Err()
		}()
		return ext.Report{ReportID: req.ReportID, Format: req.Format, ContentType: "text/csv", Body: body}, nil
	}
	f := newReportFixture(t, reportConfig{}, reportReg{"example/sox", "", p})

	ctx, cancel := context.WithCancel(context.Background())
	raw, _ := json.Marshal(generation("example/sox"))
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+generatePath("tenant-a"), bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+f.roleToken(t, identity.RoleAuditor))
	resp, err := f.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 2)); err != nil {
		t.Fatalf("first bytes: %v", err)
	}
	cancel()
	_ = resp.Body.Close()
	select {
	case err := <-seen:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Generate's context ended with %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the generation's context was not cancelled when the client went away")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !body.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the body was not closed after the client went away")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTheCeilingRefusesAGenerationPastItAndNeverTheListing(t *testing.T) {
	gate := make(chan struct{})
	p := &packs{offers: everywhere(accessReview)}
	var n atomic.Int32
	p.generate = func(_ context.Context, req ext.ReportRequest) (ext.Report, error) {
		body := io.NopCloser(strings.NewReader("done\n"))
		if n.Add(1) == 1 {
			body = &gated{first: []byte("a,"), rest: []byte("b\n"), gate: gate}
		}
		return ext.Report{ReportID: req.ReportID, Format: req.Format, ContentType: "text/csv", Body: body}, nil
	}
	f := newReportFixture(t, reportConfig{maxConcurrent: 1}, reportReg{"example/sox", "", p})
	auditor := f.roleToken(t, identity.RoleAuditor)

	first := f.send(t, http.MethodPost, generatePath("tenant-a"), auditor, generation("example/sox"))
	if _, err := io.ReadFull(first.Body, make([]byte, 2)); err != nil {
		t.Fatalf("first generation: %v", err)
	}
	reportsBefore, generateBefore := p.calls()

	busy := f.send(t, http.MethodPost, generatePath("tenant-a"), auditor, generation("example/sox"))
	if e := decodeError(t, busy); busy.StatusCode != http.StatusServiceUnavailable || e.Code != north.CodeReportGenerationBusy ||
		busy.Header.Get("Retry-After") == "" {
		t.Errorf("a second generation = %d %s Retry-After %q, want 503 report_generation_busy with Retry-After",
			busy.StatusCode, e.Code, busy.Header.Get("Retry-After"))
	}
	if r, g := p.calls(); r != reportsBefore || g != generateBefore {
		t.Errorf("the refused generation called the provider (Reports %d→%d, Generate %d→%d)", reportsBefore, r, generateBefore, g)
	}
	f.listing(t, "tenant-a", auditor) // never refused for the ceiling

	close(gate)
	if rest, err := io.ReadAll(first.Body); err != nil || string(rest) != "b\n" {
		t.Fatalf("first generation's rest = %q (%v)", rest, err)
	}
	// The place is released when the body is closed, just after the last
	// byte; the next generation succeeds once it is.
	deadline := time.Now().Add(5 * time.Second)
	for {
		next := f.send(t, http.MethodPost, generatePath("tenant-a"), auditor, generation("example/sox"))
		if next.StatusCode == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the first ended, the next generation is still %d", next.StatusCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
