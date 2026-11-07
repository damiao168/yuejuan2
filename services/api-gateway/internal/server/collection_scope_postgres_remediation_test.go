package server

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 只在显式测试实例的随机 schema 写合成记录，克隆正式表类型但不复制业务数据或触发器。
// 本回归验证实际列表 SQL 的授权过滤，不代替领域写入与完整迁移验收。
func TestPostgresCollectionScopeRemediation(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	schema := "collection_scope_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	for _, table := range []string{"exam", "exam_class", "submission", "ai_human_disagreement", "regrade_job", "grading_gold_paper", "grading_gold_paper_version", "exam_question_snapshot", "answer_segment", "question"} {
		if _, err := db.ExecContext(ctx, `CREATE TABLE `+table+` AS TABLE public.`+table+` WITH NO DATA`); err != nil {
			t.Fatal(err)
		}
	}
	// 显式合成实例可能尚未升级到 216；夹具补齐正式冻结列并逐条提供固定身份，隔离断言仍走完整列表 SQL。
	if _, err := db.ExecContext(ctx, `ALTER TABLE grading_gold_paper_version
 ADD COLUMN IF NOT EXISTS frozen_answer_segment_id UUID,
 ADD COLUMN IF NOT EXISTS frozen_crop_file_asset_id UUID,
 ADD COLUMN IF NOT EXISTS frozen_crop_sha256 TEXT,
 ADD COLUMN IF NOT EXISTS frozen_crop_owner_type TEXT,
 ADD COLUMN IF NOT EXISTS frozen_crop_owner_id UUID,
 ADD COLUMN IF NOT EXISTS image_invalidated_at TIMESTAMPTZ`); err != nil {
		t.Fatal(err)
	}
	tenant, actor, schoolA, schoolB, examA, examB, classB, student := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	insert := func(table string, fields map[string]any) {
		t.Helper()
		if fields["id"] == nil {
			fields["id"] = uuid.NewString()
		}
		fields["tenant_id"] = tenant
		fields["created_at"], fields["updated_at"] = time.Now().UTC(), time.Now().UTC()
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO `+table+` SELECT * FROM jsonb_populate_record(NULL::`+table+`,$1::jsonb)`, raw); err != nil {
			t.Fatalf("insert %s: %v", table, err)
		}
	}
	insert("exam", map[string]any{"id": examA, "school_id": schoolA})
	insert("exam", map[string]any{"id": examB, "school_id": schoolB})
	insert("exam_class", map[string]any{"exam_id": examB, "class_id": classB})
	submissions := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	memoryIssues, boundaries := aidisagreement.NewMemoryStore(), auth.NewMemoryStore()
	for i, submissionID := range submissions {
		examID := examB
		if i == 0 {
			examID = examA
		}
		questionID, snapshotID, segmentID, issueID, goldID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
		studentID := uuid.NewString()
		if i == 1 {
			studentID = student
		}
		_, _, err := memoryIssues.Upsert(ctx, tenant, aidisagreement.Disagreement{ID: issueID, ExamID: examID, SubmissionID: submissionID, AICandidateID: uuid.NewString(), HumanGradeID: uuid.NewString(), Severity: aidisagreement.SeveritySevere})
		if err != nil {
			t.Fatal(err)
		}
		schoolID, classIDs := schoolB, []string{classB}
		if i == 0 {
			schoolID, classIDs = schoolA, nil
		}
		boundaries.AddResourceBoundary("ai_human_disagreement", issueID, auth.ResourceBoundary{TenantID: tenant, SchoolID: schoolID, ExamID: examID, SubmissionID: submissionID, StudentID: studentID, ClassIDs: classIDs})
		insert("submission", map[string]any{"id": submissionID, "exam_id": examID, "student_id": studentID})
		insert("ai_human_disagreement", map[string]any{"id": issueID, "exam_id": examID, "question_id": questionID, "submission_id": submissionID, "answer_segment_id": segmentID, "ai_candidate_id": uuid.NewString(), "human_grade_id": uuid.NewString(), "ai_candidate_score": 5, "human_score": 1, "max_score": 5, "delta": 4, "absolute_delta": 4, "difference_type": "score", "severity": "severe", "risk_tier": "R3", "trigger_rules_json": []any{}, "evidence_summary_json": map[string]any{}, "status": "classified", "taxonomy": "ocr_error", "notes": "", "revision": 1})
		insert("regrade_job", map[string]any{"exam_id": examID, "question_id": questionID, "source_release_id": uuid.NewString(), "reason_code": "answer_key_error", "reason_text": "synthetic", "strategy": "human_recheck", "selector_json": map[string]any{}, "severity_delta": 2, "idempotency_key": uuid.NewString(), "status": "awaiting_approval", "affected_count": 1, "created_by": actor})
		insert("grading_gold_paper", map[string]any{"id": goldID, "exam_id": examID, "question_id": questionID, "submission_id": submissionID, "quality_pool": goldpaper.PoolCalibration, "active_version": 1, "status": "active", "nominated_by": actor, "retirement_reason": ""})
		insert("exam_question_snapshot", map[string]any{"id": snapshotID, "profile_snapshot_json": map[string]any{"subject_code": "math"}, "archetype_code": "exact_text", "risk_tier": "R2"})
		insert("answer_segment", map[string]any{"id": segmentID, "submission_id": submissionID, "question_id": questionID})
		insert("grading_gold_paper_version", map[string]any{"gold_paper_id": goldID, "version": 1, "exam_question_snapshot_id": snapshotID, "reference_score": 3, "max_score": 5, "rubric_snapshot_json": map[string]any{}, "explanation": "synthetic", "trait_scores_json": map[string]any{}, "error_tags_json": []any{}, "source_grade_ids_json": []any{}, "nominated_by": actor,
			"frozen_answer_segment_id": segmentID, "frozen_crop_file_asset_id": uuid.NewString(), "frozen_crop_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "frozen_crop_owner_type": "answer_segment_crop", "frozen_crop_owner_id": uuid.NewString()})
	}
	schoolScope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: actor, TenantID: tenant, Roles: []string{"school_admin"}, DataScope: map[string]any{"scope": "school", "school_id": schoolA}})
	if err != nil {
		t.Fatal(err)
	}
	mixed := schoolScope
	mixed.AssignedOnly = true
	mixed.ExamIDs = []string{examA, examB}
	mixed.SubmissionIDs = []string{submissions[1]}
	for _, tc := range []struct {
		name      string
		scope     auth.AccessScope
		want      []string
		examCount int
	}{
		{"school", schoolScope, submissions[:1], 1},
		{"tenant", auth.AccessScope{TenantID: tenant, ActorID: actor, TenantWide: true}, submissions, 3},
		{"assigned", auth.AccessScope{TenantID: tenant, ActorID: actor, AssignedOnly: true, ExamIDs: []string{examB}, SubmissionIDs: submissions[1:2]}, submissions[1:2], 2},
		{"mixed", mixed, submissions[:2], 3},
		{"class", auth.AccessScope{TenantID: tenant, ActorID: actor, ClassIDs: []string{classB}}, submissions[1:], 2},
		{"self", auth.AccessScope{TenantID: tenant, ActorID: actor, StudentID: student}, submissions[1:2], 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := auth.ExamCollectionAccess{Scope: &tc.scope}
			items, err := aidisagreement.NewPostgresStore(db).List(ctx, tenant, aidisagreement.Filter{Access: access, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, item := range items {
				got = append(got, item.SubmissionID)
			}
			want := append([]string{}, tc.want...)
			sort.Strings(got)
			sort.Strings(want)
			if len(got) != len(want) {
				t.Fatalf("submissions=%v want=%v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("submissions=%v want=%v", got, want)
				}
			}
			memoryAccess := auth.ExamCollectionAccess{Scope: &tc.scope, Resolver: boundaries}
			memoryItems, err := memoryIssues.List(ctx, tenant, aidisagreement.Filter{Access: memoryAccess, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			memoryIDs := []string{}
			for _, item := range memoryItems {
				memoryIDs = append(memoryIDs, item.SubmissionID)
			}
			sort.Strings(memoryIDs)
			if len(memoryIDs) != len(want) {
				t.Fatalf("memory submissions=%v want=%v", memoryIDs, want)
			}
			for i := range memoryIDs {
				if memoryIDs[i] != want[i] {
					t.Fatalf("memory submissions=%v want=%v", memoryIDs, want)
				}
			}
			limited, err := aidisagreement.NewPostgresStore(db).List(ctx, tenant, aidisagreement.Filter{Access: access, Limit: 1})
			if err != nil || len(limited) != 1 {
				t.Fatalf("authorized rows must survive limit: %v %v", limited, err)
			}
			jobs, err := regrade.NewPostgresStore(db).List(ctx, tenant, "", "", access)
			if err != nil || len(jobs) != tc.examCount {
				t.Fatalf("jobs=%d want=%d err=%v", len(jobs), tc.examCount, err)
			}
			gold, err := goldpaper.NewPostgresStore(db).List(ctx, tenant, goldpaper.ListFilter{Access: access})
			if err != nil || len(gold) != tc.examCount {
				t.Fatalf("gold=%d want=%d err=%v", len(gold), tc.examCount, err)
			}
			filtered, err := goldpaper.NewPostgresStore(db).List(ctx, tenant, goldpaper.ListFilter{Access: access, ExamID: examB})
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "school" && len(filtered) != 0 {
				t.Fatal("client exam filter expanded scope")
			}
		})
	}
}
