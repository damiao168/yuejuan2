package capture

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"errors"
	"strconv"
)

const (
	fileDownloadRoutePrefix = "/api/v1/files/"
	fileDownloadRouteSuffix = "/download"
)

type templateLayout struct {
	Pages []templateLayoutPage `json:"pages"`
}

type templateLayoutPage struct {
	PageNo          int                          `json:"page_no"`
	Width           int                          `json:"width"`
	Height          int                          `json:"height"`
	QuestionRegions []map[string]any             `json:"question_regions"`
	GradingGroups   []paper.TemplateGradingGroup `json:"grading_groups,omitempty"`
}

type registrationFallbackTemplate struct {
	ID          string
	ContentHash string
	Name        string
	VersionNo   int
	AssetID     string
	ContentType string
	Layout      templateLayout
}

type registrationPageRow struct {
	id, submissionPageID, assetID, hash string
	templateID, templateHash            string
	pageNo, revision                    int
	explicitPageNo                      bool
	identity                            registrationPageIdentity
}

type registrationPageIdentity struct {
	PageNoSource        string `json:"page_no_source"`
	MatchedTemplateID   string `json:"matched_template_id"`
	MatchedTemplateHash string `json:"matched_template_content_hash"`
	MatchedSourceHash   string `json:"matched_source_sha256"`
	MatchedPageNo       int    `json:"matched_page_no"`
}

type registrationQueueScope struct {
	tenantID, examID, batchID, actorID string
}

type registrationTemplateBinding struct {
	templateID, contentHash, routingMode string
	exists                               bool
}

type registrationQueueTemplate struct {
	id, contentHash, assetID, contentType, routingMode string
	layout                                             templateLayout
}

func (s *PostgresStore) QueueSubmissionPages(ctx context.Context, tenantID, submissionID, actorID string) ([]RegistrationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := QueueSubmissionPagesInTx(ctx, tx, tenantID, submissionID, actorID)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// QueueSubmissionPagesInTx 在调用方事务内创建配准记录与运行任务，失败由调用方回滚。
func QueueSubmissionPagesInTx(ctx context.Context, tx *sql.Tx, tenantID, submissionID, actorID string) ([]RegistrationRun, error) {
	if tx == nil || tenantID == "" || submissionID == "" || actorID == "" {
		return nil, ErrInvalidInput
	}
	var examID, batchID string
	var err error
	if err = tx.QueryRowContext(ctx, `SELECT s.exam_id::text,cp.capture_batch_id::text FROM submission s JOIN capture_page cp ON cp.tenant_id=s.tenant_id AND cp.submission_id=s.id AND cp.deleted_at IS NULL WHERE s.tenant_id=$1 AND s.id=$2::uuid AND s.deleted_at IS NULL LIMIT 1`, tenantID, submissionID).Scan(&examID, &batchID); err != nil {
		return nil, mapNotFound(err)
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return nil, err
	}
	// 未组齐的页不能进入配准；否则单独一张背面也可能被当成完整答卷。
	var waiting bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM capture_page WHERE tenant_id=$1 AND submission_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' AND page_identity->>'grouping_status' IN ('waiting_files','incomplete_pages','identity_conflict'))`, tenantID, submissionID).Scan(&waiting); err != nil {
		return nil, err
	}
	if waiting {
		return []RegistrationRun{}, nil
	}
	scope := registrationQueueScope{tenantID: tenantID, examID: examID, batchID: batchID, actorID: actorID}
	binding, err := loadRegistrationBindingTx(ctx, tx, scope, submissionID)
	if err != nil {
		return nil, err
	}
	pages, err := loadRegistrationPagesTx(ctx, tx, tenantID, submissionID, binding.exists)
	if err != nil {
		return nil, err
	}
	template, matchingQueued, err := prepareRegistrationTemplateTx(ctx, tx, scope, binding, pages)
	if err != nil {
		return nil, err
	}
	if matchingQueued {
		return []RegistrationRun{}, nil
	}
	fallbackTemplates, err := loadRegistrationFallbackTemplatesTx(ctx, tx, scope, template)
	if err != nil {
		return nil, err
	}
	out, err := queueRegistrationPagesTx(ctx, tx, scope, template, pages, fallbackTemplates)
	if err != nil {
		return nil, err
	}
	if err = aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return nil, err
	}
	return out, nil
}

