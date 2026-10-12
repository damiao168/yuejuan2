package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"golang.org/x/text/unicode/norm"
)

func (s *PostgresStore) QueuePaperImportOCR(ctx context.Context, tenantID string, job PaperImportJob, actorID string, assets []PaperImportOCRAsset) error {
	return s.QueuePaperImportDecode(ctx, tenantID, job, actorID, assets, nil)
}

// QueuePaperImportDecode 将来源文件渲染为页面图像。
// 旧名称 QueuePaperImportOCR 保留为兼容包装，但新分发的导入在此阶段后不调度文本或公式 OCR。
func (s *PostgresStore) QueuePaperImportDecode(ctx context.Context, tenantID string, job PaperImportJob, actorID string, assets []PaperImportOCRAsset, baseDocuments []PaperImportParseDocument) error {
	if len(assets) == 0 {
		return ErrInvalidInput
	}
	documents := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		if asset.SourceID == "" || asset.DocumentIndex < 0 || !validPaperImportRole(asset.RoleHint, true) {
			return ErrInvalidInput
		}
		documents = append(documents, map[string]any{
			"source_id": asset.SourceID, "document_index": asset.DocumentIndex, "role_hint": asset.RoleHint, "file_asset_id": asset.FileAssetID,
			"download_url": "/api/v1/files/" + asset.FileAssetID + "/download",
			"content_type": asset.ContentType, "render_dpi": 220, "max_pages": 100,
		})
	}
	sourceRevision := paperImportSourceConfigurationHash(job.Sources)
	payload, _ := json.Marshal(map[string]any{"paper_import_id": job.ID, "exam_id": job.ExamID, "run_id": job.RunID, "generation": job.Generation, "source_revision": sourceRevision, "documents": documents, "parse_documents": baseDocuments})
	idempotencyKey := fmt.Sprintf("paper-import:%s:g:%d:decode:%s", job.ID, job.Generation, contentHash(payload))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, currentRevision, runErr := currentPaperImportRunInTx(ctx, tx, tenantID, job.ID)
	if runErr != nil {
		return runErr
	}
	if generation != job.Generation || runID != job.RunID || currentRevision != sourceRevision {
		return ErrConflict
	}
	if err = validatePaperImportDispatchLease(ctx, tx, tenantID, job); err != nil {
		return err
	}
	if err = lockPaperImportTasksInTx(ctx, tx, tenantID, job.ID); err != nil {
		return err
	}
	var taskID string
	if err = tx.QueryRowContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by,paper_import_run_id,paper_import_generation,task_protocol_version)
VALUES($1,'layout','page-processing','paper_import_job',$2::uuid,55,$3,'paper-import-decode-v3',$4,$4,3,10,$5::uuid,$6::uuid,$7,2)
ON CONFLICT(tenant_id,task_type,idempotency_key) DO UPDATE SET updated_at=agent_worker_task.updated_at
RETURNING id::text`, tenantID, job.ID, payload, idempotencyKey, actorID, runID, generation).Scan(&taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt AS attempt
SET status='cancelled',completed_at=now(),error_code='cancelled'
FROM agent_worker_task AS task
WHERE attempt.tenant_id=$1 AND attempt.task_id=task.id AND attempt.completed_at IS NULL
  AND task.tenant_id=$1 AND task.source_type='paper_import_job' AND task.source_id=$2::uuid
  AND task.id<>$3::uuid AND task.status IN ('queued','leased','running')`, tenantID, job.ID, taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task
SET status='cancelled',cancelled_at=now(),completed_at=now(),updated_at=now(),revision=revision+1
WHERE tenant_id=$1 AND source_type='paper_import_job' AND source_id=$2::uuid
  AND id<>$3::uuid AND status IN ('queued','leased','running')`, tenantID, job.ID, taskID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='queued',dispatch_lease_owner=NULL,dispatch_lease_expires_at=NULL,initial_task_id=COALESCE(initial_task_id,$4::uuid),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, runID, generation, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// 版本摘要只包含来源配置；识别状态或置信度的回写不应让同一批输入变成新版本。
func paperImportSourceConfigurationHash(sources []PaperImportSource) string {
	type sourceConfiguration struct {
		ID            string `json:"id"`
		FileAssetID   string `json:"file_asset_id"`
		DocumentIndex int    `json:"document_index"`
		RoleHint      string `json:"role_hint"`
	}
	ordered := append([]PaperImportSource{}, sources...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].DocumentIndex == ordered[j].DocumentIndex {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].DocumentIndex < ordered[j].DocumentIndex
	})
	configurations := make([]sourceConfiguration, 0, len(ordered))
	for _, source := range ordered {
		configurations = append(configurations, sourceConfiguration{ID: source.ID, FileAssetID: source.FileAssetID, DocumentIndex: source.DocumentIndex, RoleHint: source.RoleHint})
	}
	raw, _ := json.Marshal(configurations)
	return contentHash(raw)
}

func (s *PostgresStore) QueuePaperImportParse(ctx context.Context, tenantID string, job PaperImportJob, actorID string, input PaperImportParseRequest) error {
	if len(input.Documents) == 0 || actorID == "" {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = queuePaperImportParseInTx(ctx, tx, tenantID, job, actorID, input); err != nil {
		return err
	}
	return tx.Commit()
}

// 解析输入、任务和派发状态使用调用方同一事务；任一步失败时不能留下没有任务的已完成上游。
func queuePaperImportParseInTx(ctx context.Context, tx *sql.Tx, tenantID string, job PaperImportJob, actorID string, input PaperImportParseRequest) error {
	if tx == nil || tenantID == "" || job.ID == "" || actorID == "" || len(input.Documents) == 0 {
		return ErrInvalidInput
	}
	current, err := getPaperImportInTx(ctx, tx, tenantID, job.ID)
	if err != nil {
		return err
	}
	runID, generation, persistedRevision, err := currentPaperImportRunInTx(ctx, tx, tenantID, job.ID)
	if err != nil {
		return err
	}
	sourceRevision := paperImportSourceConfigurationHash(current.Sources)
	if current.Status != "processing" || sourceRevision != paperImportSourceConfigurationHash(job.Sources) || persistedRevision != sourceRevision || (job.Generation > 0 && job.Generation != generation) {
		return ErrConflict
	}
	if err = validatePaperImportDispatchLease(ctx, tx, tenantID, job); err != nil {
		return err
	}
	if err = lockPaperImportTasksInTx(ctx, tx, tenantID, job.ID); err != nil {
		return err
	}
	if err = validatePaperImportParseSources(current.Sources, input); err != nil {
		return err
	}
	documents, err := json.Marshal(input.Documents)
	if err != nil {
		return ErrInvalidInput
	}
	persistedPages := input.Pages
	if persistedPages == nil {
		persistedPages = []PaperImportDecodedPage{}
	}
	pages, err := json.Marshal(persistedPages)
	if err != nil {
		return ErrInvalidInput
	}
	issues, err := json.Marshal(input.ExtraIssues)
	if err != nil {
		return ErrInvalidInput
	}
	serializedInput, _ := json.Marshal(input)
	inputHash := contentHash(serializedInput)
	var inputID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO paper_import_parse_input
  (tenant_id,paper_import_id,run_id,generation,source_revision,input_hash,documents,pages,extra_issues,created_by,protocol_version)
VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10::uuid,2)
ON CONFLICT (tenant_id,run_id,input_hash)
DO UPDATE SET input_hash=EXCLUDED.input_hash
RETURNING id::text`, tenantID, job.ID, runID, generation, sourceRevision, inputHash, documents, pages, issues, actorID).Scan(&inputID)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("paper-import:%s:g:%d:parse:%s", job.ID, generation, inputHash)
	task, err := workerruntime.CreateTaskInTx(ctx, tx, tenantID, actorID, workerruntime.CreateTaskInput{
		TaskType: "paper_parse", QueueName: "paper-parse", SourceType: "paper_import_parse", SourceID: inputID,
		Priority: 55, PayloadSchemaVersion: "paper-import-parse-v1", IdempotencyKey: key, DedupeKey: key,
		Payload:     map[string]any{"paper_import_id": job.ID, "parse_input_id": inputID, "source_revision": sourceRevision},
		MaxAttempts: 3, RetryBackoffSeconds: 15,
	})
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE agent_worker_task SET paper_import_run_id=$3::uuid,paper_import_generation=$4::bigint,task_protocol_version=2,payload_schema_version='paper-import-parse-v2',payload=payload || jsonb_build_object('run_id',$3::text,'generation',$4::bigint) WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, task.ID, runID, generation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='queued',dispatch_lease_owner=NULL,dispatch_lease_expires_at=NULL,initial_task_id=COALESCE(initial_task_id,$4::uuid),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, runID, generation, task.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt AS attempt
SET status='cancelled',completed_at=now(),error_code='cancelled'
FROM agent_worker_task AS prior
JOIN paper_import_parse_input AS parse_input
  ON parse_input.tenant_id=prior.tenant_id AND parse_input.id=prior.source_id
