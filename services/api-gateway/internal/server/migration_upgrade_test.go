package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"github.com/google/uuid"
)

// This exercises an upgrade with real preexisting records, rather than only
// replaying migrations into an empty database. The E2E helper uses an isolated
// database and records applied migrations so the second call applies 000155+.
func TestPostgresMigrationUpgradeFrom000154(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for PostgreSQL migration upgrade test")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrationsThrough(t, db, "000154_auth_wechat_login.sql")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	fixture := seedPre155UpgradeFixture(t, ctx, db)
	e2eApplyPostgresMigrations(t, db)

	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("find latest migration: %v (count=%d)", err, len(files))
	}
	sort.Strings(files)
	var appliedCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM edugrade_e2e_applied_migration`).Scan(&appliedCount); err != nil || appliedCount != len(files) {
		t.Fatalf("upgrade did not reach latest migration %s: applied=%d expected=%d err=%v", filepath.Base(files[len(files)-1]), appliedCount, len(files), err)
	}

	var roleCount int
	if err := db.QueryRowContext(ctx, `
SELECT count(*) FROM user_role ur
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id
WHERE ur.tenant_id=$1::uuid AND ur.user_id=$2::uuid AND r.code='tenant_admin'
  AND r.scope_type='tenant' AND ur.deleted_at IS NULL AND r.deleted_at IS NULL`, fixture.tenantID, fixture.actorID).Scan(&roleCount); err != nil || roleCount != 1 {
		t.Fatalf("tenant administrator role was not preserved: count=%d err=%v", roleCount, err)
	}
	var schoolScope string
	if err := db.QueryRowContext(ctx, `
SELECT ur.data_scope::text FROM user_role ur
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id
JOIN app_user u ON u.tenant_id=ur.tenant_id AND u.id=ur.user_id
WHERE ur.tenant_id=$1::uuid AND u.username='school_admin' AND u.school_id=$2::uuid
  AND r.code='school_admin' AND ur.deleted_at IS NULL`, fixture.tenantID, fixture.schoolID).Scan(&schoolScope); err != nil {
		t.Fatalf("read school administrator scope after upgrade: %v", err)
	}
	var scopedToFixture bool
	if err := db.QueryRowContext(ctx, `SELECT $1::jsonb = jsonb_build_object('scope','school','school_id',$2::text)`, schoolScope, fixture.schoolID).Scan(&scopedToFixture); err != nil || !scopedToFixture {
		t.Fatalf("school administrator scope expanded or changed after upgrade: %s err=%v", schoolScope, err)
	}

	var schoolName, studentName, examName, answerText, runStatus string
	var score float64
	if err := db.QueryRowContext(ctx, `
SELECT school.name, student.name, exam.name, answer.answer_text, run.status, grade.total_score
FROM school
JOIN student ON student.tenant_id=school.tenant_id AND student.school_id=school.id
JOIN submission ON submission.tenant_id=student.tenant_id AND submission.student_id=student.id
JOIN exam ON exam.tenant_id=submission.tenant_id AND exam.id=submission.exam_id AND exam.school_id=school.id
JOIN answer_segment segment ON segment.tenant_id=submission.tenant_id AND segment.submission_id=submission.id
JOIN answer_segment_answer answer ON answer.tenant_id=segment.tenant_id AND answer.answer_segment_id=segment.id
JOIN subjective_grading_run run ON run.tenant_id=segment.tenant_id AND run.answer_segment_id=segment.id
JOIN submission_grade grade ON grade.tenant_id=submission.tenant_id AND grade.submission_id=submission.id
WHERE school.tenant_id=$1::uuid AND school.id=$2::uuid AND student.id=$3::uuid
  AND exam.id=$4::uuid AND segment.id=$5::uuid AND run.id=$6::uuid
  AND school.deleted_at IS NULL AND student.deleted_at IS NULL AND exam.deleted_at IS NULL
  AND submission.deleted_at IS NULL AND segment.deleted_at IS NULL AND run.deleted_at IS NULL`,
		fixture.tenantID, fixture.schoolID, fixture.studentID, fixture.examID, fixture.segmentID, fixture.runID).
		Scan(&schoolName, &studentName, &examName, &answerText, &runStatus, &score); err != nil {
		t.Fatalf("read preexisting school-to-grading graph after upgrade: %v", err)
	}
	if schoolName != "Upgrade fixture school" || studentName != "Upgrade fixture student" || examName != "Upgrade fixture exam" ||
		answerText != "A preserved answer" || runStatus != "queued" || score != 4 {
		t.Fatalf("upgrade changed preexisting grading data: school=%q student=%q exam=%q answer=%q run=%q score=%v", schoolName, studentName, examName, answerText, runStatus, score)
	}

	for index, configID := range fixture.modelIDs {
		var ciphertext, nonce []byte
		var modelName, testStatus string
		var lastSuccessful sql.NullTime
		if err := db.QueryRowContext(ctx, `
