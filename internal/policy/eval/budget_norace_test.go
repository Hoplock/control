// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

//go:build !race

package eval_test

// measurementBudget is what TestEvaluationIsLinearAndWithinBudget holds the
// worst case to in a normal build: evaluationBudget itself, because what is
// being measured is the code that ships.
const measurementBudget = evaluationBudget

const measurementMode = "without the race detector"
