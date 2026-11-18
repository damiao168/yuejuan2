package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestDraftAIAdoptionPostgresRoundTrip(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	schema := "review_draft_adoption_" + uuid.NewString()[:8]
	if _, err = admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	// 只复制正式表结构到随机隔离区；不继承外键，也不允许草稿读写回退到 public 数据。
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	for _, table := range []string{"review_task", "review_draft"} {
		if _, err = db.ExecContext(t.Context(), `CREATE TABLE `+table+` (LIKE public.`+table+` INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)`); err != nil {
			t.Fatal(err)
		}
	}
	tenant, task, reviewer := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = db.ExecContext(t.Context(), `INSERT INTO review_task
	 (id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,created_by)
	 VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'synthetic',$5::uuid,$6::uuid,'synthetic','manual_sample','in_progress',$7::uuid,$7::uuid)`,
		task, tenant, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), reviewer)
	if err != nil {
		t.Fatal(err)
	}
	store, revision, score := NewPostgresStore(db), 0, 9.0
	for _, choice := range []any{
		map[string]any{"taskId": task, "aiGradeId": "synthetic-model", "suggestedScore": 9.0, "accepted": true},
		map[string]any{"taskId": task, "aiGradeId": "synthetic-model", "suggestedScore": 9.0, "accepted": false},
		nil,
		"absent",
	} {
		viewer := map[string]any{"mode": "segment"}
		if choice != "absent" {
			viewer["grading_ai_adoption"] = choice
		}
		saved, err := store.SaveDraft(t.Context(), tenant, task, reviewer, SaveDraftInput{Score: &score, RubricSelections: []RubricSelection{}, ViewerState: viewer, ExpectedRevision: revision})
		if err != nil {
			t.Fatal(err)
		}
		revision = saved.Revision
		restored, err := store.GetDraft(t.Context(), tenant, task, reviewer)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(viewer)
		got, _ := json.Marshal(restored.ViewerState)
		if string(got) != string(want) || restored.Score == nil || *restored.Score != score {
			t.Fatalf("draft score or adoption fact lost: got=%s want=%s score=%v", got, want, restored.Score)
		}
	}
	// 慢会话不能把已经恢复成未知的事实覆盖回旧采纳记录。
	_, err = store.SaveDraft(t.Context(), tenant, task, reviewer, SaveDraftInput{Score: &score, ExpectedRevision: revision - 1,
		ViewerState: map[string]any{"grading_ai_adoption": map[string]any{"accepted": true}}})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale adoption overwrite error=%v", err)
	}
	restored, err := store.GetDraft(t.Context(), tenant, task, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := restored.ViewerState["grading_ai_adoption"]; exists {
		t.Fatal("stale session overwrote unknown adoption fact")
	}
}
