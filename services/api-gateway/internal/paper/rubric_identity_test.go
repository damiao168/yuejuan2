package paper

import (
	"context"
	"errors"
	"testing"
)

func TestRubricPointIdentityRejectsAmbiguousEvidenceReferences(t *testing.T) {
	for _, ids := range [][]string{{"p1", "p1"}, {"", "p2"}, {" p1", "p2"}, {"p1", "p2 "}} {
		points := []RubricPoint{{ID: ids[0], Score: 1}, {ID: ids[1], Score: 1}}
		if ValidRubricPointIdentities(points) {
			t.Fatalf("invalid identities accepted: %q", ids)
		}
		store := NewMemoryStore()
		store.questions["question"] = Question{ID: "question", TenantID: "tenant", ExamID: "exam", Score: 2}
		input := RubricInput{MaxScore: 2, Points: points}
		if _, err := store.CreateRubric(context.Background(), "tenant", "question", "actor", input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("memory rubric invalid IDs=%q error=%v", ids, err)
		}
		// 持久入口应在任何数据库写入前拒绝歧义 ID；nil DB 保护这条先后边界。
		if _, err := NewPostgresStore(nil).CreateRubric(context.Background(), "tenant", "question", "actor", input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("postgres rubric invalid IDs=%q error=%v", ids, err)
		}
		if len(store.rubrics["question"]) != 0 {
			t.Fatal("rejected rubric wrote a version")
		}
	}
	if !ValidRubricPointIdentities([]RubricPoint{{ID: "p-uuid-1"}, {ID: "p-uuid-2"}}) {
		t.Fatal("distinct stable IDs rejected")
	}
}

func TestPaperImportConfirmationRejectsDuplicateRubricPointIDs(t *testing.T) {
	store, job := newReconcileFixture(t, []CreateQuestionInput{{QuestionNo: "1", QuestionType: "short_answer", Score: 10}})
	draft := importedDraft("1", "short_answer", 10)
	draft.Rubric.Points = []RubricPoint{{ID: "p1", Description: "first", Score: 5}, {ID: "p1", Description: "second", Score: 5}}
	if _, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{draft}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPaperImport(context.Background(), "tenant", job.ID, "actor"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate ID import error=%v", err)
	}
	questions, err := store.ListQuestions(context.Background(), "tenant", "exam")
	if err != nil || len(questions) != 1 || questions[0].Rubric != nil || questions[0].Stem != "" {
		t.Fatalf("rejected import mutated question=%v err=%v", questions, err)
	}
	draft.Rubric.Points[1].ID = "p2"
	if _, err := store.CompletePaperImport(context.Background(), "tenant", job.ID, []PaperImportDraftQuestion{draft}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPaperImport(context.Background(), "tenant", job.ID, "actor"); err != nil {
		t.Fatalf("corrected unique IDs could not apply: %v", err)
	}
}
