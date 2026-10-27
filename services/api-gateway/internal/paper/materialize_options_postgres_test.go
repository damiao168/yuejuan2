package paper

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresMaterializedBankOptionsSurviveReadAndFreeze(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL requires isolated migrated PostgreSQL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	schema := "materialize_options_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// 只复制合成考试所需表结构；调用生产物化/读取与冻结构造，不用脚本替身代替 options 写入。
	for _, table := range []string{"exam", "exam_session", "exam_class", "school_class", "grade", "question", "question_answer_key", "question_solution", "question_rubric", "rubric_version", "question_assessment_config", "subject_profile", "question_archetype", "question_reference_manifest", "question_reference_manifest_item"} {
		exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING ALL)")
	}
	tenant, exam, actor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic bank materialization','math','quiz',2,'draft','ai_assisted','manual_after_confirmation',$4)`, exam, tenant, uuid.NewString(), actor)
	for _, tc := range []struct {
		name    string
		options []string
	}{
		{"choice", []string{"$\\frac{1}{2}$", "<完整选项乙>", "第三项"}},
		{"text", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			kind := "single_choice"
			archetype := "selected_response"
			if tc.options == nil {
				kind = "fill_blank"
				archetype = "exact_text"
			}
			f := BankQuestionFacts{ItemID: uuid.NewString(), VersionID: uuid.NewString(), BundleHash: strings.Repeat("a", 64), QuestionNo: tc.name, QuestionType: kind, Archetype: archetype, Stem: "Frozen bank question", Options: tc.options, Score: 2, SortOrder: 1, KnowledgePoints: []string{}, Content: map[string]any{"options": tc.options}, Answer: &AnswerKeyInput{StandardAnswer: "B"}}
			q, err := MaterializeBankQuestionTx(ctx, tx, tenant, exam, actor, f)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			want := append([]string{}, tc.options...)
			if !reflect.DeepEqual(q.Options, want) {
				t.Fatalf("response options=%#v want=%#v", q.Options, want)
			}
			var raw []byte
			if err = db.QueryRowContext(ctx, `SELECT options FROM question WHERE tenant_id=$1 AND id=$2`, tenant, q.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var stored []string
			if err = json.Unmarshal(raw, &stored); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(stored, want) {
				t.Fatalf("stored options=%#v want=%#v", stored, want)
			}
			// 真实列表回读再走生产冻结构造，防止仅在物化响应上补字段却未保存正式列。
			read, err := listQuestions(ctx, db, tenant, exam)
			if err != nil {
				t.Fatal(err)
			}
			var current Question
			for _, item := range read {
				if item.ID == q.ID {
					current = item
					break
				}
			}
			if !reflect.DeepEqual(current.Options, want) {
				t.Fatalf("read options=%#v want=%#v", current.Options, want)
			}
			frozen := newReadinessImportSnapshot("synthetic-configuration", []Question{current})
			if len(frozen.Questions) != 1 || !reflect.DeepEqual(append([]string{}, frozen.Questions[0].Options...), want) {
				t.Fatalf("freeze options=%#v want=%#v", frozen, want)
			}
		})
	}
}
