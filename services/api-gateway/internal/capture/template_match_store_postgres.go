package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
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
ORDER BY (ast.id=(SELECT template_id FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid)) DESC NULLS LAST,ast.version_no DESC LIMIT 8`, tenantID, examID)
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
		for _, candidatePage := range templateMatchCandidatePages(layout, page) {
			candidates = append(candidates, map[string]any{
				"template_id": id, "template_content_hash": contentHash,
				"template_name": name, "version_no": versionNo,
				"template_download_url": "/api/v1/files/" + assetID + "/download", "template_content_type": contentType,
				"template_page_index": candidatePage.PageNo, "question_regions": candidatePage.QuestionRegions, "grading_groups": candidatePage.GradingGroups,
			})
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
	queuedCandidates, _ := json.Marshal(candidates)
	err = tx.QueryRowContext(ctx, `INSERT INTO page_template_match_run (tenant_id,exam_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,source_page_revision,created_by,candidates) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::uuid,$9::jsonb) RETURNING id::text`, tenantID, examID, page.id, page.submissionPageID, page.assetID, page.hash, page.revision, actorID, queuedCandidates).Scan(&runID)
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

func templateMatchCandidatePages(layout templateLayout, page registrationPageRow) []templateLayoutPage {
	// 单张 PNG 的 source_index=1 不是答题卡页码；只有条码或人工明确页码时才缩小候选。
	if !page.explicitPageNo {
		return layout.Pages
	}
	if candidate := registrationTemplatePage(layout, page.pageNo); candidate != nil {
		return []templateLayoutPage{*candidate}
	}
	return nil
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
	page, sourceCurrent, err := loadTemplateMatchSourceTx(ctx, tx, tenantID, current)
	if err != nil {
		return TemplateMatchRun{}, nil, err
	}
	decision := input.Decision
	selectedPageNo := 0
	if !sourceCurrent {
		// 质检重跑、人工操作或替换图片后，旧任务只留结果收据，不能覆盖新页面状态或绑定模板。
		decision = "unknown"
	} else if decision == "matched" {
		selectedPageNo = selectedTemplateMatchPage(current.Candidates, input)
		if selectedPageNo == 0 || (page.explicitPageNo && selectedPageNo != page.pageNo) {
			decision = "unknown"
		}
	}
	if decision == "matched" {
		var occupied bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM submission_page WHERE tenant_id=$1::uuid AND submission_id=(SELECT submission_id FROM submission_page WHERE tenant_id=$1::uuid AND id=$2::uuid) AND page_no=$3 AND id<>$2::uuid)`, tenantID, current.SubmissionPageID, selectedPageNo).Scan(&occupied); err != nil {
			return TemplateMatchRun{}, nil, err
		}
		// 同一提交已有该页时交给人工整理，不挪动其他页，更不能据此合并不同学生的 PNG。
		if occupied {
			decision = "conflict"
		}
	}
	if decision == "matched" {
		decision, err = bindMatchedTemplateInTx(ctx, tx, tenantID, current, input)
		if err != nil {
			return TemplateMatchRun{}, nil, err
		}
	}
	candidates, _ := json.Marshal(input.Candidates)
	selectedID := input.SelectedTemplateID
	selectedHash := input.SelectedTemplateContentHash
	if decision != "matched" {
		selectedID, selectedHash = "", ""
	}
	row := tx.QueryRowContext(ctx, `UPDATE page_template_match_run SET processing_status='completed',decision=$3,selected_template_id=NULLIF($4,'')::uuid,selected_template_content_hash=NULLIF($5,''),score=$6,margin=$7,candidates=$8::jsonb,result_version=$9,duration_ms=$10,completed_at=now(),error_code=NULL,error_detail='{}'::jsonb,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid RETURNING `+templateMatchColumns, tenantID, runID, decision, selectedID, selectedHash, input.Score, input.Margin, candidates, input.ResultVersion, input.DurationMS)
	out, err := scanTemplateMatchRun(row)
	if err != nil {
		return TemplateMatchRun{}, nil, err
	}
	if !sourceCurrent {
		return out, []RegistrationRun{}, nil
	}
	if decision == "matched" && !page.explicitPageNo {
		if _, err = tx.ExecContext(ctx, `UPDATE submission_page SET page_no=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, current.SubmissionPageID, selectedPageNo); err != nil {
			return TemplateMatchRun{}, nil, err
		}
		// 页码与视觉证据在同一事务写入，再由原配准路径读取；源哈希和模板哈希变化会使这份证据失效。
		if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET assigned_page_no=$3,revision=revision+1,page_identity=page_identity||jsonb_build_object('page_no_source','visual_template_match','matched_page_no',$3::int,'matched_template_id',$4::text,'matched_template_content_hash',$5::text,'matched_source_sha256',$6::text),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, current.CapturePageID, selectedPageNo, selectedID, selectedHash, current.SourceSHA256); err != nil {
			return TemplateMatchRun{}, nil, err
		}
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

func loadTemplateMatchSourceTx(ctx context.Context, tx *sql.Tx, tenantID string, run TemplateMatchRun) (registrationPageRow, bool, error) {
	var page registrationPageRow
	var qualityStatus string
	// 先锁 submission_page 再锁 capture_page，与质检投影的写入顺序一致。
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(sp.normalized_file_asset_id::text,''),COALESCE(fa.hash_sha256,''),sp.quality_status FROM submission_page sp LEFT JOIN file_asset fa ON fa.tenant_id=sp.tenant_id AND fa.id=sp.normalized_file_asset_id WHERE sp.tenant_id=$1::uuid AND sp.id=$2::uuid FOR UPDATE OF sp`, tenantID, run.SubmissionPageID).Scan(&page.assetID, &page.hash, &qualityStatus)
	if err != nil {
		return page, false, err
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT assigned_page_no,revision,status,COALESCE(manual_override ? 'page_no' OR page_identity->>'barcode_status'='verified',false) FROM capture_page WHERE tenant_id=$1::uuid AND id=$2::uuid AND submission_page_id=$3::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, run.CapturePageID, run.SubmissionPageID).Scan(&page.pageNo, &page.revision, &status, &page.explicitPageNo)
	if errors.Is(err, sql.ErrNoRows) {
		return page, false, nil
	}
	return page, err == nil && page.revision == run.SourcePageRevision && page.assetID == run.SourceFileAssetID && page.hash == run.SourceSHA256 && qualityStatus == "passed" && status == "page_matching", err
}

