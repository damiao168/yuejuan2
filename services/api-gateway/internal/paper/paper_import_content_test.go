package paper

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestFillContentPreservesBlueprintAndTeacherContentAndIsIdempotent(t *testing.T) {
	store, job := candidateImport(t)
	ctx := context.Background()
	for _, input := range []CreateQuestionInput{
		{QuestionNo: "1", QuestionType: "multiple_choice", Score: 6, SortOrder: 1},
		{QuestionNo: "2", QuestionType: "fill_blank", Score: 5, SortOrder: 2, Stem: "教师题干", AnswerKey: &AnswerKeyInput{StandardAnswer: "[0,1]"}},
		{QuestionNo: "3", QuestionType: "calculation", Score: 11, SortOrder: 3},
	} {
		if _, err := store.CreateQuestion(ctx, "tenant", "exam", "user", input); err != nil {
			t.Fatal(err)
		}
	}
	// 故意使用来源中不可靠的零分值；补齐内容必须保留考试蓝图的分值，并保留结构化答案。
	structured := []any{"B", "C"}
	processed, err := store.CompletePaperImportCandidates(ctx, "tenant", job.ID, PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "01", QuestionType: "multiple_choice", Stem: "选择题", Options: []string{"甲", "乙", "丙", "丁"}}, {CandidateID: "q2", QuestionNoRaw: "2", QuestionType: "fill_blank", Stem: "识别题干"}},
		AnswerCandidates:   []AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: structured}, {CandidateID: "a2", QuestionNoHint: "2", StandardAnswer: "(0,1)"}},
		SolutionCandidates: []SolutionCandidate{{CandidateID: "s1", QuestionNoHint: "1", RawText: "完整解析", Steps: []SolutionStep{{StepNo: 1, Content: "依据"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.FillPaperImportContent(ctx, "tenant", job.ID, "user", FillPaperImportContentInput{ExpectedGeneration: processed.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.MissingAnswers, []string{"3"}) {
		t.Fatalf("missing answers = %#v", result)
	}
	questions, _ := store.ListQuestions(ctx, "tenant", "exam")
	sort.Slice(questions, func(i, j int) bool { return questions[i].QuestionNo < questions[j].QuestionNo })
	if len(questions) != 3 || questions[0].Score != 6 || questions[1].Score != 5 || questions[2].Score != 11 {
		t.Fatalf("blueprint changed: %#v", questions)
	}
	if !reflect.DeepEqual(questions[0].AnswerKey.StandardAnswer, structured) || questions[0].Solution.RawText != "完整解析" || questions[0].Solution.VerificationStatus != "machine" || questions[0].Rubric != nil {
		t.Fatalf("content lost or incorrectly confirmed: %#v", questions[0])
	}
	if questions[1].Stem != "教师题干" || questions[1].AnswerKey.StandardAnswer != "[0,1]" {
		t.Fatalf("teacher content changed: %#v", questions[1])
	}
	assertPaperFillIdempotencyAndStateBoundaries(t, store, ctx, job, processed, questions)
}

func TestPlanContentSkipsAmbiguityAndLockedOrBankQuestions(t *testing.T) {
	draft := PaperImportDraftQuestion{QuestionNo: "1", QuestionType: "fill_blank", AnswerKey: &AnswerKeyInput{StandardAnswer: "(0,1)"}}
	for _, test := range []struct {
		name     string
		drafts   []PaperImportDraftQuestion
		question Question
		issues   []PaperImportIssue
	}{
		{"duplicate candidate", []PaperImportDraftQuestion{draft, draft}, Question{QuestionNo: "1", QuestionType: "fill_blank"}, nil},
		{"different type", []PaperImportDraftQuestion{draft}, Question{QuestionNo: "1", QuestionType: "single_choice"}, nil},
		{"locked", []PaperImportDraftQuestion{draft}, Question{QuestionNo: "1", QuestionType: "fill_blank", Rubric: &Rubric{Status: "locked"}}, nil},
		{"bank", []PaperImportDraftQuestion{draft}, Question{QuestionNo: "1", QuestionType: "fill_blank", SourceBankItemID: "bank-1"}, nil},
		{"conflict", []PaperImportDraftQuestion{draft}, Question{QuestionNo: "1", QuestionType: "fill_blank"}, []PaperImportIssue{{Code: "CONFLICTING_ANSWERS", QuestionNo: "1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			patches, result := planPaperImportContent(PaperImportJob{Questions: test.drafts, StructuredIssues: test.issues}, []Question{test.question})
			if len(patches) != 0 || len(result.Skipped) != 1 || len(result.MissingAnswers) != 1 {
				t.Fatalf("unsafe content accepted: %#v", result)
			}
		})
	}
}

// 首次补齐已核对蓝图和教师内容，随后重放不能新增版本或绕过人工确认与冻结边界。
func assertPaperFillIdempotencyAndStateBoundaries(t *testing.T, store *MemoryStore, ctx context.Context, job PaperImportJob, processed PaperImportJob, questions []Question) {
	t.Helper()
	firstAnswerID, firstSolutionID := questions[0].AnswerKey.ID, questions[0].Solution.ID
	result, err := store.FillPaperImportContent(ctx, "tenant", job.ID, "user", FillPaperImportContentInput{ExpectedGeneration: processed.Generation})
	questions, _ = store.ListQuestions(ctx, "tenant", "exam")
	sort.Slice(questions, func(i, j int) bool { return questions[i].QuestionNo < questions[j].QuestionNo })
	if err != nil || len(result.FilledQuestions) != 0 || questions[0].AnswerKey.ID != firstAnswerID || questions[0].Solution.ID != firstSolutionID {
		t.Fatalf("repeat fill created versions: %#v, %v", result, err)
	}
	current, _ := store.GetPaperImport(ctx, "tenant", job.ID)
	if current.Status != "review_required" || len(current.Questions[0].HumanConfirmedFields) != 0 {
		t.Fatalf("fill silently confirmed the import: %#v", current)
	}
	for _, test := range []struct {
		tenant     string
		generation int64
		want       error
	}{{"other", processed.Generation, ErrNotFound}, {"tenant", processed.Generation + 1, ErrConflict}, {"tenant", 0, ErrInvalidInput}} {
		if _, err = store.FillPaperImportContent(ctx, test.tenant, job.ID, "user", FillPaperImportContentInput{ExpectedGeneration: test.generation}); !errors.Is(err, test.want) {
			t.Fatalf("unexpected boundary error: %v", err)
		}
	}
	store.examState["exam"] = memoryExamState{Status: "ready"}
	if _, err = store.FillPaperImportContent(ctx, "tenant", job.ID, "user", FillPaperImportContentInput{ExpectedGeneration: processed.Generation}); !errors.Is(err, ErrExamFrozen) {
		t.Fatalf("frozen exam accepted fill: %v", err)
	}
}
