package paper

// 公式完成、合并与解析排队共用一份事务；证据校验失败必须整体回滚。

import (
	"context"
	"encoding/json"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

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
