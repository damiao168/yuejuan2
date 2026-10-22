package paper

import (
	"math"
	"strings"
)

var validQuestionTypes = map[string]bool{
	"single_choice":   true,
	"multiple_choice": true,
	"true_false":      true,
	"fill_blank":      true,
	"numeric":         true,
	"formula":         true,
	"short_answer":    true,
	"calculation":     true,
	"essay":           true,
	"discussion":      true,
	"coding":          true,
}

var validRubricStatuses = map[string]bool{
	"draft":          true,
	"pending_review": true,
	"approved":       true,
	"locked":         true,
}

func IsValidQuestionType(kind string) bool {
	return validQuestionTypes[kind]
}

func IsValidRubricStatus(status string) bool {
	return validRubricStatuses[status]
}

func SumRubricPoints(points []RubricPoint) float64 {
	total := 0.0
	for _, point := range points {
		total += point.Score
	}
	return total
}

// ValidRubricPointIdentities 检查同一细则中稳定、唯一的采分点标识。
// ID 会被评分证据引用，空值、首尾空格或重复值会让证据无法明确对应到一个采分点。
func ValidRubricPointIdentities(points []RubricPoint) bool {
	seen := make(map[string]bool, len(points))
	for _, point := range points {
		if point.ID == "" || point.ID != strings.TrimSpace(point.ID) || seen[point.ID] {
			return false
		}
		seen[point.ID] = true
	}
	return true
}

func ValidRubricEvidenceRequirements(points []RubricPoint) bool {
	for _, point := range points {
		for _, requirement := range point.EvidenceRequirements {
			if !validEvidenceRequirement(requirement, 0) {
				return false
			}
		}
	}
	return true
}

// 证据条件来自可编辑配置，限制嵌套深度和子项数量，避免无界递归及过宽条件树。
func validEvidenceRequirement(requirement EvidenceRequirement, depth int) bool {
	if depth > 8 {
		return false
	}
	valid := map[string]bool{"all_of": true, "any_of": true, "at_least": true, "valid_transformation": true, "final_result": true, "concept": true, "unit": true, "domain": true}
	if !valid[requirement.Type] {
		return false
	}
	if requirement.Type == "all_of" || requirement.Type == "any_of" || requirement.Type == "at_least" {
		if len(requirement.Children) == 0 || len(requirement.Children) > 32 || (requirement.Type == "at_least" && (requirement.Minimum <= 0 || requirement.Minimum > len(requirement.Children))) {
			return false
		}
		for _, child := range requirement.Children {
			if !validEvidenceRequirement(child, depth+1) {
				return false
			}
		}
		return true
	}
	return requirement.Type == "valid_transformation" || requirement.Type == "final_result" || requirement.Target != ""
}

func scoreEqual(a float64, b float64) bool {
	return math.Abs(a-b) < 0.0001
}
