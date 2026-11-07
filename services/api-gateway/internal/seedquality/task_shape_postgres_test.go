package seedquality

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 隔离schema复制正式约束；客户端旧钩子误传UUID以及旧任务已保存的特殊前缀，都不能泄露盲投身份。
func TestPostgresSeedTaskUsesOrdinaryQuestionAndSourceShape(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_REVIEW_PG_DSN")
	if dsn == "" {
		t.Skip("EDUGRADE_REVIEW_PG_DSN is required for seed task shape integration")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "seed_shape_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"question", "seed_sampling_policy", "seed_sampling_cursor", "seed_task"} {
		exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING ALL)")
	}
	tenantID, examID, questionID, graderID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status)VALUES($1,$2,$3,'12(2)','short_answer',5,1,'active')`, questionID, tenantID, examID)
	store := NewPostgresStore(db)
	policy, err := store.PutPolicy(ctx, tenantID, examID, questionID, graderID, PutPolicyInput{Rate: 1, MinInterval: 1, MaxInterval: 1, Status: PolicyActive}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	task, issued, err := store.AdvanceAndMaybeCreate(ctx, tenantID, IssueDecision{Policy: policy, QuestionNo: questionID, GraderID: graderID, Gold: GoldSample{GoldPaperID: uuid.NewString(), GoldVersion: 1, SnapshotID: uuid.NewString(), MaxScore: 5, ReferenceScore: 4, AnswerImageURL: "/synthetic.png", ExpectedCriteria: map[string]any{}, ArchetypeCode: "short_constructed"}, Probability: 0, NextInterval: 1, Now: time.Now()})
	if err != nil || !issued || task.QuestionNo != "12(2)" || task.Source != "manual_sample" || task.AnonymousCode != opaqueID(task.ID, "answer-artifact") || task.AnonymousCode == task.ID {
		t.Fatalf("distinguishable task=%+v issued=%v err=%v", task, issued, err)
	}
	exec(`UPDATE seed_task SET question_no=$2,anonymous_code='ANON-old-prefix' WHERE id=$1`, task.ID, questionID)
	read, err := store.GetTask(ctx, tenantID, task.ID)
	if err != nil || read.QuestionNo != "12(2)" || read.AnonymousCode != opaqueID(task.ID, "answer-artifact") {
		t.Fatalf("legacy task shape not normalized: %+v %v", read, err)
	}
}
