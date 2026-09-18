package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"net/http"
)

func registerPaperRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("GET /api/v1/exams/{examId}/answer-sheet-templates", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.ListTemplates)))
	mux.Handle("POST /api/v1/exams/{examId}/answer-sheet-templates", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.CreateTemplate)))
	mux.Handle("PATCH /api/v1/answer-sheet-templates/{id}", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.UpdateTemplate))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/lock", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.LockTemplate))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/clone", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.CloneTemplate))
	mux.Handle("GET /api/v1/exams/{examId}/answer-sheet-template-binding", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.GetExamTemplateBinding)))
	mux.Handle("PUT /api/v1/exams/{examId}/answer-sheet-template-binding", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.BindExamTemplate)))
	mux.Handle("DELETE /api/v1/exams/{examId}/answer-sheet-template-binding", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.UnbindExamTemplate)))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/page-barcodes", ctx.guards.requireExamManage(ctx.modules.Capture.CaptureHandler.IssueTemplateBarcodes))
	mux.Handle("POST /api/v1/answer-sheet-templates/{id}/student-barcodes", ctx.guards.requireExamManage(ctx.modules.Capture.CaptureHandler.IssueStudentBarcodes))
	mux.Handle("GET /api/v1/answer-sheet-templates/{id}/print-context", ctx.guards.requireExamManage(ctx.modules.Capture.CaptureHandler.GetStudentPrintContext))
	mux.Handle("GET /api/v1/answer-sheet-print-batches/{id}/package.pdf", ctx.guards.requireExamManage(ctx.modules.Capture.CaptureHandler.DownloadStudentPrintPackage))
	mux.Handle("POST /api/v1/answer-sheet-print-sheets/{id}/revoke", ctx.guards.requireExamManage(ctx.modules.Capture.CaptureHandler.RevokeStudentSheet))
	mux.Handle("POST /api/v1/answer-sheet-print-sheets/{id}/reprint", ctx.guards.requireExamManage(ctx.modules.Capture.CaptureHandler.ReprintStudentSheet))
	mux.Handle("GET /api/v1/exams/{examId}/readiness", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.GetReadiness)))
	mux.Handle("POST /api/v1/exams/{examId}/readiness/confirm", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.ConfirmReadiness)))
	mux.Handle("POST /api/v1/exams/{examId}/start-collection", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.StartCollection)))

	mux.Handle("POST /api/v1/exams/{examId}/papers", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.CreatePaper)))
	mux.Handle("GET /api/v1/exams/{examId}/papers", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.ListPapers)))
	mux.Handle("POST /api/v1/exams/{examId}/paper-imports", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.CreatePaperImport)))
	mux.Handle("GET /api/v1/exams/{examId}/paper-imports", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.ListPaperImports)))
	mux.Handle("GET /api/v1/paper-imports/{id}", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.GetPaperImport))
	mux.Handle("POST /api/v1/paper-imports/{id}/sources", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.AddPaperImportSources))
	mux.Handle("PUT /api/v1/paper-imports/{id}/sources", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.ReplacePaperImportSources))
	mux.Handle("PUT /api/v1/paper-imports/{id}/review", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.SavePaperImportReview))
	mux.Handle("POST /api/v1/paper-imports/{id}/apply", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.ApplyPaperImport))
	mux.Handle("POST /api/v1/paper-imports/{id}/cancel", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.CancelPaperImport))
	mux.Handle("POST /api/v1/paper-imports/{id}/retry-parse", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.RetryPaperImportParse))
	mux.Handle("POST /api/v1/exams/{examId}/questions", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.CreateQuestion)))
	mux.Handle("POST /api/v1/exams/{examId}/questions/materialize-from-bank", ctx.guards.authenticate(auth.RequirePermission("exam:manage")(auth.RequirePermission("question_bank:read")(idempotency.CommandIdentity(ctx.guards.withScopedExam(ctx.modules.Exam.QuestionBankHandler.Materialize))))))
	mux.Handle("GET /api/v1/exams/{examId}/questions", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.ListQuestions)))
	mux.Handle("PATCH /api/v1/questions/{id}", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.UpdateQuestion))
	mux.Handle("DELETE /api/v1/questions/{id}", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.DeleteQuestion))
	mux.Handle("POST /api/v1/questions/{id}/rubric", ctx.guards.requireExamManage(ctx.modules.Exam.PaperHandler.CreateRubric))
	mux.Handle("POST /api/v1/exams/{examId}/validate-paper-config", ctx.guards.requireExamManage(ctx.guards.withScopedExam(ctx.modules.Exam.PaperHandler.ValidatePaperConfig)))

}
