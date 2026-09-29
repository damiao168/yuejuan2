package auth_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type authorizationRoleContract struct {
	Scope                 string   `json:"scope"`
	Human                 bool     `json:"human"`
	ManagedUserAssignable bool     `json:"managed_user_assignable"`
	Permissions           []string `json:"permissions"`
}

func TestMigrationsDoNotSeedKnownDefaultPasswords(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("find migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no migrations found")
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read migration %s: %v", file, err)
		}
		if strings.Contains(string(raw), "crypt('ChangeMe123!', gen_salt('bf'))") {
			t.Fatalf("migration %s seeds a known default password", filepath.Base(file))
		}
	}
}

func TestStory047MigrationAddsTenantScopedDatabaseConstraints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000020_story047_tenant_constraints.sql"))
	if err != nil {
		t.Fatalf("read story 047 migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))

	for _, want := range []string{
		"ensure_tenant_identity('app_user', 'uq_app_user_tenant_id_id')",
		"ensure_tenant_identity('answer_segment', 'uq_answer_segment_tenant_id_id')",
		"ensure_tenant_identity('final_grade', 'uq_final_grade_tenant_id_id')",
		"ensure_tenant_fk('user_role', 'user_id', 'app_user', 'fk_user_role_app_user_tenant')",
		"ensure_tenant_fk('exam', 'school_id', 'school', 'fk_exam_school_tenant')",
		"ensure_tenant_fk('submission', 'student_id', 'student', 'fk_submission_student_tenant')",
		"ensure_tenant_fk('submission_page', 'file_asset_id', 'file_asset', 'fk_submission_page_file_asset_tenant')",
		"ensure_tenant_fk('ocr_result', 'submission_page_id', 'submission_page', 'fk_ocr_result_submission_page_tenant')",
		"ensure_tenant_fk('answer_segment', 'question_id', 'question', 'fk_answer_segment_question_tenant')",
		"ensure_tenant_fk('ai_grade', 'rubric_version_id', 'rubric_version', 'fk_ai_grade_rubric_version_tenant')",
		"ensure_tenant_fk('review_task', 'assigned_to', 'app_user', 'fk_review_task_assigned_to_tenant')",
		"ensure_tenant_fk('human_grade', 'reviewer_id', 'app_user', 'fk_human_grade_reviewer_tenant')",
		"ensure_tenant_fk('double_mark_session', 'first_review_task_id', 'review_task', 'fk_double_mark_session_first_review_task_tenant')",
		"ensure_tenant_fk('arbitration_task', 'double_mark_session_id', 'double_mark_session', 'fk_arbitration_task_double_mark_session_tenant')",
		"ensure_tenant_fk('final_grade', 'arbitration_task_id', 'arbitration_task', 'fk_final_grade_arbitration_task_tenant')",
		"ensure_tenant_fk('submission_grade', 'published_by', 'app_user', 'fk_submission_grade_published_by_tenant')",
		"ensure_tenant_fk('appeal', 'submission_grade_id', 'submission_grade', 'fk_appeal_submission_grade_tenant')",
		"ensure_tenant_fk('score_adjustment', 'adjusted_by', 'app_user', 'fk_score_adjustment_adjusted_by_tenant')",
		"RAISE EXCEPTION 'tenant scoped foreign key violation before adding %'",
		"FOREIGN KEY (tenant_id, %I) REFERENCES %I (tenant_id, id)",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("story 047 migration must contain %q", want)
		}
	}
}

func TestCompletedScopeHardeningAddsFileSubmissionIntegrity(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000024_completed_scope_integrity_hardening.sql"))
	if err != nil {
		t.Fatalf("read integrity hardening migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{
		"fk_file_asset_submission_tenant",
		"FOREIGN KEY (tenant_id, submission_id)",
		"REFERENCES submission (tenant_id, id)",
		"chk_file_asset_owner_reference",
		"invalid cross-tenant submission references",
		"inconsistent owner references",
		"VALIDATE CONSTRAINT chk_file_asset_owner_reference",
		"fk_quality_run_submission_tenant",
		"fk_quality_run_page_tenant",
		"fk_quality_run_source_file_tenant",
		"fk_quality_run_normalized_file_tenant",
		"fk_submission_page_latest_quality_run_tenant",
		"FOREIGN KEY (tenant_id, submission_id, submission_page_id)",
		"uq_file_asset_tenant_submission_id_id",
		"uq_quality_run_tenant_submission_page_id",
		"FOREIGN KEY (tenant_id, submission_id, source_file_asset_id)",
		"FOREIGN KEY (tenant_id, submission_id, id, latest_quality_run_id)",
		"invalid normalized file reference",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("integrity hardening migration must contain %q", want)
		}
	}
}

