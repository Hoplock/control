// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

import "regexp"

// The shape rules for a `device_field.<name>` parameter (PLAN §5.2).
//
// They are the whole of what is checked here, and that is the point. The
// contract enumerates no field names and never will, because customer-written
// drivers are first-class (proxy D13): a closed list here would make an
// estate's own driver unauthorable without a release of this server. So shape
// is checkable and meaning is the driver's — an unrecognised *name* is a
// capability question this compiler cannot answer (M17, phase 0006) and, on the
// proxy, a skipped rung rather than an error.
//
// These constants duplicate the ones in internal/contract on purpose: this
// package does not import the wire types, and contract_agreement_test.go
// asserts the two agree. A duplicate a test compares is cheaper than an import
// that makes the compiler inseparable from the transport.
const (
	// DeviceFieldMaxNameLen bounds `<name>`.
	DeviceFieldMaxNameLen = 64
	// DeviceFieldMaxValueLen bounds the value, which must also be non-empty.
	DeviceFieldMaxValueLen = 256
	// DeviceFieldMaxPerEntry bounds how many ride on one ladder entry.
	DeviceFieldMaxPerEntry = 16
	// DeviceFieldNamePattern is the name grammar.
	DeviceFieldNamePattern = `^[a-z0-9_-]{1,64}$`
)

var deviceFieldName = regexp.MustCompile(DeviceFieldNamePattern)

// ValidDeviceFieldName reports whether a device field name has the shape the
// contract specifies.
func ValidDeviceFieldName(name string) bool { return deviceFieldName.MatchString(name) }

// ValidDeviceFieldValue reports whether a device field value has the shape the
// contract specifies: non-empty and at most DeviceFieldMaxValueLen bytes.
func ValidDeviceFieldValue(value string) bool {
	return value != "" && len(value) <= DeviceFieldMaxValueLen
}