WHERE attempt.tenant_id=$1 AND attempt.task_id=prior.id AND attempt.completed_at IS NULL
  AND prior.tenant_id=$1 AND prior.source_type='paper_import_parse'
  AND parse_input.paper_import_id=$2::uuid AND prior.id<>$3::uuid
  AND prior.status IN ('queued','leased','running')`, tenantID, job.ID, task.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task AS prior
SET status='cancelled',cancelled_at=now(),completed_at=now(),updated_at=now(),revision=revision+1
FROM paper_import_parse_input AS parse_input
WHERE prior.tenant_id=$1 AND prior.source_type='paper_import_parse'
  AND parse_input.tenant_id=prior.tenant_id AND parse_input.id=prior.source_id
  AND parse_input.paper_import_id=$2::uuid AND prior.id<>$3::uuid
  AND prior.status IN ('queued','leased','running')`, tenantID, job.ID, task.ID)
	return err
}

// 当前来源在事务内完成锁定后才核对解析输入；文档必须覆盖全部来源，页面按来源和页号去重。
func validatePaperImportParseSources(current []PaperImportSource, input PaperImportParseRequest) error {
	sources := make(map[string]PaperImportSource, len(current))
	for _, source := range current {
		sources[source.ID] = source
	}
	seen := make(map[string]bool, len(input.Documents))
	for _, document := range input.Documents {
		source, ok := sources[document.SourceID]
		if !ok || seen[document.SourceID] || source.FileAssetID != document.FileAssetID || source.DocumentIndex != document.DocumentIndex || strings.TrimSpace(document.Content) == "" {
			return ErrInvalidInput
		}
		seen[document.SourceID] = true
	}
	if len(seen) != len(sources) {
		return ErrInvalidInput
	}
	seenPages := make(map[string]bool, len(input.Pages))
	for _, page := range input.Pages {
		source, ok := sources[page.SourceID]
		pageKey := fmt.Sprintf("%s:%d", page.SourceID, page.PageNo)
		if !ok || source.DocumentIndex != page.DocumentIndex || page.PageNo <= 0 || strings.TrimSpace(page.FileAssetID) == "" || seenPages[pageKey] {
			return ErrInvalidInput
		}
		if digest := strings.ToLower(strings.TrimSpace(page.SHA256)); digest != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
			return ErrInvalidInput
		}
		seenPages[pageKey] = true
	}
	return nil
}

func (s *PostgresStore) CompletePaperImportDecode(ctx context.Context, tenantID, importID string, input PaperImportDecodeResult) error {
	if input.TaskID == "" || input.LeaseToken == "" || len(input.Pages) == 0 {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, _, err := currentPaperImportRunInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, input.TaskID, runID, generation, paperImportTaskStage{taskType: "layout", sourceType: "paper_import_job", sourceID: importID}); err != nil {
		return err
	}
	var taskPayloadJSON []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM agent_worker_task WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, input.TaskID).Scan(&taskPayloadJSON); err != nil {
		return err
	}
	var taskPayload struct {
		ParseDocuments []PaperImportParseDocument `json:"parse_documents"`
	}
	if json.Unmarshal(taskPayloadJSON, &taskPayload) != nil {
		return ErrInvalidInput
	}
	result := mapFromJSON(input)
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "paper-import-decode-result-v3", Result: result, DurationMS: input.DurationMS}); err != nil {
		return err
	}
	var actorID string
	if err = tx.QueryRowContext(ctx, `SELECT created_by::text FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND status='processing' AND deleted_at IS NULL FOR UPDATE`, tenantID, importID).Scan(&actorID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	parseInput, err := buildDecodedVisualParseInput(job, taskPayload.ParseDocuments, input.Pages)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='processed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return err
	}
	if err = queuePaperImportParseInTx(ctx, tx, tenantID, job, actorID, parseInput); err != nil {
		return err
	}
	return tx.Commit()
}

func buildDecodedVisualParseInput(job PaperImportJob, baseDocuments []PaperImportParseDocument, pages []PaperImportDecodedPage) (PaperImportParseRequest, error) {
	if len(job.Sources) == 0 || len(pages) == 0 {
		return PaperImportParseRequest{}, ErrInvalidInput
	}
	sources := make(map[string]PaperImportSource, len(job.Sources))
	for _, source := range job.Sources {
		sources[source.ID] = source
	}
	baseBySource, err := indexDecodedBaseDocuments(sources, baseDocuments)
	if err != nil {
		return PaperImportParseRequest{}, err
	}
	orderedPages, pagesBySource, err := orderDecodedVisualPages(sources, pages)
	if err != nil {
		return PaperImportParseRequest{}, err
	}
	orderedSources := append([]PaperImportSource(nil), job.Sources...)
	sort.SliceStable(orderedSources, func(i, j int) bool { return orderedSources[i].DocumentIndex < orderedSources[j].DocumentIndex })
	documents := make([]PaperImportParseDocument, 0, len(orderedSources))
	for _, source := range orderedSources {
		if pagesBySource[source.ID] > 0 {
			documents = append(documents, visualParseDocument(source.ID, source.FileAssetID, source.DocumentIndex, source.RoleHint))
			continue
		}
		document, ok := baseBySource[source.ID]
		if !ok {
			return PaperImportParseRequest{}, ErrInvalidInput
		}
		documents = append(documents, document)
	}
	return PaperImportParseRequest{Documents: documents, Pages: orderedPages}, nil
}

