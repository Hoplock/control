// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package ext_test

import (
	"testing"

	"github.com/hoplock/control/ext"
)

// Every member renders as its stable code, and a value outside the enum
// renders as something no algorithm is called (0063): a stale or corrupt value
// must never read as a real key type.
func TestKeyAlgorithmString(t *testing.T) {
	members := map[ext.KeyAlgorithm]string{
		ext.KeyAlgorithmEd25519:   "ed25519",
		ext.KeyAlgorithmECDSAP256: "ecdsa-p256",
		ext.KeyAlgorithmRSA4096:   "rsa-4096",
	}
	codes := map[string]bool{}
	for a, want := range members {
		if got := a.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(a), got, want)
		}
		codes[want] = true
	}
	for _, a := range []ext.KeyAlgorithm{-1, ext.KeyAlgorithmRSA4096 + 1, 99} {
		got := a.String()
		if got != "unknown" {
			t.Errorf("out-of-range %d.String() = %q, want \"unknown\"", int(a), got)
		}
		if codes[got] {
			t.Errorf("out-of-range %d renders as the real algorithm %q", int(a), got)
		}
	}
}