SELECT credential_ciphertext,credential_nonce,model_name,last_test_status,last_successful_tested_at
FROM managed_model_api_config WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`,
			fixture.tenantID, configID).Scan(&ciphertext, &nonce, &modelName, &testStatus, &lastSuccessful); err != nil {
			t.Fatalf("read preexisting model %d after upgrade: %v", index, err)
		}
		if !bytes.Equal(ciphertext, fixture.ciphertexts[index]) || !bytes.Equal(nonce, fixture.nonces[index]) ||
			modelName != fixture.modelNames[index] || testStatus != "success" || !lastSuccessful.Valid {
			t.Fatalf("model %d metadata or ciphertext changed during upgrade", index)
		}
		plaintext, err := fixture.cipher.Decrypt(ciphertext, nonce, fixture.tenantID, configID)
		if err != nil || plaintext != fixture.apiKeys[index] {
			t.Fatalf("model %d credential cannot be decrypted after upgrade: %v", index, err)
		}
	}

	// 000156 adds the binding FK. Verify that all three preexisting model rows
	// remain valid targets for the new A/B/C configuration.
	for index, role := range []string{"primary_a", "primary_b", "arbiter"} {
		mustUpgradeExec(t, ctx, db, `
INSERT INTO model_role_binding(tenant_id,education_stage,subject_code,agent_role,
  managed_model_api_config_id,prompt_version,strength_rank,created_by)
VALUES($1::uuid,'senior','physics',$2,$3::uuid,'upgrade-prompt-v1',$4,$5::uuid)`,
			fixture.tenantID, role, fixture.modelIDs[index], index+1, fixture.actorID)
	}
	var bindingCount int
	if err := db.QueryRowContext(ctx, `
SELECT count(*) FROM model_role_binding binding
JOIN managed_model_api_config config ON config.tenant_id=binding.tenant_id AND config.id=binding.managed_model_api_config_id
WHERE binding.tenant_id=$1::uuid AND binding.status='active' AND config.deleted_at IS NULL`, fixture.tenantID).Scan(&bindingCount); err != nil || bindingCount != 3 {
		t.Fatalf("A/B/C bindings could not reference upgraded model configs: count=%d err=%v", bindingCount, err)
	}

	// The fourth historical config is deliberately unbound. A platform actor
	// belongs to another tenant, so this also checks the 000168 deleted_by FK.
	managedStore := modelgovernance.NewPostgresStore(db, fixture.cipher)
	if err := managedStore.DeleteManagedAPIConfig(ctx, fixture.tenantID, fixture.platformActorID, fixture.modelIDs[1]); !errors.Is(err, modelgovernance.ErrManagedConfigInUse) {
		t.Fatalf("active panel binding did not block PostgreSQL soft deletion: %v", err)
	}
	archivedID := fixture.modelIDs[3]
	if err := managedStore.DeleteManagedAPIConfig(ctx, fixture.tenantID, fixture.platformActorID, archivedID); err != nil {
		t.Fatalf("soft delete unbound historical model with platform actor: %v", err)
	}
	var archivedBy string
	var deletionReason string
	var archivedCiphertext, archivedNonce []byte
	if err := db.QueryRowContext(ctx, `
