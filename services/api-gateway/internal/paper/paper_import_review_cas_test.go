package paper

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

func reviewCASFixture(t *testing.T) (*MemoryStore, PaperImportJob) {
	t.Helper()
	store, job := candidateImport(t)
	job.Status = "review_required"
	job.UpdatedAt = time.Date(2030, 1, 1, 0, 0, 0, 123456000, time.UTC)
	job.Questions = []PaperImportDraftQuestion{
		{CandidateID: "q1", QuestionNo: "1", QuestionType: "short_answer", Score: 5, Stem: "原第一题"},
		{CandidateID: "q2", QuestionNo: "2", QuestionType: "short_answer", Score: 5, Stem: "原第二题"},
	}
	store.imports[job.ID] = job
	return store, job
}

func TestReviewCASRejectsAnotherClientInSameGeneration(t *testing.T) {
	store, initial := reviewCASFixture(t)
	a := append([]PaperImportDraftQuestion(nil), initial.Questions...)
	b := append([]PaperImportDraftQuestion(nil), initial.Questions...)
	a[0].Stem, a[0].HumanConfirmedFields = "A确认第一题", []string{"stem", "score"}
	b[1].Stem = "B修改第二题"
	ctx := context.Background()
	saved, err := store.SavePaperImportReview(ctx, "tenant", initial.ID, "A", ReviewPaperImportInput{
		ExpectedGeneration: initial.Generation, ExpectedUpdatedAt: initial.UpdatedAt.Format(time.RFC3339Nano), Questions: a,
	})
	if err != nil || !saved.UpdatedAt.After(initial.UpdatedAt) || saved.Generation != initial.Generation {
		t.Fatalf("first save must advance content time only: %#v, %v", saved, err)
	}
	// 两个客户端持有同代次整卷；第二次写入不能把第一题的内容和人工确认退回旧值。
	_, err = store.SavePaperImportReview(ctx, "tenant", initial.ID, "B", ReviewPaperImportInput{
		ExpectedGeneration: initial.Generation, ExpectedUpdatedAt: initial.UpdatedAt.Format(time.RFC3339Nano), Questions: b,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale client = %v", err)
	}
	latest, _ := store.GetPaperImport(ctx, "tenant", initial.ID)
	if latest.Questions[0].Stem != a[0].Stem || len(latest.Questions[0].HumanConfirmedFields) != 2 || latest.Questions[1].Stem != "原第二题" {
		t.Fatalf("rejected request changed saved facts: %#v", latest.Questions)
	}
	// 重新比较后可保存整卷草稿；使用新时间且保留第一位操作人的内容。
	fresh := append([]PaperImportDraftQuestion(nil), latest.Questions...)
	fresh[1].Stem = b[1].Stem
	resaved, err := store.SavePaperImportReview(ctx, "tenant", initial.ID, "B", ReviewPaperImportInput{
		ExpectedGeneration: latest.Generation, ExpectedUpdatedAt: latest.UpdatedAt.Format(time.RFC3339Nano), Questions: fresh,
	})
	if err != nil || resaved.Questions[0].Stem != a[0].Stem || resaved.Questions[1].Stem != b[1].Stem {
		t.Fatalf("fresh draft save: %#v, %v", resaved, err)
	}
}

func TestReviewCASRequiresContentTime(t *testing.T) {
	for _, value := range []string{"", "not-a-time", "0001-01-01T00:00:00Z"} {
		t.Run(value, func(t *testing.T) {
			store, job := reviewCASFixture(t)
			_, err := store.SavePaperImportReview(context.Background(), "tenant", job.ID, "B", ReviewPaperImportInput{
				ExpectedGeneration: job.Generation, ExpectedUpdatedAt: value, Questions: job.Questions,
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("missing or invalid time bypassed CAS: %v", err)
			}
		})
	}
}

func TestReviewCASPostgresRejectsStaleTimeBeforeAnyWrite(t *testing.T) {
	latest := time.Date(2030, 1, 1, 0, 0, 0, 123456000, time.UTC)
	// 两个时间在同一毫秒内；不能用浏览器毫秒精度比较后放行旧版本。
	store, script := openPaperBatch10Store(t, []paperBatch10Step{
		{kind: "query", fragment: "SELECT q.question_no,q.question_type", columns: 3},
		{kind: "query", fragment: "SELECT current_generation,status,updated_at", columns: 3, rows: [][]driver.Value{{int64(7), "review_required", latest}}},
	})
	_, err := store.SavePaperImportReview(context.Background(), "tenant", "import", "B", ReviewPaperImportInput{
		ExpectedGeneration: 7, ExpectedUpdatedAt: latest.Add(-time.Microsecond).Format(time.RFC3339Nano),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale timestamp = %v", err)
	}
	assertPaperBatch10Proof(t, script, "rollback", err.Error())
}
