package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresCalibrationFreezesActualAutoAcceptanceAndRejectsHistoricalBadApproval(t *testing.T) {
	ctx, db, migrate := newCalibrationGateFixture(t)
	migrate()
	store := NewPostgresStore(db)
	tenant, creator, labeler, approver, template, reference := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	session, cases := calibrationGateCases()
	session.ID, session.TenantID = uuid.NewString(), tenant
	session.TemplateID, session.TemplateContentHash = template, "sha256:synthetic-template"
	session.ProfileVersion, session.ProfileHash = "synthetic-profile-v1", "sha256:synthetic-profile"
	session.ReferenceFileAssetID, session.ReferenceSHA256 = reference, "sha256:synthetic-reference"
	single, multiple := uuid.NewString(), uuid.NewString()
	session.QuestionIDs = []string{single, multiple}
	insertCalibrationGateSession(t, ctx, db, session, creator)
	mustExec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	runIDs, taskIDs := make([]string, len(cases)), make([]string, len(cases))
	for index, item := range cases {
		questionID := single
		if item.QuestionType == "multiple_choice" {
			questionID = multiple
		}
		segment, runID, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		runIDs[index], taskIDs[index] = runID, taskID
		input, _ := calibrationObservedResult(item)
		input.ResultVersion = "synthetic-result-v1"
		result, err := json.Marshal(omrRuntimeResult(runID, input))
		if err != nil {
			t.Fatal(err)
		}
		selected, _ := json.Marshal(item.ObservedOptions)
		mustExec(`INSERT INTO answer_segment VALUES($1,$2,$3,'synthetic',NULL)`, tenant, segment, questionID)
		mustExec(`INSERT INTO agent_worker_task VALUES($1,$2,'omr_run',$3,'succeeded',$4::jsonb)`, tenant, taskID, runID, result)
		mustExec(`INSERT INTO omr_run(tenant_id,id,answer_segment_id,template_id,template_content_hash,profile_version,profile_hash,reference_file_asset_id,reference_sha256,status,decision,selected_options,confidence,measurements,runtime_task_id,result_version)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'completed',$10,$11::jsonb,$12,'[]',$13,'synthetic-result-v1')`, tenant, runID, segment, template,
			session.TemplateContentHash, session.ProfileVersion, session.ProfileHash, reference, session.ReferenceSHA256, item.ObservedDecision, selected, item.ObservedConfidence, taskID)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	questions := []calibrationQuestion{{ID: single, Type: "single_choice", OptionLabels: []string{"A", "B"}}, {ID: multiple, Type: "multiple_choice", OptionLabels: []string{"A", "B"}}}
	if err := store.populateOMRCalibrationCasesTx(ctx, tx, tenant, session, questions); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// 标签由合成真值给出；低置信错例留在人工分支，blank 和清晰多选必须完全一致才可批准。
	mustExec(`UPDATE omr_calibration_case SET expected_options=CASE WHEN sample_stratum='selected_low' THEN '["A"]'::jsonb ELSE observed_options END,
		matches=(sample_stratum<>'selected_low'),labeled_by=$2,labeled_at=now() WHERE calibration_session_id=$1`, session.ID, labeler)
	for _, index := range []int{2, 3} {
		mustExec(`UPDATE omr_calibration_case SET expected_options='["A"]',matches=false WHERE omr_run_id=$1`, runIDs[index])
		if _, err := store.ApproveOMRCalibration(ctx, tenant, session.ID, approver, ApproveOMRCalibrationInput{ApprovalNote: "synthetic approval regression"}); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("actual approval accepted a blank/multiple-choice automatic error at %d: %v", index, err)
		}
		mustExec(`UPDATE omr_calibration_case SET expected_options=observed_options,matches=true WHERE omr_run_id=$1`, runIDs[index])
	}
	approved, err := store.ApproveOMRCalibration(ctx, tenant, session.ID, approver, ApproveOMRCalibrationInput{ApprovalNote: "synthetic approval regression"})
	if err != nil || approved.Session.Status != "approved" || approved.Session.Summary.EligibleCount != 75 {
		t.Fatalf("correct balanced frozen evidence must approve: %#v, %v", approved.Session, err)
	}
	assertQualification := func(want bool) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		scope := omrCalibrationScope{templateID: template, templateContentHash: session.TemplateContentHash, profileVersion: session.ProfileVersion,
			profileHash: session.ProfileHash, referenceFileAssetID: reference, referenceSHA256: session.ReferenceSHA256}
		approval, err := store.loadApprovedOMRCalibrationTx(ctx, tx, tenant, scope, single)
		if err != nil || (approval != nil) != want {
			t.Fatalf("new task qualification: approval=%#v want=%v err=%v", approval, want, err)
		}
		valid, err := omrCalibrationEvidenceAllowsAutoConfirmTx(ctx, tx, tenant, session.ID)
		if err != nil || valid != want {
			t.Fatalf("in-flight callback qualification=%v want=%v err=%v", valid, want, err)
		}
	}
	assertQualification(true)
	// 模拟旧规则漏过并已批准的多选错例；无需改写历史 hash，也必须停止授予新任务及在途结果资格。
	mustExec(`UPDATE omr_calibration_case SET expected_options='["A"]',matches=false WHERE omr_run_id=$1`, runIDs[3])
	assertQualification(false)
	var historicalHash string
	if err := db.QueryRowContext(ctx, `SELECT evidence_hash FROM omr_calibration_session WHERE id=$1`, session.ID).Scan(&historicalHash); err != nil || historicalHash != approved.Session.EvidenceHash {
		t.Fatalf("historical approval evidence was rewritten: %q, %v", historicalHash, err)
	}
	mustExec(`UPDATE omr_calibration_case SET expected_options=observed_options,matches=true WHERE omr_run_id=$1`, runIDs[3])
	mustExec(`UPDATE agent_worker_task SET result='{}' WHERE id=$1`, taskIDs[3])
	assertQualification(true)
	mustExec(`UPDATE omr_calibration_case SET observed_result_gate='{}' WHERE omr_run_id=$1`, runIDs[3])
	assertQualification(false)
}

