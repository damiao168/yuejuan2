package review

import (
	"context"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// TaskContextStore 汇集工作台所需的题目快照、当前任务、答案和辅助材料。
// 读取不会领取任务或写入成绩；正式评分通过 Store.SubmitGrade 提交。
type TaskContextStore interface {
	GetTaskContext(context.Context, string, string) (TaskContext, error)
}

type TaskContext struct {
	Task             ReviewTask                      `json:"task"`
	ExpectedRevision int64                           `json:"expected_revision"`
	QuestionSnapshot assessment.ExamQuestionSnapshot `json:"question_snapshot"`
	Question         paper.Question                  `json:"question"`
	AnswerArtifact   AnswerArtifact                  `json:"answer_artifact"`
	FrozenRubric     paper.Rubric                    `json:"frozen_rubric"`
	AICandidates     []AnswerCandidate               `json:"ai_candidates"`
	ScoringEvidence  []assessment.ScoringEvidence    `json:"scoring_evidence"`
	AISecondOpinion  *AISecondOpinion                `json:"ai_second_opinion,omitempty"`
	Claim            TaskClaim                       `json:"claim"`
	Draft            *ReviewDraft                    `json:"draft"`
	SubjectToolHints SubjectToolHints                `json:"subject_tool_hints"`
	AutomationResult *AutomationResult               `json:"automation_result,omitempty"`
}

type AnswerArtifact struct {
	AnswerSegmentID  string   `json:"answer_segment_id"`
	Source           string   `json:"source,omitempty"`
	RawAnswer        string   `json:"raw_answer,omitempty"`
	OCRText          string   `json:"ocr_text,omitempty"`
	Status           string   `json:"status"`
	Confidence       *float64 `json:"confidence,omitempty"`
	SegmentImageURL  string   `json:"segment_image_url"`
	OriginalImageURL string   `json:"original_image_url,omitempty"`
}

type AnswerCandidate struct {
	ID                     string         `json:"id"`
	AnswerSegmentID        string         `json:"answer_segment_id"`
	ScoringRunID           string         `json:"scoring_run_id,omitempty"`
	ExamQuestionSnapshotID string         `json:"exam_question_snapshot_id,omitempty"`
	Source                 string         `json:"source"`
	Payload                map[string]any `json:"payload"`
	DisplayText            string         `json:"display_text"`
	Confidence             *float64       `json:"confidence,omitempty"`
	Decision               string         `json:"decision"`
	Evidence               map[string]any `json:"evidence"`
	EngineVersion          string         `json:"engine_version"`
	ProfileVersion         string         `json:"profile_version"`
	IsCurrent              bool           `json:"is_current"`
	CreatedAt              time.Time      `json:"created_at"`
}

type AISecondOpinion struct {
	Available           bool             `json:"available"`
	Presentation        string           `json:"presentation"`
	ScorePrefillAllowed bool             `json:"score_prefill_allowed"`
	Metadata            map[string]any   `json:"metadata"`
	History             []map[string]any `json:"history,omitempty"`
}

type TaskClaim struct {
	OwnerID   string     `json:"owner_id,omitempty"`
	State     string     `json:"state"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CanRenew  bool       `json:"can_renew"`
}

type SubjectToolHints struct {
	SubjectCode          assessment.SubjectCode    `json:"subject_code"`
	ArchetypeCode        string                    `json:"archetype_code"`
	AllowedEvidenceTypes []assessment.EvidenceType `json:"allowed_evidence_types"`
	ParserPolicy         map[string]any            `json:"parser_policy"`
	EvidencePolicy       map[string]any            `json:"evidence_policy"`
	ResponseSchema       map[string]any            `json:"response_schema"`
}

func taskContextFromWorkspace(workspace Workspace) TaskContext {
	snapshot := workspace.Context.AssessmentSnapshot
	claimState := "unclaimed"
	if workspace.Task.AssignedTo != "" {
		claimState = "assigned"
	}
	out := TaskContext{
		Task:             workspace.Task,
		ExpectedRevision: workspace.Task.Revision,
		QuestionSnapshot: snapshot,
		Question:         workspace.Context.Question,
		AnswerArtifact: AnswerArtifact{
			AnswerSegmentID: workspace.Task.AnswerSegmentID,
			RawAnswer:       workspace.Context.RawAnswer, OCRText: workspace.Context.OCRText,
			Status: workspace.SegmentStatus, Confidence: workspace.SegmentConfidence,
			SegmentImageURL: workspace.SegmentImageURL, OriginalImageURL: workspace.OriginalImageURL,
		},
		FrozenRubric:     workspace.Context.Rubric,
		AICandidates:     []AnswerCandidate{},
		ScoringEvidence:  []assessment.ScoringEvidence{},
		Claim:            TaskClaim{OwnerID: workspace.Task.AssignedTo, State: claimState},
		SubjectToolHints: subjectToolHints(snapshot),
		AutomationResult: workspace.Context.AutomationResult,
	}
	if !allowsAIScorePrefill(snapshot) && out.AutomationResult != nil {
		copy := *out.AutomationResult
		copy.Score = nil
		copy.MaxScore = nil
		out.AutomationResult = &copy
	}
	if workspace.Context.OCRText != "" {
		out.AnswerArtifact.Source = "ocr_text"
	} else if workspace.Context.RawAnswer != "" {
		out.AnswerArtifact.Source = "answer_segment_answer"
	}
	if len(workspace.Context.AISuggestion) > 0 {
		prefillAllowed := allowsAIScorePrefill(snapshot)
		metadata := cloneMap(workspace.Context.AISuggestion)
		history := []map[string]any{}
		if entries, ok := metadata["suggestion_history"].([]any); ok {
			for _, entry := range entries {
				if item, valid := entry.(map[string]any); valid {
					copy := cloneMap(item)
					if !prefillAllowed {
						copy = removeScoreFields(copy)
					}
					history = append(history, copy)
				}
			}
		}
		delete(metadata, "suggestion_history")
		if !prefillAllowed {
			metadata = removeScoreFields(metadata)
		}
		out.AISecondOpinion = &AISecondOpinion{
			Available: true, Presentation: "explicit_second_opinion",
			ScorePrefillAllowed: prefillAllowed, Metadata: metadata,
			History: history,
		}
	}
	return out
}

// R3 且以人工评分为主时，AI 只能提供辅助材料，不能提供可预填的分数字段。
func allowsAIScorePrefill(snapshot assessment.ExamQuestionSnapshot) bool {
	return !(snapshot.RiskTier == assessment.RiskR3 &&
		snapshot.ScoringPolicySnapshot.Mode == assessment.ScoringHumanPrimary)
}

func subjectToolHints(snapshot assessment.ExamQuestionSnapshot) SubjectToolHints {
	return SubjectToolHints{
		SubjectCode: snapshot.SubjectCode, ArchetypeCode: snapshot.ArchetypeCode,
		AllowedEvidenceTypes: append([]assessment.EvidenceType(nil), snapshot.AllowedEvidenceTypes...),
		ParserPolicy:         objectField(snapshot.ProfileSnapshot, "parser_policy"),
		EvidencePolicy:       objectField(snapshot.ProfileSnapshot, "evidence_policy"),
		ResponseSchema:       objectField(snapshot.ArchetypeSnapshot, "response_schema"),
	}
}

func objectField(source map[string]any, key string) map[string]any {
	value, _ := source[key].(map[string]any)
	return cloneMap(value)
}

// removeScoreFields is applied only to R3 HUMAN_PRIMARY AI material. It also
// descends into evidence objects so a UI cannot accidentally discover a score
// and turn it into a default value.
func removeScoreFields(source map[string]any) map[string]any {
	clean := make(map[string]any, len(source))
	for key, value := range source {
		if strings.Contains(strings.ToLower(key), "score") {
			continue
		}
		clean[key] = removeScoreValue(value)
	}
	return clean
}

func removeScoreValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return removeScoreFields(typed)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = removeScoreValue(item)
		}
		return out
	default:
		return typed
	}
}
