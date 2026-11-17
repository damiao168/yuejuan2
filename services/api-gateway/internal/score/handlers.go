package score

import (
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	"github.com/google/uuid"
)

type Handler struct {
	store Store
	audit auth.AuditRecorder
}

func NewHandler(store Store, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, audit: audit}
}

func (h *Handler) FinalizeExam(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.store.FinalizeExam(r.Context(), user.TenantID, r.PathValue("examId"), user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "score.finalized", "exam", r.PathValue("examId"), "汇总考试成绩")
	httpx.JSON(w, http.StatusCreated, result)
}

func (h *Handler) ListExamGrades(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 50, 200)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 200")
		return
	}
	cursor, err := pagination.DecodeParts(r.URL.Query().Get("cursor"), 2)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !isGradeStatus(status) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_filter", "status is invalid")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) > 100 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_filter", "q must not exceed 100 bytes")
		return
	}
	filter := GradeListFilter{Status: status, Query: query, Limit: limit + 1}
	// 多取一条只用于判断下一页；游标由匿名码和 ID 组成，必须与存储排序保持一致。
	if len(cursor) == 2 {
		filter.CursorAnonymousCode = cursor[0]
		filter.CursorID = cursor[1]
	}
	result, err := h.store.ListExamGrades(r.Context(), user.TenantID, r.PathValue("examId"), filter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	hasMore := len(result.Grades) > limit
	if hasMore {
		result.Grades = result.Grades[:limit]
	}
	nextCursor := ""
	if hasMore && len(result.Grades) > 0 {
		last := result.Grades[len(result.Grades)-1]
		nextCursor = pagination.EncodeParts(last.AnonymousCode, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"grades": result.Grades, "total": result.Total, "filtered_total": result.FilteredTotal,
		"all_locked": result.AllLocked, "next_cursor": nextCursor, "has_more": hasMore,
	})
}

func isGradeStatus(value string) bool {
	for _, status := range Statuses() {
		if value == status {
			return true
		}
	}
	return false
}

func (h *Handler) CheckQuality(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	stage := r.URL.Query().Get("stage")
	requirePendingPublish := stage == "publish" || r.URL.Query().Get("require_publish") == "true"
	quality, err := h.store.CheckQuality(r.Context(), user.TenantID, r.PathValue("examId"), requirePendingPublish)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"quality":     quality,
		"can_publish": quality.Passed && requirePendingPublish,
		"stage":       map[bool]string{true: "publish", false: "confirmation"}[requirePendingPublish],
	})
}

func (h *Handler) ListRoster(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	examID := r.PathValue("examId")
	if _, err := uuid.Parse(examID); err != nil {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	report, err := h.store.ListRoster(r.Context(), user.TenantID, examID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"roster": report})
}

// SetAttendance 不再接受人工覆盖；旧调用明确失败，当前缺考状态始终从冻结名册与答卷关系派生。
func (h *Handler) SetAttendance(w http.ResponseWriter, r *http.Request) {
	httpx.Error(w, r, http.StatusGone, "manual_attendance_retired", "出勤状态已改为根据答卷匹配自动判断，无需手动标记缺考或恢复应考。")
}

func (h *Handler) ConfirmGrades(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(commandreceipt.WithID(r.Context(), r.Header.Get("Idempotency-Key")))
	user := mustUser(r)
	var input ConfirmInput
	if !decodeJSON(w, r, &input) {
		return
	}
	grades, err := h.store.ConfirmGrades(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "score.confirmed", "exam", r.PathValue("examId"), "确认考试成绩")
	httpx.JSON(w, http.StatusOK, map[string]any{"grades": grades})
}

func (h *Handler) PublishGrades(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(commandreceipt.WithID(r.Context(), r.Header.Get("Idempotency-Key")))
	user := mustUser(r)
	var input PublishInput
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := h.store.PublishGrades(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		if errors.Is(err, ErrQualityGateFailed) {
			httpx.JSON(w, http.StatusConflict, result)
			return
		}
		writeStoreError(w, r, err)
		return
	}
	if release := result.ScoreRelease; result.newlyPublished && release != nil {
		// 兼容响应仍沿用旧 DTO，审计统一记录正式版本，不能只留下考试 ID 而失去版本关联。
		auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: "score_release.published", TargetType: "score_release", TargetID: release.ID,
			AfterValue: map[string]any{"exam_id": release.ExamID, "version": release.Version, "supersedes_release_id": release.SupersedesReleaseID},
			Reason:     "向学生端发布成绩", IPAddress: httpx.ClientIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) GetStudentGrade(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	studentID := r.PathValue("studentId")
	if !auth.HasPermission(user, "score:manage") {
		scoped, ok := scopedStudentID(user)
		if !auth.HasPermission(user, "student:grade:read") || !ok || scoped != studentID {
			httpx.Error(w, r, http.StatusForbidden, "student_grade_scope_violation", "student can only view own grade")
			return
		}
	}
	grade, err := h.store.GetStudentGrade(r.Context(), user.TenantID, studentID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"grade": grade})
}

func (h *Handler) ExportGrades(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.store.ExportGradesCSV(r.Context(), user.TenantID, r.PathValue("examId"), user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "score.exported", "exam", r.PathValue("examId"), "导出内部核对成绩（非正式版本）")
	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+result.Filename+`"`)
	w.Header().Set("X-EduGrade-Watermark", result.Watermark)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-EduGrade-Score-Source", "internal-draft")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
}

func (h *Handler) ExportGradesReport(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, scoped := auth.AccessScopeFromContext(r.Context())
	exporter, supported := h.store.(scopedScoreReportExporter)
	if !scoped || !supported || scope.TenantID != user.TenantID {
		httpx.Error(w, r, http.StatusForbidden, "score_report_scope_required", "导出整场考试成绩需要本校完整的成绩管理权限。")
		return
	}
	result, err := exporter.ExportExamReport(r.Context(), scope, r.PathValue("examId"), user.ID)
	if errors.Is(err, errReportScope) {
		httpx.Error(w, r, http.StatusForbidden, "score_report_scope_required", "只有本校完整成绩管理权限可导出整场考试及各科排名；单科阅卷分派不包含此权限。")
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "score.report_exported", "exam", r.PathValue("examId"), "导出整场考试内部成绩报表（非正式版本）")
	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+result.Filename+`"`)
	w.Header().Set("X-EduGrade-Watermark", result.Watermark)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-EduGrade-Score-Source", "internal-draft")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
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
	case errors.Is(err, ErrPublicationUnavailable):
		httpx.Error(w, r, http.StatusServiceUnavailable, "score_publication_unavailable", "正式成绩发布服务未配置")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "score_resource_not_found", "score resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_score_input", "score input is invalid")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_score_transition", "score transition is invalid")
	case errors.Is(err, ErrQualityGateFailed):
		httpx.Error(w, r, http.StatusConflict, "score_quality_gate_failed", "score quality gate failed")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "score_operation_failed", "score operation failed")
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func scopedStudentID(user auth.User) (string, bool) {
	return auth.ScopedStudentID(user)
}

// auditAction 的 reason 是系统写入、在统一审计页“说明”列原样显示的中文说明；旧记录的英文说明由前端兼容显示。
func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		IPAddress:  httpx.ClientIP(r),
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}
