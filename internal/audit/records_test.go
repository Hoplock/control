// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/hoplock/control/internal/audit"
	"github.com/hoplock/control/internal/contract"
	"github.com/hoplock/control/internal/credential"
	"github.com/hoplock/control/internal/extdefault"
	"github.com/hoplock/control/internal/store"
	"github.com/hoplock/control/internal/store/storetest"
)

// The records the proxy emits since its phases 0043 to 0046 (Hoplock/proxy#66,
// #68, #69, #71), stored and answered for.

func withAttrs(rec contract.LogRecord, kind string, attrs map[string]string) contract.LogRecord {
	rec.Kind = kind
	rec.Attributes = attrs
	return rec
}

// A batch mixing session records with a sweep's change is accepted whole, and
// a session-less record of every kind is accepted on both paths.
func TestSessionLessRecordsAreStoredOnBothPaths(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	sweep := withAttrs(record("rec-sweep", ""), "provisioning", map[string]string{
		audit.AttrEvent: audit.EventDeviceConfigChange, audit.AttrPlatform: "fortios",
		audit.AttrDeviceChangeOp: "delete", audit.AttrTargetAccount: "hl-stale",
	})
	sweep.Subject, sweep.Login = "", ""
	batch := []contract.LogRecord{record("rec-s1", "sess-1"), sweep, record("rec-s2", "sess-1")}
	for i, kind := range audit.Kinds {
		rec := record("rec-kind-"+strconv.Itoa(i), "")
		rec.Kind = kind
		if kind == "stream" {
			rec.Payload = "JCA="
		}
		batch = append(batch, rec)
	}
	resp, err := in.Ingest(ctx, audit.Submission{Tenant: tenantA, ProxyID: proxyA, Records: batch})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if resp.Stored != len(batch) {
		t.Fatalf("stored = %d, want every one of %d", resp.Stored, len(batch))
	}

	// The sweep failure by name, on the priority path: the record the old
	// rule refused, which said a privileged administrator was left standing.
	failure := withAttrs(record("rec-sweep-failed", ""), "policy_decision", map[string]string{
		audit.AttrEvent: audit.EventSweepFailed, audit.AttrPlatform: "fortios",
	})
	failure.Severity, failure.Subject, failure.Login = contract.SeverityCritical, "", ""
	if _, err := in.IngestPriority(ctx, tenantA, proxyA, &contract.LogPriorityRequest{Record: failure}); err != nil {
		t.Fatalf("the sweep failure was refused on the priority path: %v", err)
	}
	for _, kind := range audit.Kinds {
		rec := record("rec-prio-"+kind, "")
		rec.Kind, rec.Severity = kind, contract.SeverityCritical
		if kind == "stream" {
			rec.Payload = "JCA="
		}
		if _, err := in.IngestPriority(ctx, tenantA, proxyA, &contract.LogPriorityRequest{Record: rec}); err != nil {
			t.Errorf("a session-less %s record was refused on the priority path: %v", kind, err)
		}
	}

	stored, err := rowsOf(reader.Find(ctx, tenantA, audit.Query{NoSession: true}, store.Page{Limit: 100}))
	if err != nil {
		t.Fatal(err)
	}
	if want := 1 + len(audit.Kinds)*2 + 1; len(stored) != want {
		t.Errorf("%d session-less records stored, want %d", len(stored), want)
	}

	// A kind outside the list still fails its batch, session or not.
	unknown := record("rec-unknown", "")
	unknown.Kind = "exfiltration"
	if _, err := in.Ingest(ctx, audit.Submission{Tenant: tenantA, ProxyID: proxyA,
		Records: []contract.LogRecord{record("rec-ok", "sess-2"), unknown}}); err == nil {
		t.Fatal("a batch carrying an unknown kind was accepted")
	}
	if _, err := reader.Get(ctx, tenantA, "rec-ok"); !store.IsNotFound(err) {
		t.Errorf("a refused batch stored its other record (%v)", err)
	}

	// "" is no session: the by-session lookups refuse it.
	if _, err := reader.Session(ctx, tenantA, "", 0); !errors.Is(err, audit.ErrNoSession) {
		t.Errorf("Session(\"\") = %v, want ErrNoSession", err)
	}
	if _, err := reader.SessionLoss(ctx, tenantA, ""); !errors.Is(err, audit.ErrNoSession) {
		t.Errorf("SessionLoss(\"\") = %v, want ErrNoSession", err)
	}
	if _, err := reader.Algorithms(ctx, tenantA, ""); !errors.Is(err, audit.ErrNoSession) {
		t.Errorf("Algorithms(\"\") = %v, want ErrNoSession", err)
	}
	if _, err := reader.SessionCertificates(ctx, tenantA, ""); !errors.Is(err, audit.ErrNoSession) {
		t.Errorf("SessionCertificates(\"\") = %v, want ErrNoSession", err)
	}
}

