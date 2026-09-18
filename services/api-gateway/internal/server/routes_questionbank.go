package server

import (
	"net/http"
)

func registerQuestionBankRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("GET /api/v1/question-banks", ctx.guards.requireQuestionBankAny(ctx.modules.Exam.QuestionBankHandler.ListBanks, "read", "manage"))
	mux.Handle("POST /api/v1/question-banks", ctx.guards.requireQuestionBank("create", ctx.modules.Exam.QuestionBankHandler.CreateBank))
	mux.Handle("GET /api/v1/question-banks/{bankId}", ctx.guards.requireQuestionBankAny(ctx.modules.Exam.QuestionBankHandler.GetBank, "read", "manage"))
	mux.Handle("PATCH /api/v1/question-banks/{bankId}", ctx.guards.requireQuestionBank("manage", ctx.modules.Exam.QuestionBankHandler.UpdateBank))
	mux.Handle("GET /api/v1/question-banks/{bankId}/items", ctx.guards.requireQuestionBank("read", ctx.modules.Exam.QuestionBankHandler.ListItems))
	mux.Handle("POST /api/v1/question-banks/{bankId}/items", ctx.guards.requireQuestionBank("create", ctx.modules.Exam.QuestionBankHandler.CreateItem))
	mux.Handle("GET /api/v1/question-bank/items/{itemId}", ctx.guards.requireQuestionBank("read", ctx.modules.Exam.QuestionBankHandler.GetItem))
	mux.Handle("GET /api/v1/question-bank/items/{itemId}/versions", ctx.guards.requireQuestionBank("read", ctx.modules.Exam.QuestionBankHandler.ListVersions))
	mux.Handle("POST /api/v1/question-bank/items/{itemId}/versions", ctx.guards.requireQuestionBank("edit", ctx.modules.Exam.QuestionBankHandler.CreateVersion))
	mux.Handle("GET /api/v1/question-bank/versions/{versionId}", ctx.guards.requireQuestionBank("read", ctx.modules.Exam.QuestionBankHandler.GetVersion))
	mux.Handle("PATCH /api/v1/question-bank/versions/{versionId}", ctx.guards.requireQuestionBank("edit", ctx.modules.Exam.QuestionBankHandler.UpdateVersion))
	mux.Handle("PUT /api/v1/question-bank/versions/{versionId}/scoring", ctx.guards.requireQuestionBank("edit", ctx.modules.Exam.QuestionBankHandler.UpdateScoring))
	mux.Handle("GET /api/v1/question-bank/versions/{versionId}/reviews", ctx.guards.requireQuestionBank("read", ctx.modules.Exam.QuestionBankHandler.ListReviews))
	mux.Handle("POST /api/v1/question-bank/versions/{versionId}/submit-review", ctx.guards.requireQuestionBank("edit", ctx.modules.Exam.QuestionBankHandler.Transition("submit-review")))
	mux.Handle("POST /api/v1/question-bank/versions/{versionId}/approve", ctx.guards.requireQuestionBank("review", ctx.modules.Exam.QuestionBankHandler.Transition("approve")))
	mux.Handle("POST /api/v1/question-bank/versions/{versionId}/return-to-draft", ctx.guards.requireQuestionBankAny(ctx.modules.Exam.QuestionBankHandler.Transition("return-to-draft"), "edit", "review"))
	mux.Handle("POST /api/v1/question-bank/versions/{versionId}/publish", ctx.guards.requireQuestionBank("publish", ctx.modules.Exam.QuestionBankHandler.Transition("publish")))
	mux.Handle("POST /api/v1/question-banks/{bankId}/reviewers", ctx.guards.requireQuestionBank("manage", ctx.modules.Exam.QuestionBankHandler.BindReviewers))
	mux.Handle("GET /api/v1/question-banks/{bankId}/metadata-schema", ctx.guards.requireQuestionBankAny(ctx.modules.Exam.QuestionBankHandler.GetMetadataSchema, "read", "manage"))
	mux.Handle("PUT /api/v1/question-banks/{bankId}/metadata-schema", ctx.guards.requireQuestionBank("manage", ctx.modules.Exam.QuestionBankHandler.UpdateMetadataSchema))
	mux.Handle("POST /api/v1/question-banks/{bankId}/metadata-schema/validate", ctx.guards.requireQuestionBankAny(ctx.modules.Exam.QuestionBankHandler.ValidateMetadata, "read", "manage"))
	mux.Handle("GET /api/v1/question-banks/{bankId}/acl", ctx.guards.requireQuestionBank("manage", ctx.modules.Exam.QuestionBankHandler.GetACL))
	mux.Handle("PUT /api/v1/question-banks/{bankId}/acl", ctx.guards.requireQuestionBank("manage", ctx.modules.Exam.QuestionBankHandler.UpdateACL))
	mux.Handle("GET /api/v1/question-bank/items", ctx.guards.requireQuestionBank("read", ctx.modules.Exam.QuestionBankHandler.SearchItems))
	mux.Handle("POST /api/v1/question-bank/items/{itemId}/retire", ctx.guards.requireQuestionBank("retire", ctx.modules.Exam.QuestionBankHandler.RetireItem))
	mux.Handle("GET /api/v1/question-bank/rubric-templates", ctx.guards.requireQuestionBank("read", ctx.modules.Exam.QuestionBankHandler.ListTemplates))
	mux.Handle("POST /api/v1/question-bank/rubric-templates", ctx.guards.requireQuestionBank("create", ctx.modules.Exam.QuestionBankHandler.CreateTemplate))
	mux.Handle("POST /api/v1/question-bank/imports/preview", ctx.guards.requireQuestionBankImport(false, ctx.modules.Exam.QuestionBankHandler.PreviewImports))
	mux.Handle("POST /api/v1/question-bank/imports/confirm", ctx.guards.requireQuestionBankImport(false, ctx.modules.Exam.QuestionBankHandler.ConfirmImportBatch))
	ctx.questionBankImportMux.Handle("POST /api/v1/question-bank/items/import-from-question/{questionId}", ctx.guards.requireQuestionBankImport(true, ctx.modules.Exam.QuestionBankHandler.ImportQuestion))
}
