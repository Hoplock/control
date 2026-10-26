// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/store/storetest"
)

// A session's certificates and its algorithm summary are bounded by what one
// session is, so neither is a list the caller pages (M26.3, 0037): each reads
// at most audit.SessionRecordBound of the session's records, one more to learn
// whether there were others, and says when the bound cut it. At the bound the
// answer is whole; one past it, the answer is the bound's and says so.
func TestTheSessionReadsSayWhenTheirBoundCutThem(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	const bound = audit.SessionRecordBound
	if bound != 500 {
		t.Fatalf("SessionRecordBound is %d; M26.3 and PLAN §7 say a per-session read keeps 500", bound)
	}
	// Each session's records name a certificate apiece and negotiate a key
	// exchange apiece; the last one past the bound negotiates one the first
	// 500 do not, so a summary that read it would name it.
	session := func(name string, n int) []contract.LogRecord {
		out := make([]contract.LogRecord, 0, n)
		for i := range n {
			kex := "curve25519-sha256"
			if i == bound {
				kex = "diffie-hellman-group14-sha1"
			}
			out = append(out, withAttrs(record(fmt.Sprintf("%s-%d", name, i), name), "provisioning", map[string]string{
				audit.AttrCredentialMethod: "brokered-certificate", audit.AttrCredentialRung: "1",
				audit.AttrCertificateSerial: fmt.Sprint(1000 + i),
				audit.AttrEvent:             audit.EventAlgorithmsNegotiated, audit.AttrTargetKexAlgorithm: kex,
			}))
		}
		return out
	}
	mustIngest(ctx, t, in, tenantA, proxyA, session("sess-whole", bound)...)
	mustIngest(ctx, t, in, tenantA, proxyA, session("sess-cut", bound+1)...)

	for _, c := range []struct {
		session   string
		truncated bool
	}{
		{"sess-whole", false},
		{"sess-cut", true},
	} {
		certs, truncated, err := reader.SessionCertificates(ctx, tenantA, c.session)
		if err != nil {
			t.Fatal(err)
		}
		if len(certs) != bound || truncated != c.truncated {
			t.Errorf("%s's certificates: %d, truncated %t; want %d, truncated %t", c.session, len(certs), truncated, bound, c.truncated)
		}
		if len(certs) > 0 && (certs[0].Serial != "1000" || certs[len(certs)-1].Serial != fmt.Sprint(1000+bound-1)) {
			t.Errorf("%s's certificates run %s to %s, want the session's first %d, oldest first",
				c.session, certs[0].Serial, certs[len(certs)-1].Serial, bound)
		}

		algs, err := reader.Algorithms(ctx, tenantA, c.session)
		if err != nil {
			t.Fatal(err)
		}
		if algs.Truncated != c.truncated {
			t.Errorf("%s's algorithms: truncated %t, want %t", c.session, algs.Truncated, c.truncated)
		}
		// The summary of the first records is still served.
		if got := algs.Negotiated[contract.AxisKeyExchanges]; !slices.Equal(got, []string{"curve25519-sha256"}) {
			t.Errorf("%s's key exchanges = %v, want the first %d records' alone", c.session, got, bound)
		}
	}
}
