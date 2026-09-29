package server

import (
	"net/http"
)

// worker 回调先验证执行权限，再按 sourceType、路径参数或 payload 绑定持久化任务，防止跨任务写回结果。
func registerWorkerRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/internal/image-quality/jobs/claim", ctx.guards.requireOCRManage(ctx.modules.Capture.ImageQualityHandler.ClaimJobs))
	mux.Handle("POST /api/v1/internal/image-quality/runs/{runId}/normalized-assets", ctx.guards.requireOCRManage(ctx.guards.withWorkerTaskSource("image_quality_run", "runId", ctx.modules.Capture.ImageQualityHandler.CreateNormalizedAssetSlot)))
	mux.Handle("POST /api/v1/internal/image-quality/runs/{runId}/result", ctx.guards.requireOCRManage(ctx.guards.withWorkerTaskSource("image_quality_run", "runId", ctx.modules.Capture.ImageQualityHandler.SubmitResult)))
	mux.Handle("POST /api/v1/internal/worker/tasks", ctx.guards.requireWorkerExecute(ctx.modules.Capture.WorkerRuntimeHandler.CreateTask))
	mux.Handle("POST /api/v1/internal/worker/tasks/claim", ctx.guards.requireWorkerExecute(ctx.modules.Capture.WorkerRuntimeHandler.Claim))
	mux.Handle("GET /api/v1/internal/worker/tasks/{taskId}", ctx.guards.requireWorkerRead(ctx.guards.withWorkerTaskScope(ctx.modules.Capture.WorkerRuntimeHandler.Get)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/heartbeat", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskScope(ctx.modules.Capture.WorkerRuntimeHandler.Heartbeat)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/complete", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskScope(ctx.modules.Capture.WorkerRuntimeHandler.Complete)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/fail", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskScope(ctx.modules.Capture.WorkerRuntimeHandler.Fail)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/cancel", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskScope(ctx.modules.Capture.WorkerRuntimeHandler.Cancel)))
	mux.Handle("POST /api/v1/internal/worker/tasks/{taskId}/requeue", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskScope(ctx.modules.Capture.WorkerRuntimeHandler.Requeue)))
	mux.Handle("POST /api/v1/internal/paper-imports/{id}/decode-result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("paper_import_job", "id", ctx.modules.Exam.PaperHandler.CompletePaperImportDecode)))
	mux.Handle("POST /api/v1/internal/paper-imports/{id}/ocr-result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("paper_import_job", "id", ctx.modules.Exam.PaperHandler.CompletePaperImportOCR)))
	mux.Handle("POST /api/v1/internal/paper-imports/{id}/formula-result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("paper_import_job", "id", ctx.modules.Exam.PaperHandler.CompletePaperImportFormula)))
	mux.Handle("POST /api/v1/internal/paper-imports/{id}/failure", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("paper_import_job", "id", ctx.modules.Exam.PaperHandler.FailPaperImportRuntime)))
	mux.Handle("POST /api/v1/internal/capture/files/{fileId}/result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("capture_file", "fileId", ctx.modules.Capture.CaptureHandler.CompleteFile)))
	mux.Handle("POST /api/v1/internal/capture/files/{fileId}/fail", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("capture_file", "fileId", ctx.modules.Capture.CaptureHandler.FailFile)))
	mux.Handle("POST /api/v1/internal/page-registration-runs/{runId}/result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("page_registration_run", "runId", ctx.modules.Capture.CaptureHandler.CompleteRegistration)))
	mux.Handle("POST /api/v1/internal/page-registration-runs/{runId}/fail", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("page_registration_run", "runId", ctx.modules.Capture.CaptureHandler.FailRegistration)))
	mux.Handle("POST /api/v1/internal/page-template-match-runs/{runId}/result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("page_template_match_run", "runId", ctx.modules.Capture.CaptureHandler.CompleteTemplateMatch)))
	mux.Handle("POST /api/v1/internal/page-template-match-runs/{runId}/fail", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("page_template_match_run", "runId", ctx.modules.Capture.CaptureHandler.FailTemplateMatch)))
	mux.Handle("POST /api/v1/internal/page-registration-corrections/{id}/result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("page_registration_correction", "id", ctx.modules.Capture.CaptureHandler.CompleteRegistrationCorrection)))
	mux.Handle("POST /api/v1/internal/page-registration-corrections/{id}/failure", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("page_registration_correction", "id", ctx.modules.Capture.CaptureHandler.FailRegistrationCorrection)))
	mux.Handle("GET /api/v1/internal/answer-segments/{id}/image", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskPayload("id", "answer_segment_id", ctx.modules.Exam.SegmentHandler.GetImage)))
	mux.Handle("POST /api/v1/internal/omr-runs/{runId}/result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("omr_run", "runId", ctx.modules.Grading.GradingHandler.CompleteOMR)))
	mux.Handle("POST /api/v1/internal/omr-runs/{runId}/failure", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("omr_run", "runId", ctx.modules.Grading.GradingHandler.FailOMR)))
	mux.Handle("POST /api/v1/internal/subjective-grading/runs/{runId}/result", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("subjective_grading_run", "runId", ctx.modules.Grading.SubjectiveHandler.CompleteWorker)))
	mux.Handle("POST /api/v1/internal/subjective-grading/runs/{runId}/failure", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("subjective_grading_run", "runId", ctx.modules.Grading.SubjectiveHandler.FailWorker)))
	mux.Handle("POST /api/v1/internal/subjective-grading/runs/{runId}/execute", ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskSource("subjective_grading_run", "runId", ctx.modules.Grading.SubjectiveHandler.ExecuteWorker)))
	mux.Handle("GET /api/v1/internal/worker/metrics", ctx.guards.requireWorkerRead(ctx.modules.Capture.WorkerRuntimeHandler.Metrics))
}
