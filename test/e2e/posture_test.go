// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"crypto/tls"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testWirePosture: this server's own listeners serve the posture
// deploy/control/control.yaml states, asserted on the wire rather than read
// from the file (0063, PLAN M29). The north-bound listener negotiates
// X25519MLKEM768 with a default client and still serves a classical-only one
// (`preferred`); the south-bound listener negotiates it with a probe on the
// proxies' network and refuses a classical-only handshake (`required`) — so
// every request a proxy made in this run is a handshake that negotiated a
// hybrid. Neither answers plaintext. And each says so on its start-up line.
func testWirePosture(t *testing.T) {
	pool, err := topologyCA()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("north prefers the hybrid", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			curves []tls.CurveID
			want   tls.CurveID
		}{
			{"a default client", nil, tls.X25519MLKEM768},
			{"a classical-only client", []tls.CurveID{tls.X25519}, tls.X25519},
		} {
			client := northClient(pool, c.curves)
			req, err := http.NewRequest(http.MethodGet, northURL+"/proxies?limit=1", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+operatorToken)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			_ = resp.Body.Close()
			client.CloseIdleConnections()
			if resp.StatusCode != http.StatusOK || resp.TLS == nil {
				t.Fatalf("%s: %s, TLS %v", c.name, resp.Status, resp.TLS)
			}
			if resp.TLS.Version != tls.VersionTLS13 || resp.TLS.CurveID != c.want {
				t.Fatalf("%s negotiated %s over %#x, want %s over TLS 1.3", c.name, resp.TLS.CurveID, resp.TLS.Version, c.want)
			}
		}
		// Plaintext is not served: Go's TLS server answers a plaintext request
		// with a bare 400 and nothing of the API.
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(strings.Replace(northURL, "https://", "http://", 1) + "/proxies?limit=1")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest || strings.Contains(string(body), "proxies") {
				t.Fatalf("a plaintext request to the north-bound listener: %s %s", resp.Status, body)
			}
		}
	})

	t.Run("south requires the hybrid", func(t *testing.T) {
		hybrid := probeSouth(t, "X25519MLKEM768")
		if !hybrid.served {
			t.Fatalf("a hybrid handshake to the south-bound listener was not served:\n%s", hybrid.out)
		}
		if hybrid.group != "X25519MLKEM768" {
			t.Fatalf("the south-bound listener negotiated %q, want X25519MLKEM768:\n%s", hybrid.group, hybrid.out)
		}
		classical := probeSouth(t, "X25519")
		if classical.served {
			t.Fatalf("the south-bound listener served a classical-only handshake (require_hybrid_kex):\n%s", classical.out)
		}
		// The refusal is this server's, during the handshake, and it says why.
		waitFor(t, 30*time.Second, "the south-bound listener's refusal in its log", func() bool {
			logs := compose(t, "logs", "--no-color", "control").output()
			return strings.Contains(logs, "south.tls.require_hybrid_kex") && strings.Contains(logs, "X25519")
		})
	})

	t.Run("each listener states its posture at start-up", func(t *testing.T) {
		logs := compose(t, "logs", "--no-color", "control").output()
		for _, c := range []struct{ msg, kex string }{
			{"south-bound listener starting", "required"},
			{"north-bound listener starting", "preferred"},
		} {
			line := logLine(logs, c.msg)
			if line == "" {
				t.Fatalf("no %q line in control's log", c.msg)
			}
			for _, want := range []string{`"tls":"on"`, `"min_tls_version":"1.3"`, `"post_quantum_kex":"` + c.kex + `"`} {
				if !strings.Contains(line, want) {
					t.Errorf("%q does not state %s:\n%s", c.msg, want, line)
				}
			}
		}
	})
}

// probed is one TLS handshake the probe made to the south-bound listener.
type probed struct {
	out    string
	group  string // the group openssl says it negotiated
	served bool   // an HTTP response came back over it
}

var negotiatedGroup = regexp.MustCompile(`(?m)Negotiated TLS1\.3 group: (\S+)`)

