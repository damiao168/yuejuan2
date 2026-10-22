package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

const (
	invalidCursorMessage   = "cursor is invalid"
	reviewImageUnavailable = "review image handler is unavailable"
)

type GraderQualificationGate interface {
	RequireQualification(context.Context, string, string, string, string) error
}

// SeedObservationRefresher 采用尽力而为方式。
// 即使聚合监控投影暂时不可用，已完成的盲测质量样本仍已持久化。
type SeedObservationRefresher interface {
	RefreshSeedObservation(context.Context, string, string, string, string) error
}

// AIHumanDisagreementObserver 只在人工成绩持久化提交后运行，并采用尽力而为方式：
// 可观测性绝不能回滚教师的有效评分操作，也不能改变当前或最终成绩。
type AIHumanDisagreementObserver interface {
	ObserveHumanGrade(context.Context, string, string, string) (aidisagreement.Disagreement, bool, error)
}

// AIFailureReader 只返回当前任务、答案和冻结量规版本对应的白名单失败类别。
// 实现不得把供应商正文、请求标识或其他租户的运行状态投影到教师工作台。
type AIFailureReader interface {
	CurrentAIFailure(context.Context, string, string, string, string, string, string) (string, error)
}

type Handler struct {
	referenceMaterials *paper.ReferenceMaterialsService
	store              Store
	audit              auth.Store
	segmentImage       segment.CropImageReader
	fileDownload       files.DownloadReader
	qualificationGate  GraderQualificationGate
	seedHook           seedquality.ReviewHook
	seedRefresher      SeedObservationRefresher
	disagreement       AIHumanDisagreementObserver
	aiFailure          AIFailureReader
}

func NewHandler(store Store, audit auth.Store, segmentImage segment.CropImageReader, fileDownload files.DownloadReader) *Handler {
	return &Handler{store: store, audit: audit, segmentImage: segmentImage, fileDownload: fileDownload}
}

func (h *Handler) WithQualificationGate(gate GraderQualificationGate) *Handler {
	h.qualificationGate = gate
	return h
}

// WithSeedHook 将暗样本隐藏在普通复核路由之后。
// 钩子只返回合成标识，绝不暴露 Gold 来源。
func (h *Handler) WithSeedHook(hook seedquality.ReviewHook) *Handler {
	h.seedHook = hook
	return h
}

func (h *Handler) WithSeedObservationRefresher(refresher SeedObservationRefresher) *Handler {
	h.seedRefresher = refresher
	return h
}

func (h *Handler) WithAIHumanDisagreementObserver(observer AIHumanDisagreementObserver) *Handler {
	h.disagreement = observer
	return h
}

