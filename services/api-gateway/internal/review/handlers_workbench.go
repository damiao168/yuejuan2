package review

import (
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"net/http"
)

func (h *Handler) ClaimNextTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input NextTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if h.seedHook != nil && input.ExamID != "" && input.QuestionID != "" {
		// 从本人同题任务读取真实题号；题目 UUID 不能冒充题号，且无本题分派时不额外发盲投。
		ordinary, listErr := h.store.ListTasks(r.Context(), user.TenantID, ListFilter{ExamID: input.ExamID, QuestionID: input.QuestionID, AssignedTo: user.ID, Limit: 1})
		if listErr != nil {
			writeStoreError(w, r, listErr)
			return
		}
		questionNo := ""
		if len(ordinary) > 0 {
			questionNo = ordinary[0].QuestionNo
		}
		if questionNo != "" {
			seedTask, issued, seedErr := h.seedHook.MaybeIssue(r.Context(), user.TenantID, input.ExamID, input.QuestionID, questionNo, user.ID)
			if seedErr != nil {
				writeSeedHookError(w, r, seedErr)
				return
			}
			if issued {
				h.auditAction(r, "review.task_claimed", "review_task", seedTask.ID, "claim next review task")
				httpx.JSON(w, http.StatusOK, map[string]any{"task": seedquality.PublicTask(seedTask, user.TenantID)})
				return
			}
		}
	}
	// 待处理任务要求管理员显式分配；“next”绝不能通过隐式自分配授予答案访问权。
	options := ClaimTaskOptions{}
	task, err := h.store.(WorkbenchStore).ClaimNextTask(r.Context(), user.TenantID, user.ID, input, options)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if h.qualificationGate != nil {
		contextValue, contextErr := h.store.(TaskContextStore).GetTaskContext(r.Context(), user.TenantID, task.ID)
		if contextErr != nil {
			_, _ = h.store.(WorkbenchStore).ReleaseTaskClaim(r.Context(), user.TenantID, task.ID, user.ID)
			writeStoreError(w, r, contextErr)
			return
		}
		if contextValue.QuestionSnapshot.RiskTier == "R3" {
			if qualificationErr := h.qualificationGate.RequireQualification(r.Context(), user.TenantID, task.ExamID, task.QuestionID, user.ID); qualificationErr != nil {
				_, _ = h.store.(WorkbenchStore).ReleaseTaskClaim(r.Context(), user.TenantID, task.ID, user.ID)
				httpx.Error(w, r, http.StatusForbidden, "grader_qualification_required", "complete the current question calibration before claiming R3 work")
				return
			}
		}
	}
	h.auditAction(r, "review.task_claimed", "review_task", task.ID, "claim next review task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": graderTaskView(user, task)})
}

func (h *Handler) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeTaskWorker(w, r, user) {
		return
	}
	workspace, err := h.store.(WorkbenchStore).GetWorkspace(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if !canViewOriginalReviewImage(user) {
		workspace.OriginalImageURL = ""
		workspace.OriginalFileID = ""
	}
	workspace.Task = graderTaskView(user, workspace.Task)
	if reviewWorkerScoped(user) {
		workspace.Context.AnonymousCode = workspace.Task.AnonymousCode
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"workspace": workspace})
}

func (h *Handler) GetWorkspaceSegmentImage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.seedHook != nil {
		seedImage, handled, seedErr := h.seedHook.GetGraderImageSource(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
		if seedErr != nil {
			writeSeedHookError(w, r, seedErr)
			return
		}
		if handled {
			if h.goldImages == nil {
				httpx.Error(w, r, http.StatusInternalServerError, "review_image_unavailable", reviewImageUnavailable)
				return
			}
			resource, err := h.goldImages.ReadVersionImage(r.Context(), user.TenantID, seedImage.GoldPaperID, seedImage.GoldVersion)
			if err != nil {
				segment.WriteImageError(w, r, err)
				return
			}
			// 受限读取返回的文件名不能向阅卷者提示 Gold 来源。
			resource.Disposition = `inline; filename="answer.png"`
			binaryresourcehttp.Serve(w, r, resource, h.audit)
			return
		}
	}
	if !h.authorizeTaskWorker(w, r, user) {
		return
	}
	workspace, err := h.store.(WorkbenchStore).GetWorkspace(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if h.segmentImage == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "review_image_unavailable", reviewImageUnavailable)
		return
	}
	readImage := h.segmentImage.ReadCropImage
	if r.URL.Query().Get("scope") == "grading_group" {
		reader, ok := h.segmentImage.(segment.GradingGroupReader)
		if !ok {
			segment.WriteImageError(w, r, segment.ErrEvidenceUnavailable)
			return
		}
		readImage = reader.ReadGradingGroupImage
	}
	resource, err := readImage(r.Context(), user.TenantID, workspace.Task.AnswerSegmentID)
	if err != nil {
		segment.WriteImageError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}

func (h *Handler) GetWorkspaceOriginalImage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !canViewOriginalReviewImage(user) {
		writeStoreError(w, r, ErrForbidden)
		return
	}
	workspace, err := h.store.(WorkbenchStore).GetWorkspace(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if workspace.OriginalFileID == "" {
		writeStoreError(w, r, ErrNotFound)
		return
	}
	if h.fileDownload == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "review_image_unavailable", reviewImageUnavailable)
		return
	}
	resource, err := h.fileDownload.ReadDownload(r.Context(), user, workspace.OriginalFileID)
	if err != nil {
		files.WriteDownloadError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}

func (h *Handler) RenewTaskClaim(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if err := h.store.(WorkbenchStore).RenewTaskClaim(r.Context(), user.TenantID, r.PathValue("id"), user.ID); err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"renewed": true})
}

func (h *Handler) ReleaseTaskClaim(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.(WorkbenchStore).ReleaseTaskClaim(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.task_released", "review_task", task.ID, "release review task claim")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": graderTaskView(user, task)})
}
