// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

//go:build race

package eval_test

// measurementBudget under `-race`, which is the only way `make test` ever runs
// these (PLAN §8).
//
// THE RACE DETECTOR IS NOT FREE AND THE BUDGET HAS TO SAY SO. It instruments
// every memory access on the path, and phase 0007 measured the cost at 6-9x
// for this workload: the 2,000-rule case is ~27µs in a normal build and ~244µs
// here. Holding an instrumented measurement to evaluationBudget compares two
// different things, and it left ~19% of headroom where the plain build has an
// order of magnitude — which is why this test used to fail for reasons that had
// nothing to do with the evaluator.
//
// The multiplier restores the SHAPE of the original number rather than picking
// a value that cannot fail: ten times the production budget is a little over
// ten times the instrumented measurement, the same relationship
// evaluationBudget has to the 27µs figure. A genuine order-of-magnitude
// regression still fails here.
//
// What this does NOT do is weaken the other two assertions. Linearity is a
// ratio, so the detector's constant factor cancels out of it entirely, and the
// allocation check has no clock in it at all. Both run unchanged under both
// builds, and between them they are what would catch a real regression.
const measurementBudget = 10 * evaluationBudget

const measurementMode = "under the race detector"
