package review

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeReviewManager(w, r, user) {
		return
	}
	var input CreateTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !h.authorizeManagedSegment(w, r, user, input.AnswerSegmentID) {
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
	httpx.JSON(w, http.StatusCreated, map[string]any{"task": graderTaskView(user, task)})
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
	if value := r.URL.Query().Get("statistics"); value != "" {
		filter.IncludeStatistics, err = strconv.ParseBool(value)
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_filter", "statistics must be a boolean")
			return
		}
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
	for i := range tasks {
		tasks[i] = graderTaskView(user, tasks[i])
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
	httpx.JSON(w, http.StatusOK, map[string]any{"task": graderTaskView(user, task)})
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
	httpx.JSON(w, http.StatusOK, map[string]any{"task": graderTaskView(user, task)})
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
	// 集合路径没有对象 ID；先核完全部任务，不能在混入外校任务时先分派本校部分。
	for _, taskID := range input.TaskIDs {
		if !h.authorizeManagedBodyResource(w, r, "review_task", taskID) {
			return
		}
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
	httpx.JSON(w, http.StatusOK, map[string]any{"task": graderTaskView(user, task)})
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
