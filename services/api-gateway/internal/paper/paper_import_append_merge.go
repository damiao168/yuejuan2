package paper

// mergePaperImportAppendResult keeps the accepted candidate history when a run
// only adds sources. New material replaces the same normalized question number
// within its own candidate kind and otherwise appends to the prior result.
func mergePaperImportAppendResult(previous PaperImportJob, fresh PaperImportParseResult) PaperImportParseResult {
	fresh.QuestionCandidates = mergeQuestionCandidates(previous.QuestionCandidates, fresh.QuestionCandidates)
	fresh.AnswerCandidates = mergeAnswerCandidates(previous.AnswerCandidates, fresh.AnswerCandidates)
	fresh.SolutionCandidates = mergeSolutionCandidates(previous.SolutionCandidates, fresh.SolutionCandidates)
	fresh.RubricCandidates = mergeRubricCandidates(previous.RubricCandidates, fresh.RubricCandidates)
	return fresh
}

func appendCandidateKey(number, fallback string) string {
	if normalized := normalizePaperImportQuestionNumber(number); normalized != "" {
		return "number:" + normalized
	}
	return "candidate:" + fallback
}

func mergeQuestionCandidates(previous, fresh []QuestionCandidate) []QuestionCandidate {
	out := append([]QuestionCandidate{}, previous...)
	positions := map[string]int{}
	for index, candidate := range out {
		positions[appendCandidateKey(firstNonEmpty(candidate.QuestionNoNormalized, candidate.QuestionNoRaw), candidate.CandidateID)] = index
	}
	for _, candidate := range fresh {
		key := appendCandidateKey(firstNonEmpty(candidate.QuestionNoNormalized, candidate.QuestionNoRaw), candidate.CandidateID)
		if index, ok := positions[key]; ok {
			out[index] = candidate
		} else {
			positions[key] = len(out)
			out = append(out, candidate)
		}
	}
	return out
}

func mergeAnswerCandidates(previous, fresh []AnswerCandidate) []AnswerCandidate {
	out := append([]AnswerCandidate{}, previous...)
	positions := map[string]int{}
	for index, candidate := range out {
		positions[appendCandidateKey(firstNonEmpty(candidate.QuestionNoNormalized, candidate.QuestionNoHint), candidate.CandidateID)] = index
	}
	for _, candidate := range fresh {
		key := appendCandidateKey(firstNonEmpty(candidate.QuestionNoNormalized, candidate.QuestionNoHint), candidate.CandidateID)
		if index, ok := positions[key]; ok {
			out[index] = candidate
		} else {
			positions[key] = len(out)
			out = append(out, candidate)
		}
	}
	return out
}

func mergeSolutionCandidates(previous, fresh []SolutionCandidate) []SolutionCandidate {
	out := append([]SolutionCandidate{}, previous...)
	positions := map[string]int{}
	for index, candidate := range out {
		positions[appendCandidateKey(firstNonEmpty(candidate.QuestionNoNormalized, candidate.QuestionNoHint), candidate.CandidateID)] = index
	}
	for _, candidate := range fresh {
		key := appendCandidateKey(firstNonEmpty(candidate.QuestionNoNormalized, candidate.QuestionNoHint), candidate.CandidateID)
		if index, ok := positions[key]; ok {
			out[index] = candidate
		} else {
			positions[key] = len(out)
			out = append(out, candidate)
		}
	}
	return out
}

func mergeRubricCandidates(previous, fresh []RubricCandidate) []RubricCandidate {
	out := append([]RubricCandidate{}, previous...)
	positions := map[string]int{}
	for index, candidate := range out {
		positions[appendCandidateKey(firstNonEmpty(candidate.QuestionNoNormalized, candidate.QuestionNoHint), candidate.CandidateID)] = index
	}
	for _, candidate := range fresh {
		key := appendCandidateKey(firstNonEmpty(candidate.QuestionNoNormalized, candidate.QuestionNoHint), candidate.CandidateID)
		if index, ok := positions[key]; ok {
			out[index] = candidate
		} else {
			positions[key] = len(out)
			out = append(out, candidate)
		}
	}
	return out
}
