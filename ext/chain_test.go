// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext_test

import (
	"testing"

	"github.com/hoplock/control/ext"
)

// ChainLink's encoding is a promise an archive verifies years-old records
// against, so it is pinned by a vector computed outside Go: SHA-256 over
// `15:hoplock.chain/1` `8:tenant-a` `8:proxy:p1` `1:2` `10:sha256:abc` and the
// 23-byte body, each length-prefixed. A change to it fails here, and the fix is
// a new ChainVersion and a new function, never a new vector.
func TestTheChainLinkEncodingIsPinned(t *testing.T) {
	t.Parallel()
	got := ext.ChainLink("tenant-a", "proxy:p1", 2, "sha256:abc", `{"v":"hoplock.audit/1"}`)
	const want = "sha256:777ba63b22931050109ed67a09da68b8eed3c324f9088aaedc2880915bc6b116"
	if got != want {
		t.Fatalf("ChainLink = %s, want %s", got, want)
	}
	if ext.ChainVersion != "hoplock.chain/1" {
		t.Fatalf("ChainVersion = %q; a later encoding is a new version beside this one", ext.ChainVersion)
	}
}

// Every input is inside the link: changing any one of them changes it, which
// is what stops a record from being moved to another tenant, stream or
// position and still verifying (M18).
func TestEveryPartOfALinkIsCovered(t *testing.T) {
	t.Parallel()
	base := ext.ChainLink("t", "s", 3, "p", "b")
	for name, got := range map[string]string{
		"tenant":   ext.ChainLink("u", "s", 3, "p", "b"),
		"stream":   ext.ChainLink("t", "x", 3, "p", "b"),
		"sequence": ext.ChainLink("t", "s", 4, "p", "b"),
		"prev":     ext.ChainLink("t", "s", 3, "q", "b"),
		"body":     ext.ChainLink("t", "s", 3, "p", "c"),
		// The length prefixes: moving a byte from the stream to the
		// tenant is a different tuple, and a different link.
		"boundary": ext.ChainLink("ts", "", 3, "p", "b"),
	} {
		if got == base {
			t.Errorf("changing the %s left the link unchanged", name)
		}
	}
}
