package server

import (
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"net/http"
)

func registerCaptureRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/exams/{examId}/submissions", ctx.guards.requireSubmissionManage(ctx.guards.withScopedExam(ctx.modules.Exam.SubmissionHandler.Create)))
	mux.Handle("GET /api/v1/exams/{examId}/submissions", ctx.guards.requireSubmissionManage(ctx.guards.withScopedExam(ctx.modules.Exam.SubmissionHandler.ListByExam)))
	mux.Handle("GET /api/v1/submissions/{id}", ctx.guards.requireSubmissionManage(ctx.modules.Exam.SubmissionHandler.Get))
	mux.Handle("POST /api/v1/submissions/{id}/pages", ctx.guards.requireSubmissionManage(ctx.modules.Exam.SubmissionHandler.AddPage))
	mux.Handle("PUT /api/v1/submissions/{id}/pages/{pageNo}", ctx.guards.requireSubmissionManage(ctx.modules.Exam.SubmissionHandler.ReplacePage))
	mux.Handle("GET /api/v1/submissions/{id}/pages", ctx.guards.requireSubmissionManage(ctx.modules.Exam.SubmissionHandler.ListPages))
	mux.Handle("POST /api/v1/submissions/{id}/quality-check", ctx.guards.requireSubmissionManage(ctx.modules.Exam.SubmissionHandler.QualityCheck))
	mux.Handle("POST /api/v1/submissions/{id}/run-quality-check", ctx.guards.requireSubmissionManage(ctx.modules.Capture.ImageQualityHandler.RunQualityCheck))
	mux.Handle("GET /api/v1/submission-pages/{id}/quality-runs", ctx.guards.requireSubmissionManage(ctx.modules.Capture.ImageQualityHandler.ListPageRuns))
	mux.Handle("POST /api/v1/submission-pages/{id}/quality-override", ctx.guards.requireCaptureManage(ctx.modules.Capture.ImageQualityHandler.OverridePageQuality))
	mux.Handle("POST /api/v1/submissions/{id}/status", ctx.guards.requireSubmissionManage(ctx.modules.Exam.SubmissionHandler.UpdateStatus))
	mux.Handle("POST /api/v1/exams/{examId}/capture-batches", ctx.guards.requireCaptureManage(ctx.guards.withScopedExam(ctx.modules.Capture.CaptureHandler.CreateBatch)))
	mux.Handle("GET /api/v1/exams/{examId}/capture-batches/commands/{commandId}", ctx.guards.requireCaptureManage(ctx.guards.withScopedExam(ctx.modules.Capture.CaptureHandler.RecoverBatchCommand)))
	if ctx.modules.Capture.CaptureUploadHandler != nil {
		captureupload.RegisterRoutes(mux, ctx.modules.Capture.CaptureUploadHandler, ctx.guards.requireCaptureManage)
	}
	processing.RegisterRoutes(mux, ctx.modules.Capture.ProcessingHandler, func(handler http.HandlerFunc) http.Handler {
		return ctx.guards.requireCaptureManage(ctx.guards.withScopedExam(handler))
	}, ctx.guards.requireCaptureManage, ctx.guards.requireCaptureManage)
	mux.Handle("GET /api/v1/exams/{examId}/capture-batches", ctx.guards.requireCaptureManage(ctx.guards.withScopedExam(ctx.modules.Capture.CaptureHandler.ListBatches)))
	mux.Handle("GET /api/v1/capture-batches/{id}", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.GetBatch))
	mux.Handle("GET /api/v1/capture-batches/{id}/matching-queue", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.GetMatchingQueue))
	mux.Handle("POST /api/v1/capture-batches/{id}/files", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.RegisterFile))
	mux.Handle("POST /api/v1/capture-batches/{id}/process", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ProcessBatch))
	mux.Handle("GET /api/v1/capture-batches/{id}/pages", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ListPages))
	mux.Handle("PATCH /api/v1/capture-pages/{id}", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.UpdatePage))
	mux.Handle("POST /api/v1/capture-pages/{id}/page-match/confirm", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ConfirmPageMatch))
	mux.Handle("POST /api/v1/capture-pages/{id}/delete", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.DeletePage))
	mux.Handle("POST /api/v1/capture-pages/{id}/restore", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.RestorePage))
	mux.Handle("POST /api/v1/capture-batches/{id}/submissions/split", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.SplitSubmission))
	mux.Handle("POST /api/v1/capture-batches/{id}/submissions/merge", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.MergeSubmissions))
	mux.Handle("POST /api/v1/submissions/{id}/student-match/confirm", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ConfirmStudentMatch))
	mux.Handle("POST /api/v1/submissions/{id}/student-match/unknown", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.MarkStudentUnknown))
	mux.Handle("POST /api/v1/capture-batches/{id}/cancel", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.CancelBatch))
	mux.Handle("POST /api/v1/capture-batches/{id}/reopen", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ReopenBatch))
	mux.Handle("POST /api/v1/capture-batches/{id}/complete", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.CompleteBatch))
	mux.Handle("POST /api/v1/submissions/{id}/process-pages", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ProcessSubmissionPages))
	mux.Handle("GET /api/v1/submission-pages/{id}/registration-runs", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ListRegistrationRuns))
	mux.Handle("GET /api/v1/submissions/{id}/processing-summary", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.GetProcessingSummary))
	mux.Handle("POST /api/v1/page-registration-runs/{id}/confirm", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ConfirmRegistration))
	mux.Handle("POST /api/v1/page-registration-runs/{id}/retry", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.RetryRegistration))
	mux.Handle("POST /api/v1/page-registration-runs/{id}/corrections", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.CreateRegistrationCorrection))
	mux.Handle("GET /api/v1/page-registration-runs/{id}/correction-context", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.GetRegistrationCorrectionContext))
	mux.Handle("GET /api/v1/page-registration-corrections/{id}", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.GetRegistrationCorrection))
	mux.Handle("POST /api/v1/page-registration-corrections/{id}/preview", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.PreviewRegistrationCorrection))
	mux.Handle("POST /api/v1/page-registration-corrections/{id}/apply", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.ApplyRegistrationCorrection))
	mux.Handle("POST /api/v1/page-registration-corrections/{id}/undo", ctx.guards.requireCaptureManage(ctx.modules.Capture.CaptureHandler.UndoRegistrationCorrection))
}
