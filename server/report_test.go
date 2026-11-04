// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hoplock/control/ext"
	"github.com/hoplock/control/internal/identity"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/server"
)

// callerPack is a report provider a host registers, recording what
// server.CallerFrom and server.WriteError answer inside Control's calls.
type callerPack struct {
	mu        sync.Mutex
	callers   map[string]server.Caller
	writeCode string
}

func (p *callerPack) record(op string, ctx context.Context) server.Caller {
	c, ok := server.CallerFrom(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	if ok {
		p.callers[op] = c
	}
	return c
}

func (p *callerPack) Reports(ctx context.Context, _ ext.Tenant) ([]ext.ReportDescriptor, error) {
	p.record("Reports", ctx)
	return []ext.ReportDescriptor{{ReportID: "who-generated", Formats: []ext.ReportFormat{ext.ReportFormatJSON}}}, nil
}

func (p *callerPack) Generate(ctx context.Context, req ext.ReportRequest) (ext.Report, error) {
	c := p.record("Generate", ctx)
	// A provider call is not a host route: WriteError still refuses.
	rec := httptest.NewRecorder()
	server.WriteError(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil),
		http.StatusNotFound, "not_found", nil, "a provider has no response to write")
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	p.mu.Lock()
	p.writeCode = fmt.Sprintf("%d %s", rec.Code, e.Code)
	p.mu.Unlock()
	return ext.Report{ReportID: req.ReportID, Format: req.Format, ContentType: "application/json",
		Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"generated_by":%q}`, c.Principal)))}, nil
}

func TestAHostsReportPackIsListedAndGeneratedThroughControlWithItsCaller(t *testing.T) {
	pack := &callerPack{callers: map[string]server.Caller{}}
	registry := ext.NewRegistry()
	if err := registry.RegisterReportProvider(ext.Registration{Provider: "github.com/example/packs", Version: "1.2.3"}, pack); err != nil {
		t.Fatal(err)
	}
	base, st, _ := runHost(t, registry, "")
	federation, err := identity.NewFederation(st)
	if err != nil {
		t.Fatal(err)
	}
	cred, principal, err := federation.IssueToken(context.Background(), "default", identity.TokenRequest{
		DisplayName: "auditor", Scopes: map[store.Tenant]identity.RoleSet{"default": {identity.RoleAuditor}},
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := get(t, base+"/api/v1/tenants/default/reports", cred.String())
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	want := `{"providers":[{"provider":"github.com/example/packs","version":"1.2.3",` +
		`"reports":[{"report":"who-generated","formats":["json"],"parameters":[]}]}]}`
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(raw)) != want {
		t.Fatalf("listing = %d %s, want 200 %s", resp.StatusCode, raw, want)
	}

	body := `{"provider":"github.com/example/packs","report":"who-generated","format":"json",` +
		`"from":"2026-01-01T00:00:00Z","to":"2026-01-02T00:00:00Z"}`
	req, _ := http.NewRequest(http.MethodPost, base+"/api/v1/tenants/default/reports/generate", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cred.String())
	req.Header.Set("X-Correlation-Id", "corr-pack-1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(raw) != fmt.Sprintf(`{"generated_by":%q}`, principal.ID) {
		t.Fatalf("generate = %d %s, want 200 naming %s", resp.StatusCode, raw, principal.ID)
	}

	pack.mu.Lock()
	defer pack.mu.Unlock()
	for _, op := range []string{"Reports", "Generate"} {
		c, ok := pack.callers[op]
		if !ok {
			t.Errorf("server.CallerFrom answered nothing inside %s", op)
			continue
		}
		if c.Tenant != "default" || c.Principal != principal.ID || c.Subject.ID != "" || c.BreakGlass {
			t.Errorf("%s: caller %+v, want tenant default, principal %s, no subject (a token)", op, c, principal.ID)
		}
		if op == "Generate" && c.CorrelationID != "corr-pack-1" {
			t.Errorf("Generate: correlation id %q, want the request's", c.CorrelationID)
		}
	}
	if pack.writeCode != "500 internal" {
		t.Errorf("server.WriteError inside a provider call answered %s, want 500 internal", pack.writeCode)
	}
}
