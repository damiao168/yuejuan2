package subjective

import (
	"encoding/json"
	"fmt"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type confirmedQuestionReference struct {
	Answer *struct {
		StandardAnswer    any   `json:"standard_answer"`
		EquivalentAnswers []any `json:"equivalent_answers"`
	} `json:"answer"`
	Solution *struct {
		RawText string               `json:"raw_text"`
		Steps   []paper.SolutionStep `json:"steps"`
	} `json:"solution"`
}

// 参考答案只从考试确认时冻结的快照读取；快照缺失或哈希不匹配时不向模型提供标准答案。
func referenceContextFromSnapshot(raw []byte, snapshotHash string) (*GradingReferenceContext, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var source confirmedQuestionReference
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("decode confirmed question reference: %w", err)
	}
	reference := &GradingReferenceContext{
		Source: "confirmed_exam_import_snapshot", SnapshotHash: snapshotHash,
		EquivalentAnswers: []any{}, SolutionSteps: []paper.SolutionStep{},
	}
	if source.Answer != nil {
		reference.StandardAnswer = source.Answer.StandardAnswer
		if source.Answer.EquivalentAnswers != nil {
			reference.EquivalentAnswers = source.Answer.EquivalentAnswers
		}
	}
	if source.Solution != nil {
		reference.SolutionText = source.Solution.RawText
		if source.Solution.Steps != nil {
			reference.SolutionSteps = source.Solution.Steps
		}
	}
	return reference, nil
}
