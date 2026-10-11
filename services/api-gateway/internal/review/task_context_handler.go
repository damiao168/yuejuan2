package review

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
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
	if !canViewOriginalReviewImage(user) {
		contextValue.AnswerArtifact.OriginalImageURL = ""
	}
	contextValue.Claim.CanRenew = contextValue.Claim.CanRenew && contextValue.Task.AssignedTo == user.ID
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
