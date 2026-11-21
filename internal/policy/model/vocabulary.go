// Copyright (c) 2026 Mauro Silva
// SPDX-License-Identifier: Apache-2.0

package model

// THE CLOSED VOCABULARY, AS DATA (0066; PLAN M3). Every enumerated field of a
// bundle takes its value from one of the kinds in kind.go, and Vocabularies
// lists each kind's members in the order the kind declares them. The console's
// visual policy editor offers exactly these as its choices: it builds from a
// file generated from this function (ui/src/screens/policyVocabulary.json),
// held equal to it by a test in internal/authoring, so no option an author is
// offered is a list somebody typed by hand.

// Vocabularies is every closed vocabulary a bundle's value can be drawn
// from, by its identifier, each with its members in declared order.
func Vocabularies() map[Vocabulary][]string {
	out := make(map[Vocabulary][]string, len(vocabularyWords))
	for v := range vocabularyWords {
		out[v] = vocabularyMembers(v)
	}
	return out
}

// MatchAxes are the five input axes a rule's match is made of (§5.1), in the
// order a rule's match is read.
func MatchAxes() []string { return members(matchAxes) }

var matchAxes = []MatchAxis{AxisSubject, AxisDevice, AxisContext, AxisTarget, AxisGrant}

func vocabularyMembers(v Vocabulary) []string {
	switch v {
	case VocabularyEffect:
		return members(effects)
	case VocabularyRouteIntent:
		return members(routeIntents)
	case VocabularyChannelType:
		return members(channelTypes)
	case VocabularyRequestType:
		return members(requestTypes)
	case VocabularyCredentialMethod:
		return members(credentialMethods)
	case VocabularyCredentialKind:
		return members(credentialKinds)
	case VocabularyExpiryPosture:
		return members(expiryPostures)
	case VocabularyAlgorithmProfile:
		return members(algorithmProfiles)
	case VocabularyAlgorithmFloor:
		return members(algorithmFloors)
	case VocabularyFilterMode:
		return members(filterModes)
	case VocabularyExecMode:
		return members(execModes)
	case VocabularyFilterAction:
		return members(filterActions)
	case VocabularyCommandForm:
		return members(commandForms)
	case VocabularyArgumentKind:
		return members(argumentKinds)
	case VocabularyExecutionRung:
		return members(executionRungs)
	case VocabularyReachRung:
		return members(reachRungs)
	case VocabularyObligationKind:
		return members(obligationKinds)
	case VocabularyGrantOrigin:
		return members(grantOrigins)
	case VocabularyUnanswered:
		return members(unansweredAnswers)
	case VocabularyCacheKeyComponent:
		return members(cacheKeyComponents)
	case VocabularyAuthMethod:
		return members(authMethods)
	case VocabularyDay:
		return members(days)
	case VocabularyUsernameSource:
		return members(usernameSources)
	}
	// Unreachable: the switch is exhaustive (the `exhaustive` linter), and
	// TestEveryVocabularyHasMembers refuses an empty answer.
	return nil
}

func members[T ~string](set []T) []string {
	out := make([]string, len(set))
	for i, m := range set {
		out[i] = string(m)
	}
	return out
}
