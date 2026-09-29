package auth

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrBootstrapAlreadyCompleted = errors.New("bootstrap already completed")
	ErrWeakBootstrapPassword     = errors.New("bootstrap password does not meet strength requirements")
	ErrInvalidBootstrapInput     = errors.New("invalid bootstrap input")
)

type BootstrapAdminInput struct {
	TenantCode  string
	RoleCode    string
	Username    string
	DisplayName string
	Password    string
}

type BootstrapAdminResult struct {
	TenantID   string `json:"tenant_id"`
	TenantCode string `json:"tenant_code"`
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	RoleCode   string `json:"role_code"`
}

type BootstrapStore interface {
	ActiveAdminExists(ctx context.Context, tenantCode string, roleCode string) (bool, error)
	UpsertBootstrapAdmin(ctx context.Context, input BootstrapAdminInput, passwordHash string) (BootstrapAdminResult, error)
}

// BootstrapInitialAdmin 创建首次部署需要的平台管理员；只接受 platform 租户及 platform_admin 角色。
// 已有启用的管理员时拒绝初始化，后续账号管理应使用正常管理接口。
func BootstrapInitialAdmin(ctx context.Context, store BootstrapStore, input BootstrapAdminInput) (BootstrapAdminResult, error) {
	input = normalizeBootstrapAdminInput(input)
	if input.TenantCode != "platform" || input.RoleCode != "platform_admin" {
		return BootstrapAdminResult{}, ErrInvalidBootstrapInput
	}
	if input.Username == "" {
		return BootstrapAdminResult{}, ErrInvalidBootstrapInput
	}
	if !bootstrapFieldsWithinLimits(input) {
		return BootstrapAdminResult{}, ErrInvalidBootstrapInput
	}
	if !strongBootstrapPassword(input.Password) {
		return BootstrapAdminResult{}, ErrWeakBootstrapPassword
	}
	exists, err := store.ActiveAdminExists(ctx, input.TenantCode, input.RoleCode)
	if err != nil {
		return BootstrapAdminResult{}, err
	}
	if exists {
		return BootstrapAdminResult{}, ErrBootstrapAlreadyCompleted
	}
	hash, err := HashPassword(input.Password)
	if err != nil {
		return BootstrapAdminResult{}, err
	}
	return store.UpsertBootstrapAdmin(ctx, input, hash)
}

func normalizeBootstrapAdminInput(input BootstrapAdminInput) BootstrapAdminInput {
	input.TenantCode = strings.TrimSpace(input.TenantCode)
	if input.TenantCode == "" {
		input.TenantCode = "platform"
	}
	input.RoleCode = strings.TrimSpace(input.RoleCode)
	if input.RoleCode == "" {
		input.RoleCode = "platform_admin"
	}
	input.Username = strings.TrimSpace(input.Username)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		input.DisplayName = input.Username
	}
	return input
}

func strongBootstrapPassword(password string) bool {
	return StrongPassword(password)
}

func StrongPassword(password string) bool {
	if len([]rune(password)) < 15 {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(password))
	if normalized == "" {
		return false
	}
	_, blocked := commonPasswordBlocklist[normalized]
	return !blocked
}

var commonPasswordBlocklist = map[string]struct{}{
	"123456789012345":  {},
	"passwordpassword": {},
	"qwertyuiopasdfgh": {},
	"adminadminadmin":  {},
	"changemechangeme": {},
}