func selectedTemplateMatchPage(queued []map[string]any, input TemplateMatchResultInput) int {
	if input.Score < 0.70 || input.Margin < 0.06 {
		return 0
	}
	for _, evidence := range input.Candidates {
		geometryPassed, _ := evidence["geometry_passed"].(bool)
		score, _ := evidence["score"].(float64)
		pageIndex := templateMatchPageIndex(evidence["template_page_index"])
		if !geometryPassed || pageIndex <= 0 || evidence["template_id"] != input.SelectedTemplateID || evidence["template_content_hash"] != input.SelectedTemplateContentHash || math.Abs(score-input.Score) > 0.000001 {
			continue
		}
		for _, candidate := range queued {
			if candidate["template_id"] == input.SelectedTemplateID && candidate["template_content_hash"] == input.SelectedTemplateContentHash && templateMatchPageIndex(candidate["template_page_index"]) == pageIndex {
				return pageIndex
			}
		}
	}
	return 0
}

func templateMatchPageIndex(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		if number > 0 && number == math.Trunc(number) && number <= float64(math.MaxInt32) {
			return int(number)
		}
	}
	return 0
}

// 同一考试只能保留一个绑定；并发自动匹配选中不同模板时转人工冲突，不覆盖先前绑定。
func bindMatchedTemplateInTx(ctx context.Context, tx *sql.Tx, tenantID string, current TemplateMatchRun, input TemplateMatchResultInput) (string, error) {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM answer_sheet_template WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND id=$3::uuid AND content_hash=$4 AND status='locked' AND deleted_at IS NULL)`, tenantID, current.ExamID, input.SelectedTemplateID, input.SelectedTemplateContentHash).Scan(&valid)
	if err != nil || !valid {
		return "", ErrInvalidInput
	}
	// 自动匹配和管理员命令采用相同的锁顺序与游标；否则自动重建会回到 revision=1，复活旧页面请求。
	if _, err = tx.ExecContext(ctx, `INSERT INTO exam_answer_sheet_template_binding_revision (tenant_id,exam_id,last_revision) VALUES ($1::uuid,$2::uuid,0) ON CONFLICT (tenant_id,exam_id) DO NOTHING`, tenantID, current.ExamID); err != nil {
		return "", err
	}
	var currentRevision int
	if err = tx.QueryRowContext(ctx, `SELECT last_revision FROM exam_answer_sheet_template_binding_revision WHERE tenant_id=$1::uuid AND exam_id=$2::uuid FOR UPDATE`, tenantID, current.ExamID).Scan(&currentRevision); err != nil {
		return "", err
	}
	var boundID, boundHash string
	err = tx.QueryRowContext(ctx, `SELECT template_id::text,template_content_hash FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid FOR UPDATE`, tenantID, current.ExamID).Scan(&boundID, &boundHash)
	if errors.Is(err, sql.ErrNoRows) {
		nextRevision := currentRevision + 1
		if _, err = tx.ExecContext(ctx, `INSERT INTO exam_answer_sheet_template_binding (tenant_id,exam_id,template_id,template_content_hash,mode,source,revision,bound_by) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,'bound_auto','automatic',$6,$5::uuid)`, tenantID, current.ExamID, input.SelectedTemplateID, input.SelectedTemplateContentHash, current.CreatedBy, nextRevision); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE exam_answer_sheet_template_binding_revision SET last_revision=$3,updated_at=now() WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, tenantID, current.ExamID, nextRevision); err != nil {
			return "", err
		}
		return "matched", nil
	}
	if err != nil {
		return "", err
	}
	if boundID != input.SelectedTemplateID || boundHash != input.SelectedTemplateContentHash {
		return "conflict", nil
	}
	return "matched", nil
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
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page cp SET status=$3,updated_at=now() FROM submission_page sp JOIN file_asset fa ON fa.tenant_id=sp.tenant_id AND fa.id=sp.normalized_file_asset_id WHERE cp.tenant_id=$1::uuid AND cp.id=$2::uuid AND cp.revision=$4 AND sp.tenant_id=cp.tenant_id AND sp.id=cp.submission_page_id AND sp.normalized_file_asset_id=$5::uuid AND fa.hash_sha256=$6 AND sp.quality_status='passed' AND cp.status='page_matching'`, tenantID, out.CapturePageID, pageStatus, out.SourcePageRevision, out.SourceFileAssetID, out.SourceSHA256); err != nil {
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
