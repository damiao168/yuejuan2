package server

import (
	"net/http"
)

// OCR 和编排写回接口必须同时验证任务来源与租约；分段证据读取另用角色和权限组合，允许复核人员查看而不授予处理写权。
func registerProcessingRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/submissions/{id}/ocr-tasks", ctx.guards.requireOCRManage(ctx.modules.Capture.OCRHandler.CreateTask))
	mux.Handle("GET /api/v1/submissions/{id}/ocr-tasks", ctx.guards.requireOCRManage(ctx.modules.Capture.OCRHandler.ListBySubmission))
	mux.Handle("GET /api/v1/ocr-tasks/pending", ctx.guards.requireOCRManage(ctx.modules.Capture.OCRHandler.ListPending))
	mux.Handle("GET /api/v1/ocr-tasks/{id}", ctx.guards.requireOCRManage(ctx.modules.Capture.OCRHandler.GetTask))
	mux.Handle("GET /api/v1/ocr-tasks/{id}/input", ctx.guards.requireOCRManage(ctx.guards.withWorkerTaskSource("ocr_task", "id", ctx.modules.Capture.OCRHandler.GetTaskInput)))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/start", ctx.guards.requireOCRManage(ctx.guards.withWorkerTaskSource("ocr_task", "id", ctx.modules.Capture.OCRHandler.StartTask)))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/results", ctx.guards.requireOCRManage(ctx.guards.withWorkerTaskSource("ocr_task", "id", ctx.modules.Capture.OCRHandler.CompleteTask)))
	mux.Handle("POST /api/v1/ocr-tasks/{id}/fail", ctx.guards.requireOCRManage(ctx.guards.withWorkerTaskSource("ocr_task", "id", ctx.modules.Capture.OCRHandler.FailTask)))

	mux.Handle("POST /api/v1/submissions/{id}/segment-answers", ctx.guards.requireSegmentManage(ctx.modules.Exam.SegmentHandler.Generate))
	mux.Handle("GET /api/v1/submissions/{id}/answer-segments", ctx.guards.requireSegmentManage(ctx.modules.Exam.SegmentHandler.ListBySubmission))
	mux.Handle("PATCH /api/v1/answer-segments/{id}", ctx.guards.requireSegmentManage(ctx.modules.Exam.SegmentHandler.Update))
	mux.Handle("GET /api/v1/answer-segments/{id}/evidence", ctx.guards.requireSegmentEvidenceRead(ctx.modules.Exam.SegmentHandler.GetEvidence))
	mux.Handle("GET /api/v1/answer-segments/{id}/image", ctx.guards.requireSegmentEvidenceRead(ctx.modules.Exam.SegmentHandler.GetImage))
	mux.Handle("HEAD /api/v1/answer-segments/{id}/image", ctx.guards.requireSegmentEvidenceRead(ctx.modules.Exam.SegmentHandler.GetImage))

	mux.Handle("POST /api/v1/orchestrations", ctx.guards.requireOrchestratorManage(ctx.modules.Capture.OrchestratorHandler.CreateRun))
	mux.Handle("GET /api/v1/orchestrations/{id}", ctx.guards.requireOrchestratorManage(ctx.modules.Capture.OrchestratorHandler.GetRun))
	mux.Handle("GET /api/v1/orchestrations/{id}/tasks", ctx.guards.requireOrchestratorManage(ctx.modules.Capture.OrchestratorHandler.ListTasks))
	mux.Handle("POST /api/v1/orchestrations/{id}/tasks", ctx.guards.requireOrchestratorManage(ctx.modules.Capture.OrchestratorHandler.CreateTask))
	mux.Handle("POST /api/v1/agent-tasks/{id}/start", ctx.guards.requireOrchestratorManage(ctx.modules.Capture.OrchestratorHandler.StartTask))
	mux.Handle("POST /api/v1/agent-tasks/{id}/complete", ctx.guards.requireOrchestratorManage(ctx.modules.Capture.OrchestratorHandler.CompleteTask))
	mux.Handle("POST /api/v1/agent-tasks/{id}/fail", ctx.guards.requireOrchestratorManage(ctx.modules.Capture.OrchestratorHandler.FailTask))
	mux.Handle("POST /api/v1/agent-tasks/{id}/retry", ctx.guards.requireOrchestratorManage(ctx.modules.Capture.OrchestratorHandler.RetryTask))
}
