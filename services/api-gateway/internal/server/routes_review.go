package server

import (
	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/backmark"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/graderdrift"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/regraderelease"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"net/http"
)

func registerReviewRoutes(mux *http.ServeMux, ctx routerContext) {
	scoreReleaseManage := func(handler http.HandlerFunc) http.Handler {
		return ctx.guards.requireSensitiveMutation("score:manage", handler)
	}
	// Gold sets, answer-group reference cases, Seed observations and drift
	// evidence are quality-management facts.  A grader receives only the
	// current calibration/Seed task through the ordinary review flow; exposing
	// these lists to review:work would reveal reference scores or make dark
	// samples identifiable.
	goldpaper.RegisterRoutes(mux, ctx.modules.Grading.GoldPaperHandler, ctx.guards.requireReviewManage, ctx.guards.requireReviewManage)
	calibration.RegisterRoutes(mux, ctx.modules.Grading.CalibrationHandler, ctx.guards.requireReviewWork, ctx.guards.requireReviewManage, ctx.guards.requireReviewWork)
	answergroup.RegisterRoutes(mux, ctx.modules.Grading.AnswerGroupHandler, ctx.guards.requireReviewManage, ctx.guards.requireReviewManage)
	seedquality.RegisterRoutes(mux, ctx.modules.Grading.SeedQualityHandler, ctx.guards.requireReviewManage, ctx.guards.requireReviewManage)
	graderdrift.RegisterRoutes(mux, ctx.modules.Grading.GraderDriftHandler, ctx.guards.requireReviewManage, ctx.guards.requireReviewManage)
	backmark.RegisterRoutes(mux, ctx.modules.Grading.BackmarkHandler, ctx.guards.requireReviewManage, ctx.guards.requireReviewWork)
	regrade.RegisterRoutes(mux, ctx.modules.Grading.RegradeHandler, ctx.guards.requireReviewManage, ctx.guards.requireReviewWork)
	regraderelease.RegisterRoutes(mux, ctx.modules.Release.RegradeReleaseHandler, scoreReleaseManage)
	if ctx.modules.Grading.QualityDashboardHandler != nil {
		qualitydashboard.RegisterRoutes(mux, ctx.modules.Grading.QualityDashboardHandler, ctx.guards.requireReviewManage)
	}
	mux.Handle("POST /api/v1/review-tasks", ctx.guards.requireReviewManage(ctx.modules.Grading.ReviewHandler.CreateTask))
	mux.Handle("GET /api/v1/review-tasks", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.ListTasks))
	mux.Handle("POST /api/v1/review-tasks/next", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.ClaimNextTask))
	mux.Handle("POST /api/v1/review-tasks/batch-assign", ctx.guards.requireReviewManage(ctx.modules.Grading.ReviewHandler.BatchAssignTasks))
	mux.Handle("GET /api/v1/review-tasks/{id}", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.GetTask))
	mux.Handle("GET /api/v1/review-tasks/{id}/context", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.GetTaskContext))
	mux.Handle("GET /api/v1/review-tasks/{id}/workspace", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.GetWorkspace))
	mux.Handle("GET /api/v1/review-tasks/{id}/segment-image", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.GetWorkspaceSegmentImage))
	mux.Handle("GET /api/v1/review-tasks/{id}/original-image", ctx.guards.requireOriginalReviewImage(ctx.modules.Grading.ReviewHandler.GetWorkspaceOriginalImage))
	mux.Handle("POST /api/v1/review-tasks/{id}/renew", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.RenewTaskClaim))
	mux.Handle("POST /api/v1/review-tasks/{id}/release", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.ReleaseTaskClaim))
	mux.Handle("POST /api/v1/review-tasks/{id}/assign", ctx.guards.requireReviewManage(ctx.modules.Grading.ReviewHandler.AssignTask))
	mux.Handle("GET /api/v1/review-commands/{commandId}", ctx.guards.requireAuth(auth.RequireAnyPermission("review:manage", "review:work", "arbitration:manage", "arbitration:work")(http.HandlerFunc(ctx.modules.Grading.ReviewHandler.RecoverCommand))))
	mux.Handle("GET /api/v1/score-commands/{commandId}", ctx.guards.requireScoreManage(ctx.modules.Release.ScoreHandler.RecoverCommand))
	mux.Handle("GET /api/v1/report-commands/{commandId}", ctx.guards.requireRecentPermission("report:export", ctx.modules.Release.ReportHandler.RecoverCommand))
	mux.Handle("POST /api/v1/review-tasks/{id}/submit", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.SubmitGrade))
	mux.Handle("POST /api/v1/review-tasks/{id}/return", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.ReturnTask))
	mux.Handle("GET /api/v1/review-tasks/{id}/draft", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.GetDraft))
	mux.Handle("PUT /api/v1/review-tasks/{id}/draft", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewHandler.SaveDraft))
	mux.Handle("GET /api/v1/review-tasks/{id}/annotations", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.ListAnnotations))
	mux.Handle("POST /api/v1/review-tasks/{id}/annotations", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.CreateAnnotation))
	mux.Handle("GET /api/v1/student/exams/{examId}/questions/{questionId}/annotations", ctx.guards.requireStudentGradeAccess(ctx.modules.Grading.ReviewAnnotationHandler.ListStudentQuestionAnnotations))
	mux.Handle("GET /api/v1/review/annotations/{annotationId}", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.GetAnnotation))
	mux.Handle("PUT /api/v1/review/annotations/{annotationId}", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.UpdateAnnotation))
	mux.Handle("DELETE /api/v1/review/annotations/{annotationId}", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.DeleteAnnotation))
	mux.Handle("GET /api/v1/review/comment-templates", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.ListCommentTemplates))
	mux.Handle("POST /api/v1/review/comment-templates", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.CreateCommentTemplate))
	mux.Handle("GET /api/v1/review/comment-templates/{templateId}", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.GetCommentTemplate))
	mux.Handle("PUT /api/v1/review/comment-templates/{templateId}", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.UpdateCommentTemplate))
	mux.Handle("DELETE /api/v1/review/comment-templates/{templateId}", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.DeleteCommentTemplate))
	mux.Handle("POST /api/v1/review/comment-templates/{shortcut}/use", ctx.guards.requireReviewWork(ctx.modules.Grading.ReviewAnnotationHandler.UseCommentTemplate))
}
