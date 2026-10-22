package paper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// 正式题目可以没有导入文件；引用取自仓储当前题目及答案版本，不能伪造成文件来源。
// Python 只能检查网关提供的绑定格式，授权与快照前后复核仍由网关完成。
type questionRubricReference struct {
	QuestionID     string `json:"question_id"`
	ExamID         string `json:"exam_id"`
	SourceType     string `json:"source_type"`
	SourceID       string `json:"source_id"`
	SourceVersion  string `json:"source_version"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
}

func newQuestionRubricReference(question Question, snapshot []byte) (*questionRubricReference, error) {
	digest := sha256.Sum256(snapshot)
	reference := &questionRubricReference{QuestionID: question.ID, ExamID: question.ExamID, SnapshotSHA256: hex.EncodeToString(digest[:])}
	if solution := question.Solution; solution != nil && (len(mathRubricEvidenceSteps(solution.Steps)) > 0 || len(mathRubricTextSteps(solution.RawText)) > 0) {
		reference.SourceType, reference.SourceID, reference.SourceVersion = "question_solution", solution.ID, solution.SolutionVersion
	} else if answer := question.AnswerKey; answer != nil {
		reference.SourceType, reference.SourceID, reference.SourceVersion = "answer_key", answer.ID, answer.AnswerVersion
	}
	for _, value := range []string{reference.QuestionID, reference.ExamID, reference.SourceID, reference.SourceVersion} {
		if value == "" || value != strings.TrimSpace(value) {
			return nil, ErrInvalidInput
		}
	}
	return reference, nil
}

func validateQuestionRubricReference(result MathRubricDraftResponse, expected *questionRubricReference) error {
	if expected == nil {
		return nil
	}
	if len(result.SuggestedRubricCandidates) != 1 {
		return errors.New("math rubric draft question reference invalid")
	}
	data, err := json.Marshal(result.SuggestedRubricCandidates[0].Provenance["question_reference"])
	if err != nil {
		return errors.New("math rubric draft question reference invalid")
	}
	var actual questionRubricReference
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&actual); err != nil || actual != *expected {
		return errors.New("math rubric draft question reference invalid")
	}
	return nil
}
