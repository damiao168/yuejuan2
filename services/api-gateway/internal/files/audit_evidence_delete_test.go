package files

import (
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestGenericDeletionCannotRemoveAnswerEvidence(t *testing.T) {
	for _, owner := range []string{"submission", "submission_page_original", "submission_page_normalized", "capture_batch", "capture_page_decoded", "answer_segment_crop", "exam"} {
		t.Run(owner, func(t *testing.T) {
			store := NewMemoryStore()
			asset, err := store.Create(t.Context(), CreateAssetInput{TenantID: "tenant", OwnerType: owner, OwnerID: "owner", HashSHA256: owner, OriginalName: "synthetic.png", StorageBucket: "test", StorageKey: owner})
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.BeginDelete(t.Context(), auth.AccessScope{TenantID: "tenant", TenantWide: true}, asset.ID, asset.Revision)
			if !errors.Is(err, ErrForbidden) {
				t.Fatal("evidence deletion allowed", err)
			}
			if current, err := store.Get(t.Context(), "tenant", asset.ID); err != nil || current.Lifecycle != LifecycleActive {
				t.Fatal(current, err)
			}
		})
	}
}
