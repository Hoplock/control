// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/hoplock/control/internal/store"
)

// The hash chain (M8, M18).
//
// WHAT IT DEFENDS AGAINST, EXACTLY. Each record's hash covers its own bytes,
// its position, its stream, its tenant, and its predecessor's hash. Altering a
// stored record changes its hash; removing one leaves its successor pointing
// at a hash nothing produces and leaves a gap in the sequence. So anybody who
// can reach the database but cannot rewrite every later record in the stream
// is detected — a DBA with an UPDATE, an application bug, a restored-from-
// backup row, a partially-successful attacker.
//
// WHAT IT DOES NOT DEFEND AGAINST, AND THIS IS NOT A GAP TO BE APOLOGISED FOR
// BUT A LIMIT TO BE STATED. An attacker who can write every row of a stream
// can recompute the whole chain from the point of the change onwards and
// produce a store that verifies perfectly. Nothing inside the database can
// prevent that, because the verifier's only input is the database. Closing it
// needs an anchor the attacker cannot rewrite — a hash published outside this
// system, on a schedule, so a rewritten chain disagrees with something already
// in someone else's hands. That is future work and it is deliberately not
// claimed here: an audit store that overstates its guarantee is worse than one
// that states a smaller one, because the overstatement is what somebody builds
// a compliance claim on.
//
// ONE CHAIN PER TENANT PER STREAM. The tenant half is M18: a customer leaving
// must be able to take a chain that still verifies, and a chain spanning
// tenants makes departure either a broken chain or a disclosure of everybody
// else's record count and timing. The stream half is throughput: a chain has
// one head, so every append to it serialises, and one chain for a whole
// deployment would put every proxy in the fleet behind one lock.

// StreamControl is the chain this server's OWN records join: authentication
// outcomes, break-glass logins, and anything else Control writes down about
// itself rather than relays from a proxy (0011). It is a stream of its own so
// that `hoplock-control audit-verify --stream control` verifies exactly the
// records nobody outside this process produced.
const StreamControl = "control"

// StreamFor names the chain a record joins.
//
// IT IS THE SUBMITTING PROXY, never anything in the record. The proxy is the
// unit that writes in order — one writer, one connection, records that already
// arrived in sequence — so it is the unit whose serialisation costs nothing,
// and it is the unit an operator can name when asking what to verify. A stream
// per session would make each chain a handful of records and a deleted session
// undetectable; one stream per tenant would make the fleet queue behind one
// lock.
func StreamFor(proxyID string) string {
	if proxyID == "" {
		// A record ingested by a caller with no proxy identity still
		// belongs in a chain rather than outside one. It is named
		// rather than empty so that the stream list an operator sees
		// says what it is.
		return "unattributed"
	}
	return "proxy:" + proxyID
}