func queueRegistrationPagesTx(ctx context.Context, tx *sql.Tx, scope registrationQueueScope, template registrationQueueTemplate, pages []registrationPageRow, fallbackTemplates []registrationFallbackTemplate) ([]RegistrationRun, error) {
	out := []RegistrationRun{}
	for _, page := range pages {
		run, queueErr := queueRegistrationPageTx(ctx, tx, scope, template, page, fallbackTemplates)
		if queueErr != nil {
			return nil, queueErr
		}
		if run.ID != "" {
			out = append(out, run)
		}
	}
	if len(out) == 0 {
		return nil, ErrInvalidTransition
	}
	return out, nil
}

func loadRegistrationBindingTx(ctx context.Context, tx *sql.Tx, scope registrationQueueScope, submissionID string) (registrationTemplateBinding, error) {
	binding := registrationTemplateBinding{exists: true}
	bindingErr := tx.QueryRowContext(ctx, `SELECT template_id::text,template_content_hash,mode FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid FOR SHARE`, scope.tenantID, scope.examID).Scan(&binding.templateID, &binding.contentHash, &binding.routingMode)
	if errors.Is(bindingErr, sql.ErrNoRows) {
		binding.exists = false
	} else if bindingErr != nil {
		return registrationTemplateBinding{}, bindingErr
	}
	if !binding.exists {
		var pendingTemplateReview bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1
FROM capture_page cp
JOIN LATERAL (
  SELECT processing_status,decision
  FROM page_template_match_run pm
  WHERE pm.tenant_id=cp.tenant_id AND pm.capture_page_id=cp.id
    AND pm.source_page_revision=cp.revision AND pm.deleted_at IS NULL
  ORDER BY pm.created_at DESC LIMIT 1
) latest ON true
WHERE cp.tenant_id=$1::uuid AND cp.submission_id=$2::uuid AND cp.status='needs_review'
  AND (latest.processing_status='terminal_error' OR latest.decision IN ('ambiguous','unknown','conflict'))
)`, scope.tenantID, submissionID).Scan(&pendingTemplateReview); err != nil {
			return registrationTemplateBinding{}, err
		}
		if pendingTemplateReview {
			return registrationTemplateBinding{}, ErrInvalidTransition
		}
	}
	return binding, nil
}

func loadRegistrationPagesTx(ctx context.Context, tx *sql.Tx, tenantID, submissionID string, bindingExists bool) ([]registrationPageRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT cp.id::text,cp.submission_page_id::text,sp.normalized_file_asset_id::text,cp.assigned_page_no,cp.revision,fa.hash_sha256,
	COALESCE(sheet.template_id::text,cp.barcode_template_id::text,''),COALESCE(sheet.template_content_hash,''),
	COALESCE(cp.manual_override ? 'page_no' OR cp.page_identity->>'barcode_status'='verified',false),cp.page_identity
	FROM capture_page cp
	JOIN submission_page sp ON sp.tenant_id=cp.tenant_id AND sp.id=cp.submission_page_id
	JOIN file_asset fa ON fa.tenant_id=sp.tenant_id AND fa.id=sp.normalized_file_asset_id
	LEFT JOIN answer_sheet_print_sheet sheet ON sheet.tenant_id=cp.tenant_id AND sheet.id=cp.sheet_serial
	WHERE cp.tenant_id=$1 AND cp.submission_id=$2::uuid
	  AND (cp.status='normalized' OR ($3::boolean AND cp.status='needs_review' AND EXISTS(
	    SELECT 1 FROM page_template_match_run pm
	    WHERE pm.tenant_id=cp.tenant_id AND pm.capture_page_id=cp.id
	      AND pm.source_page_revision=cp.revision AND pm.deleted_at IS NULL
	      AND (pm.processing_status='terminal_error' OR pm.decision IN ('ambiguous','unknown','conflict'))
	      AND pm.id=(SELECT latest.id FROM page_template_match_run latest WHERE latest.tenant_id=cp.tenant_id AND latest.capture_page_id=cp.id AND latest.deleted_at IS NULL ORDER BY latest.created_at DESC LIMIT 1)
	  )))
	  AND sp.quality_status='passed' AND sp.normalized_file_asset_id IS NOT NULL
	  AND cp.deleted_at IS NULL ORDER BY cp.sequence_no FOR UPDATE OF cp`, tenantID, submissionID, bindingExists)
	if err != nil {
		return nil, err
	}
	pages := []registrationPageRow{}
	for rows.Next() {
		var p registrationPageRow
		var identityRaw []byte
		if err = rows.Scan(&p.id, &p.submissionPageID, &p.assetID, &p.pageNo, &p.revision, &p.hash, &p.templateID, &p.templateHash, &p.explicitPageNo, &identityRaw); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(identityRaw, &p.identity); err != nil {
			rows.Close()
			return nil, err
		}
		pages = append(pages, p)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, ErrNotFound
	}
	return pages, nil
}

