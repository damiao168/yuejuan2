package goldpaper

import (
	"fmt"
	"sort"
)

// 调用方先筛选启用的样卷；这里只统计其当前已批准版本，任一覆盖缺口未满足就不能标记为 Ready。
func buildCoverage(examID, questionID, subjectCode, archetypeCode, riskTier string, maxScore float64, items []GoldPaper) Coverage {
	result := Coverage{ExamID: examID, QuestionID: questionID, SubjectCode: subjectCode, ArchetypeCode: archetypeCode, RiskTier: riskTier}
	bands := map[string]int{"zero": 0, "middle": 0, "full": 0}
	traitSet, tagSet := map[string]struct{}{}, map[string]struct{}{}
	for _, item := range items {
		for _, version := range item.Versions {
			if item.ActiveVersion != version.Version || version.ApprovedAt == nil {
				continue
			}
			result.ActiveApprovedCount++
			switch {
			case version.ReferenceScore == 0:
				bands["zero"]++
			case version.ReferenceScore == maxScore:
				bands["full"]++
			default:
				bands["middle"]++
			}
			for key, value := range version.TraitScores {
				traitSet[fmt.Sprintf("%s=%v", key, value)] = struct{}{}
			}
			for _, tag := range version.ErrorTags {
				tagSet[tag] = struct{}{}
			}
		}
	}
	result.ScoreBands = []ScoreBandCoverage{{Band: "zero", Count: bands["zero"]}, {Band: "middle", Count: bands["middle"]}, {Band: "full", Count: bands["full"]}}
	for value := range traitSet {
		result.TraitPatterns = append(result.TraitPatterns, value)
	}
	for value := range tagSet {
		result.ErrorTags = append(result.ErrorTags, value)
	}
	sort.Strings(result.TraitPatterns)
	sort.Strings(result.ErrorTags)
	if bands["zero"] == 0 {
		result.Gaps = append(result.Gaps, "missing_zero_score")
	}
	if bands["full"] == 0 {
		result.Gaps = append(result.Gaps, "missing_full_score")
	}
	if maxScore >= 2 && bands["middle"] == 0 {
		result.Gaps = append(result.Gaps, "missing_middle_score")
	}
	if len(result.ErrorTags) == 0 {
		result.Gaps = append(result.Gaps, "missing_typical_error")
	}
	if archetypeCode == "extended_response" && len(result.TraitPatterns) < 2 {
		result.Gaps = append(result.Gaps, "insufficient_trait_boundaries")
	}
	for _, tag := range requiredCoverageTags(subjectCode, archetypeCode) {
		if _, ok := tagSet[tag]; !ok {
			result.Gaps = append(result.Gaps, "missing_pattern:"+tag)
		}
	}
	result.Ready = result.ActiveApprovedCount > 0 && len(result.Gaps) == 0
	return result
}

func requiredCoverageTags(subjectCode, archetypeCode string) []string {
	if archetypeCode == "extended_response" && (subjectCode == "chinese" || subjectCode == "english") {
		return []string{"content_strong_language_weak", "language_strong_content_off_topic", "structural_imbalance"}
	}
	if subjectCode == "mathematics" && archetypeCode == "structured_steps" {
		return []string{"alternative_valid_path", "missing_key_step", "correct_conclusion_insufficient_process"}
	}
	if subjectCode == "physics" {
		return []string{"unit_error", "condition_error", "calculation_error"}
	}
	if subjectCode == "chemistry" {
		return []string{"unit_error", "condition_error", "balancing_error", "calculation_error"}
	}
	switch subjectCode {
	case "history", "geography", "ethics_politics", "biology":
		return []string{"insufficient_material_evidence", "relation_error"}
	default:
		return nil
	}
}
