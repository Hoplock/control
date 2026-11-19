// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoplock/control/internal/contract"
)

// provisioningOf is a session's `provisioning` record carrying the credential
// method — the one that says how the target was reached.
func provisioningOf(t *testing.T, sid string) record {
	t.Helper()
	var found record
	waitFor(t, 30*time.Second, "the provisioning record of "+sid, func() bool {
		for _, rec := range recordsOf(t, sid) {
			if rec.Kind == "provisioning" && rec.Attributes["credential_method"] != "" {
				found = rec
				return true
			}
		}
		return false
	})
	return found
}

// testBrokeredCertificate: a route naming brokered-certificate logs the user in
// to the CA-trusting account with a certificate this server signed for that
// session, the record stores its serial, the serial resolves to the row minted
// for the session, and two sessions served by one CACHED decision present two
// different serials (Hoplock/proxy#68).
func testBrokeredCertificate(t *testing.T) {
	var serials []string
	for i := 0; i < 2; i++ {
		r := ssh(t, session{who: "alice", target: "certified", command: "id -un"})
		wantExit(t, r, "brokered-certificate", 0)
		if strings.TrimSpace(r.stdout) != "causer" {
			t.Fatalf("brokered-certificate logged in as %q", r.stdout)
		}
		sid := sessionIDOf(t, r)
		rec := provisioningOf(t, sid)
		if rec.Attributes["credential_method"] != "brokered-certificate" {
			t.Fatalf("%s: credential_method %q", sid, rec.Attributes["credential_method"])
		}
		serial := rec.Attributes["credential_certificate_serial"]
		if serial == "" {
			t.Fatalf("%s: the provisioning record stores no credential_certificate_serial: %v", sid, rec.Attributes)
		}
		// The serial resolves to the ssh_certificates row minted for this session.
		var certs struct {
			Certificates []struct {
				Serial string `json:"credential_certificate_serial"`
				Found  bool   `json:"found"`
				Row    struct {
					SessionID  string   `json:"session_id"`
					Principals []string `json:"principals"`
				} `json:"certificate"`
			} `json:"certificates"`
		}
		north(t, "/audit/sessions/"+url.PathEscape(sid)+"/certificates", &certs)
		if len(certs.Certificates) != 1 {
			t.Fatalf("%s: %d certificates presented, want 1: %+v", sid, len(certs.Certificates), certs)
		}
		c := certs.Certificates[0]
		if c.Serial != serial || !c.Found || c.Row.SessionID != sid {
			t.Fatalf("%s: serial %s does not resolve to the row minted for the session: %+v", sid, serial, c)
		}
		serials = append(serials, serial)
		// The first session is decided; the second is served by that decision
		// from the proxy's cache, so it has no decision record of its own and
		// explain says this server took none for it.
		code, body, err := northDo(http.MethodGet, "/sessions/"+url.PathEscape(sid)+"/decisions", nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := []int{http.StatusOK, http.StatusNotFound}[i]; code != want {
			t.Fatalf("session %d (%s): explain answered %d, want %d (decided, then cached): %s", i+1, sid, code, want, body)
		}
	}
	if serials[0] == serials[1] {
		t.Fatalf("two sessions presented one serial %s: a certificate is minted per session, not per decision", serials[0])
	}
}

// testFailedIssuance: a ladder of brokered-certificate then brokered-key, its
// decision cached, then this server stopped. The proxy still holds the
// decision and cannot get a certificate; the user is told it is an outage and
// NO brokered-key session is opened — a weaker standing credential offered
// exactly when Control cannot say otherwise is what the rule exists to refuse.
func testFailedIssuance(t *testing.T) {
	warm := ssh(t, session{who: "alice", target: "ladder", command: "id -un"})
	wantExit(t, warm, "the ladder's first rung, to cache the decision", 0)
	if strings.TrimSpace(warm.stdout) != "causer" {
		t.Fatalf("the ladder's first session used %q, want the certificate rung's causer", warm.stdout)
	}

	compose(t, "stop", "control")
	defer startControl(t)
	r := ssh(t, session{who: "alice", target: "ladder", command: "id -un"})
	wantContains(t, r, "a failed issuance", textOutage)
	wantNotContains(t, r, "a failed issuance", "netadmin")
	if r.code == 0 {
		t.Fatalf("a session opened while no certificate could be issued:\n%s", r)
	}
}

// background runs a session on another goroutine and returns a func that waits
// for it to end.
func background(s session) func() result {
	var wg sync.WaitGroup
	var out result
	wg.Add(1)
	go func() {
		defer wg.Done()
		out, _ = sshE(s)
	}()
	return func() result { wg.Wait(); return out }
}

// liveSessionOf finds the newest started session of who on target, waiting for
// its session_start record.
func liveSessionOf(t *testing.T, who, target string) string {
	t.Helper()
	var sid string
	waitFor(t, 30*time.Second, "a live session of "+who+" on "+target, func() bool {
		var out struct {
			Records []record `json:"records"`
		}
		north(t, "/audit/records?kind=session_start&limit=1&login="+who+"&target="+target, &out)
		if len(out.Records) == 1 {
			sid = out.Records[0].SessionID
			return true
		}
		return false
	})
	return sid
}

// testRevocation: an operator kills a live session and the user is told why;
// a cached decision is invalidated and the next connection is re-decided.
func testRevocation(t *testing.T) {
	// A cacheable decision, then a session held open under it.
	warm := ssh(t, session{who: "alice", target: "cached", command: "true"})
	wantExit(t, warm, "warm the cache", 0)
	cachedSID := sessionIDOf(t, warm)

	wait := background(session{who: "alice", target: "cached", command: "echo live; sleep 120"})
	var sid string
	waitFor(t, 30*time.Second, "the held session to start", func() bool {
		sid = liveSessionOf(t, "alice", "cached")
		return sid != cachedSID
	})

	northPost(t, "/revocations", map[string]any{
		"type": "session_kill", "session_ids": []string{sid},
		"reason": "e2e: an operator ended this session",
	}, http.StatusOK, http.StatusAccepted)
	done := wait()
	if done.code == 0 {
		t.Fatalf("the killed session ended cleanly:\n%s", done)
	}
	wantContains(t, done, "the killed session's user", "an operator ended this session")

	// Invalidate the cached decision; the next connection is decided afresh.
	before := countDecisions(t, "alice@e2e.example", "cached")
	northPost(t, "/revocations", map[string]any{"type": "cache_invalidate", "all": true},
		http.StatusOK, http.StatusAccepted)
	waitFor(t, 30*time.Second, "a re-decided connection", func() bool {
		r, _ := sshE(session{who: "alice", target: "cached", command: "true"})
		return r.code == 0 && countDecisions(t, "alice@e2e.example", "cached") > before
	})
}

// countDecisions counts the authorize records for subject on target: one per
// decision this server made, so a cache hit adds none.
func countDecisions(t *testing.T, subject, target string) int {
	t.Helper()
	var out struct {
		Records []record `json:"records"`
	}
	north(t, "/audit/records?kind=authorize&limit=500&subject="+url.QueryEscape(subject)+"&target="+target, &out)
	return len(out.Records)
}

// testJIT: a denied person is granted access by an approver, the next
// connection succeeds, and after the grant expires it is denied again (M10).
func testJIT(t *testing.T) {
	denied := ssh(t, session{who: "bob", target: "target", command: "id -un"})
	wantContains(t, denied, "before the grant", textDenied)

	created := northPost(t, "/grants", map[string]any{
		"subject":  "bob@e2e.example",
		"scope":    map[string]any{"name": "jit-target", "targets": []string{"target"}},
		"duration": "30s",
		"reason":   "e2e: just-in-time access",
	}, http.StatusCreated, http.StatusOK)
	grantID, _ := field(created, "grant.id").(string)
	if grantID == "" {
		t.Fatalf("the grant has no id: %v", created)
	}

	ok := ssh(t, session{who: "bob", target: "target", command: "id -un"})
	wantExit(t, ok, "under the grant", 0)
	d := decisionsOf(t, sessionIDOf(t, ok))
	if len(d) == 0 || d[0].Rule != "jit-target" || field(d[0].Explanation, "grant") != grantID {
		t.Fatalf("the granted session was not decided by jit-target under %s: %+v", grantID, d)
	}

	// A command blocked under the grant is the showcase's whole question:
	// what was blocked, who ran it, over which route, under which decision,
	// and the grant that made the access possible.
	blocked := ssh(t, session{who: "bob", target: "target", command: "cat /etc/shadow"})
	wantContains(t, blocked, "a blocked command under the grant", "blocked by policy")
	row := showcaseRow(t, sessionIDOf(t, blocked))
	if row["grant_id"] != grantID || row["matched_rule"] != "jit-target" {
		t.Fatalf("the showcase row for bob's blocked command: grant %v rule %v, want %s by jit-target",
			row["grant_id"], row["matched_rule"], grantID)
	}

	waitFor(t, 60*time.Second, "the grant to expire and the deny to return", func() bool {
		r, _ := sshE(session{who: "bob", target: "target", command: "id -un"})
		return strings.Contains(r.output(), textDenied)
	})
}

// testAudit: the showcase query, and the field names the real proxy emits
// stored under the names this store indexes (Hoplock/proxy#66, #69). The two
// components once spelled these differently while each one's tests passed
// (0010's learnings); this is the one place that disagreement can fail.
func testAudit(t *testing.T) {
	// The showcase: blocked commands with the identity, route and decision
	// behind them (the grant half is asserted under jit_grant, where one
	// exists).
	blocked := ssh(t, session{who: "alice", target: "target", command: "cat /etc/shadow"})
	row := showcaseRow(t, sessionIDOf(t, blocked))
	if field(row, "record.subject") != "alice@e2e.example" || row["matched_rule"] != "direct-shell" ||
		row["route_type"] != "direct" || row["decision_id"] == nil || row["command"] != "cat /etc/shadow" {
		t.Fatalf("the showcase row: %v", row)
	}

	// An ephemeral-user session's provisioning record.
	r := ssh(t, session{who: "alice", target: "target", command: "true"})
	sid := sessionIDOf(t, r)
	prov := provisioningOf(t, sid)
	if prov.Attributes["credential_method"] != "ephemeral-user" {
		t.Fatalf("%s: credential_method %q", sid, prov.Attributes["credential_method"])
	}
	if got := field(prov.Doc(), "algorithm_profile"); got != "default" && prov.Attributes["algorithm_profile"] != "default" {
		t.Fatalf("%s: algorithm_profile %v / %q, want default", sid, got, prov.Attributes["algorithm_profile"])
	}

	// The ladder's first rung is stored as 1, and the degradation query does
	// not return it.
	lr := ssh(t, session{who: "alice", target: "ladder", command: "true"})
	lsid := sessionIDOf(t, lr)
	lprov := provisioningOf(t, lsid)
	if lprov.Attributes["credential_rung"] != "1" {
		t.Fatalf("%s: credential_rung %q, want 1", lsid, lprov.Attributes["credential_rung"])
	}
	var degraded struct {
		Records []record `json:"records"`
	}
	north(t, "/audit/records?degraded=true&limit=500&session="+lsid, &degraded)
	if len(degraded.Records) != 0 {
		t.Fatalf("the degradation query returned a first-rung session %s", lsid)
	}

	// target.algorithms_negotiated: stored with target_kex_algorithm, never
	// kex_algorithm, and the by-value query finds the session by it.
	var negotiated record
	waitFor(t, 30*time.Second, "the negotiated record", func() bool {
		for _, rec := range recordsOf(t, sid) {
			if rec.Event == "target.algorithms_negotiated" {
				negotiated = rec
				return true
			}
		}
		return false
	})
	kex := negotiated.Attributes["target_kex_algorithm"]
	if kex == "" || negotiated.Attributes["kex_algorithm"] != "" {
		t.Fatalf("negotiated record fields: %v", negotiated.Attributes)
	}
	var byValue struct {
		Records []record `json:"records"`
	}
	north(t, "/audit/records?limit=500&negotiated="+url.QueryEscape("key_exchanges:"+kex)+"&session="+sid, &byValue)
	if len(byValue.Records) == 0 {
		t.Fatalf("the by-value query does not find %s by key_exchanges:%s", sid, kex)
	}

	// The target holds both observations the proxy reports — the probe's
	// rungs and the key-exchange report — and neither overwrites the other.
	var kexObs struct {
		Observations []map[string]any `json:"key_exchanges"`
	}
	var rungObs struct {
		Observations []map[string]any `json:"rungs"`
	}
	north(t, "/fleet/target-observations/key-exchanges?limit=200", &kexObs)
	north(t, "/fleet/target-observations/rungs?limit=200", &rungObs)
	if !mentions(kexObs.Observations, "target") || !mentions(rungObs.Observations, "target") {
		t.Fatalf("target observations: key exchanges %v; rungs %v", kexObs.Observations, rungObs.Observations)
	}
}

// showcaseRow is the showcase query's row for one session's blocked command
// (PLAN §7: "every blocked command on env=prod, and who approved the access").
func showcaseRow(t *testing.T, sid string) map[string]any {
	t.Helper()
	var row map[string]any
	waitFor(t, 30*time.Second, "the blocked command of "+sid+" in the showcase query", func() bool {
		var show struct {
			Blocked []map[string]any `json:"blocked"`
		}
		north(t, "/audit/blocked-commands?label=env=prod&limit=200", &show)
		for _, b := range show.Blocked {
			if field(b, "record.session_id") == sid && b["decision_found"] == true {
				row = b
				return true
			}
		}
		return false
	})
	return row
}

// mentions reports whether any observation names host.
func mentions(obs []map[string]any, host string) bool {
	for _, o := range obs {
		if o["target"] == host {
			return true
		}
	}
	return false
}

// testDeclaredProfiles: the declaration this server recorded for the real
// proxy names every profile it accepts, each with every axis non-empty
// (Hoplock/proxy#72). The decoder ignores a key it does not know, so a
// misspelled key would turn every ban judgement into "unknown".
func testDeclaredProfiles(t *testing.T) {
	var out struct {
		Declarations []struct {
			ProxyID     string `json:"proxy_id"`
			Declaration struct {
				Profiles []map[string]any `json:"algorithm_profiles"`
			} `json:"declaration"`
		} `json:"declarations"`
	}
	north(t, "/fleet/algorithms?limit=50", &out)
	want := map[string]bool{"proxy-edge": false, "proxy-relay": false}
	for _, d := range out.Declarations {
		if _, ok := want[d.ProxyID]; !ok {
			continue
		}
		want[d.ProxyID] = true
		names := map[string]bool{}
		for _, p := range d.Declaration.Profiles {
			name, _ := p["profile"].(string)
			names[name] = true
			for _, axis := range []string{"key_exchanges", "ciphers", "macs", "host_keys", "public_key_auth"} {
				if v, _ := p[axis].([]any); len(v) == 0 {
					t.Errorf("%s: profile %q declares an empty %s", d.ProxyID, name, axis)
				}
			}
		}
		for _, p := range contractProfiles {
			if !names[p] {
				t.Errorf("%s: the recorded declaration omits profile %q (has %v)", d.ProxyID, p, names)
			}
		}
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("no declaration recorded for %s", p)
		}
	}
}

