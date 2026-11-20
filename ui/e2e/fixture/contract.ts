// The policy vocabulary a fixture declares on `/v1/authorize`: the one this
// server serves, read out of the vendored contract rather than spelled here.
//
// This server serves exactly one vocabulary (0064), so a fixture declaring any
// other is refused with a 5xx naming both numbers — and a fixture spelling the
// number would be a second copy of it, which the repository's version-literal
// check (version_literal_test.go) refuses. internal/contract/version.go is the
// one place the number is written, and its test ties it to the same sentence
// this reads, so the two cannot disagree without that test failing first.

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { UI } from "../harness";

const CURRENT = /The current vocabulary is\s+`([0-9]+)`/;

function vendored(): number {
  const doc = readFileSync(join(UI, "..", "contract", "control.yaml"), "utf8");
  const m = CURRENT.exec(doc);
  if (!m) throw new Error("contract/control.yaml states no current vocabulary; see internal/contract/version_test.go");
  return Number(m[1]);
}

/** The one policy vocabulary this server serves. */
export const POLICY_VERSION = vendored();
