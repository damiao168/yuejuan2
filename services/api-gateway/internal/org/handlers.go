package org

import (
	"encoding/csv"
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
	audit auth.Store
}

func NewHandler(store Store, audit auth.Store) *Handler {
	return &Handler{store: store, audit: audit}
}

// 租户创建只允许平台管理员；后续写入都沿用当前用户的租户和资源范围。
func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	if !isPlatformTenant(r) {
		httpx.Error(w, r, http.StatusForbidden, "platform_tenant_required", "tenant administration requires the platform tenant")
		return
	}
	var input struct {
		Name             string `json:"name"`
		Code             string `json:"code"`
		Status           string `json:"status"`
		AdminUsername    string `json:"admin_username"`
		AdminDisplayName string `json:"admin_display_name"`
		AdminPassword    string `json:"admin_password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Code = strings.ToLower(strings.TrimSpace(input.Code))
	input.AdminUsername = strings.TrimSpace(input.AdminUsername)
	input.AdminDisplayName = strings.TrimSpace(input.AdminDisplayName)
	if input.Name == "" || input.Code == "" || input.AdminUsername == "" || input.AdminDisplayName == "" || input.AdminPassword == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "school and administrator fields are required")
		return
	}
	if !auth.StrongPassword(input.AdminPassword) {
		httpx.Error(w, r, http.StatusBadRequest, "weak_password", "password must contain upper, lower, number and symbol and be at least 12 characters")
		return
	}
	hash, err := auth.HashPassword(input.AdminPassword)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "password_hash_failed", "failed to create school")
		return
	}
	out, err := h.store.CreateTenant(r.Context(), TenantProvision{
		Name: input.Name, Code: input.Code, Status: input.Status,
		AdminUsername: input.AdminUsername, AdminDisplayName: input.AdminDisplayName,
		PasswordHash: hash,
	})
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "tenant_create_failed", "failed to create tenant")
		return
	}
	h.auditAction(r, "org.tenant_created", "tenant", out.ID, "create tenant")
	httpx.JSON(w, http.StatusCreated, map[string]any{"tenant": out})
}

func (h *Handler) ListTenants(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	canListAll := user.TenantID == auth.PlatformTenantID && hasPermission(user, "tenant:manage")
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
	filter := TenantListFilter{Query: strings.TrimSpace(r.URL.Query().Get("q")), Limit: limit + 1}
	if len(filter.Query) > 100 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_filter", "q must not exceed 100 bytes")
		return
	}
	if len(cursor) == 2 {
		filter.CursorCode, filter.CursorID = cursor[0], cursor[1]
	}
	out, err := h.store.ListTenants(r.Context(), user.TenantID, canListAll, filter)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "tenant_list_failed", "failed to list tenants")
		return
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = pagination.EncodeParts(last.Code, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tenants": out, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	if !isPlatformTenant(r) {
		httpx.Error(w, r, http.StatusForbidden, "platform_tenant_required", "tenant administration requires the platform tenant")
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Status == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "status is required")
		return
	}
	out, err := h.store.UpdateTenantStatus(r.Context(), r.PathValue("id"), input.Status)
	if err != nil {
		httpx.Error(w, r, http.StatusNotFound, "tenant_not_found", "tenant not found")
		return
	}
	h.auditAction(r, "org.tenant_updated", "tenant", out.ID, "update tenant status")
	httpx.JSON(w, http.StatusOK, map[string]any{"tenant": out})
}

func isPlatformTenant(r *http.Request) bool {
	user := mustUser(r)
	return user.TenantID == auth.PlatformTenantID
}

func (h *Handler) CreateSchool(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, _ := auth.AccessScopeFromContext(r.Context())
	if !(scope.IsPlatform || scope.TenantWide) || (!auth.HasRole(user, "tenant_admin") && !auth.HasRole(user, "platform_admin")) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "current identity cannot create schools")
		return
	}
	var input School
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Name == "" || input.Code == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "name and code are required")
		return
	}
	out, err := h.store.CreateSchool(r.Context(), user.TenantID, input)
	if err != nil {
		writeStoreError(w, r, err, "school_create_failed", "failed to create school")
		return
	}
	h.auditAction(r, "org.school_created", "school", out.ID, "create school")
	httpx.JSON(w, http.StatusCreated, map[string]any{"school": out})
}

func (h *Handler) ListSchools(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, _ := auth.AccessScopeFromContext(r.Context())
	out, err := h.store.ListSchools(r.Context(), user.TenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "school_list_failed", "failed to list schools")
		return
	}
	filtered := out[:0]
	for _, item := range out {
		if auth.AllowsResourceBoundary(scope, auth.ResourceBoundary{TenantID: user.TenantID, SchoolID: item.ID}) {
			filtered = append(filtered, item)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"schools": filtered})
}

func (h *Handler) ListAcademicYears(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, _ := auth.AccessScopeFromContext(r.Context())
	schoolID := strings.TrimSpace(r.URL.Query().Get("school_id"))
	if schoolID != "" && !allowsSchool(scope, schoolID) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "school is outside the current data access scope")
		return
	}
	out, err := h.store.ListAcademicYears(r.Context(), user.TenantID, schoolID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "academic_year_list_failed", "failed to list academic years")
		return
	}
	filtered := out[:0]
	for _, item := range out {
		if allowsSchool(scope, item.SchoolID) {
			filtered = append(filtered, item)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"academic_years": filtered})
}

func (h *Handler) ListGradeCohorts(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, _ := auth.AccessScopeFromContext(r.Context())
	schoolID := strings.TrimSpace(r.URL.Query().Get("school_id"))
	if schoolID != "" && !allowsSchool(scope, schoolID) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "school is outside the current data access scope")
		return
	}
	out, err := h.store.ListGradeCohorts(r.Context(), user.TenantID, schoolID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "grade_cohort_list_failed", "failed to list grade cohorts")
		return
	}
	filtered := out[:0]
	for _, item := range out {
		if allowsSchool(scope, item.SchoolID) {
			filtered = append(filtered, item)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"grade_cohorts": filtered})
}

func (h *Handler) CreateGrade(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input Grade
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.SchoolID == "" || input.Name == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "school_id and name are required")
		return
	}
	if scope, _ := auth.AccessScopeFromContext(r.Context()); !allowsSchool(scope, input.SchoolID) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "school is outside the current data access scope")
		return
	}
	if input.EducationStage != "" && input.EducationStage != "junior" && input.EducationStage != "senior" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_education_stage", "education_stage must be junior or senior")
		return
	}
	out, err := h.store.CreateGrade(r.Context(), user.TenantID, input)
	if err != nil {
		writeStoreError(w, r, err, "grade_create_failed", "failed to create grade")
		return
	}
	h.auditAction(r, "org.grade_created", "grade", out.ID, "create grade")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": out})
}

func (h *Handler) ListGrades(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, _ := auth.AccessScopeFromContext(r.Context())
	schoolID := strings.TrimSpace(r.URL.Query().Get("school_id"))
	if schoolID != "" && !allowsSchool(scope, schoolID) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "school is outside the current data access scope")
		return
	}
	out, err := h.store.ListGrades(r.Context(), user.TenantID, schoolID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "grade_list_failed", "failed to list grades")
		return
	}
	filtered := out[:0]
	for _, item := range out {
		if allowsSchool(scope, item.SchoolID) {
			filtered = append(filtered, item)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"grades": filtered})
}

func (h *Handler) CreateClass(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input Class
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.SchoolID == "" || input.GradeID == "" || input.Name == "" || input.Code == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "school_id, grade_id, name and code are required")
		return
	}
	if scope, _ := auth.AccessScopeFromContext(r.Context()); !allowsSchool(scope, input.SchoolID) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "school is outside the current data access scope")
		return
	}
	out, err := h.store.CreateClass(r.Context(), user.TenantID, input)
	if err != nil {
		writeStoreError(w, r, err, "class_create_failed", "failed to create class")
		return
	}
	h.auditAction(r, "org.class_created", "class", out.ID, "create class")
	httpx.JSON(w, http.StatusCreated, map[string]any{"class": out})
}

func (h *Handler) ListClasses(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, _ := auth.AccessScopeFromContext(r.Context())
	gradeID := strings.TrimSpace(r.URL.Query().Get("grade_id"))
	out, err := h.store.ListClasses(r.Context(), user.TenantID, gradeID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "class_list_failed", "failed to list classes")
		return
	}
	filtered := out[:0]
	for _, item := range out {
		if auth.AllowsResourceBoundary(scope, auth.ResourceBoundary{TenantID: user.TenantID, SchoolID: item.SchoolID, GradeID: item.GradeID, ClassIDs: []string{item.ID}}) {
			filtered = append(filtered, item)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"classes": filtered})
}

func (h *Handler) CreateStudent(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input Student
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.SchoolID == "" || input.ClassID == "" || input.StudentNo == "" || input.Name == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "school_id, class_id, student_no and name are required")
		return
	}
	if scope, _ := auth.AccessScopeFromContext(r.Context()); !allowsSchool(scope, input.SchoolID) || !h.allowsResource(r, scope, "school_class", input.ClassID) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "student organization is outside the current data access scope")
		return
	}
	out, err := h.store.CreateStudent(r.Context(), user.TenantID, input)
	if err != nil {
		writeStoreError(w, r, err, "student_create_failed", "failed to create student")
		return
	}
	h.auditAction(r, "org.student_created", "student", out.ID, "create student")
	httpx.JSON(w, http.StatusCreated, map[string]any{"student": out})
}

func (h *Handler) ListStudents(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scope, _ := auth.AccessScopeFromContext(r.Context())
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 100, 200)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 200")
		return
	}
	parts, err := pagination.DecodeParts(r.URL.Query().Get("cursor"), 2)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return
	}
	studentIDs := []string{}
	for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, parseErr := uuid.Parse(id); parseErr != nil || len(studentIDs) >= 200 {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_student_filter", "student ids are invalid")
			return
		}
		studentIDs = append(studentIDs, id)
	}
	filter := StudentListFilter{
		ClassID: r.URL.Query().Get("class_id"), StudentIDs: studentIDs,
		Query: strings.TrimSpace(r.URL.Query().Get("q")), Limit: limit + 1,
	}
	if !scope.IsPlatform && !scope.TenantWide {
		filter.RestrictClasses = true
		filter.ClassIDs = append([]string(nil), scope.ClassIDs...)
		filter.StudentID = scope.StudentID
	}
	if len(parts) == 2 {
		filter.CursorStudentNo, filter.CursorID = parts[0], parts[1]
	}
	out, err := h.store.ListStudents(r.Context(), user.TenantID, filter)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "student_list_failed", "failed to list students")
		return
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = pagination.EncodeParts(last.StudentNo, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"students": out, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) UpdateStudent(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	out, err := h.store.UpdateStudentStatus(r.Context(), user.TenantID, r.PathValue("id"), input.Status)
	if err != nil {
		httpx.Error(w, r, http.StatusNotFound, "student_not_found", "student not found")
		return
	}
	h.auditAction(r, "org.student_updated", "student", out.ID, "update student status")
	httpx.JSON(w, http.StatusOK, map[string]any{"student": out})
}

func (h *Handler) TransferStudent(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input struct {
		ClassID   string `json:"class_id"`
		StartDate string `json:"start_date"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ClassID == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "class_id is required")
		return
	}
	if scope, _ := auth.AccessScopeFromContext(r.Context()); !h.allowsResource(r, scope, "school_class", input.ClassID) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "target class is outside the current data access scope")
		return
	}
	out, err := h.store.TransferStudent(r.Context(), user.TenantID, r.PathValue("id"), input.ClassID, input.StartDate)
	if err != nil {
		writeStoreError(w, r, err, "student_transfer_failed", "failed to transfer student")
		return
	}
	h.auditAction(r, "org.student_transferred", "student", r.PathValue("id"), "change student enrollment")
	httpx.JSON(w, http.StatusOK, map[string]any{"enrollment": out})
}

