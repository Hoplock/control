// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The constants a running binary reports are the vendored copy's.
func TestDocumentVersionIsTheVendoredOne(t *testing.T) {
	doc := loadContract(t)
	if doc.Info.Version != DocumentVersion {
		t.Errorf("DocumentVersion = %q, the vendored document says %q: move the constant with the contract",
			DocumentVersion, doc.Info.Version)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "contract", "UPSTREAM"))
	if err != nil {
		t.Fatal(err)
	}
	var commit string
	for line := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "commit="); ok {
			commit = strings.TrimSpace(v)
		}
	}
	if commit != VendoredCommit {
		t.Errorf("VendoredCommit = %q, contract/UPSTREAM says %q", VendoredCommit, commit)
	}
}
