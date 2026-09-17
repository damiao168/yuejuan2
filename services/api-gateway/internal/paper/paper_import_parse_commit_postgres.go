package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func (s *PostgresStore) CompletePaperImportParseTask(ctx context.Context, tenantID, taskID, leaseToken string, binding PaperImportRunBinding, parsed PaperImportParseResult, durationMS int) (PaperImportJob, error) {
	drafts, structured := reconcilePaperImportCandidates(parsed.QuestionCandidates, parsed.AnswerCandidates, parsed.SolutionCandidates, parsed.RubricCandidates, appendDetectedRoleIssues(parsed.Issues, parsed.Documents))
	if err := s.applyPaperImportAssessmentArchetypes(ctx, tenantID, binding.ImportID, drafts); err != nil {
		return PaperImportJob{}, err
	}
	structured = appendReviewedDraftIssues(structured, drafts)
	structured = s.appendPaperImportBlueprintIssues(ctx, tenantID, binding.ImportID, parsed.QuestionCandidates, structured)
	resultPayload := mapFromJSON(parsed)
	resultPayload["paper_import_id"] = binding.ImportID
	resultPayload["run_id"] = binding.RunID
	resultPayload["generation"] = binding.Generation
	resultHash := contentHash(mustJSON(resultPayload))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	runID, generation, revision, jobStatus, err := paperImportRunForUpdate(ctx, tx, tenantID, binding.ImportID)
	if err != nil {
		return PaperImportJob{}, err
	}
	var inputHash string
	if err = tx.QueryRowContext(ctx, `SELECT input_hash FROM paper_import_parse_input
WHERE tenant_id=$1 AND id=$2::uuid AND paper_import_id=$3::uuid AND run_id=$4::uuid AND generation=$5 AND source_revision=$6 AND protocol_version=2`, tenantID, binding.InputID, binding.ImportID, binding.RunID, binding.Generation, binding.SourceRevision).Scan(&inputHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PaperImportJob{}, ErrConflict
		}
		return PaperImportJob{}, err
	}
	if inputHash != binding.InputHash {
		return PaperImportJob{}, ErrConflict
	}
	if runID != binding.RunID || generation != binding.Generation || revision != binding.SourceRevision {
		if err = confirmHistoricalPaperImportParseResult(ctx, tx, tenantID, taskID, leaseToken, binding, resultPayload, resultHash, durationMS); err != nil {
			return PaperImportJob{}, err
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, binding.ImportID)
	}
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, taskID, runID, generation, "paper_parse", "paper_import_parse", binding.InputID); err != nil {
		return PaperImportJob{}, err
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, taskID, workerruntime.CompleteInput{
		LeaseToken: leaseToken, ResultSchemaVersion: "paper-import-parse-result-v2", Result: resultPayload, DurationMS: durationMS,
	}); err != nil {
		if errors.Is(err, workerruntime.ErrConflict) {
			return PaperImportJob{}, ErrConflict
		}
		return PaperImportJob{}, err
	}
	if jobStatus == "review_required" {
		var resultTaskID, persistedHash string
		var resultGeneration sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(result_task_id::text,''),COALESCE(result_payload_hash,''),result_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, binding.ImportID).Scan(&resultTaskID, &persistedHash, &resultGeneration); err != nil {
			return PaperImportJob{}, err
		}
		if !resultGeneration.Valid || resultGeneration.Int64 != binding.Generation || resultTaskID != taskID || persistedHash != resultHash {
			return PaperImportJob{}, ErrConflict
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, binding.ImportID)
	}
	if jobStatus != "processing" {
		return PaperImportJob{}, ErrConflict
	}

	var previousRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT draft_questions FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, binding.ImportID).Scan(&previousRaw); err != nil {
		return PaperImportJob{}, err
	}
	var previous []PaperImportDraftQuestion
	if json.Unmarshal(previousRaw, &previous) == nil {
		structured = appendHumanConfirmationConflicts(structured, drafts, previous)
		drafts = preserveHumanConfirmedDrafts(drafts, previous)
		structured = issuesAfterHumanReview(structured, drafts, false)
	}
	q, _ := json.Marshal(parsed.QuestionCandidates)
	a, _ := json.Marshal(parsed.AnswerCandidates)
	so, _ := json.Marshal(parsed.SolutionCandidates)
	ru, _ := json.Marshal(parsed.RubricCandidates)
	d, _ := json.Marshal(drafts)
	si, _ := json.Marshal(structured)
	messages, _ := json.Marshal(issueMessages(structured))
	usage, _ := json.Marshal(parsed.ModelUsage)
	update, err := tx.ExecContext(ctx, `UPDATE paper_import_job SET status='review_required',question_candidates=$4,answer_candidates=$5,solution_candidates=$6,rubric_candidates=$7,draft_questions=$8,structured_issues=$9,issues=$10,error_code='',result_generation=$3,result_task_id=$11::uuid,result_payload_hash=$12,model_usage=$14::jsonb,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND current_generation=$3 AND source_revision=$13 AND status='processing' AND deleted_at IS NULL`, tenantID, binding.ImportID, binding.Generation, q, a, so, ru, d, si, messages, taskID, resultHash, binding.SourceRevision, string(usage))
	if err != nil {
		return PaperImportJob{}, err
	}
	rows, rowsErr := update.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if rows != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='processed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, binding.ImportID); err != nil {
		return PaperImportJob{}, err
	}
	for _, item := range parsed.Documents {
		if !validPaperImportRole(item.DetectedRole, false) {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET detected_role=$4,role_confidence=$5,updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND id=$3::uuid AND deleted_at IS NULL`, tenantID, binding.ImportID, item.SourceID, item.DetectedRole, item.RoleConfidence); err != nil {
			return PaperImportJob{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE paper_import_run SET status='review_required',result_task_id=$4::uuid,result_payload_hash=$5,completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, binding.RunID, binding.Generation, taskID, resultHash)
	if err != nil {
		return PaperImportJob{}, err
	}
	rows, rowsErr = result.RowsAffected()
	if rowsErr != nil {
		return PaperImportJob{}, rowsErr
	}
	if rows != 1 {
		return PaperImportJob{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return PaperImportJob{}, err
	}
	return s.GetPaperImport(ctx, tenantID, binding.ImportID)
}

func confirmHistoricalPaperImportParseResult(ctx context.Context, tx *sql.Tx, tenantID, taskID, leaseToken string, binding PaperImportRunBinding, resultPayload map[string]any, resultHash string, durationMS int) error {
	var sourceRevision, status, persistedTaskID, persistedHash string
	err := tx.QueryRowContext(ctx, `SELECT source_revision,status,COALESCE(result_task_id::text,''),COALESCE(result_payload_hash,'')
FROM paper_import_run
WHERE tenant_id=$1 AND id=$2::uuid AND paper_import_id=$3::uuid AND generation=$4
FOR UPDATE`, tenantID, binding.RunID, binding.ImportID, binding.Generation).Scan(&sourceRevision, &status, &persistedTaskID, &persistedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if sourceRevision != binding.SourceRevision || (status != "review_required" && status != "applied") || persistedTaskID != taskID || persistedHash != resultHash {
		return ErrConflict
	}
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, taskID, binding.RunID, binding.Generation, "paper_parse", "paper_import_parse", binding.InputID); err != nil {
		return err
	}
	_, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, taskID, workerruntime.CompleteInput{
		LeaseToken: leaseToken, ResultSchemaVersion: "paper-import-parse-result-v2", Result: resultPayload, DurationMS: durationMS,
	})
	if errors.Is(err, workerruntime.ErrConflict) {
		return ErrConflict
	}
	return err
}
