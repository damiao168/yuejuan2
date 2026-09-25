package paper

import "sort"

// Configuration accepts more question types than either scoring path implements.
// A paper that only passes the blocking readiness checks can therefore still
// contain questions no automated path will ever score, and that gap is only
// discovered when the scoring run reaches them. The two mirrors below make the
// gap describable before the exam starts; drift tests in the grading and
// subjective packages fail if either path changes without updating them.

// ruleGradedQuestionTypes mirrors the switch in grading.Engine.Grade.
var ruleGradedQuestionTypes = map[string]bool{
	"single_choice":   true,
	"multiple_choice": true,
	"true_false":      true,
	"fill_blank":      true,
	"numeric":         true,
}

// aiAssistedQuestionTypes mirrors subjective.supportedQuestionTypes. A human
// still confirms every one of these, but the reviewer starts from a suggested
// score instead of a blank page.
var aiAssistedQuestionTypes = map[string]bool{
	"short_answer": true,
	"calculation":  true,
	"essay":        true,
	"discussion":   true,
}

// HasAutomatedScoringPath reports whether a question type reaches any automated
// scoring path. Types that do not are graded by a human for every submission.
func HasAutomatedScoringPath(questionType string) bool {
	return ruleGradedQuestionTypes[questionType] || aiAssistedQuestionTypes[questionType]
}

// RuleGradedQuestionTypes lists the types the rule engine claims to score.
func RuleGradedQuestionTypes() []string {
	return sortedKeys(ruleGradedQuestionTypes)
}

// AIAssistedQuestionTypes lists the types the subjective adapter claims to score.
func AIAssistedQuestionTypes() []string {
	return sortedKeys(aiAssistedQuestionTypes)
}

// ManualOnlyQuestionTypes lists the configurable question types that no
// automated scoring path supports.
func ManualOnlyQuestionTypes() []string {
	out := []string{}
	for questionType := range validQuestionTypes {
		if !HasAutomatedScoringPath(questionType) {
			out = append(out, questionType)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
