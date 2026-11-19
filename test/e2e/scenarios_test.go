// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTopology is the whole suite, in one ordered run: some scenarios stop and
// start a component, and the uid assertion spans every scenario, so the order
// is part of what is tested. Each subtest is one product claim proven across
// both components (deploy/README.md lists what each proves).
func TestTopology(t *testing.T) {
	uids := &uidLedger{}
	baseline := snapshotStandingAccounts(t)

	t.Run("isolation", testIsolation)
	t.Run("direct_route", func(t *testing.T) { testDirectRoute(t, uids) })
	t.Run("multi_hop_relay", testMultiHopRelay)
	t.Run("denied_and_explained", testDenied)
	t.Run("outage_is_not_a_deny", testOutage)
	t.Run("policy_axes", testPolicyAxes)
	t.Run("filtering_tiers", testFilteringTiers)
	t.Run("credential_methods", func(t *testing.T) { testCredentialMethods(t, uids) })
	t.Run("uid_floor_survives_restarts", func(t *testing.T) { testUIDFloor(t, uids) })
	t.Run("brokered_certificate", testBrokeredCertificate)
	t.Run("failed_issuance_is_an_outage", testFailedIssuance)
	t.Run("revocation", testRevocation)
	t.Run("jit_grant", testJIT)
	t.Run("audit", testAudit)
	t.Run("declared_algorithm_profiles", testDeclaredProfiles)
	t.Run("wire_posture", testWirePosture)
	t.Run("algorithm_floor", testAlgorithmFloor)

	// The suite's closing claims, over everything above.
	t.Run("no_uid_issued_twice", func(t *testing.T) { uids.assertStrictlyIncreasing(t) })
	t.Run("nothing_leaked", func(t *testing.T) { assertNothingLeaked(t, baseline) })
	t.Run("no_refused_gap", testNoRefusedGap)
	t.Run("audit_chain_verifies", testAuditVerify)
}

// testIsolation asserts the network restrictions the multi-hop claim rests on,
// rather than inferring them from a session that worked: the client cannot
// reach a target, nothing can open a connection to proxy-relay, and the
// protected zone's host is not on any network proxy-edge is on.
func testIsolation(t *testing.T) {
	cases := []struct {
		from, host, port, why string
	}{
		{"client", "target", "22", "the client is not on `core`: a target is reachable only through a proxy"},
		{"client", "proxy-relay", "2222", "proxy-relay accepts no inbound connection"},
		{"proxy-edge", "proxy-relay", "2222", "not even from the proxy it registers with"},
		{"client", "zone-host", "22", "the protected zone is not on the client's network"},
	}
	for _, c := range cases {
		probe := probeTCP(t, c.from, c.host, c.port)
		if probe {
			t.Errorf("%s reached %s:%s, but %s", c.from, c.host, c.port, c.why)
		}
	}
	// proxy-edge cannot resolve the protected zone's host at all: it is on no
	// network proxy-edge is on, so the only way a session gets there is the
	// relay registration proxy-relay opened outbound.
	if r := execIn(t, "proxy-edge", "getent", "hosts", "zone-host"); r.code == 0 {
		// The proxy image has no getent-free resolver probe; getent is libc's.
		t.Errorf("proxy-edge resolves zone-host: %s", r.stdout)
	}
	// And proxy-relay publishes nothing.
	if r := compose(t, "port", "proxy-relay", "2222"); strings.TrimSpace(r.stdout) != "" && r.code == 0 {
		t.Errorf("proxy-relay publishes 2222 on the host: %s", r.stdout)
	}
}

// probeTCP reports whether node can open a TCP connection to host:port. The
// client image carries netcat; the proxy image carries nothing but the proxy, so
// it is probed from a sidecar sharing its network namespace.
func probeTCP(t *testing.T, node, host, port string) bool {
	t.Helper()
	if node == "client" {
		return execIn(t, "client", "nc", "-z", "-w", "3", host, port).code == 0
	}
	cid := strings.TrimSpace(compose(t, "ps", "-q", node).stdout)
	r := run(t, "", "docker", "run", "--rm", "--network", "container:"+cid,
		"--entrypoint", "nc", "hoplock-control-e2e/client", "-z", "-w", "3", host, port)
	return r.code == 0
}