func TestOCRAvailabilityMigrationRevokesFullSystemReadFromReviewRoles(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000047_remove_teacher_grader_system_read.sql"))
	if err != nil {
		t.Fatalf("read OCR availability permission migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{
		"UPDATE role_permission AS rp",
		"rp.deleted_at IS NULL",
		"r.code IN ('teacher', 'grader')",
		"p.code = 'system:read'",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("OCR availability permission migration must contain %q", want)
		}
	}
}

func TestOCRRuntimeAtomicityMigrationAddsIdempotencyAndCanonicalEscaping(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000048_ocr_runtime_atomicity.sql"))
	if err != nil {
		t.Fatalf("read OCR runtime atomicity migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS idempotency_key TEXT",
		"ALTER COLUMN idempotency_key SET NOT NULL",
		"uq_ocr_task_active_idempotency",
		"CREATE OR REPLACE FUNCTION pg_temp.go_json_string",
		`E'\\u0026'`,
		`E'\\u003c'`,
		`E'\\u003e'`,
		"chr(8232)",
		"chr(8233)",
		"canonical_hash_input",
		"encode(digest(source.canonical_hash_input, 'sha256'), 'hex')",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("OCR runtime atomicity migration must contain %q", want)
		}
	}
}

func TestTeacherRoleDoesNotRetainReviewManagementPermissions(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000049_remove_teacher_review_management.sql"))
	if err != nil {
		t.Fatalf("read teacher review permission migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{
		"UPDATE role_permission AS rp",
		"rp.deleted_at IS NULL",
		"r.code = 'teacher'",
		"p.code IN ('review:manage', 'arbitration:manage')",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("teacher review permission migration must contain %q", want)
		}
	}
}

func TestScoringRunIndexesCoverRunScopedGradeLookups(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000050_scoring_run_grade_candidate_indexes.sql"))
	if err != nil {
		t.Fatalf("read scoring run index migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{
		"idx_question_grade_scoring_run",
		"question_grade (tenant_id, scoring_run_id, answer_segment_id)",
		"idx_answer_candidate_scoring_run",
		"answer_candidate (tenant_id, scoring_run_id, answer_segment_id)",
		"WHERE scoring_run_id IS NOT NULL AND deleted_at IS NULL",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("scoring run index migration must contain %q", want)
		}
	}
}

func TestHumanGradeAILinkMigrationAddsNullableForeignKeyAndLookupIndex(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000051_human_grade_ai_link.sql"))
	if err != nil {
		t.Fatalf("read human grade AI link migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS ai_grade_id UUID",
		"FOREIGN KEY (ai_grade_id) REFERENCES ai_grade(id)",
		"idx_human_grade_ai_grade",
		"human_grade (tenant_id, ai_grade_id)",
		"WHERE ai_grade_id IS NOT NULL AND deleted_at IS NULL",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("human grade AI link migration must contain %q", want)
		}
	}
}