func (h *Handler) ListStudentEnrollments(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.ListStudentEnrollments(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "student_enrollment_list_failed", "failed to list student enrollments")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"enrollments": out})
}

// CSV 导入逐行报告错误并继续处理，已成功的行不因单行格式问题回滚。
func (h *Handler) ImportStudentsCSV(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	reader := csv.NewReader(r.Body)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_csv", "failed to parse csv")
		return
	}
	result := CSVImportResult{Errors: []CSVImportError{}}
	for i, row := range rows {
		if i == 0 && len(row) >= 3 && strings.EqualFold(row[0], "student_no") {
			continue
		}
		if len(row) < 4 {
			result.Errors = append(result.Errors, CSVImportError{Row: i + 1, Message: "列不完整，请使用学生导入模板重新填写"})
			continue
		}
		student := Student{StudentNo: strings.TrimSpace(row[0]), Name: strings.TrimSpace(row[1]), SchoolID: strings.TrimSpace(row[2]), ClassID: strings.TrimSpace(row[3])}
		if student.StudentNo == "" || student.Name == "" || student.SchoolID == "" || student.ClassID == "" {
			result.Errors = append(result.Errors, CSVImportError{Row: i + 1, Message: "学号、姓名、学校和班级不能为空"})
			continue
		}
		scope, _ := auth.AccessScopeFromContext(r.Context())
		if !allowsSchool(scope, student.SchoolID) || !h.allowsResource(r, scope, "school_class", student.ClassID) {
			result.Errors = append(result.Errors, CSVImportError{Row: i + 1, Message: "所选学校或班级不在你的管理范围内"})
			continue
		}
		if _, err := h.store.CreateStudent(r.Context(), user.TenantID, student); err != nil {
			message := "该行暂时无法导入，请稍后重试"
			switch {
			case errors.Is(err, ErrStudentNoConflict):
				message = "该学号已存在"
			case errors.Is(err, ErrInvalidParent):
				message = "所选学校或班级不存在，请重新选择"
			}
			result.Errors = append(result.Errors, CSVImportError{Row: i + 1, Message: message})
			continue
		}
		result.Created++
	}
	h.auditAction(r, "org.students_imported", "student", "", "import students csv")
	httpx.JSON(w, http.StatusOK, map[string]any{"result": result})
}