// testDirectRoute: exec and an interactive shell succeed, the store holds a
// replayable record of the session and a decision record that explains the
// allow.
func testDirectRoute(t *testing.T, uids *uidLedger) {
	r := ssh(t, session{who: "alice", target: "target", command: "id -u; hostname"})
	wantExit(t, r, "exec", 0)
	uids.observe(t, "target", r)
	sid := sessionIDOf(t, r)

	shell := ssh(t, session{who: "alice", target: "target", opts: []string{"-tt"},
		stdin: "echo shell-$((40+2))\nexit\n"})
	wantContains(t, shell, "interactive shell", "shell-42")

	// The decision record explains the allow: the rule, the terms that matched.
	ds := decisionsOf(t, sid)
	if len(ds) == 0 {
		t.Fatalf("no decision record for %s", sid)
	}
	d := ds[0]
	if d.Effect != "allow" || d.Rule != "direct-shell" {
		t.Fatalf("decision %s: effect %q rule %q, want allow by direct-shell", d.DecisionID, d.Effect, d.Rule)
	}
	if terms, _ := d.Explanation["terms"].([]any); len(terms) == 0 {
		t.Fatalf("decision %s explains nothing: %v", d.DecisionID, d.Explanation)
	}

	// The replayable record: the session's output, captured as `stream`
	// records whose bytes this server stores beside them and serves back for
	// replay (PLAN §7).
	var replayed string
	waitFor(t, 30*time.Second, "the session's capture", func() bool {
		replayed = ""
		for _, rec := range recordsOf(t, sid) {
			if rec.Kind == "stream" && rec.Attributes["stream"] == "stdout" {
				var c struct {
					Payload []byte `json:"payload"`
				}
				north(t, "/audit/records/"+rec.RecordID+"/capture", &c)
				replayed += string(c.Payload)
			}
		}
		return strings.Contains(replayed, strings.Fields(r.stdout)[0])
	})
}

// testMultiHopRelay: a session to the protected zone's host succeeds with no
// inbound rule into the zone, and BOTH halves of the chain contract this server
// owes (PLAN §4) are visible: the inner proxy authenticated as a chain leg on
// its own key and was answered with the user's identity, and it authorized for
// itself carrying a hop trail naming the edge proxy.
func testMultiHopRelay(t *testing.T) {
	r := ssh(t, session{who: "alice", target: "zone-host", command: "hostname; id -un"})
	wantExit(t, r, "relayed exec", 0)
	wantContains(t, r, "relayed exec", "netadmin")
	sid := sessionIDOf(t, r)

	edgeDecisions := decisionsOf(t, sid)
	if len(edgeDecisions) != 1 || edgeDecisions[0].ProxyID != "proxy-edge" {
		t.Fatalf("the user's session %s: want one decision, by proxy-edge: %+v", sid, edgeDecisions)
	}
	edge := edgeDecisions[0]
	if rt := field(edge.Snapshot, "route_type"); rt != "nexthop" {
		t.Fatalf("proxy-edge's leg: route_type %v, want nexthop", rt)
	}
	if c := field(edge.Snapshot, "hop.connection"); c != "relay" {
		t.Fatalf("proxy-edge's leg: hop.connection %v, want relay", c)
	}
	if n := field(edge.Snapshot, "hop.next_proxy_id"); n != "proxy-relay" {
		t.Fatalf("proxy-edge's leg: hop.next_proxy_id %v, want proxy-relay", n)
	}

	// The inner leg is a session of its own on proxy-relay, with an id of its
	// own: the edge's session id does not travel down the chain, so the inner
	// leg is found by who and where, newest first.
	var leg struct {
		Records []record `json:"records"`
	}
	north(t, "/audit/records?kind=authorize&limit=1&proxy=proxy-relay&subject=alice@e2e.example&target=zone-host", &leg)
	if len(leg.Records) != 1 {
		t.Fatal("proxy-relay recorded no authorize for alice on zone-host")
	}
	innerSID := leg.Records[0].SessionID
	var inner decision
	north(t, "/decisions/"+leg.Records[0].Attributes["decision_id"], &inner)
	// It authorized for itself, carrying a trail naming the edge (0008).
	trail, _ := field(inner.Inputs, "context.hop_trail").([]any)
	if inner.ProxyID != "proxy-relay" || len(trail) != 1 || trail[0] != "proxy-edge" {
		t.Fatalf("proxy-relay's authorize: proxy %q hop trail %v, want proxy-relay with [proxy-edge]", inner.ProxyID, trail)
	}
	// And it was answered with the USER's identity, having authenticated as a
	// chain leg on its own key (0007): no user key ever reaches proxy-relay.
	if subj := field(inner.Inputs, "subject.id"); subj != "alice@e2e.example" {
		t.Fatalf("proxy-relay was answered with subject %v, want alice's identity", subj)
	}
	if rt := field(inner.Snapshot, "route_type"); rt != "direct" {
		t.Fatalf("proxy-relay's leg: route_type %v, want direct", rt)
	}

	// Each hop's leg and direction is in the trail: the edge's hop record names
	// the relay connection, and the inner session says it arrived from a peer
	// proxy with the edge in its trail and authenticated alice.
	var edgeLeg, innerStart, innerAuth bool
	for _, rec := range recordsOf(t, sid) {
		if rec.ProxyID == "proxy-edge" && rec.Attributes["hop_connection"] == "relay" &&
			rec.Attributes["hop_next_proxy"] == "proxy-relay" {
			edgeLeg = true
		}
	}
	for _, rec := range recordsOf(t, innerSID) {
		if rec.Kind == "session_start" && rec.Attributes["hop_peer"] == "true" && rec.Attributes["hop_trail"] == "proxy-edge" {
			innerStart = true
		}
		if rec.Kind == "auth" && rec.Subject == "alice@e2e.example" {
			innerAuth = true
		}
	}
	if !edgeLeg || !innerStart || !innerAuth {
		t.Fatalf("hop legs in the audit trail: edge relay leg %v, inner peer start %v, inner auth %v",
			edgeLeg, innerStart, innerAuth)
	}
}

