package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestCancelledScoringRunCannotReassignOrSubmitReview(t *testing.T) {
	db, _, _, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	var segmentID, questionNo string
	questionID := f.QuestionIDs["single_choice"]
	if err := db.QueryRowContext(ctx, `SELECT answer_segment_id::text,question_no FROM final_grade WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID, &questionNo); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,started_by,cancelled_at) VALUES($1,$2::uuid,'cancelled-review-regression','cancelled',1,$3::uuid,now()) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := db.QueryRowContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,grade_round,scoring_run_id,created_by) VALUES($1,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,'anonymous','manual_sample','cancelled','single',$7::uuid,$8::uuid) RETURNING id::text`, f.TenantID, f.ExamID, questionID, questionNo, segmentID, answers[0].submission, runID, f.AdminID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	store := review.NewPostgresStore(db)
	if _, err := store.AssignTask(ctx, f.TenantID, taskID, f.AdminID, review.AssignTaskInput{AssignedTo: f.AdminID, ExpectedRevision: 1}); !errors.Is(err, review.ErrInvalidTransition) {
		t.Fatalf("assign cancelled task: %v", err)
	}
	if _, err := store.BatchAssignTasks(ctx, f.TenantID, f.AdminID, review.BatchAssignInput{TaskIDs: []string{taskID}, AssignedTo: f.AdminID, ExpectedRevisions: map[string]int64{taskID: 1}}); !errors.Is(err, review.ErrInvalidTransition) {
		t.Fatalf("batch assign cancelled task: %v", err)
	}
	if _, err := store.SubmitGrade(ctx, f.TenantID, taskID, f.AdminID, review.SubmitGradeInput{ExpectedRevision: 1, Score: 1}); !errors.Is(err, review.ErrInvalidTransition) {
		t.Fatalf("submit cancelled task: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,source,decision,engine_version,profile_version,input_hash,is_current,created_by) VALUES($1,$2::uuid,$3::uuid,'manual','confirmed','v1','v1','1234567890abcdef',true,$4::uuid)`, f.TenantID, segmentID, runID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	_, err := grading.NewPostgresStore(db).ConfirmRuleGrade(ctx, f.TenantID, segmentID, f.AdminID, grading.Grade{AnswerSegmentID: segmentID, AutoPass: true, SuggestedScore: 1, MaxScore: 1})
	if !errors.Is(err, grading.ErrInvalidTransition) {
		t.Fatalf("rule confirmation after cancellation: %v", err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, runID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("run status %q: %v", status, err)
	}
}

func TestRuleConfirmationWaitsForConcurrentCancellation(t *testing.T) {
	db, _, _, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	questionID := f.QuestionIDs["single_choice"]
	var segmentID string
	if err := db.QueryRowContext(ctx, `SELECT answer_segment_id::text FROM final_grade WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,started_by) VALUES($1,$2::uuid,'concurrent-cancel-regression','processing',1,$3::uuid) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,source,decision,engine_version,profile_version,input_hash,is_current,created_by) VALUES($1,$2::uuid,$3::uuid,'manual','confirmed','v1','v1','1234567890abcdef',true,$4::uuid)`, f.TenantID, segmentID, runID, f.AdminID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_run SET status='cancelled',cancelled_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, runID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, confirmErr := grading.NewPostgresStore(db).ConfirmRuleGrade(ctx, f.TenantID, segmentID, f.AdminID, grading.Grade{AnswerSegmentID: segmentID, AutoPass: true, SuggestedScore: 1, MaxScore: 1})
		result <- confirmErr
	}()
	select {
	case early := <-result:
		t.Fatalf("rule confirmation bypassed in-flight cancellation lock: %v", early)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case confirmErr := <-result:
		if !errors.Is(confirmErr, grading.ErrInvalidTransition) {
			t.Fatalf("rule confirmation after cancellation: %v", confirmErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("rule confirmation remained blocked after cancellation committed")
	}
	var runGrades int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM question_grade WHERE tenant_id=$1 AND scoring_run_id=$2::uuid AND is_current`, f.TenantID, runID).Scan(&runGrades); err != nil || runGrades != 0 {
		t.Fatalf("cancelled run gained %d current grades: %v", runGrades, err)
	}
}

func TestScoringAIBatchCancellationBlocksNewTasks(t *testing.T) {
	db, router, token, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	questionID := f.QuestionIDs["single_choice"]
	var segmentID string
	if err := db.QueryRowContext(ctx, `SELECT answer_segment_id::text FROM final_grade WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,started_by) VALUES($1,$2::uuid,'cancel-ai-batch-regression','processing',1,$3::uuid) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var batchID string
	if err := db.QueryRowContext(ctx, `INSERT INTO subjective_grading_batch(tenant_id,idempotency_key,scoring_run_id,status,segment_ids,total_count,created_by) VALUES($1,'linked-ai-batch',$2::uuid,'processing',jsonb_build_array($3::text),1,$4::uuid) RETURNING id::text`, f.TenantID, runID, segmentID, f.AdminID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	listed := e2eGetJSON(t, router, "/api/v1/scoring-runs/"+runID+"/ai-batches", token, http.StatusOK)
	batches, ok := listed["batches"].([]any)
	if !ok || len(batches) != 1 || batches[0].(map[string]any)["id"] != batchID {
		t.Fatalf("linked batches not restored from server: %+v", listed)
	}
	var aiRunID string
	if err := db.QueryRowContext(ctx, `INSERT INTO subjective_grading_run(tenant_id,batch_id,answer_segment_id,answer_version,question_id,rubric_version,model_version,prompt_version,request_id) VALUES($1,$2::uuid,$3::uuid,'v1',$4::uuid,'v1','v1','v1','linked-ai-run') RETURNING id::text`, f.TenantID, batchID, segmentID, questionID).Scan(&aiRunID); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := db.QueryRowContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,payload_schema_version,idempotency_key,created_by) VALUES($1,'ai_grade','subjective-grading','subjective_grading_run',$2::uuid,'subjective-grade-v1','linked-ai-task',$3::uuid) RETURNING id::text`, f.TenantID, aiRunID, f.AdminID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_run SET status='cancelling' WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, runID); err != nil {
		t.Fatal(err)
	}
	lateTask := make(chan error, 1)
	go func() {
		_, insertErr := db.ExecContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,payload_schema_version,idempotency_key,created_by) VALUES($1,'ai_grade','subjective-grading','subjective_grading_run',$2::uuid,'subjective-grade-v1','late-ai-task',$3::uuid)`, f.TenantID, aiRunID, f.AdminID)
		lateTask <- insertErr
	}()
	select {
	case insertErr := <-lateTask:
		t.Fatalf("AI task insertion bypassed in-flight cancellation lock: %v", insertErr)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case insertErr := <-lateTask:
		if insertErr == nil {
			t.Fatal("AI task was created after cancellation began")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("AI task insertion remained blocked after cancellation committed")
	}
	store := grading.NewPostgresStore(db)
	run, taskIDs, err := store.BeginScoringRunCancellation(ctx, f.TenantID, runID)
	if err != nil || run.Status != "cancelling" || len(taskIDs) != 1 || taskIDs[0] != taskID {
		t.Fatalf("begin AI cancellation: run=%+v tasks=%v err=%v", run, taskIDs, err)
	}
	if _, err := store.FinalizeScoringRunCancellation(ctx, f.TenantID, runID); !errors.Is(err, grading.ErrInvalidTransition) {
		t.Fatalf("finalize with active AI task: %v", err)
	}
	cancelled := e2ePostJSON(t, router, http.MethodPost, "/api/v1/scoring-runs/"+runID+"/cancel", token, `{}`, http.StatusOK)
	if cancelled["scoring_run"].(map[string]any)["status"] != "cancelled" {
		t.Fatalf("cancel API did not finalize AI task: %+v", cancelled)
	}
	var taskStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM agent_worker_task WHERE id=$1::uuid`, taskID).Scan(&taskStatus); err != nil || taskStatus != "cancelled" {
		t.Fatalf("AI task status %q: %v", taskStatus, err)
	}
	var batchStatus string
	if err := db.QueryRowContext(ctx, `SELECT status FROM subjective_grading_batch WHERE id=$1::uuid`, batchID).Scan(&batchStatus); err != nil || batchStatus != "cancelled" {
		t.Fatalf("AI batch status %q: %v", batchStatus, err)
	}
}

func TestFailedScoringAIBatchSegmentsRequireOpenTeacherTask(t *testing.T) {
	db, router, token, f, answers, _ := repairFixture(t)
	ctx := context.Background()
	questionID := f.QuestionIDs["single_choice"]
	var segmentID, questionNo string
	if err := db.QueryRowContext(ctx, `SELECT answer_segment_id::text,question_no FROM final_grade WHERE tenant_id=$1 AND submission_id=$2::uuid AND question_id=$3::uuid AND deleted_at IS NULL`, f.TenantID, answers[0].submission, questionID).Scan(&segmentID, &questionNo); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRowContext(ctx, `INSERT INTO scoring_run(tenant_id,exam_id,idempotency_key,status,total_count,started_by) VALUES($1,$2::uuid,'failed-ai-segment-regression','needs_review',1,$3::uuid) RETURNING id::text`, f.TenantID, f.ExamID, f.AdminID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := db.QueryRowContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,grade_round,scoring_run_id,created_by) VALUES($1,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,'anonymous','manual_sample','pending','single',$7::uuid,$8::uuid) RETURNING id::text`, f.TenantID, f.ExamID, questionID, questionNo, segmentID, answers[0].submission, runID, f.AdminID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	var batchID string
	if err := db.QueryRowContext(ctx, `INSERT INTO subjective_grading_batch(tenant_id,idempotency_key,scoring_run_id,status,segment_ids,total_count,created_by) VALUES($1,'failed-ai-segments',$2::uuid,'failed',jsonb_build_array($3::text),1,$4::uuid) RETURNING id::text`, f.TenantID, runID, segmentID, f.AdminID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO subjective_grading_run(tenant_id,batch_id,answer_segment_id,answer_version,question_id,rubric_version,model_version,prompt_version,request_id,status) VALUES($1,$2::uuid,$3::uuid,'v1',$4::uuid,'v1','v1','v1','failed-ai-run','failed')`, f.TenantID, batchID, segmentID, questionID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/subjective-grading-batches/" + batchID + "/failed-segments"
	listed := e2eGetJSON(t, router, path, token, http.StatusOK)
	segments, ok := listed["segment_ids"].([]any)
	if !ok || len(segments) != 1 || segments[0] != segmentID {
		t.Fatalf("failed segment not available for retry: %+v", listed)
	}
	if _, err := db.ExecContext(ctx, `UPDATE review_task SET status='cancelled' WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, taskID); err != nil {
		t.Fatal(err)
	}
	listed = e2eGetJSON(t, router, path, token, http.StatusOK)
	segments, ok = listed["segment_ids"].([]any)
	if !ok || len(segments) != 0 {
		t.Fatalf("closed teacher task remained retryable: %+v", listed)
	}
}
