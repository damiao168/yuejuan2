package review

import (
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

// GetTaskContext 返回一个聚合的工作台载荷。路由连接如下：
// GET /api/v1/review-tasks/{id}/context -> reviewHandler.GetTaskContext.
func (h *Handler) GetTaskContext(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	taskID := r.PathValue("taskId")
	if taskID == "" {
		taskID = r.PathValue("id")
	} else if r.PathValue("id") == "" {
		// 既有复核授权辅助函数使用相邻路由的“id”通配符；同时接受 OpenAPI 中描述更明确的 taskId。
		r.SetPathValue("id", taskID)
	}
	if h.seedHook != nil {
		seedContext, handled, seedErr := h.seedHook.GetGraderTaskContext(r.Context(), user.TenantID, taskID, user.ID)
		if seedErr != nil {
			writeSeedHookError(w, r, seedErr)
			return
		}
		if handled {
			httpx.JSON(w, http.StatusOK, map[string]any{"context": seedContext})
			return
		}
	}
	if !h.authorizeTaskWorker(w, r, user) {
		return
	}
	contextValue, err := h.store.(TaskContextStore).GetTaskContext(r.Context(), user.TenantID, taskID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if h.referenceMaterials != nil {
		materials, materialErr := h.referenceMaterials.ForSnapshot(r.Context(), user.TenantID, contextValue.Task.ExamID, contextValue.Task.QuestionID, contextValue.QuestionSnapshot.ID)
		if materialErr != nil {
			httpx.Error(w, r, 503, "reference_material_unavailable", "参考材料暂时不可读，请稍后重试")
			return
		}
		if materials.ID != "" {
			contextValue.Question.ReferenceMaterials = &paper.ReferenceMaterialBinding{ID: materials.ID, Mode: materials.Mode, Revision: materials.Revision, ContentHash: materials.ContentHash, ImageCount: len(materials.Items)}
		}
	}
	if reader, ok := h.segmentImage.(segment.GradingGroupReader); ok {
		group, groupErr := reader.GetGradingGroup(r.Context(), user.TenantID, contextValue.Task.AnswerSegmentID)
		if groupErr == nil && group != nil {
			contextValue.AnswerArtifact.GradingGroupID = group.ID
			contextValue.AnswerArtifact.GradingGroupQuestionIDs = group.QuestionIDs
			contextValue.AnswerArtifact.GradingGroupTasks = h.gradingGroupTaskRefs(r.Context(), user.TenantID, user.ID, contextValue.Task, group)
			separator := "?"
			if strings.Contains(contextValue.AnswerArtifact.SegmentImageURL, "?") {
				separator = "&"
			}
			contextValue.AnswerArtifact.GradingGroupImageURL = contextValue.AnswerArtifact.SegmentImageURL + separator + "scope=grading_group"
		}
	}
	if reader, ok := h.segmentImage.(segment.AnswerScopeReader); ok {
		scope, scopeErr := reader.GetAnswerScope(r.Context(), user.TenantID, contextValue.Task.AnswerSegmentID)
		if scopeErr != nil {
			writeStoreError(w, r, scopeErr)
			return
		}
		contextValue.AnswerArtifact.AnswerScope = scope
		if scope == "grading_group" {
			// 同题仍保留自己的量规和分数；这里只关闭并不存在的精确单题图片。
			contextValue.AnswerArtifact.SegmentImageURL = ""
		}
	}
	if h.aiFailure != nil {
		failureKind, failureErr := h.aiFailure.CurrentAIFailure(
			r.Context(), user.TenantID, taskID, contextValue.Task.AnswerSegmentID,
			contextValue.Task.QuestionID, contextValue.QuestionSnapshot.ID, contextValue.FrozenRubric.Version,
		)
		if failureErr != nil {
			// 诊断投影暂时不可用时，停用无法确认有效的 AI 建议，仍交付答题图、细则和本人草稿供人工继续。
			// 错误原文可能含数据库和供应商信息，不进入教师载荷。
			failureKind = "output_incomplete"
		}
		if failureKind != "" {
			// 这里只在响应副本上停用建议，不更新任务、成绩或人工草稿。
			contextValue.AIFailure = &AIFailure{FailureKind: taskContextFailureKind(failureKind)}
			contextValue.AICandidates = []AnswerCandidate{}
			contextValue.AISecondOpinion = nil
			// 当前执行已失败时，旧成功建议中的转写也不能继续冒充本次图像识别结果。
			if contextValue.AnswerArtifact.Source == imageObservationSource {
				contextValue.AnswerArtifact.OCRText = ""
				contextValue.AnswerArtifact.Source = ""
			}
			if contextValue.AutomationResult != nil && contextValue.AutomationResult.GradeSource == "ai_suggestion" {
				copy := *contextValue.AutomationResult
				copy.Score, copy.MaxScore = nil, nil
				contextValue.AutomationResult = &copy
			}
		}
	}
	if !canViewOriginalReviewImage(user) {
		contextValue.AnswerArtifact.OriginalImageURL = ""
	}
	contextValue.Claim.CanRenew = contextValue.Claim.CanRenew && contextValue.Task.AssignedTo == user.ID
	if contextValue.Task.AssignedTo == user.ID && submittedReviewStatus(contextValue.Task.Status) {
		if reader, supported := h.store.(SubmittedGradeReader); supported {
			// 正式提交会清除草稿；回看必须展示本人正式评分，不能用 AI 建议或另一阅卷人的成绩代替。
			grade, gradeErr := reader.GetSubmittedGrade(r.Context(), user.TenantID, contextValue.Task.ID, user.ID)
			if gradeErr == nil {
				contextValue.HumanGrade = &grade
			} else if !errors.Is(gradeErr, ErrNotFound) {
				writeStoreError(w, r, gradeErr)
				return
			}
		}
	}
	// 管理员可查看任务，但这里也只附带当前登录者本人负责的草稿。
	if contextValue.Task.AssignedTo == user.ID {
		draft, draftErr := h.store.(DraftStore).GetDraft(r.Context(), user.TenantID, contextValue.Task.ID, user.ID)
		switch {
		case draftErr == nil:
			contextValue.Draft = &draft
		case errors.Is(draftErr, ErrNotFound):
			// 草稿缺失是首次打开时的正常状态。
		default:
			writeStoreError(w, r, draftErr)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"context": contextValue})
}

func taskContextFailureKind(kind string) string {
	switch kind {
	case "output_truncated", "finish_reason_missing", "finish_reason_unknown", "provider_refused", "content_filtered",
		"unsupported_generation", "schema_invalid", "evidence_invalid", "capability_unavailable", "response_envelope_invalid":
		return kind
	default:
		return "output_incomplete"
	}
}
