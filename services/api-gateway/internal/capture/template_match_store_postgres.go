package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const templateMatchColumns = `id::text,exam_id::text,capture_page_id::text,submission_page_id::text,source_file_asset_id::text,source_sha256,source_page_revision,processing_status,COALESCE(decision,''),COALESCE(selected_template_id::text,''),COALESCE(selected_template_content_hash,''),COALESCE(score,0),COALESCE(margin,0),candidates,profile_version,COALESCE(runtime_task_id::text,''),COALESCE(error_code,''),created_by::text,created_at`

func queueTemplateMatchRun(ctx context.Context, tx *sql.Tx, tenantID, examID, actorID string, page registrationPageRow) (TemplateMatchRun, error) {
	if existing, err := scanTemplateMatchRun(tx.QueryRowContext(ctx, `SELECT `+templateMatchColumns+` FROM page_template_match_run WHERE tenant_id=$1::uuid AND capture_page_id=$2::uuid AND source_sha256=$3 AND source_page_revision=$4 AND processing_status IN ('processing','completed') AND deleted_at IS NULL`, tenantID, page.id, page.hash, page.revision)); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return TemplateMatchRun{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT ast.id::text,ast.content_hash,ast.name,ast.version_no,ast.layout,ep.file_asset_id::text,fa.content_type
FROM answer_sheet_template ast
JOIN exam_paper ep ON ep.tenant_id=ast.tenant_id AND ep.id=ast.exam_paper_id
JOIN file_asset fa ON fa.tenant_id=ep.tenant_id AND fa.id=ep.file_asset_id
WHERE ast.tenant_id=$1::uuid AND ast.exam_id=$2::uuid AND ast.status='locked' AND ast.deleted_at IS NULL
ORDER BY ast.version_no DESC LIMIT 8`, tenantID, examID)
	if err != nil {
		return TemplateMatchRun{}, err
	}
	candidates := []map[string]any{}
	for rows.Next() {
		var id, contentHash, name, assetID, contentType string
		var versionNo int
		var layoutRaw []byte
		if err = rows.Scan(&id, &contentHash, &name, &versionNo, &layoutRaw, &assetID, &contentType); err != nil {
			rows.Close()
			return TemplateMatchRun{}, err
		}
		var layout templateLayout
		if json.Unmarshal(layoutRaw, &layout) != nil {
			continue
		}
		for _, candidatePage := range layout.Pages {
			if candidatePage.PageNo != page.pageNo {
				continue
			}
			candidates = append(candidates, map[string]any{
				"template_id": id, "template_content_hash": contentHash,
				"template_name": name, "version_no": versionNo,
				"template_download_url": "/api/v1/files/" + assetID + "/download", "template_content_type": contentType,
				"template_page_index": candidatePage.PageNo, "question_regions": candidatePage.QuestionRegions,
			})
			break
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return TemplateMatchRun{}, err
	}
	if err = rows.Close(); err != nil {
		return TemplateMatchRun{}, err
	}
	// 没有任何锁定模板能覆盖该页时记录 unknown 并转人工，不创建无效的 worker 任务。
	if len(candidates) == 0 {
		evidence, _ := json.Marshal(candidates)
		run, insertErr := scanTemplateMatchRun(tx.QueryRowContext(ctx, `INSERT INTO page_template_match_run (tenant_id,exam_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,source_page_revision,processing_status,decision,candidates,created_by,completed_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,'completed','unknown',$8::jsonb,$9::uuid,now()) RETURNING `+templateMatchColumns, tenantID, examID, page.id, page.submissionPageID, page.assetID, page.hash, page.revision, evidence, actorID))
		if insertErr != nil {
			return TemplateMatchRun{}, insertErr
		}
		_, err = tx.ExecContext(ctx, `UPDATE capture_page SET status='needs_review',match_candidates=$3::jsonb,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, page.id, evidence)
		return run, err
	}
	var runID string
	err = tx.QueryRowContext(ctx, `INSERT INTO page_template_match_run (tenant_id,exam_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,source_page_revision,created_by) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::uuid) RETURNING id::text`, tenantID, examID, page.id, page.submissionPageID, page.assetID, page.hash, page.revision, actorID).Scan(&runID)
	if err != nil {
		return TemplateMatchRun{}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"template_match_run_id": runID, "capture_page_id": page.id, "exam_id": examID,
		"source_download_url": "/api/v1/files/" + page.assetID + "/download", "source_content_type": "image/png",
		"page_no": page.pageNo, "render_dpi": 300, "candidates": candidates,
	})
	key := "page-template-match:" + page.id + ":" + page.hash + ":r" + strconv.Itoa(page.revision)
	var taskID string
	err = tx.QueryRowContext(ctx, `INSERT INTO agent_worker_task (tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by) VALUES ($1::uuid,'page_template_match','page-processing','page_template_match_run',$2::uuid,65,$3::jsonb,'page-template-match-v1',$4,$4,3,10,$5::uuid) RETURNING id::text`, tenantID, runID, payload, key, actorID).Scan(&taskID)
	if err != nil {
		return TemplateMatchRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE page_template_match_run SET runtime_task_id=$3::uuid,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID, taskID); err != nil {
		return TemplateMatchRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status='page_matching',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, page.id); err != nil {
		return TemplateMatchRun{}, err
	}
	return scanTemplateMatchRun(tx.QueryRowContext(ctx, `SELECT `+templateMatchColumns+` FROM page_template_match_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID))
}

func scanTemplateMatchRun(row scanner) (TemplateMatchRun, error) {
	var item TemplateMatchRun
	var candidates []byte
	err := row.Scan(&item.ID, &item.ExamID, &item.CapturePageID, &item.SubmissionPageID, &item.SourceFileAssetID, &item.SourceSHA256, &item.SourcePageRevision, &item.ProcessingStatus, &item.Decision, &item.SelectedTemplateID, &item.SelectedTemplateContentHash, &item.Score, &item.Margin, &candidates, &item.ProfileVersion, &item.RuntimeTaskID, &item.ErrorCode, &item.CreatedBy, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TemplateMatchRun{}, ErrNotFound
	}
	if err != nil {
		return TemplateMatchRun{}, err
	}
	_ = json.Unmarshal(candidates, &item.Candidates)
	if item.Candidates == nil {
		item.Candidates = []map[string]any{}
	}
	return item, nil
}

func (s *PostgresStore) GetTemplateMatchRun(ctx context.Context, tenantID, runID string) (TemplateMatchRun, error) {
	return scanTemplateMatchRun(s.db.QueryRowContext(ctx, `SELECT `+templateMatchColumns+` FROM page_template_match_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, runID))
}

