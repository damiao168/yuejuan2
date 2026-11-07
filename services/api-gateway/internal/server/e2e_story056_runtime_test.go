package server

import (
	"context"
	"crypto/sha256"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func e2eClaimStory056OMRTasks(t *testing.T, router http.Handler, adminToken string, limit int, service, instance string) map[string]story056WorkerLease {
	t.Helper()
	response := e2ePostWorkerJSON(t, router, http.MethodPost, "/api/v1/internal/worker/tasks/claim", adminToken, story056JSON(t, map[string]any{
		"queue_name": "page-processing", "worker_service": service, "worker_instance_id": instance, "limit": limit, "lease_seconds": 300,
	}), http.StatusOK)
	items, ok := response["tasks"].([]any)
	if !ok {
		t.Fatalf("claim tasks response has unexpected shape: %#v", response)
	}
	leases := make(map[string]story056WorkerLease, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("claimed task has unexpected shape: %#v", raw)
		}
		if e2eString(t, item, "task_type") != "omr_extract" || e2eString(t, item, "source_type") != "omr_run" {
			t.Fatalf("acceptance run claimed a non-OMR task: %#v", item)
		}
		lease := story056WorkerLease{TaskID: e2eString(t, item, "id"), OMRRunID: e2eString(t, item, "source_id"), Token: e2eString(t, item, "lease_token")}
		leases[lease.OMRRunID] = lease
	}
	return leases
}

func e2eCreateStory056Overlay(t *testing.T, fileStore files.Store, fixture story056AcceptanceFixture, omrRunID string, sequence int) files.FileAsset {
	t.Helper()
	assetToken := fmt.Sprintf("%s-%03d", omrRunID, sequence)
	asset, err := fileStore.Create(context.Background(), files.CreateAssetInput{
		TenantID: fixture.TenantID, ExamID: fixture.ExamID, OwnerType: "omr_evidence", OwnerID: omrRunID,
		OriginalName: fmt.Sprintf("story056-overlay-%s.png", assetToken), ContentType: "image/png", SizeBytes: 1,
		HashSHA256: e2eStory056AssetHash("overlay", fixture.ExamID, omrRunID, fmt.Sprintf("%d", sequence)), StorageBucket: "edugrade-story056-e2e", StorageKey: fmt.Sprintf("overlays/%s.png", assetToken),
		Visibility: "private", UploadedBy: fixture.AdminID,
	})
	if err != nil {
		t.Fatalf("create OMR overlay for %s: %v", omrRunID, err)
	}
	return asset
}

// e2eStory056AssetHash 根据夹具身份生成有效 SHA-256 值。
// 文件资源按租户去重，因此固定的合成哈希会让本来独立的 PostgreSQL 端到端用例
// 在共享演示租户时意外重复。
func e2eStory056AssetHash(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

func e2eStory056RunItems(t *testing.T, response map[string]any) []map[string]any {
	t.Helper()
	rawItems, ok := response["items"].([]any)
	if !ok {
		t.Fatalf("scoring run detail has unexpected items: %#v", response)
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("scoring run item has unexpected shape: %#v", raw)
		}
		items = append(items, item)
	}
	return items
}

func e2eStory056SelectedOptions(t *testing.T, questionType string) []string {
	t.Helper()
	switch questionType {
	case "single_choice":
		return []string{"A"}
	case "true_false":
		return []string{"true"}
	case "multiple_choice":
		return []string{"A", "C"}
	default:
		t.Fatalf("unexpected question type in OMR acceptance data: %s", questionType)
		return nil
	}
}

// 运行摘要按明确命名的计数核对；自动确认与人工确认不能交换或合并。
type story056RunExpectation struct {
	Status                                                       string
	Total, Queued, AutoConfirmed, HumanConfirmed, Review, Failed int
}

func e2eAssertStory056Run(t *testing.T, run map[string]any, expected story056RunExpectation) {
	t.Helper()
	status, total, queued, autoConfirmed, humanConfirmed, review, failed := expected.Status, expected.Total, expected.Queued, expected.AutoConfirmed, expected.HumanConfirmed, expected.Review, expected.Failed
	if run["status"] != status ||
		int(e2eFloat(t, run, "total_count")) != total ||
		int(e2eFloat(t, run, "queued_count")) != queued ||
		int(e2eFloat(t, run, "auto_confirmed_count")) != autoConfirmed ||
		int(e2eFloat(t, run, "human_confirmed_count")) != humanConfirmed ||
		int(e2eFloat(t, run, "review_count")) != review ||
		int(e2eFloat(t, run, "failed_count")) != failed {
		t.Fatalf("unexpected scoring run summary: %#v", run)
	}
}

func e2eAssertStory056ItemStates(t *testing.T, items []map[string]any, wantConfirmed, wantReview int) {
	t.Helper()
	confirmed := 0
	review := 0
	for _, item := range items {
		switch item["state"] {
		case "confirmed":
			confirmed++
		case "review":
			review++
			if e2eString(t, item, "question_type") != "multiple_choice" {
				t.Fatalf("only multi-select OMR answers should require human review: %#v", item)
			}
		default:
			t.Fatalf("acceptance item must be confirmed or awaiting review: %#v", item)
		}
	}
	if confirmed != wantConfirmed || review != wantReview {
		t.Fatalf("unexpected scoring item states: confirmed=%d review=%d want_confirmed=%d want_review=%d", confirmed, review, wantConfirmed, wantReview)
	}
}
