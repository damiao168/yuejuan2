package org

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// StudentAccountProvision 只在服务端传递密码哈希；账号和 self 作用域由仓储固定生成。
type StudentAccountProvision struct {
	Student      Student
	PasswordHash string
}

// StudentAccountProvisioner 必须把名册、学籍、账号和本人权限一起提交。
// 分离的内存仓储不实现此接口，HTTP 不能退回先写名册再创建账号的非原子流程。
type StudentAccountProvisioner interface {
	CreateStudentWithAccount(context.Context, string, StudentAccountProvision) (Student, error)
	OpenStudentAccount(context.Context, string, string, string) (Student, error)
}

type studentPasswordInput struct {
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

func validateStudentPassword(w http.ResponseWriter, r *http.Request, input studentPasswordInput) bool {
	// 密码原样比较并哈希；去掉首尾空格会悄悄改变管理员实际交付给学生的凭据。
	if input.Password == "" || input.PasswordConfirm == "" {
		httpx.Error(w, r, http.StatusBadRequest, "student_password_required", "请填写初始密码和确认密码")
		return false
	}
	if input.Password != input.PasswordConfirm {
		httpx.Error(w, r, http.StatusBadRequest, "password_mismatch", "两次输入的密码不一致")
		return false
	}
	if !auth.PasswordMeetsMinimum(input.Password, auth.PasswordMinimum([]string{"student"})) {
		httpx.Error(w, r, http.StatusBadRequest, "weak_password", "学生初始密码至少需要8个字符")
		return false
	}
	return true
}

func (h *Handler) studentAccountStore(w http.ResponseWriter, r *http.Request) (StudentAccountProvisioner, bool) {
	store, ok := h.store.(StudentAccountProvisioner)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "student_account_unavailable", "当前存储不支持同时创建学生和账号，本次没有写入，请联系管理员")
	}
	return store, ok
}

// OpenStudentAccount 为既有名册补开账号，身份只能来自路径中的本校学生，不能指定其他用户或角色。
func (h *Handler) OpenStudentAccount(w http.ResponseWriter, r *http.Request) {
	var input studentPasswordInput
	if !decodeOrganizationPatch(w, r, &input) || !validateStudentPassword(w, r, input) {
		return
	}
	scope, _ := auth.AccessScopeFromContext(r.Context())
	if !h.allowsResource(r, scope, "student", r.PathValue("id")) {
		httpx.Error(w, r, http.StatusForbidden, "organization_scope_forbidden", "student is outside the current data access scope")
		return
	}
	store, ok := h.studentAccountStore(w, r)
	if !ok {
		return
	}
	hash, err := auth.HashPassword(input.Password)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "password_hash_failed", "暂时无法创建学生账号，请稍后重试")
		return
	}
	out, err := store.OpenStudentAccount(r.Context(), mustUser(r).TenantID, r.PathValue("id"), hash)
	if err != nil {
		writeStudentAccountError(w, r, err, "student_account_create_failed")
		return
	}
	h.auditAction(r, "org.student_account_opened", "student", out.ID, "open student login account")
	httpx.JSON(w, http.StatusCreated, map[string]any{"student": out})
}

func writeStudentAccountError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	switch {
	case errors.Is(err, ErrStudentAccountExists):
		httpx.Error(w, r, http.StatusConflict, "student_account_exists", "该学生已开通账号，请刷新学生列表")
	case errors.Is(err, ErrStudentAccountInactive):
		httpx.Error(w, r, http.StatusConflict, "student_account_inactive", "请先恢复学生，再开通登录账号")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "student_not_found", "学生已不存在，请刷新列表")
	default:
		writeStoreError(w, r, err, fallback, "failed to create student account")
	}
}