// contractProfiles is every algorithm profile the contract defines, which is
// every profile a proxy built from it accepts.
var contractProfiles = []string{
	string(contract.AlgorithmProfileDefault),
	string(contract.AlgorithmProfileLegacyRSASHA1),
	string(contract.AlgorithmProfileLegacyDevice),
}

// testNoRefusedGap: since Hoplock/proxy#71 a record this server refuses no
// longer stalls the proxy's delivery — it is set aside and reported in a
// logging.gap — so every assertion above can pass while a record the real
// proxy emits is refused. Any such gap is exactly the disagreement this
// topology exists to catch, and its refusal_message names the record (PLAN §7).
func testNoRefusedGap(t *testing.T) {
	var out struct {
		Gaps []map[string]any `json:"gaps"`
	}
	north(t, "/audit/gaps?limit=500", &out)
	for _, g := range out.Gaps {
		if g["gap_cause"] == "refused" || g["cause"] == "refused" {
			t.Errorf("a record the real proxy emitted was refused: %v", g)
		}
	}
	var recs struct {
		Records []record `json:"records"`
	}
	north(t, "/audit/records?gaps=true&gap_cause=refused&limit=500", &recs)
	for _, r := range recs.Records {
		t.Errorf("logging.gap with gap_cause refused: %s %v", r.RecordID, r.Attributes)
	}
}

// testAuditVerify: every stream's hash chain verifies after the whole run.
func testAuditVerify(t *testing.T) {
	r := execIn(t, "control", "hoplock-control", "audit-verify", "--config", "/etc/hoplock/control.yaml", "--tenant", "default")
	wantExit(t, r, "audit-verify", 0)
}
