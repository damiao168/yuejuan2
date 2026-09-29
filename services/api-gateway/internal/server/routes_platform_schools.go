package server

import "net/http"

// 平台学校汇总接口统一由平台管理员 guard 保护，路径中的 tenant_id 只作为受控查询条件。
func registerPlatformSchoolRoutes(mux *http.ServeMux, ctx routerContext) {
	handler := ctx.modules.PlatformSchools
	mux.Handle("GET /api/v1/platform/schools", ctx.guards.requirePlatformSchoolRead(handler.List))
	mux.Handle("GET /api/v1/platform/schools/{tenant_id}", ctx.guards.requirePlatformSchoolRead(handler.Get))
	mux.Handle("GET /api/v1/platform/schools/{tenant_id}/members", ctx.guards.requirePlatformSchoolRead(handler.Members))
	mux.Handle("GET /api/v1/platform/schools/{tenant_id}/usage", ctx.guards.requirePlatformSchoolRead(handler.Usage))
	mux.Handle("GET /api/v1/platform/schools/{tenant_id}/model-health", ctx.guards.requirePlatformSchoolRead(handler.ModelHealth))
	mux.Handle("GET /api/v1/platform/schools/{tenant_id}/activity", ctx.guards.requirePlatformSchoolRead(handler.Activity))
}