// probeSouth makes one handshake to control:8080 from the probe container on
// the proxies' network (deploy/compose.yaml, `probe`), offering group alone,
// and sends one request over it. In TLS 1.3 a server's refusal of the client's
// half arrives after the client thinks the handshake is done, so "served"
// means an HTTP status line came back, not that the handshake returned.
func probeSouth(t *testing.T, group string) probed {
	t.Helper()
	script := `printf 'GET /v1/events HTTP/1.0\r\nHost: control\r\n\r\n' | ` +
		`openssl s_client -connect control:8080 -servername control -CAfile /material/tls_ca.pem ` +
		`-verify_return_error -groups "$1" -brief -ign_eof 2>&1`
	r := compose(t, "--profile", "probe", "run", "--rm", "--no-deps", "-T", "probe", "sh", "-c", script, "probe", group)
	p := probed{out: r.output(), served: regexp.MustCompile(`(?m)^HTTP/1\.[01] \d{3}`).MatchString(r.output())}
	if m := negotiatedGroup.FindStringSubmatch(r.output()); m != nil {
		p.group = m[1]
	}
	return p
}

// logLine is the first JSON log line whose message is msg.
func logLine(logs, msg string) string {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, `"msg":"`+msg+`"`) {
			return line
		}
	}
	return ""
}

