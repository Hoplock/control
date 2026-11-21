// THE POLICY VOCABULARY, AS THE SERVER STATES IT (0066; PLAN M3). Every
// enumerated choice the visual policy editor offers — an effect, an intent, a
// channel type, a rung, an obligation, a cache-key component — is read from
// here, and this reads policyVocabulary.json, which
// TestTheConsolesPolicyVocabularyIsTheServers (internal/authoring) generates
// from internal/policy/model's kinds and holds equal to them. No option list
// in the editor is typed by hand: test/policyVisual.test.ts reads the editor's
// source and refuses one.

import data from "./policyVocabulary.json";

export type VocabularyName = keyof typeof data.vocabularies;

/** A closed vocabulary's members, in the order the server declares them. */
export function vocabulary(name: VocabularyName): readonly string[] {
  return data.vocabularies[name];
}

/** A rule's five match axes, in the order a match is read (PLAN §5.1). */
export const MATCH_AXES: readonly string[] = data.match_axes;
