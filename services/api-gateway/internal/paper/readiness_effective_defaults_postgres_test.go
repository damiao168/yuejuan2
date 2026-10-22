package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// 只在事务内建立独立 schema 和最小历史表，真实执行新增 SQL；回滚不触碰已有业务表。
// 这里验证默认解析与物化一致，ready 触发器的并发回滚仍由确认流程测试覆盖。
func TestReadinessEffectiveDefaultsPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for effective assessment defaults test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	schema := "assessment_preview_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("execute fixture SQL: %v", err)
		}
	}
	exec(fmt.Sprintf(`CREATE SCHEMA %s; SET LOCAL search_path TO %s,pg_catalog`, schema, schema))
	exec(`
CREATE TABLE exam (id uuid,tenant_id uuid,school_id uuid,exam_session_id uuid,subject text,grading_mode text,deleted_at timestamptz);
CREATE TABLE exam_session (id uuid,tenant_id uuid,school_id uuid,grade_id uuid,deleted_at timestamptz);
CREATE TABLE exam_class (tenant_id uuid,exam_id uuid,class_id uuid,deleted_at timestamptz);
CREATE TABLE school_class (id uuid,tenant_id uuid,school_id uuid,grade_id uuid,deleted_at timestamptz);
CREATE TABLE grade (id uuid,tenant_id uuid,school_id uuid,education_stage text,level_no int,deleted_at timestamptz);
CREATE TABLE question (id uuid,tenant_id uuid,exam_id uuid,question_type text,question_no text,sort_order int,answer_area jsonb,status text,deleted_at timestamptz);
CREATE TABLE subject_profile (id uuid,tenant_id uuid,code text,education_stage text,subject_code text,version int,status text,parser_policy_json jsonb,evidence_policy_json jsonb,scoring_default_json jsonb);
CREATE TABLE question_archetype (code text,response_schema_json jsonb,evidence_types_json jsonb,default_scoring_mode text);
CREATE TABLE question_assessment_config (id uuid DEFAULT gen_random_uuid(),tenant_id uuid,exam_id uuid,question_id uuid,subject_profile_id uuid,archetype_code text,allowed_evidence_types jsonb,risk_tier text,scoring_policy_json jsonb,UNIQUE(tenant_id,exam_id,question_id));
CREATE TABLE exam_question_snapshot (tenant_id uuid,exam_id uuid,question_id uuid,snapshot_version int,scoring_policy_snapshot_json jsonb,risk_tier text,allowed_evidence_types jsonb,profile_snapshot_json jsonb,archetype_snapshot_json jsonb);
INSERT INTO grade VALUES
 ('00000000-0000-0000-0000-000000000005','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','senior',2,NULL),
 ('00000000-0000-0000-0000-000000000006','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','junior',8,NULL);
INSERT INTO school_class VALUES ('00000000-0000-0000-0000-000000000007','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000006',NULL);
INSERT INTO exam_session VALUES ('00000000-0000-0000-0000-000000000004','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000005',NULL);
INSERT INTO exam VALUES ('00000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000004','数学','ai_assisted',NULL);
INSERT INTO question VALUES
 ('00000000-0000-0000-0000-000000000008','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000003','single_choice','1',1,'{}','active',NULL),
 ('00000000-0000-0000-0000-000000000009','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000003','short_answer','2',2,'{}','active',NULL);
INSERT INTO subject_profile VALUES
 ('00000000-0000-0000-0000-000000000010','00000000-0000-0000-0000-000000000001','senior.mathematics.standard','senior','mathematics',1,'active','{}','{"allowed_types":["selected_option","text_span"]}','{"mode":"AI_ASSIST"}'),
 ('00000000-0000-0000-0000-000000000011','00000000-0000-0000-0000-000000000001','junior.mathematics.standard','junior','mathematics',1,'active','{}','{"allowed_types":["selected_option","text_span"]}','{"mode":"AI_ASSIST"}');
INSERT INTO question_archetype VALUES
 ('selected_response','{}','["selected_option"]','RULE_AUTO'),
 ('short_constructed','{}','["text_span"]','AI_ASSIST');
`)
	migration, err := os.ReadFile("../../migrations/000200_assessment_effective_defaults.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	const tenantID = "00000000-0000-0000-0000-000000000001"
	const examID = "00000000-0000-0000-0000-000000000003"
	read := func(status string) []readinessAssessmentFact {
		t.Helper()
		facts, err := loadReadinessAssessmentFacts(ctx, tx, tenantID, examID, status)
		if err != nil || len(facts) != 2 {
			t.Fatalf("read effective assessment: %#v %v", facts, err)
		}
		return facts
	}
	assertStageBlocked := func(label string) {
		t.Helper()
		for _, fact := range read("configured") {
			if fact.BlockingReason == "" || fact.BlockingSection != "exam" || fact.Profile["id"] != nil {
				t.Fatalf("%s silently guessed a stage: %#v", label, fact)
			}
		}
	}

	// 高二在校内以 2 表示；没有选班时仍使用考试场次的明确高中年级。
	initial := read("configured")
	if initial[0].Source != "default" || initial[0].Profile["education_stage"] != "senior" || initial[0].Policy.Mode != assessment.ScoringRuleAuto || initial[1].Policy.Mode != assessment.ScoringAIAssist {
		t.Fatalf("session grade and question archetypes were not respected: %#v", initial)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM question_assessment_config`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("preview wrote defaults: count=%d err=%v", count, err)
	}
	exec(`UPDATE exam SET exam_session_id=NULL`)
	assertStageBlocked("missing grade")
	exec(`UPDATE exam SET exam_session_id='00000000-0000-0000-0000-000000000004'`)
	exec(`INSERT INTO exam_class VALUES ('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000007',NULL)`)
	assertStageBlocked("mixed junior/senior grades")
	exec(`DELETE FROM exam_class`)
	exec(`UPDATE grade SET education_stage='junior',level_no=11 WHERE id='00000000-0000-0000-0000-000000000005'`)
	assertStageBlocked("conflicting grade fields")
	exec(`UPDATE grade SET education_stage='senior',level_no=2 WHERE id='00000000-0000-0000-0000-000000000005'`)
	exec(`UPDATE exam SET grading_mode='double_mark'`)
	for _, fact := range read("configured") {
		if fact.Policy.Mode != assessment.ScoringDualHuman {
			t.Fatalf("default ignored exam double marking: %#v", fact)
		}
	}
	exec(`UPDATE exam SET grading_mode='ai_assisted'`)
	for _, fact := range initial {
		exec(`SELECT assessment_apply_default_question_config($1::uuid,$2::uuid,$3::uuid)`, tenantID, examID, fact.QuestionID)
	}
	materialized := read("configured")
	if readinessAssessmentFactsHash(initial) != readinessAssessmentFactsHash(materialized) || materialized[0].Source != "saved" {
		t.Fatalf("materialized defaults diverged from the preview: %#v %#v", initial, materialized)
	}

	// 保存配置不依赖默认选班学段，且重复调用默认物化不能覆盖人工和双评策略。
	exec(`UPDATE question_assessment_config SET scoring_policy_json=jsonb_set(scoring_policy_json,'{mode}','"MANUAL_ONLY"') WHERE question_id='00000000-0000-0000-0000-000000000008'`)
	exec(`UPDATE question_assessment_config SET scoring_policy_json=jsonb_set(scoring_policy_json,'{mode}','"DUAL_HUMAN"') WHERE question_id='00000000-0000-0000-0000-000000000009'`)
	exec(`UPDATE exam SET exam_session_id=NULL`)
	for _, fact := range read("configured") {
		exec(`SELECT assessment_apply_default_question_config($1::uuid,$2::uuid,$3::uuid)`, tenantID, examID, fact.QuestionID)
		if fact.BlockingReason != "" || fact.Source != "saved" {
			t.Fatalf("saved config unexpectedly needs a default stage: %#v", fact)
		}
	}
	saved := read("configured")
	if saved[0].Policy.Mode != assessment.ScoringManualOnly || saved[1].Policy.Mode != assessment.ScoringDualHuman {
		t.Fatalf("saved policies were overwritten: %#v", saved)
	}
	for _, fact := range initial {
		policy, _ := json.Marshal(fact.Policy)
		profile, _ := json.Marshal(fact.Profile)
		archetype, _ := json.Marshal(fact.Archetype)
		evidence, _ := json.Marshal(fact.AllowedEvidence)
		exec(`INSERT INTO exam_question_snapshot VALUES ($1::uuid,$2::uuid,$3::uuid,1,$4::jsonb,$5,$6::jsonb,$7::jsonb,$8::jsonb)`, tenantID, examID, fact.QuestionID, policy, fact.RiskTier, evidence, profile, archetype)
	}
	exec(`UPDATE subject_profile SET parser_policy_json='{"changed":true}',version=2`)
	frozen := read("ready")
	if frozen[0].Source != "snapshot" || readinessAssessmentFactsHash(initial) != readinessAssessmentFactsHash(frozen) {
		t.Fatalf("frozen preview used current editable templates: %#v", frozen)
	}
}