// testDenied is the disclosure story end to end (M4): the user is told as
// little as possible, and the operator resolves the session id the user quotes
// to the exact rule.
func testDenied(t *testing.T) {
	r := ssh(t, session{who: "bob", target: "target", command: "id"})
	wantContains(t, r, "denied user", textDenied)
	wantNotContains(t, r, "denied user", "contractors")
	wantNotContains(t, r, "denied user", textOutage)
	sid := sessionIDOf(t, r)

	ds := decisionsOf(t, sid)
	if len(ds) != 1 {
		t.Fatalf("want one decision for %s, got %d", sid, len(ds))
	}
	d := ds[0]
	if d.Effect != "deny" || d.Rule != "contractors-denied-prod" {
		t.Fatalf("explain %s: effect %q rule %q", sid, d.Effect, d.Rule)
	}
	if reason := field(d.Explanation, "deny_reason"); reason != "contractors need a grant for production" {
		t.Fatalf("explain %s: deny_reason %v", sid, reason)
	}
}

// testOutage: a stopped server is never reported as a denial (M11).
func testOutage(t *testing.T) {
	compose(t, "stop", "control")
	defer startControl(t)

	r := ssh(t, session{who: "alice", target: "appliance", command: "true"})
	wantContains(t, r, "outage", textOutage)
	wantNotContains(t, r, "outage", textDenied)
	if sessionIDOf(t, r) == "" {
		t.Fatal("an outage must quote a session id")
	}
}

// startControl starts this server and waits for the proxies to hear it again.
func startControl(t *testing.T) {
	t.Helper()
	compose(t, "start", "control")
	waitFor(t, 60*time.Second, "control to answer", func() bool {
		_, err := northGet("/proxies?limit=1")
		return err == nil
	})
	waitForProxies(t)
}

// waitForProxies waits until a session through each route class works again —
// the proxies' event streams have reconnected and the relay is registered.
func waitForProxies(t *testing.T) {
	t.Helper()
	waitFor(t, 90*time.Second, "the fleet to serve again", func() bool {
		a, _ := sshE(session{who: "alice", target: "appliance", command: "true"})
		z, _ := sshE(session{who: "alice", target: "zone-host", command: "true"})
		return a.code == 0 && z.code == 0
	})
}

