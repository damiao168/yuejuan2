package subjective

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 隔离 schema 复制正式成绩结构，并安装真实新迁移的函数与索引。
// 有意让其他题目历史样本携带同一个答题段ID，验证Go入口和触发器都按题目收窄，且查询规划实际使用索引。
func TestPostgresTrialSampleLookupScopesHistoryByQuestion(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_REVIEW_PG_DSN")
	if dsn == "" {
		t.Skip("EDUGRADE_REVIEW_PG_DSN is required for isolated trial lookup test")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "trial_lookup_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	exec(`CREATE TABLE scoring_ai_trial(tenant_id uuid, question_id uuid, payload jsonb)`)
	exec(`CREATE TABLE ai_grade(LIKE public.ai_grade INCLUDING ALL)`)
	exec(`CREATE TABLE subjective_grading_run(LIKE public.subjective_grading_run INCLUDING ALL)`)
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000224_scoring_trial_question_lookup.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(string(raw))
	exec(`CREATE TRIGGER trial_lookup BEFORE INSERT ON ai_grade FOR EACH ROW EXECUTE FUNCTION mark_scoring_trial_sample_grade()`)
	tenantID, actorID, segmentID, questionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO scoring_ai_trial SELECT $1::uuid,gen_random_uuid(),jsonb_build_object('samples',jsonb_build_array(jsonb_build_object('segment_id',$2::text))) FROM generate_series(1,3000)`, tenantID, segmentID)
	grade := successfulGrade(serviceTestContext(), ModelPolicy{ModelVersion: "model", PromptVersion: "prompt"}, "", validServiceOutput())
	grade.AnswerSegmentID, grade.QuestionID, grade.AdapterRequestID, grade.Mock = segmentID, questionID, uuid.NewString(), true
	store := NewPostgresStore(db)
	ordinary, err := store.CreateGrade(ctx, tenantID, actorID, grade)
	if err != nil || slices.Contains(ordinary.RiskFlags, "trial_sample") {
		t.Fatalf("unrelated question contaminated ordinary grade: %+v %v", ordinary, err)
	}
	exec(`INSERT INTO scoring_ai_trial VALUES($1,$2,jsonb_build_object('samples',jsonb_build_array(jsonb_build_object('segment_id',$3::text))))`, tenantID, questionID, segmentID)
	grade.AdapterRequestID = uuid.NewString()
	sample, err := store.CreateGrade(ctx, tenantID, actorID, grade)
	if err != nil || !slices.Contains(sample.RiskFlags, "trial_sample") || !sample.NeedsHumanReview {
		t.Fatalf("actual trial sample lost guard: %+v %v", sample, err)
	}
	// 绕过Go入口的写入仍由数据库标记；不能只靠客户端或供应商带回risk_flags。
	var marked, requiresReview bool
	err = db.QueryRowContext(ctx, `INSERT INTO ai_grade(tenant_id,answer_segment_id,question_id,question_no,question_type,answer_version,grader_type,rule_version,suggested_score,max_score,confidence,matched_points,missing_points,evidence,risk_flags,needs_human_review,auto_pass,mock,raw_output,created_by,status)
SELECT tenant_id,answer_segment_id,question_id,question_no,question_type,answer_version,grader_type,rule_version,suggested_score,max_score,confidence,matched_points,missing_points,evidence,'[]',false,false,mock,raw_output,created_by,status FROM ai_grade WHERE id=$1 RETURNING risk_flags @> '["trial_sample"]',needs_human_review`, ordinary.ID).Scan(&marked, &requiresReview)
	if err != nil || !marked || !requiresReview {
		t.Fatalf("direct insert bypassed trial guard: marked=%v review=%v err=%v", marked, requiresReview, err)
	}
	exec(`ANALYZE scoring_ai_trial`)
	var plan string
	if err := db.QueryRowContext(ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT EXISTS(SELECT 1 FROM scoring_ai_trial trial CROSS JOIN LATERAL jsonb_array_elements(trial.payload->'samples') sample WHERE trial.tenant_id=$1::uuid AND trial.question_id=$3::uuid AND sample->>'segment_id'=$2)`, tenantID, segmentID, questionID).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_scoring_ai_trial_question") {
		t.Fatalf("trial lookup did not use question index: %s", plan)
	}
	t.Logf("3000 unrelated trials and one matching trial; actual scoped query plan: %s", plan)
}