// Link computes the hash of a record at a position in a chain.
//
// The tenant and the stream are inside the hash, not merely beside it: without
// them a record could be moved from one tenant's chain to another's at the same
// position and still verify, which is exactly the isolation M18 is for.
func Link(tenant, stream string, seq int64, prevHash, body string) string {
	h := sha256.New()
	// Every part is length-prefixed so that no two different tuples can
	// produce the same byte stream. Without it a stream named `a` with a
	// body starting `b` hashes the same as a stream `ab` with a shorter
	// body, and a collision an attacker can construct is not a chain.
	for _, part := range []string{
		chainVersion, tenant, stream, strconv.FormatInt(seq, 10), prevHash, body,
	} {
		h.Write([]byte(strconv.Itoa(len(part))))
		h.Write([]byte(":"))
		h.Write([]byte(part))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// chainVersion is inside every link, so a future change to the construction
// cannot silently produce a chain that verifies under both readings.
const chainVersion = "hoplock.chain/1"

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Position assigns a record its place in a chain and computes its hash. It is
// the only place a chain position is created.
func Position(tenant, stream string, prev store.AuditRecord, row store.AuditRecord) store.AuditRecord {
	row.Stream = stream
	row.ChainSeq = prev.ChainSeq + 1
	row.PrevHash = prev.Hash
	row.Hash = Link(tenant, stream, row.ChainSeq, row.PrevHash, row.Body)
	return row
}

// BreakKind says how a chain failed.
type BreakKind string

const (
	// BreakGap is a missing record: the sequence jumps, so something was
	// deleted.
	BreakGap BreakKind = "gap"
	// BreakPrevMismatch is a record whose predecessor link does not name
	// the record in front of it — the predecessor was altered, or the
	// record was moved.
	BreakPrevMismatch BreakKind = "prev-mismatch"
	// BreakHashMismatch is a record whose stored hash does not match its
	// stored bytes: the row was edited.
	BreakHashMismatch BreakKind = "hash-mismatch"
	// BreakCaptureMismatch is a session capture whose bytes no longer match
	// the digest inside the hashed record.
	BreakCaptureMismatch BreakKind = "capture-mismatch"
	// BreakUnknownVersion is a record whose body was written by an encoding
	// this build does not know. It is reported rather than treated as a
	// break, because "I cannot check this" and "this was tampered with" are
	// different answers and only one of them should wake somebody.
	BreakUnknownVersion BreakKind = "unknown-body-version"
)

// Break is the first failure in a chain, with enough context to investigate.
//
// It names the record, its position, and both hashes, because an investigator
// arriving at this has to be able to go to the row — and because "the chain is
// broken" without a row to look at is a page nobody can action.
type Break struct {
	Tenant   string
	Stream   string
	Kind     BreakKind
	ChainSeq int64
	// ExpectedSeq is set on a gap: the position the walk was at when the
	// next record turned out to be somewhere else.
	ExpectedSeq int64
	RecordID    string
	Want        string
	Got         string
	Detail      string
}

func (b *Break) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "audit chain %s/%s broken at sequence %d", b.Tenant, b.Stream, b.ChainSeq)
	if b.RecordID != "" {
		fmt.Fprintf(&sb, " (record %s)", b.RecordID)
	}
	fmt.Fprintf(&sb, ": %s", b.Kind)
	if b.Kind == BreakGap {
		fmt.Fprintf(&sb, ", expected sequence %d", b.ExpectedSeq)
	}
	if b.Want != "" || b.Got != "" {
		fmt.Fprintf(&sb, "; want %s, got %s", or(b.Want, "(nothing)"), or(b.Got, "(nothing)"))
	}
	if b.Detail != "" {
		fmt.Fprintf(&sb, "; %s", b.Detail)
	}
	return sb.String()
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// verifyLink checks one record against its predecessor and its own bytes.
//
// The order of the checks is the order an investigator wants them: a gap first
// because a deletion is the loudest finding, then the link, then the bytes.
func verifyLink(tenant, stream string, prev, rec store.AuditRecord, expectedSeq int64) *Break {
	if rec.ChainSeq != expectedSeq {
		return &Break{
			Tenant: tenant, Stream: stream, Kind: BreakGap,
			ChainSeq: rec.ChainSeq, ExpectedSeq: expectedSeq, RecordID: rec.RecordID,
			Detail: "a record was removed, or a sequence was rewritten",
		}
	}
	if rec.PrevHash != prev.Hash {
		return &Break{
			Tenant: tenant, Stream: stream, Kind: BreakPrevMismatch,
			ChainSeq: rec.ChainSeq, RecordID: rec.RecordID,
			Want: prev.Hash, Got: rec.PrevHash,
			Detail: "this record does not chain onto the one in front of it",
		}
	}
	if want := Link(tenant, stream, rec.ChainSeq, rec.PrevHash, rec.Body); want != rec.Hash {
		return &Break{
			Tenant: tenant, Stream: stream, Kind: BreakHashMismatch,
			ChainSeq: rec.ChainSeq, RecordID: rec.RecordID,
			Want: want, Got: rec.Hash,
			Detail: "the stored bytes do not produce the stored hash",
		}
	}
	if !strings.HasPrefix(rec.Body, `{"v":"`+bodyVersion+`"`) {
		return &Break{
			Tenant: tenant, Stream: stream, Kind: BreakUnknownVersion,
			ChainSeq: rec.ChainSeq, RecordID: rec.RecordID,
			Detail: "this build cannot read the record's encoding; the link verified, the content did not",
		}
	}
	return nil
}
