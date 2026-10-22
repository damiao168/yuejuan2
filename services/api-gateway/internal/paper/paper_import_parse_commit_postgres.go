package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// CompletePaperImportParseTask 在同一事务内提交任务回执与导入待核对结果。
// 当前代次改变后只能核对相同历史成功回执，不能把旧结果写到新代次。
func (s *PostgresStore) CompletePaperImportParseTask(ctx context.Context, tenantID, taskID, leaseToken string, binding PaperImportRunBinding, parsed PaperImportParseResult, durationMS int) (PaperImportJob, error) {
	if binding.CommandType == "add_sources" {
		previous, err := s.GetPaperImport(ctx, tenantID, binding.ImportID)
		if err != nil {
			return PaperImportJob{}, err
		}
		parsed = mergePaperImportAppendResult(previous, parsed)
	}
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
	completion := workerruntime.CompleteInput{
		LeaseToken: leaseToken, ResultSchemaVersion: "paper-import-parse-result-v2", Result: resultPayload, DurationMS: durationMS,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer tx.Rollback()
	runID, generation, revision, jobStatus, err := paperImportRunForUpdate(ctx, tx, tenantID, binding.ImportID)
	if err != nil {
		return PaperImportJob{}, err
	}
	if err = validatePaperImportParseInputTx(ctx, tx, tenantID, binding); err != nil {
		return PaperImportJob{}, err
	}
	// 代次已变化时只允许确认历史上相同任务、相同结果的成功回执，不把旧结果写到当前代次。
	if runID != binding.RunID || generation != binding.Generation || revision != binding.SourceRevision {
		if err = confirmHistoricalPaperImportParseResult(ctx, tx, tenantID, taskID, binding, completion, resultHash); err != nil {
			return PaperImportJob{}, err
		}
		if err = tx.Commit(); err != nil {
			return PaperImportJob{}, err
		}
		return s.GetPaperImport(ctx, tenantID, binding.ImportID)
	}
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, taskID, runID, generation, paperImportTaskStage{taskType: "paper_parse", sourceType: "paper_import_parse", sourceID: binding.InputID}); err != nil {
		return PaperImportJob{}, err
	}
	// 任务完成与待核对结果共用事务；后续校验失败也会回滚此处，避免任务成功却没有业务结果。
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, taskID, completion); err != nil {
		if errors.Is(err, workerruntime.ErrConflict) {
			return PaperImportJob{}, ErrConflict
		}
		return PaperImportJob{}, err
	}
	if jobStatus == "review_required" {
		if err = confirmCurrentPaperImportParseResultTx(ctx, tx, tenantID, taskID, binding, resultHash); err != nil {
			return PaperImportJob{}, err
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
		// 先记录新旧候选冲突，再保留人工确认值；顺序反过来会掩盖本轮识别与人工结果的差异。
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
	if err = recordPaperImportParseUsageTx(ctx, tx, tenantID, binding, parsed.ModelUsage); err != nil {
		return PaperImportJob{}, err
	}
	if err = updatePaperImportParsedSourcesTx(ctx, tx, tenantID, binding.ImportID, parsed.Documents); err != nil {
		return PaperImportJob{}, err
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

// 输入必须来自本导入的同一运行、代次和来源版本；缺失输入与哈希变化都按版本冲突处理。
func validatePaperImportParseInputTx(ctx context.Context, tx *sql.Tx, tenantID string, binding PaperImportRunBinding) error {
	var inputHash string
	err := tx.QueryRowContext(ctx, `SELECT input_hash FROM paper_import_parse_input
WHERE tenant_id=$1 AND id=$2::uuid AND paper_import_id=$3::uuid AND run_id=$4::uuid AND generation=$5 AND source_revision=$6 AND protocol_version=2`, tenantID, binding.InputID, binding.ImportID, binding.RunID, binding.Generation, binding.SourceRevision).Scan(&inputHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if inputHash != binding.InputHash {
		return ErrConflict
	}
	return nil
}

// 当前代次已待核对时只确认相同任务和相同结果，不能用重试覆盖已保存的候选。
func confirmCurrentPaperImportParseResultTx(ctx context.Context, tx *sql.Tx, tenantID, taskID string, binding PaperImportRunBinding, resultHash string) error {
	var resultTaskID, persistedHash string
	var resultGeneration sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(result_task_id::text,''),COALESCE(result_payload_hash,''),result_generation FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, binding.ImportID).Scan(&resultTaskID, &persistedHash, &resultGeneration); err != nil {
		return err
	}
	if !resultGeneration.Valid || resultGeneration.Int64 != binding.Generation || resultTaskID != taskID || persistedHash != resultHash {
		return ErrConflict
	}
	return nil
}

// 历史回执必须同时匹配运行版本、任务和结果；完成输入沿用当前提交的同一租约与 payload。
func confirmHistoricalPaperImportParseResult(ctx context.Context, tx *sql.Tx, tenantID, taskID string, binding PaperImportRunBinding, completion workerruntime.CompleteInput, resultHash string) error {
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
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, taskID, binding.RunID, binding.Generation, paperImportTaskStage{taskType: "paper_parse", sourceType: "paper_import_parse", sourceID: binding.InputID}); err != nil {
		return err
	}
	_, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, taskID, completion)
	if errors.Is(err, workerruntime.ErrConflict) {
		return ErrConflict
	}
	return err
}

// 用量事件与业务结果使用调用方事务；请求 ID 沿用 run，重复结果不会新增同一事件。
func recordPaperImportParseUsageTx(ctx context.Context, tx *sql.Tx, tenantID string, binding PaperImportRunBinding, usage PaperImportModelUsage) error {
	if usage.RequestCount > 0 || usage.TotalTokens > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CachedInputTokens > 0 || usage.ReasoningTokens > 0 {
		_, err := tx.ExecContext(ctx, `
INSERT INTO model_usage_event(
  tenant_id,school_id,request_id,feature,provider_key,model_name,
  request_count,input_tokens,output_tokens,cached_input_tokens,reasoning_tokens,total_tokens,status,metadata
)
SELECT job.tenant_id,exam.school_id,$3,'paper_import',
  COALESCE(NULLIF($11,''),config.provider_key,'document_parser'),COALESCE(NULLIF($12,''),config.model_name,'document-parser'),
  CASE WHEN $13::bigint > 0 THEN $13::bigint ELSE 1 END,
  $4,$5,$6,$7,CASE WHEN $8::bigint > 0 THEN $8::bigint ELSE $4::bigint+$5::bigint END,
  'succeeded',jsonb_build_object('paper_import_id',$2::text,'generation',$9::bigint,'run_id',$10::text,'provider_request_count',CASE WHEN $13::bigint > 0 THEN $13::bigint ELSE 1 END)
FROM paper_import_job job
JOIN exam ON exam.tenant_id=job.tenant_id AND exam.id=job.exam_id
LEFT JOIN LATERAL (
  SELECT provider_key,model_name FROM managed_model_api_config
  WHERE tenant_id=job.tenant_id AND is_default AND deleted_at IS NULL
  ORDER BY updated_at DESC LIMIT 1
) config ON true
WHERE job.tenant_id=$1::uuid AND job.id=$2::uuid
ON CONFLICT (tenant_id,request_id,feature) DO NOTHING`, tenantID, binding.ImportID, "paper-import:"+binding.RunID,
			usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens, usage.ReasoningTokens, usage.TotalTokens, binding.Generation, binding.RunID,
			usage.ProviderKey, usage.ModelName, usage.RequestCount)
		return err
	}
	return nil
}

// 先标记全部来源处理完成，再按解析结果顺序更新合法角色；失败仍由外层整批回滚。
func updatePaperImportParsedSourcesTx(ctx context.Context, tx *sql.Tx, tenantID, importID string, documents []PaperImportDetectedDocument) error {
	if _, err := tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='processed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return err
	}
	for _, item := range documents {
		if !validPaperImportRole(item.DetectedRole, false) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE paper_import_source SET detected_role=$4,role_confidence=$5,updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND id=$3::uuid AND deleted_at IS NULL`, tenantID, importID, item.SourceID, item.DetectedRole, item.RoleConfidence); err != nil {
			return err
		}
	}
	return nil
}
