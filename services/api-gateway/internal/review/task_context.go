package review

import (
	"context"
	"math"
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
	// AIFailure 只标记当前版本的 AI 建议不可用；失败正文和供应商细节不进入教师载荷。
	AIFailure *AIFailure `json:"ai_failure,omitempty"`
}

type AIFailure struct {
	FailureKind string `json:"failure_kind"`
}

type AnswerArtifact struct {
	AnswerScope             string                `json:"answer_scope"`
	AnswerSegmentID         string                `json:"answer_segment_id"`
	Source                  string                `json:"source,omitempty"`
	RawAnswer               string                `json:"raw_answer,omitempty"`
	OCRText                 string                `json:"ocr_text,omitempty"`
	Status                  string                `json:"status"`
	Confidence              *float64              `json:"confidence,omitempty"`
	SegmentImageURL         string                `json:"segment_image_url"`
	OriginalImageURL        string                `json:"original_image_url,omitempty"`
	GradingGroupID          string                `json:"grading_group_id,omitempty"`
	GradingGroupImageURL    string                `json:"grading_group_image_url,omitempty"`
	GradingGroupQuestionIDs []string              `json:"grading_group_question_ids,omitempty"`
	GradingGroupTasks       []GradingGroupTaskRef `json:"grading_group_tasks,omitempty"`
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
			AnswerScope:     "question",
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
	// 当前建议及其可采纳历史必须属于同一答案、快照和冻结量规；旧版本仍保存在管理端成绩历史中。
	out.AISecondOpinion = aiSecondOpinionFromSuggestion(currentTaskAISuggestion(workspace.Context), allowsAIScorePrefill(snapshot))
	return out
}

func currentTaskAISuggestion(value Context) map[string]any {
	if value.AnswerVersion == "" || value.AssessmentSnapshot.ID == "" || value.Rubric.Version == "" {
		return nil
	}
	matches := func(item map[string]any) bool {
		return item["answer_segment_id"] == value.AnswerSegmentID && item["question_id"] == value.Question.ID &&
			item["answer_version"] == value.AnswerVersion && item["exam_question_snapshot_id"] == value.AssessmentSnapshot.ID &&
			item["rubric_version"] == value.Rubric.Version
	}
	history := []any{}
	if entries, ok := value.AISuggestion["suggestion_history"].([]any); ok {
		for _, entry := range entries {
			if item, valid := entry.(map[string]any); valid && matches(item) {
				history = append(history, cloneMap(item))
				if len(history) == 20 {
					break
				}
			}
		}
	}
	var current map[string]any
	if matches(value.AISuggestion) {
		current = cloneMap(value.AISuggestion)
	} else if len(history) > 0 {
		current = cloneMap(history[0].(map[string]any))
	} else {
		return nil
	}
	current["suggestion_history"] = history
	return current
}

// aiSecondOpinionFromSuggestion 在副本上整理建议；R3 HUMAN_PRIMARY 的材料不能带可预填分数字段。
func aiSecondOpinionFromSuggestion(suggestion map[string]any, prefillAllowed bool) *AISecondOpinion {
	if len(suggestion) == 0 {
		return nil
	}
	metadata := cloneMap(suggestion)
	// 有建议而无有效历史时仍返回 []，保留原 JSON 形态。
	history := []map[string]any{}
	if entries, ok := metadata["suggestion_history"].([]any); ok {
		for _, entry := range entries {
			item, valid := entry.(map[string]any)
			if !valid {
				continue
			}
			copy := cloneMap(item)
			if !prefillAllowed {
				copy = removeScoreFields(copy)
			}
			history = append(history, copy)
		}
	}
	delete(metadata, "suggestion_history")
	if !prefillAllowed {
		metadata = removeScoreFields(metadata)
	}
	return &AISecondOpinion{
		Available: true, Presentation: "explicit_second_opinion",
		ScorePrefillAllowed: prefillAllowed && imageSuggestionHasFinalScore(suggestion), Metadata: metadata,
		History: history,
	}
}

// 多模型有成功角色但未形成汇总时仍可查看角色依据；该候选分不能预填为本题建议分。
// 历史单模型没有 panel_execution，继续沿用其原有评分政策。
func imageSuggestionHasFinalScore(suggestion map[string]any) bool {
	raw, ok := suggestion["raw_output"].(map[string]any)
	if !ok || raw["schema_version"] != "grading-image-v3" {
		return true
	}
	value, exists := raw["panel_execution"]
	if !exists {
		return true
	}
	panel, ok := value.(map[string]any)
	if !ok {
		return false
	}
	final, ok := panel["final_score"].(float64)
	return ok && !math.IsNaN(final) && !math.IsInf(final, 0)
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

// removeScoreFields 只用于 R3 HUMAN_PRIMARY 的 AI 材料。
// 它也递归处理证据对象，避免 UI 意外发现分数并将其设为默认值。
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