// 带模板身份的页面必须同时给出模板和内容哈希，且彼此一致；无模板身份的页面不参与此比较。
func controlledRegistrationTemplate(pages []registrationPageRow) (templateID, templateHash string, controlled bool, err error) {
	for _, page := range pages {
		if page.templateID == "" && page.templateHash == "" {
			continue
		}
		if page.templateID == "" || page.templateHash == "" {
			return "", "", false, ErrInvalidTransition
		}
		if !controlled {
			templateID, templateHash, controlled = page.templateID, page.templateHash, true
		} else if page.templateID != templateID || page.templateHash != templateHash {
			return "", "", false, ErrInvalidTransition
		}
	}
	return templateID, templateHash, controlled, nil
}

func prepareRegistrationTemplateTx(ctx context.Context, tx *sql.Tx, scope registrationQueueScope, binding registrationTemplateBinding, pages []registrationPageRow) (registrationQueueTemplate, bool, error) {
	template, err := loadRegistrationTemplateTx(ctx, tx, scope, binding, pages)
	if err != nil {
		// 多模板或无模板时先建立匹配任务，返回空配准列表，等待匹配结果继续排队。
		if template.routingMode == "single_template" && errors.Is(err, sql.ErrNoRows) {
			for _, page := range pages {
				if _, matchErr := queueTemplateMatchRun(ctx, tx, scope.tenantID, scope.examID, scope.actorID, page); matchErr != nil {
					return registrationQueueTemplate{}, false, matchErr
				}
			}
			if err = aggregateBatchTx(ctx, tx, scope.tenantID, scope.batchID); err != nil {
				return registrationQueueTemplate{}, false, err
			}
			return template, true, nil
		}
		return registrationQueueTemplate{}, false, mapNotFound(err)
	}
	matchingQueued := false
	for _, page := range pages {
		// 文件内页序只定位解码图片；即使考试已绑定模板，也要先识别没有受控条码或人工页码的答题卡页面。
		if registrationPageNumberResolved(page, template) {
			continue
		}
		if _, err = queueTemplateMatchRun(ctx, tx, scope.tenantID, scope.examID, scope.actorID, page); err != nil {
			return registrationQueueTemplate{}, false, err
		}
		matchingQueued = true
	}
	if matchingQueued {
		if err = aggregateBatchTx(ctx, tx, scope.tenantID, scope.batchID); err != nil {
			return registrationQueueTemplate{}, false, err
		}
	}
	return template, matchingQueued, nil
}