func indexDecodedBaseDocuments(sources map[string]PaperImportSource, documents []PaperImportParseDocument) (map[string]PaperImportParseDocument, error) {
	indexed := make(map[string]PaperImportParseDocument, len(documents))
	for _, document := range documents {
		source, ok := sources[document.SourceID]
		if !ok || source.FileAssetID != document.FileAssetID || source.DocumentIndex != document.DocumentIndex || strings.TrimSpace(document.Content) == "" {
			return nil, ErrInvalidInput
		}
		if _, duplicate := indexed[document.SourceID]; duplicate {
			return nil, ErrInvalidInput
		}
		indexed[document.SourceID] = document
	}
	return indexed, nil
}

func orderDecodedVisualPages(sources map[string]PaperImportSource, pages []PaperImportDecodedPage) ([]PaperImportDecodedPage, map[string]int, error) {
	counts := make(map[string]int, len(pages))
	seen := make(map[string]bool, len(pages))
	// 先复制再稳定排序，避免完成回执中原有页面顺序被组装步骤改动。
	ordered := append([]PaperImportDecodedPage(nil), pages...)
	for _, page := range ordered {
		source, ok := sources[page.SourceID]
		identity := fmt.Sprintf("%s:%d", page.SourceID, page.PageNo)
		if !ok || source.DocumentIndex != page.DocumentIndex || page.PageNo <= 0 || strings.TrimSpace(page.FileAssetID) == "" || seen[identity] {
			return nil, nil, ErrInvalidInput
		}
		seen[identity] = true
		counts[page.SourceID]++
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].DocumentIndex == ordered[j].DocumentIndex {
			return ordered[i].PageNo < ordered[j].PageNo
		}
		return ordered[i].DocumentIndex < ordered[j].DocumentIndex
	})
	return ordered, counts, nil
}

func (s *PostgresStore) CompletePaperImportOCR(ctx context.Context, tenantID, importID string, input PaperImportOCRResult, parseInput PaperImportParseRequest) error {
	if input.TaskID == "" || input.LeaseToken == "" || len(input.Blocks) == 0 || len(parseInput.Documents) == 0 {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, sourceRevision, err := currentPaperImportRunInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, input.TaskID, runID, generation, paperImportTaskStage{taskType: "ocr", sourceType: "paper_import_job", sourceID: importID}); err != nil {
		return err
	}
	var policyJSON []byte
	var policyHash string
	if err = tx.QueryRowContext(ctx, `SELECT recognition_policy_snapshot,recognition_policy_hash FROM paper_import_run WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID).Scan(&policyJSON, &policyHash); err != nil {
		return err
	}
	var policy PaperRecognitionPolicy
	if json.Unmarshal(policyJSON, &policy) != nil {
		return ErrInvalidInput
	}
	var ocrPayload []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM agent_worker_task WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, input.TaskID).Scan(&ocrPayload); err != nil {
		return err
	}
	var taskPayload struct {
		Pages []PaperImportDecodedPage `json:"pages"`
	}
	if json.Unmarshal(ocrPayload, &taskPayload) != nil || len(taskPayload.Pages) == 0 {
		return ErrInvalidInput
	}
	parseInput.Pages = taskPayload.Pages
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "paper-import-ocr-result-v3", Result: mapFromJSON(input), DurationMS: input.DurationMS}); err != nil {
		return err
	}
	var status, actorID string
	if err = tx.QueryRowContext(ctx, `SELECT status,created_by::text FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, importID).Scan(&status, &actorID); err != nil {
		return err
	}
	if status != "processing" {
		return fmt.Errorf("%w: paper import is %s", ErrConflict, status)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='processed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return err
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if policy.FormulaEnabled {
		documents, _ := json.Marshal(parseInput.Documents)
		issues, _ := json.Marshal(parseInput.ExtraIssues)
		pagesJSON, _ := json.Marshal(parseInput.Pages)
		inputHash := contentHash(pagesJSON, documents, issues, policyJSON)
		var formulaInputID string
		if err = tx.QueryRowContext(ctx, `INSERT INTO paper_import_formula_input(tenant_id,paper_import_id,run_id,generation,source_revision,policy_hash,pages,documents,extra_issues,created_by)
VALUES($1,$2::uuid,$3::uuid,$4,$5,$6,$7::jsonb,$8::jsonb,$9::jsonb,$10::uuid)
ON CONFLICT(tenant_id,run_id) DO UPDATE SET policy_hash=EXCLUDED.policy_hash
RETURNING id::text`, tenantID, importID, runID, generation, sourceRevision, policyHash, pagesJSON, documents, issues, actorID).Scan(&formulaInputID); err != nil {
			return err
		}
		formulaPayload, _ := json.Marshal(map[string]any{"paper_import_id": importID, "exam_id": job.ExamID, "formula_input_id": formulaInputID, "run_id": runID, "generation": generation, "source_revision": sourceRevision, "subject_code": policy.SubjectCode, "recognition_policy": policy, "pages": parseInput.Pages, "input_hash": inputHash})
		key := fmt.Sprintf("paper-import:%s:g:%d:formula:%s", importID, generation, inputHash)
		if _, err = tx.ExecContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by,paper_import_run_id,paper_import_generation,task_protocol_version)
VALUES($1,'paper_formula','paper-formula','paper_import_job',$2::uuid,58,$3,'paper-import-formula-v2',$4,$4,2,30,$5::uuid,$6::uuid,$7,2)
ON CONFLICT(tenant_id,task_type,idempotency_key) DO NOTHING`, tenantID, importID, formulaPayload, key, actorID, runID, generation); err != nil {
			return err
		}
	} else if err = queuePaperImportParseInTx(ctx, tx, tenantID, job, actorID, parseInput); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) CompletePaperImportFormula(ctx context.Context, tenantID, importID string, input PaperImportFormulaResult) error {
	if input.TaskID == "" || input.LeaseToken == "" {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, _, err := currentPaperImportRunInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if err = validatePaperImportStageBinding(ctx, tx, tenantID, input.TaskID, runID, generation, paperImportTaskStage{taskType: "paper_formula", sourceType: "paper_import_job", sourceID: importID}); err != nil {
		return err
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "paper-import-formula-result-v2", Result: mapFromJSON(input), DurationMS: input.DurationMS}); err != nil {
		return err
	}
	var pagesJSON, documentsJSON, issuesJSON []byte
	if err = tx.QueryRowContext(ctx, `SELECT pages,documents,extra_issues FROM paper_import_formula_input WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND run_id=$3::uuid FOR UPDATE`, tenantID, importID, runID).Scan(&pagesJSON, &documentsJSON, &issuesJSON); err != nil {
		return err
	}
	var parseInput PaperImportParseRequest
	if json.Unmarshal(pagesJSON, &parseInput.Pages) != nil || json.Unmarshal(documentsJSON, &parseInput.Documents) != nil || json.Unmarshal(issuesJSON, &parseInput.ExtraIssues) != nil {
		return ErrInvalidInput
	}
	if !validPaperFormulaResult(parseInput.Documents, input.Regions) {
		return ErrInvalidInput
	}
	parseInput = mergePaperFormulaResult(parseInput, input.Regions)
	resultJSON, _ := json.Marshal(input)
	reviewRequired := false
	for _, region := range input.Regions {
		if region.Status != "accepted" {
			reviewRequired = true
			break
		}
	}
	formulaStatus := "succeeded"
	if reviewRequired {
		formulaStatus = "review_required"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_formula_input SET result=$4::jsonb,status=$5,completed_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND run_id=$3::uuid`, tenantID, importID, runID, resultJSON, formulaStatus); err != nil {
		return err
	}
	var actorID string
	if err = tx.QueryRowContext(ctx, `SELECT created_by::text FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND status='processing' AND deleted_at IS NULL FOR UPDATE`, tenantID, importID).Scan(&actorID); err != nil {
		return err
	}
	job, err := getPaperImportInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if err = queuePaperImportParseInTx(ctx, tx, tenantID, job, actorID, parseInput); err != nil {
		return err
	}
	return tx.Commit()
}

