package paper

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type paperMemoryStructureGuardCase struct {
	name, tenant, status, examStatus string
	complete, emptyHash, staleHash   bool
	confirmed, blocked               bool
	generationDelta                  int64
	want                             error
}

func assertPaperMemoryStructureGuardPrecedence(t *testing.T, test paperMemoryStructureGuardCase) {
	t.Helper()
	job, qs := structureBatch37Fixture()
	if test.status != "" {
		job.Status = test.status
	}
	if test.confirmed {
		job.StructureConfirmedGeneration = job.Generation
	}
	if test.blocked {
		job.Sources[0].DetectedRole = "answer"
	}
	s := structureBatch37Memory(t, job, qs)
	if test.examStatus != "" {
		state := s.examState["exam"]
		state.Status = test.examStatus
		s.examState["exam"] = state
	}
	preview, err := s.PreviewPaperStructure(context.Background(), "tenant", "import")
	if err != nil {
		t.Fatal(err)
	}
	input := AdoptPaperStructureInput{ExpectedGeneration: job.Generation + test.generationDelta, PreviewHash: preview.PreviewHash, CompletePaperConfirmed: test.complete}
	if test.emptyHash {
		input.PreviewHash = ""
	}
	if test.staleHash {
		input.PreviewHash = "stale"
	}
	tenant := "tenant"
	if test.tenant != "" {
		tenant = test.tenant
	}
	before := structureBatch37JSON(t, map[string]any{"questions": s.questions, "imports": s.imports, "readiness": s.readiness, "exam": s.examState, "next": s.next})
	out, err := s.AdoptPaperStructure(context.Background(), tenant, "import", "user", input)
	if !errors.Is(err, test.want) {
		t.Fatalf("guard error=%v, want %v", err, test.want)
	}
	after := structureBatch37JSON(t, map[string]any{"questions": s.questions, "imports": s.imports, "readiness": s.readiness, "exam": s.examState, "next": s.next})
	if string(before) != string(after) {
		t.Fatal("guard or fresh confirmation wrote memory state")
	}
	logStructureBatch37Proof(t, map[string]any{"preview": out, "error": fmt.Sprint(err), "state": json.RawMessage(after)})
}

type paperStructureReceiptCase struct {
	name   string
	column int
	value  driver.Value
}

func assertPaperStructureReceiptFailureOrder(t *testing.T, test paperStructureReceiptCase) {
	t.Helper()
	job, qs := structureBatch37Fixture()
	steps := structureBatch37ReadSteps(t, job, qs)[:2]
	steps[1].rows[0][test.column] = test.value
	store, script := openStructureBatch37Store(t, steps)
	_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("receipt error=%v", err)
	}
	assertStructureBatch37Script(t, script, "rollback", err.Error())
}

type paperStructureAdoptionGuardCase struct {
	name                          string
	invalid, oldGeneration        bool
	staleHash, blocked, confirmed bool
	want                          error
}

func assertPaperStructureAdoptionWithoutWrite(t *testing.T, test paperStructureAdoptionGuardCase) {
	t.Helper()
	job, qs := structureBatch37Fixture()
	if test.blocked {
		job.Sources[0].DetectedRole = "answer"
	}
	if test.confirmed {
		job.StructureConfirmedGeneration = job.Generation
	}
	input := structureBatch37Input(job, qs)
	steps := structureBatch37ReadSteps(t, job, qs)
	end := "rollback"
	if test.invalid {
		input.ExpectedGeneration = 0
		steps, end = nil, ""
	}
	if test.oldGeneration {
		input.ExpectedGeneration++
	}
	if test.staleHash {
		input.PreviewHash = "stale"
	}
	store, script := openStructureBatch37Store(t, steps)
	out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
	if !errors.Is(err, test.want) {
		t.Fatalf("adoption guard=%v, want %v", err, test.want)
	}
	if test.invalid && len(script.events) != 0 {
		t.Fatal("invalid input opened a transaction")
	}
	assertStructureBatch37Script(t, script, end, map[string]any{"preview": out, "error": fmt.Sprint(err)})
}

// 采用结构只能填充允许变化的正式题目；教师内容、题库题、锁定题和其他租户资料逐项核对。
func assertPaperAdoptionPreservesProtectedQuestions(t *testing.T, s *MemoryStore, job PaperImportJob, bank, locked, foreign Question) {
	t.Helper()
	teacher := s.questions["question"]
	if teacher.Stem != "teacher stem" || !reflect.DeepEqual(teacher.Options, []string{"teacher option"}) || teacher.AnswerKey.StandardAnswer != "teacher answer" || teacher.Solution.RawText != "teacher solution" {
		t.Fatalf("teacher content replaced: %#v", teacher)
	}
	if !reflect.DeepEqual(s.questions[bank.ID], bank) || !reflect.DeepEqual(s.questions[locked.ID], locked) || !reflect.DeepEqual(s.questions[foreign.ID], foreign) {
		t.Fatal("protected or foreign question changed")
	}
	if _, exists := s.readiness["exam"]; exists || s.imports["import"].StructureConfirmedGeneration != job.Generation {
		t.Fatal("successful adoption did not persist receipt and invalidate readiness")
	}
	var added Question
	for _, q := range s.questions {
		if q.TenantID == "tenant" && q.QuestionNo == "5" {
			added = q
		}
	}
	if added.Status != "draft" || added.SortOrder != 4 || added.Stem != "material 5" {
		t.Fatalf("new draft question changed: %#v", added)
	}
}
