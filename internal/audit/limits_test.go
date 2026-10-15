// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoplock/control/internal/config"
	"github.com/hoplock/control/internal/contract"
)

// The configuration restates the shipped bounds, because it imports nothing of
// the server's and has to know how large one record can be to refuse a log body
// limit below it. This holds the two to each other.
func TestTheConfigurationKnowsTheShippedBounds(t *testing.T) {
	if DefaultLimits.MaxRecordBytes != config.DefaultAuditMaxRecordBytes ||
		DefaultLimits.MaxCaptureBytes != config.DefaultAuditMaxCaptureBytes {
		t.Fatalf("audit.DefaultLimits = %+v, and the configuration takes record %d, capture %d",
			DefaultLimits, config.DefaultAuditMaxRecordBytes, config.DefaultAuditMaxCaptureBytes)
	}
}

// The floor config.MinLogBodyBytes refuses below is enough for the largest
// record the shipped bounds store: a full capture beside a body near its own
// limit, on either log path.
func TestTheLargestStorableRecordFitsTheLogBodyFloor(t *testing.T) {
	rec := aRecord()
	rec.Kind = "stream"
	rec.Payload = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("\x1b[0m$ ", DefaultLimits.MaxCaptureBytes/6)))
	rec.Attributes = map[string]string{"note": strings.Repeat("x", DefaultLimits.MaxRecordBytes-1024)}
	if _, err := Parse(rec, testTenant, 0, DefaultLimits); err != nil {
		t.Fatalf("the record is not one this server stores, so it proves nothing: %v", err)
	}

	floor := config.MinLogBodyBytes(config.AuditConfig{})
	for name, body := range map[string]any{
		contract.PathLogsBatch:    contract.LogBatchRequest{Records: []contract.LogRecord{rec}},
		contract.PathLogsPriority: contract.LogPriorityRequest{Record: rec},
	} {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(raw)) > floor {
			t.Errorf("%s: one storable record is %d bytes on the wire, above the %d floor", name, len(raw), floor)
		}
	}
}
