package subjectcode

import "strings"

// Normalize 统一人员资格和考试配置中的学科代码；未知学科不能用于阅卷分配。
func Normalize(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "math", "数学":
		normalized = "mathematics"
	case "politics", "civics", "道德与法治", "思想政治", "政治":
		normalized = "ethics_politics"
	case "语文":
		normalized = "chinese"
	case "英语":
		normalized = "english"
	case "物理":
		normalized = "physics"
	case "化学":
		normalized = "chemistry"
	case "生物":
		normalized = "biology"
	case "历史":
		normalized = "history"
	case "地理":
		normalized = "geography"
	}
	switch normalized {
	case "chinese", "mathematics", "english", "physics", "chemistry", "biology", "history", "geography", "ethics_politics":
		return normalized, true
	default:
		return normalized, false
	}
}