func TestTheDriftFeedFiltersByDeviceObjectAndOperation(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	change := func(id, session, device, op, account, kind string) contract.LogRecord {
		attrs := map[string]string{
			audit.AttrEvent: audit.EventDeviceConfigChange, audit.AttrPlatform: "fortios",
			audit.AttrDeviceChangeOp: op, audit.AttrTargetAccount: account,
		}
		if kind != "" {
			attrs[audit.AttrDeviceObjectKind] = kind
		}
		if session != "" {
			attrs[audit.AttrDeviceFieldPrefix+"vdom"] = "root"
		}
		rec := withAttrs(record(id, session), "provisioning", attrs)
		rec.Target = device
		return rec
	}
	mustIngest(ctx, t, in, tenantA, proxyA,
		change("c-create", "sess-a", "fw-1", "create", "hl-a7f3", ""),
		change("c-modify", "sess-a", "fw-1", "modify", "hl-a7f3", ""),
		change("c-group", "sess-a", "fw-1", "create", "ops", "user-group"),
		change("c-other", "sess-b", "fw-2", "create", "hl-b1", ""),
		change("c-sweep", "", "fw-1", "delete", "hl-stale", ""),
	)

	ids := func(q audit.DriftQuery) []string {
		t.Helper()
		got, err := rowsOf(reader.DeviceConfigChanges(ctx, tenantA, q, allOfIt))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range got {
			out = append(out, c.RecordID)
		}
		slices.Sort(out)
		return out
	}
	if got := ids(audit.DriftQuery{Device: "fw-1", Op: "create"}); !slices.Equal(got, []string{"c-create", "c-group"}) {
		t.Errorf("fw-1 creates = %v", got)
	}
	if got := ids(audit.DriftQuery{Device: "fw-1", ObjectKind: audit.DriftAdministrator, Op: "create"}); !slices.Equal(got, []string{"c-create"}) {
		t.Errorf("fw-1 administrator creates = %v", got)
	}
	if got := ids(audit.DriftQuery{ObjectKind: "user-group"}); !slices.Equal(got, []string{"c-group"}) {
		t.Errorf("user-group changes = %v", got)
	}
	if got := ids(audit.DriftQuery{Account: "hl-a7f3"}); !slices.Equal(got, []string{"c-create", "c-modify"}) {
		t.Errorf("hl-a7f3 changes = %v", got)
	}

	sweeps, err := rowsOf(reader.DeviceConfigChanges(ctx, tenantA, audit.DriftQuery{Op: "delete"}, allOfIt))
	if err != nil || len(sweeps) != 1 {
		t.Fatalf("deletes = %+v (%v)", sweeps, err)
	}
	if s := sweeps[0]; s.SessionID != "" || len(s.DeviceFields) != 0 || s.Device != "fw-1" || s.Account != "hl-stale" {
		t.Errorf("the sweep's change = %+v, want no session, no device fields, the device and the object", s)
	}
}

