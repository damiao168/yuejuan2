package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"net/http"
)

// 普通文件操作按 file:manage 授权；worker 下载必须先从持久化任务恢复文件作用域，不能信任浏览器请求中的空 scope。
func registerFileRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/files", ctx.guards.requireFileManage(ctx.guards.withWorkerTaskScope(ctx.modules.Exam.FileHandler.Upload)))
	mux.Handle("GET /api/v1/files/{id}", ctx.guards.requireFileManage(ctx.modules.Exam.FileHandler.Get))
	mux.Handle("GET /api/v1/files/{id}/download", ctx.guards.requireTaskScopedFileManage("id", ctx.modules.Exam.FileHandler.Download))
	mux.Handle("DELETE /api/v1/files/{id}", ctx.guards.requireFileManage(ctx.modules.Exam.FileHandler.Delete))
	mux.Handle("GET /api/v1/system/file-reconciliation", ctx.guards.requireAuth(auth.RequireAnyRole("platform_admin")(http.HandlerFunc(ctx.modules.Exam.FileHandler.ReconciliationStatus))))

}