func (h *Handler) WithAIFailureReader(reader AIFailureReader) *Handler {
	h.aiFailure = reader
	return h
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input CreateTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.AssignedTo != "" && !h.authorizeReviewAssignee(w, r, user.TenantID, input.AssignedTo) {
		return
	}
	task, err := h.store.CreateTask(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.task_created", "review_task", task.ID, "create review task")
	httpx.JSON(w, http.StatusCreated, map[string]any{"task": task})
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 50, 200)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 200")
		return
	}
	filter := ListFilter{
		Status:     r.URL.Query().Get("status"),
		AssignedTo: r.URL.Query().Get("assigned_to"),
		ExamID:     r.URL.Query().Get("exam_id"),
		QuestionID: r.URL.Query().Get("question_id"),
		Subject:    strings.TrimSpace(r.URL.Query().Get("subject")),
		Q:          strings.TrimSpace(r.URL.Query().Get("q")),
		Limit:      limit + 1,
	}
	if value := r.URL.Query().Get("unassigned"); value != "" {
		filter.Unassigned, err = strconv.ParseBool(value)
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_filter", "unassigned must be a boolean")
			return
		}
	}
	if scope, ok := auth.AccessScopeFromContext(r.Context()); ok {
		applyReviewListScope(&filter, scope)
	}
	if err := applyReviewListCursor(&filter, r.URL.Query().Get("cursor")); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", invalidCursorMessage)
		return
	}
	if reviewWorkerScoped(user) {
		// 阅卷员只能读取分配给自己的任务，客户端筛选不能放宽认证身份的边界。
		filter.AssignedTo = user.ID
	}
	tasks, err := h.store.ListTasks(r.Context(), user.TenantID, filter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	aggregate, err := h.store.AggregateTasks(r.Context(), user.TenantID, filter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	hasMore := len(tasks) > limit
	if hasMore {
		tasks = tasks[:limit]
	}
	nextCursor := ""
	if hasMore && len(tasks) > 0 {
		last := tasks[len(tasks)-1]
		nextCursor = pagination.EncodeParts(strconv.Itoa(last.Priority), last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": tasks, "next_cursor": nextCursor, "has_more": hasMore, "aggregate": aggregate})
}

// 非空游标必须有三个非空字段；优先级和时间另做类型解析，任务 ID 沿用游标原值。
func applyReviewListCursor(filter *ListFilter, cursor string) error {
	parts, err := pagination.DecodeParts(cursor, 3)
	if err != nil {
		return err
	}
	if len(parts) != 3 {
		return nil
	}
	filter.CursorPriority, err = strconv.Atoi(parts[0])
	if err == nil {
		filter.CursorCreatedAt, err = time.Parse(time.RFC3339Nano, parts[1])
	}
	if err != nil {
		return err
	}
	filter.CursorID = parts[2]
	return nil
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if reviewWorkerScoped(user) && task.AssignedTo != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) AssignTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input AssignTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !h.authorizeReviewAssignee(w, r, user.TenantID, input.AssignedTo) {
		return
	}
	task, err := h.store.AssignTask(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.task_assigned", "review_task", task.ID, "assign review task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) BatchAssignTasks(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input BatchAssignInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !h.authorizeReviewAssignee(w, r, user.TenantID, input.AssignedTo) {
		return
	}
	tasks, err := h.store.BatchAssignTasks(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.tasks_batch_assigned", "review_task", "", "batch assign review tasks")
	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (h *Handler) SubmitGrade(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(commandreceipt.WithID(r.Context(), r.Header.Get("Idempotency-Key")))
	user := mustUser(r)
	var input SubmitGradeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	r = r.WithContext(commandreceipt.WithInput(r.Context(), input))
	if h.trySubmitSeedGrade(w, r, user, input) {
		return
	}
	r = r.WithContext(prepareSubmissionQualification(r.Context(), h.store, h.qualificationGate, user.TenantID, r.PathValue("id"), user.ID))
	result, err := h.store.SubmitGrade(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.human_grade_submitted", "review_task", result.Task.ID, "submit human grade")
	if h.disagreement != nil && result.Task.GradeRound == "single" && result.Grade.AIGradeID != "" {
		// Capture 直接从租户作用域内的存储取得两侧来源事实。
		// 临时投影错误不能使已提交成绩失效。
		_, _, _ = h.disagreement.ObserveHumanGrade(r.Context(), user.TenantID, result.Grade.AIGradeID, result.Grade.ID)
	}
	if result.FinalGrade != nil {
		h.auditAction(r, "review.double_mark_auto_finalized", "final_grade", result.FinalGrade.ID, "double mark score difference within threshold")
	}
	if result.ArbitrationTask != nil {
		h.auditAction(r, "arbitration.task_created", "arbitration_task", result.ArbitrationTask.ID, "double mark score difference exceeds threshold")
	}
	response := map[string]any{"task": result.Task, "human_grade": result.Grade}
	if result.QuestionGradeID != "" {
		response["question_grade_id"] = result.QuestionGradeID
	}
	if result.DoubleMarkSession != nil {
		response["double_mark_session"] = result.DoubleMarkSession
	}
	if result.FinalGrade != nil {
		response["final_grade"] = result.FinalGrade
	}
	if result.ArbitrationTask != nil {
		response["arbitration_task"] = result.ArbitrationTask
	}
	httpx.JSON(w, http.StatusCreated, response)
}

// trySubmitSeedGrade 返回 true 表示已写成功或错误响应；仅未接管时允许普通存储继续提交。
func (h *Handler) trySubmitSeedGrade(w http.ResponseWriter, r *http.Request, user auth.User, input SubmitGradeInput) bool {
	if h.seedHook == nil {
		return false
	}
	seedExamID, seedQuestionID := "", ""
	if h.seedRefresher != nil {
		// 任务上下文只携带合成标识，但保留聚合质量监控所需的考试与题目作用域。
		if seedContext, handled, contextErr := h.seedHook.GetGraderTaskContext(r.Context(), user.TenantID, r.PathValue("id"), user.ID); contextErr == nil && handled {
			seedExamID, seedQuestionID = seedContext.Task.ExamID, seedContext.Task.QuestionID
		}
	}
	selections := make(map[string]any, len(input.RubricSelections))
	for _, selection := range input.RubricSelections {
		selections[selection.PointID] = selection.Score
	}
	receipt, handled, seedErr := h.seedHook.TrySubmit(r.Context(), user.TenantID, r.PathValue("id"), user.ID, seedquality.SubmitInput{
		Score: input.Score, RubricSelections: selections, ExpectedRevision: input.ExpectedRevision,
	})
	// 钩子错误优先于 handled；失败不能回退普通存储，避免同次请求提交另一种任务。
	if seedErr != nil {
		writeSeedHookError(w, r, seedErr)
		return true
	}
	if !handled {
		return false
	}
	h.auditAction(r, "review.human_grade_submitted", "review_task", receipt.TaskID, "submit quality review task")
	if h.seedRefresher != nil && seedExamID != "" && seedQuestionID != "" {
		// 即使暂时无法刷新异步质量投影，盲测提交仍保持成功。
		_ = h.seedRefresher.RefreshSeedObservation(r.Context(), user.TenantID, seedExamID, seedQuestionID, user.ID)
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"task": map[string]any{
		"id": receipt.TaskID, "status": receipt.Status, "revision": receipt.Revision,
	}})
	return true
}

func (h *Handler) ReturnTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	isReviewManager := reviewManagerScoped(user)
	if !h.authorizeTaskReturn(w, r, user) {
		return
	}
	var input ReturnTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.MustOwnActiveClaim = !isReviewManager
	// 该标记由服务端身份决定，客户端不能通过请求体绕过写入时的租约检查。
	task, err := h.store.ReturnTask(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.task_returned", "review_task", task.ID, "return review task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

// 队列管理员可退回任意可变任务。复核人员只能退回已持久分配给自己且当前由自己领取的任务。
// 仅分配还不够：打开或预取任务不能授予修改队列的权限。
func (h *Handler) authorizeTaskReturn(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	if reviewManagerScoped(user) {
		return true
	}
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return false
	}
	if task.AssignedTo == "" || task.AssignedTo != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	contextStore, ok := h.store.(TaskContextStore)
	if !ok {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	taskContext, err := contextStore.GetTaskContext(r.Context(), user.TenantID, task.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return false
	}
	if taskContext.Claim.OwnerID != user.ID || taskContext.Claim.State != "claimed" {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	return true
}

func (h *Handler) GetDraft(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeAssignedReviewer(w, r, user) {
		return
	}
	draft, err := h.store.(DraftStore).GetDraft(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	if errors.Is(err, ErrNotFound) {
		httpx.JSON(w, http.StatusOK, map[string]any{"draft": nil})
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"draft": draft})
}

func (h *Handler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeAssignedReviewer(w, r, user) {
		return
	}
	var input SaveDraftInput
	if !decodeJSON(w, r, &input) {
		return
	}
	draft, err := h.store.(DraftStore).SaveDraft(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.draft_saved", "review_task", draft.ReviewTaskID, "save review draft")
	httpx.JSON(w, http.StatusOK, map[string]any{"draft": draft})
}

func (h *Handler) authorizeTaskWorker(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return false
	}
	if reviewWorkerScoped(user) && task.AssignedTo != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	return true
}

// 草稿属于被分配复核人员的可变工作成果。
// 复核管理员可以检查任务上下文和答案图像，但不能仅凭管理队列权限读取或覆盖复核人员的私有草稿。
func (h *Handler) authorizeAssignedReviewer(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return false
	}
	if task.AssignedTo == "" || task.AssignedTo != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return false
	}
	return true
}

// 队列分配的限制比 review:manage 更严格：目标必须是同一租户内启用的阅卷账户。
func (h *Handler) authorizeReviewAssignee(w http.ResponseWriter, r *http.Request, tenantID, assigneeID string) bool {
	return h.authorizeActiveRole(w, r, tenantID, assigneeID, "grader")
}

func (h *Handler) authorizeArbitrationAssignee(w http.ResponseWriter, r *http.Request, tenantID, assigneeID string) bool {
	return h.authorizeActiveRole(w, r, tenantID, assigneeID, "arbitrator")
}

func (h *Handler) authorizeActiveRole(w http.ResponseWriter, r *http.Request, tenantID, userID, roleCode string) bool {
	if userID == "" {
		writeStoreError(w, r, ErrInvalidInput)
		return false
	}
	users, err := h.audit.ListManagedUsers(r.Context(), tenantID, auth.ManagedUserFilter{UserID: userID, Limit: 2})
	if err != nil {
		writeStoreError(w, r, err)
		return false
	}
	for _, candidate := range users {
		if candidate.ID != userID || candidate.Status != "active" {
			continue
		}
		for _, role := range candidate.Roles {
			if role == roleCode {
				return true
			}
		}
		break
	}
	writeStoreError(w, r, ErrForbidden)
	return false
}

func (h *Handler) authorizeReviewManager(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	if reviewManagerScoped(user) {
		return true
	}
	writeStoreError(w, r, ErrForbidden)
	return false
}

func (h *Handler) authorizeArbitrationManager(w http.ResponseWriter, r *http.Request, user auth.User) bool {
	if arbitrationManagerScoped(user) {
		return true
	}
	writeStoreError(w, r, ErrForbidden)
	return false
}

// 原始整页可能含身份信息；即使能复核题目，也须同时满足这里的管理角色与权限条件。
func canViewOriginalReviewImage(user auth.User) bool {
	if !hasAnyRole(user, "platform_admin", "tenant_admin", "school_admin") {
		return false
	}
	return auth.HasPermission(user, "review:manage") ||
		auth.HasPermission(user, "evidence:manage") ||
		auth.HasPermission(user, "tenant:manage")
}

func hasAnyRole(user auth.User, roles ...string) bool {
	for _, current := range user.Roles {
		for _, allowed := range roles {
			if current == allowed {
				return true
			}
		}
	}
	return false
}

func (h *Handler) ClaimNextTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input NextTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if h.seedHook != nil && input.ExamID != "" && input.QuestionID != "" {
		seedTask, issued, seedErr := h.seedHook.MaybeIssue(r.Context(), user.TenantID, input.ExamID, input.QuestionID, input.QuestionID, user.ID)
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
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
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
			if h.segmentImage == nil {
				httpx.Error(w, r, http.StatusInternalServerError, "review_image_unavailable", reviewImageUnavailable)
				return
			}
			resource, err := h.segmentImage.ReadCropImage(r.Context(), user.TenantID, seedImage.AnswerSegmentID)
			if err != nil {
				segment.WriteImageError(w, r, err)
				return
			}
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
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) SetExamDoubleMarkPolicy(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input SetDoubleMarkPolicyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	policy, err := h.store.SetExamDoubleMarkPolicy(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.double_mark_policy_set", "exam", policy.ExamID, "set exam double mark policy")
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (h *Handler) SetQuestionDoubleMarkPolicy(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input SetDoubleMarkPolicyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	policy, err := h.store.SetQuestionDoubleMarkPolicy(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.double_mark_policy_set", "question", policy.QuestionID, "set question double mark policy")
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (h *Handler) ListDoubleMarkPolicies(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	policyFilter := PolicyFilter{
		ExamID:     strings.TrimSpace(r.URL.Query().Get("exam_id")),
		QuestionID: strings.TrimSpace(r.URL.Query().Get("question_id")),
	}
	access, ok := h.doubleMarkCollectionAccess(w, r, user)
	if !ok {
		return
	}
	policyFilter.Access = access
	policies, err := h.store.ListDoubleMarkPolicies(r.Context(), user.TenantID, policyFilter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policies": policies})
}

func (h *Handler) CreateDoubleMarkSession(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input CreateDoubleMarkSessionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !h.authorizeReviewAssignee(w, r, user.TenantID, input.FirstReviewerID) {
		return
	}
	if !h.authorizeReviewAssignee(w, r, user.TenantID, input.SecondReviewerID) {
		return
	}
	session, err := h.store.CreateDoubleMarkSession(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "review.double_mark_session_created", "double_mark_session", session.ID, "create double mark session")
	httpx.JSON(w, http.StatusCreated, map[string]any{"double_mark_session": session})
}

func (h *Handler) ListDoubleMarkSessions(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	sessionFilter := DoubleMarkSessionFilter{
		Status:          strings.TrimSpace(r.URL.Query().Get("status")),
		AnswerSegmentID: strings.TrimSpace(r.URL.Query().Get("answer_segment_id")),
		ExamID:          strings.TrimSpace(r.URL.Query().Get("exam_id")),
		QuestionID:      strings.TrimSpace(r.URL.Query().Get("question_id")),
	}
	access, ok := h.doubleMarkCollectionAccess(w, r, user)
	if !ok {
		return
	}
	sessionFilter.Access = access
	sessions, err := h.store.ListDoubleMarkSessions(r.Context(), user.TenantID, sessionFilter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"double_mark_sessions": sessions})
}

func (h *Handler) GetDoubleMarkSession(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	session, err := h.store.GetDoubleMarkSession(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"double_mark_session": session})
}

// 集合路由没有路径对象可供边界中间件检查，必须把认证范围交给存储筛选；请求参数只能继续缩小范围。
func (h *Handler) doubleMarkCollectionAccess(w http.ResponseWriter, r *http.Request, user auth.User) (auth.ExamCollectionAccess, bool) {
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok || scope.TenantID != user.TenantID || scope.ActorID != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return auth.ExamCollectionAccess{}, false
	}
	resolver, _ := h.audit.(auth.ResourceBoundaryResolver)
	return auth.ExamCollectionAccess{Scope: &scope, Resolver: resolver}, true
}

func (h *Handler) CreateArbitrationTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeArbitrationManager(w, r, user) {
		return
	}
	var input CreateArbitrationTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.AssignedTo != "" && !h.authorizeArbitrationAssignee(w, r, user.TenantID, input.AssignedTo) {
		return
	}
	task, err := h.store.CreateArbitrationTask(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "arbitration.task_created", "arbitration_task", task.ID, "create arbitration task")
	httpx.JSON(w, http.StatusCreated, map[string]any{"arbitration_task": task})
}

func (h *Handler) ListArbitrationTasks(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 50, 200)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 200")
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", invalidCursorMessage)
		return
	}
	filter := ArbitrationFilter{
		Status:          r.URL.Query().Get("status"),
		AssignedTo:      r.URL.Query().Get("assigned_to"),
		ExamID:          r.URL.Query().Get("exam_id"),
		Limit:           limit + 1,
		CursorCreatedAt: cursor.CreatedAt,
		CursorID:        cursor.ID,
	}
	if scope, ok := auth.AccessScopeFromContext(r.Context()); ok {
		applyArbitrationListScope(&filter, scope)
	}
	if arbitrationWorkerScoped(user) {
		filter.AssignedTo = user.ID
	}
	tasks, err := h.store.ListArbitrationTasks(r.Context(), user.TenantID, filter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	hasMore := len(tasks) > limit
	if hasMore {
		tasks = tasks[:limit]
	}
	nextCursor := ""
	if hasMore && len(tasks) > 0 {
		last := tasks[len(tasks)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"arbitration_tasks": tasks, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) GetArbitrationTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.GetArbitrationTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if arbitrationWorkerScoped(user) && task.AssignedTo != user.ID {
		writeStoreError(w, r, ErrForbidden)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"arbitration_task": task})
}

func (h *Handler) AssignArbitrationTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeArbitrationManager(w, r, user) {
		return
	}
	var input AssignArbitrationTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !h.authorizeArbitrationAssignee(w, r, user.TenantID, input.AssignedTo) {
		return
	}
	task, err := h.store.AssignArbitrationTask(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "arbitration.task_assigned", "arbitration_task", task.ID, "assign arbitration task")
	httpx.JSON(w, http.StatusOK, map[string]any{"arbitration_task": task})
}

func (h *Handler) SubmitArbitration(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(commandreceipt.WithID(r.Context(), r.Header.Get("Idempotency-Key")))
	user := mustUser(r)
	var input SubmitArbitrationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !h.authorizeArbitrationAssignee(w, r, user.TenantID, user.ID) {
		return
	}
	task, finalGrade, err := h.store.SubmitArbitration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "arbitration.submitted", "arbitration_task", task.ID, "submit arbitration final score")
	h.auditAction(r, "final_grade.created", "final_grade", finalGrade.ID, "create final grade from arbitration")
	httpx.JSON(w, http.StatusCreated, map[string]any{"arbitration_task": task, "final_grade": finalGrade})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, commandreceipt.ErrConflict) {
		httpx.Error(w, r, http.StatusConflict, "command_request_conflict", "command ID is associated with a different request")
		return
	}
	switch {
	case errors.Is(err, ErrQualificationRequired):
		httpx.Error(w, r, http.StatusForbidden, "grader_qualification_required", "complete the current question calibration before submitting R3 work")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "review_resource_not_found", "review resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_review_input", "review input is invalid")
	case errors.Is(err, ErrForbidden), errors.Is(err, auth.ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "review_action_forbidden", "review action is forbidden")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_review_transition", "review task transition is invalid")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "resource_version_conflict", "resource was updated in another session; refresh and retry")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "review_operation_failed", "review operation failed")
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func reviewWorkerScoped(user auth.User) bool {
	return !reviewManagerScoped(user)
}

func applyReviewListScope(filter *ListFilter, scope auth.AccessScope) {
	filter.ScopeMode = scope.QueryMode()
	filter.ScopeActorID = scope.ActorID
	filter.ScopeSchoolIDs = append([]string(nil), scope.SchoolIDs...)
	filter.ScopeExamIDs = append([]string(nil), scope.ExamIDs...)
	filter.ScopeTaskIDs = append([]string(nil), scope.ReviewTaskIDs...)
}

func applyArbitrationListScope(filter *ArbitrationFilter, scope auth.AccessScope) {
	filter.ScopeMode = scope.QueryMode()
	filter.ScopeActorID = scope.ActorID
	filter.ScopeSchoolIDs = append([]string(nil), scope.SchoolIDs...)
	filter.ScopeExamIDs = append([]string(nil), scope.ExamIDs...)
	filter.ScopeTaskIDs = append([]string(nil), scope.ArbitrationTaskIDs...)
}

func arbitrationWorkerScoped(user auth.User) bool {
	return !arbitrationManagerScoped(user)
}

func reviewManagerScoped(user auth.User) bool {
	return hasAnyRole(user, "platform_admin", "tenant_admin", "school_admin") && auth.HasPermission(user, "review:manage")
}

func arbitrationManagerScoped(user auth.User) bool {
	return hasAnyRole(user, "platform_admin", "tenant_admin", "school_admin") && auth.HasPermission(user, "arbitration:manage")
}

func writeSeedHookError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, commandreceipt.ErrConflict) {
		writeStoreError(w, r, err)
		return
	}
	switch {
	case errors.Is(err, seedquality.ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "review_resource_not_found", "review resource not found")
	case errors.Is(err, seedquality.ErrInvalidInput), errors.Is(err, seedquality.ErrContextUnavailable):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_review_task", "review task is invalid")
	case errors.Is(err, seedquality.ErrQualificationNeeded), errors.Is(err, seedquality.ErrSeedTaskForbidden):
		httpx.Error(w, r, http.StatusForbidden, "grader_qualification_required", "current grader qualification is required")
	case errors.Is(err, seedquality.ErrGoldSetChanged), errors.Is(err, seedquality.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "review_task_conflict", "review task state changed; reload before continuing")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "review_operation_failed", "review operation failed")
	}
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		IPAddress:  r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}
