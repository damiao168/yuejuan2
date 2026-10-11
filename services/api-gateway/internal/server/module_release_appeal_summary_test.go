package server

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
)

type summaryScopeExamStore struct {
	exam.Store
	scope  auth.AccessScope
	examID string
	result exam.Exam
}

func (store *summaryScopeExamStore) GetExam(_ context.Context, scope auth.AccessScope, examID string) (exam.Exam, error) {
	store.scope, store.examID = scope, examID
	return store.result, nil
}

func TestPublishedQuestionAppealExamSummaryReaderUsesOnlyAuthorizedExamScope(t *testing.T) {
	store := &summaryScopeExamStore{result: exam.Exam{ID: "exam-1", TenantID: "tenant-1", Name: "高一第一次月考", Subject: "english", ClassIDs: []string{"private-class"}, CreatedBy: "private-teacher"}}
	reader := publishedQuestionAppealExamReader{store: store}
	summary, err := reader.ReadQuestionAppealExamSummary(t.Context(), "tenant-1", "exam-1")
	if err != nil || summary.Name != "高一第一次月考" || summary.Subject != "english" {
		t.Fatalf("missing task summary: %+v %v", summary, err)
	}
	if store.examID != "exam-1" || store.scope.TenantID != "tenant-1" || store.scope.TenantWide || store.scope.IsPlatform || len(store.scope.ExamIDs) != 1 || store.scope.ExamIDs[0] != "exam-1" || len(store.scope.SchoolIDs) != 0 || len(store.scope.ClassIDs) != 0 {
		t.Fatalf("summary reader widened scope: %+v", store.scope)
	}
	for _, result := range []exam.Exam{{ID: "other-exam", TenantID: "tenant-1"}, {ID: "exam-1", TenantID: "tenant-other"}} {
		store.result = result
		if _, err := reader.ReadQuestionAppealExamSummary(t.Context(), "tenant-1", "exam-1"); !errors.Is(err, exam.ErrNotFound) {
			t.Fatalf("accepted unrelated summary: %+v %v", result, err)
		}
	}
}
