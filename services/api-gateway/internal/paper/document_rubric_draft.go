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

// SuggestMathRubricDraft makes a read-only inference from one reviewed import
// candidate. The response is never persisted or treated as an extracted rubric.
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
	var draft *PaperImportDraftQuestion
	for index := range job.Questions {
		if job.Questions[index].CandidateID == input.CandidateID {
			draft = &job.Questions[index]
			break
		}
	}
	if draft == nil || (draft.QuestionType != "calculation" && draft.QuestionType != "short_answer" && draft.QuestionType != "essay" && draft.QuestionType != "discussion" && draft.QuestionType != "formula") {
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	var question *QuestionCandidate
	for index := range job.QuestionCandidates {
		if job.QuestionCandidates[index].CandidateID == input.CandidateID {
			question = &job.QuestionCandidates[index]
			break
		}
	}
	if question == nil {
		return MathRubricDraftResponse{}, ErrInvalidInput
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
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	var answer *AnswerCandidate
	var solution *SolutionCandidate
	for index := range job.SolutionCandidates {
		candidate := &job.SolutionCandidates[index]
		if draft.SolutionCandidateID != "" {
			if candidate.CandidateID == draft.SolutionCandidateID {
				solution = candidate
				break
			}
			continue
		}
		candidateNo := candidate.QuestionNoNormalized
		if candidateNo == "" {
			candidateNo = candidate.QuestionNoHint
		}
		if normalizePaperImportQuestionNumber(candidateNo) == questionNo {
			if solution != nil {
				return MathRubricDraftResponse{}, ErrConflict
			}
			solution = candidate
		}
	}
	if solution == nil || len(solution.Steps) == 0 || len(solution.SourceRefs) == 0 {
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	questionCopy.QuestionNoNormalized = questionNo
	solutionCopy := *solution
	solutionCopy.QuestionNoNormalized = questionNo
	if draft.Solution == nil || len(draft.Solution.Steps) == 0 {
		return MathRubricDraftResponse{}, ErrInvalidInput
	}
	solutionCopy.RawText = draft.Solution.RawText
	solutionCopy.Steps = draft.Solution.Steps
	if len(draft.Solution.SourceRefs) > 0 {
		solutionCopy.SourceRefs = draft.Solution.SourceRefs
	}
	if draft.AnswerKey != nil {
		answerCopy := AnswerCandidate{CandidateID: draft.AnswerCandidateID, QuestionNoNormalized: questionNo,
			StandardAnswer: draft.AnswerKey.StandardAnswer, EquivalentAnswers: draft.AnswerKey.EquivalentAnswers}
		answer = &answerCopy
	} else {
		answer = nil
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
	result, err := client.SuggestMathRubricDraft(ctx, questionCopy, answer, solutionCopy, model)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	// Review saves do not advance the import generation. Recheck after the
	// potentially slow model call so its result cannot outlive a saved edit.
	latest, err := s.store.GetPaperImport(ctx, tenantID, importID)
	if err != nil {
		return MathRubricDraftResponse{}, err
	}
	if !mathRubricDraftReviewIsCurrent(latest, input.ExpectedGeneration, expectedUpdatedAt) {
		return MathRubricDraftResponse{}, ErrConflict
	}
	return result, nil
}

func mathRubricDraftReviewIsCurrent(job PaperImportJob, generation int64, updatedAt time.Time) bool {
	return job.Status == "review_required" && job.Generation == generation && !job.UpdatedAt.IsZero() && job.UpdatedAt.Equal(updatedAt)
}

func (p *HTTPDocumentParser) SuggestMathRubricDraft(ctx context.Context, question QuestionCandidate, answer *AnswerCandidate, solution SolutionCandidate, model *DocumentModelConfig) (MathRubricDraftResponse, error) {
	if p == nil || p.baseURL == "" || len(p.token) < 32 {
		return MathRubricDraftResponse{}, errors.New("AI service not configured")
	}
	body, err := json.Marshal(map[string]any{
		"request_id": uuid.NewString(), "subject": "mathematics",
		"question_candidate": question, "answer_candidate": answer,
		"solution_candidate": solution, "managed_model": model,
	})
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
		return MathRubricDraftResponse{}, fmt.Errorf("math rubric draft service returned %d", response.StatusCode)
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
	if len(result.SuggestedRubricCandidates) != 1 || result.SuggestedRubricCandidates[0].Status != "review_required" || result.SuggestedRubricCandidates[0].Origin != "ai_suggestion_from_solution" || len(result.SuggestedRubricCandidates[0].Points) == 0 {
		return MathRubricDraftResponse{}, errors.New("math rubric draft response invalid")
	}
	suggestion := &result.SuggestedRubricCandidates[0]
	if normalizePaperImportQuestionNumber(suggestion.QuestionNoNormalized) != normalizePaperImportQuestionNumber(question.QuestionNoNormalized) ||
		suggestion.Provenance["solution_candidate_id"] != solution.CandidateID {
		return MathRubricDraftResponse{}, errors.New("math rubric draft response binding invalid")
	}
	validScore := func(score *float64) bool {
		return score == nil || (!math.IsNaN(*score) && !math.IsInf(*score, 0) && *score >= 0)
	}
	if !validScore(suggestion.MaxScore) || question.Score == nil && suggestion.MaxScore != nil ||
		question.Score != nil && (suggestion.MaxScore == nil || !scoreEqual(*question.Score, *suggestion.MaxScore)) {
		return MathRubricDraftResponse{}, errors.New("math rubric draft maximum invalid")
	}
	knownSteps := map[string]bool{}
	for index, step := range solution.Steps {
		if strings.TrimSpace(step.Content) != "" {
			knownSteps[fmt.Sprintf("step-%d", index+1)] = true
		}
	}
	knownPoints := map[string]bool{}
	for _, point := range suggestion.Points {
		if strings.TrimSpace(point.ID) == "" || knownPoints[point.ID] || strings.TrimSpace(point.Description) == "" || !validScore(point.SuggestedScore) ||
			question.Score == nil && point.SuggestedScore != nil || len(point.EvidenceStepIDs) == 0 || !reflect.DeepEqual(point.SourceRefs, solution.SourceRefs) {
			return MathRubricDraftResponse{}, errors.New("math rubric draft point invalid")
		}
		knownPoints[point.ID] = true
		for _, evidenceID := range point.EvidenceStepIDs {
			if !knownSteps[evidenceID] {
				return MathRubricDraftResponse{}, errors.New("math rubric draft evidence invalid")
			}
		}
	}
	// The browser binds its asynchronous response to this original candidate;
	// generated suggestion IDs remain only inside individual review point IDs.
	suggestion.CandidateID = question.CandidateID
	return result, nil
}
