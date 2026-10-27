// 既有 OCR 修复仅委托生产 ReconcileCompletedRuntimeTask，不能重新实现状态恢复规则。
package main

import (
	"context"
	"database/sql"
	"fmt"

	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
)

type ocrRuntimeRepairResult struct {
	DatasetID  string `json:"dataset_id"`
	ExamID     string `json:"exam_id"`
	Found      int    `json:"found"`
	Reconciled int    `json:"reconciled"`
}

func reconcileExistingOCRRuntime(
	ctx context.Context,
	db *sql.DB,
	tenantID string,
	adminID string,
	examID string,
) (ocrRuntimeRepairResult, error) {
	result := ocrRuntimeRepairResult{DatasetID: datasetID, ExamID: examID}
	rows, err := db.QueryContext(ctx, `
SELECT task.id::text
FROM ocr_task task
JOIN submission sub
  ON sub.tenant_id = task.tenant_id AND sub.id = task.submission_id
WHERE task.tenant_id = $1::uuid AND sub.exam_id = $2::uuid
  AND task.status = 'completed'
  AND task.deleted_at IS NULL AND sub.deleted_at IS NULL
ORDER BY task.created_at, task.id
`, tenantID, examID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	taskIDs := []string{}
	for rows.Next() {
		var taskID string
		if err = rows.Scan(&taskID); err != nil {
			return result, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.Found = len(taskIDs)
	store := ocrpkg.NewPostgresStore(db)
	for _, taskID := range taskIDs {
		if _, err = store.ReconcileCompletedRuntimeTask(ctx, tenantID, adminID, taskID); err != nil {
			return result, fmt.Errorf("reconcile OCR task %s: %w", taskID, err)
		}
		result.Reconciled++
	}
	return result, nil
}
