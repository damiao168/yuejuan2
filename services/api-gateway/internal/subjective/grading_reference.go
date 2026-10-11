package subjective

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
		if text, ok := source.Answer.StandardAnswer.(string); ok {
			reference.AnswerParts = gradingAnswerParts(text)
		}
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

var answerPartMarker = regexp.MustCompile(`(?:^|[。；;\n]\s*)[（(](\d{1,2})[）)]`)

// 只拆分从 (1) 开始且编号连续的答案段落，保留原答案和完整解析。
// 区间、方程编号和没有明确序号的文字不能据此推断子题，更不能生成缺失答案。
func gradingAnswerParts(text string) []GradingAnswerPart {
	matches := answerPartMarker.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 || strings.TrimSpace(text[:matches[0][0]]) != "" {
		return nil
	}
	parts := make([]GradingAnswerPart, 0, len(matches))
	for i, match := range matches {
		no, _ := strconv.Atoi(text[match[2]:match[3]])
		if no != i+1 {
			return nil
		}
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		content := strings.TrimSpace(text[match[1]:end])
		if content == "" {
			return nil
		}
		parts = append(parts, GradingAnswerPart{PartNo: no, AnswerText: content})
	}
	return parts
}