SELECT deleted_by::text,deletion_reason,credential_ciphertext,credential_nonce
FROM managed_model_api_config WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NOT NULL`,
		fixture.tenantID, archivedID).Scan(&archivedBy, &deletionReason, &archivedCiphertext, &archivedNonce); err != nil {
		t.Fatalf("read soft-deleted historical model: %v", err)
	}
	if archivedBy != fixture.platformActorID || deletionReason != "removed_by_platform_admin" || !bytes.Equal(archivedCiphertext, fixture.ciphertexts[3]) ||
		!bytes.Equal(archivedNonce, fixture.nonces[3]) {
		t.Fatal("soft deletion lost the historical credential or cross-tenant actor attribution")
	}
	if plaintext, err := fixture.cipher.Decrypt(archivedCiphertext, archivedNonce, fixture.tenantID, archivedID); err != nil || plaintext != fixture.apiKeys[3] {
		t.Fatalf("soft-deleted model credential cannot be read for audit: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM model_role_binding WHERE tenant_id=$1::uuid`, fixture.tenantID).Scan(&bindingCount); err != nil || bindingCount != 3 {
		t.Fatalf("soft deletion damaged other model binding FKs: count=%d err=%v", bindingCount, err)
	}
}

type pre155UpgradeFixture struct {
	tenantID, actorID, platformActorID, schoolID, studentID, examID, segmentID, runID string
	modelIDs, modelNames, apiKeys                                                     []string
	ciphertexts, nonces                                                               [][]byte
	cipher                                                                            *modelgovernance.CredentialCipher
}

func seedPre155UpgradeFixture(t *testing.T, ctx context.Context, db *sql.DB) pre155UpgradeFixture {
	t.Helper()
	fixture := pre155UpgradeFixture{
		schoolID: uuid.NewString(), studentID: uuid.NewString(), examID: uuid.NewString(),
		segmentID: uuid.NewString(), runID: uuid.NewString(),
	}
	if err := db.QueryRowContext(ctx, `
SELECT tenant.id::text, app_user.id::text FROM tenant
JOIN app_user ON app_user.tenant_id=tenant.id AND app_user.username='tenant_admin'
WHERE tenant.code='demo' AND tenant.deleted_at IS NULL AND app_user.deleted_at IS NULL`).
		Scan(&fixture.tenantID, &fixture.actorID); err != nil {
		t.Fatalf("find seeded tenant and administrator: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
SELECT u.id::text FROM app_user u JOIN tenant t ON t.id=u.tenant_id
WHERE t.code='platform' AND u.username='platform_admin' AND u.deleted_at IS NULL`).
		Scan(&fixture.platformActorID); err != nil {
		t.Fatalf("find seeded platform administrator: %v", err)
	}
	yearID, cohortID := uuid.NewString(), uuid.NewString()
	gradeID, classID, questionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	submissionID, assetID, pageID, answerID, gradeResultID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustUpgradeExec(t, ctx, db, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Upgrade fixture school','upgrade-fixture','active')`, fixture.schoolID, fixture.tenantID)
	mustUpgradeExec(t, ctx, db, `UPDATE app_user SET school_id=$1::uuid WHERE tenant_id=$2::uuid AND username='school_admin'`, fixture.schoolID, fixture.tenantID)
	mustUpgradeExec(t, ctx, db, `UPDATE user_role ur SET data_scope=jsonb_build_object('scope','school','school_id',$1::text)
FROM app_user u JOIN role r ON r.tenant_id=u.tenant_id
WHERE ur.tenant_id=$2::uuid AND ur.user_id=u.id AND ur.role_id=r.id
  AND u.username='school_admin' AND r.code='school_admin'`, fixture.schoolID, fixture.tenantID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current) VALUES($1::uuid,$2::uuid,$3::uuid,'2026-2027',2026,2027,'2026-09-01','2027-08-31',true)`, yearID, fixture.tenantID, fixture.schoolID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1::uuid,$2::uuid,$3::uuid,'senior',2026,2029,'2026 cohort')`, cohortID, fixture.tenantID, fixture.schoolID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO grade(id,tenant_id,school_id,name,level_no,academic_year,academic_year_id,grade_cohort_id,education_stage,status) VALUES($1::uuid,$2::uuid,$3::uuid,'Senior 1',10,'2026-2027',$4::uuid,$5::uuid,'senior','active')`, gradeID, fixture.tenantID, fixture.schoolID, yearID, cohortID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO school_class(id,tenant_id,school_id,grade_id,academic_year_id,grade_cohort_id,name,code,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'Class 1','upgrade-class','active')`, classID, fixture.tenantID, fixture.schoolID, gradeID, yearID, cohortID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'UPGRADE-001','Upgrade fixture student','active')`, fixture.studentID, fixture.tenantID, fixture.schoolID, classID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO student_enrollment(tenant_id,school_id,student_id,academic_year_id,grade_cohort_id,class_id,status,start_date) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'enrolled','2026-09-01')`, fixture.tenantID, fixture.schoolID, fixture.studentID, yearID, cohortID, classID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'Upgrade fixture exam','physics','midterm',5,'draft','ai_assisted','manual_after_confirmation',$4::uuid)`, fixture.examID, fixture.tenantID, fixture.schoolID, fixture.actorID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,stem,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1','short_answer',5,'Explain the result',1,'draft')`, questionID, fixture.tenantID, fixture.examID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'UPGRADE-001','pdf_upload','created',$5::uuid)`, submissionID, fixture.tenantID, fixture.examID, fixture.studentID, fixture.actorID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO file_asset(id,tenant_id,school_id,exam_id,submission_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'submission',$5::uuid,'answer.pdf','application/pdf',128,repeat('a',64),'upgrade-fixture','answer.pdf','private',$6::uuid)`, assetID, fixture.tenantID, fixture.schoolID, fixture.examID, submissionID, fixture.actorID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, pageID, fixture.tenantID, submissionID, assetID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'Q1','{"x":0.1,"y":0.2,"w":0.5,"h":0.2}'::jsonb,'manual','accepted')`, fixture.segmentID, fixture.tenantID, submissionID, pageID, questionID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO answer_segment_answer(id,tenant_id,answer_segment_id,answer_text,source,recorded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'A preserved answer','manual_entry',$4::uuid)`, answerID, fixture.tenantID, fixture.segmentID, fixture.actorID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO subjective_grading_run(id,tenant_id,answer_segment_id,answer_version,question_id,rubric_version,model_version,prompt_version,request_id,status) VALUES($1::uuid,$2::uuid,$3::uuid,'answer-v1',$4::uuid,'rubric-v1','model-v1','prompt-v1','upgrade-subjective-001','queued')`, fixture.runID, fixture.tenantID, fixture.segmentID, questionID)
	mustUpgradeExec(t, ctx, db, `INSERT INTO submission_grade(id,tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'UPGRADE-ANON-001',4,5,'pending_confirmation',$6::uuid)`, gradeResultID, fixture.tenantID, fixture.examID, submissionID, fixture.studentID, fixture.actorID)

	cipher, err := modelgovernance.NewCredentialCipher("upgrade-fixture-key-with-at-least-32-characters")
	if err != nil {
		t.Fatalf("create test credential cipher: %v", err)
	}
	fixture.cipher = cipher
	for index := 0; index < 4; index++ {
		configID := uuid.NewString()
		modelName := []string{"upgrade-model-a", "upgrade-model-b", "upgrade-model-c", "upgrade-model-archive"}[index]
		apiKey := "upgrade-secret-" + modelName
		ciphertext, nonce, err := cipher.Encrypt(apiKey, fixture.tenantID, configID)
		if err != nil {
			t.Fatalf("encrypt preexisting model credential %d: %v", index, err)
		}
		mustUpgradeExec(t, ctx, db, `
