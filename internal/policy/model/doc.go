// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

// Package model is the policy bundle as data: parsing, validation, and
// versioning of the authored document (PLAN M3). It knows the closed input and
// output vocabularies and rejects anything outside them at authoring time,
// which is what makes an unreachable or contradictory rule a compile error.
package model
