package server

import (
	"edugrade-enterprise/services/api-gateway/internal/releasegate"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"edugrade-enterprise/services/api-gateway/internal/studentportal"
	"net/http"
)

func registerReleaseRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/exams/{examId}/finalize", ctx.guards.requireRecentPermission("score:manage", ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.FinalizeExam)))
	mux.Handle("GET /api/v1/exams/{examId}/grades", ctx.guards.requireScoreManage(ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.ListExamGrades)))
	mux.Handle("GET /api/v1/exams/{examId}/grades/quality", ctx.guards.requireScoreManage(ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.CheckQuality)))
	mux.Handle("GET /api/v1/exams/{examId}/roster", ctx.guards.requireRosterManage(ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.ListRoster)))
	mux.Handle("PUT /api/v1/exams/{examId}/roster/{studentId}/attendance", ctx.guards.requireRosterManage(ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.SetAttendance)))
	mux.Handle("POST /api/v1/exams/{examId}/confirm-grades", ctx.guards.requireRecentPermission("score:manage", ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.ConfirmGrades)))
	mux.Handle("POST /api/v1/exams/{examId}/publish", ctx.guards.requireRecentPermission("score:manage", ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.PublishGrades)))
	scoreReleaseExamManage := func(handler http.HandlerFunc) http.Handler {
		return ctx.guards.requireSensitiveMutation("score:manage", ctx.guards.withScopedExam(handler))
	}
	scoreReleaseManage := func(handler http.HandlerFunc) http.Handler {
		return ctx.guards.requireSensitiveMutation("score:manage", handler)
	}
	scorerelease.RegisterRoutes(mux, ctx.modules.Release.ScoreReleaseHandler, scoreReleaseExamManage, scoreReleaseManage, ctx.guards.requireStudentGradeAccess)
	releasegate.RegisterRoutes(mux, ctx.modules.Release.ReleaseGateHandler, scoreReleaseExamManage)
	studentportal.RegisterRoutes(mux, ctx.modules.Release.StudentPortalHandler, ctx.guards.requireStudentGradeAccess)
	mux.Handle("GET /api/v1/exams/{examId}/grades/export", ctx.guards.requireRecentPermission("score:manage", ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.ExportGrades)))
	mux.Handle("GET /api/v1/students/{studentId}/exams/{examId}/grade", ctx.guards.requireStudentGradeAccess(ctx.guards.withScopedExam(ctx.modules.Release.ScoreHandler.GetStudentGrade)))
	mux.Handle("POST /api/v1/appeals", ctx.guards.requireAppealCreate(ctx.modules.Release.AppealHandler.CreateAppeal))
	mux.Handle("GET /api/v1/appeals", ctx.guards.requireAppealRead(ctx.modules.Release.AppealHandler.ListAppeals))
	mux.Handle("GET /api/v1/appeals/statistics", ctx.guards.requireAppealManage(ctx.modules.Release.AppealHandler.Statistics))
	mux.Handle("GET /api/v1/appeals/{id}", ctx.guards.requireAppealRead(ctx.modules.Release.AppealHandler.GetAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/assign", ctx.guards.requireAppealManage(ctx.modules.Release.AppealHandler.AssignAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/recommendation", ctx.guards.requireAppealWork(ctx.modules.Release.AppealHandler.SubmitRecommendation))
	mux.Handle("POST /api/v1/appeals/{id}/review", ctx.guards.requireAppealManage(ctx.modules.Release.AppealHandler.ReviewAppeal))
	mux.Handle("POST /api/v1/appeals/{id}/close", ctx.guards.requireAppealManage(ctx.modules.Release.AppealHandler.CloseAppeal))
	mux.Handle("POST /api/v1/student/exams/{examId}/question-appeals", ctx.guards.requireStudentGradeAccess(ctx.modules.Release.PublishedQuestionAppealHandler.Create))
	mux.Handle("GET /api/v1/student/question-appeals", ctx.guards.requireStudentGradeAccess(ctx.modules.Release.PublishedQuestionAppealHandler.List))
	mux.Handle("GET /api/v1/question-appeals", ctx.guards.requireQuestionAppealRead(ctx.modules.Release.PublishedQuestionAppealHandler.List))
	mux.Handle("GET /api/v1/question-appeals/{id}", ctx.guards.requireQuestionAppealRead(ctx.modules.Release.PublishedQuestionAppealHandler.Get))
	mux.Handle("GET /api/v1/question-appeals/{id}/context", ctx.guards.requireQuestionAppealRead(ctx.modules.Release.PublishedQuestionAppealHandler.Context))
	mux.Handle("GET /api/v1/question-appeals/{id}/answer-image", ctx.guards.requireQuestionAppealRead(ctx.modules.Release.PublishedQuestionAppealHandler.AnswerImage))
	mux.Handle("POST /api/v1/question-appeals/{id}/start-review", ctx.guards.requireAppealManage(ctx.modules.Release.PublishedQuestionAppealHandler.StartReview))
	mux.Handle("POST /api/v1/question-appeals/{id}/decide", ctx.guards.requireQuestionAppealRead(ctx.modules.Release.PublishedQuestionAppealHandler.Decide))
	mux.Handle("POST /api/v1/question-appeals/{id}/resolve", ctx.guards.requireQuestionAppealRead(ctx.modules.Release.PublishedQuestionAppealHandler.Resolve))
	mux.Handle("GET /api/v1/question-appeals/{id}/events", ctx.guards.requireQuestionAppealRead(ctx.modules.Release.PublishedQuestionAppealHandler.Events))
	mux.Handle("GET /api/v1/exams/{examId}/reports/overview", ctx.guards.requireReportRead(ctx.guards.withScopedExam(ctx.modules.Release.ReportHandler.Overview)))
	mux.Handle("GET /api/v1/exams/{examId}/reports/classes", ctx.guards.requireReportRead(ctx.guards.withScopedExam(ctx.modules.Release.ReportHandler.Classes)))
	mux.Handle("GET /api/v1/exams/{examId}/reports/questions", ctx.guards.requireReportRead(ctx.guards.withScopedExam(ctx.modules.Release.ReportHandler.Questions)))
	mux.Handle("GET /api/v1/exams/{examId}/reports/grading-quality", ctx.guards.requireReportRead(ctx.guards.withScopedExam(ctx.modules.Release.ReportHandler.GradingQuality)))
	mux.Handle("GET /api/v1/students/{studentId}/reports/{examId}", ctx.guards.requireAuth(http.HandlerFunc(ctx.modules.Release.ReportHandler.StudentReport)))
	mux.Handle("POST /api/v1/exams/{examId}/reports/export", ctx.guards.requireRecentPermission("report:export", ctx.guards.withScopedExam(ctx.modules.Release.ReportHandler.Export)))
}
