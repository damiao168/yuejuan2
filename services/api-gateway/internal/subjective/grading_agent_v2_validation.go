package subjective

import (
	"crypto/sha256"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"
)

// 标识检查后按题目、细则、模型策略的顺序检查评分上下文，多个缺陷仍返回原来的首个错误。
func validateGradingAgentV2Context(input AdapterInput, policy ModelPolicy) error {
	subject := gradingAgentSubject(input.Subject)
	gradeLevel := gradingAgentGradeLevel(input.GradeLevel)
	questionType := strings.ToLower(strings.TrimSpace(input.Question.QuestionType))
	rubricID := strings.TrimSpace(input.Rubric.ID)
	rubricVersion := strings.TrimSpace(input.Rubric.Version)
	modelVersion := strings.TrimSpace(policy.ModelVersion)
	promptVersion := strings.TrimSpace(policy.PromptVersion)
	if !gradingAgentV2Subjects[subject] ||
		!gradingAgentV2GradeLevels[gradeLevel] ||
		!gradingAgentV2QuestionTypes[questionType] ||
		!boundedNonBlank(input.Question.Stem, 12_000) ||
		!boundedNonBlank(input.AnswerText, 20_000) ||
		math.IsNaN(input.Question.Score) ||
		math.IsInf(input.Question.Score, 0) ||
		input.Question.Score <= 0 ||
		input.Question.Score > 1_000 {
		return &GradingAgentError{Code: "grading_context_incomplete"}
	}
	if !boundedNonBlank(rubricID, 128) ||
		!boundedNonBlank(rubricVersion, 128) ||
		len(input.Rubric.Points) == 0 ||
		len(input.Rubric.Points) > 100 ||
		len(input.Rubric.Deductions) > 100 ||
		len(input.Rubric.Examples) > 100 ||
		math.IsNaN(input.Rubric.MaxScore) ||
		math.IsInf(input.Rubric.MaxScore, 0) ||
		input.Rubric.MaxScore <= 0 ||
		input.Rubric.MaxScore > 1_000 ||
		!almostEqual(input.Rubric.MaxScore, input.Question.Score) {
		return &GradingAgentError{Code: "rubric_context_incomplete"}
	}
	if !boundedNonBlank(modelVersion, 256) ||
		!boundedNonBlank(promptVersion, 128) ||
		math.IsNaN(policy.MinConfidence) ||
		math.IsInf(policy.MinConfidence, 0) ||
		policy.MinConfidence <= 0 ||
		policy.MinConfidence > 1 {
		return &GradingAgentError{Code: "grading_v2_policy_invalid"}
	}
	return nil
}

// 只在副本上把缺失数组归一为空数组；nil参考沿用历史考试的省略方式。
func normalizedGradingReferenceForV2(reference *GradingReferenceContext) *GradingReferenceContext {
	if reference == nil {
		return nil
	}
	copyReference := *reference
	copyReference.EquivalentAnswers = nonNilAny(copyReference.EquivalentAnswers)
	if copyReference.SolutionSteps == nil {
		copyReference.SolutionSteps = []paper.SolutionStep{}
	}
	return &copyReference
}

// 数量超限先于输出权限报错，单条信号检查随后执行；保留调用方既有的错误优先级。
func validateGradingAgentV2OutputRules(guard PromptGuard, constraint aieligibility.OutputConstraint) error {
	if len(guard.Signals) > 32 {
		return &GradingAgentError{Code: "grading_v2_prompt_guard_invalid"}
	}
	if !constraint.CriteriaEvidenceOnly || constraint.AllowModelFinalScore ||
		constraint.FinalScoreAuthority != "server_rubric_or_human_confirmation" {
		return &GradingAgentError{Code: "grading_v2_output_constraint_invalid"}
	}
	for _, signal := range guard.Signals {
		if !boundedNonBlank(signal, 128) {
			return &GradingAgentError{Code: "grading_v2_prompt_guard_invalid"}
		}
	}
	return nil
}

// 细则点按原顺序投影；ID去空白后判重，各点分值相加仍须等于冻结细则的满分。
func gradingAgentV2RubricPoints(rubric paper.Rubric) ([]gradingAgentRubricPoint, error) {
	points := make([]gradingAgentRubricPoint, 0, len(rubric.Points))
	pointIDs := make(map[string]bool, len(rubric.Points))
	pointTotal := 0.0
	for _, point := range rubric.Points {
		pointID := strings.TrimSpace(point.ID)
		if !boundedNonBlank(pointID, 128) ||
			!boundedNonBlank(point.Description, 4_000) ||
			pointIDs[pointID] ||
			math.IsNaN(point.Score) ||
			math.IsInf(point.Score, 0) ||
			point.Score <= 0 ||
			point.Score > 1_000 {
			return nil, &GradingAgentError{Code: "rubric_context_invalid"}
		}
		pointIDs[pointID] = true
		pointTotal += point.Score
		points = append(points, gradingAgentRubricPoint{
			ID:               pointID,
			Description:      point.Description,
			Score:            point.Score,
			Required:         point.Required,
			Aliases:          []string{},
			EvidenceRequired: true,
			MatchPolicy:      "semantic",
		})
	}
	if !almostEqual(pointTotal, rubric.MaxScore) {
		return nil, &GradingAgentError{Code: "rubric_context_invalid"}
	}
	return points, nil
}

