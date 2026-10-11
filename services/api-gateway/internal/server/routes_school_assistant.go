package server

import (
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/schoolassistant"
)

// 学校身份只控制助手入口，各查询还会检查业务权限和请求体中的考试范围；聊天不进入通用幂等回执存储。
func registerSchoolAssistantRoutes(mux *http.ServeMux, ctx routerContext) {
	schoolassistant.RegisterRoutes(mux, ctx.modules.Assistant.Handler, ctx.guards.requireSchoolAdmin)
}
