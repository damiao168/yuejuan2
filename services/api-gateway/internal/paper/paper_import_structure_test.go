package paper

import (
	"context"
	"errors"
	"testing"
)

func TestStructurePreviewSeparatesPresetExtrasAndUncertainScore(t *testing.T) {
	job := PaperImportJob{ID: "import", Generation: 1, Sources: []PaperImportSource{{DetectedRole: "mixed"}}, Questions: []PaperImportDraftQuestion{{QuestionNo: "1", QuestionType: "calculation", Stem: "比赛得1分", Score: 1, HumanConfirmedFields: []string{"score"}}}}
	qs := []Question{{ID: "q1", QuestionNo: "1", QuestionType: "calculation", Score: 11, ScoreSource: "preset", SortOrder: 1}, {ID: "q2", QuestionNo: "2", QuestionType: "calculation", Score: 11, ScoreSource: "preset", SortOrder: 2}}
	preview, drafts := planPaperStructure(job, qs, 22)
	if preview.RecognizedCount != 1 || len(preview.MissingScores) != 1 || drafts[0].Score != 0 || preview.Changes[1].Action != "remove" {
		t.Fatalf("unsafe preset reconciliation: %#v %#v", preview, drafts)
	}
	qs[1].Stem = "教师补充内容"
	preview, _ = planPaperStructure(job, qs, 22)
	if len(preview.BlockedReasons) == 0 || preview.Changes[1].Action != "conflict" {
		t.Fatal("authored extra silently removed")
	}
	job.Sources = []PaperImportSource{{DetectedRole: "answer"}}
	preview, _ = planPaperStructure(job, qs, 22)
	if preview.PaperDetected || len(preview.BlockedReasons) == 0 {
		t.Fatal("answer-only material established paper structure")
	}
	qs[0].ScoreSource = "human"
	job.Questions[0].Score, job.Questions[0].ScoreSource = 12, "material"
	_, drafts = planPaperStructure(job, qs, 22)
	if drafts[0].Score != 11 || drafts[0].ScoreSource != "human" {
		t.Fatal("material score replaced authored score")
	}
}

func TestAdoptStructurePreservesAuthoredFieldsAndRejectsOldPreview(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	q, _ := s.CreateQuestion(ctx, "tenant", "exam", "user", CreateQuestionInput{QuestionNo: "1", QuestionType: "single_choice", Score: 5, Stem: "教师题干", AnswerKey: &AnswerKeyInput{StandardAnswer: "A"}})
	extra, _ := s.CreateQuestion(ctx, "tenant", "exam", "user", CreateQuestionInput{QuestionNo: "2", QuestionType: "calculation", Score: 11})
	s.mu.Lock()
	v := s.questions[extra.ID]
	v.ScoreSource = "preset"
	s.questions[v.ID] = v
	s.mu.Unlock()
	job, _ := s.CreatePaperImport(ctx, "tenant", "exam", "user", CreatePaperImportInput{Subject: "math", Sources: []CreatePaperImportSourceInput{{FileAssetID: "asset", RoleHint: "question"}}})
	job, _ = s.CompletePaperImportCandidates(ctx, "tenant", job.ID, PaperImportParseResult{Documents: []PaperImportDetectedDocument{{SourceID: job.Sources[0].ID, DetectedRole: "question"}}, QuestionCandidates: []QuestionCandidate{{QuestionNoRaw: "1", QuestionType: "single_choice", Stem: "资料题干"}}})
	preview, err := s.PreviewPaperStructure(ctx, "tenant", job.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := AdoptPaperStructureInput{ExpectedGeneration: job.Generation, PreviewHash: preview.PreviewHash, CompletePaperConfirmed: true}
	input.CompletePaperConfirmed = false
	if _, err = s.AdoptPaperStructure(ctx, "tenant", job.ID, "user", input); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("incomplete paper was adopted")
	}
	input.CompletePaperConfirmed = true
	s.mu.Lock()
	v = s.questions[q.ID]
	v.Stem = "更新后的教师题干"
	s.questions[v.ID] = v
	s.mu.Unlock()
	if _, err = s.AdoptPaperStructure(ctx, "tenant", job.ID, "user", input); !errors.Is(err, ErrConflict) {
		t.Fatal("stale preview overwrote new edits")
	}
	preview, _ = s.PreviewPaperStructure(ctx, "tenant", job.ID)
	input.PreviewHash = preview.PreviewHash
	if _, err = s.AdoptPaperStructure(ctx, "tenant", job.ID, "user", input); err != nil {
		t.Fatal(err)
	}
	qs, _ := s.ListQuestions(ctx, "tenant", "exam")
	if len(qs) != 1 || qs[0].Stem != "更新后的教师题干" || qs[0].AnswerKey.StandardAnswer != "A" || qs[0].Score != 5 {
		t.Fatalf("authored content lost: %#v", qs)
	}
	confirmed, _ := s.GetPaperImport(ctx, "tenant", job.ID)
	if confirmed.StructureConfirmedGeneration != job.Generation {
		t.Fatal("structure receipt missing")
	}
}

func TestMissingScoreAndUnconfirmedMaterialBlockReadiness(t *testing.T) {
	result := buildReadiness(10, 1, 1, []Paper{{ID: "paper"}}, []Question{{QuestionNo: "1", Score: 10}, {QuestionNo: "2", Score: 0}}, nil)
	for _, check := range result.Checks {
		if check.Code == "question_scores" && check.Passed {
			t.Fatal("total masked missing score")
		}
	}
	before := withStructureReadiness(ReadinessResult{Ready: true, ConfigurationHash: "x"}, PaperImportJob{ID: "i", Generation: 2, Status: "review_required", StructureConfirmedGeneration: 1})
	if before.Ready {
		t.Fatal("old structure confirmation allowed new materials")
	}
	after := withStructureReadiness(ReadinessResult{Ready: true, ConfigurationHash: "x"}, PaperImportJob{ID: "i", Generation: 2, Status: "review_required", StructureConfirmedGeneration: 2})
	if !after.Ready || before.ConfigurationHash == after.ConfigurationHash {
		t.Fatal("structure confirmation missing from readiness hash")
	}
}

func TestStructureDoesNotConfirmConflictingAnswersOrCountChildrenAsWholeQuestions(t *testing.T) {
	job := PaperImportJob{ID: "i", Generation: 1, Sources: []PaperImportSource{{DetectedRole: "question"}}, Questions: []PaperImportDraftQuestion{{QuestionNo: "18", QuestionType: "calculation", Stem: "比赛题", Score: 12, ScoreSource: "material", AnswerKey: &AnswerKeyInput{StandardAnswer: "冲突答案"}}}, StructuredIssues: []PaperImportIssue{{Code: "CONFLICTING_ANSWERS", QuestionNo: "18"}}}
	preview, drafts := planPaperStructure(job, nil, 12)
	if len(preview.BlockedReasons) != 0 || drafts[0].Score != 12 || drafts[0].AnswerKey != nil {
		t.Fatalf("structure accepted answer conflict: %#v %#v", preview, drafts)
	}
	job.Questions[0].ParentQuestionNo, job.Questions[0].SubquestionNo = "18", "1"
	job.Questions[0].QuestionNo = "18(1)"
	preview, _ = planPaperStructure(job, nil, 12)
	if len(preview.BlockedReasons) == 0 {
		t.Fatal("independent child expanded the formal paper structure")
	}
}
