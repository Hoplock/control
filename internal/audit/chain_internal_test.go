// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/hoplock/control/ext"
)

// The retention seals are written by writeParts, and a link by ext.ChainLink,
// which documents the same encoding as a public promise (0025). The two are
// held to each other here, so a change to either is a failure rather than two
// encodings that drift apart.
func TestTheSealEncoderIsTheChainLinksEncoding(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		tenant, stream, prev, body string
		seq                        int64
	}{
		{"tenant-a", StreamControl, "", `{"v":"hoplock.audit/1"}`, 1},
		{"t", "proxy:p-1", "sha256:00", "é∑ multi-byte", 42},
		{"", "", "", "", 9_007_199_254_740_993},
	} {
		h := sha256.New()
		writeParts(h, chainVersion, c.tenant, c.stream, strconv.FormatInt(c.seq, 10), c.prev, c.body)
		want := "sha256:" + hex.EncodeToString(h.Sum(nil))
		if got := ext.ChainLink(ext.Tenant(c.tenant), c.stream, uint64(c.seq), c.prev, c.body); got != want {
			t.Errorf("ext.ChainLink(%+v) = %s, writeParts gives %s", c, got, want)
		}
		if got := Link(c.tenant, c.stream, c.seq, c.prev, c.body); got != want {
			t.Errorf("Link(%+v) = %s, want %s", c, got, want)
		}
	}
}