INSERT INTO managed_model_api_config(id,tenant_id,provider_key,display_name,adapter_type,base_url,
  model_name,model_version,credential_ciphertext,credential_nonce,credential_hint,status,
  is_default,last_test_status,last_tested_at,last_capability_status,last_capability_tested_at,
  last_capability_probe_version,created_by)
VALUES($1::uuid,$2::uuid,$3,$4,'openai_compatible','https://example.invalid/v1',$5,$5,$6,$7,'key-***',
  'active',$8,'success',now(),'success',now(),'structured-json-v3',$9::uuid)`,
			configID, fixture.tenantID, modelName, modelName, modelName, ciphertext, nonce, index == 0, fixture.actorID)
		fixture.modelIDs = append(fixture.modelIDs, configID)
		fixture.modelNames = append(fixture.modelNames, modelName)
		fixture.apiKeys = append(fixture.apiKeys, apiKey)
		fixture.ciphertexts = append(fixture.ciphertexts, ciphertext)
		fixture.nonces = append(fixture.nonces, nonce)
	}
	return fixture
}

func mustUpgradeExec(t *testing.T, ctx context.Context, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, statement, args...); err != nil {
		t.Fatalf("seed/verify migration upgrade fixture: %v\nstatement: %s", err, statement)
	}
}