func boundedNonBlank(value string, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return strings.TrimSpace(value) != "" && length <= maximum
}

func validateResolvedActiveCropForV2(crop ResolvedActiveCrop) error {
	if crop.Kind != "answer_segment_crop" ||
		crop.MediaType != "image/png" ||
		crop.ByteSize <= 0 ||
		crop.ByteSize > maxActiveCropBytes ||
		crop.ByteSize != int64(len(crop.Data)) ||
		!validLowerSHA256(crop.SHA256) {
		return &GradingAgentError{Code: "grading_v2_crop_invalid"}
	}
	width, height, err := validateActiveCropPNG(crop.Data)
	if err != nil ||
		width != crop.WidthPixels ||
		height != crop.HeightPixels {
		return &GradingAgentError{Code: "grading_v2_crop_invalid"}
	}
	actualHash := sha256.Sum256(crop.Data)
	if hex.EncodeToString(actualHash[:]) != crop.SHA256 {
		return &GradingAgentError{Code: "grading_v2_crop_invalid"}
	}
	bbox, err := activeCropBBox(map[string]any{
		"x": crop.NormalizedBBox.X, "y": crop.NormalizedBBox.Y,
		"width": crop.NormalizedBBox.Width, "height": crop.NormalizedBBox.Height,
	})
	if err != nil || bbox != crop.NormalizedBBox {
		return &GradingAgentError{Code: "grading_v2_crop_invalid"}
	}
	return nil
}

func validGradingReferenceContext(reference *GradingReferenceContext) bool {
	if reference == nil {
		return true // 历史考试可能早于已确认导入快照机制。
	}
	if reference.Source != "confirmed_exam_import_snapshot" || !validLowerSHA256(reference.SnapshotHash) ||
		len(reference.EquivalentAnswers) > 100 || len(reference.SolutionSteps) > 100 ||
		len(reference.SolutionText) > 20_000 {
		return false
	}
	if !validGradingReferenceAnswers(reference) {
		return false
	}
	for _, step := range reference.SolutionSteps {
		if step.StepNo <= 0 || len(step.Content) > 4_000 {
			return false
		}
	}
	return reference.StandardAnswer != nil || strings.TrimSpace(reference.SolutionText) != "" || len(reference.SolutionSteps) > 0
}

// 上层已处理nil和来源限制；答案部分按原文字节数，标准/等价答案按JSON编码字节数限制。
func validGradingReferenceAnswers(reference *GradingReferenceContext) bool {
	if len(reference.AnswerParts) > 100 {
		return false
	}
	for index, part := range reference.AnswerParts {
		if part.PartNo != index+1 || strings.TrimSpace(part.AnswerText) == "" || len(part.AnswerText) > 20_000 {
			return false
		}
	}
	answer, err := json.Marshal(reference.StandardAnswer)
	if err != nil || len(answer) > 20_000 {
		return false
	}
	for _, equivalent := range reference.EquivalentAnswers {
		encoded, err := json.Marshal(equivalent)
		if err != nil || len(encoded) > 4_000 {
			return false
		}
	}
	return true
}

func validMathEvidenceForV2(evidence *MathEvidenceContext, input AdapterInput) bool {
	if evidence == nil || evidence.ArtifactID == "" || evidence.ArtifactVersion <= 0 || evidence.CorrectionRevision < 0 ||
		evidence.ExamQuestionSnapshotID == "" || evidence.ExamQuestionSnapshotID != input.AssessmentSnapshot.ID ||
		evidence.ScoringVersion != MathScoringVersionV1 || evidence.OverallConfidence < 0 || evidence.OverallConfidence > 1 ||
		evidence.Quality.Critical < 0 || evidence.Quality.Critical > 1 || len(evidence.Steps) == 0 || len(evidence.Steps) > 512 ||
		len(evidence.Formulas) > 512 || len(evidence.Verifications) > 2048 || len(evidence.RubricEvidence) > 512 {
		return false
	}
	known := map[string]bool{}
	for _, step := range evidence.Steps {
		if !boundedNonBlank(step.ID, 128) || len(step.Text) > 4_000 || step.Confidence < 0 || step.Confidence > 1 || known[step.ID] {
			return false
		}
		known[step.ID] = true
	}
	for _, formula := range evidence.Formulas {
		if !boundedNonBlank(formula.ID, 128) || len(formula.CanonicalLatex) > 4_000 || formula.Confidence < 0 || formula.Confidence > 1 || known[formula.ID] {
			return false
		}
		known[formula.ID] = true
	}
	for _, check := range evidence.Verifications {
		if !boundedNonBlank(check.ID, 128) || check.Confidence < 0 || check.Confidence > 1 {
			return false
		}
	}
	return true
}
