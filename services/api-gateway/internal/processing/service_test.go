package processing

import (
	"context"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

func TestSummaryProjectsOperationalIssueWithoutChangingSourceFacts(t *testing.T) {
	// 查询应只读取投影，异常详情仍指向可解释的源页面，不修改源事实。
	store := NewMemoryStore()
	store.PutState("tenant-a", PageState{PageID: "page-a", SubmissionID: "submission-a", ExamID: "exam-a", CurrentStage: StageQualityChecked, Blocking: true, IssueCode: IssueLowImageQuality})
	store.PutState("tenant-a", PageState{PageID: "page-b", SubmissionID: "submission-b", ExamID: "exam-a", CurrentStage: StageReady})
	service := NewService(store, nil)

	summary, err := service.Summary(context.Background(), "tenant-a", "exam-a")
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.TotalPages != 2 || summary.ReadyPages != 1 || summary.BlockedPages != 1 || len(summary.Issues) != 1 || summary.Issues[0].Code != IssueLowImageQuality {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	items, err := service.ListExceptions(context.Background(), "tenant-a", ExceptionFilter{ExamID: "exam-a"})
	if err != nil || len(items.Exceptions) != 1 {
		t.Fatalf("exceptions = %#v, %v", items, err)
	}
	if items.Exceptions[0].SourceType != "submission_page" || items.Exceptions[0].SourceID != "page-a" || items.Exceptions[0].Blocking != true {
		t.Fatalf("exception must remain an explainable page projection: %#v", items.Exceptions[0])
	}
}

func TestParserQualityNeverSubstitutesTextForStructuredEvidence(t *testing.T) {
	// 数学等结构化题型缺少专用质量信号时必须保持 nil，让上层选择 abstain。
	text, math, table := 0.93, 0.78, 0.81
	quality := ParserQuality{Text: &text, MathExpression: &math, TableStructure: &table}
	if got := quality.For(assessment.SubjectMathematics, "numeric_expression"); got == nil || *got != math {
		t.Fatalf("math quality = %v, want %v", got, math)
	}
	if got := quality.For(assessment.SubjectBiology, "table_experiment"); got == nil || *got != table {
		t.Fatalf("table quality = %v, want %v", got, table)
	}
	if got := (ParserQuality{Text: &text}).For(assessment.SubjectMathematics, "numeric_expression"); got != nil {
		t.Fatalf("structured math must not silently use text quality: %v", *got)
	}
}
