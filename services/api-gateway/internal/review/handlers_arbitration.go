package review

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	"net/http"
)

func (h *Handler) CreateArbitrationTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.authorizeArbitrationManager(w, r, user) {
		return
	}
	var input CreateArbitrationTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !h.authorizeManagedBodyResource(w, r, "double_mark_session", input.DoubleMarkSessionID) {
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
		if arbitrationManagerScoped(user) && scope.QueryMode() == "school" {
			resolver, _ := h.audit.(auth.ResourceBoundaryResolver)
			filter.Access = auth.ExamCollectionAccess{Scope: &scope, Resolver: resolver, Management: true}
		}
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
	if h.privateDrafts != nil {
		_ = h.privateDrafts.Commit(r.Context(), user.TenantID, user.ID, "arbitration", r.PathValue("id"), input.ExpectedRevision)
	}
	h.auditAction(r, "arbitration.submitted", "arbitration_task", task.ID, "submit arbitration final score")
	h.auditAction(r, "final_grade.created", "final_grade", finalGrade.ID, "create final grade from arbitration")
	httpx.JSON(w, http.StatusCreated, map[string]any{"arbitration_task": task, "final_grade": finalGrade})
}
