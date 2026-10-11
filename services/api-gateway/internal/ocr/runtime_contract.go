package ocr

import "edugrade-enterprise/services/api-gateway/internal/workerruntime"

const RuntimeResultSchema = "ocr-result.v1"

// RuntimeResult 是映射到 Worker Runtime 的标准来源结果。
// 保持结构精简且由来源事实生成，使重试比较保持确定性。
func RuntimeResult(task Task) map[string]any {
	return map[string]any{
		"ocr_task_id":   task.ID,
		"result_count":  task.ResultCount,
		"model_version": task.ModelVersion,
		"config_hash":   task.ConfigHash,
		"input_hash":    task.InputHash,
	}
}

func runtimeCreateInput(task Task, sourceFileAssetIDs ...string) workerruntime.CreateTaskInput {
	pages := make([]any, 0, len(sourceFileAssetIDs))
	for _, fileAssetID := range sourceFileAssetIDs {
		pages = append(pages, map[string]any{"source_file_asset_id": fileAssetID})
	}
	return workerruntime.CreateTaskInput{
		TaskType: "ocr", QueueName: "ocr", SourceType: "ocr_task", SourceID: task.ID,
		IdempotencyKey: "ocr-task:" + task.ID, PayloadSchemaVersion: "ocr-task.v1",
		Payload: map[string]any{
			"ocr_task_id": task.ID, "submission_id": task.SubmissionID,
			"engine": task.Engine, "engine_version": task.EngineVersion,
			"pages": pages,
		},
		MaxAttempts: 3, RetryBackoffSeconds: 30,
	}
}

func runtimeCompleteInput(task Task, leaseToken string) workerruntime.CompleteInput {
	return workerruntime.CompleteInput{
		LeaseToken: leaseToken, ResultSchemaVersion: RuntimeResultSchema,
		DurationMS: task.DurationMS, Result: RuntimeResult(task),
	}
}
