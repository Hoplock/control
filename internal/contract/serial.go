// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"fmt"
	"regexp"
	"strconv"
)

// A certificate serial on the wire is a DECIMAL STRING (CertificateResponse.
// serial, `^[0-9]{1,20}$`), and so is the `credential_certificate_serial` a
// proxy stamps on its records. An SSH serial is a uint64 and JSON numbers are
// not safely integral above 2^53, so a serial carried as a number can lose its
// low digits in any peer's parser — and a serial that has lost digits is a join
// key that no longer joins. These two functions are the only rendering and the
// only parsing of one in this server: never through a float, and never "fixed"
// to a JSON number. cmd/pdpconform renders the serial it grades with strconv
// on purpose, because a grader that called the code it grades would agree
// with it by construction.

var serialPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

// FormatSerial renders a certificate serial as the contract's decimal string.
func FormatSerial(serial uint64) string { return strconv.FormatUint(serial, 10) }

// ParseSerial reads a decimal serial back, as an unsigned integer. A value that
// is not the contract's shape, or overflows a uint64, is refused.
func ParseSerial(s string) (uint64, error) {
	if !serialPattern.MatchString(s) {
		return 0, fmt.Errorf("contract: %q is not a decimal certificate serial", s)
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("contract: certificate serial %q does not fit an unsigned 64-bit integer", s)
	}
	return v, nil
}
