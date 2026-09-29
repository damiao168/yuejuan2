package exam

import "sort"

// CanChangeCandidateRoster 返回仍可按当前学籍重建名册的考试阶段。
// 准备确认后快照代表已确认的名册，班级变更不能再替换它。
func CanChangeCandidateRoster(status string) bool {
	return status == "draft" || status == "configured"
}

// classIDSetsEqual 将顺序或重复不同但集合相同的输入视为同一次请求。
// 这样不会因等价列表删除并重建已冻结的名册。
func classIDSetsEqual(left, right []string) bool {
	left = normalizedClassIDs(left)
	right = normalizedClassIDs(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func normalizedClassIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}