// testAlgorithmFloor: the proxy→target floor (Hoplock/proxy#69) against a real
// proxy and real sshds, asserted on the records this server stored — the only
// per-session proof that the floor held (PLAN §7). One host runs three sshds
// (deploy/target/entrypoint.sh): the default one meets pq-hybrid-kex with
// mlkem768x25519-sha256; the classical-only one and the one whose only hybrid
// is sntrup761 do not, and each fails as an OUTAGE, never a deny, with
// target.algorithm_policy_unmet stored. The no-floor routes to the same two
// sshds connect, so each outage is the floor's and nothing else's.
func testAlgorithmFloor(t *testing.T) {
	// The evidence the learnings name: the OpenSSH the met floor rests on.
	version := execIn(t, "target", "sh", "-c", "ssh -V 2>&1")
	t.Logf("target: %s", strings.TrimSpace(version.output()))
	if !openSSHAtLeast(version.output(), 9, 9) {
		t.Fatalf("the target's OpenSSH is older than 9.9 and offers no mlkem768x25519-sha256: %s", version.output())
	}

	t.Run("a floor the target meets stores the hybrid beside it", func(t *testing.T) {
		r := ssh(t, session{who: "alice", target: "pq", command: "echo floor-met"})
		wantExit(t, r, "pq floor, default sshd", 0)
		wantContains(t, r, "pq floor, default sshd", "floor-met")
		rec := negotiatedOf(t, sessionIDOf(t, r))
		t.Logf("pq (port 22): target_kex_algorithm=%s algorithm_floor=%s",
			rec.Attributes["target_kex_algorithm"], rec.Attributes["algorithm_floor"])
		if got := rec.Attributes["target_kex_algorithm"]; got != "mlkem768x25519-sha256" {
			t.Errorf("target_kex_algorithm = %q, want mlkem768x25519-sha256", got)
		}
		if got := rec.Attributes["algorithm_floor"]; got != "pq-hybrid-kex" {
			t.Errorf("algorithm_floor = %q, want pq-hybrid-kex on the record beside what was negotiated", got)
		}
	})

	for _, c := range []struct{ name, target, offers string }{
		{"no hybrid at all", "pq-classical", ""},
		{"sntrup761 as the only hybrid", "pq-sntrup", "sntrup761x25519-sha512@openssh.com"},
	} {
		t.Run("the same floor against "+c.name+" is an outage", func(t *testing.T) {
			r := ssh(t, session{who: "alice", target: c.target, command: "true"})
			if r.code == 0 {
				t.Fatalf("a pq-hybrid-kex route to %s connected:\n%s", c.target, r)
			}
			wantContains(t, r, c.target, textOutage)
			wantNotContains(t, r, c.target, textDenied)
			sid := sessionIDOf(t, r)
			unmet := eventOf(t, sid, "target.algorithm_policy_unmet")
			for key, want := range map[string]string{
				"algorithm_axis":         "key_exchange",
				"algorithm_policy_cause": "floor",
				"algorithm_floor":        "pq-hybrid-kex",
			} {
				if got := unmet.Attributes[key]; got != want {
					t.Errorf("%s: %s = %q, want %q", sid, key, got, want)
				}
			}
			offered := unmet.Attributes["target_algorithms_offered"]
			t.Logf("%s: target_algorithms_offered=%s", c.target, offered)
			if strings.Contains(offered, "mlkem") || !strings.Contains(offered, "curve25519-sha256") {
				t.Errorf("%s: target_algorithms_offered = %q, want the sshd's classical list and no ML-KEM", sid, offered)
			}
			if c.offers != "" && !strings.Contains(offered, c.offers) {
				t.Errorf("%s: target_algorithms_offered = %q, want %s among them", sid, offered, c.offers)
			}
			// Refused at the provisioning login, before anything was made.
			for _, rec := range recordsOf(t, sid) {
				if rec.Kind == "provisioning" && rec.Attributes["credential_method"] != "" {
					t.Errorf("%s: a credential was provisioned for a session whose floor was not met: %v", sid, rec.Attributes)
				}
			}
		})
	}

	for _, c := range []struct{ target, port string }{{"classical", "2222"}, {"sntrup", "2223"}} {
		t.Run("without a floor the sshd on "+c.port+" connects", func(t *testing.T) {
			r := ssh(t, session{who: "alice", target: c.target, command: "echo no-floor"})
			wantExit(t, r, c.target, 0)
			rec := negotiatedOf(t, sessionIDOf(t, r))
			t.Logf("%s (port %s): target_kex_algorithm=%s", c.target, c.port, rec.Attributes["target_kex_algorithm"])
			if got := rec.Attributes["target_kex_algorithm"]; got != "curve25519-sha256" {
				t.Errorf("target_kex_algorithm = %q, want the classical curve25519-sha256", got)
			}
			if got, ok := rec.Attributes["algorithm_floor"]; ok {
				t.Errorf("a route with no floor stored algorithm_floor %q", got)
			}
		})
	}

	// Each sshd's key exchange reaches the store as an observation of its
	// own, by host and port; the rung observation of the host whose floor
	// was met is still there beside it (0019).
	t.Run("each sshd's key exchange is observed beside its rungs", func(t *testing.T) {
		want := map[string]string{"pq:22": "pq-hybrid-kex", "pq-classical:2222": "modern-kex", "pq-sntrup:2223": "modern-kex"}
		var kex struct {
			Observations []map[string]any `json:"key_exchanges"`
		}
		waitFor(t, 30*time.Second, "a key-exchange observation for every pq-* sshd", func() bool {
			north(t, "/fleet/target-observations/key-exchanges?limit=1000", &kex)
			seen := 0
			for _, o := range kex.Observations {
				if _, ok := want[observationKey(o)]; ok {
					seen++
				}
			}
			return seen == len(want)
		})
		for _, o := range kex.Observations {
			key := observationKey(o)
			floor, ok := want[key]
			if !ok {
				continue
			}
			t.Logf("observed %s: floor_met=%v negotiated=%v", key, o["floor_met"], o["negotiated"])
			if o["floor_met"] != floor {
				t.Errorf("%s: floor_met %v, want %s", key, o["floor_met"], floor)
			}
		}
		var rungs struct {
			Observations []map[string]any `json:"rungs"`
		}
		north(t, "/fleet/target-observations/rungs?limit=1000", &rungs)
		if !mentions(rungs.Observations, "pq") {
			t.Errorf("no rung observation for pq beside its key-exchange one: %v", rungs.Observations)
		}
	})
}

// observationKey is an observation's host:port, port 22 when it states none.
func observationKey(o map[string]any) string {
	port := "22"
	if p, ok := o["target_port"].(float64); ok && p != 0 {
		port = strconv.Itoa(int(p))
	}
	host, _ := o["target"].(string)
	return host + ":" + port
}

// negotiatedOf is the session's stored target.algorithms_negotiated record.
func negotiatedOf(t *testing.T, sid string) record {
	t.Helper()
	return eventOf(t, sid, "target.algorithms_negotiated")
}

// eventOf waits for the session's first stored record of event.
func eventOf(t *testing.T, sid, event string) record {
	t.Helper()
	var found record
	waitFor(t, 30*time.Second, event+" for "+sid, func() bool {
		for _, rec := range recordsOf(t, sid) {
			if rec.Event == event {
				found = rec
				return true
			}
		}
		return false
	})
	return found
}

var openSSHVersion = regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`)

// openSSHAtLeast reports whether `ssh -V` output names OpenSSH major.minor or
// later.
func openSSHAtLeast(out string, major, minor int) bool {
	m := openSSHVersion.FindStringSubmatch(out)
	if m == nil {
		return false
	}
	maj, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	return maj > major || (maj == major && mi >= minor)
}
