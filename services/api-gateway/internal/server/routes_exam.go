package server

import (
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/workspace"
	"net/http"
)

func registerExamRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/exams", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.CreateExam))
	mux.Handle("GET /api/v1/exam-templates", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.ListExamTemplates))
	mux.Handle("POST /api/v1/exam-sessions", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.CreateExamSession))
	mux.Handle("GET /api/v1/exam-sessions/commands/{commandId}", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.RecoverExamSessionCommand))
	mux.Handle("GET /api/v1/exams", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.ListExams))
	mux.Handle("GET /api/v1/exams/{id}", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.GetExam))
	mux.Handle("PATCH /api/v1/exams/{id}", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.UpdateExam))
	mux.Handle("POST /api/v1/exams/{id}/archive", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.Archive))
	mux.Handle("POST /api/v1/exams/{id}/status", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.UpdateStatus))
	mux.Handle("POST /api/v1/exams/{id}/candidates/refresh", ctx.guards.requireExamManage(ctx.modules.Exam.ExamHandler.RefreshCandidates))
	workspace.RegisterRoutes(mux, ctx.modules.Exam.WorkspaceHandler, ctx.guards.requireDashboardRead)
	mux.Handle("GET /api/v1/assessment/subject-profiles", ctx.guards.requireAssessmentRead(ctx.modules.Exam.AssessmentHandler.ListSubjectProfiles))
	mux.Handle("GET /api/v1/assessment/question-archetypes", ctx.guards.requireAssessmentRead(ctx.modules.Exam.AssessmentHandler.ListQuestionArchetypes))
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/assessment-profile", ctx.guards.requireAssessmentRead(ctx.guards.withScopedExam(ctx.modules.Exam.AssessmentHandler.GetQuestionConfig)))
	mux.Handle("PUT /api/v1/exams/{examId}/questions/{questionId}/assessment-profile", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.AssessmentHandler.ConfigureQuestion)))
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/assessment-snapshot", ctx.guards.requireAssessmentRead(ctx.guards.withScopedExam(ctx.modules.Exam.AssessmentHandler.GetQuestionSnapshot)))
	mathunderstanding.RegisterRoutes(mux, ctx.modules.AIGovernance.MathUnderstandingHandler, ctx.guards.requireReviewWork, ctx.guards.requireReviewManage)
	mathunderstanding.RegisterRuntimeRoutes(mux, ctx.modules.AIGovernance.MathUnderstandingHandler, func(handler http.HandlerFunc) http.Handler {
		return ctx.guards.requireWorkerExecute(ctx.guards.withWorkerTaskScope(handler))
	})
}
