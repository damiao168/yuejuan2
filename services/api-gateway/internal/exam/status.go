package exam

var validStatuses = map[string]bool{
	"draft":      true,
	"configured": true,
	"ready":      true,
	"collecting": true,
	"grading":    true,
	"reviewing":  true,
	"finalized":  true,
	"published":  true,
	"archived":   true,
}

var validGradingModes = map[string]bool{
	"auto_objective_only":   true,
	"ai_assisted":           true,
	"human_review_required": true,
	"double_mark":           true,
	"blind_double_mark":     true,
}

var allowedTransitions = map[string]map[string]bool{
	"draft": {
		"configured": true,
	},
	"ready": {
		"configured": true,
	},
	"collecting": {
		"grading": true,
	},
	"grading": {
		"reviewing": true,
	},
	"reviewing": {
		"finalized": true,
	},
}

func IsValidStatus(status string) bool {
	return validStatuses[status]
}

func IsValidGradingMode(mode string) bool {
	return validGradingModes[mode]
}

func CanTransition(from string, to string) bool {
	// 发布由成绩质量与确认流程控制；归档使用独立元数据和整场资格，普通状态转换不能替代它们。
	if to == "published" || to == "archived" {
		return false
	}
	if from == to {
		return true
	}
	return allowedTransitions[from][to]
}

func IsCoreLocked(status string) bool {
	return status == "published" || status == "archived"
}
