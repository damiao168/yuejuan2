package ocr

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestBatch24CoordinatorRejectsUnrelatedRuntimeBeforeSourceMutation(t *testing.T) {
	for _, operation := range []string{"complete", "fail"} {
		for _, tc := range []runtimeBindingCase{
			{"missing", "", "", "", workerruntime.ErrNotFound},
			{"other_tenant", "tenant-2", "ocr_task", "", workerruntime.ErrNotFound},
			{"other_type", "tenant-1", "other_task", "", workerruntime.ErrInvalidInput},
			{"other_source", "tenant-1", "ocr_task", "other-source", workerruntime.ErrInvalidInput},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				assertRejectedRuntimeBinding(t, operation, tc)
			})
		}
	}
}

type runtimeBindingCase struct {
	name, runtimeTenant, sourceType, sourceID string
	want                                      error
}

// 不同绑定使用独立存储；拒绝入口返回后再核对两侧快照与故障钩子，避免只检查错误码。
func assertRejectedRuntimeBinding(t *testing.T, operation string, tc runtimeBindingCase) {
	t.Helper()
	ctx := context.Background()
	source := NewMemoryStore()
	runtime := workerruntime.NewMemoryStore()
	coordinator := &memoryRuntimeCoordinator{source: source, runtime: runtime}
	task, err := coordinator.CreateTask(ctx, "tenant-1", "submission-1", "actor-1", CreateTaskInput{
		Engine: "paddleocr", EngineVersion: "test", MinConfidence: 0.8, IdempotencyKey: "binding-source",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.StartTask(ctx, "tenant-1", task.ID); err != nil {
		t.Fatal(err)
	}
	runtimeID, runtimeBefore := seedRuntimeBindingDecoy(t, ctx, runtime, task, tc)
	sourceBefore, err := source.GetTask(ctx, "tenant-1", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 先提供不存在、跨租户或别的来源任务；两种终态入口都必须在改来源之前拒绝。
	hookCalls := 0
	coordinator.hook = func(string) error { hookCalls++; return nil }
	var result Task
	if operation == "complete" {
		result, err = coordinator.CompleteTask(ctx, "tenant-1", task.ID, CompleteTaskInput{RuntimeTaskID: runtimeID})
	} else {
		result, err = coordinator.FailTask(ctx, "tenant-1", task.ID, FailTaskInput{RuntimeTaskID: runtimeID, ErrorMessage: "synthetic-error"})
	}
	if !errors.Is(err, tc.want) || !reflect.DeepEqual(result, Task{}) || hookCalls != 0 {
		t.Fatalf("result=%#v error=%v hookCalls=%d", result, err, hookCalls)
	}
	sourceAfter, err := source.GetTask(ctx, "tenant-1", task.ID)
	if err != nil || !reflect.DeepEqual(sourceAfter, sourceBefore) {
		t.Fatalf("source changed after rejected binding: %#v error=%v", sourceAfter, err)
	}
	if tc.runtimeTenant != "" {
		runtimeAfter, err := runtime.Get(ctx, tc.runtimeTenant, runtimeID)
		if err != nil || !reflect.DeepEqual(runtimeAfter, runtimeBefore) {
			t.Fatalf("runtime changed after rejected binding: %#v error=%v", runtimeAfter, err)
		}
	}
}

func seedRuntimeBindingDecoy(t *testing.T, ctx context.Context, runtime *workerruntime.MemoryStore, task Task, tc runtimeBindingCase) (string, workerruntime.Task) {
	t.Helper()
	if tc.runtimeTenant == "" {
		return "missing", workerruntime.Task{}
	}
	input := runtimeCreateInput(task)
	input.IdempotencyKey = "binding-decoy"
	input.SourceType = tc.sourceType
	if tc.sourceID != "" {
		input.SourceID = tc.sourceID
	}
	runtimeBefore, err := runtime.CreateTask(ctx, tc.runtimeTenant, "actor-1", input)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeBefore.ID, runtimeBefore
}
