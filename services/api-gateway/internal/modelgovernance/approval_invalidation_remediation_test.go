package modelgovernance

import (
	"context"
	"testing"
	"time"
)

// 仅用已有内存夹具模拟完成证据→审批→证据失效，不调用HTTP、真实账号、模型或数据库。
// 作废证据必须使依赖批准失效，同时保留批准作为历史。
func TestInvalidatedEvaluationApprovalCannotMatch(t *testing.T) {
	store, run, candidate, input := routingApprovalFixture()
	ctx := context.Background()
	approval, err := store.CreateModelApproval(ctx, run.TenantID, "synthetic-reviewer", input)
	if err != nil {
		t.Fatal(err)
	}
	invalidated, err := store.InvalidateEvaluationRun(ctx, run.TenantID, "synthetic-reviewer", run.ID, "synthetic evidence authorization withdrawn")
	if err != nil || invalidated.Status != EvaluationStatusInvalidated {
		t.Fatalf("invalidate synthetic evidence: status=%s err=%v", invalidated.Status, err)
	}
	items, err := store.ListModelApprovals(ctx, run.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	scope := ModelApprovalScope{
		ModelConfigID: candidate.ModelConfigID, ModelVersion: candidate.ModelVersion,
		PromptVersion: candidate.PromptVersion, RubricVersion: candidate.RubricVersion,
		Subject: run.Subject, Grade: run.Grade, QuestionType: run.QuestionType, Modality: run.Modality,
	}
	_, err = RequireActiveModelApproval(items, scope, time.Now().UTC())
	if err == nil {
		t.Fatal("invalidated evaluation approval still authorizes runtime")
	}
	if len(items) != 1 || items[0].ID != approval.ID || items[0].RevokedAt == nil || items[0].Revision != approval.Revision+1 {
		t.Fatalf("history or revocation missing: %#v", items)
	}
}