// testPolicyAxes: the three axes (M3) — channel request, forwarding
// destination, global request — each enforced by the real proxy on a decision
// the real server made.
func testPolicyAxes(t *testing.T) {
	// sftp refused while a shell succeeds.
	sftp := run(t, "ls\n", "docker", "compose", "exec", "-T", "client", "sftp",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-i", "/material/user_alice",
		"-P", proxyPort, "-b", "-", "alice#target@"+edgeProxy)
	if sftp.code == 0 {
		t.Fatalf("sftp succeeded on a route that permits no subsystem:\n%s", sftp)
	}
	shell := ssh(t, session{who: "alice", target: "target", command: "echo shell-ok"})
	wantContains(t, shell, "shell beside the refused sftp", "shell-ok")

	// A tunnel to the permitted host:port succeeds; another is refused.
	ok := ssh(t, session{who: "alice", target: "target", opts: []string{"-W", "target:22"}, stdin: ""})
	wantContains(t, ok, "permitted tunnel", "SSH-2.0-")
	refused := ssh(t, session{who: "alice", target: "target", opts: []string{"-W", "target:23"}})
	wantNotContains(t, refused, "refused tunnel", "SSH-2.0-OpenSSH")
	if refused.code == 0 {
		t.Fatalf("a tunnel to an unlisted port succeeded:\n%s", refused)
	}

	// tcpip-forward refused, and no listener created on the target side.
	fwd := ssh(t, session{who: "alice", target: "target",
		opts:    []string{"-o", "ExitOnForwardFailure=yes", "-R", "2900:localhost:22"},
		command: "true"})
	if fwd.code == 0 {
		t.Fatalf("tcpip-forward was granted on a route that permits no global request:\n%s", fwd)
	}
	if r := execIn(t, "target", "sh", "-c", "ss -ltn 2>/dev/null | grep -c ':2900 ' || true"); strings.TrimSpace(r.stdout) != "0" && strings.TrimSpace(r.stdout) != "" {
		t.Fatalf("a listener exists on :2900 after a refused tcpip-forward: %s", r.stdout)
	}
}

// testFilteringTiers: filtered exec is a guardrail with an honest limit — a
// shell wrapper walks past it — and restricted exec denies the same wrapper.
// The audit record names which tier decided.
func testFilteringTiers(t *testing.T) {
	blocked := ssh(t, session{who: "alice", target: "target", command: "cat /etc/shadow"})
	wantContains(t, blocked, "filtered exec, the command itself", "blocked by policy")

	through := ssh(t, session{who: "alice", target: "target", command: `sh -c 'cat /etc/shadow'`})
	wantNotContains(t, through, "filtered exec, wrapped in sh -c", "blocked by policy")

	ran := ssh(t, session{who: "svc", target: "restricted", command: "/usr/bin/id"})
	wantExit(t, ran, "restricted exec, the permitted command", 0)
	wrapped := ssh(t, session{who: "svc", target: "restricted", command: "sh -c /usr/bin/id"})
	wantContains(t, wrapped, "restricted exec, wrapped in sh -c", "blocked by policy")

	tier := func(sid string) string {
		for _, rec := range recordsOf(t, sid) {
			if rec.Kind == "policy_decision" {
				if v := rec.Attributes["tier"]; v != "" {
					return v
				}
			}
		}
		return ""
	}
	if got := tier(sessionIDOf(t, blocked)); got != "filtered_exec" {
		t.Errorf("the filtered tier's block names tier %q", got)
	}
	if got := tier(sessionIDOf(t, wrapped)); got != "restricted_exec" {
		t.Errorf("the restricted tier's refusal names tier %q", got)
	}
}

// testCredentialMethods: ephemeral-user creates the account and removes it;
// brokered-key logs in to the standing account and changes nothing.
func testCredentialMethods(t *testing.T, uids *uidLedger) {
	r := ssh(t, session{who: "alice", target: "target", command: "id -un; id -u"})
	wantExit(t, r, "ephemeral-user", 0)
	uids.observe(t, "target", r)
	account := strings.Fields(r.stdout)[0]
	if !strings.HasPrefix(account, "hl-") {
		t.Fatalf("ephemeral-user logged in as %q, want an hl- account", account)
	}
	waitFor(t, 60*time.Second, "the ephemeral account to be removed", func() bool {
		return execIn(t, "target", "getent", "passwd", account).code != 0
	})

	b := ssh(t, session{who: "alice", target: "appliance", command: "id -un"})
	wantExit(t, b, "brokered-key", 0)
	if strings.TrimSpace(b.stdout) != "netadmin" {
		t.Fatalf("brokered-key logged in as %q", b.stdout)
	}
}

