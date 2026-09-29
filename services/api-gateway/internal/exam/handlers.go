package exam

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
)

type Handler struct {
	store Store
	audit auth.AuditRecorder
}

func NewHandler(store Store, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, audit: audit}
}

func (h *Handler) CreateExam(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	var input CreateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateCreate(input); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_exam", err.Error())
		return
	}
	out, err := h.store.CreateExam(r.Context(), scope, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.created", "exam", out.ID, "create exam")
	httpx.JSON(w, http.StatusCreated, map[string]any{"exam": out})
}

func (h *Handler) CreateExamSession(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	store, ok := h.store.(SessionStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "exam_session_unavailable", "多科目考试创建服务未配置")
		return
	}
	var input CreateSessionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	// HTTP 重放缓存与业务恢复回执必须使用同一命令键，避免产生两套操作身份。
	headerCommandID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if input.CommandID == "" {
		input.CommandID = headerCommandID
	} else if headerCommandID != "" && input.CommandID != headerCommandID {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_exam_session", "command_id 必须与 Idempotency-Key 一致")
		return
	}
	if input.CommandID != "" {
		w.Header().Set(httpx.CommandIDHeader, input.CommandID)
	}
	if err := validateCreateSession(input); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_exam_session", err.Error())
		return
	}
	out, err := store.CreateExamSession(r.Context(), scope, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam_session.created", "exam_session", out.ID, "create multi-subject exam session")
	httpx.JSON(w, http.StatusCreated, map[string]any{"exam_session": out})
}

func (h *Handler) RecoverExamSessionCommand(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	store, ok := h.store.(SessionCommandStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "exam_session_command_recovery_unavailable", "考试创建命令恢复服务未配置")
		return
	}
	out, err := store.RecoverExamSessionCommand(r.Context(), scope, user.ID, r.PathValue("commandId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set(httpx.CommandIDHeader, out.CommandID)
	w.Header().Set(httpx.OperationOutcomeHeader, out.Status)
	httpx.JSON(w, http.StatusOK, map[string]any{"command": out})
}

func (h *Handler) ListExams(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 50, 200)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "pagination limit is invalid")
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "pagination cursor is invalid")
		return
	}
	out, err := h.store.ListExams(r.Context(), scope, ListFilter{
		Status:   r.URL.Query().Get("status"),
		SchoolID: r.URL.Query().Get("school_id"),
		Limit:    limit + 1,
		CursorAt: cursor.CreatedAt,
		CursorID: cursor.ID,
	})
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "exam_list_failed", "failed to list exams")
		return
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"exams": out, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) GetExam(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	out, err := h.store.GetExam(r.Context(), scope, r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, http.StatusNotFound, "exam_not_found", "exam not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": out})
}

func (h *Handler) UpdateExam(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	var input UpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateUpdate(input); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_exam", err.Error())
		return
	}
	out, err := h.store.UpdateExam(r.Context(), scope, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.updated", "exam", out.ID, "update exam")
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": out})
}

