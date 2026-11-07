package paper

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBatch37StructureSeenPrecedesSourceAndRemoval(t *testing.T) {
	job, _ := structureBatch37Fixture()
	job.Sources[0].DetectedRole = "answer"
	job.QuestionCandidates = []QuestionCandidate{{QuestionNoRaw: "9"}}
	job.Questions[0].ParentQuestionNo, job.Questions[0].SubquestionNo = "1", "1"
	job.Questions = append(job.Questions, PaperImportDraftQuestion{QuestionNo: "01", QuestionType: "calculation", Stem: "duplicate"})
	qs := []Question{{ID: "q3", QuestionNo: "3", SortOrder: 3}, {ID: "q1", QuestionNo: "1", SortOrder: 1}, {ID: "q2", QuestionNo: "2", SortOrder: 2, Stem: "teacher content"}}
	preview, drafts := planPaperStructure(job, qs, 10)
	wantReasons := []string{"尚未识别到试卷题目资料；仅有答案或解析时不能确认题目结构", "第 1 题是独立子题，请先合并到对应大题并核对结构", "题目缺少试卷识别来源：1", "识别题号、题型或题干需要核对：01", "第 2 题已有内容，不能直接移除", "没有可采用的试卷题目"}
	if !reflect.DeepEqual(preview.BlockedReasons, wantReasons) || len(drafts) != 0 || len(preview.Changes) != 2 {
		t.Fatalf("source/seen/removal order changed: %#v %#v", preview, drafts)
	}
	if preview.Changes[0].QuestionID != "q2" || preview.Changes[0].Action != "conflict" || preview.Changes[1].QuestionID != "q3" || preview.Changes[1].Action != "remove" {
		t.Fatalf("unseen questions changed: %#v", preview.Changes)
	}
	if qs[0].ID != "q1" || qs[1].ID != "q2" || qs[2].ID != "q3" {
		t.Fatalf("questions no longer sorted in place: %#v", qs)
	}
	wantHash := stableContentHash(struct {
		Job       PaperImportJob
		Questions []Question
		Total     float64
	}{job, qs, 10})
	if preview.PreviewHash != wantHash {
		t.Fatal("preview hash stopped covering raw job and sorted official questions")
	}
	logStructureBatch37Proof(t, map[string]any{"preview": preview, "drafts": drafts, "questions": qs})
}

func TestBatch37StructureScoreSourcesAndDuplicateFormalNumber(t *testing.T) {
	for _, test := range []struct {
		name, source, oldSource, wantSource string
		confirmed                           bool
		oldScore, wantScore                 float64
	}{
		{name: "material", source: "material", wantSource: "material", wantScore: 5},
		{name: "human_confirmed", source: "human", confirmed: true, wantSource: "human", wantScore: 5},
		{name: "human_unconfirmed", source: "human", wantSource: "missing"},
		{name: "preset_not_material", source: "blueprint", confirmed: true, wantSource: "missing"},
		{name: "formal_human_wins", source: "material", oldSource: "human", oldScore: 7, wantSource: "human", wantScore: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			job, qs := structureBatch37Fixture()
			job.Questions[0].ScoreSource = test.source
			if test.confirmed {
				job.Questions[0].HumanConfirmedFields = []string{"score"}
			}
			qs[0].ScoreSource, qs[0].Score = test.oldSource, test.oldScore
			preview, drafts := planPaperStructure(job, qs, 10)
			if drafts[0].Score != test.wantScore || drafts[0].ScoreSource != test.wantSource || preview.RecognizedTotal != test.wantScore {
				t.Fatalf("score source changed: %#v %#v", preview, drafts)
			}
			logStructureBatch37Proof(t, map[string]any{"preview": preview, "drafts": drafts})
		})
	}
	t.Run("formal_duplicate_last_item", func(t *testing.T) {
		job, qs := structureBatch37Fixture()
		qs[0].ID, qs[0].ScoreSource, qs[0].Score = "first", "human", 3
		last := qs[0]
		last.ID, last.QuestionNo, last.SortOrder, last.Score = "last", "01", 2, 7
		qs = append(qs, last)
		preview, drafts := planPaperStructure(job, qs, 10)
		if preview.Changes[0].QuestionID != "last" || drafts[0].Score != 7 || preview.BlockedReasons[0] != "正式题号不唯一：1" {
			t.Fatalf("duplicate preview stopped using last official item: %#v %#v", preview, drafts)
		}
		logStructureBatch37Proof(t, map[string]any{"preview": preview, "drafts": drafts})
	})
}

