package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"net/http"
)

func registerFileRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/files", ctx.guards.requireFileManage(ctx.guards.withWorkerTaskScope(ctx.modules.Exam.FileHandler.Upload)))
	mux.Handle("GET /api/v1/files/{id}", ctx.guards.requireFileManage(ctx.modules.Exam.FileHandler.Get))
	mux.Handle("GET /api/v1/files/{id}/download", ctx.guards.requireTaskScopedFileManage("id", ctx.modules.Exam.FileHandler.Download))
	mux.Handle("DELETE /api/v1/files/{id}", ctx.guards.requireFileManage(ctx.modules.Exam.FileHandler.Delete))
	mux.Handle("GET /api/v1/system/file-reconciliation", ctx.guards.requireAuth(auth.RequireAnyRole("platform_admin")(http.HandlerFunc(ctx.modules.Exam.FileHandler.ReconciliationStatus))))

}
