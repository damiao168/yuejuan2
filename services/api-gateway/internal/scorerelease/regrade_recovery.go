package scorerelease

import (
	"encoding/json"
	"sort"
)

func sameRegradeFacts(expected, actual []SubmissionFact) bool {
	// 比较整份冻结事实及评分来源ID，不能以同分、同名或相同说明回认另一项历史复评。
	normalize := func(items []SubmissionFact) []SubmissionFact {
		raw, _ := json.Marshal(items)
		var out []SubmissionFact
		_ = json.Unmarshal(raw, &out)
		for i := range out {
			sort.Slice(out[i].Questions, func(a, b int) bool { return out[i].Questions[a].QuestionID < out[i].Questions[b].QuestionID })
		}
		sort.Slice(out, func(i, j int) bool { return out[i].SubmissionID < out[j].SubmissionID })
		return out
	}
	left, _ := json.Marshal(normalize(expected))
	right, _ := json.Marshal(normalize(actual))
	return string(left) == string(right)
}
