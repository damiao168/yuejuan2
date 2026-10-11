package paper

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

func TestFillContentBatch32KeepsMissingAnswersAndSolutionVersions(t *testing.T) {
	store, job := candidateImport(t)
	ctx := context.Background()
	for _, input := range []CreateQuestionInput{
		{QuestionNo: "1", QuestionType: "fill_blank", Score: 6},
		{QuestionNo: "2", QuestionType: "fill_blank", Score: 5, AnswerKey: &AnswerKeyInput{StandardAnswer: "teacher"}},
	} {
		if _, err := store.CreateQuestion(ctx, "tenant", "exam", "user", input); err != nil {
			t.Fatal(err)
		}
	}
	// 来源只有题干和解析；有补齐结果不能代替答案，已有教师答案仍须保留。
	processed, err := store.CompletePaperImportCandidates(ctx, "tenant", job.ID, PaperImportParseResult{
		QuestionCandidates: []QuestionCandidate{
			{CandidateID: "q1", QuestionNoRaw: "01", QuestionType: "fill_blank", Stem: "题干一"},
			{CandidateID: "q2", QuestionNoRaw: "2", QuestionType: "fill_blank", Stem: "题干二"},
		},
		SolutionCandidates: []SolutionCandidate{
			{CandidateID: "s1", QuestionNoHint: "1", RawText: "解析一"},
			{CandidateID: "s2", QuestionNoHint: "2", Steps: []SolutionStep{{StepNo: 1, Content: "解析步骤"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := FillPaperImportContentInput{ExpectedGeneration: processed.Generation}
	result, err := store.FillPaperImportContent(ctx, "tenant", job.ID, "user", input)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(result.FilledQuestions)
	if !reflect.DeepEqual(result.FilledQuestions, []string{"1", "2"}) || !reflect.DeepEqual(result.MissingAnswers, []string{"1"}) {
		t.Fatalf("content fill changed answer availability: %#v", result)
	}
	questions, err := store.ListQuestions(ctx, "tenant", "exam")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(questions, func(i, j int) bool { return questions[i].QuestionNo < questions[j].QuestionNo })
	if len(questions) != 2 || questions[0].AnswerKey != nil || questions[1].AnswerKey == nil || questions[1].AnswerKey.StandardAnswer != "teacher" || questions[0].Score != 6 || questions[1].Score != 5 {
		t.Fatalf("blueprint or existing answer changed: %#v", questions)
	}
	firstIDs := map[string]string{}
	for _, question := range questions {
		if question.Solution == nil || question.Solution.VerificationStatus != "machine" || question.PaperImportID != job.ID {
			t.Fatalf("solution or import provenance missing: %#v", question)
		}
		firstIDs[question.ID] = question.Solution.ID
	}
	result, err = store.FillPaperImportContent(ctx, "tenant", job.ID, "user", input)
	if err != nil || len(result.FilledQuestions) != 0 || !reflect.DeepEqual(result.MissingAnswers, []string{"1"}) {
		t.Fatalf("repeat fill result = %#v, %v", result, err)
	}
	questions, err = store.ListQuestions(ctx, "tenant", "exam")
	if err != nil {
		t.Fatal(err)
	}
	for _, question := range questions {
		if question.Solution == nil || question.Solution.ID != firstIDs[question.ID] {
			t.Fatalf("repeat fill allocated a solution version: %#v", question)
		}
	}
}

func TestPlanContentBatch32PreservesSkipReasonPriority(t *testing.T) {
	draft := PaperImportDraftQuestion{QuestionNo: "1", QuestionType: "fill_blank", Stem: "识别题干"}
	question := Question{QuestionNo: "1", QuestionType: "fill_blank"}
	protected := question
	protected.SourceType, protected.Rubric = "question_bank", &Rubric{Status: "locked"}
	conflict := []PaperImportIssue{{Code: "CONFLICTING_ANSWERS", QuestionNo: "1"}}
	duplicate := question
	duplicate.QuestionNo = "01"
	parent := question
	parent.ParentQuestionNo = "parent"
	subquestion := question
	subquestion.SubquestionNo = "sub"
	bankID := question
	bankID.SourceBankItemID = "bank-item"
	bankID.Rubric = &Rubric{Status: "locked"}
	locked := parent
	locked.Rubric = &Rubric{Status: "locked"}
	// 同时满足多个拒绝条件时，用户看到的原因仍按原流程的业务优先级报告。
	for _, test := range []struct {
		name      string
		drafts    []PaperImportDraftQuestion
		questions []Question
		issues    []PaperImportIssue
		reason    string
	}{
		{"missing before protections", nil, []Question{protected}, conflict, "没有对应识别内容"},
		{"duplicate candidate before conflict", []PaperImportDraftQuestion{draft, draft}, []Question{protected}, conflict, "题号不唯一，请核对对应关系"},
		{"duplicate existing number", []PaperImportDraftQuestion{draft}, []Question{question, duplicate}, conflict, "题号不唯一，请核对对应关系"},
		{"conflict before protections", []PaperImportDraftQuestion{draft}, []Question{protected}, conflict, "识别内容存在冲突，请核对来源"},
		{"duplicate issue code", []PaperImportDraftQuestion{draft}, []Question{question}, []PaperImportIssue{{Code: "DUPLICATE_QUESTION_NO", QuestionNo: "01"}}, "识别内容存在冲突，请核对来源"},
		{"human conflict code", []PaperImportDraftQuestion{draft}, []Question{question}, []PaperImportIssue{{Code: "HUMAN_CONFIRMED_CONFLICT", QuestionNo: "1"}}, "识别内容存在冲突，请核对来源"},
		{"bank type before locked", []PaperImportDraftQuestion{draft}, []Question{protected}, nil, "保留题库题目的原内容"},
		{"bank ID before locked", []PaperImportDraftQuestion{draft}, []Question{bankID}, nil, "保留题库题目的原内容"},
		{"locked before parent mismatch", []PaperImportDraftQuestion{draft}, []Question{locked}, nil, "保留已锁定题目的内容"},
		{"parent mismatch", []PaperImportDraftQuestion{draft}, []Question{parent}, nil, "题型或子题结构不同，请核对对应关系"},
		{"subquestion mismatch", []PaperImportDraftQuestion{draft}, []Question{subquestion}, nil, "题型或子题结构不同，请核对对应关系"},
	} {
		t.Run(test.name, func(t *testing.T) {
			patches, result := planPaperImportContent(PaperImportJob{Questions: test.drafts, StructuredIssues: test.issues}, test.questions)
			if len(patches) != 0 || len(result.FilledQuestions) != 0 || len(result.Skipped) != len(test.questions) || len(result.MissingAnswers) != len(test.questions) {
				t.Fatalf("protected questions produced content: %#v", result)
			}
			for i, skip := range result.Skipped {
				if skip.QuestionNo != test.questions[i].QuestionNo || skip.Reason != test.reason || result.MissingAnswers[i] != test.questions[i].QuestionNo {
					t.Fatalf("skip priority or input order changed: %#v", result)
				}
			}
		})
	}
}

func TestPlanContentBatch32PreservesIndependentEmptyFields(t *testing.T) {
	for _, test := range []paperIndependentContentCase{
		{"empty candidate answer", nil, nil, &AnswerKeyInput{StandardAnswer: " "}, &SolutionInput{RawText: "解析"}, false, true, true},
		{"existing answer and steps", &AnswerKey{StandardAnswer: "teacher"}, &QuestionSolution{Steps: []SolutionStep{{Content: "已有步骤"}}}, &AnswerKeyInput{StandardAnswer: "candidate"}, &SolutionInput{RawText: "候选解析"}, false, false, false},
		{"existing raw solution", nil, &QuestionSolution{RawText: "已有解析"}, &AnswerKeyInput{StandardAnswer: "candidate"}, &SolutionInput{Steps: []SolutionStep{{Content: "候选步骤"}}}, true, false, false},
		{"step-only candidate solution", nil, nil, nil, &SolutionInput{RawText: " ", Steps: []SolutionStep{{Content: "候选步骤"}}}, false, true, true},
		{"empty candidate solution", nil, nil, &AnswerKeyInput{StandardAnswer: "candidate"}, &SolutionInput{RawText: " "}, true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperIndependentEmptyFields(t, test)
		})
	}
}

type paperIndependentContentCase struct {
	name          string
	answer        *AnswerKey
	solution      *QuestionSolution
	draftAnswer   *AnswerKeyInput
	draftSolution *SolutionInput
	wantAnswer    bool
	wantSolution  bool
	wantMissing   bool
}

func assertPaperIndependentEmptyFields(t *testing.T, test paperIndependentContentCase) {
	t.Helper()
	question := Question{QuestionNo: "1", QuestionType: "fill_blank", Score: 7, AnswerKey: test.answer, Solution: test.solution}
	draft := PaperImportDraftQuestion{QuestionNo: "1", QuestionType: "fill_blank", Score: 99, Stem: "识别题干", Options: []string{"A"}, AnswerKey: test.draftAnswer, Solution: test.draftSolution, Rubric: &RubricInput{Status: "locked"}, HumanConfirmedFields: []string{"answer", "solution", "rubric"}}
	patches, result := planPaperImportContent(PaperImportJob{Questions: []PaperImportDraftQuestion{draft}}, []Question{question})
	if len(patches) != 1 || !reflect.DeepEqual(result.FilledQuestions, []string{"1"}) || len(result.Skipped) != 0 || (len(result.MissingAnswers) == 1) != test.wantMissing {
		t.Fatalf("independent field result = %#v", result)
	}
	patch := patches[0]
	if !patch.stem || !patch.options || (patch.draft.AnswerKey != nil) != test.wantAnswer || (patch.draft.Solution != nil) != test.wantSolution {
		t.Fatalf("independent field patch = %#v", patch)
	}
	if patch.draft.Score != question.Score || patch.draft.Rubric != nil || patch.draft.HumanConfirmedFields != nil {
		t.Fatalf("fill changed blueprint or human confirmation: %#v", patch.draft)
	}
	if draft.Score != 99 || draft.Rubric == nil || len(draft.HumanConfirmedFields) != 3 {
		t.Fatalf("planning changed its input: %#v", draft)
	}
}