func TestAuthorizationHardeningDefinesFinalRoleMatrix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000111_authorization_model_hardening.sql"))
	if err != nil {
		t.Fatalf("read authorization hardening migration: %v", err)
	}
	contractRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "authorization", "role-matrix.json"))
	if err != nil {
		t.Fatalf("read authorization role contract: %v", err)
	}
	contract := map[string]authorizationRoleContract{}
	if err := json.Unmarshal(contractRaw, &contract); err != nil {
		t.Fatalf("decode authorization role contract: %v", err)
	}
	wantScopes := map[string]string{
		"platform_admin": "platform", "tenant_admin": "tenant", "school_admin": "school",
		"teacher": "class", "grader": "exam_task", "arbitrator": "exam_task",
		"student": "self", "auditor": "tenant", "page_processing_worker": "service",
	}
	for role, scope := range wantScopes {
		if contract[role].Scope != scope {
			t.Fatalf("contract role %s scope=%q want %q", role, contract[role].Scope, scope)
		}
	}
	contains := func(values []string, target string) bool {
		for _, value := range values {
			if value == target {
				return true
			}
		}
		return false
	}
	for _, permission := range []string{"org:manage", "exam:manage", "file:manage", "submission:manage", "capture:manage", "review:manage", "review:work", "arbitration:manage", "score:manage", "report:read", "appeal:manage", "audit:read", "dashboard:read"} {
		if !contains(contract["school_admin"].Permissions, permission) {
			t.Fatalf("school_admin contract missing %s", permission)
		}
	}
	for _, role := range []string{"teacher", "grader", "arbitrator"} {
		for _, forbidden := range []string{"tenant:manage", "org:manage", "exam:manage", "system:read", "review:manage", "arbitration:manage", "score:manage"} {
			if contains(contract[role].Permissions, forbidden) {
				t.Fatalf("%s contract retains management permission %s", role, forbidden)
			}
		}
	}
	if contract["page_processing_worker"].Human || contract["page_processing_worker"].ManagedUserAssignable {
		t.Fatal("service role must not be human managed-user assignable")
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{"'dashboard:read'", "('teacher','class')", "('grader','exam_task')", "('arbitrator','exam_task')", "declared_school_binding", "HAVING COUNT(DISTINCT school_id)=1", "u.school_id IS NULL", "jsonb_build_object('scope','none')", "jsonb_build_object('scope','service')", "FROM student st", "UPDATE teacher_class"} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("authorization hardening migration missing %q", want)
		}
	}
}

func TestSchoolAdminRBACBackfillRepairsExistingTenantsAdditively(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000112_backfill_school_admin_rbac.sql"))
	if err != nil {
		t.Fatalf("read school administrator RBAC backfill migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{
		"source_role.code = 'school_admin'",
		"source_tenant.code = 'platform'",
		"CROSS JOIN school_admin_template template",
		"WHERE target_tenant.deleted_at IS NULL",
		"ON CONFLICT (tenant_id, code) DO UPDATE",
		"description = EXCLUDED.description",
		"INSERT INTO role_permission (tenant_id, role_id, permission_id)",
		"JOIN school_admin_template_codes template",
		"target_role.code = 'school_admin'",
		"ON CONFLICT (tenant_id, role_id, permission_id) DO UPDATE",
		"SET deleted_at = NULL",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("school administrator RBAC backfill migration must contain %q", want)
		}
	}
	for _, forbidden := range []string{"DELETE FROM permission", "DELETE FROM role_permission", "TRUNCATE"} {
		if strings.Contains(strings.ToUpper(sqlText), forbidden) {
			t.Fatalf("school administrator RBAC backfill must remain additive; found %q", forbidden)
		}
	}
}

func TestStory061OfflineEvaluationMigrationEnforcesEvidenceAndCompletionBoundaries(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000061_story061_offline_model_evaluation.sql"))
	if err != nil {
		t.Fatalf("read STORY-061 offline evaluation migration: %v", err)
	}
	sqlText := compactMigrationSQL(string(raw))
	for _, want := range []string{
		"CREATE TABLE model_evaluation_run",
		"CREATE TABLE model_evaluation_candidate",
		"evidence_class = 'protocol_fixture' AND authorization_reference = ''",
		"evidence_class = 'authorized_frozen_set'",
		"FOREIGN KEY (tenant_id, run_id) REFERENCES model_evaluation_run(tenant_id, id)",
		"FOREIGN KEY (tenant_id, deployment_id) REFERENCES model_deployment(tenant_id, id)",
		"enforce_model_evaluation_candidate_scope",
		"evaluation.status <> 'draft'",
		"NEW.provider_key <> governed_provider_key",
		"NEW.deployment_key <> governed_deployment_key",
		"NEW.model_version <> governed_model_version",
		"evaluation.evidence_class = 'authorized_frozen_set'",
		"evaluation.evidence_class = 'protocol_fixture'",
		"enforce_model_evaluation_completion",
		"candidate_count < 2 OR local_candidate_count < 1",
		"model evaluation evidence is immutable",
		"invalid model evaluation status transition",
		"completed model evaluation provenance is immutable",
		"reject_model_evaluation_candidate_mutation",
		"BEFORE UPDATE OR DELETE ON model_evaluation_candidate",
		"'model:evaluation:manage'",
		"role.code IN ('platform_admin', 'tenant_admin')",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("STORY-061 offline evaluation migration must contain %q", want)
		}
	}
}

func compactMigrationSQL(value string) string {
	// 这些断言只保护迁移源码契约；实际最终权限另由授权矩阵 PostgreSQL 测试核对。
	return strings.Join(strings.Fields(value), " ")
}