func (s *PostgresStore) ApplyTemplateMatchResult(ctx context.Context, tenantID, runID string, input TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TemplateMatchRun{}, nil, err
	}
	defer tx.Rollback()
	out, registrations, err := applyTemplateMatchResultInTx(ctx, tx, tenantID, runID, input)
	if err != nil {
		return TemplateMatchRun{}, nil, err
	}
	return out, registrations, tx.Commit()
}

func applyTemplateMatchResultInTx(ctx context.Context, tx *sql.Tx, tenantID, runID string, input TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error) {
	if input.Decision != "matched" && input.Decision != "ambiguous" && input.Decision != "unknown" {
		return TemplateMatchRun{}, nil, ErrInvalidInput
	}
	if input.Score < 0 || input.Score > 1 || input.Margin < 0 || input.Margin > 1 {
		return TemplateMatchRun{}, nil, ErrInvalidInput
	}
	current, err := scanTemplateMatchRun(tx.QueryRowContext(ctx, `SELECT `+templateMatchColumns+` FROM page_template_match_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID))
	if err != nil {
		return TemplateMatchRun{}, nil, err
	}
	if current.ProcessingStatus == "completed" {
		return current, []RegistrationRun{}, nil
	}
	if current.ProcessingStatus != "processing" && current.ProcessingStatus != "retryable_error" {
		return TemplateMatchRun{}, nil, ErrInvalidTransition
	}
	decision := input.Decision
	if decision == "matched" {
		var valid bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM answer_sheet_template WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND id=$3::uuid AND content_hash=$4 AND status='locked' AND deleted_at IS NULL)`, tenantID, current.ExamID, input.SelectedTemplateID, input.SelectedTemplateContentHash).Scan(&valid)
		if err != nil || !valid {
			return TemplateMatchRun{}, nil, ErrInvalidInput
		}
		result, bindErr := tx.ExecContext(ctx, `INSERT INTO exam_answer_sheet_template_binding (tenant_id,exam_id,template_id,template_content_hash,mode,source,bound_by) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,'bound_auto','automatic',$5::uuid) ON CONFLICT (tenant_id,exam_id) DO NOTHING`, tenantID, current.ExamID, input.SelectedTemplateID, input.SelectedTemplateContentHash, current.CreatedBy)
		if bindErr != nil {
			return TemplateMatchRun{}, nil, bindErr
		}
		// 同一考试只能保留一个绑定；并发自动匹配选中不同模板时转人工冲突，不覆盖先前绑定。
		if affected, _ := result.RowsAffected(); affected == 0 {
			var boundID, boundHash string
			if bindErr = tx.QueryRowContext(ctx, `SELECT template_id::text,template_content_hash FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, tenantID, current.ExamID).Scan(&boundID, &boundHash); bindErr != nil {
				return TemplateMatchRun{}, nil, bindErr
			}
			if boundID != input.SelectedTemplateID || boundHash != input.SelectedTemplateContentHash {
				decision = "conflict"
			}
		}
	}
	candidates, _ := json.Marshal(input.Candidates)
	selectedID := input.SelectedTemplateID
	selectedHash := input.SelectedTemplateContentHash
	if input.Decision != "matched" {
		selectedID, selectedHash = "", ""
	}
	row := tx.QueryRowContext(ctx, `UPDATE page_template_match_run SET processing_status='completed',decision=$3,selected_template_id=NULLIF($4,'')::uuid,selected_template_content_hash=NULLIF($5,''),score=$6,margin=$7,candidates=$8::jsonb,result_version=$9,duration_ms=$10,completed_at=now(),error_code=NULL,error_detail='{}'::jsonb,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+templateMatchColumns, tenantID, runID, decision, selectedID, selectedHash, input.Score, input.Margin, candidates, input.ResultVersion, input.DurationMS)
	out, err := scanTemplateMatchRun(row)
	if err != nil {
		return TemplateMatchRun{}, nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status=$3,match_candidates=$4::jsonb,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, current.CapturePageID, map[bool]string{true: "normalized", false: "needs_review"}[decision == "matched"], candidates); err != nil {
		return TemplateMatchRun{}, nil, err
	}
	registrations := []RegistrationRun{}
	if decision == "matched" {
		var submissionID string
		if err = tx.QueryRowContext(ctx, `SELECT submission_id::text FROM capture_page WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, current.CapturePageID).Scan(&submissionID); err != nil {
			return TemplateMatchRun{}, nil, err
		}
		registrations, err = QueueSubmissionPagesInTx(ctx, tx, tenantID, submissionID, current.CreatedBy)
		if err != nil {
			return TemplateMatchRun{}, nil, err
		}
	} else if err = aggregateTemplateMatchPageBatchInTx(ctx, tx, tenantID, current.CapturePageID); err != nil {
		return TemplateMatchRun{}, nil, err
	}
	return out, registrations, nil
}

func (s *PostgresStore) SubmitTemplateMatchResultCommand(ctx context.Context, tenantID, runID string, input TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TemplateMatchRun{}, nil, err
	}
	defer tx.Rollback()
	out, registrations, err := applyTemplateMatchResultInTx(ctx, tx, tenantID, runID, input)
	if err != nil {
		return TemplateMatchRun{}, nil, err
	}
	result := map[string]any{"template_match_run_id": runID, "result_version": input.ResultVersion, "decision": out.Decision, "selected_template_id": out.SelectedTemplateID, "score": out.Score, "margin": out.Margin}
	// 业务结果与任务租约完成共用事务，过期工作进程不能单独提交模板绑定和后续任务。
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "page-template-match-result-v1", Result: result, DurationMS: input.DurationMS}); err != nil {
		return TemplateMatchRun{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return TemplateMatchRun{}, nil, err
	}
	return out, registrations, nil
}

func (s *PostgresStore) ApplyTemplateMatchFailure(ctx context.Context, tenantID, runID, errorCode string, errorDetail map[string]any, retryable bool) (TemplateMatchRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TemplateMatchRun{}, err
	}
	defer tx.Rollback()
	out, err := applyTemplateMatchFailureInTx(ctx, tx, tenantID, runID, errorCode, errorDetail, retryable)
	if err != nil {
		return TemplateMatchRun{}, err
	}
	return out, tx.Commit()
}

func applyTemplateMatchFailureInTx(ctx context.Context, tx *sql.Tx, tenantID, runID, errorCode string, errorDetail map[string]any, retryable bool) (TemplateMatchRun, error) {
	status := "terminal_error"
	pageStatus := "needs_review"
	if retryable {
		status = "retryable_error"
		pageStatus = "page_matching"
	}
	detail, _ := json.Marshal(errorDetail)
	out, err := scanTemplateMatchRun(tx.QueryRowContext(ctx, `UPDATE page_template_match_run SET processing_status=$3,error_code=$4,error_detail=$5::jsonb,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL RETURNING `+templateMatchColumns, tenantID, runID, status, errorCode, detail))
	if err != nil {
		return TemplateMatchRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, out.CapturePageID, pageStatus); err != nil {
		return TemplateMatchRun{}, err
	}
	if err = aggregateTemplateMatchPageBatchInTx(ctx, tx, tenantID, out.CapturePageID); err != nil {
		return TemplateMatchRun{}, err
	}
	return out, nil
}

func aggregateTemplateMatchPageBatchInTx(ctx context.Context, tx *sql.Tx, tenantID, capturePageID string) error {
	var batchID string
	if err := tx.QueryRowContext(ctx, `SELECT capture_batch_id::text FROM capture_page WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, capturePageID).Scan(&batchID); err != nil {
		return err
	}
	return aggregateBatchTx(ctx, tx, tenantID, batchID)
}

func (s *PostgresStore) SubmitTemplateMatchFailureCommand(ctx context.Context, tenantID, runID string, input TemplateMatchFailureInput) (TemplateMatchRun, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TemplateMatchRun{}, "", err
	}
	defer tx.Rollback()
	task, err := workerruntime.FailTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		return TemplateMatchRun{}, "", err
	}
	out, err := applyTemplateMatchFailureInTx(ctx, tx, tenantID, runID, input.ErrorCode, input.ErrorDetail, task.Status == workerruntime.StatusQueued)
	if err != nil {
		return TemplateMatchRun{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return TemplateMatchRun{}, "", err
	}
	return out, task.Status, nil
}