func registrationPageNumberResolved(page registrationPageRow, template registrationQueueTemplate) bool {
	return page.explicitPageNo || (page.identity.PageNoSource == "visual_template_match" &&
		page.identity.MatchedPageNo == page.pageNo && page.identity.MatchedTemplateID == template.id &&
		page.identity.MatchedTemplateHash == template.contentHash && page.identity.MatchedSourceHash == page.hash)
}

func loadRegistrationTemplateTx(ctx context.Context, tx *sql.Tx, scope registrationQueueScope, binding registrationTemplateBinding, pages []registrationPageRow) (registrationQueueTemplate, error) {
	templateID, templateHash, controlled, err := controlledRegistrationTemplate(pages)
	if err != nil {
		return registrationQueueTemplate{}, err
	}
	tenantID, examID := scope.tenantID, scope.examID
	var templateAssetID, templateContentType string
	routingMode := "barcode"
	var layoutRaw []byte
	// 已验证的实体纸张模板优先，其次是考试绑定；都没有时仅允许唯一锁定模板直接配准。
	if controlled {
		err = tx.QueryRowContext(ctx, `SELECT ast.id::text,ast.content_hash,ast.layout,ep.file_asset_id::text,fa.content_type
FROM answer_sheet_template ast JOIN exam_paper ep ON ep.tenant_id=ast.tenant_id AND ep.id=ast.exam_paper_id JOIN file_asset fa ON fa.tenant_id=ep.tenant_id AND fa.id=ep.file_asset_id
WHERE ast.tenant_id=$1 AND ast.exam_id=$2::uuid AND ast.id=$3::uuid AND ast.content_hash=$4 AND ast.status='locked' AND ast.deleted_at IS NULL`, tenantID, examID, templateID, templateHash).Scan(&templateID, &templateHash, &layoutRaw, &templateAssetID, &templateContentType)
	} else {
		if binding.exists {
			routingMode = binding.routingMode
			err = tx.QueryRowContext(ctx, `SELECT ast.id::text,ast.content_hash,ast.layout,ep.file_asset_id::text,fa.content_type
FROM answer_sheet_template ast JOIN exam_paper ep ON ep.tenant_id=ast.tenant_id AND ep.id=ast.exam_paper_id JOIN file_asset fa ON fa.tenant_id=ep.tenant_id AND fa.id=ep.file_asset_id
WHERE ast.tenant_id=$1 AND ast.exam_id=$2::uuid AND ast.id=$3::uuid AND ast.content_hash=$4 AND ast.status='locked' AND ast.deleted_at IS NULL`, tenantID, examID, binding.templateID, binding.contentHash).Scan(&templateID, &templateHash, &layoutRaw, &templateAssetID, &templateContentType)
		} else {
			routingMode = "single_template"
			err = tx.QueryRowContext(ctx, `WITH locked AS (SELECT ast.* FROM answer_sheet_template ast WHERE ast.tenant_id=$1 AND ast.exam_id=$2::uuid AND ast.status='locked' AND ast.deleted_at IS NULL)
SELECT ast.id::text,ast.content_hash,ast.layout,ep.file_asset_id::text,fa.content_type FROM locked ast JOIN exam_paper ep ON ep.tenant_id=ast.tenant_id AND ep.id=ast.exam_paper_id JOIN file_asset fa ON fa.tenant_id=ep.tenant_id AND fa.id=ep.file_asset_id WHERE (SELECT COUNT(*) FROM locked)=1`, tenantID, examID).Scan(&templateID, &templateHash, &layoutRaw, &templateAssetID, &templateContentType)
		}
	}
	template := registrationQueueTemplate{id: templateID, contentHash: templateHash, assetID: templateAssetID, contentType: templateContentType, routingMode: routingMode}
	if err != nil {
		return template, err
	}
	if json.Unmarshal(layoutRaw, &template.layout) != nil || len(template.layout.Pages) == 0 {
		return registrationQueueTemplate{}, ErrInvalidInput
	}
	return template, nil
}

