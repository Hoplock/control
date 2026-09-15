// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/hoplock/control/internal/contract"
)

const groupLogs = "logs (POST /v1/logs/{batch,priority})"

// CheckLogs grades the two ingest paths and the obligation the batch one's
// status code cannot express.
func (s *Suite) CheckLogs() {
	e := &s.expect.Logs

	s.run(groupLogs, "a batch answers 202 and counts the records it stored", func(c *Case) {
		records := s.logRecords(e.BatchSize)
		r, err := s.postObject(contract.PathLogsBatch, contract.LogBatchRequest{Records: records})
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 202,
			"want 202 (accepted for storage), got %d: %s", r.status, snippet(r.body))

		var got contract.LogBatchResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(r.has("accepted"), "response omits accepted, which is required")
		c.require(got.Accepted == int32(len(records)),
			"sent %d fresh records and the server accepted %d", len(records), got.Accepted)
	})

	s.run(groupLogs, "the same batch replayed does not double-count", func(c *Case) {
		// A proxy draining a disk buffer will resend. Idempotency is on
		// record_id, and the only black-box way to see it is the count.
		records := s.logRecords(e.BatchSize)

		first, err := s.postObject(contract.PathLogsBatch, contract.LogBatchRequest{Records: records})
		c.must(err == nil, "first request failed: %v", err)
		c.must(first.status == 202, "first batch: want 202, got %d: %s", first.status, snippet(first.body))
		var got1 contract.LogBatchResponse
		c.must(first.into(&got1) == nil, "undecodable body: %s", snippet(first.body))
		c.must(got1.Accepted == int32(len(records)),
			"first batch of %d fresh records accepted %d", len(records), got1.Accepted)

		second, err := s.postObject(contract.PathLogsBatch, contract.LogBatchRequest{Records: records})
		c.must(err == nil, "replay failed: %v", err)
		c.must(second.status == 202, "replay: want 202, got %d: %s", second.status, snippet(second.body))
		var got2 contract.LogBatchResponse
		c.must(second.into(&got2) == nil, "undecodable body: %s", snippet(second.body))
		c.require(got2.Accepted == 0,
			"the same %d records replayed were accepted %d more times; fewer than sent means duplicates, and all of these were duplicates",
			len(records), got2.Accepted)
	})

	s.run(groupLogs, "a priority record answers 200 and is retrievable immediately after the ack", func(c *Case) {
		// The ack means DURABLE. Acking before the write lands turns the
		// guarantee the proxy acts on into a lie that only shows up after an
		// incident — so this case does not stop at the status code. The read
		// path is a suite input (logs.read_url), which is what keeps the
		// assertion black-box: the contract defines no read endpoint.
		rec := s.logRecords(1)[0]
		rec.Severity = contract.SeverityCritical
		rec.Kind = "command"
		rec.Message = "pdpconform durability probe"

		r, err := s.postObject(contract.PathLogsPriority, contract.LogPriorityRequest{Record: rec})
		c.must(err == nil, "request failed: %v", err)
		c.must(r.status == 200,
			"want 200 (the ack means durable), got %d: %s", r.status, snippet(r.body))

		var got contract.LogPriorityResponse
		c.must(r.into(&got) == nil, "undecodable body: %s", snippet(r.body))
		c.require(r.has("accepted"), "response omits accepted, which is required")
		c.require(got.Accepted, "accepted is false on a 200; the ack is what the proxy acts on")

		read, err := s.do(http.MethodGet, e.ReadURL, nil, s.token)
		c.must(err == nil, "reading back from %s failed: %v", e.ReadURL, err)
		c.must(read.status == 200, "read path %s answered %d: %s", e.ReadURL, read.status, snippet(read.body))
		c.require(strings.Contains(string(read.body), rec.RecordID),
			"record %s was acked and is not in the read path %s straight afterwards; the ack claimed a durability the server did not have",
			rec.RecordID, e.ReadURL)
	})
}

func (s *Suite) logRecords(n int) []contract.LogRecord {
	sessionID := s.nextID("pdpconform-session")
	out := make([]contract.LogRecord, n)
	for i := range out {
		out[i] = contract.LogRecord{
			RecordID:  s.nextID("pdpconform-record"),
			SessionID: sessionID,
			Timestamp: nowRFC3339(),
			Kind:      "session_start",
			Severity:  contract.SeverityInfo,
			Message:   "pdpconform conformance record",
			Subject:   "pdpconform@example.invalid",
		}
	}
	return out
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