func mergePaperFormulaResult(input PaperImportParseRequest, regions []PaperImportFormulaRegion) PaperImportParseRequest {
	for documentIndex := range input.Documents {
		document := &input.Documents[documentIndex]
		input.ExtraIssues = append(input.ExtraIssues, mergePaperFormulaDocument(document, regions)...)
		sortPaperImportOCRBlocks(document.Blocks)
		parts := make([]string, 0, len(document.Blocks))
		for _, block := range document.Blocks {
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		document.Content = strings.Join(parts, "\n")
	}
	return input
}

func mergePaperFormulaDocument(document *PaperImportParseDocument, regions []PaperImportFormulaRegion) []PaperImportIssue {
	var issues []PaperImportIssue
	for _, region := range regions {
		if region.SourceID != document.SourceID || region.DocumentIndex != document.DocumentIndex {
			continue
		}
		if region.Status != "accepted" || strings.TrimSpace(region.SelectedLatex) == "" {
			issues = append(issues, PaperImportIssue{Code: "FORMULA_REVIEW_REQUIRED", Severity: "warning", Certainty: "confirmed", Message: fmt.Sprintf("第 %d 页有数学公式需要人工核对", region.PageNo), SourceRefs: []PaperImportSourceRef{{SourceID: document.SourceID, FileAssetID: document.FileAssetID, DocumentIndex: document.DocumentIndex, PageNo: region.PageNo, BlockID: region.RegionID, BBox: region.BBox}}, ResolutionHint: "请对照原图核对公式"})
			continue
		}
		blocks, review := mergeAcceptedPaperFormulaRegion(document.Blocks, region)
		if review {
			issues = append(issues, formulaMergeReviewIssue(*document, region))
		}
		document.Blocks = blocks
	}
	return issues
}

type paperFormulaBlockMerge struct {
	kept           []PaperImportOCRBlock
	insertAt       int
	mergedInline   bool
	reviewRequired bool
}

func mergeAcceptedPaperFormulaRegion(blocks []PaperImportOCRBlock, region PaperImportFormulaRegion) ([]PaperImportOCRBlock, bool) {
	formulaBlock := PaperImportOCRBlock{SourceID: region.SourceID, DocumentIndex: region.DocumentIndex, BlockID: region.RegionID, PageNo: region.PageNo, Text: "\\(" + region.SelectedLatex + "\\)", BBox: region.BBox, Confidence: selectedFormulaConfidence(region), Kind: "formula", CanonicalLatex: region.SelectedLatex, Engine: "paddle-formula", EngineVersion: region.SelectedModel, ReviewStatus: "accepted", Segments: []PaperImportContentSegment{{Kind: "formula", Latex: region.SelectedLatex, BBox: region.BBox, SourceBlockID: region.RegionID}}}
	merge := paperFormulaBlockMerge{kept: make([]PaperImportOCRBlock, 0, len(blocks)+1), insertAt: -1}
	for _, block := range blocks {
		merge.appendBlock(block, region)
	}
	if merge.mergedInline {
		return merge.kept, merge.reviewRequired
	}
	if merge.insertAt < 0 {
		merge.insertAt = paperFormulaGeometryInsertAt(merge.kept, region)
	}
	insertAt := max(0, min(merge.insertAt, len(merge.kept)))
	merge.kept = append(merge.kept, PaperImportOCRBlock{})
	copy(merge.kept[insertAt+1:], merge.kept[insertAt:])
	merge.kept[insertAt] = formulaBlock
	return merge.kept, merge.reviewRequired
}

// 一个区域最多内联到一块正文；其余重叠块仍按原顺序保留或删除，插入位置也按该顺序确定。
func (m *paperFormulaBlockMerge) appendBlock(block PaperImportOCRBlock, region PaperImportFormulaRegion) {
	if block.PageNo != region.PageNo || !paperBBoxOverlap(block.BBox, region.BBox) {
		m.kept = append(m.kept, block)
		return
	}
	if paperFormulaReplacementSafe(block, region) {
		if m.insertAt < 0 {
			m.insertAt = len(m.kept)
		}
		return
	}
	if !m.mergedInline {
		if mixed, ok, review := mergeOverlappingPaperFormulaBlock(block, region); ok {
			m.kept = append(m.kept, mixed)
			m.mergedInline, m.reviewRequired = true, review
			return
		}
	}
	if m.insertAt < 0 {
		m.insertAt = len(m.kept) + 1
	}
	m.kept = append(m.kept, block)
}

func mergeOverlappingPaperFormulaBlock(block PaperImportOCRBlock, region PaperImportFormulaRegion) (PaperImportOCRBlock, bool, bool) {
	if mixed, ok := mergeInlineFormulaSegment(block, region); ok {
		return mixed, true, mixed.ReviewStatus == "review_required"
	}
	if mixed, ok := mergeInlineFormulaByGeometry(block, region); ok {
		return mixed, true, true
	}
	return block, false, false
}

func validPaperFormulaResult(documents []PaperImportParseDocument, regions []PaperImportFormulaRegion) bool {
	documentKeys := map[string]bool{}
	for _, document := range documents {
		documentKeys[fmt.Sprintf("%s:%d", document.SourceID, document.DocumentIndex)] = true
	}
	seen := map[string]bool{}
	for _, region := range regions {
		key := fmt.Sprintf("%s:%d:%d:%s", region.SourceID, region.DocumentIndex, region.PageNo, region.RegionID)
		if seen[key] || !documentKeys[fmt.Sprintf("%s:%d", region.SourceID, region.DocumentIndex)] || !validPaperFormulaRegion(region) {
			return false
		}
		seen[key] = true
	}
	return true
}

func validPaperFormulaRegion(region PaperImportFormulaRegion) bool {
	validValidationVersion := region.ValidationVersion == "latex-structure-render-v1" || region.ValidationVersion == "latex-structure-render-v2"
	if region.PageNo <= 0 || region.RegionID == "" || len(region.BBox) != 4 || region.DetectorModel != "PP-DocLayout_plus-L" || !finiteUnit(region.DetectorConfidence) || !finiteUnit(region.EdgeInkRatio) || len(region.CropSHA256) != 64 || (region.Status != "accepted" && region.Status != "review_required") || len(region.Candidates) > 2 || region.RecropCount < 0 || region.RecropCount > 8 || !validValidationVersion {
		return false
	}
	if region.Status == "accepted" && (!region.CropComplete || len(region.Candidates) == 0) {
		return false
	}
	if !validPaperFormulaBBox(region.BBox) {
		return false
	}
	selectedMatches, valid := validPaperFormulaCandidates(region)
	if !valid {
		return false
	}
	// 待核对区域允许没有模型候选；已接受区域必须选中一条校验通过的同模型、同 LaTeX 证据。
	return region.Status != "accepted" || (selectedMatches && strings.TrimSpace(region.SelectedLatex) != "")
}

func validPaperFormulaBBox(box []float64) bool {
	for index, value := range box {
		if !math.IsNaN(value) && !math.IsInf(value, 0) && ((index < 2 && value >= 0) || (index >= 2 && value > 0)) {
			continue
		}
		return false
	}
	return true
}

func validPaperFormulaCandidates(region PaperImportFormulaRegion) (bool, bool) {
	models := map[string]bool{}
	selectedMatches := false
	for _, candidate := range region.Candidates {
		if models[candidate.ModelVersion] || !validPaperFormulaCandidate(candidate) {
			return false, false
		}
		models[candidate.ModelVersion] = true
		if candidate.ModelVersion == region.SelectedModel && candidate.CanonicalLatex == region.SelectedLatex && candidate.Valid {
			selectedMatches = true
		}
	}
	return selectedMatches, true
}

func validPaperFormulaCandidate(candidate PaperImportFormulaCandidate) bool {
	if (candidate.ModelVersion != "PP-FormulaNet_plus-M" && candidate.ModelVersion != "PP-FormulaNet_plus-L") || !finiteUnit(candidate.Confidence) || (candidate.RenderSimilarity != nil && !finiteUnit(*candidate.RenderSimilarity)) {
		return false
	}
	if candidate.ValidationAction != "accept" && candidate.ValidationAction != "retry_l" && candidate.ValidationAction != "review" && candidate.ValidationAction != "recrop" {
		return false
	}
	return candidate.Valid == (candidate.SyntaxValid && candidate.StructureValid && candidate.ValidationAction == "accept")
}

func finiteUnit(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func selectedFormulaConfidence(region PaperImportFormulaRegion) float64 {
	for _, candidate := range region.Candidates {
		if candidate.ModelVersion == region.SelectedModel {
			// 当前 Paddle FormulaRecognition 构建不一定提供识别分数。
			// 将此时的零视为不可用，而非确定失败，但也绝不能仅用检测器分数替代：
			// 这种情况下，重新渲染后的相似度是唯一的识别级证据。
			if candidate.Confidence > 0 {
				return min(region.DetectorConfidence, candidate.Confidence)
			}
			if candidate.RenderSimilarity != nil {
				return min(region.DetectorConfidence, *candidate.RenderSimilarity)
			}
			return min(region.DetectorConfidence, 0.5)
		}
	}
	return 0
}

func paperBBoxOverlap(value any, region []float64) bool {
	box, ok := paperBBox(value)
	if !ok || len(region) != 4 {
		return false
	}
	ax2, ay2 := box[0]+box[2], box[1]+box[3]
	bx2, by2 := region[0]+region[2], region[1]+region[3]
	w, h := min(ax2, bx2)-max(box[0], region[0]), min(ay2, by2)-max(box[1], region[1])
	intersection := w * h
	return w > 0 && h > 0 && intersection >= min(box[2]*box[3], region[2]*region[3])*0.5
}

func paperBBox(value any) ([]float64, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var box []float64
	if json.Unmarshal(raw, &box) != nil || len(box) != 4 || box[2] <= 0 || box[3] <= 0 {
		return nil, false
	}
	return box, true
}

// 覆盖比例只是几何筛选；还要结合文本，避免把与公式重叠的中文正文整块替换。
func paperFormulaReplacementSafe(block PaperImportOCRBlock, region PaperImportFormulaRegion) bool {
	box, ok := paperBBox(block.BBox)
	if !ok {
		return false
	}
	ax2, ay2 := box[0]+box[2], box[1]+box[3]
	bx2, by2 := region.BBox[0]+region.BBox[2], region.BBox[1]+region.BBox[3]
	w, h := min(ax2, bx2)-max(box[0], region.BBox[0]), min(ay2, by2)-max(box[1], region.BBox[1])
	coverage := max(0, w) * max(0, h) / (box[2] * box[3])
	if coverage < 0.82 {
		return false
	}
	plainBlock := normalizeFormulaSearchText(block.Text)
	plainFormula := formulaSearchText(region.SelectedLatex)
	if len([]rune(plainFormula)) >= 2 && plainBlock == plainFormula {
		return true
	}
	for _, char := range block.Text {
		if unicode.In(char, unicode.Han) {
			return false
		}
	}
	formulaLike := 0
	total := 0
	for _, char := range plainBlock {
		if unicode.IsSpace(char) {
			continue
		}
		total++
		if unicode.IsDigit(char) || unicode.IsLetter(char) || strings.ContainsRune("+-=*/()[]{}<>.,:|", char) {
			formulaLike++
		}
	}
	return coverage >= 0.94 && total > 0 && float64(formulaLike)/float64(total) >= 0.9
}

func mergeInlineFormulaSegment(block PaperImportOCRBlock, region PaperImportFormulaRegion) (PaperImportOCRBlock, bool) {
	if block.Kind == "formula" {
		return block, false
	}
	plainFormula := formulaSearchText(region.SelectedLatex)
	if len([]rune(plainFormula)) < 3 {
		return block, false
	}
	searchable, starts, ends := normalizeFormulaSearchWithOffsets(block.Text)
	start := strings.Index(searchable, plainFormula)
	if start < 0 {
		sourceStart, sourceEnd, ok := approximateFormulaRange(block.Text, plainFormula)
		if !ok {
			return block, false
		}
		mixed, ok := replaceInlineFormulaRange(block, region, sourceStart, sourceEnd)
		if ok {
			mixed.ReviewStatus = "review_required"
			mixed.Confidence = min(mixed.Confidence, 0.6)
		}
		return mixed, ok
	}
	end := start + len(plainFormula)
	if start >= len(starts) || end <= 0 || end > len(ends) {
		return block, false
	}
	sourceStart, sourceEnd := starts[start], ends[end-1]
	return replaceInlineFormulaRange(block, region, sourceStart, sourceEnd)
}

func replaceInlineFormulaRange(block PaperImportOCRBlock, region PaperImportFormulaRegion, sourceStart, sourceEnd int) (PaperImportOCRBlock, bool) {
	if sourceStart < 0 || sourceEnd <= sourceStart || sourceEnd > len(block.Text) {
		return block, false
	}
	// 绝不能替换已插入公式内部的子串。第二个 ROI 不能创建嵌套数学分隔符或改动之前的证据。
	if strings.Count(block.Text[:sourceStart], "\\(") > strings.Count(block.Text[:sourceStart], "\\)") || strings.Contains(block.Text[sourceStart:sourceEnd], "\\(") || strings.Contains(block.Text[sourceStart:sourceEnd], "\\)") {
		return block, false
	}
	if block.RawText == "" {
		block.RawText = block.Text
	}
	previousRequiresReview := block.ReviewStatus == "review_required"
	block.Text = block.Text[:sourceStart] + "\\(" + region.SelectedLatex + "\\)" + block.Text[sourceEnd:]
	block.Kind = "mixed"
	block.ReviewStatus = "accepted"
	if previousRequiresReview {
		block.ReviewStatus = "review_required"
	}
	block.Confidence = mergedFormulaConfidence(block.Confidence, selectedFormulaConfidence(region))
	block.Segments = inlineFormulaSegments(block.Text, block.BlockID, block.Segments, region)
	return block, true
}

func mergeInlineFormulaByGeometry(block PaperImportOCRBlock, region PaperImportFormulaRegion) (PaperImportOCRBlock, bool) {
	// Paddle 文本 OCR 返回行级框，而公式检测器返回行内 ROI。
	// OCR 已丢失上标或竖线时，文本匹配无法找到公式，因此使用可信水平 ROI 只替换该行的对应部分。
	// 已混合的块不再重复这种近似处理，因为插入的 LaTeX 已不具备原始行的字符几何关系。
	if block.Kind == "mixed" || block.Kind == "formula" || strings.TrimSpace(block.Text) == "" {
		return block, false
	}
	box, ok := paperBBox(block.BBox)
	if !ok || len(region.BBox) != 4 {
		return block, false
	}
	tx, ty, tw, th := box[0], box[1], box[2], box[3]
	fx, fy, fw, fh := region.BBox[0], region.BBox[1], region.BBox[2], region.BBox[3]
	verticalOverlap := min(ty+th, fy+fh) - max(ty, fy)
	if verticalOverlap <= 0 || verticalOverlap/min(th, fh) < 0.55 {
		return block, false
	}
	overlapLeft, overlapRight := max(tx, fx), min(tx+tw, fx+fw)
	if overlapRight <= overlapLeft {
		return block, false
	}
	runes := []rune(block.Text)
	coverage := (overlapRight - overlapLeft) * verticalOverlap / (tw * th)
	if coverage < 0.70 && len(runes) > 12 {
		return block, false
	}
	start := max(0, min(len(runes), int(math.Round((overlapLeft-tx)/tw*float64(len(runes))))))
	end := max(start, min(len(runes), int(math.Round((overlapRight-tx)/tw*float64(len(runes))))))
	if end <= start {
		return block, false
	}
	for _, char := range runes[start:end] {
		if unicode.In(char, unicode.Han) {
			return block, false
		}
	}
	if block.RawText == "" {
		block.RawText = block.Text
	}
	block.Text = string(runes[:start]) + "\\(" + region.SelectedLatex + "\\)" + string(runes[end:])
	block.Kind = "mixed"
	block.ReviewStatus = "review_required"
	block.Confidence = min(mergedFormulaConfidence(block.Confidence, selectedFormulaConfidence(region)), 0.6)
	block.Segments = inlineFormulaSegments(block.Text, block.BlockID, block.Segments, region)
	return block, true
}

func formulaMergeReviewIssue(document PaperImportParseDocument, region PaperImportFormulaRegion) PaperImportIssue {
	return PaperImportIssue{
		Code: "FORMULA_ALIGNMENT_REVIEW_REQUIRED", Severity: "warning", Certainty: "suspected",
		Message:        fmt.Sprintf("第 %d 页有公式与正文的对齐位置需要人工核对", region.PageNo),
		SourceRefs:     []PaperImportSourceRef{{SourceID: document.SourceID, FileAssetID: document.FileAssetID, DocumentIndex: document.DocumentIndex, PageNo: region.PageNo, BlockID: region.RegionID, BBox: region.BBox}},
		ResolutionHint: "公式已作为候选替换 OCR 片段，原文已保留；请核对公式两侧文字及符号是否完整",
	}
}

func mergedFormulaConfidence(textConfidence, formulaConfidence float64) float64 {
	if formulaConfidence <= 0 {
		return 0
	}
	if textConfidence <= 0 {
		return formulaConfidence
	}
	return min(textConfidence, formulaConfidence)
}

func inlineFormulaSegments(text, sourceBlockID string, previous []PaperImportContentSegment, current PaperImportFormulaRegion) []PaperImportContentSegment {
	knownBoxes := map[string][]float64{current.SelectedLatex: current.BBox}
	for _, segment := range previous {
		if segment.Kind == "formula" && segment.Latex != "" && len(segment.BBox) == 4 {
			knownBoxes[segment.Latex] = segment.BBox
		}
	}
	segments := []PaperImportContentSegment{}
	for len(text) > 0 {
		start := strings.Index(text, "\\(")
		if start < 0 {
			if text != "" {
				segments = append(segments, PaperImportContentSegment{Kind: "text", Text: text, SourceBlockID: sourceBlockID})
			}
			break
		}
		if start > 0 {
			segments = append(segments, PaperImportContentSegment{Kind: "text", Text: text[:start], SourceBlockID: sourceBlockID})
		}
		end := strings.Index(text[start+2:], "\\)")
		if end < 0 {
			segments = append(segments, PaperImportContentSegment{Kind: "text", Text: text[start:], SourceBlockID: sourceBlockID})
			break
		}
		latex := text[start+2 : start+2+end]
		segments = append(segments, PaperImportContentSegment{Kind: "formula", Latex: latex, BBox: knownBoxes[latex], SourceBlockID: sourceBlockID})
		text = text[start+2+end+2:]
	}
	return segments
}

var formulaCommandPattern = regexp.MustCompile(`\\[A-Za-z]+`)

// 这里只生成 OCR 片段的查找键，会丢弃部分 LaTeX 结构，不能用来判断数学等价。
func formulaSearchText(value string) string {
	value = strings.NewReplacer(
		`\times`, "*", `\cdot`, "*", `\div`, "/", `\leq`, "<=", `\geq`, ">=", `\neq`, "!=",
		`\left`, "", `\right`, "", `\{`, "", `\}`, "", `\|`, "|", `\.`, ".", "^{", "{", "_{", "{",
	).Replace(value)
	value = formulaCommandPattern.ReplaceAllString(value, "")
	value = strings.NewReplacer("{", "", "}", "", "^", "", "_", "").Replace(value)
	return normalizeFormulaSearchText(value)
}

func approximateFormulaRange(value, formula string) (int, int, bool) {
	searchable, starts, ends := normalizeFormulaSearchRunesWithOffsets(value)
	target := []rune(formula)
	if len(target) < 4 || len(target) > 128 || len(searchable) < 2 || len(searchable) > 1024 {
		return 0, 0, false
	}
	maxEdits := min(6, max(1, int(math.Ceil(float64(len(target))*0.30))))
	minLength := max(2, len(target)-maxEdits)
	maxLength := min(len(searchable), len(target)+maxEdits)
	bestDistance, bestStart, bestEnd := maxEdits+1, -1, -1
	for start := range searchable {
		for length := minLength; length <= maxLength && start+length <= len(searchable); length++ {
			if formulaRangeContainsProse(searchable[start : start+length]) {
				continue
			}
			distance := formulaEditDistance(searchable[start:start+length], target, bestDistance)
			// 距离相同仍保留先找到的范围，避免同一文本出现多个近似片段时改变定位顺序。
			if distance < bestDistance {
				bestDistance, bestStart, bestEnd = distance, start, start+length
			}
		}
	}
	if bestStart < 0 || bestDistance > maxEdits {
		return 0, 0, false
	}
	return starts[bestStart], ends[bestEnd-1], true
}

// 近似匹配不能跨过中文正文或已插入的 LaTeX 分隔符。
func formulaRangeContainsProse(chars []rune) bool {
	for _, char := range chars {
		if unicode.In(char, unicode.Han) || char == '\\' {
			return true
		}
	}
	return false
}

func normalizeFormulaSearchRunesWithOffsets(value string) ([]rune, []int, []int) {
	output := []rune{}
	starts := []int{}
	ends := []int{}
	for sourceStart, char := range value {
		sourceEnd := sourceStart + len(string(char))
		normalized := strings.ToLower(norm.NFKC.String(string(char)))
		normalized = strings.NewReplacer("×", "*", "·", "*", "÷", "/", "−", "-").Replace(normalized)
		for _, normalizedChar := range normalized {
			if unicode.IsSpace(normalizedChar) {
				continue
			}
			output = append(output, normalizedChar)
			starts = append(starts, sourceStart)
			ends = append(ends, sourceEnd)
		}
	}
	return output, starts, ends
}

func formulaEditDistance(left, right []rune, stopAfter int) int {
	previous := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftChar := range left {
		current := make([]int, len(right)+1)
		current[0] = leftIndex + 1
		rowMinimum := current[0]
		for rightIndex, rightChar := range right {
			cost := 0
			if leftChar != rightChar {
				cost = 1
			}
			current[rightIndex+1] = min(
				min(current[rightIndex]+1, previous[rightIndex+1]+1),
				previous[rightIndex]+cost,
			)
			rowMinimum = min(rowMinimum, current[rightIndex+1])
		}
		previous = current
		if rowMinimum > stopAfter && leftIndex >= len(right) {
			return stopAfter + 1
		}
	}
	return previous[len(right)]
}

func normalizeFormulaSearchText(value string) string {
	value = strings.ToLower(norm.NFKC.String(value))
	value = strings.NewReplacer("×", "*", "·", "*", "÷", "/", "−", "-", " ", "", "\t", "", "\n", "").Replace(value)
	return value
}

// strings.Index 返回字节偏移，因此每个归一化后的 UTF-8 字节都映射回原字符的完整边界。
func normalizeFormulaSearchWithOffsets(value string) (string, []int, []int) {
	var builder strings.Builder
	starts := []int{}
	ends := []int{}
	for sourceStart, char := range value {
		sourceEnd := sourceStart + len(string(char))
		normalized := strings.ToLower(norm.NFKC.String(string(char)))
		normalized = strings.NewReplacer("×", "*", "·", "*", "÷", "/", "−", "-").Replace(normalized)
		for _, output := range normalized {
			if unicode.IsSpace(output) {
				continue
			}
			encoded := string(output)
			builder.WriteString(encoded)
			for range len(encoded) {
				starts = append(starts, sourceStart)
				ends = append(ends, sourceEnd)
			}
		}
	}
	return builder.String(), starts, ends
}

func paperFormulaGeometryInsertAt(blocks []PaperImportOCRBlock, region PaperImportFormulaRegion) int {
	for index, block := range blocks {
		if block.PageNo != region.PageNo {
			if block.PageNo > region.PageNo {
				return index
			}
			continue
		}
		box, ok := paperBBox(block.BBox)
		if ok && (box[1] > region.BBox[1] || (box[1] == region.BBox[1] && box[0] > region.BBox[0])) {
			return index
		}
	}
	return len(blocks)
}

func getPaperImportInTx(ctx context.Context, tx *sql.Tx, tenantID, importID string) (PaperImportJob, error) {
	job, err := scanPaperImport(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, importID))
	if err != nil {
		return PaperImportJob{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text,file_asset_id::text,document_index,role_hint,detected_role,role_confidence::float8,processing_status,created_at FROM paper_import_source WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL ORDER BY document_index,id`, tenantID, importID)
	if err != nil {
		return PaperImportJob{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var source PaperImportSource
		if err = rows.Scan(&source.ID, &source.FileAssetID, &source.DocumentIndex, &source.RoleHint, &source.DetectedRole, &source.RoleConfidence, &source.ProcessingStatus, &source.CreatedAt); err != nil {
			return PaperImportJob{}, err
		}
		job.Sources = append(job.Sources, source)
	}
	return job, rows.Err()
}

// 审核保存需要完整回执；解析入队仍使用最小读取，先拒绝错误来源绑定，再进入运行详情查询。
func getPaperImportDetailsInTx(ctx context.Context, tx *sql.Tx, tenantID, importID string) (PaperImportJob, error) {
	job, err := scanPaperImport(tx.QueryRowContext(ctx, `SELECT id::text,tenant_id::text,exam_id::text,COALESCE(exam_paper_id::text,''),COALESCE(paper_file_asset_id::text,''),COALESCE(answer_file_asset_id::text,''),status,subject,draft_questions,issues,error_code,created_by::text,created_at,updated_at,applied_at FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, importID))
	if err != nil {
		return PaperImportJob{}, err
	}
	return loadPaperImportDetails(ctx, tx, tenantID, job)
}

func (s *PostgresStore) LoadPaperImportParseInput(ctx context.Context, tenantID, inputID string) (string, PaperImportParseRequest, error) {
	var importID string
	var documents, pages, issues []byte
	err := s.db.QueryRowContext(ctx, `SELECT paper_import_id::text,documents,pages,extra_issues FROM paper_import_parse_input WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, inputID).Scan(&importID, &documents, &pages, &issues)
	if errors.Is(err, sql.ErrNoRows) {
		return "", PaperImportParseRequest{}, ErrNotFound
	}
	if err != nil {
		return "", PaperImportParseRequest{}, err
	}
	var input PaperImportParseRequest
	if json.Unmarshal(documents, &input.Documents) != nil || json.Unmarshal(pages, &input.Pages) != nil || json.Unmarshal(issues, &input.ExtraIssues) != nil || len(input.Documents) == 0 {
		return "", PaperImportParseRequest{}, ErrInvalidInput
	}
	return importID, input, nil
}

func (s *PostgresStore) LoadPaperImportParseRunInput(ctx context.Context, tenantID, inputID string) (PaperImportRunBinding, error) {
	var binding PaperImportRunBinding
	var documents, pages, issues, previousSnapshot []byte
	err := s.db.QueryRowContext(ctx, `SELECT input.paper_import_id::text,input.run_id::text,input.generation,input.source_revision,input.input_hash,
       input.documents,input.pages,input.extra_issues,run.command_type,previous.source_snapshot
FROM paper_import_parse_input input
JOIN paper_import_run run ON run.tenant_id=input.tenant_id AND run.id=input.run_id AND run.generation=input.generation
LEFT JOIN paper_import_run previous ON previous.tenant_id=input.tenant_id AND previous.paper_import_id=input.paper_import_id AND previous.generation=input.generation-1
WHERE input.tenant_id=$1 AND input.id=$2::uuid AND input.protocol_version=2`, tenantID, inputID).Scan(
		&binding.ImportID, &binding.RunID, &binding.Generation, &binding.SourceRevision, &binding.InputHash, &documents, &pages, &issues, &binding.CommandType, &previousSnapshot,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PaperImportRunBinding{}, ErrNotFound
	}
	if err != nil {
		return PaperImportRunBinding{}, err
	}
	binding.InputID = inputID
	if json.Unmarshal(documents, &binding.Input.Documents) != nil || json.Unmarshal(pages, &binding.Input.Pages) != nil || json.Unmarshal(issues, &binding.Input.ExtraIssues) != nil || len(binding.Input.Documents) == 0 {
		return PaperImportRunBinding{}, ErrInvalidInput
	}
	if binding.CommandType == "add_sources" && len(previousSnapshot) > 0 {
		binding.NewSourceIDs, err = paperImportNewSourceIDs(binding.Input.Documents, previousSnapshot)
		if err != nil {
			return PaperImportRunBinding{}, err
		}
	}
	serialized, err := json.Marshal(binding.Input)
	if err != nil || contentHash(serialized) != binding.InputHash {
		return PaperImportRunBinding{}, ErrConflict
	}
	return binding, nil
}

func paperImportNewSourceIDs(documents []PaperImportParseDocument, snapshot []byte) ([]string, error) {
	var previous []PaperImportSource
	if json.Unmarshal(snapshot, &previous) != nil {
		return nil, ErrInvalidInput
	}
	previousIDs := make(map[string]bool, len(previous))
	for _, source := range previous {
		previousIDs[source.ID] = true
	}
	var added []string
	for _, document := range documents {
		if !previousIDs[document.SourceID] {
			added = append(added, document.SourceID)
		}
	}
	return added, nil
}

func (s *PostgresStore) FailPaperImportRuntime(ctx context.Context, tenantID, importID string, input PaperImportRuntimeFailure) error {
	if input.TaskID == "" || input.LeaseToken == "" || input.ErrorCode == "" {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	runID, generation, _, err := currentPaperImportRunInTx(ctx, tx, tenantID, importID)
	if err != nil {
		return err
	}
	if err = validatePaperImportTaskBinding(ctx, tx, tenantID, input.TaskID, runID, generation); err != nil {
		return err
	}
	task, err := workerruntime.FailTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		return err
	}
	// 可重试失败只把任务重新入队，导入仍保持处理中；耗尽重试后才同步终止导入和来源。
	if task.Status == workerruntime.StatusQueued {
		return tx.Commit()
	}
	if task.TaskType == "paper_formula" {
		detail, _ := json.Marshal(map[string]any{"error_code": input.ErrorCode, "error_detail": input.ErrorDetail})
		if _, err = tx.ExecContext(ctx, `UPDATE paper_import_formula_input SET status='failed',result=$4::jsonb,completed_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND run_id=$3::uuid`, tenantID, importID, runID, detail); err != nil {
			return err
		}
	}
	jobErrorCode := "paper_ocr_failed"
	issuePrefix := "扫描文档文字识别失败："
	if input.ErrorCode == "paper_parse_failed" {
		jobErrorCode = "ai_parse_failed"
		issuePrefix = "AI 解析服务暂不可用："
	}
	issues, _ := json.Marshal([]string{issuePrefix + paperImportRuntimeErrorMessage(input.ErrorCode)})
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',error_code=$3,issues=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND status='processing' AND deleted_at IS NULL`, tenantID, importID, jobErrorCode, issues); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='failed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return err
	}
	detail, _ := json.Marshal(input.ErrorDetail)
	if _, err = tx.ExecContext(ctx, `UPDATE paper_import_run SET status='failed',error_code=$4,error_detail=$5,completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND generation=$3 AND status='processing'`, tenantID, runID, generation, input.ErrorCode, detail); err != nil {
		return err
	}
	return tx.Commit()
}

func paperImportRuntimeErrorMessage(code string) string {
	switch strings.TrimSpace(code) {
	case "page_processing_failed":
		return "页面处理失败"
	case "source_ocr_failed":
		return "图片文字识别失败"
	case "paper_ocr_unavailable":
		return "文字识别服务暂不可用"
	case "paper_parse_failed":
		return "结构化解析失败，请稍后重试"
	case "paper_formula_failed":
		return "数学公式识别失败，请稍后重试"
	case "source_download_forbidden":
		return "源文件访问被拒绝，请重新提交资料或联系管理员"
	case "source_not_found":
		return "源文件不存在或已删除"
	case "source_download_failed":
		return "源文件读取失败，请稍后重试"
	default:
		return "处理失败，请检查资料后重试"
	}
}

func mapFromJSON(value any) map[string]any {
	raw, _ := json.Marshal(value)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