func loadRegistrationFallbackTemplatesTx(ctx context.Context, tx *sql.Tx, scope registrationQueueScope, template registrationQueueTemplate) ([]registrationFallbackTemplate, error) {
	// 绑定模板的路由可附带少量候选模板供算法比较；候选只是建议，最终绑定仍由事务校验。
	fallbackTemplates := []registrationFallbackTemplate{}
	if template.routingMode != "bound_auto" && template.routingMode != "locked_with_guard" {
		return fallbackTemplates, nil
	}
	fallbackRows, fallbackErr := tx.QueryContext(ctx, `SELECT ast.id::text,ast.content_hash,ast.name,ast.version_no,ast.layout,ep.file_asset_id::text,fa.content_type
FROM answer_sheet_template ast
JOIN exam_paper ep ON ep.tenant_id=ast.tenant_id AND ep.id=ast.exam_paper_id
JOIN file_asset fa ON fa.tenant_id=ep.tenant_id AND fa.id=ep.file_asset_id
WHERE ast.tenant_id=$1::uuid AND ast.exam_id=$2::uuid AND ast.status='locked' AND ast.id<>$3::uuid AND ast.deleted_at IS NULL
ORDER BY ast.version_no DESC LIMIT 7`, scope.tenantID, scope.examID, template.id)
	if fallbackErr != nil {
		return nil, fallbackErr
	}
	for fallbackRows.Next() {
		var candidate registrationFallbackTemplate
		var candidateLayout []byte
		if fallbackErr = fallbackRows.Scan(&candidate.ID, &candidate.ContentHash, &candidate.Name, &candidate.VersionNo, &candidateLayout, &candidate.AssetID, &candidate.ContentType); fallbackErr != nil {
			fallbackRows.Close()
			return nil, fallbackErr
		}
		if json.Unmarshal(candidateLayout, &candidate.Layout) == nil && len(candidate.Layout.Pages) > 0 {
			fallbackTemplates = append(fallbackTemplates, candidate)
		}
	}
	if fallbackErr = fallbackRows.Err(); fallbackErr != nil {
		fallbackRows.Close()
		return nil, fallbackErr
	}
	if fallbackErr = fallbackRows.Close(); fallbackErr != nil {
		return nil, fallbackErr
	}
	return fallbackTemplates, nil
}

func registrationTemplatePage(layout templateLayout, pageNo int) *templateLayoutPage {
	for i := range layout.Pages {
		if layout.Pages[i].PageNo == pageNo {
			return &layout.Pages[i]
		}
	}
	return nil
}

