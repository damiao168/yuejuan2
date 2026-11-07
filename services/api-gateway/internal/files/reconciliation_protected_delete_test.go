package files

import (
	"context"
	"testing"
)

func TestReconciliationCancelsProtectedBusinessDeletion(t *testing.T) {
	for _, lifecycle := range []string{LifecyclePendingDelete, LifecycleDeleteFailed} {
		for _, present := range []bool{true, false} {
			t.Run(lifecycle+"/present="+map[bool]string{true: "true", false: "false"}[present], func(t *testing.T) {
				asset := FileAsset{ID: "asset", TenantID: "tenant", OwnerType: "capture", ExamID: "exam", Lifecycle: lifecycle, Revision: 7}
				status, errorCode := LifecycleActive, ""
				if !present {
					status, errorCode = LifecycleMissingObject, "object_missing"
				}
				r := newReconciliationBatch21(t, []reconciliationBatch21Step{{
					kind: "exec", fragment: "SET lifecycle_status=$3", affected: 1,
					args: []any{asset.ID, int64(asset.Revision), status, errorCode},
				}})
				called := false
				add := func(code string, _ map[string]any, repaired bool) error {
					called = true
					if !repaired || (present && code != "protected_delete_cancelled") || (!present && code != "object_missing") {
						t.Fatalf("finding code=%q repaired=%v", code, repaired)
					}
					return nil
				}
				var err error
				if present {
					err = r.inspectVerifiedAsset(context.Background(), asset, ReconciliationOptions{Repair: true}, add)
				} else {
					err = r.inspectMissingAsset(context.Background(), asset, ReconciliationOptions{Repair: true}, add)
				}
				if err != nil || !called {
					t.Fatalf("protected asset interrupted reconciliation: called=%v err=%v", called, err)
				}
			})
		}
	}
}