func TestPostgresCalibrationGateMigrationOnlyBackfillsMatchingRuntimeResult(t *testing.T) {
	ctx, db, migrate := newCalibrationGateFixture(t)
	tenant, creator := uuid.NewString(), uuid.NewString()
	session := OMRCalibrationSession{ID: uuid.NewString(), TenantID: tenant, TemplateID: uuid.NewString(), TemplateContentHash: "sha256:synthetic-template",
		ProfileVersion: "synthetic-v1", ProfileHash: "sha256:synthetic-profile", ReferenceFileAssetID: uuid.NewString(), ReferenceSHA256: "sha256:synthetic-reference",
		QuestionIDs: []string{uuid.NewString()}, OptionLabels: []string{"A", "B"}, MinimumSamples: 100, MinimumSamplesPerOption: 10, MinimumSamplesPerStratum: 10, MinimumConfidence: .98}
	insertCalibrationGateSession(t, ctx, db, session, creator)
	for _, matching := range []bool{true, false} {
		runID, taskID, segmentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		version := "synthetic-v1"
		if !matching {
			version = "stale-version"
		}
		result, _ := json.Marshal(map[string]any{"omr_run_id": runID, "result_version": version, "needs_human_review": false, "thresholds": nil})
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO agent_worker_task VALUES($1,$2,'omr_run',$3,'succeeded',$4::jsonb)`, []any{tenant, taskID, runID, result}},
			{`INSERT INTO omr_run(tenant_id,id,runtime_task_id,result_version) VALUES($1,$2,$3,'synthetic-v1')`, []any{tenant, runID, taskID}},
			{`INSERT INTO omr_calibration_case(tenant_id,calibration_session_id,omr_run_id,answer_segment_id,question_id,question_type,option_labels,crop_sha256,observed_decision,observed_options,observed_confidence,measurements)
			VALUES($1,$2,$3,$4,$5,'single_choice','["A","B"]','synthetic-crop','selected','["A"]',.99,'[]')`, []any{tenant, session.ID, runID, segmentID, session.QuestionIDs[0]}},
		} {
			if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	migrate()
	var filled, missing int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE observed_result_gate ? 'needs_human_review'),count(*) FILTER(WHERE observed_result_gate='{}') FROM omr_calibration_case`).Scan(&filled, &missing); err != nil || filled != 1 || missing != 1 {
		t.Fatalf("migration must not attach a stale runtime gate: filled=%d missing=%d err=%v", filled, missing, err)
	}
}

func insertCalibrationGateSession(t *testing.T, ctx context.Context, db *sql.DB, session OMRCalibrationSession, creator string) {
	t.Helper()
	questions, _ := json.Marshal(session.QuestionIDs)
	labels, _ := json.Marshal(session.OptionLabels)
	_, err := db.ExecContext(ctx, `INSERT INTO omr_calibration_session(id,tenant_id,template_id,template_content_hash,scope_type,question_id,question_ids,question_type,profile_version,profile_hash,reference_file_asset_id,reference_sha256,option_labels,minimum_samples,minimum_samples_per_option,minimum_samples_per_stratum,minimum_confidence,status,created_by)
	VALUES($1,$2,$3,$4,'template',NULL,$5::jsonb,'template',$6,$7,$8,$9,$10::jsonb,100,10,10,.98,'draft',$11)`, session.ID, session.TenantID, session.TemplateID,
		session.TemplateContentHash, questions, session.ProfileVersion, session.ProfileHash, session.ReferenceFileAssetID, session.ReferenceSHA256, labels, creator)
	if err != nil {
		t.Fatal(err)
	}
}

func newCalibrationGateFixture(t *testing.T) (context.Context, *sql.DB, func()) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_OMR_CALIBRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_OMR_CALIBRATION_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	admin := stdlib.OpenDB(*cfg)
	schema := "remediation_calibration_gate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean synthetic schema: %v", err)
		}
		admin.Close()
	})
	// 审批和样本使用正式表约束；抽样来源仅建所需列。本测试不声称覆盖跨模块外键、触发器和完整迁移。
	for _, ddl := range []string{
		`CREATE TABLE omr_calibration_session (LIKE public.omr_calibration_session INCLUDING ALL)`,
		`CREATE TABLE omr_calibration_case (LIKE public.omr_calibration_case INCLUDING ALL)`,
		`ALTER TABLE omr_calibration_case DROP COLUMN IF EXISTS observed_result_gate CASCADE`,
		`CREATE TABLE answer_segment(tenant_id uuid,id uuid,question_id uuid,question_no text,deleted_at timestamptz)`,
		`CREATE TABLE agent_worker_task(tenant_id uuid,id uuid,source_type text,source_id uuid,status text,result jsonb)`,
		`CREATE TABLE omr_run(tenant_id uuid,id uuid,answer_segment_id uuid,template_id uuid,template_content_hash text,profile_version text,profile_hash text,reference_file_asset_id uuid,reference_sha256 text,status text,decision text,selected_options jsonb,confidence float8,measurements jsonb,runtime_task_id uuid,result_version text,completed_at timestamptz,deleted_at timestamptz,overlay_file_asset_id uuid,crop_sha256 text DEFAULT 'synthetic-crop')`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, db, func() {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000213_omr_calibration_result_gate.sql"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
}
