package auth

import "strings"

const (
	UserPasswordMinimum          = 8
	AdministratorPasswordMinimum = 12
)

// PasswordMinimum 按目标账号角色决定长度；兼任管理员时使用管理员要求，不能取较短角色的值。
func PasswordMinimum(roles []string) int {
	for _, role := range roles {
		switch role {
		case "school_admin", "tenant_admin", "platform_admin":
			return AdministratorPasswordMinimum
		}
	}
	return UserPasswordMinimum
}

// PasswordMeetsMinimum 只校验字符数、非空白及协议字节上限；不要求字符组合，也不查询弱密码名单。
func PasswordMeetsMinimum(password string, minimum int) bool {
	if minimum < UserPasswordMinimum {
		minimum = UserPasswordMinimum
	}
	return len([]rune(password)) >= minimum && len(password) <= maxPasswordBytes && strings.TrimSpace(password) != ""
}