func queueRegistrationPageTx(ctx context.Context, tx *sql.Tx, scope registrationQueueScope, template registrationQueueTemplate, page registrationPageRow, fallbackTemplates []registrationFallbackTemplate) (RegistrationRun, error) {
	tenantID, examID, actorID := scope.tenantID, scope.examID, scope.actorID
	templateID, templateHash, routingMode := template.id, template.contentHash, template.routingMode
	templateAssetID, templateContentType := template.assetID, template.contentType
	templatePage := registrationTemplatePage(template.layout, page.pageNo)
	if templatePage == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE capture_page SET status='needs_review',updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, page.id); err != nil {
			return RegistrationRun{}, err
		}
		return RegistrationRun{}, nil
	}
	if existing, findErr := scanRegistrationRun(tx.QueryRowContext(ctx, `SELECT `+registrationColumns+` FROM page_registration_run WHERE tenant_id=$1 AND capture_page_id=$2::uuid AND source_sha256=$3 AND template_content_hash=$4 AND page_no=$5 AND processing_status IN ('processing','completed') AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`, tenantID, page.id, page.hash, templateHash, page.pageNo)); findErr == nil {
		return existing, nil
	} else if !errors.Is(findErr, ErrNotFound) {
		return RegistrationRun{}, findErr
	}
	var runID string
	err := tx.QueryRowContext(ctx, `INSERT INTO page_registration_run (tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,template_id,template_content_hash,page_no,routing_mode,processing_status,profile_version,started_at)
VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9,'processing','opencv-orb-akaze-v1',now()) RETURNING id::text`, tenantID, page.id, page.submissionPageID, page.assetID, page.hash, templateID, templateHash, page.pageNo, routingMode).Scan(&runID)
	if err != nil {
		return RegistrationRun{}, err
	}
	fallbackPayload := registrationFallbackPayload(fallbackTemplates, page.pageNo)
	payload, _ := json.Marshal(map[string]any{
		"registration_run_id": runID, "capture_page_id": page.id, "exam_id": examID,
		"source_file_asset_id": page.assetID, "source_download_url": fileDownloadRoutePrefix + page.assetID + fileDownloadRouteSuffix, "source_content_type": "image/png",
		"template_id": templateID, "template_content_hash": templateHash, "template_file_asset_id": templateAssetID, "template_download_url": fileDownloadRoutePrefix + templateAssetID + fileDownloadRouteSuffix, "template_content_type": templateContentType,
		"page_no": page.pageNo, "template_page_index": page.pageNo, "template_width": templatePage.Width, "template_height": templatePage.Height, "question_regions": templatePage.QuestionRegions, "grading_groups": templatePage.GradingGroups, "render_dpi": 300,
		"routing_mode":       routingMode,
		"fallback_templates": fallbackPayload,
	})
	key := "page-registration:" + page.id + ":" + templateHash + ":r" + strconv.Itoa(page.revision)
	var taskID string
	err = tx.QueryRowContext(ctx, `INSERT INTO agent_worker_task (tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by)
VALUES ($1,'page_registration','page-processing','page_registration_run',$2::uuid,60,$3,'page-registration-v1',$4,$4,3,10,$5::uuid) RETURNING id::text`, tenantID, runID, payload, key, actorID).Scan(&taskID)
	if err != nil {
		return RegistrationRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE page_registration_run SET runtime_task_id=$3::uuid,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID, taskID); err != nil {
		return RegistrationRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status='registration',updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, page.id); err != nil {
		return RegistrationRun{}, err
	}
	run, scanErr := scanRegistrationRun(tx.QueryRowContext(ctx, `SELECT `+registrationColumns+` FROM page_registration_run WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID))
	if scanErr != nil {
		return RegistrationRun{}, scanErr
	}
	return run, nil
}

func registrationFallbackPayload(templates []registrationFallbackTemplate, pageNo int) []map[string]any {
	payload := []map[string]any{}
	for _, candidate := range templates {
		candidatePage := registrationTemplatePage(candidate.Layout, pageNo)
		if candidatePage == nil {
			continue
		}
		payload = append(payload, map[string]any{
			"template_id": candidate.ID, "template_content_hash": candidate.ContentHash,
			"template_name": candidate.Name, "version_no": candidate.VersionNo,
			"template_download_url": fileDownloadRoutePrefix + candidate.AssetID + fileDownloadRouteSuffix, "template_content_type": candidate.ContentType,
			"template_page_index": candidatePage.PageNo, "question_regions": candidatePage.QuestionRegions, "grading_groups": candidatePage.GradingGroups,
		})
	}
	return payload
}

func (s *PostgresStore) GetRegistrationRun(ctx context.Context, tenantID, runID string) (RegistrationRun, error) {
	return scanRegistrationRun(s.db.QueryRowContext(ctx, `SELECT `+registrationColumns+` FROM page_registration_run WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, runID))
}