func (h *Handler) BindTeacherClass(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input struct {
		TeacherID string `json:"teacher_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.TeacherID == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "teacher_id is required")
		return
	}
	classID := r.PathValue("id")
	if err := h.store.BindTeacherClass(r.Context(), user.TenantID, input.TeacherID, classID); err != nil {
		if errors.Is(err, ErrInvalidTeacherBinding) {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_teacher_binding", "target user must have an active teacher role")
			return
		}
		httpx.Error(w, r, http.StatusInternalServerError, "teacher_bind_failed", "failed to bind teacher to class")
		return
	}
	h.auditAction(r, "org.teacher_bound_to_class", "class", classID, "bind teacher to class")
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "bound"})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error, code string, message string) {
	if errors.Is(err, ErrSchoolCodeConflict) {
		httpx.Error(w, r, http.StatusConflict, "school_code_conflict", "school code already exists")
		return
	}
	if errors.Is(err, ErrClassCodeConflict) {
		httpx.Error(w, r, http.StatusConflict, "class_code_conflict", "class code already exists in this grade")
		return
	}
	if errors.Is(err, ErrStudentNoConflict) {
		httpx.Error(w, r, http.StatusConflict, "student_no_conflict", "student number already exists in this school")
		return
	}
	if errors.Is(err, ErrInvalidParent) {
		switch code {
		case "grade_create_failed":
			httpx.Error(w, r, http.StatusBadRequest, "grade_school_invalid", "selected school is unavailable")
		case "class_create_failed":
			httpx.Error(w, r, http.StatusBadRequest, "class_grade_invalid", "selected grade does not belong to this school")
		case "student_create_failed":
			httpx.Error(w, r, http.StatusBadRequest, "student_class_invalid", "selected class does not belong to this school")
		case "student_transfer_failed":
			httpx.Error(w, r, http.StatusBadRequest, "student_transfer_target_invalid", "student or target class is unavailable")
		default:
			httpx.Error(w, r, http.StatusBadRequest, "invalid_parent_scope", "selected organization is unavailable")
		}
		return
	}
	httpx.Error(w, r, http.StatusInternalServerError, code, message)
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func hasPermission(user auth.User, permission string) bool {
	for _, current := range user.Permissions {
		if current == permission {
			return true
		}
	}
	return false
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

func allowsSchool(scope auth.AccessScope, schoolID string) bool {
	return scope.IsPlatform || scope.AllowsSchool(strings.TrimSpace(schoolID))
}

func (h *Handler) allowsResource(r *http.Request, scope auth.AccessScope, resourceType, resourceID string) bool {
	resolver, ok := h.audit.(auth.ResourceBoundaryResolver)
	if !ok {
		return false
	}
	boundary, err := resolver.ResolveResourceBoundary(r.Context(), scope, resourceType, strings.TrimSpace(resourceID))
	return err == nil && auth.AllowsResourceBoundary(scope, boundary)
}