func (h *Handler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	var input struct {
		Status           string `json:"status"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !IsValidStatus(input.Status) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_status", "unknown exam status")
		return
	}
	if input.ExpectedRevision <= 0 {
		httpx.Error(w, r, http.StatusBadRequest, "expected_revision_required", "expected_revision is required")
		return
	}
	out, err := h.store.UpdateStatus(r.Context(), scope, r.PathValue("id"), input.Status, input.ExpectedRevision)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.status_changed", "exam", out.ID, "change exam status")
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": out})
}

func (h *Handler) RefreshCandidates(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	out, err := h.store.RefreshCandidateSnapshot(r.Context(), scope, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.candidates_refreshed", "exam", r.PathValue("id"), "refresh candidate snapshot from active enrollments")
	httpx.JSON(w, http.StatusOK, map[string]any{"candidate_refresh": out})
}

func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	scope, ok := mustAccessScope(w, r)
	if !ok {
		return
	}
	var input struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ExpectedRevision <= 0 {
		httpx.Error(w, r, http.StatusBadRequest, "expected_revision_required", "expected_revision is required")
		return
	}
	out, err := h.store.UpdateStatus(r.Context(), scope, r.PathValue("id"), "archived", input.ExpectedRevision)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.archived", "exam", out.ID, "archive exam")
	httpx.JSON(w, http.StatusOK, map[string]any{"exam": out})
}

func validateCreate(input CreateInput) error {
	if input.SchoolID == "" || input.Name == "" || input.Subject == "" || input.ExamType == "" {
		return ErrInvalidInput
	}
	if input.TotalScore <= 0 {
		return errors.New("total_score must be greater than 0")
	}
	if !IsValidGradingMode(input.GradingMode) {
		return errors.New("invalid grading_mode")
	}
	if input.PublishPolicy == "" {
		return errors.New("publish_policy is required")
	}
	return nil
}

func validateCreateSession(input CreateSessionInput) error {
	if input.SchoolID == "" || input.GradeID == "" || input.Name == "" || input.ExamType == "" {
		return errors.New("学校、年级、考试名称和类型不能为空")
	}
	if !IsValidGradingMode(input.GradingMode) || input.PublishPolicy == "" {
		return errors.New("阅卷或发布设置无效")
	}
	if len(input.ClassIDs) == 0 || len(input.Subjects) == 0 {
		return errors.New("请至少选择一个班级和一个科目")
	}
	if input.CommandID != "" && (len(input.CommandID) < 8 || len(input.CommandID) > 128) {
		return errors.New("command_id 长度无效")
	}
	seen := map[string]bool{}
	validQuestionTypes := map[string]bool{"single_choice": true, "multiple_choice": true, "true_false": true, "fill_blank": true, "numeric": true, "formula": true, "short_answer": true, "calculation": true, "essay": true, "discussion": true, "coding": true}
	for _, subject := range input.Subjects {
		if subject.Subject == "" || seen[subject.Subject] || subject.TotalScore <= 0 || subject.DurationMinutes <= 0 {
			return errors.New("科目、满分或考试时长无效，且科目不能重复")
		}
		seen[subject.Subject] = true
		if subject.CandidateRule != "" && subject.CandidateRule != "all_selected_classes" && subject.CandidateRule != "subject_selected_classes" {
			return errors.New("参考范围规则无效")
		}
		total := 0.0
		for _, section := range subject.Sections {
			if section.Title == "" || !validQuestionTypes[section.QuestionType] || section.QuestionCount <= 0 || section.ScorePerQuestion <= 0 {
				return errors.New("试卷分区的名称、题型、题数或分值无效")
			}
			total += float64(section.QuestionCount) * section.ScorePerQuestion
		}
		if len(subject.Sections) > 0 && (total-subject.TotalScore > 0.001 || subject.TotalScore-total > 0.001) {
			return errors.New("各分区题目分值之和必须等于科目满分")
		}
	}
	return nil
}

func validateUpdate(input UpdateInput) error {
	if input.ExpectedRevision <= 0 {
		return errors.New("expected_revision is required")
	}
	if input.TotalScore != nil && *input.TotalScore <= 0 {
		return errors.New("total_score must be greater than 0")
	}
	if input.GradingMode != nil && !IsValidGradingMode(*input.GradingMode) {
		return errors.New("invalid grading_mode")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "exam_not_found", "exam not found")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_status_transition", "invalid exam status transition")
	case errors.Is(err, ErrLocked):
		httpx.Error(w, r, http.StatusConflict, "exam_locked", "published or archived exam cannot be modified")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_exam", "exam input is invalid")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "resource_version_conflict", "exam was changed by another user; refresh and retry")
	case errors.Is(err, ErrScopeForbidden):
		httpx.Error(w, r, http.StatusForbidden, "access_scope_forbidden", "exam is outside the assigned data scope")
	case errors.Is(err, ErrCandidatesFrozen):
		httpx.Error(w, r, http.StatusConflict, "exam_candidates_frozen", "candidate roster is frozen after readiness confirmation")
	case errors.Is(err, ErrCommandConflict):
		httpx.Error(w, r, http.StatusConflict, "idempotency_key_reused_with_different_request", "command_id was already used for another exam-session request")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "exam_operation_failed", "exam operation failed")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func mustAccessScope(w http.ResponseWriter, r *http.Request) (auth.AccessScope, bool) {
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
	}
	return scope, ok
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