// uidLedger collects every ephemeral-user uid the run produces, per target.
type uidLedger struct {
	seen map[string][]int
}

func (l *uidLedger) observe(t *testing.T, target string, r result) {
	t.Helper()
	for _, f := range strings.Fields(r.stdout) {
		if n, err := strconv.Atoi(f); err == nil && n >= 2000000 {
			if l.seen == nil {
				l.seen = map[string][]int{}
			}
			l.seen[target] = append(l.seen[target], n)
			return
		}
	}
	t.Fatalf("no ephemeral uid in the output:\n%s", r)
}

func (l *uidLedger) last(target string) int {
	s := l.seen[target]
	if len(s) == 0 {
		return 0
	}
	return s[len(s)-1]
}

// assertStrictlyIncreasing: no uid is ever issued twice across the whole run,
// restarts of either component included, and each is above the last.
func (l *uidLedger) assertStrictlyIncreasing(t *testing.T) {
	for target, s := range l.seen {
		for i := 1; i < len(s); i++ {
			if s[i] <= s[i-1] {
				t.Errorf("target %s: uid %d issued after %d (sequence %v)", target, s[i], s[i-1], s)
			}
		}
		t.Logf("target %s: %d ephemeral uids, strictly increasing: %v", target, len(s), s)
	}
	if len(l.seen) == 0 {
		t.Fatal("the run produced no ephemeral uid at all")
	}
}

// testUIDFloor: the uid floor survives a restart of the proxy, and of this
// server (0007). Asserted on the uid, not on the lease call: a proxy holding a
// block legitimately makes no call at all.
func testUIDFloor(t *testing.T, uids *uidLedger) {
	session := session{who: "alice", target: "target", command: "id -u"}
	before := uids.last("target")
	r := ssh(t, session)
	uids.observe(t, "target", r)

	compose(t, "restart", "proxy-edge")
	waitForProxies(t)
	r = ssh(t, session)
	uids.observe(t, "target", r)
	if uids.last("target") <= before {
		t.Fatalf("after a proxy restart: uid %d is not above %d", uids.last("target"), before)
	}

	before = uids.last("target")
	compose(t, "restart", "control")
	startControl(t)
	r = ssh(t, session)
	uids.observe(t, "target", r)
	if uids.last("target") <= before {
		t.Fatalf("after a control restart: uid %d is not above %d", uids.last("target"), before)
	}
}

// standingAccounts is what "byte-identical afterwards" is measured on: the
// standing accounts' passwd lines and every file under their homes, hashed.
type standingAccounts map[string]string

func snapshotStandingAccounts(t *testing.T) standingAccounts {
	t.Helper()
	out := standingAccounts{}
	for _, node := range []string{"target", "target-zone"} {
		for _, acct := range []string{"netadmin", "causer"} {
			r := execIn(t, node, "sh", "-c", fmt.Sprintf(
				"getent passwd %[1]s; cd /home/%[1]s && find . -type f -print0 | sort -z | xargs -0 sha256sum; ls -la --time-style=+ /home/%[1]s", acct))
			out[node+"/"+acct] = r.stdout
		}
	}
	return out
}

// assertNothingLeaked: no ephemeral account, home or key survives the run, and
// the standing accounts are byte-identical to the start.
func assertNothingLeaked(t *testing.T, baseline standingAccounts) {
	for _, node := range []string{"target", "target-zone"} {
		waitFor(t, 90*time.Second, "every ephemeral account on "+node+" to be removed", func() bool {
			r := execIn(t, node, "sh", "-c", "getent passwd | grep -c '^hl-' || true; ls /home | grep -c '^hl-' || true")
			return strings.Fields(r.stdout)[0] == "0" && strings.Fields(r.stdout)[1] == "0"
		})
	}
	now := snapshotStandingAccounts(t)
	for k, v := range baseline {
		if now[k] != v {
			t.Errorf("%s changed during the run:\nbefore:\n%s\nafter:\n%s", k, v, now[k])
		}
	}
}
