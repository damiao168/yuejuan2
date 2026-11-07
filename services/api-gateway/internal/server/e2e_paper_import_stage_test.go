package server

import (
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"errors"
	"testing"
)

// 空页数组及已解码页均核对数据库 JSON、内容顺序和存储重新读取，不能只检查序列化输入。
func e2eAssertPersistedPaperPages(t *testing.T, scenario paperApplyGateScenario, where string, expected []paper.PaperImportDecodedPage) string {
	t.Helper()
	ctx, db, store, tenantID, job := scenario.Context, scenario.DB, scenario.Store, scenario.TenantID, scenario.Job
	var inputID, pagesType string
	var pagesJSON []byte
	if err := db.QueryRowContext(ctx, `SELECT id::text,pages,jsonb_typeof(pages) FROM paper_import_parse_input WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid AND `+where+` ORDER BY created_at DESC LIMIT 1`, tenantID, job.ID).Scan(&inputID, &pagesJSON, &pagesType); err != nil {
		t.Fatalf("load persisted parse pages: %v", err)
	}
	if pagesType != "array" {
		t.Fatalf("persisted parse pages type = %q, want array", pagesType)
	}
	var persisted []paper.PaperImportDecodedPage
	if err := json.Unmarshal(pagesJSON, &persisted); err != nil {
		t.Fatalf("decode persisted parse pages: %v", err)
	}
	if len(persisted) != len(expected) {
		t.Fatalf("persisted parse pages = %#v, want %#v", persisted, expected)
	}
	for index := range expected {
		if persisted[index] != expected[index] {
			t.Fatalf("persisted parse page %d = %#v, want %#v", index, persisted[index], expected[index])
		}
	}
	loaded, err := store.LoadPaperImportParseRunInput(ctx, tenantID, inputID)
	if err != nil {
		t.Fatalf("reload persisted parse pages: %v", err)
	}
	if len(loaded.Input.Pages) != len(expected) {
		t.Fatalf("reloaded parse pages = %#v, want %#v", loaded.Input.Pages, expected)
	}
	for index := range expected {
		if loaded.Input.Pages[index] != expected[index] {
			t.Fatalf("reloaded parse page %d = %#v, want %#v", index, loaded.Input.Pages[index], expected[index])
		}
	}
	return inputID

}

// 解析输入和已接受来源快照不可变，重新读取仍保持原哈希及文档集合。
func e2eAssertPaperStageSnapshotImmutable(t *testing.T, scenario paperApplyGateScenario) {
	t.Helper()
	ctx, db, store, tenantID, job, binding := scenario.Context, scenario.DB, scenario.Store, scenario.TenantID, scenario.Job, scenario.Binding
	if _, err := db.ExecContext(ctx, `UPDATE paper_import_parse_input SET documents='[]'::jsonb WHERE id=$1::uuid`, binding.InputID); err == nil {
		t.Fatal("persisted stage input was mutable")
	}
	if _, err := db.ExecContext(ctx, `UPDATE paper_import_run SET source_snapshot='[]'::jsonb WHERE id=$1::uuid`, job.RunID); err == nil {
		t.Fatal("accepted material snapshot was mutable")
	}
	loaded, err := store.LoadPaperImportParseRunInput(ctx, tenantID, binding.InputID)
	if err != nil || loaded.InputHash != binding.InputHash || len(loaded.Input.Documents) != len(binding.Input.Documents) {
		t.Fatalf("immutable input changed: %#v %v", loaded, err)
	}

}

// 缺少活动尝试时完成操作必须整体回滚，再恢复原尝试供后续边界测试。
func e2eAssertPaperMissingAttemptRollback(t *testing.T, scenario paperApplyGateScenario) {
	t.Helper()
	ctx, db, store, tenantID, job, claimedParse, binding := scenario.Context, scenario.DB, scenario.Store, scenario.TenantID, scenario.Job, scenario.ClaimedParse, scenario.Binding
	if _, err := db.ExecContext(ctx, `UPDATE agent_worker_task_attempt SET status='failed',completed_at=now() WHERE task_id=$1::uuid AND attempt_no=1`, claimedParse[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompletePaperImportParseTask(ctx, tenantID, claimedParse[0].ID, claimedParse[0].LeaseToken, binding, paper.PaperImportParseResult{}, 1); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("missing active attempt accepted: %v", err)
	}
	var taskStatus, jobStatus string
	if err := db.QueryRowContext(ctx, `SELECT t.status,j.status FROM agent_worker_task t JOIN paper_import_job j ON j.id=$2::uuid WHERE t.id=$1::uuid`, claimedParse[0].ID, job.ID).Scan(&taskStatus, &jobStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "leased" || jobStatus != "processing" {
		t.Fatalf("partial commit: %s %s", taskStatus, jobStatus)
	}
	if _, err := db.ExecContext(ctx, `UPDATE agent_worker_task_attempt SET status='leased',completed_at=NULL WHERE task_id=$1::uuid AND attempt_no=1`, claimedParse[0].ID); err != nil {
		t.Fatal(err)
	}

}

// 逐个错误阶段和输入子测试保留原恢复步骤，拒绝结果不能改任务、作业或候选。
func e2eAssertPaperStageAndInputFence(t *testing.T, scenario paperApplyGateScenario) {
	t.Helper()
	for _, mutation := range []paperBindingMutation{
		{"wrong stage", `UPDATE agent_worker_task SET task_type='ocr' WHERE id=$1::uuid`},
		{"wrong input", `UPDATE agent_worker_task SET source_id=gen_random_uuid() WHERE id=$1::uuid`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			e2eAssertPaperBindingMutation(t, scenario, mutation)
		})
	}

}

type paperBindingMutation struct{ name, update string }

// 每条错误绑定先修改原任务、确认冲突且事实未变，再恢复原输入和阶段供下一场景使用。
func e2eAssertPaperBindingMutation(t *testing.T, scenario paperApplyGateScenario, mutation paperBindingMutation) {
	t.Helper()
	ctx, db, store, tenantID, job, claimedParse, binding := scenario.Context, scenario.DB, scenario.Store, scenario.TenantID, scenario.Job, scenario.ClaimedParse, scenario.Binding
	if _, err := db.ExecContext(ctx, mutation.update, claimedParse[0].ID); err != nil {
		t.Fatal(err)
	}
	_, err := store.CompletePaperImportParseTask(ctx, tenantID, claimedParse[0].ID, claimedParse[0].LeaseToken, binding, paper.PaperImportParseResult{}, 1)
	if !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("mismatched stage/input published: %v", err)
	}
	var taskStatus, jobStatus string
	var candidates int
	if err := db.QueryRowContext(ctx, `SELECT t.status,j.status,jsonb_array_length(j.question_candidates) FROM agent_worker_task t JOIN paper_import_job j ON j.id=$2::uuid AND j.tenant_id=t.tenant_id WHERE t.id=$1::uuid`, claimedParse[0].ID, job.ID).Scan(&taskStatus, &jobStatus, &candidates); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "leased" || jobStatus != "processing" || candidates != 0 {
		t.Fatalf("rejected result changed facts: %s %s %d", taskStatus, jobStatus, candidates)
	}
	if _, err := db.ExecContext(ctx, `UPDATE agent_worker_task SET task_type='paper_parse',source_id=$2::uuid WHERE id=$1::uuid`, claimedParse[0].ID, binding.InputID); err != nil {
		t.Fatal(err)
	}

}
