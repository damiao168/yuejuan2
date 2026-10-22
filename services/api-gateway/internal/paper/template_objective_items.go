package paper

// 同一题在本页出现多个模型子区域时不能自动选其中一个；所有冲突大区一起降级。
func templateObjectiveItemCounts(candidates []TemplateAnalyzedRegion) map[string]int {
	counts := map[string]int{}
	for _, candidate := range candidates {
		for _, item := range candidate.ObjectiveItems {
			counts[item.PrintedQuestionLabel]++
		}
	}
	return counts
}

func templateObjectiveItemsValid(candidate TemplateAnalyzedRegion, objective map[string]bool, counts map[string]int) bool {
	if len(candidate.ObjectiveItems) > 256 {
		return false
	}
	for index, item := range candidate.ObjectiveItems {
		if !objective[item.PrintedQuestionLabel] || counts[item.PrintedQuestionLabel] != 1 || validateTemplateAnalysisBBox(item.BBox) != nil || !templateAnalysisBBoxContains(candidate.ProposedBBox, item.BBox) || len(item.Options) < 2 || len(item.Options) > 12 {
			return false
		}
		for _, other := range candidate.ObjectiveItems[index+1:] {
			if templateAnalysisBBoxesOverlap(item.BBox, other.BBox) {
				return false
			}
		}
		seen := map[string]bool{}
		for optionIndex, option := range item.Options {
			if len(option.Label) != 1 || option.Label[0] < 'A' || option.Label[0] > 'L' || seen[option.Label] || validateTemplateAnalysisBBox(option.BBox) != nil || !templateAnalysisBBoxContains(item.BBox, option.BBox) {
				return false
			}
			seen[option.Label] = true
			for _, other := range item.Options[optionIndex+1:] {
				if templateAnalysisBBoxesOverlap(option.BBox, other.BBox) {
					return false
				}
			}
		}
		for offset := range len(item.Options) {
			if !seen[string(rune('A'+offset))] {
				return false
			}
		}
	}
	return true
}

// 不同大区也不能借不同题号重复使用同一片填涂区；先计算冲突再降级，避免处理顺序影响结果。
func templateObjectiveConflictingGroups(candidates []TemplateAnalyzedRegion) map[int]bool {
	conflicts := map[int]bool{}
	for index, candidate := range candidates {
		if candidate.Role != "objective_group" {
			continue
		}
		for otherIndex := index + 1; otherIndex < len(candidates); otherIndex++ {
			other := candidates[otherIndex]
			if other.Role != "objective_group" {
				continue
			}
			for _, item := range candidate.ObjectiveItems {
				for _, otherItem := range other.ObjectiveItems {
					if templateAnalysisBBoxesOverlap(item.BBox, otherItem.BBox) {
						conflicts[index], conflicts[otherIndex] = true, true
					}
				}
			}
		}
	}
	return conflicts
}

// 统一采用整页坐标；1e-6 仅吸收 JSON 小数误差，不扩张框到相邻题或相邻选项。
func templateAnalysisBBoxContains(parent, child TemplateAnalysisBBox) bool {
	const epsilon = 1e-6
	return child.X >= parent.X-epsilon && child.Y >= parent.Y-epsilon && child.X+child.Width <= parent.X+parent.Width+epsilon && child.Y+child.Height <= parent.Y+parent.Height+epsilon
}

func templateAnalysisBBoxesOverlap(a, b TemplateAnalysisBBox) bool {
	const epsilon = 1e-6
	return min(a.X+a.Width, b.X+b.Width)-max(a.X, b.X) > epsilon && min(a.Y+a.Height, b.Y+b.Height)-max(a.Y, b.Y) > epsilon
}
