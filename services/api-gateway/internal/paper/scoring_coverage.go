package paper

import "sort"

// 配置接受的题型多于两条评分路径实际支持的题型。
// 因此，仅通过阻断性准备度检查的试卷，仍可能含有自动路径永远不会评分的题目，直到评分运行遇到它们才发现缺口。
// 下方两份对应清单让考试开始前就能说明该缺口；grading 和 subjective 中的偏移测试会在路径改变却未同步清单时失败。

// ruleGradedQuestionTypes 与 grading.Engine.Grade 中的 switch 对应。
var ruleGradedQuestionTypes = map[string]bool{
	"single_choice":   true,
	"multiple_choice": true,
	"true_false":      true,
	"fill_blank":      true,
	"numeric":         true,
}

// aiAssistedQuestionTypes 与 subjective.supportedQuestionTypes 对应。
// 这些题目仍逐题由人工确认，但复核人员从建议分数开始，而非从空白开始。
var aiAssistedQuestionTypes = map[string]bool{
	"short_answer": true,
	"calculation":  true,
	"essay":        true,
	"discussion":   true,
}

// HasAutomatedScoringPath 判断题型是否进入任何自动评分路径。未进入的题型，每份提交都由人工评分。
func HasAutomatedScoringPath(questionType string) bool {
	return ruleGradedQuestionTypes[questionType] || aiAssistedQuestionTypes[questionType]
}

// RuleGradedQuestionTypes 列出规则引擎声明支持评分的题型。
func RuleGradedQuestionTypes() []string {
	return sortedKeys(ruleGradedQuestionTypes)
}

// AIAssistedQuestionTypes 列出主观题适配器声明支持评分的题型。
func AIAssistedQuestionTypes() []string {
	return sortedKeys(aiAssistedQuestionTypes)
}

// ManualOnlyQuestionTypes 列出可配置但任何自动评分路径都不支持的题型。
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
