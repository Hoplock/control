// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package south_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/fleet"
	"github.com/hoplock/control/internal/store"
)

func configPath(proxyID string) string {
	return strings.Replace(contract.PathProxyConfig, "{proxy_id}", proxyID, 1)
}

func reportPath(proxyID string) string {
	return strings.Replace(contract.PathProxyConfigReport, "{proxy_id}", proxyID, 1)
}

// The fetch, through the whole stack: 204 with nothing published, 200 with the
// hash as the ETag, 304 on the held hash however it is quoted, and
// 404 not_enrolled — never 401 — for an id this tenant does not hold.
func TestTheConfigFetchOverTheWire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()

	if res := h.do("GET", configPath("proxy-2"), nil, h.token()); res.Code != http.StatusNoContent {
		t.Fatalf("nothing published = %d, want 204: %s", res.Code, res.Body)
	}

	if _, err := h.fleet.PublishConfig(ctx, testTenant, fleet.ZoneScope("deep"), fleet.ConfigDocument{
		"logging.batch_size": float64(128),
	}, "south_test"); err != nil {
		t.Fatal(err)
	}
	res := h.do("GET", configPath("proxy-2"), nil, h.token())
	if res.Code != http.StatusOK {
		t.Fatalf("fetch = %d: %s", res.Code, res.Body)
	}
	var doc contract.ProxyConfigDocument
	if err := json.Unmarshal(res.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if res.Header().Get("ETag") != `"`+doc.Hash+`"` || doc.Settings["logging.batch_size"] != float64(128) {
		t.Fatalf("ETag %q, document %+v", res.Header().Get("ETag"), doc)
	}

	for _, tag := range []string{res.Header().Get("ETag"), doc.Hash} {
		notModified := h.doWithHeaders("GET", configPath("proxy-2"), nil, h.token(), map[string]string{"If-None-Match": tag})
		if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
			t.Errorf("If-None-Match %s = %d with %d bytes, want 304 and no body", tag, notModified.Code, notModified.Body.Len())
		}
		// RFC 9110 §15.4.5: a 304 carries the ETag the 200 would have.
		if got := notModified.Header().Get("ETag"); got != res.Header().Get("ETag") {
			t.Errorf("If-None-Match %s: the 304 carries ETag %q, want %q", tag, got, res.Header().Get("ETag"))
		}
	}

	if err := h.store.Proxies().Upsert(ctx, "globex", store.Proxy{
		ID: "p-elsewhere", Zone: "deep", PublicKey: []byte("k"), State: store.EnrollmentEnrolled,
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p-nobody", "p-elsewhere"} {
		res := h.do("GET", configPath(id), nil, h.token())
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404: %s", id, res.Code, res.Body)
		}
		var env contract.ErrorResponse
		decode(t, res, &env)
		if env.Error.Code != contract.ErrCodeNotEnrolled {
			t.Errorf("%s code = %q, want not_enrolled", id, env.Error.Code)
		}
	}
}

// The report answers 200 {"accepted": true}, and the same report twice is
// accepted twice: a server that rejected the repeat would break the proxy's
// report after every sync.
func TestTheConfigReportOverTheWire(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rep := contract.ProxyConfigReport{State: contract.ConfigStateApplied, ReportedAt: time.Now().UTC().Format(time.RFC3339)}
	for range 2 {
		res := h.post(t, reportPath("proxy-2"), rep)
		if res.Code != http.StatusOK {
			t.Fatalf("report = %d: %s", res.Code, res.Body)
		}
		var ack contract.ProxyConfigReportResponse
		decode(t, res, &ack)
		if !ack.Accepted {
			t.Fatal("accepted is false on a recorded report")
		}
	}
	for name, bad := range map[string]contract.ProxyConfigReport{
		"no state":       {ReportedAt: rep.ReportedAt},
		"an odd state":   {State: "done", ReportedAt: rep.ReportedAt},
		"no reported_at": {State: contract.ConfigStateApplied},
	} {
		if res := h.post(t, reportPath("proxy-2"), bad); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, res.Code)
		}
	}
	if res := h.post(t, reportPath("p-nobody"), rep); res.Code != http.StatusNotFound {
		t.Errorf("a report for an unknown proxy = %d, want 404", res.Code)
	}
}