func (s *PostgresStore) ListRegistrationRuns(ctx context.Context, tenantID, submissionPageID string) ([]RegistrationRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+registrationColumns+` FROM page_registration_run WHERE tenant_id=$1 AND submission_page_id=$2::uuid AND deleted_at IS NULL ORDER BY created_at DESC`, tenantID, submissionPageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RegistrationRun{}
	for rows.Next() {
		item, scanErr := scanRegistrationRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ApplyRegistrationResult(ctx context.Context, tenantID, runID string, input RegistrationResultInput) (RegistrationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationRun{}, err
	}
	defer tx.Rollback()
	out, err := s.applyRegistrationResultInTx(ctx, tx, tenantID, runID, input)
	if err != nil {
		return RegistrationRun{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) applyRegistrationResultInTx(ctx context.Context, tx *sql.Tx, tenantID, runID string, input RegistrationResultInput) (RegistrationRun, error) {
	current, err := scanRegistrationRun(tx.QueryRowContext(ctx, `SELECT `+registrationColumns+` FROM page_registration_run WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID))
	if err != nil {
		return RegistrationRun{}, err
	}
	if current.ProcessingStatus == "completed" {
		return current, nil
	}
	if current.ProcessingStatus != "processing" && current.ProcessingStatus != "retryable_error" {
		return RegistrationRun{}, ErrInvalidTransition
	}
	var submissionID string
	if err = tx.QueryRowContext(ctx, `SELECT submission_id::text FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.CapturePageID).Scan(&submissionID); err != nil {
		return RegistrationRun{}, err
	}
	for _, segment := range input.Segments {
		if segment.QuestionID == "" || segment.FileAssetID == "" || segment.SHA256 == "" {
			return RegistrationRun{}, ErrInvalidInput
		}
		var questionNo string
		if err = tx.QueryRowContext(ctx, `SELECT question_no FROM question WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, segment.QuestionID).Scan(&questionNo); err != nil {
			return RegistrationRun{}, mapNotFound(err)
		}
		normalized, _ := json.Marshal(segment.NormalizedBBox)
		pixels, _ := json.Marshal(segment.PixelBBox)
		status := "generated"
		if input.Confidence < 0.75 {
			status = "needs_manual_review"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO answer_segment (tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,template_id,template_content_hash,registration_run_id,normalized_bbox,pixel_bbox,crop_file_asset_id,crop_sha256,question_version,processing_status,confidence)
VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5,$6,'configured_answer_area',$7,$8::uuid,$9,$10::uuid,$6,$11,$12::uuid,$13,1,'completed',$14)
ON CONFLICT (tenant_id,submission_id,question_id) DO UPDATE SET submission_page_id=EXCLUDED.submission_page_id,bbox=EXCLUDED.bbox,status=EXCLUDED.status,template_id=EXCLUDED.template_id,template_content_hash=EXCLUDED.template_content_hash,registration_run_id=EXCLUDED.registration_run_id,normalized_bbox=EXCLUDED.normalized_bbox,pixel_bbox=EXCLUDED.pixel_bbox,crop_file_asset_id=EXCLUDED.crop_file_asset_id,crop_sha256=EXCLUDED.crop_sha256,question_version=EXCLUDED.question_version,processing_status='completed',confidence=EXCLUDED.confidence,deleted_at=NULL,updated_at=now()`, tenantID, submissionID, current.SubmissionPageID, segment.QuestionID, questionNo, normalized, status, current.TemplateID, current.TemplateContentHash, current.ID, pixels, segment.FileAssetID, segment.SHA256, input.Confidence)
		if err != nil {
			return RegistrationRun{}, err
		}
	}
	// 配准计算完成不代表可直接使用；低于 0.75 的结果保留切片，但页面仍需人工确认。
	matchStatus := "matched"
	pageStatus := "ready"
	if input.Confidence < 0.75 {
		matchStatus = "needs_review"
		pageStatus = "needs_review"
	}
	sourceMatrix, _ := json.Marshal(input.SourceToTemplate)
	inverseMatrix, _ := json.Marshal(input.TemplateToSource)
	guardReport, _ := json.Marshal(input.GuardReport)
	row := tx.QueryRowContext(ctx, `UPDATE page_registration_run SET processing_status='completed',match_status=$3,confidence=$4,method=$5,source_to_template_matrix=$6,template_to_source_matrix=$7,feature_count=$8,match_count=$9,inlier_count=$10,inlier_ratio=$11,reprojection_error=$12,registered_file_asset_id=$13::uuid,result_version=$14,duration_ms=$15,guard_report=$16::jsonb,completed_at=now(),error_code=NULL,error_detail='{}',updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+registrationColumns, tenantID, runID, matchStatus, input.Confidence, input.Method, sourceMatrix, inverseMatrix, input.FeatureCount, input.MatchCount, input.InlierCount, input.InlierRatio, input.ReprojectionError, input.RegisteredFileAssetID, input.ResultVersion, input.DurationMS, guardReport)
	out, err := scanRegistrationRun(row)
	if err != nil {
		return RegistrationRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status=$3,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.CapturePageID, pageStatus); err != nil {
		return RegistrationRun{}, err
	}
	// 配准回调与其它入口共用聚合；页面就绪不能掩盖同批文件失败或学生身份冲突。
	var batchID string
	if err = tx.QueryRowContext(ctx, `SELECT capture_batch_id::text FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.CapturePageID).Scan(&batchID); err != nil {
		return RegistrationRun{}, err
	}
	if err = aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationRun{}, err
	}
	return out, nil
}

func (s *PostgresStore) ApplyRegistrationFailure(ctx context.Context, tenantID, runID, errorCode string, errorDetail map[string]any, retryable bool) (RegistrationRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationRun{}, err
	}
	defer tx.Rollback()
	status := "terminal_error"
	pageStatus := "needs_review"
	if retryable {
		status = "retryable_error"
		pageStatus = "registration"
	}
	detail, _ := json.Marshal(errorDetail)
	out, err := scanRegistrationRun(tx.QueryRowContext(ctx, `UPDATE page_registration_run SET processing_status=$3,error_code=$4,error_detail=$5,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL RETURNING `+registrationColumns, tenantID, runID, status, errorCode, detail))
	if err != nil {
		return RegistrationRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status=$3,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, out.CapturePageID, pageStatus); err != nil {
		return RegistrationRun{}, err
	}
	var batchID string
	if err = tx.QueryRowContext(ctx, `SELECT capture_batch_id::text FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, out.CapturePageID).Scan(&batchID); err != nil {
		return RegistrationRun{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationRun{}, err
	}
	return out, tx.Commit()
}

const registrationColumns = `id::text,capture_page_id::text,submission_page_id::text,source_file_asset_id::text,template_id::text,template_content_hash,routing_mode,guard_report,page_no,processing_status,COALESCE(match_status,''),COALESCE(confidence,0),COALESCE(method,''),profile_version,source_to_template_matrix,template_to_source_matrix,feature_count,match_count,inlier_count,COALESCE(inlier_ratio,0),COALESCE(reprojection_error,0),COALESCE(registered_file_asset_id::text,''),COALESCE(runtime_task_id::text,''),COALESCE(error_code,''),created_at`

func scanRegistrationRun(row scanner) (RegistrationRun, error) {
	var item RegistrationRun
	var source, inverse []byte
	var guard []byte
	err := row.Scan(&item.ID, &item.CapturePageID, &item.SubmissionPageID, &item.SourceFileAssetID, &item.TemplateID, &item.TemplateContentHash, &item.RoutingMode, &guard, &item.PageNo, &item.ProcessingStatus, &item.MatchStatus, &item.Confidence, &item.Method, &item.ProfileVersion, &source, &inverse, &item.FeatureCount, &item.MatchCount, &item.InlierCount, &item.InlierRatio, &item.ReprojectionError, &item.RegisteredFileAssetID, &item.RuntimeTaskID, &item.ErrorCode, &item.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RegistrationRun{}, ErrNotFound
		}
		return RegistrationRun{}, err
	}
	_ = json.Unmarshal(source, &item.SourceToTemplate)
	_ = json.Unmarshal(inverse, &item.TemplateToSource)
	_ = json.Unmarshal(guard, &item.GuardReport)
	if item.GuardReport == nil {
		item.GuardReport = map[string]any{}
	}
	if item.SourceToTemplate == nil {
		item.SourceToTemplate = []any{}
	}
	if item.TemplateToSource == nil {
		item.TemplateToSource = []any{}
	}
	return item, nil
}