func TestBatch37StructureCandidateAndConflictBoundaries(t *testing.T) {
	for _, code := range []string{"CONFLICTING_ANSWERS", "DUPLICATE_QUESTION_NO", "HUMAN_CONFIRMED_CONFLICT"} {
		for _, no := range []string{"", "01"} {
			t.Run(code+"/"+no, func(t *testing.T) {
				job, _ := structureBatch37Fixture()
				job.QuestionCandidates = []QuestionCandidate{{QuestionNoNormalized: "01", Options: []string{"first"}}, {QuestionNoRaw: "1", Options: []string{"later"}}}
				job.StructuredIssues = []PaperImportIssue{{Code: code, QuestionNo: no}}
				job.Questions[0].AnswerKey = &AnswerKeyInput{StandardAnswer: "unresolved"}
				job.Questions[0].Solution = &SolutionInput{RawText: "unresolved"}
				job.Questions[0].Rubric = &RubricInput{}
				preview, drafts := planPaperStructure(job, nil, 10)
				if len(preview.BlockedReasons) != 0 || drafts[0].AnswerKey != nil || drafts[0].Solution != nil || drafts[0].Rubric != nil || !reflect.DeepEqual(drafts[0].Options, []string{"first"}) {
					t.Fatalf("structure inferred answer resolution or changed first candidate: %#v %#v", preview, drafts)
				}
				job.Questions[0].Options = []string{"authored"}
				_, preserved := planPaperStructure(job, nil, 10)
				if !reflect.DeepEqual(preserved[0].Options, []string{"authored"}) {
					t.Fatal("candidate overwrote existing draft options")
				}
				logStructureBatch37Proof(t, map[string]any{"preview": preview, "drafts": drafts, "preserved": preserved})
			})
		}
	}
}

func structureBatch37Memory(t *testing.T, job PaperImportJob, qs []Question) *MemoryStore {
	t.Helper()
	s := NewMemoryStore()
	s.imports["import"] = job
	s.examState["exam"] = memoryExamState{Total: 10, Status: "configured"}
	s.readiness["exam"] = ReadinessResult{Ready: true, ConfigurationHash: "cached"}
	for _, q := range qs {
		s.questions[q.ID] = q
	}
	return s
}

func TestBatch37MemoryStructureGuardPrecedence(t *testing.T) {
	for _, test := range []paperMemoryStructureGuardCase{
		{name: "tenant_before_every_guard", tenant: "other", examStatus: "ready", generationDelta: 1, emptyHash: true, want: ErrNotFound},
		{name: "generation_before_complete", generationDelta: 1, want: ErrConflict},
		{name: "status_before_complete", status: "failed", want: ErrConflict},
		{name: "complete_before_frozen", examStatus: "ready", want: ErrInvalidInput},
		{name: "frozen_before_hash", complete: true, examStatus: "ready", staleHash: true, want: ErrExamFrozen},
		{name: "hash_before_blocked", complete: true, staleHash: true, blocked: true, want: ErrConflict},
		{name: "blocked_before_confirmed", complete: true, confirmed: true, blocked: true, want: ErrInvalidInput},
		{name: "fresh_confirmed_does_not_write", complete: true, confirmed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperMemoryStructureGuardPrecedence(t, test)
		})
	}
}

func TestBatch37MemoryStructureKeepsTeacherAndProtectedContent(t *testing.T) {
	job, qs := structureBatch37Fixture()
	qs[0].Options = []string{"teacher option"}
	qs[0].AnswerKey = &AnswerKey{StandardAnswer: "teacher answer"}
	qs[0].Solution = &QuestionSolution{RawText: "teacher solution"}
	bank := Question{ID: "bank", TenantID: "tenant", ExamID: "exam", QuestionNo: "2", QuestionType: "calculation", Score: 5, ScoreSource: "material", SourceType: "question_bank", SortOrder: 2, Status: "draft"}
	locked := Question{ID: "locked", TenantID: "tenant", ExamID: "exam", QuestionNo: "3", QuestionType: "calculation", Score: 5, ScoreSource: "material", SortOrder: 3, Status: "draft"}
	extra := Question{ID: "extra", TenantID: "tenant", ExamID: "exam", QuestionNo: "4", SortOrder: 4, Status: "draft"}
	foreign := Question{ID: "foreign", TenantID: "other", ExamID: "exam", QuestionNo: "9", Stem: "foreign"}
	qs = append(qs, bank, locked, extra, foreign)
	for _, no := range []string{"2", "3", "5"} {
		draft := job.Questions[0]
		draft.QuestionNo, draft.Stem = no, "material "+no
		job.Questions = append(job.Questions, draft)
	}
	job.Questions[0].AnswerKey = &AnswerKeyInput{StandardAnswer: "material answer"}
	job.Questions[0].Solution = &SolutionInput{RawText: "material solution"}
	s := structureBatch37Memory(t, job, qs)
	s.rubrics[locked.ID] = []Rubric{{Status: "locked"}}
	preview, err := s.PreviewPaperStructure(context.Background(), "tenant", "import")
	if err != nil {
		t.Fatal(err)
	}
	input := AdoptPaperStructureInput{ExpectedGeneration: job.Generation, PreviewHash: preview.PreviewHash, CompletePaperConfirmed: true}
	out, err := s.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
	if err != nil || !out.Confirmed || s.questions[extra.ID].Status != "deleted" {
		t.Fatalf("adoption=%#v err=%v extra=%#v", out, err, s.questions[extra.ID])
	}
	assertPaperAdoptionPreservesProtectedQuestions(t, s, job, bank, locked, foreign)
	if _, err = s.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input); !errors.Is(err, ErrConflict) {
		t.Fatal("receipt change did not invalidate old preview hash")
	}
	confirmed := s.imports["import"]
	confirmed.UpdatedAt = time.Time{}
	logStructureBatch37Proof(t, map[string]any{"preview": out, "questions": s.questions, "receipt": confirmed})
}