// The weakening query is one filter: the profile is present and is not
// `default`. Absence is not `default`, and `default` is not a weakening.
func TestTheWeakeningQueryReadsPresenceAndNeverAbsence(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	mustIngest(ctx, t, in, tenantA, proxyA,
		withAttrs(record("w-legacy", "s1"), "provisioning", map[string]string{audit.AttrAlgorithmProfile: "legacy-device"}),
		withAttrs(record("w-default", "s2"), "provisioning", map[string]string{audit.AttrAlgorithmProfile: "default"}),
		withAttrs(record("w-none", "s3"), "provisioning", map[string]string{}),
	)
	got, err := reader.Weakened(ctx, tenantA, time.Time{}, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RecordID != "w-legacy" {
		t.Fatalf("weakened = %v, want only the legacy-device record", got)
	}
	none, _ := reader.Get(ctx, tenantA, "w-none")
	if none.AlgorithmProfile != "" {
		t.Errorf("an absent profile was stored as %q; it is never backfilled to default", none.AlgorithmProfile)
	}
}

// The serial on a session's provisioning record joins the certificate row this
// server minted for that session, and back.
func TestACertificateSerialJoinsItsRow(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	keys, err := extdefault.NewSoftwareKeyStore(st, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := credential.New(st, keys, credential.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ca.Ensure(ctx, tenantA); err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	issued, err := ca.Issue(ctx, tenantA, credential.IssueRequest{
		SubjectID: "alice@example.invalid", Principals: []string{"netadmin"},
		Target: "db-1.example.invalid", SessionID: "sess-cert", PublicKey: sshPub.Marshal(),
	})
	if err != nil {
		t.Fatal(err)
	}
	serial := contract.FormatSerial(uint64(issued.Serial))

	mustIngest(ctx, t, in, tenantA, proxyA,
		withAttrs(record("p-cert", "sess-cert"), "provisioning", map[string]string{
			audit.AttrCredentialMethod: "brokered-certificate", audit.AttrCredentialRung: "1",
			audit.AttrCertificateSerial: serial,
		}),
		record("p-other", "sess-cert"),
	)

	presented, err := reader.SessionCertificates(ctx, tenantA, "sess-cert")
	if err != nil {
		t.Fatal(err)
	}
	if len(presented) != 1 || !presented[0].Found || presented[0].Row.SessionID != "sess-cert" {
		t.Fatalf("presented = %+v, want the row minted for the session", presented)
	}
	sessions, err := rowsOf(reader.CertificateSessions(ctx, tenantA, audit.CertificateQuery{Serial: uint64(issued.Serial)}, allOfIt))
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].RecordID != "p-cert" {
		t.Fatalf("the row resolves to %v, want the provisioning record", sessions)
	}
}

func TestTheUnmetAlgorithmPolicyIsFindable(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	unmet := withAttrs(record("u-1", "sess-u"), "error", map[string]string{
		audit.AttrEvent: audit.EventAlgorithmPolicyUnmet, audit.AttrAlgorithmProfile: "default",
		audit.AttrTargetAddr: "10.0.0.5:22", audit.AttrAlgorithmAxis: "host_key",
		audit.AttrTargetAlgorithmsOffered: "ssh-rsa,ssh-dss", audit.AttrAlgorithmPolicyCause: "profile",
	})
	unmet.Severity, unmet.Target = contract.SeverityWarn, "old-switch"
	pubkey := withAttrs(record("u-2", "sess-v"), "error", map[string]string{
		audit.AttrEvent: audit.EventAlgorithmPolicyUnmet, audit.AttrAlgorithmProfile: "default",
		audit.AttrAlgorithmAxis: "public_key_auth", audit.AttrAlgorithmPolicyCause: "ban",
		audit.AttrAlgorithmBansPrefix + "public_key_auth": "ssh-rsa",
	})
	pubkey.Target = "old-switch"
	mustIngest(ctx, t, in, tenantA, proxyA, unmet, pubkey)

	got, err := rowsOf(reader.AlgorithmPolicyUnmet(ctx, tenantA, audit.UnmetQuery{Target: "old-switch"}, allOfIt))
	if err != nil || len(got) != 2 {
		t.Fatalf("unmet = %+v (%v)", got, err)
	}
	byID := map[string]audit.UnmetAlgorithmPolicy{}
	for _, u := range got {
		byID[u.RecordID] = u
	}
	if u := byID["u-1"]; u.Axis != "host_key" || !slices.Equal(u.Offered, []string{"ssh-rsa", "ssh-dss"}) || !u.OfferedKnown || u.Profile != "default" {
		t.Errorf("u-1 = %+v", u)
	}
	if u := byID["u-2"]; u.OfferedKnown || u.Cause != "ban" || !slices.Equal(u.Bans, []string{"public_key_auth:ssh-rsa"}) {
		t.Errorf("u-2 = %+v: absent offered on public_key_auth means the library could not say", u)
	}
}

func TestTheNegotiatedRecordsAnswerTheirThreeQuestions(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	negotiated := func(id, session, kex, cipher string) contract.LogRecord {
		return withAttrs(record(id, session), "provisioning", map[string]string{
			audit.AttrEvent: audit.EventAlgorithmsNegotiated, audit.AttrTargetKexAlgorithm: kex,
			audit.AttrTargetHostKeyAlgorithm: "ssh-ed25519", audit.AttrTargetCipherOut: cipher,
			audit.AttrTargetCipherIn: cipher, audit.AttrTargetMACOut: "hmac-sha2-256-etm@openssh.com",
			audit.AttrTargetPublicKeyAlgorithmsOffered: "ssh-ed25519,rsa-sha2-256",
		})
	}
	policy := func(id, session string, attrs map[string]string) contract.LogRecord {
		attrs[audit.AttrAlgorithmProfile] = "default"
		attrs[audit.AttrCredentialMethod] = "ephemeral-user"
		return withAttrs(record(id, session), "provisioning", attrs)
	}
	mustIngest(ctx, t, in, tenantA, proxyA,
		negotiated("n-1", "sess-pq", "mlkem768x25519-sha256", "chacha20-poly1305@openssh.com"),
		policy("p-1", "sess-pq", map[string]string{
			audit.AttrAlgorithmFloor: "pq-hybrid-kex", audit.AttrAlgorithmBansPrefix + "ciphers": "aes128-cbc,aes128-ctr",
			audit.AttrAlgorithmBansUnmatched: "ciphers:aes128-cbc",
		}),
		negotiated("n-2", "sess-classic", "curve25519-sha256", "aes128-ctr"),
		policy("p-2", "sess-classic", map[string]string{}),
	)

	sessions := func(q audit.NegotiatedQuery) []string {
		t.Helper()
		got, _, err := reader.SessionsBy(ctx, tenantA, q)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(got)
		return got
	}
	if got := sessions(audit.NegotiatedQuery{Axis: contract.AxisKeyExchanges, Value: "curve25519-sha256"}); !slices.Equal(got, []string{"sess-classic"}) {
		t.Errorf("by key exchange = %v", got)
	}
	if got := sessions(audit.NegotiatedQuery{Axis: contract.AxisCiphers, Value: "aes128-ctr"}); !slices.Equal(got, []string{"sess-classic"}) {
		t.Errorf("by cipher = %v", got)
	}
	if got := sessions(audit.NegotiatedQuery{Axis: contract.AxisHostKeys, Value: "ssh-ed25519"}); len(got) != 2 {
		t.Errorf("by host key = %v", got)
	}
	if got := sessions(audit.NegotiatedQuery{Axis: contract.AxisMACs, Value: "hmac-sha2-256-etm@openssh.com"}); len(got) != 2 {
		t.Errorf("by mac = %v", got)
	}
	if got := sessions(audit.NegotiatedQuery{Floor: "pq-hybrid-kex"}); !slices.Equal(got, []string{"sess-pq"}) {
		t.Errorf("under the floor = %v; an absent floor is not a floor", got)
	}
	if got := sessions(audit.NegotiatedQuery{Floor: "modern-kex"}); len(got) != 0 {
		t.Errorf("under modern-kex = %v, want none", got)
	}
	// Per axis, never a substring: aes128-ctr is banned on ciphers only.
	if got := sessions(audit.NegotiatedQuery{BanAxis: contract.AxisCiphers, BanName: "aes128-ctr"}); !slices.Equal(got, []string{"sess-pq"}) {
		t.Errorf("under a ciphers ban = %v", got)
	}
	if got := sessions(audit.NegotiatedQuery{BanAxis: contract.AxisMACs, BanName: "aes128-ctr"}); len(got) != 0 {
		t.Errorf("a ciphers ban matched as a macs ban: %v", got)
	}

	proof, err := reader.Algorithms(ctx, tenantA, "sess-pq")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(proof.Negotiated[contract.AxisKeyExchanges], []string{"mlkem768x25519-sha256"}) ||
		proof.Floor != "pq-hybrid-kex" || !slices.Contains(proof.BansUnmatched, "ciphers:aes128-cbc") {
		t.Errorf("per-session proof = %+v", proof)
	}
}

func gap(id, session, cause string, records int, first, last time.Time, extra map[string]string) contract.LogRecord {
	attrs := map[string]string{
		audit.AttrEvent: audit.EventLoggingGap, audit.AttrGapCause: cause,
		audit.AttrGapRecords: strconv.Itoa(records), audit.AttrGapBytes: strconv.Itoa(records * 100),
		audit.AttrGapKinds: "command,stream", audit.AttrGapCritical: "0",
	}
	if !first.IsZero() {
		attrs[audit.AttrGapFirstAt] = first.Format(time.RFC3339Nano)
		attrs[audit.AttrGapLastAt] = last.Format(time.RFC3339Nano)
	}
	for k, v := range extra {
		attrs[k] = v
	}
	rec := withAttrs(record(id, session), "error", attrs)
	rec.Severity = contract.SeverityWarn
	return rec
}

func TestWhatAProxyCouldNotDeliverIsFindable(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	day := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	mustIngest(ctx, t, in, tenantA, proxyA,
		gap("g-ev-1", "sess-g", "evicted", 10, day.Add(time.Hour), day.Add(2*time.Hour), nil),
		gap("g-ev-2", "sess-g", "evicted", 5, day.Add(3*time.Hour), day.Add(4*time.Hour), nil),
		gap("g-ref", "sess-g", "refused", 1, day.Add(5*time.Hour), day.Add(5*time.Hour), map[string]string{
			audit.AttrGapRecordIDs: "rec-x,rec-y", audit.AttrGapCritical: "1", audit.AttrGapKinds: "policy_decision",
			audit.AttrRefusalCode: "invalid_record", audit.AttrRefusalMessage: "record 3 (rec-y) is malformed",
		}),
		gap("g-orphan", "", "evicted", 2, day.Add(10*time.Hour), day.Add(11*time.Hour), nil),
		gap("g-nospan", "sess-h", "evicted", 3, time.Time{}, time.Time{}, map[string]string{audit.AttrGapRecords: "many"}),
	)

	ids := func(q audit.GapQuery) []string {
		t.Helper()
		got, err := rowsOf(reader.Gaps(ctx, tenantA, q, allOfIt))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, g := range got {
			out = append(out, g.RecordID)
		}
		slices.Sort(out)
		return out
	}
	if got := ids(audit.GapQuery{SessionID: "sess-g"}); !slices.Equal(got, []string{"g-ev-1", "g-ev-2", "g-ref"}) {
		t.Errorf("by session = %v, want both causes", got)
	}
	if got := ids(audit.GapQuery{Cause: "refused"}); !slices.Equal(got, []string{"g-ref"}) {
		t.Errorf("by cause = %v", got)
	}
	// The span is the missing records' timestamps, never the gap record's
	// own (which is now, in this test).
	if got := ids(audit.GapQuery{SpanFrom: day.Add(90 * time.Minute), SpanTo: day.Add(3*time.Hour + time.Minute)}); !slices.Equal(got, []string{"g-ev-1", "g-ev-2"}) {
		t.Errorf("by span = %v", got)
	}
	if got := ids(audit.GapQuery{SpanFrom: time.Now().Add(-time.Hour), SpanTo: time.Now().Add(time.Hour)}); len(got) != 0 {
		t.Errorf("a span around the gap records' own timestamps matched %v", got)
	}
	if got := ids(audit.GapQuery{RecordID: "rec-y"}); !slices.Equal(got, []string{"g-ref"}) {
		t.Errorf("by record id = %v", got)
	}
	if got := ids(audit.GapQuery{Kind: "policy_decision"}); !slices.Equal(got, []string{"g-ref"}) {
		t.Errorf("by kind = %v", got)
	}
	if got := ids(audit.GapQuery{Critical: true}); !slices.Equal(got, []string{"g-ref"}) {
		t.Errorf("critical = %v", got)
	}
	// A session-less gap is found by span and by proxy, never by "".
	if got := ids(audit.GapQuery{SpanFrom: day.Add(10 * time.Hour), SpanTo: day.Add(12 * time.Hour)}); !slices.Equal(got, []string{"g-orphan"}) {
		t.Errorf("session-less by span = %v", got)
	}
	if got := ids(audit.GapQuery{ProxyID: proxyA, NoSession: true}); !slices.Equal(got, []string{"g-orphan"}) {
		t.Errorf("session-less by proxy = %v", got)
	}

	// Two reports of one cause both return, and the loss is their sum.
	loss, err := reader.SessionLoss(ctx, tenantA, "sess-g")
	if err != nil {
		t.Fatal(err)
	}
	byCause := map[string]store.GapLoss{}
	for _, l := range loss {
		byCause[l.Cause] = l
	}
	if l := byCause["evicted"]; l.Reports != 2 || l.Records != 15 || l.Bytes != 1500 {
		t.Errorf("evicted loss = %+v, want two reports summing 15 records", l)
	}

	// No span, and a key that does not parse: stored, projecting nothing.
	nospan, err := reader.Get(ctx, tenantA, "g-nospan")
	if err != nil {
		t.Fatalf("a gap with no span was not stored: %v", err)
	}
	if nospan.Gap.FirstAt != nil || nospan.Gap.Records != nil || nospan.Gap.Cause != "evicted" {
		t.Errorf("g-nospan projected %+v", nospan.Gap)
	}
}

// The runbook's third step hands this answer to session_kill, so its limit is a
// number of SESSIONS (0021). Counted in records, a session with several
// matching records — a floor is stamped on four of them — crowded the others
// out of the page, and the sessions left out kept running.
func TestSessionsByLimitsSessionsNotRecords(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	floored := func(id, session string, at time.Time) contract.LogRecord {
		rec := withAttrs(record(id, session), "provisioning", map[string]string{
			audit.AttrAlgorithmProfile: "default", audit.AttrAlgorithmFloor: "modern-kex",
		})
		rec.Timestamp = at.UTC().Format(time.RFC3339)
		return rec
	}
	now := time.Now()
	mustIngest(ctx, t, in, tenantA, proxyA,
		floored("old-1", "sess-old", now.Add(-time.Hour)),
		floored("new-1", "sess-new", now), floored("new-2", "sess-new", now), floored("new-3", "sess-new", now),
		floored("orphan", "", now),
	)
	got, truncated, err := reader.SessionsBy(ctx, tenantA, audit.NegotiatedQuery{Floor: "modern-kex", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"sess-new", "sess-old"}) || truncated {
		t.Errorf("two sessions asked for = %v (truncated %v), want both, newest first, never the session-less record, and complete", got, truncated)
	}
	// One fewer than matched: the answer says it stopped short, because the
	// runbook ends exactly the sessions it lists (0023).
	if got, truncated, _ := reader.SessionsBy(ctx, tenantA, audit.NegotiatedQuery{Floor: "modern-kex", Limit: 1}); !slices.Equal(got, []string{"sess-new"}) || !truncated {
		t.Errorf("one session asked for = %v (truncated %v), want sess-new and truncated", got, truncated)
	}
}

// A session's certificate and its algorithms are read from the records that
// carry them, wherever they fall in the session (0021). Read from the session's
// first page of records, a long session's answer depended on how much it had
// done before its target leg came up.
func TestASessionsCertificateAndAlgorithmsAreFoundPastItsFirstPage(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	early := time.Now().Add(-time.Hour)
	busy := records(600, "sess-long")
	for i := range busy {
		busy[i].Timestamp = early.UTC().Format(time.RFC3339)
	}
	mustIngest(ctx, t, in, tenantA, proxyA, busy...)
	mustIngest(ctx, t, in, tenantA, proxyA,
		withAttrs(record("late-cert", "sess-long"), "provisioning", map[string]string{
			audit.AttrCredentialMethod: "brokered-certificate", audit.AttrCredentialRung: "1",
			audit.AttrCertificateSerial: "77", audit.AttrAlgorithmProfile: "default",
		}),
		withAttrs(record("late-negotiated", "sess-long"), "provisioning", map[string]string{
			audit.AttrEvent: audit.EventAlgorithmsNegotiated, audit.AttrTargetKexAlgorithm: "curve25519-sha256",
		}),
	)

	presented, err := reader.SessionCertificates(ctx, tenantA, "sess-long")
	if err != nil {
		t.Fatal(err)
	}
	if len(presented) != 1 || presented[0].Serial != "77" {
		t.Errorf("presented = %+v, want the serial on the session's 601st record", presented)
	}
	algs, err := reader.Algorithms(ctx, tenantA, "sess-long")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(algs.Negotiated[contract.AxisKeyExchanges], []string{"curve25519-sha256"}) || algs.Profile != "default" {
		t.Errorf("algorithms = %+v, want what the late records carry", algs)
	}
}

// Every reader stays inside its tenant (M18's test class, 0021). The same
// records are stored under two tenants, and each reader asked in one returns
// that tenant's alone. Nothing else here held the readers' shared query to
// its tenant filter: dropping it passed every test in the package.
func TestEveryReaderStaysInsideItsTenant(t *testing.T) {
	t.Parallel()
	st := storetest.New(t)
	in := newIngester(t, st)
	reader := audit.NewReader(st)
	ctx := t.Context()

	day := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, tenant := range []store.Tenant{tenantA, tenantB} {
		tag := string(tenant)
		mustIngest(ctx, t, in, tenant, proxyA,
			withAttrs(record(tag+"-prov", "sess-shared"), "provisioning", map[string]string{
				audit.AttrCredentialMethod: "brokered-certificate", audit.AttrCredentialRung: "2",
				audit.AttrCertificateSerial: "9", audit.AttrAlgorithmProfile: "legacy-sha1",
				audit.AttrAlgorithmFloor: "modern-kex", audit.AttrAlgorithmBansPrefix + "ciphers": "aes128-cbc",
				audit.AttrTargetKexAlgorithm: "curve25519-sha256",
			}),
			withAttrs(record(tag+"-change", "sess-shared"), "provisioning", map[string]string{
				audit.AttrEvent: audit.EventDeviceConfigChange, audit.AttrDeviceChangeOp: "create", audit.AttrTargetAccount: "hl-1",
			}),
			withAttrs(record(tag+"-unmet", "sess-shared"), "error", map[string]string{
				audit.AttrEvent: audit.EventAlgorithmPolicyUnmet, audit.AttrAlgorithmAxis: "key_exchange",
			}),
			gap(tag+"-gap", "sess-shared", "refused", 2, day, day.Add(time.Hour), map[string]string{audit.AttrGapRecordIDs: "rec-x"}),
		)
	}

	own := func(name string, ids []string) {
		t.Helper()
		if len(ids) == 0 {
			t.Errorf("%s: found nothing in its own tenant", name)
		}
		for _, id := range ids {
			if strings.HasPrefix(id, string(tenantB)) {
				t.Errorf("%s in %s returned %s", name, tenantA, id)
			}
		}
	}
	ids := func(rows []store.AuditRecord) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.RecordID)
		}
		return out
	}
	mustOK := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	found, err := rowsOf(reader.Find(ctx, tenantA, audit.Query{SessionID: "sess-shared"}, allOfIt))
	mustOK(err)
	own("Find", ids(found))
	degraded, err := reader.DegradedCredentials(ctx, tenantA, time.Time{}, time.Time{}, 0)
	mustOK(err)
	own("DegradedCredentials", ids(degraded))
	weakened, err := reader.Weakened(ctx, tenantA, time.Time{}, time.Time{}, 0)
	mustOK(err)
	own("Weakened", ids(weakened))
	bySerial, err := rowsOf(reader.CertificateSessions(ctx, tenantA, audit.CertificateQuery{Serial: 9}, allOfIt))
	mustOK(err)
	own("CertificateSessions", ids(bySerial))

	drift, err := rowsOf(reader.DeviceConfigChanges(ctx, tenantA, audit.DriftQuery{}, allOfIt))
	mustOK(err)
	var driftIDs []string
	for _, d := range drift {
		driftIDs = append(driftIDs, d.RecordID)
	}
	own("DeviceConfigChanges", driftIDs)

	unmet, err := rowsOf(reader.AlgorithmPolicyUnmet(ctx, tenantA, audit.UnmetQuery{}, allOfIt))
	mustOK(err)
	var unmetIDs []string
	for _, u := range unmet {
		unmetIDs = append(unmetIDs, u.RecordID)
	}
	own("AlgorithmPolicyUnmet", unmetIDs)

	gaps, err := rowsOf(reader.Gaps(ctx, tenantA, audit.GapQuery{RecordID: "rec-x"}, allOfIt))
	mustOK(err)
	var gapIDs []string
	for _, g := range gaps {
		gapIDs = append(gapIDs, g.RecordID)
	}
	own("Gaps", gapIDs)

	certs, err := reader.SessionCertificates(ctx, tenantA, "sess-shared")
	mustOK(err)
	var certIDs []string
	for _, c := range certs {
		certIDs = append(certIDs, c.RecordID)
	}
	own("SessionCertificates", certIDs)

	// Answers with no record ids: counted, so a second tenant shows as a
	// second share of the count.
	loss, err := reader.SessionLoss(ctx, tenantA, "sess-shared")
	mustOK(err)
	if len(loss) != 1 || loss[0].Reports != 1 || loss[0].Records != 2 {
		t.Errorf("SessionLoss in %s = %+v, want its one report of 2 records", tenantA, loss)
	}
	sessions, _, err := reader.SessionsBy(ctx, tenantA, audit.NegotiatedQuery{BanAxis: "ciphers", BanName: "aes128-cbc"})
	mustOK(err)
	if !slices.Equal(sessions, []string{"sess-shared"}) {
		t.Errorf("SessionsBy in %s = %v", tenantA, sessions)
	}
	if none, _, err := reader.SessionsBy(ctx, "initech", audit.NegotiatedQuery{BanAxis: "ciphers", BanName: "aes128-cbc"}); err != nil || len(none) != 0 {
		t.Errorf("SessionsBy in a tenant with no records = %v, %v", none, err)
	}
	algs, err := reader.Algorithms(ctx, "initech", "sess-shared")
	mustOK(err)
	if len(algs.Negotiated) != 0 || algs.Profile != "" {
		t.Errorf("Algorithms in a tenant with no records = %+v", algs)
	}
}
