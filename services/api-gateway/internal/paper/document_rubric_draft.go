package paper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxMathRubricDraftResponseBytes = 1 << 20

type MathRubricDraftInput struct {
	CandidateID        string `json:"candidate_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	ExpectedUpdatedAt  string `json:"expected_updated_at"`
}

type MathRubricDraftPoint struct {
	ID              string                 `json:"id"`
	Description     string                 `json:"description"`
	SuggestedScore  *float64               `json:"suggested_score"`
	EvidenceStepIDs []string               `json:"evidence_step_ids"`
	SourceRefs      []PaperImportSourceRef `json:"source_refs"`
	ReviewNote      string                 `json:"review_note"`
}

type MathRubricDraftSuggestion struct {
	CandidateID          string                 `json:"candidate_id"`
	QuestionNoNormalized string                 `json:"question_no_normalized"`
	MaxScore             *float64               `json:"max_score"`
	Points               []MathRubricDraftPoint `json:"points"`
	Origin               string                 `json:"origin"`
	Status               string                 `json:"status"`
	Provenance           map[string]any         `json:"provenance"`
	Issues               []string               `json:"issues"`
}

type MathRubricDraftResponse struct {
	SuggestedRubricCandidates []MathRubricDraftSuggestion `json:"suggested_rubric_candidates"`
}

// SuggestMathRubricDraft 根据一个已复核的导入候选进行只读推断。
// 响应不会持久化，也不会被视为已提取的评分细则。
func (s *DocumentImportService) SuggestMathRubricDraft(ctx context.Context, tenantID, importID string, input MathRubricDraftInput) (MathRubricDraftResponse, error) {
	if s == nil || s.store == nil || input.ExpectedGeneration <= 0 || strings.TrimSpace(input.CandidateID) == "" {
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	expectedUpdatedAt, err := time.Parse(time.RFC3339Nano, input.ExpectedUpdatedAt)
	if err != nil {
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	job, err := s.store.GetPaperImport(ctx, tenantID, importID)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	if !mathRubricDraftReviewIsCurrent(job, input.ExpectedGeneration, expectedUpdatedAt) {
		return MathRubricDraftResponse{}, ErrConflict
	}
	subject := strings.ToLower(strings.TrimSpace(job.Subject))
	if subject != "math" && subject != "mathematics" && job.Subject != "数学" {
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	inputs, err := prepareMathRubricInputs(job, input.CandidateID)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	client, ok := s.parser.(*HTTPDocumentParser)
	if !ok {
		return MathRubricDraftResponse{}, errors.New("math rubric draft service is unavailable")
	}
	var model *DocumentModelConfig
	if s.modelResolver != nil {
		model, err = s.modelResolver(ctx, tenantID)
		if err != nil {
			return MathRubricDraftResponse{}, errors.New("school model configuration unavailable")
		}
	}
	result, err := client.SuggestMathRubricDraft(ctx, inputs.question, inputs.answer, inputs.solution, model)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	// 保存复核不会推进导入代次。
	// 模型调用可能较慢，调用后需重新检查，避免结果沿用已被保存修改替代的内容。
	latest, err := s.store.GetPaperImport(ctx, tenantID, importID)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	if !mathRubricDraftReviewIsCurrent(latest, input.ExpectedGeneration, expectedUpdatedAt) {
		return MathRubricDraftResponse{}, ErrConflict
	}
	return result, nil
}

// 推断输入只包含当前复核内容及其原候选绑定，不携带模型配置或仓储执行依赖。
type reviewedMathRubricInputs struct {
	question QuestionCandidate
	answer   *AnswerCandidate
	solution SolutionCandidate
}

func prepareMathRubricInputs(job PaperImportJob, candidateID string) (reviewedMathRubricInputs, error) {
	draft, err := reviewedMathRubricDraft(job.Questions, candidateID)
	if err != nil {
		return reviewedMathRubricInputs{}, err
	}
	question := mathRubricQuestionCandidate(job.QuestionCandidates, candidateID)
	if question == nil {
		return reviewedMathRubricInputs{}, ErrInvalidInput
	}
	questionCopy := *question
	questionCopy.QuestionType = draft.QuestionType
	questionCopy.Stem = draft.Stem
	if draft.Score > 0 && draft.MatchStatus != "mismatch" {
		score := draft.Score
		questionCopy.Score = &score
	} else {
		questionCopy.Score = nil
	}
	questionNo := normalizePaperImportQuestionNumber(draft.QuestionNo)
	if questionNo == "" {
		return reviewedMathRubricInputs{}, ErrInvalidInput
	}
	solution, err := mathRubricSolutionCandidate(job.SolutionCandidates, *draft, questionNo)
	if err != nil {
		return reviewedMathRubricInputs{}, err
	}
	questionCopy.QuestionNoNormalized = questionNo
	solutionCopy := *solution
	solutionCopy.QuestionNoNormalized = questionNo
	if draft.Solution == nil || len(draft.Solution.Steps) == 0 {
		return reviewedMathRubricInputs{}, ErrInvalidInput
	}
	solutionCopy.RawText = draft.Solution.RawText
	solutionCopy.Steps = draft.Solution.Steps
	if len(draft.Solution.SourceRefs) > 0 {
		solutionCopy.SourceRefs = draft.Solution.SourceRefs
	}
	var answer *AnswerCandidate
	if draft.AnswerKey != nil {
		answerCopy := AnswerCandidate{CandidateID: draft.AnswerCandidateID, QuestionNoNormalized: questionNo,
			StandardAnswer: draft.AnswerKey.StandardAnswer, EquivalentAnswers: draft.AnswerKey.EquivalentAnswers}
		answer = &answerCopy
	}
	return reviewedMathRubricInputs{question: questionCopy, answer: answer, solution: solutionCopy}, nil
}

func reviewedMathRubricDraft(drafts []PaperImportDraftQuestion, candidateID string) (*PaperImportDraftQuestion, error) {
	var draft *PaperImportDraftQuestion
	for index := range drafts {
		if drafts[index].CandidateID == candidateID {
			draft = &drafts[index]
			break
		}
	}
	if draft == nil || (draft.QuestionType != "calculation" && draft.QuestionType != "short_answer" && draft.QuestionType != "essay" && draft.QuestionType != "discussion" && draft.QuestionType != "formula") {
		return nil, ErrInvalidInput
	}
	return draft, nil
}

func mathRubricQuestionCandidate(questions []QuestionCandidate, candidateID string) *QuestionCandidate {
	for index := range questions {
		if questions[index].CandidateID == candidateID {
			return &questions[index]
		}
	}
	return nil
}

func mathRubricSolutionCandidate(solutions []SolutionCandidate, draft PaperImportDraftQuestion, questionNo string) (*SolutionCandidate, error) {
	var solution *SolutionCandidate
	if draft.SolutionCandidateID != "" {
		for index := range solutions {
			if solutions[index].CandidateID == draft.SolutionCandidateID {
				solution = &solutions[index]
				break
			}
		}
	} else {
		var err error
		solution, err = mathRubricSolutionByNumber(solutions, questionNo)
		if err != nil {
			return nil, err
		}
	}
	if solution == nil || len(solution.Steps) == 0 || len(solution.SourceRefs) == 0 {
		return nil, ErrInvalidInput
	}
	return solution, nil
}

// 显式候选 ID 不走题号回退；只有未绑定时才按题号找唯一解析，重复匹配必须交回人工处理。
func mathRubricSolutionByNumber(solutions []SolutionCandidate, questionNo string) (*SolutionCandidate, error) {
	var solution *SolutionCandidate
	for index := range solutions {
		candidate := &solutions[index]
		candidateNo := candidate.QuestionNoNormalized
		if candidateNo == "" {
			candidateNo = candidate.QuestionNoHint
		}
		if normalizePaperImportQuestionNumber(candidateNo) == questionNo {
			if solution != nil {
				return nil, ErrConflict
			}
			solution = candidate
		}
	}
	return solution, nil
}

func mathRubricDraftReviewIsCurrent(job PaperImportJob, generation int64, updatedAt time.Time) bool {
	return job.Status == "review_required" && job.Generation == generation && !job.UpdatedAt.IsZero() && job.UpdatedAt.Equal(updatedAt)
}

func (p *HTTPDocumentParser) SuggestMathRubricDraft(ctx context.Context, question QuestionCandidate, answer *AnswerCandidate, solution SolutionCandidate, model *DocumentModelConfig) (MathRubricDraftResponse, error) {
	return p.suggestMathRubricDraft(ctx, question, answer, solution, model, nil)
}

func (p *HTTPDocumentParser) suggestMathRubricDraft(ctx context.Context, question QuestionCandidate, answer *AnswerCandidate, solution SolutionCandidate, model *DocumentModelConfig, reference *questionRubricReference) (MathRubricDraftResponse, error) {
	if p == nil || p.baseURL == "" || len(p.token) < 32 {
		return MathRubricDraftResponse{}, errors.New("AI service not configured")
	}
	payload := map[string]any{
		"request_id": uuid.NewString(), "subject": "mathematics",
		"question_candidate": question, "answer_candidate": answer,
		"solution_candidate": solution, "managed_model": model,
	}
	if reference != nil {
		payload["question_reference"] = reference
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/paper/rubric-draft", bytes.NewReader(body))
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+p.token)
	response, err := p.client.Do(request)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		status := response.StatusCode
		// AI 服务将供应商限流封装为 502；只读取白名单错误码来安排重试，不保存供应商原文。
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&failure)
		if failure.Error.Code == "model_rate_limited" || failure.Code == "model_rate_limited" {
			status = http.StatusTooManyRequests
		}
		return MathRubricDraftResponse{}, &rubricDraftHTTPError{Status: status}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxMathRubricDraftResponseBytes+1))
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	if len(data) > maxMathRubricDraftResponseBytes {
		return MathRubricDraftResponse{}, errors.New("math rubric draft response exceeded size limit")
	}
	var result MathRubricDraftResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return MathRubricDraftResponse{}, err
	}
	if err := validateMathRubricResponse(&result, question, solution); err != nil {
		return MathRubricDraftResponse{}, err
	}
	if err := validateQuestionRubricReference(result, reference); err != nil {
		return MathRubricDraftResponse{}, err
	}
	return result, nil
}

func validateMathRubricResponse(result *MathRubricDraftResponse, question QuestionCandidate, solution SolutionCandidate) error {
	if len(result.SuggestedRubricCandidates) != 1 || result.SuggestedRubricCandidates[0].Status != "review_required" || result.SuggestedRubricCandidates[0].Origin != "ai_suggestion_from_solution" || len(result.SuggestedRubricCandidates[0].Points) == 0 {
		return errors.New("math rubric draft response invalid")
	}
	suggestion := &result.SuggestedRubricCandidates[0]
	if normalizePaperImportQuestionNumber(suggestion.QuestionNoNormalized) != normalizePaperImportQuestionNumber(question.QuestionNoNormalized) ||
		suggestion.Provenance["solution_candidate_id"] != solution.CandidateID {
		return errors.New("math rubric draft response binding invalid")
	}
	if !validMathRubricScore(suggestion.MaxScore) || question.Score == nil && suggestion.MaxScore != nil ||
		question.Score != nil && (suggestion.MaxScore == nil || !scoreEqual(*question.Score, *suggestion.MaxScore)) {
		return errors.New("math rubric draft maximum invalid")
	}
	if err := validateMathRubricPoints(suggestion.Points, question.Score, solution); err != nil {
		return err
	}
	// 浏览器将异步响应绑定到这个原始候选；生成的建议 ID 只保留在各个复核评分点 ID 中。
	suggestion.CandidateID = question.CandidateID
	return nil
}

func validMathRubricScore(score *float64) bool {
	return score == nil || (!math.IsNaN(*score) && !math.IsInf(*score, 0) && *score >= 0)
}

func mathRubricEvidenceSteps(steps []SolutionStep) map[string]bool {
	knownSteps := map[string]bool{}
	for index, step := range steps {
		if strings.TrimSpace(step.Content) != "" {
			knownSteps[fmt.Sprintf("step-%d", index+1)] = true
		}
	}
	return knownSteps
}

func validateMathRubricPoints(points []MathRubricDraftPoint, score *float64, solution SolutionCandidate) error {
	knownSteps := mathRubricEvidenceSteps(solution.Steps)
	knownPoints := map[string]bool{}
	for _, point := range points {
		if err := validateMathRubricPoint(point, score, solution.SourceRefs, knownPoints, knownSteps); err != nil {
			return err
		}
	}
	return nil
}

func validateMathRubricPoint(point MathRubricDraftPoint, score *float64, sourceRefs []PaperImportSourceRef, knownPoints, knownSteps map[string]bool) error {
	if strings.TrimSpace(point.ID) == "" || knownPoints[point.ID] || strings.TrimSpace(point.Description) == "" || !validMathRubricScore(point.SuggestedScore) ||
		score == nil && point.SuggestedScore != nil || len(point.EvidenceStepIDs) == 0 || !reflect.DeepEqual(point.SourceRefs, sourceRefs) {
		return errors.New("math rubric draft point invalid")
	}
	knownPoints[point.ID] = true
	for _, evidenceID := range point.EvidenceStepIDs {
		if !knownSteps[evidenceID] {
			return errors.New("math rubric draft evidence invalid")
		}
	}
	return nil
}
